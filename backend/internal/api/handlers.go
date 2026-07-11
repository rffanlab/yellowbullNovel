package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rffanlab/yellowbullNovel/backend/internal/config"
	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
	"github.com/rffanlab/yellowbullNovel/backend/internal/state"
)

// ============================================================================
// 请求/响应结构体
// ============================================================================

// CreateBookRequest 创建书籍请求
type CreateBookRequest struct {
	Title            string `json:"title" binding:"required"`
	Genre            string `json:"genre" binding:"required"`
	Platform         string `json:"platform"`
	Language         string `json:"language"`
	TargetChapters   int    `json:"targetChapters"`
	ChapterWordCount int    `json:"chapterWordCount"`
	ExternalContext  string `json:"externalContext"`
	AuthorIntent     string `json:"authorIntent"`
}

// WriteNextRequest 写下一章请求
type WriteNextRequest struct {
	WordCount             int     `json:"wordCount,omitempty"`
	TemperatureOverride   float64 `json:"temperatureOverride,omitempty"`
}

// WriteDraftRequest 写草稿请求
type WriteDraftRequest struct {
	Context   string `json:"context,omitempty"`
	WordCount int    `json:"wordCount,omitempty"`
}

// PlanRequest 规划请求
type PlanRequest struct {
	Context string `json:"context,omitempty"`
}

// ComposeRequest 组装请求
type ComposeRequest struct {
	Context string `json:"context,omitempty"`
}

// ReviseChapterRequest 回炉请求
type ReviseChapterRequest struct {
	Mode string `json:"mode"`
}

// ReviewChapterRequest 审查请求
type ReviewChapterRequest struct {
	Deep bool `json:"deep,omitempty"`
}

// PolishChapterRequest 润色请求
type PolishChapterRequest struct {
	Temperature float64 `json:"temperature,omitempty"`
}

// PatchChapterRequest 更新章节请求
type PatchChapterRequest struct {
	Title  *string `json:"title,omitempty"`
	Status *string `json:"status,omitempty"`
}

// ReviseFoundationRequest 基础设定修订请求
type ReviseFoundationRequest struct {
	Feedback string `json:"feedback" binding:"required"`
}

// ConsolidateRequest 卷级合并请求
type ConsolidateRequest struct {
	VolumeName string `json:"volumeName"`
	StartCh    int    `json:"startCh"`
	EndCh      int    `json:"endCh"`
}

// ImportRequest 导入请求
type ImportRequest struct {
	Source       string `json:"source" binding:"required"`
	Format       string `json:"format" binding:"required"`
	StartChapter int    `json:"startChapter,omitempty"`
	MaxChapters  int    `json:"maxChapters,omitempty"`
}

// ShortFictionRunRequest 短篇生产请求
type ShortFictionRunRequest struct {
	Title       string `json:"title" binding:"required"`
	Genre       string `json:"genre" binding:"required"`
	Prompt      string `json:"prompt" binding:"required"`
	TargetWords int    `json:"targetWords" binding:"required"`
	Platform    string `json:"platform,omitempty"`
}

// SchedulerStartRequest 调度启动请求
type SchedulerStartRequest struct {
	BookID        string `json:"bookId,omitempty"`
	ChaptersPerRun int   `json:"chaptersPerRun,omitempty"`
}

// LLMConfigUpdateRequest LLM 配置更新
type LLMConfigUpdateRequest struct {
	Provider    string  `json:"provider"`
	APIKey      string  `json:"apiKey,omitempty"`
	BaseURL     string  `json:"baseUrl,omitempty"`
	Model       string  `json:"model,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int    `json:"maxTokens,omitempty"`
}

// ============================================================================
// 书籍管理 Handlers
// ============================================================================

// handleCreateBook POST /api/books
func (s *Server) handleCreateBook(c *gin.Context) {
	var req CreateBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "参数错误: "+err.Error()))
		return
	}

	bookID := uuid.NewString()
	book := &models.BookConfig{
		ID:               bookID,
		Title:            req.Title,
		Genre:            req.Genre,
		Platform:         req.Platform,
		Language:         req.Language,
		TargetChapters:   req.TargetChapters,
		ChapterWordCount: req.ChapterWordCount,
		Status:           models.BookStatusDraft,
	}
	if book.Platform == "" {
		book.Platform = "番茄小说"
	}
	if book.Language == "" {
		book.Language = "zh"
	}
	if book.TargetChapters <= 0 {
		book.TargetChapters = 100
	}
	if book.ChapterWordCount <= 0 {
		book.ChapterWordCount = 3000
	}
	book.ChapterReviewMode = models.ReviewModeAuto
	book.RevisionGate = models.GateStrict

	// 同步执行 InitBook
	if err := s.pipeline.InitBook(book, req.ExternalContext, req.AuthorIntent); err != nil {
		s.logger.Error("创建书籍失败", zap.String("bookId", bookID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "创建书籍失败: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, successResponse(gin.H{
		"id":     book.ID,
		"title":  book.Title,
		"status": book.Status,
	}))
}

// handleListBooks GET /api/books
func (s *Server) handleListBooks(c *gin.Context) {
	books, err := s.state.ListBooks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "列出书籍失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(books))
}

// handleGetBook GET /api/books/:id
func (s *Server) handleGetBook(c *gin.Context) {
	bookID := c.Param("id")
	book, err := s.state.LoadBookConfig(bookID)
	if err != nil {
		c.JSON(http.StatusNotFound, errorResponse(1004, "书籍不存在"))
		return
	}
	c.JSON(http.StatusOK, successResponse(book))
}

// handleDeleteBook DELETE /api/books/:id
func (s *Server) handleDeleteBook(c *gin.Context) {
	bookID := c.Param("id")
	if err := s.state.DeleteBook(bookID); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "删除书籍失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(gin.H{"deleted": true}))
}

// ============================================================================
// 写作接口 Handlers
// ============================================================================

// handleWriteNext POST /api/books/:id/write-next
func (s *Server) handleWriteNext(c *gin.Context) {
	bookID := c.Param("id")
	var req WriteNextRequest
	_ = c.ShouldBindJSON(&req)

	result, err := s.pipeline.WriteNextChapter(bookID, req.WordCount, req.TemperatureOverride)
	if err != nil {
		s.logger.Error("写下一章失败", zap.String("bookId", bookID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "写章失败: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, successResponse(result))
}

// handleWriteDraft POST /api/books/:id/write-draft
func (s *Server) handleWriteDraft(c *gin.Context) {
	bookID := c.Param("id")
	var req WriteDraftRequest
	_ = c.ShouldBindJSON(&req)

	result, err := s.pipeline.WriteDraft(bookID, req.Context, req.WordCount)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "写草稿失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(result))
}

// handlePlan POST /api/books/:id/plan
func (s *Server) handlePlan(c *gin.Context) {
	bookID := c.Param("id")
	var req PlanRequest
	_ = c.ShouldBindJSON(&req)

	result, err := s.pipeline.PlanChapter(bookID, req.Context)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "规划失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(result))
}

// handleCompose POST /api/books/:id/compose
func (s *Server) handleCompose(c *gin.Context) {
	bookID := c.Param("id")
	var req ComposeRequest
	_ = c.ShouldBindJSON(&req)

	result, err := s.pipeline.ComposeChapter(bookID, req.Context)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "组装失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(result))
}

// ============================================================================
// 审查/回炉/润色 Handlers
// ============================================================================

// handleRevise POST /api/books/:id/chapters/:num/revise
func (s *Server) handleRevise(c *gin.Context) {
	bookID := c.Param("id")
	chapterNum, err := strconv.Atoi(c.Param("num"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "无效章号"))
		return
	}

	var req ReviseChapterRequest
	_ = c.ShouldBindJSON(&req)

	mode := models.ReviseMode(req.Mode)
	if mode == "" {
		mode = models.ReviseModeAuto
	}

	result, err := s.pipeline.ReviseChapter(bookID, chapterNum, mode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "回炉失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(result))
}

// handleReview POST /api/books/:id/chapters/:num/review
func (s *Server) handleReview(c *gin.Context) {
	bookID := c.Param("id")
	chapterNum, err := strconv.Atoi(c.Param("num"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "无效章号"))
		return
	}

	result, err := s.pipeline.ReviewChapter(bookID, chapterNum)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "审查失败: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, successResponse(result))
}

// handlePolish POST /api/books/:id/chapters/:num/polish
func (s *Server) handlePolish(c *gin.Context) {
	bookID := c.Param("id")
	chapterNum, err := strconv.Atoi(c.Param("num"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "无效章号"))
		return
	}

	var req PolishChapterRequest
	_ = c.ShouldBindJSON(&req)

	// 润色等同于 polish 模式的回炉
	result, err := s.pipeline.ReviseChapter(bookID, chapterNum, models.ReviseModePolish)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "润色失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(result))
}

// ============================================================================
// 章节管理 Handlers
// ============================================================================

// handleListChapters GET /api/books/:id/chapters
func (s *Server) handleListChapters(c *gin.Context) {
	bookID := c.Param("id")
	chapters, err := s.state.LoadChapterIndex(bookID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "加载章节列表失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(chapters))
}

// handleGetChapter GET /api/books/:id/chapters/:num
func (s *Server) handleGetChapter(c *gin.Context) {
	bookID := c.Param("id")
	chapterNum, err := strconv.Atoi(c.Param("num"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "无效章号"))
		return
	}

	// 读取元数据
	chapters, err := s.state.LoadChapterIndex(bookID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "加载章节失败: "+err.Error()))
		return
	}

	var meta *models.ChapterMeta
	for i := range chapters {
		if chapters[i].Number == chapterNum {
			meta = &chapters[i]
			break
		}
	}
	if meta == nil {
		c.JSON(http.StatusNotFound, errorResponse(1004, "章节不存在"))
		return
	}

	// 读取正文
	chapterPath := s.state.ChapterFilePath(bookID, chapterNum)
	content := state.ReadFileOrDefault(chapterPath, "")

	c.JSON(http.StatusOK, successResponse(gin.H{
		"number":    meta.Number,
		"title":     meta.Title,
		"wordCount": meta.WordCount,
		"status":    meta.Status,
		"revised":   meta.Revised,
		"auditScore": meta.AuditScore,
		"content":   content,
		"filePath":  meta.FilePath,
		"createdAt": meta.CreatedAt,
		"updatedAt": meta.UpdatedAt,
	}))
}

// handleUpdateChapter PATCH /api/books/:id/chapters/:num
func (s *Server) handleUpdateChapter(c *gin.Context) {
	bookID := c.Param("id")
	chapterNum, err := strconv.Atoi(c.Param("num"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "无效章号"))
		return
	}

	var req PatchChapterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "参数错误: "+err.Error()))
		return
	}

	// 加载现有章节
	chapters, err := s.state.LoadChapterIndex(bookID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "加载章节失败: "+err.Error()))
		return
	}

	var meta *models.ChapterMeta
	for i := range chapters {
		if chapters[i].Number == chapterNum {
			meta = &chapters[i]
			break
		}
	}
	if meta == nil {
		c.JSON(http.StatusNotFound, errorResponse(1004, "章节不存在"))
		return
	}

	// 更新字段
	if req.Title != nil {
		meta.Title = *req.Title
	}
	if req.Status != nil {
		meta.Status = models.ChapterStatus(*req.Status)
	}

	if err := s.state.SaveChapterMeta(meta); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "保存章节失败: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, successResponse(meta))
}

// ============================================================================
// 状态接口 Handlers
// ============================================================================

// handleGetStatus GET /api/books/:id/status
func (s *Server) handleGetStatus(c *gin.Context) {
	bookID := c.Param("id")
	info, err := s.state.GetBookStatusInfo(bookID)
	if err != nil {
		c.JSON(http.StatusNotFound, errorResponse(1004, "书籍不存在"))
		return
	}
	c.JSON(http.StatusOK, successResponse(info))
}

// handleGetTruthFiles GET /api/books/:id/truth-files
func (s *Server) handleGetTruthFiles(c *gin.Context) {
	bookID := c.Param("id")
	bookDir := s.state.BookDir(bookID)
	storyDir := filepath.Join(bookDir, "story")

	result := gin.H{
		"bookId":           bookID,
		"pendingHooks":     state.ReadFileOrDefault(filepath.Join(storyDir, "pending_hooks.md"), ""),
		"chapterSummaries": state.ReadFileOrDefault(filepath.Join(storyDir, "chapter_summaries.md"), ""),
		"currentState":     state.ReadFileOrDefault(filepath.Join(storyDir, "current_state.md"), ""),
		"characterMatrix":  state.ReadFileOrDefault(filepath.Join(storyDir, "character_matrix.md"), ""),
		"volumeMap":        state.ReadFileOrDefault(filepath.Join(storyDir, "outline", "volume_map.md"), ""),
		"storyFrame":       state.ReadFileOrDefault(filepath.Join(storyDir, "outline", "story_frame.md"), ""),
		"bookRules":        state.ReadFileOrDefault(filepath.Join(storyDir, "book_rules.md"), ""),
	}
	c.JSON(http.StatusOK, successResponse(result))
}

// ============================================================================
// 基础设定修订 / 卷级合并 / 导入 Handlers
// ============================================================================

// handleReviseFoundation POST /api/books/:id/revise-foundation
func (s *Server) handleReviseFoundation(c *gin.Context) {
	bookID := c.Param("id")
	var req ReviseFoundationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "参数错误: "+err.Error()))
		return
	}

	if err := s.pipeline.ReviseFoundation(bookID, req.Feedback); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "基础设定修订失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(gin.H{"success": true}))
}

// handleConsolidate POST /api/books/:id/consolidate
func (s *Server) handleConsolidate(c *gin.Context) {
	bookID := c.Param("id")
	result, err := s.pipeline.Consolidate(bookID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "卷级合并失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(result))
}

// handleImport POST /api/books/:id/import
func (s *Server) handleImport(c *gin.Context) {
	bookID := c.Param("id")
	var req ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "参数错误: "+err.Error()))
		return
	}

	// 简化实现：从文件读取章节
	if req.Source == "" {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "source 不能为空"))
		return
	}

	content, err := os.ReadFile(req.Source)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "读取源文件失败: "+err.Error()))
		return
	}

	// 按 chapter 分割（简化版：按 "# 第" 或 "## 第" 分割）
	chapters := splitImportChapters(string(content))
	startCh := req.StartChapter
	if startCh <= 0 {
		startCh = 1
	}

	maxCh := req.MaxChapters
	if maxCh <= 0 {
		maxCh = len(chapters)
	}

	imported := 0
	bookDir := s.state.BookDir(bookID)
	for i, ch := range chapters {
		if i >= maxCh {
			break
		}
		chNum := startCh + i
		chapterPath := s.state.ChapterFilePath(bookID, chNum)
		if err := state.WriteFile(chapterPath, ch.content); err != nil {
			s.logger.Warn("导入章节失败", zap.Int("chapter", chNum), zap.Error(err))
			continue
		}

		meta := &models.ChapterMeta{
			BookID:    bookID,
			Number:    chNum,
			Title:     ch.title,
			WordCount: len([]rune(ch.content)),
			Status:    models.ChapterStatusAccepted,
			FilePath:  chapterPath,
		}
		_ = s.state.SaveChapterMeta(meta)
		imported++
	}

	_ = bookDir
	c.JSON(http.StatusOK, successResponse(gin.H{
		"bookId":   bookID,
		"imported": imported,
	}))
}

// importedChapter 导入的章节
type importedChapter struct {
	title   string
	content string
}

// splitImportChapters 分割导入的章节
func splitImportChapters(content string) []importedChapter {
	var chapters []importedChapter
	lines := strings.Split(content, "\n")

	var current strings.Builder
	var currentTitle string
	started := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# 第") || strings.HasPrefix(trimmed, "## 第") {
			if started {
				chapters = append(chapters, importedChapter{
					title:   currentTitle,
					content: current.String(),
				})
			}
			current.Reset()
			currentTitle = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			started = true
			current.WriteString(line + "\n")
		} else if started {
			current.WriteString(line + "\n")
		}
	}
	if started {
		chapters = append(chapters, importedChapter{
			title:   currentTitle,
			content: current.String(),
		})
	}

	return chapters
}

// ============================================================================
// 短篇 Handlers
// ============================================================================

// handleShortFiction POST /api/short-fiction/run
func (s *Server) handleShortFiction(c *gin.Context) {
	var req ShortFictionRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "参数错误: "+err.Error()))
		return
	}

	// 简化实现：创建一本书，写入一章作为短篇
	bookID := uuid.NewString()
	book := &models.BookConfig{
		ID:               bookID,
		Title:            req.Title,
		Genre:            req.Genre,
		Platform:         req.Platform,
		Language:         "zh",
		TargetChapters:   1,
		ChapterWordCount: req.TargetWords,
		Status:           models.BookStatusDraft,
	}
	if book.Platform == "" {
		book.Platform = "知乎"
	}

	if err := s.pipeline.InitBook(book, req.Prompt, ""); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "短篇初始化失败: "+err.Error()))
		return
	}

	// 写一章
	result, err := s.pipeline.WriteNextChapter(bookID, req.TargetWords, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "短篇写作失败: "+err.Error()))
		return
	}

	// 读取正文
	chapterPath := s.state.ChapterFilePath(bookID, result.ChapterNumber)
	content := state.ReadFileOrDefault(chapterPath, "")

	c.JSON(http.StatusOK, successResponse(gin.H{
		"id":         bookID,
		"title":      req.Title,
		"content":    content,
		"wordCount":  result.WordCount,
		"tokenUsage": result.TokenUsage,
	}))
}

// ============================================================================
// 定时调度 Handlers
// ============================================================================

// handleSchedulerStart POST /api/scheduler/start
func (s *Server) handleSchedulerStart(c *gin.Context) {
	var req SchedulerStartRequest
	_ = c.ShouldBindJSON(&req)

	if err := s.scheduler.Start(); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "启动调度失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, successResponse(gin.H{"running": true}))
}

// handleSchedulerStop POST /api/scheduler/stop
func (s *Server) handleSchedulerStop(c *gin.Context) {
	s.scheduler.Stop()
	c.JSON(http.StatusOK, successResponse(gin.H{"running": false}))
}

// handleSchedulerStatus GET /api/scheduler/status
func (s *Server) handleSchedulerStatus(c *gin.Context) {
	status := s.scheduler.Status()
	c.JSON(http.StatusOK, successResponse(status))
}

// ============================================================================
// LLM 配置 Handlers
// ============================================================================

// handleGetLLMProviders GET /api/llm/providers
func (s *Server) handleGetLLMProviders(c *gin.Context) {
	providers := []gin.H{}
	for name, p := range s.config.LLM.Providers {
		masked := maskAPIKey(p.APIKey)
		providers = append(providers, gin.H{
			"id":         name,
			"name":       name,
			"type":       p.Type,
			"baseUrl":    p.BaseURL,
			"model":      p.Model,
			"configured": p.APIKey != "",
			"apiKeyMasked": masked,
		})
	}
	c.JSON(http.StatusOK, successResponse(gin.H{
		"defaultProvider": s.config.LLM.DefaultProvider,
		"defaultModel":    s.config.LLM.DefaultModel,
		"providers":       providers,
	}))
}

// handleUpdateLLMConfig PUT /api/llm/config
func (s *Server) handleUpdateLLMConfig(c *gin.Context) {
	var req LLMConfigUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse(1001, "参数错误: "+err.Error()))
		return
	}

	// 更新配置
	if req.Provider != "" {
		s.config.LLM.DefaultProvider = req.Provider
	}
	if req.Model != "" {
		s.config.LLM.DefaultModel = req.Model
	}

	provider, ok := s.config.LLM.Providers[req.Provider]
	if !ok {
		provider = config.ProviderConfig{}
	}
	if req.APIKey != "" {
		provider.APIKey = req.APIKey
	}
	if req.BaseURL != "" {
		provider.BaseURL = req.BaseURL
	}
	if req.Model != "" {
		provider.Model = req.Model
	}
	if req.Provider != "" {
		provider.Type = req.Provider
		if req.Provider == "anthropic" {
			provider.Type = "anthropic"
		} else {
			provider.Type = "openai"
		}
	}
	s.config.LLM.Providers[req.Provider] = provider

	// 重新创建 LLM 客户端
	llmCfg := llm.ProviderConfig{
		Type:    llm.ProviderType(provider.Type),
		BaseURL: provider.BaseURL,
		APIKey:  provider.APIKey,
		Model:   provider.Model,
	}
	client, err := llm.NewLLMClient(llmCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "LLM 客户端创建失败: "+err.Error()))
		return
	}
	s.llmClient = client

	c.JSON(http.StatusOK, successResponse(gin.H{
		"provider":      req.Provider,
		"model":         provider.Model,
		"apiKeyMasked":  maskAPIKey(provider.APIKey),
		"updated":       true,
	}))
}

// ============================================================================
// SSE 流式写作 Handler
// ============================================================================

// handleWriteStream GET /api/books/:id/write-stream
func (s *Server) handleWriteStream(c *gin.Context) {
	bookID := c.Param("id")
	task := c.DefaultQuery("task", "write-next")

	// 设置 SSE 头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, errorResponse(2001, "SSE 不支持"))
		return
	}

	// 发送 stage 事件
	writeSSEEventToWriter(c.Writer, SSEEvent{
		Type:  SSEEventStage,
		Stage: "planning",
		Data: gin.H{
			"name":     "planning",
			"label":    "规划中",
			"progress": 0.1,
		},
	})
	flusher.Flush()

	// 执行任务
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	var err error
	switch task {
	case "write-next":
		var req WriteNextRequest
		_ = c.ShouldBindJSON(&req)

		// 发送 writing stage
		writeSSEEventToWriter(c.Writer, SSEEvent{
			Type:  SSEEventStage,
			Stage: "writing",
			Data: gin.H{
				"name":     "writing",
				"label":    "写作中",
				"progress": 0.3,
			},
		})
		flusher.Flush()

		result, writeErr := s.pipeline.WriteNextChapter(bookID, req.WordCount, req.TemperatureOverride)
		err = writeErr

		if writeErr == nil {
			// 发送 reviewing stage
			writeSSEEventToWriter(c.Writer, SSEEvent{
				Type:  SSEEventStage,
				Stage: "reviewing",
				Data: gin.H{
					"name":     "reviewing",
					"label":    "审查中",
					"progress": 0.8,
				},
			})
			flusher.Flush()

			// 发送 done
			writeSSEEventToWriter(c.Writer, SSEEvent{
				Type: SSEEventDone,
				Data: gin.H{
					"chapterNumber": result.ChapterNumber,
					"title":         result.Title,
					"wordCount":     result.WordCount,
					"revised":       result.Revised,
					"tokenUsage":    result.TokenUsage,
				},
			})
			flusher.Flush()
		}

	default:
		err = fmt.Errorf("unknown task: %s", task)
	}

	if err != nil {
		writeSSEEventToWriter(c.Writer, SSEEvent{
			Type: SSEEventError,
			Data: gin.H{
				"code":      "TASK_FAILED",
				"message":   err.Error(),
				"retryable": false,
			},
		})
		flusher.Flush()
	}

	_ = ctx
}

// ============================================================================
// 辅助函数
// ============================================================================

// maskAPIKey 脱敏 API Key
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "***"
	}
	return key[:3] + "***" + key[len(key)-4:]
}

// jsonMarshal JSON 序列化
func jsonMarshal(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// writeSSEEventToWriter 写入 SSE 事件到 writer
func writeSSEEventToWriter(w io.Writer, event SSEEvent) {
	event.Timestamp = time.Now()
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, jsonMarshal(event))
}
