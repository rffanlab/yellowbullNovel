package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rffanlab/yellowbullNovel/backend/internal/config"
	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
	"github.com/rffanlab/yellowbullNovel/backend/internal/pipeline"
	"github.com/rffanlab/yellowbullNovel/backend/internal/state"
)

// Server HTTP API 服务器
type Server struct {
	router    *gin.Engine
	pipeline  *pipeline.PipelineRunner
	state     *state.StateManager
	scheduler *pipeline.Scheduler
	config    *config.Config
	logger    *zap.Logger

	// LLM 客户端（可热更新）
	llmClient llm.LLMClient
}

// NewServer 创建 HTTP API 服务器
func NewServer(cfg *config.Config) (*Server, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}

	// 初始化状态管理器
	sm, err := state.NewStateManager(cfg.Database, cfg.Writing.ProjectRoot, logger)
	if err != nil {
		return nil, fmt.Errorf("init state manager: %w", err)
	}

	// 初始化 LLM 客户端
	var llmClient llm.LLMClient
	if cfg.LLM.DefaultProvider != "" {
		providerCfg, ok := cfg.LLM.Providers[cfg.LLM.DefaultProvider]
		if ok {
			llmCfg := llm.ProviderConfig{
				Type:    llm.ProviderType(providerCfg.Type),
				BaseURL: providerCfg.BaseURL,
				APIKey:  providerCfg.APIKey,
				Model:   providerCfg.Model,
			}
			client, err := llm.NewLLMClient(llmCfg)
			if err != nil {
				logger.Warn("LLM 客户端初始化失败，API 可用但写作功能不可用", zap.Error(err))
			} else {
				llmClient = client
			}
		}
	}

	// 初始化流水线
	pipelineConfig := pipeline.PipelineConfig{
		Client:                  llmClient,
		ProjectRoot:             cfg.Writing.ProjectRoot,
		StateManager:            sm,
		ChapterReviewMode:       cfg.Writing.ChapterReviewMode,
		RevisionGate:            cfg.Writing.RevisionGate,
		MaxReviewIterations:     cfg.Writing.MaxReviewIterations,
		FoundationReviewRetries: cfg.Writing.FoundationReviewRetries,
		Logger:                  logger,
	}
	runner := pipeline.NewPipelineRunner(pipelineConfig)

	// 初始化调度器
	schedulerConfig := pipeline.SchedulerConfigFromAppConfig(cfg.Scheduler)
	scheduler := pipeline.NewScheduler(runner, schedulerConfig, logger)

	s := &Server{
		router:    gin.New(),
		pipeline:  runner,
		state:     sm,
		scheduler: scheduler,
		config:    cfg,
		logger:    logger,
		llmClient: llmClient,
	}

	s.setupMiddleware()
	s.setupRoutes()

	return s, nil
}

// Run 启动 HTTP 服务
func (s *Server) Run(port int) error {
	addr := fmt.Sprintf(":%d", port)
	s.logger.Info("启动 HTTP API 服务器", zap.String("addr", addr))
	return s.router.Run(addr)
}

// Shutdown 优雅关闭
func (s *Server) Shutdown(ctx context.Context) error {
	s.scheduler.Stop()
	return s.logger.Sync()
}

// setupMiddleware 设置中间件
func (s *Server) setupMiddleware() {
	// RequestID
	s.router.Use(func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-Id")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Set("requestId", requestID)
		c.Header("X-Request-Id", requestID)
		c.Next()
	})

	// CORS
	corsConfig := cors.Config{
		AllowOrigins:     s.config.Server.AllowOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-Id", "Idempotency-Key"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	if len(corsConfig.AllowOrigins) == 0 {
		corsConfig.AllowOrigins = []string{"*"}
	}
	s.router.Use(cors.New(corsConfig))

	// 日志
	s.router.Use(func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start)
		s.logger.Info("HTTP 请求",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("duration", duration),
			zap.String("requestId", c.GetString("requestId")),
			zap.String("clientIp", c.ClientIP()),
		)
	})

	// 错误恢复
	s.router.Use(func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				s.logger.Error("panic recovered",
					zap.Any("error", err),
					zap.String("path", c.Request.URL.Path),
				)
				c.JSON(http.StatusInternalServerError, ApiResponse{
					Code:    2001,
					Message: "内部错误",
				})
				c.Abort()
			}
		}()
		c.Next()
	})
}

// setupRoutes 设置路由
func (s *Server) setupRoutes() {
	api := s.router.Group("/api")
	{
		// 书籍管理
		api.POST("/books", s.handleCreateBook)
		api.GET("/books", s.handleListBooks)
		api.GET("/books/:id", s.handleGetBook)
		api.DELETE("/books/:id", s.handleDeleteBook)

		// 写作接口
		api.POST("/books/:id/write-next", s.handleWriteNext)
		api.POST("/books/:id/write-draft", s.handleWriteDraft)
		api.POST("/books/:id/plan", s.handlePlan)
		api.POST("/books/:id/compose", s.handleCompose)

		// 审查/回炉/润色
		api.POST("/books/:id/chapters/:num/revise", s.handleRevise)
		api.POST("/books/:id/chapters/:num/review", s.handleReview)
		api.POST("/books/:id/chapters/:num/polish", s.handlePolish)

		// 章节管理
		api.GET("/books/:id/chapters", s.handleListChapters)
		api.GET("/books/:id/chapters/:num", s.handleGetChapter)
		api.PATCH("/books/:id/chapters/:num", s.handleUpdateChapter)

		// 状态接口
		api.GET("/books/:id/status", s.handleGetStatus)
		api.GET("/books/:id/truth-files", s.handleGetTruthFiles)

		// 基础设定修订
		api.POST("/books/:id/revise-foundation", s.handleReviseFoundation)

		// 卷级合并
		api.POST("/books/:id/consolidate", s.handleConsolidate)

		// 导入
		api.POST("/books/:id/import", s.handleImport)

		// 短篇
		api.POST("/short-fiction/run", s.handleShortFiction)

		// 定时调度
		api.POST("/scheduler/start", s.handleSchedulerStart)
		api.POST("/scheduler/stop", s.handleSchedulerStop)
		api.GET("/scheduler/status", s.handleSchedulerStatus)

		// LLM 配置
		api.GET("/llm/providers", s.handleGetLLMProviders)
		api.PUT("/llm/config", s.handleUpdateLLMConfig)

		// SSE 流式写作
		api.GET("/books/:id/write-stream", s.handleWriteStream)
	}
}

// ============================================================================
// 统一响应类型
// ============================================================================

// ApiResponse 统一响应包络
type ApiResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// successResponse 成功响应
func successResponse(data interface{}) ApiResponse {
	return ApiResponse{Code: 0, Message: "ok", Data: data}
}

// errorResponse 错误响应
func errorResponse(code int, msg string) ApiResponse {
	return ApiResponse{Code: code, Message: msg}
}

// ============================================================================
// SSE 事件类型
// ============================================================================

// SSEEventType SSE 事件类型
type SSEEventType string

const (
	SSEEventStage      SSEEventType = "stage"
	SSEEventProgress   SSEEventType = "progress"
	SSEEventChunk      SSEEventType = "chunk"
	SSEEventTokenUsage SSEEventType = "tokenUsage"
	SSEEventIssue      SSEEventType = "issue"
	SSEEventNote       SSEEventType = "note"
	SSEEventError      SSEEventType = "error"
	SSEEventDone       SSEEventType = "done"
)

// SSEEvent SSE 事件
type SSEEvent struct {
	Type      SSEEventType `json:"type"`
	Stage     string       `json:"stage,omitempty"`
	Data      interface{}  `json:"data,omitempty"`
	Timestamp time.Time    `json:"ts"`
}

// writeSSEEvent 写入 SSE 事件
func writeSSEEvent(w io.Writer, event SSEEvent) {
	event.Timestamp = time.Now()
	ginCtx := &gin.Context{}
	_ = ginCtx
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, toJSON(event))
}

// toJSON 简单 JSON 序列化（避免循环引用 gin.Context）
func toJSON(v interface{}) string {
	// 使用 encoding/json
	return jsonMarshal(v)
}
