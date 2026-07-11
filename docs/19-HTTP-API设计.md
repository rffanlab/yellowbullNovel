# 19 - HTTP API 设计

> 本文档定义 yellowbullNovel 后端的 HTTP API 层（Gin 路由）。
> 对应架构总览中的 `internal/api/` 包。
> inkos 无 server 包，本层为 Go 版新增设计。

---

## 目录

- [一、设计原则](#一设计原则)
- [二、API 路由总表](#二api-路由总表)
- [三、请求/响应结构体定义](#三请求响应结构体定义)
- [四、中间件设计](#四中间件设计)
- [五、SSE 流式输出设计](#五sse-流式输出设计)
- [六、错误处理规范](#六错误处理规范)
- [七、完整 Go Handler 接口定义](#七完整-go-handler-接口定义)

---

## 一、设计原则

1. **RESTful + SSE**：常规操作走 REST，长任务（写作/审查/润色）走 SSE 流式推送。
2. **统一响应包络**：所有 JSON 响应包在 `ApiResponse` 中，错误用 `ApiError`。
3. **书级写锁**：所有写操作经 `StateManager` 的书籍写锁串行化（见 16-状态管理系统）。
4. **幂等性**：创建类接口支持客户端传入 `Idempotency-Key` 头。
5. **上下文超时**：所有 handler 接受 `context.Context`，长任务支持客户端取消。
6. **日志/可观测**：每个请求记录 book_id / chapter_num / duration / token usage。

---

## 二、API 路由总表

### 2.1 书籍管理

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/books` | 创建书籍（建书骨架流程入口） |
| GET | `/api/books` | 列出全部书籍 |
| GET | `/api/books/:id` | 获取单本书详情 |
| DELETE | `/api/books/:id` | 删除书籍（软删除） |

### 2.2 写作接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/books/:id/write-next` | 写下一章（完整流水线：规划→写作→审查→润色） |
| POST | `/api/books/:id/write-draft` | 仅写草稿（不审查不润色） |
| POST | `/api/books/:id/plan` | 仅规划下一章（planner 出 memo） |
| POST | `/api/books/:id/compose` | 仅写作（需先有 memo） |

### 2.3 审查接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/books/:id/chapters/:num/review` | 审查指定章节 |
| POST | `/api/books/:id/chapters/:num/audit` | 仅跑连续性审计（不出 issues 列表） |

### 2.4 回炉接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/books/:id/chapters/:num/revise` | 回炉重写，body: `{mode, issues}` |

### 2.5 润色接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/books/:id/chapters/:num/polish` | 对指定章节独立润色 |

### 2.6 章节管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/books/:id/chapters` | 列出全部章节 |
| GET | `/api/books/:id/chapters/:num` | 获取单章内容 |
| PATCH | `/api/books/:id/chapters/:num` | 修改章节元数据（标题/状态/标签，不改正文） |

### 2.7 状态接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/books/:id/status` | 获取书籍写作状态（当前章/进度/写锁） |
| GET | `/api/books/:id/truth-files` | 获取 truth files 内容（pending_hooks / chapter_summaries 等） |

### 2.8 基础设定修订

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/books/:id/revise-foundation` | 基础设定修订（14-基础设定修订流程） |

### 2.9 卷级合并

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/books/:id/consolidate` | 卷级合并（09-卷级合并流程） |

### 2.10 导入

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/books/:id/import` | 导入已有小说（08-导入续写流程） |

### 2.11 短篇

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/short-fiction/run` | 短篇生产全流程（10-短篇生产流程） |

### 2.12 定时调度

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/scheduler/start` | 启动定时调度（11-定时调度流程） |
| POST | `/api/scheduler/stop` | 停止定时调度 |
| GET | `/api/scheduler/status` | 查询调度状态 |

### 2.13 LLM 配置

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/llm/providers` | 列出可用 LLM 提供商 |
| PUT | `/api/llm/config` | 更新 LLM 配置 |

### 2.14 SSE 流式

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/books/:id/write-stream` | 写作进度实时推送（Server-Sent Events） |

---

## 三、请求/响应结构体定义

文件位置：`internal/api/types.go`。

### 3.1 统一响应包络

```go
package api

// ApiResponse 统一响应包络
type ApiResponse struct {
    Code    int         `json:"code"`    // 0=成功，非 0=业务错误码
    Message string      `json:"message"` // 人类可读消息
    Data    interface{} `json:"data,omitempty"`
}

// ApiError 错误响应
type ApiError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Detail  string `json:"detail,omitempty"` // 调试细节（仅开发环境）
}

// PagedResponse 分页响应
type PagedResponse struct {
    Items    interface{} `json:"items"`
    Total    int64       `json:"total"`
    Page     int         `json:"page"`
    PageSize int         `json:"pageSize"`
}
```

### 3.2 书籍管理

```go
// CreateBookRequest 创建书籍请求（对应 01-建书骨架流程）
type CreateBookRequest struct {
    Title       string   `json:"title" binding:"required"`
    Author      string   `json:"author"`
    Platform    string   `json:"platform" binding:"required"`    // tomato/feilu/qidian/other
    Genre       string   `json:"genre" binding:"required"`       // 都市/玄幻/科幻/...
    TargetWords int      `json:"targetWords" binding:"required"` // 目标总字数
    Synopsis    string   `json:"synopsis"`                       // 一句话简介
    Outline     string   `json:"outline,omitempty"`              // 可选：已有大纲
    Fanfic      *bool    `json:"fanfic,omitempty"`               // 同人模式
    BookRules   *BookRules `json:"bookRules,omitempty"`          // 题材规则覆盖
}

// BookResponse 书籍详情
type BookResponse struct {
    ID           string      `json:"id"`
    Title        string      `json:"title"`
    Author       string      `json:"author"`
    Platform     string      `json:"platform"`
    Genre        string      `json:"genre"`
    TargetWords  int         `json:"targetWords"`
    Status       string      `json:"status"` // incubating/outlining/active/paused/completed/dropped
    CurrentChapter int       `json:"currentChapter"`
    CreatedAt    time.Time   `json:"createdAt"`
    UpdatedAt    time.Time   `json:"updatedAt"`
    Config       *BookConfig `json:"config,omitempty"`
}
```

### 3.3 写作接口

```go
// WriteNextRequest 写下一章
type WriteNextRequest struct {
    DryRun       bool   `json:"dryRun,omitempty"`       // 仅规划不落盘
    SkipPolish   bool   `json:"skipPolish,omitempty"`   // 跳过润色
    SkipReview   bool   `json:"skipReview,omitempty"`   // 跳过审查（危险，仅调试）
    MaxRetries   int    `json:"maxRetries,omitempty"`   // 审查回炉最大次数
    Stream       bool   `json:"stream,omitempty"`       // 是否走 SSE 流式
}

// WriteNextResponse 写下一章结果
type WriteNextResponse struct {
    ChapterNumber int             `json:"chapterNumber"`
    Title         string          `json:"title"`
    Content       string          `json:"content,omitempty"`     // 非 stream 时返回
    WordCount     int             `json:"wordCount"`
    ReviewResult  *ReviewResult   `json:"reviewResult,omitempty"`
    PolishResult  *PolishResult   `json:"polishResult,omitempty"`
    TokenUsage    *TokenUsage     `json:"tokenUsage,omitempty"`
    Duration      float64         `json:"durationSeconds"`
}

// ReviewResult 审查结果
type ReviewResult struct {
    Score       float64       `json:"score"`       // 0-100
    Pass        bool          `json:"pass"`
    Issues      []AuditIssue  `json:"issues"`
    Retries     int           `json:"retries"`
}

// PolishResult 润色结果
type PolishResult struct {
    Changed       bool     `json:"changed"`
    PolisherNotes []string `json:"polisherNotes,omitempty"`
    Skipped       bool     `json:"skipped"`
}

// PlanChapterRequest 仅规划
type PlanChapterRequest struct {
    ChapterNumber int `json:"chapterNumber,omitempty"` // 0 表示下一章
}

// PlanChapterResponse 规划结果
type PlanChapterResponse struct {
    ChapterNumber int          `json:"chapterNumber"`
    Memo          *ChapterMemo `json:"memo"`
}

// ComposeRequest 仅写作
type ComposeRequest struct {
    ChapterNumber int          `json:"chapterNumber" binding:"required"`
    Memo          *ChapterMemo `json:"memo"` // 可选，不传则读已存的
}
```

### 3.4 审查/回炉/润色

```go
// ReviewChapterRequest 审查章节
type ReviewChapterRequest struct {
    Deep bool `json:"deep,omitempty"` // 深度审查（含连续性审计）
}

// ReviseChapterRequest 回炉重写
type ReviseChapterRequest struct {
    Mode   string        `json:"mode" binding:"required"`  // auto/polish/rewrite/rework/anti-detect/spot-fix
    Issues []AuditIssue  `json:"issues"`                   // mode=auto 时可为空（自动检测）
}

// ReviseChapterResponse 回炉结果
type ReviseChapterResponse struct {
    RevisedContent string        `json:"revisedContent"`
    Patches        []SpotFixPatch `json:"patches,omitempty"` // spot-fix 模式
    ReviewResult   *ReviewResult  `json:"reviewResult,omitempty"`
    TokenUsage     *TokenUsage    `json:"tokenUsage,omitempty"`
}

// PolishChapterRequest 润色请求（对应 07-润色流程）
type PolishChapterRequest struct {
    Temperature float64 `json:"temperature,omitempty"` // 默认 0.4
    Language    string  `json:"language,omitempty"`    // 默认 "zh"
}

// PolishChapterResponse 润色响应
type PolishChapterResponse struct {
    PolishedContent string   `json:"polishedContent"`
    Changed         bool     `json:"changed"`
    PolisherNotes   []string `json:"polisherNotes,omitempty"`
    Skipped         bool     `json:"skipped"`
    TokenUsage      *TokenUsage `json:"tokenUsage,omitempty"`
}
```

### 3.5 章节管理

```go
// ChapterListItem 章节列表项
type ChapterListItem struct {
    Number    int       `json:"number"`
    Title     string    `json:"title"`
    WordCount int       `json:"wordCount"`
    Status    string    `json:"status"` // draft/reviewed/polished/final
    CreatedAt time.Time `json:"createdAt"`
    UpdatedAt time.Time `json:"updatedAt"`
}

// ChapterDetail 章节详情
type ChapterDetail struct {
    ChapterListItem
    Content       string         `json:"content"`
    Memo          *ChapterMemo   `json:"memo,omitempty"`
    ReviewResult  *ReviewResult  `json:"reviewResult,omitempty"`
    PolishResult  *PolishResult  `json:"polishResult,omitempty"`
}

// PatchChapterRequest 修改章节元数据
type PatchChapterRequest struct {
    Title  *string `json:"title,omitempty"`
    Status *string `json:"status,omitempty"`
    Tags   []string `json:"tags,omitempty"`
}
```

### 3.6 状态接口

```go
// BookStatusResponse 书籍状态
type BookStatusResponse struct {
    BookID          string   `json:"bookId"`
    Status          string   `json:"status"`
    CurrentChapter  int      `json:"currentChapter"`
    TargetChapters  int      `json:"targetChapters"`
    TotalWords      int      `json:"totalWords"`
    WriteLockHeld   bool     `json:"writeLockHeld"`
    WriteLockOwner  string   `json:"writeLockOwner,omitempty"`
    LastWriteAt     *time.Time `json:"lastWriteAt,omitempty"`
    ActiveHooks     int      `json:"activeHooks"`
    PendingTasks    []string `json:"pendingTasks,omitempty"`
}

// TruthFilesResponse truth files 内容
type TruthFilesResponse struct {
    BookID              string `json:"bookId"`
    PendingHooks        string `json:"pendingHooks"`        // pending_hooks.md 原文
    ChapterSummaries    string `json:"chapterSummaries"`    // chapter_summaries.md 原文
    CurrentState        string `json:"currentState"`        // current_state.md 原文
    CharacterMatrix     string `json:"characterMatrix"`     // character_matrix.md 原文
    VolumeMap           string `json:"volumeMap,omitempty"` // volume_map.md 原文
}
```

### 3.7 基础设定修订 / 卷级合并 / 导入

```go
// ReviseFoundationRequest 基础设定修订
type ReviseFoundationRequest struct {
    Instructions string `json:"instructions" binding:"required"` // 修订指令
    DryRun       bool   `json:"dryRun,omitempty"`                // 仅生成 diff 不落盘
}

// ConsolidateRequest 卷级合并
type ConsolidateRequest struct {
    VolumeName string `json:"volumeName" binding:"required"`
    StartCh    int    `json:"startCh" binding:"required"`
    EndCh      int    `json:"endCh" binding:"required"`
}

// ImportRequest 导入已有小说
type ImportRequest struct {
    Source      string `json:"source" binding:"required"`        // 文件路径或 URL
    Format      string `json:"format" binding:"required"`        // markdown/txt/docx
    StartChapter int   `json:"startChapter,omitempty"`           // 从第几章开始导入
    MaxChapters  int   `json:"maxChapters,omitempty"`            // 最多导入多少章
}
```

### 3.8 短篇

```go
// ShortFictionRunRequest 短篇生产请求
type ShortFictionRunRequest struct {
    Title    string `json:"title" binding:"required"`
    Genre    string `json:"genre" binding:"required"`   // 盐言/追妻/虐渣/重生/世情/...
    Prompt   string `json:"prompt" binding:"required"`  // 核心创意/要求
    TargetWords int `json:"targetWords" binding:"required"`
    Platform string `json:"platform,omitempty"`         // 知乎/七猫/黑岩/点众
    Stream   bool   `json:"stream,omitempty"`           // SSE 流式
}

// ShortFictionRunResponse 短篇生产结果
type ShortFictionRunResponse struct {
    ID         string      `json:"id"`
    Title      string      `json:"title"`
    Content    string      `json:"content,omitempty"`
    WordCount  int         `json:"wordCount"`
    TokenUsage *TokenUsage `json:"tokenUsage,omitempty"`
    Duration   float64     `json:"durationSeconds"`
}
```

### 3.9 调度器

```go
// SchedulerStartRequest 启动调度
type SchedulerStartRequest struct {
    BookID    string   `json:"bookId" binding:"required"`
    Rrule     string   `json:"rrule" binding:"required"` // RFC 5545 RRULE
    ChaptersPerRun int `json:"chaptersPerRun,omitempty"` // 每次写几章，默认 1
}

// SchedulerStatusResponse 调度状态
type SchedulerStatusResponse struct {
    Running       bool       `json:"running"`
    BookID        string     `json:"bookId,omitempty"`
    Rrule         string     `json:"rrule,omitempty"`
    NextRunAt     *time.Time `json:"nextRunAt,omitempty"`
    LastRunAt     *time.Time `json:"lastRunAt,omitempty"`
    LastRunStatus string     `json:"lastRunStatus,omitempty"` // success/failed/running
    TotalRuns     int        `json:"totalRuns"`
    FailedRuns    int        `json:"failedRuns"`
}
```

### 3.10 LLM 配置

```go
// LLMProviderInfo LLM 提供商信息
type LLMProviderInfo struct {
    ID           string   `json:"id"`           // openai/anthropic/custom-xxx
    Name         string   `json:"name"`
    Models       []string `json:"models"`       // 支持的模型列表
    Capabilities []string `json:"capabilities"` // chat/streaming/vision/function-call
    Configured   bool     `json:"configured"`   // 是否已配置 API Key
}

// LLMConfigUpdateRequest LLM 配置更新
type LLMConfigUpdateRequest struct {
    Provider    string  `json:"provider" binding:"required"`
    APIKey      string  `json:"apiKey,omitempty"`      // 写入时需脱敏
    BaseURL     string  `json:"baseUrl,omitempty"`
    Model       string  `json:"model,omitempty"`
    Temperature *float64 `json:"temperature,omitempty"`
    MaxTokens   *int    `json:"maxTokens,omitempty"`
}

// LLMConfigResponse LLM 配置回读（API Key 脱敏）
type LLMConfigResponse struct {
    Provider    string   `json:"provider"`
    BaseURL     string   `json:"baseUrl"`
    Model       string   `json:"model"`
    Temperature float64  `json:"temperature"`
    MaxTokens   int      `json:"maxTokens"`
    APIKeyMasked string  `json:"apiKeyMasked"` // 如 sk-***...***
}
```

### 3.11 通用引用类型

```go
// AuditIssue 审计问题（与 18-数据模型 一致）
type AuditIssue struct {
    Severity   string `json:"severity"`
    Category   string `json:"category"`
    Description string `json:"description"`
    Suggestion string `json:"suggestion"`
}

// ChapterMemo 章节备忘
type ChapterMemo struct {
    Goal string `json:"goal"`
    Body string `json:"body"`
}

// TokenUsage token 用量
type TokenUsage struct {
    PromptTokens     int `json:"promptTokens"`
    CompletionTokens int `json:"completionTokens"`
    TotalTokens      int `json:"totalTokens"`
}

// SpotFixPatch 定点修复补丁（spot-fix 模式）
type SpotFixPatch struct {
    Anchor     string `json:"anchor"`     // 锚点文本
    OldText    string `json:"oldText"`
    NewText    string `json:"newText"`
    Reason     string `json:"reason"`
}

// BookRules 题材规则覆盖（与 18-数据模型 一致）
type BookRules struct {
    // 见 18-数据模型 §18
}
```

---

## 四、中间件设计

文件位置：`internal/api/middleware.go`。

### 4.1 CORS

```go
// CORSMiddleware 跨域中间件
// 允许前端开发域名访问，生产环境通过配置项限定
func CORSMiddleware(cfg CORSConfig) gin.HandlerFunc

type CORSConfig struct {
    AllowOrigins     []string // 默认 ["*"]，生产建议限定
    AllowMethods     []string // 默认 GET/POST/PUT/PATCH/DELETE/OPTIONS
    AllowHeaders     []string // 默认 Authorization/Content-Type/Idempotency-Key
    AllowCredentials bool
    MaxAge           time.Duration // 默认 12h
}
```

### 4.2 日志

```go
// LoggingMiddleware 请求日志中间件
// 记录：method / path / status / duration / book_id / chapter_num / token_usage
func LoggingMiddleware(logger Logger) gin.HandlerFunc
```

日志格式（结构化 JSON）：

```json
{
  "ts": "2026-07-11T10:30:00Z",
  "method": "POST",
  "path": "/api/books/yb-001/write-next",
  "status": 200,
  "durationMs": 45230,
  "bookId": "yb-001",
  "chapterNum": 12,
  "tokenUsage": {"prompt": 8200, "completion": 3100, "total": 11300},
  "requestId": "req-abc123",
  "clientIp": "127.0.0.1"
}
```

### 4.3 错误处理

```go
// ErrorHandlerMiddleware 错误恢复中间件
// 1. panic 恢复：捕获 panic 返回 500
// 2. 统一错误响应：把 error 转为 ApiError JSON
func ErrorHandlerMiddleware(logger Logger) gin.HandlerFunc
```

错误码约定：

| HTTP 状态 | code | 含义 |
|-----------|------|------|
| 400 | 1001 | 请求参数错误 |
| 401 | 1002 | 未认证 |
| 403 | 1003 | 无权限 |
| 404 | 1004 | 资源不存在 |
| 409 | 1005 | 资源冲突（如写锁被占） |
| 422 | 1006 | 业务校验失败 |
| 429 | 1007 | 请求频率超限 |
| 500 | 2001 | 内部错误 |
| 503 | 2002 | LLM 服务不可用 |
| 504 | 2003 | 上游超时 |

### 4.4 认证

```go
// AuthMiddleware 认证中间件
// 支持 Bearer Token / API Key 两种方式
// 单机开发模式可配置为匿名（cfg.DevMode=true）
func AuthMiddleware(cfg AuthConfig) gin.HandlerFunc

type AuthConfig struct {
    DevMode  bool   // 开发模式跳过认证
    Token    string // 静态 token（简单场景）
    JWTSecret string // JWT 密钥（多用户场景）
}
```

### 4.5 请求 ID

```go
// RequestIDMiddleware 为每个请求注入唯一 requestId
// 优先复用客户端的 X-Request-Id 头
func RequestIDMiddleware() gin.HandlerFunc
```

### 4.6 速率限制

```go
// RateLimitMiddleware 速率限制
// 按 book_id 维度限流，避免同一本书并发写入冲突
// 默认：每书每秒 5 个请求，突发 10
func RateLimitMiddleware(cfg RateLimitConfig) gin.HandlerFunc

type RateLimitConfig struct {
    RequestsPerSecond int
    Burst             int
    KeyFunc           func(*gin.Context) string // 默认按 book_id
}
```

### 4.7 写锁

```go
// BookWriteLockMiddleware 书级写锁中间件
// 对 POST/PATCH/DELETE 写操作加书籍级互斥锁
// 锁竞争时返回 409 Conflict
func BookWriteLockMiddleware(sm state.StateManager) gin.HandlerFunc
```

### 4.8 中间件链

```go
func SetupMiddlewares(r *gin.Engine, cfg ServerConfig) {
    r.Use(
        RequestIDMiddleware(),
        CORSMiddleware(cfg.CORS),
        LoggingMiddleware(cfg.Logger),
        ErrorHandlerMiddleware(cfg.Logger),
        AuthMiddleware(cfg.Auth),
        RateLimitMiddleware(cfg.RateLimit),
    )
    // 写锁中间件只挂在写路由组
    writeGroup := r.Group("/api", BookWriteLockMiddleware(cfg.StateManager))
    // ... 注册写路由
}
```

---

## 五、SSE 流式输出设计

文件位置：`internal/api/sse.go`。

### 5.1 概述

长任务（写作/审查/润色）通过 SSE（Server-Sent Events）实时推送进度，前端用 `EventSource` 接收。SSE 相比 WebSocket 更简单：单向推送、自动重连、HTTP/2 友好。

### 5.2 端点

```
GET /api/books/:id/write-stream
```

**Query 参数**：

| 参数 | 说明 |
|------|------|
| `task` | 任务类型：`write-next` / `review` / `polish` / `short-fiction` |
| `chapter` | 目标章号（`write-next`/`review`/`polish` 需要） |
| `mode` | 回炉模式（`task=revise` 时） |

### 5.3 SSE 事件类型

```go
// SSEEventType SSE 事件类型
type SSEEventType string

const (
    SSEEventStage      SSEEventType = "stage"       // 阶段切换
    SSEEventProgress   SSEEventType = "progress"    // 阶段内进度
    SSEEventChunk      SSEEventType = "chunk"       // 正文增量 chunk
    SSEEventTokenUsage SSEEventType = "tokenUsage"  // token 用量
    SSEEventIssue      SSEEventType = "issue"       // 审查发现的 issue
    SSEEventNote       SSEEventType = "note"        // polisher-note
    SSEEventError      SSEEventType = "error"       // 错误
    SSEEventDone       SSEEventType = "done"        // 完成
)
```

### 5.4 事件载荷

```go
// SSEEvent SSE 事件
type SSEEvent struct {
    Type  SSEEventType `json:"type"`
    Stage string       `json:"stage,omitempty"` // planning/writing/reviewing/revising/polishing/done
    Data  interface{}  `json:"data,omitempty"`
    Timestamp time.Time `json:"ts"`
}

// StageData 阶段切换事件
type StageData struct {
    Name      string `json:"name"`      // planning/writing/reviewing/...
    Label     string `json:"label"`     // "规划中" / "写作中" / ...
    Progress  float64 `json:"progress"`  // 0-1 整体进度
}

// ChunkData 正文增量
type ChunkData struct {
    Content string `json:"content"` // 本次新增的文本片段
    Offset  int    `json:"offset"`  // 累计偏移
}

// IssueData 审查问题
type IssueData struct {
    Issue AuditIssue `json:"issue"`
    Retry int        `json:"retry"` // 第几次回炉
}

// DoneData 完成事件
type DoneData struct {
    ChapterNumber int         `json:"chapterNumber,omitempty"`
    Title         string      `json:"title,omitempty"`
    WordCount     int         `json:"wordCount,omitempty"`
    ReviewResult  *ReviewResult `json:"reviewResult,omitempty"`
    PolishResult  *PolishResult `json:"polishResult,omitempty"`
    TokenUsage    *TokenUsage   `json:"tokenUsage,omitempty"`
    Duration      float64       `json:"durationSeconds"`
}

// ErrorData 错误事件
type ErrorData struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    Stage   string `json:"stage,omitempty"`
    Retryable bool `json:"retryable"`
}
```

### 5.5 线上传输格式

SSE 遵循 `text/event-stream` 规范，每个事件用两行 `\n\n` 分隔：

```
event: stage
data: {"type":"stage","stage":"planning","data":{"name":"planning","label":"规划中","progress":0.1},"ts":"2026-07-11T10:30:01Z"}

event: chunk
data: {"type":"chunk","stage":"writing","data":{"content":"林晚站在山门前","offset":0},"ts":"2026-07-11T10:30:15Z"}

event: chunk
data: {"type":"chunk","stage":"writing","data":{"content":"，看着满地落叶","offset":8},"ts":"2026-07-11T10:30:16Z"}

event: issue
data: {"type":"issue","stage":"reviewing","data":{"issue":{"severity":"warning","category":"伏笔债务","description":"..."},"retry":1},"ts":"2026-07-11T10:31:00Z"}

event: done
data: {"type":"done","data":{"chapterNumber":12,"title":"碎星","wordCount":3120,"durationSeconds":92.3},"ts":"2026-07-11T10:31:32Z"}
```

### 5.6 Handler 实现

```go
// SSEHandler SSE 流式 handler
type SSEHandler struct {
    pipeline  pipeline.Service
    logger    Logger
}

// WriteStream GET /api/books/:id/write-stream
func (h *SSEHandler) WriteStream(c *gin.Context) {
    bookID := c.Param("id")
    task := c.Query("task")

    // 设置 SSE 头
    c.Header("Content-Type", "text/event-stream")
    c.Header("Cache-Control", "no-cache")
    c.Header("Connection", "keep-alive")
    c.Header("X-Accel-Buffering", "no") // nginx 关闭缓冲

    // 创建事件通道
    eventCh := make(chan SSEEvent, 64)
    errCh := make(chan error, 1)

    // 后台跑任务，事件推到 eventCh
    ctx, cancel := context.WithCancel(c.Request.Context())
    defer cancel()
    go func() {
        err := h.runTask(ctx, bookID, task, c.Query("chapter"), eventCh)
        if err != nil {
            errCh <- err
        }
        close(eventCh)
    }()

    // 心跳（每 15s 发一个注释行，防止代理超时）
    ticker := time.NewTicker(15 * time.Second)
    defer ticker.Stop()

    c.Stream(func(w io.Writer) bool {
        select {
        case <-c.Request.Context().Done():
            return false // 客户端断开
        case err := <-errCh:
            h.writeEvent(c, SSEEvent{Type: SSEEventError, Data: ErrorData{
                Code: "TASK_FAILED", Message: err.Error(), Retryable: false,
            }})
            return false
        case ev, ok := <-eventCh:
            if !ok {
                return false
            }
            h.writeEvent(c, ev)
            return ev.Type != SSEEventDone
        case <-ticker.C:
            c.SSEvent("ping", "") // 心跳注释
            return true
        }
    })
}

func (h *SSEHandler) writeEvent(c *gin.Context, ev SSEEvent) {
    data, _ := json.Marshal(ev)
    c.SSEvent(string(ev.Type), string(data))
    c.Writer.Flush()
}

func (h *SSEHandler) runTask(ctx context.Context, bookID, task, chapter string, eventCh chan<- SSEEvent) error {
    switch task {
    case "write-next":
        return h.pipeline.WriteNextStream(ctx, bookID, eventCh)
    case "review":
        num, _ := strconv.Atoi(chapter)
        return h.pipeline.ReviewStream(ctx, bookID, num, eventCh)
    case "polish":
        num, _ := strconv.Atoi(chapter)
        return h.pipeline.PolishStream(ctx, bookID, num, eventCh)
    default:
        return fmt.Errorf("unknown task: %s", task)
    }
}
```

### 5.7 Pipeline 侧事件发射

pipeline.Service 在执行各阶段时往 `eventCh` 推事件：

```go
// 简化伪代码
func (p *Service) WriteNextStream(ctx context.Context, bookID string, ch chan<- api.SSEEvent) error {
    start := time.Now()
    // 1. 规划
    ch <- api.SSEEvent{Type: api.SSEEventStage, Stage: "planning", Data: api.StageData{Name: "planning", Label: "规划中", Progress: 0.1}}
    memo, err := p.planner.Plan(ctx, bookID)
    if err != nil { return p.emitError(ch, err, "planning") }

    // 2. 写作（chunk 流式）
    ch <- api.SSEEvent{Type: api.SSEEventStage, Stage: "writing", Data: api.StageData{Name: "writing", Label: "写作中", Progress: 0.3}}
    var content strings.Builder
    err = p.writer.WriteStream(ctx, bookID, memo, func(chunk string) {
        offset := content.Len()
        content.WriteString(chunk)
        ch <- api.SSEEvent{Type: api.SSEEventChunk, Stage: "writing", Data: api.ChunkData{Content: chunk, Offset: offset}}
    })
    if err != nil { return p.emitError(ch, err, "writing") }

    // 3. 审查 + 回炉循环
    ch <- api.SSEEvent{Type: api.SSEEventStage, Stage: "reviewing", Data: api.StageData{Name: "reviewing", Label: "审查中", Progress: 0.6}}
    for retry := 0; retry < maxRetries; retry++ {
        review := p.reviewer.Review(ctx, bookID, num, content.String())
        for _, issue := range review.Issues {
            ch <- api.SSEEvent{Type: api.SSEEventIssue, Stage: "reviewing", Data: api.IssueData{Issue: issue, Retry: retry}}
        }
        if review.Pass { break }
        // 回炉...
        ch <- api.SSEEvent{Type: api.SSEEventStage, Stage: "revising", Data: api.StageData{Name: "revising", Label: fmt.Sprintf("回炉第%d次", retry+1), Progress: 0.6 + 0.05*float64(retry)}}
        content = p.reviser.Revise(...)
    }

    // 4. 润色
    ch <- api.SSEEvent{Type: api.SSEEventStage, Stage: "polishing", Data: api.StageData{Name: "polishing", Label: "润色中", Progress: 0.9}}
    polish := p.polisher.PolishChapter(...)

    // 5. 完成
    ch <- api.SSEEvent{Type: api.SSEEventDone, Data: api.DoneData{
        ChapterNumber: num,
        WordCount:     content.Len(),
        Duration:      time.Since(start).Seconds(),
    }}
    return nil
}
```

---

## 六、错误处理规范

### 6.1 统一错误响应

```json
{
  "code": 1005,
  "message": "书写锁被占用",
  "detail": "book yb-001 is currently being written by another process"
}
```

### 6.2 常见错误场景

| 场景 | HTTP | code | 处理 |
|------|------|------|------|
| 请求参数缺失/格式错 | 400 | 1001 | gin binding error |
| 未带认证 | 401 | 1002 | AuthMiddleware |
| 书不存在 | 404 | 1004 | handler 返回 |
| 书写锁冲突 | 409 | 1005 | BookWriteLockMiddleware |
| 章节号超出范围 | 422 | 1006 | handler 校验 |
| LLM 调用失败 | 503 | 2002 | 重试 3 次后返回 |
| LLM 超时 | 504 | 2003 | context.DeadlineExceeded |
| panic | 500 | 2001 | ErrorHandlerMiddleware 恢复 |

### 6.3 幂等性

`POST /api/books` 和 `POST /api/books/:id/write-next` 支持 `Idempotency-Key` 头：

```go
// IdempotencyMiddleware 幂等中间件
// 同一个 Idempotency-Key 在 24h 内重复请求，直接返回首次结果
func IdempotencyMiddleware(store IdempotencyStore) gin.HandlerFunc
```

---

## 七、完整 Go Handler 接口定义

文件位置：`internal/api/handlers.go`、`internal/api/router.go`。

### 7.1 Router 注册

```go
package api

// SetupRouter 注册全部路由
func SetupRouter(
    r *gin.Engine,
    cfg ServerConfig,
    bookHandler BookHandler,
    writeHandler WriteHandler,
    reviewHandler ReviewHandler,
    reviseHandler ReviseHandler,
    polishHandler PolishHandler,
    chapterHandler ChapterHandler,
    statusHandler StatusHandler,
    foundationHandler FoundationHandler,
    consolidateHandler ConsolidateHandler,
    importHandler ImportHandler,
    shortFictionHandler ShortFictionHandler,
    schedulerHandler SchedulerHandler,
    llmHandler LLMHandler,
    sseHandler SSEHandler,
) {
    api := r.Group("/api")
    api.Use(
        RequestIDMiddleware(),
        CORSMiddleware(cfg.CORS),
        LoggingMiddleware(cfg.Logger),
        ErrorHandlerMiddleware(cfg.Logger),
        AuthMiddleware(cfg.Auth),
        RateLimitMiddleware(cfg.RateLimit),
    )

    // 书籍管理
    books := api.Group("/books")
    {
        books.POST("", bookHandler.Create)                 // 创建
        books.GET("", bookHandler.List)                    // 列表
        books.GET("/:id", bookHandler.Get)                 // 详情
        books.DELETE("/:id", bookHandler.Delete)           // 删除
    }

    // 写作（写操作加书锁）
    bookWrite := api.Group("/books/:id", BookWriteLockMiddleware(cfg.StateManager))
    {
        bookWrite.POST("/write-next", writeHandler.WriteNext)
        bookWrite.POST("/write-draft", writeHandler.WriteDraft)
        bookWrite.POST("/plan", writeHandler.Plan)
        bookWrite.POST("/compose", writeHandler.Compose)
    }

    // 审查 / 回炉 / 润色（写操作）
    chapterOp := api.Group("/books/:id/chapters/:num", BookWriteLockMiddleware(cfg.StateManager))
    {
        chapterOp.POST("/review", reviewHandler.Review)
        chapterOp.POST("/audit", reviewHandler.Audit)
        chapterOp.POST("/revise", reviseHandler.Revise)
        chapterOp.POST("/polish", polishHandler.Polish)
    }

    // 章节管理（读 + 元数据修改）
    chapters := api.Group("/books/:id/chapters")
    {
        chapters.GET("", chapterHandler.List)
        chapters.GET("/:num", chapterHandler.Get)
        chapters.PATCH("/:num", BookWriteLockMiddleware(cfg.StateManager), chapterHandler.Patch)
    }

    // 状态
    api.GET("/books/:id/status", statusHandler.GetStatus)
    api.GET("/books/:id/truth-files", statusHandler.GetTruthFiles)

    // 基础设定修订 / 卷级合并 / 导入
    api.POST("/books/:id/revise-foundation", BookWriteLockMiddleware(cfg.StateManager), foundationHandler.Revise)
    api.POST("/books/:id/consolidate", BookWriteLockMiddleware(cfg.StateManager), consolidateHandler.Consolidate)
    api.POST("/books/:id/import", BookWriteLockMiddleware(cfg.StateManager), importHandler.Import)

    // 短篇
    api.POST("/short-fiction/run", shortFictionHandler.Run)

    // 调度器
    sched := api.Group("/scheduler")
    {
        sched.POST("/start", schedulerHandler.Start)
        sched.POST("/stop", schedulerHandler.Stop)
        sched.GET("/status", schedulerHandler.Status)
    }

    // LLM 配置
    llm := api.Group("/llm")
    {
        llm.GET("/providers", llmHandler.ListProviders)
        llm.PUT("/config", llmHandler.UpdateConfig)
    }

    // SSE 流式（GET，不加写锁，由内部任务管理）
    api.GET("/books/:id/write-stream", sseHandler.WriteStream)
}
```

### 7.2 Handler 接口

```go
package api

// BookHandler 书籍管理
type BookHandler interface {
    Create(c *gin.Context)   // POST /api/books
    List(c *gin.Context)     // GET /api/books
    Get(c *gin.Context)      // GET /api/books/:id
    Delete(c *gin.Context)   // DELETE /api/books/:id
}

// WriteHandler 写作接口
type WriteHandler interface {
    WriteNext(c *gin.Context)   // POST /api/books/:id/write-next
    WriteDraft(c *gin.Context)  // POST /api/books/:id/write-draft
    Plan(c *gin.Context)        // POST /api/books/:id/plan
    Compose(c *gin.Context)     // POST /api/books/:id/compose
}

// ReviewHandler 审查接口
type ReviewHandler interface {
    Review(c *gin.Context)  // POST /api/books/:id/chapters/:num/review
    Audit(c *gin.Context)   // POST /api/books/:id/chapters/:num/audit
}

// ReviseHandler 回炉接口
type ReviseHandler interface {
    Revise(c *gin.Context)  // POST /api/books/:id/chapters/:num/revise
}

// PolishHandler 润色接口
type PolishHandler interface {
    Polish(c *gin.Context)  // POST /api/books/:id/chapters/:num/polish
}

// ChapterHandler 章节管理
type ChapterHandler interface {
    List(c *gin.Context)   // GET /api/books/:id/chapters
    Get(c *gin.Context)    // GET /api/books/:id/chapters/:num
    Patch(c *gin.Context)  // PATCH /api/books/:id/chapters/:num
}

// StatusHandler 状态接口
type StatusHandler interface {
    GetStatus(c *gin.Context)      // GET /api/books/:id/status
    GetTruthFiles(c *gin.Context)  // GET /api/books/:id/truth-files
}

// FoundationHandler 基础设定修订
type FoundationHandler interface {
    Revise(c *gin.Context)  // POST /api/books/:id/revise-foundation
}

// ConsolidateHandler 卷级合并
type ConsolidateHandler interface {
    Consolidate(c *gin.Context)  // POST /api/books/:id/consolidate
}

// ImportHandler 导入
type ImportHandler interface {
    Import(c *gin.Context)  // POST /api/books/:id/import
}

// ShortFictionHandler 短篇
type ShortFictionHandler interface {
    Run(c *gin.Context)  // POST /api/short-fiction/run
}

// SchedulerHandler 调度器
type SchedulerHandler interface {
    Start(c *gin.Context)   // POST /api/scheduler/start
    Stop(c *gin.Context)    // POST /api/scheduler/stop
    Status(c *gin.Context)  // GET /api/scheduler/status
}

// LLMHandler LLM 配置
type LLMHandler interface {
    ListProviders(c *gin.Context)  // GET /api/llm/providers
    UpdateConfig(c *gin.Context)   // PUT /api/llm/config
}

// SSEHandler SSE 流式
type SSEHandler interface {
    WriteStream(c *gin.Context)  // GET /api/books/:id/write-stream
}
```

### 7.3 ServerConfig

```go
package api

// ServerConfig 服务器配置
type ServerConfig struct {
    ListenAddr    string         // 监听地址，默认 ":8080"
    CORS          CORSConfig
    Auth          AuthConfig
    RateLimit     RateLimitConfig
    StateManager  state.StateManager
    Logger        Logger
    DevMode       bool           // 开发模式（跳过认证、开放 CORS）
    ReadTimeout   time.Duration  // 默认 30s
    WriteTimeout  time.Duration  // 默认 300s（长任务）
    IdleTimeout   time.Duration  // 默认 120s
    MaxHeaderBytes int            // 默认 1MB
}

// Logger 日志接口
type Logger interface {
    Info(msg string, fields ...Field)
    Warn(msg string, fields ...Field)
    Error(msg string, fields ...Field)
    Debug(msg string, fields ...Field)
}

// Field 结构化日志字段
type Field struct {
    Key   string
    Value interface{}
}
```

### 7.4 Server 启动

```go
package api

// Server HTTP 服务器
type Server struct {
    engine *gin.Engine
    cfg    ServerConfig
}

// NewServer 构造服务器
func NewServer(cfg ServerConfig) *Server

// RegisterHandlers 注册所有 handler（由 main 调用）
func (s *Server) RegisterHandlers(deps HandlerDeps)

// Start 启动（阻塞）
func (s *Server) Start() error

// Shutdown 优雅关闭
func (s *Server) Shutdown(ctx context.Context) error
```

### 7.5 HandlerDeps

```go
package api

// HandlerDeps 所有 handler 的依赖
type HandlerDeps struct {
    Pipeline         pipeline.Service
    StateManager     state.StateManager
    PolisherAgent    agents.PolisherAgent
    SchedulerService scheduler.Service
    LLMRegistry      llm.Registry
    Importer         pipeline.Importer
    ShortFictionSvc  shortfiction.Service
    IdempotencyStore IdempotencyStore
    Logger           Logger
}
```
