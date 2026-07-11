package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rffanlab/yellowbullNovel/backend/internal/config"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
	"go.uber.org/zap"
)

// SchedulerConfig 调度器配置
type SchedulerConfig struct {
	WriteCron          string // cron 表达式（简化版：仅解析为间隔毫秒）
	MaxConcurrentBooks int    // 最大并发书籍数
	ChaptersPerCycle   int    // 每次循环每书写几章
	MaxChaptersPerDay  int    // 每日全局章数上限
	CooldownMs         int    // 章节间冷却毫秒
	FailureThreshold   int    // 连续失败暂停阈值
}

// SchedulerStatus 调度器状态
type SchedulerStatus struct {
	Running         bool       `json:"running"`
	NextRunAt       *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt       *time.Time `json:"lastRunAt,omitempty"`
	LastRunStatus   string     `json:"lastRunStatus,omitempty"` // success/failed/running
	TotalRuns       int        `json:"totalRuns"`
	FailedRuns      int        `json:"failedRuns"`
	TotalChapters   int        `json:"totalChapters"`
	DailyChapters   int        `json:"dailyChapters"`
	DailyResetAt    *time.Time `json:"dailyResetAt,omitempty"`
	PausedBooks     []string   `json:"pausedBooks,omitempty"`
}

// bookState 单本书的调度状态
type bookState struct {
	consecutiveFailures int
	lastWriteAt         time.Time
	paused              bool
}

// Scheduler 定时调度器
type Scheduler struct {
	pipeline *PipelineRunner
	config   SchedulerConfig
	logger   *zap.Logger

	stopCh chan struct{}
	running atomic.Bool

	// 状态追踪
	mu            sync.Mutex
	writeTicker   *time.Ticker
	lastRunAt     time.Time
	nextRunAt     time.Time
	totalRuns     int64
	failedRuns    int64
	totalChapters int64
	dailyChapters int64
	dailyResetAt  time.Time
	bookStates    map[string]*bookState
}

// NewScheduler 创建调度器
func NewScheduler(pipeline *PipelineRunner, cfg SchedulerConfig, logger *zap.Logger) *Scheduler {
	if cfg.MaxConcurrentBooks <= 0 {
		cfg.MaxConcurrentBooks = 3
	}
	if cfg.ChaptersPerCycle <= 0 {
		cfg.ChaptersPerCycle = 1
	}
	if cfg.MaxChaptersPerDay <= 0 {
		cfg.MaxChaptersPerDay = 10
	}
	if cfg.CooldownMs <= 0 {
		cfg.CooldownMs = 30000
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.WriteCron == "" {
		cfg.WriteCron = "0 */2 * * *"
	}

	return &Scheduler{
		pipeline:   pipeline,
		config:     cfg,
		logger:     logger,
		stopCh:     make(chan struct{}),
		bookStates: make(map[string]*bookState),
		dailyResetAt: time.Now().Add(24 * time.Hour),
	}
}

// SchedulerConfigFromAppConfig 从应用配置构建调度器配置
func SchedulerConfigFromAppConfig(cfg config.SchedulerConfig) SchedulerConfig {
	return SchedulerConfig{
		WriteCron:          cfg.WriteCron,
		MaxConcurrentBooks: cfg.MaxConcurrentBooks,
		ChaptersPerCycle:   cfg.ChaptersPerCycle,
		MaxChaptersPerDay:  cfg.MaxChaptersPerDay,
		CooldownMs:         cfg.CooldownMs,
		FailureThreshold:   3,
	}
}

// Start 启动调度器
func (s *Scheduler) Start() error {
	if s.running.Load() {
		return nil // 幂等
	}
	s.running.Store(true)
	s.stopCh = make(chan struct{})

	interval := s.cronToInterval()
	s.writeTicker = time.NewTicker(interval)

	s.logInfo("调度器启动",
		zap.Duration("interval", interval),
		zap.Int("maxConcurrentBooks", s.config.MaxConcurrentBooks),
		zap.Int("chaptersPerCycle", s.config.ChaptersPerCycle),
	)

	// 立即触发一次写作循环
	go s.runWriteCycle(context.Background())

	// 启动定时循环
	go s.writeLoop()

	return nil
}

// Stop 停止调度器
func (s *Scheduler) Stop() {
	if !s.running.Load() {
		return
	}
	s.running.Store(false)
	close(s.stopCh)
	if s.writeTicker != nil {
		s.writeTicker.Stop()
	}
	s.logInfo("调度器已停止")
}

// Status 返回调度器状态
func (s *Scheduler) Status() SchedulerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	var pausedBooks []string
	for bookID, bs := range s.bookStates {
		if bs.paused {
			pausedBooks = append(pausedBooks, bookID)
		}
	}

	status := SchedulerStatus{
		Running:       s.running.Load(),
		LastRunAt:     nil,
		NextRunAt:     nil,
		TotalRuns:     int(s.totalRuns),
		FailedRuns:    int(s.failedRuns),
		TotalChapters: int(s.totalChapters),
		DailyChapters: int(s.dailyChapters),
		PausedBooks:   pausedBooks,
	}

	if !s.lastRunAt.IsZero() {
		status.LastRunAt = &s.lastRunAt
	}
	if !s.nextRunAt.IsZero() {
		status.NextRunAt = &s.nextRunAt
	}
	if !s.dailyResetAt.IsZero() {
		status.DailyResetAt = &s.dailyResetAt
	}

	return status
}

// ResumeBook 恢复被暂停的书
func (s *Scheduler) ResumeBook(bookID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bs, ok := s.bookStates[bookID]; ok {
		bs.paused = false
		bs.consecutiveFailures = 0
		s.logInfo("恢复书籍调度", zap.String("bookId", bookID))
	}
}

// writeLoop 定时写作循环
func (s *Scheduler) writeLoop() {
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.writeTicker.C:
			s.runWriteCycle(context.Background())
		}
	}
}

// runWriteCycle 执行一次写作循环
func (s *Scheduler) runWriteCycle(ctx context.Context) {
	s.mu.Lock()
	s.lastRunAt = time.Now()
	s.totalRuns++
	s.mu.Unlock()

	s.logInfo("写作循环开始")

	// 检查日章数上限
	if s.isDailyCapReached() {
		s.logInfo("达到每日章数上限，跳过本轮")
		return
	}

	// 列出所有活跃书籍
	books, err := s.pipeline.state.ListBooks()
	if err != nil {
		s.logWarn("列出书籍失败", zap.Error(err))
		s.mu.Lock()
		s.failedRuns++
		s.mu.Unlock()
		return
	}

	// 过滤活跃书籍
	var activeBooks []models.BookConfig
	for _, b := range books {
		if b.Status == models.BookStatusActive {
			// 跳过被暂停的书
			s.mu.Lock()
			bs, ok := s.bookStates[b.ID]
			s.mu.Unlock()
			if ok && bs.paused {
				continue
			}
			activeBooks = append(activeBooks, b)
		}
	}

	// 限制并发数
	if len(activeBooks) > s.config.MaxConcurrentBooks {
		activeBooks = activeBooks[:s.config.MaxConcurrentBooks]
	}

	if len(activeBooks) == 0 {
		s.logInfo("没有活跃书籍需要写作")
		return
	}

	// 并行处理多本书
	var wg sync.WaitGroup
	for _, book := range activeBooks {
		wg.Add(1)
		go func(b models.BookConfig) {
			defer wg.Done()
			s.processBook(ctx, b.ID)
		}(book)
	}
	wg.Wait()

	s.logInfo("写作循环完成")
}

// processBook 处理单本书的多章写作
func (s *Scheduler) processBook(ctx context.Context, bookID string) {
	for i := 0; i < s.config.ChaptersPerCycle; i++ {
		// 检查日上限
		if s.isDailyCapReached() {
			s.logInfo("达到每日章数上限，停止本书写作", zap.String("bookId", bookID))
			return
		}

		// 检查书是否被暂停
		s.mu.Lock()
		bs := s.getOrCreateBookState(bookID)
		if bs.paused {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()

		// 章节间冷却
		if i > 0 {
			time.Sleep(time.Duration(s.config.CooldownMs) * time.Millisecond)
		}

		// 写一章
		s.writeOneChapter(ctx, bookID)
	}
}

// writeOneChapter 写一章并处理结果
func (s *Scheduler) writeOneChapter(ctx context.Context, bookID string) {
	s.logInfo("自动写章", zap.String("bookId", bookID))

	result, err := s.pipeline.WriteNextChapter(bookID, 0, 0)
	if err != nil {
		s.logWarn("写章失败", zap.String("bookId", bookID), zap.Error(err))
		s.handleAuditFailure(bookID, nil)
		return
	}

	s.mu.Lock()
	s.totalChapters++
	s.dailyChapters++
	s.mu.Unlock()

	// 判定结果
	if result.Status == models.ChapterStatusReadyForReview || result.AuditResult.Passed {
		// 成功：清零失败计数
		s.mu.Lock()
		bs := s.getOrCreateBookState(bookID)
		bs.consecutiveFailures = 0
		bs.lastWriteAt = time.Now()
		s.mu.Unlock()
		s.logInfo("写章成功",
			zap.String("bookId", bookID),
			zap.Int("chapter", result.ChapterNumber),
			zap.Int("score", scoreFromResult(result)),
		)
	} else {
		// 失败
		s.handleAuditFailure(bookID, result)
	}
}

// handleAuditFailure 处理审计失败
func (s *Scheduler) handleAuditFailure(bookID string, result *models.ChapterPipelineResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	bs := s.getOrCreateBookState(bookID)
	bs.consecutiveFailures++

	s.logWarn("章节审计失败",
		zap.String("bookId", bookID),
		zap.Int("consecutiveFailures", bs.consecutiveFailures),
	)

	if result != nil && len(result.AuditResult.Issues) > 0 {
		// 失败维度聚类
		dimCounts := make(map[string]int)
		for _, issue := range result.AuditResult.Issues {
			if issue.Severity == models.SeverityCritical || issue.Severity == models.SeverityWarning {
				dimCounts[issue.Category]++
			}
		}
		for dim, count := range dimCounts {
			if count >= 3 {
				s.logWarn("失败维度聚类告警",
					zap.String("bookId", bookID),
					zap.String("dimension", dim),
					zap.Int("count", count),
				)
			}
		}
	}

	// 超过阈值 → 暂停
	if bs.consecutiveFailures >= s.config.FailureThreshold {
		bs.paused = true
		s.logWarn("连续失败超阈值，暂停书籍调度",
			zap.String("bookId", bookID),
			zap.Int("failures", bs.consecutiveFailures),
			zap.Int("threshold", s.config.FailureThreshold),
		)
	}
}

// getOrCreateBookState 获取或创建书籍状态（调用方需持有锁）
func (s *Scheduler) getOrCreateBookState(bookID string) *bookState {
	if bs, ok := s.bookStates[bookID]; ok {
		return bs
	}
	bs := &bookState{}
	s.bookStates[bookID] = bs
	return bs
}

// isDailyCapReached 检查是否达到每日章数上限
func (s *Scheduler) isDailyCapReached() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查是否需要重置日计数
	now := time.Now()
	if now.After(s.dailyResetAt) {
		s.dailyChapters = 0
		s.dailyResetAt = now.Add(24 * time.Hour)
	}

	return s.dailyChapters >= int64(s.config.MaxChaptersPerDay)
}

// cronToInterval 将 cron 表达式简化为时间间隔
// 支持格式：解析失败则默认 2 小时
func (s *Scheduler) cronToInterval() time.Duration {
	// 简化版：从配置中读取 cron 字符串，尝试解析常见格式
	// 默认 2 小时
	interval := 2 * time.Hour

	// 尝试解析 "0 */N * * *" 格式
	cron := s.config.WriteCron
	if len(cron) >= 8 && cron[:2] == "0 " {
		// 尝试提取小时间隔
		var hours int
		if _, err := fmt.Sscanf(cron, "0 */%d * * *", &hours); err == nil && hours > 0 {
			interval = time.Duration(hours) * time.Hour
		}
	}

	// 如果配置了 CooldownMs 且小于 interval，用它作为最小间隔
	return interval
}

// scoreFromResult 从结果中提取分数
func scoreFromResult(result *models.ChapterPipelineResult) int {
	if result.AuditResult.OverallScore != nil {
		return *result.AuditResult.OverallScore
	}
	return 0
}

// ============================================================================
// 日志辅助
// ============================================================================

func (s *Scheduler) logInfo(msg string, fields ...zap.Field) {
	if s.logger != nil {
		s.logger.Info("[scheduler] "+msg, fields...)
	}
}

func (s *Scheduler) logWarn(msg string, fields ...zap.Field) {
	if s.logger != nil {
		s.logger.Warn("[scheduler] "+msg, fields...)
	}
}
