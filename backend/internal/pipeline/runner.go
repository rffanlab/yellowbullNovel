package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rffanlab/yellowbullNovel/backend/internal/agents"
	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
	"github.com/rffanlab/yellowbullNovel/backend/internal/state"
	"go.uber.org/zap"
)

// PipelineConfig 流水线配置
type PipelineConfig struct {
	Client               llm.LLMClient
	ProjectRoot          string
	StateManager         *state.StateManager
	ChapterReviewMode    string // auto / manual
	RevisionGate         string // strict / lenient / always
	MaxReviewIterations  int
	FoundationReviewRetries int
	Logger               *zap.Logger
	OnStreamProgress     llm.OnStreamProgress
}

// PipelineRunner 核心写作流水线编排器
type PipelineRunner struct {
	state  *state.StateManager
	config PipelineConfig
}

// DraftResult 仅写草稿的结果
type DraftResult struct {
	ChapterNumber int             `json:"chapterNumber"`
	Title         string          `json:"title"`
	WordCount     int             `json:"wordCount"`
	FilePath      string          `json:"filePath"`
	TokenUsage    *models.TokenUsage `json:"tokenUsage,omitempty"`
}

// ReviseResult 回炉重写结果
type ReviseResult struct {
	ChapterNumber int        `json:"chapterNumber"`
	WordCount     int        `json:"wordCount"`
	FixedIssues   []string   `json:"fixedIssues"`
	Applied       bool       `json:"applied"`
	Status        string     `json:"status"`
	TokenUsage    *models.TokenUsage `json:"tokenUsage,omitempty"`
}

// NewPipelineRunner 创建流水线编排器
func NewPipelineRunner(cfg PipelineConfig) *PipelineRunner {
	if cfg.MaxReviewIterations <= 0 {
		cfg.MaxReviewIterations = 1
	}
	if cfg.FoundationReviewRetries <= 0 {
		cfg.FoundationReviewRetries = 2
	}
	if cfg.ChapterReviewMode == "" {
		cfg.ChapterReviewMode = "auto"
	}
	if cfg.RevisionGate == "" {
		cfg.RevisionGate = "strict"
	}
	return &PipelineRunner{
		state:  cfg.StateManager,
		config: cfg,
	}
}

// zapLoggerAdapter 将 *zap.Logger 适配为 llm.Logger 接口
type zapLoggerAdapter struct {
	logger *zap.Logger
}

func (z zapLoggerAdapter) Info(msg string, fields ...any) {
	z.logger.Info(msg, toZapFields(fields)...)
}

func (z zapLoggerAdapter) Warn(msg string, fields ...any) {
	z.logger.Warn(msg, toZapFields(fields)...)
}

func (z zapLoggerAdapter) Error(msg string, fields ...any) {
	z.logger.Error(msg, toZapFields(fields)...)
}

func (z zapLoggerAdapter) Debug(msg string, fields ...any) {
	z.logger.Debug(msg, toZapFields(fields)...)
}

func toZapFields(fields []any) []zap.Field {
	zf := make([]zap.Field, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			continue
		}
		val := fields[i+1]
		switch v := val.(type) {
		case string:
			zf = append(zf, zap.String(key, v))
		case int:
			zf = append(zf, zap.Int(key, v))
		case int64:
			zf = append(zf, zap.Int64(key, v))
		case float64:
			zf = append(zf, zap.Float64(key, v))
		case bool:
			zf = append(zf, zap.Bool(key, v))
		case error:
			zf = append(zf, zap.Error(v))
		default:
			zf = append(zf, zap.Any(key, v))
		}
	}
	return zf
}

// agentCtx 构建传递给各 Agent 的上下文
func (r *PipelineRunner) agentCtx() llm.AgentContext {
	var logger llm.Logger
	if r.config.Logger != nil {
		logger = zapLoggerAdapter{logger: r.config.Logger}
	} else {
		logger = llm.NoopLogger()
	}
	return llm.AgentContext{
		Client:      r.config.Client,
		ProjectRoot: r.config.ProjectRoot,
		Logger:      logger,
	}
}

// resolveLanguage 解析书籍语言
func resolveLanguage(book *models.BookConfig) string {
	if book.Language != "" {
		return book.Language
	}
	return "zh"
}

// buildLengthSpec 构建字数规格
func buildLengthSpec(target int, language string) models.LengthSpec {
	countingMode := "zh_chars"
	if language == "en" {
		countingMode = "en_words"
	}
	if target <= 0 {
		target = 3000
	}
	min := target * 85 / 100
	max := target * 120 / 100
	hardMin := target * 70 / 100
	hardMax := target * 150 / 100
	_ = hardMin
	_ = hardMax
	return models.LengthSpec{
		Target:       target,
		Min:          min,
		Max:          max,
		CountingMode: countingMode,
	}
}

// countLength 按计数模式计算字数
func countLength(content string, spec models.LengthSpec) int {
	runes := []rune(content)
	if spec.CountingMode == "en_words" {
		return len(strings.Fields(content))
	}
	return len(runes)
}

// ============================================================================
// InitBook 建书骨架
// ============================================================================

// InitBook 初始化一本书：Architect生成基础设定 → FoundationReviewer审查 → 落盘 → 初始化控制文档 → 快照
func (r *PipelineRunner) InitBook(book *models.BookConfig, externalContext, authorIntent string) error {
	if book.ID == "" {
		book.ID = uuid.NewString()
	}
	if book.Status == "" {
		book.Status = models.BookStatusDraft
	}

	language := resolveLanguage(book)
	actx := r.agentCtx()
	architect := agents.NewArchitectAgent(actx)
	reviewer := agents.NewFoundationReviewerAgent(actx)

	r.logInfo("InitBook: 生成基础设定", zap.String("bookId", book.ID), zap.String("title", book.Title))

	// 生成 + 审查 + 重试循环
	foundation, err := r.generateAndReviewFoundation(context.Background(), architect, reviewer, book, externalContext, language)
	if err != nil {
		return fmt.Errorf("InitBook: generate foundation: %w", err)
	}

	// 创建目录结构
	if err := r.state.EnsureBookDirs(book.ID); err != nil {
		return fmt.Errorf("InitBook: ensure dirs: %w", err)
	}

	bookDir := r.state.BookDir(book.ID)

	// 落盘基础设定文件
	r.logInfo("InitBook: 写入基础设定文件", zap.String("bookId", book.ID))
	if err := architect.WriteFoundationFiles(bookDir, foundation); err != nil {
		return fmt.Errorf("InitBook: write foundation files: %w", err)
	}

	// 写入 brief.md（外部指令）
	if strings.TrimSpace(externalContext) != "" {
		briefPath := filepath.Join(bookDir, "story", "brief.md")
		if err := state.WriteFile(briefPath, externalContext); err != nil {
			return fmt.Errorf("InitBook: write brief: %w", err)
		}
	}

	// 初始化控制文档
	r.logInfo("InitBook: 初始化控制文档", zap.String("bookId", book.ID))
	if err := r.state.EnsureControlDocuments(book.ID, language, authorIntent); err != nil {
		return fmt.Errorf("InitBook: ensure control docs: %w", err)
	}

	// 保存书籍配置到数据库
	book.Status = models.BookStatusActive
	if err := r.state.SaveBookConfig(book); err != nil {
		return fmt.Errorf("InitBook: save book config: %w", err)
	}

	// 创建初始快照
	r.logInfo("InitBook: 创建初始快照", zap.String("bookId", book.ID))
	if err := r.state.SnapshotState(book.ID, 0); err != nil {
		r.logWarn("InitBook: snapshot failed (non-fatal)", zap.Error(err))
	}

	r.logInfo("InitBook: 完成", zap.String("bookId", book.ID))
	return nil
}

// generateAndReviewFoundation 生成基础设定并审查，未通过则带反馈重试
func (r *PipelineRunner) generateAndReviewFoundation(
	ctx context.Context,
	architect *agents.ArchitectAgent,
	reviewer *agents.FoundationReviewerAgent,
	book *models.BookConfig,
	externalContext string,
	language string,
) (*models.ArchitectOutput, error) {
	var reviewFeedback string
	maxRetries := r.config.FoundationReviewRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}

	var lastFoundation *models.ArchitectOutput
	var lastReview *models.FoundationReviewResult

	for attempt := 0; attempt <= maxRetries; attempt++ {
		r.logInfo("InitBook: 生成基础设定",
			zap.Int("attempt", attempt+1),
			zap.Int("maxAttempts", maxRetries+1),
		)

		foundation, err := architect.GenerateFoundation(ctx, *book, externalContext, reviewFeedback)
		if err != nil {
			return nil, fmt.Errorf("architect generate (attempt %d): %w", attempt+1, err)
		}
		lastFoundation = foundation

		r.logInfo("InitBook: 审查基础设定", zap.Int("attempt", attempt+1))
		review, err := reviewer.Review(ctx, foundation, "original", language)
		if err != nil {
			r.logWarn("InitBook: review failed, accepting foundation", zap.Error(err))
			return foundation, nil
		}
		lastReview = review

		r.logInfo("InitBook: 审查结果",
			zap.Int("score", review.TotalScore),
			zap.Bool("passed", review.Passed),
		)

		if review.Passed {
			return foundation, nil
		}

		// 未通过，构建反馈进入下一轮
		reviewFeedback = agents.FormatReviewFeedback(review, language)
	}

	// 超过重试次数，使用最后一次的结果（降级接受）
	r.logWarn("InitBook: foundation review not passed after retries, accepting last version",
		zap.Int("finalScore", lastReview.TotalScore),
	)
	return lastFoundation, nil
}

// ============================================================================
// WriteNextChapter 写下一章（完整流水线）
// ============================================================================

// WriteNextChapter 完整写章流水线：
// 获取锁 → prepareWriteInput(Plan+Compose) → Writer写草稿 → ChapterReviewCycle → 伏笔晋升 → 持久化 → 状态校验 → 释放锁
func (r *PipelineRunner) WriteNextChapter(bookID string, wordCount int, temperatureOverride float64) (*models.ChapterPipelineResult, error) {
	releaseLock := r.state.AcquireBookLock(bookID)
	defer releaseLock()

	return r.writeNextChapterLocked(context.Background(), bookID, wordCount, temperatureOverride)
}

func (r *PipelineRunner) writeNextChapterLocked(ctx context.Context, bookID string, wordCount int, temperatureOverride float64) (*models.ChapterPipelineResult, error) {
	// 确保控制文档存在
	if err := r.state.EnsureControlDocuments(bookID, "zh", ""); err != nil {
		return nil, fmt.Errorf("ensure control docs: %w", err)
	}

	book, err := r.state.LoadBookConfig(bookID)
	if err != nil {
		return nil, fmt.Errorf("load book config: %w", err)
	}

	language := resolveLanguage(book)
	bookDir := r.state.BookDir(bookID)

	chapterNumber, err := r.state.GetNextChapterNumber(bookID)
	if err != nil {
		return nil, fmt.Errorf("get next chapter number: %w", err)
	}

	r.logInfo("WriteNextChapter: 开始",
		zap.String("bookId", bookID),
		zap.Int("chapter", chapterNumber),
	)

	// 准备写章输入（Plan + Compose）
	r.logInfo("WriteNextChapter: 准备章节输入", zap.Int("chapter", chapterNumber))
	writeInput, err := r.prepareWriteInput(ctx, book, bookDir, chapterNumber, "")
	if err != nil {
		return nil, fmt.Errorf("prepare write input: %w", err)
	}

	// 构建字数规格
	targetWordCount := wordCount
	if targetWordCount <= 0 {
		targetWordCount = book.ChapterWordCount
	}
	lengthSpec := buildLengthSpec(targetWordCount, language)

	// Writer 写草稿
	r.logInfo("WriteNextChapter: 撰写章节草稿", zap.Int("chapter", chapterNumber))
	writer := agents.NewWriterAgent(r.agentCtx())

	writeChapterInput := &models.WriteChapterInput{
		Book:               *book,
		BookDir:            bookDir,
		ChapterNumber:      chapterNumber,
		ChapterIntent:      writeInput.chapterIntent,
		ChapterMemo:        writeInput.chapterMemo,
		ChapterIntentData:  writeInput.chapterIntentData,
		ContextPackage:     writeInput.contextPackage,
		RuleStack:          writeInput.ruleStack,
		LengthSpec:         &lengthSpec,
	}
	if wordCount > 0 {
		wc := wordCount
		writeChapterInput.WordCountOverride = &wc
	}
	if temperatureOverride > 0 {
		temp := temperatureOverride
		writeChapterInput.TemperatureOverride = &temp
	}

	output, err := writer.WriteChapter(ctx, writeChapterInput)
	if err != nil {
		return nil, fmt.Errorf("writer write chapter: %w", err)
	}

	// 累计 token usage
	totalUsage := &models.TokenUsage{}
	if output.TokenUsage != nil {
		totalUsage.PromptTokens = output.TokenUsage.PromptTokens
		totalUsage.CompletionTokens = output.TokenUsage.CompletionTokens
		totalUsage.TotalTokens = output.TokenUsage.TotalTokens
	}

	var finalContent string
	var finalWordCount int
	var revised bool
	var auditResult models.AuditResult
	var normalizeApplied bool

	// 章节审查循环
	if r.config.ChapterReviewMode == "manual" {
		// 手动模式：写完即停，不自动审查
		r.logInfo("WriteNextChapter: 手动审查模式，写完即停", zap.Int("chapter", chapterNumber))
		finalContent = output.Content
		finalWordCount = countLength(finalContent, lengthSpec)
		revised = false
		auditResult = models.AuditResult{
			Passed:  false,
			Issues:  []models.AuditIssue{},
			Summary: "尚未审查（手动模式：写完即停）",
		}
	} else {
		// 自动模式：审计 → 修订 → 重审
		r.logInfo("WriteNextChapter: 执行章节审查循环", zap.Int("chapter", chapterNumber))
		auditor := agents.NewContinuityAuditor(r.agentCtx())

		cycleResult, err := RunChapterReviewCycle(ctx, ReviewCycleParams{
			BookDir:       bookDir,
			ChapterNumber: chapterNumber,
			Genre:         book.Genre,
			InitialContent: output.Content,
			InitialWordCount: output.WordCount,
			LengthSpec:    &lengthSpec,
			PostWriteErrors: output.PostWriteErrors,
			Auditor:       auditor,
			CreateReviser: func() *agents.ReviserAgent {
				return agents.NewReviserAgent(r.agentCtx())
			},
			MaxReviewIterations: r.config.MaxReviewIterations,
			Logger:              r.config.Logger,
		})
		if err != nil {
			r.logWarn("WriteNextChapter: review cycle failed, using raw draft", zap.Error(err))
			finalContent = output.Content
			finalWordCount = output.WordCount
			auditResult = models.AuditResult{
				Passed:  false,
				Summary: fmt.Sprintf("审查循环失败: %v", err),
			}
		} else {
			finalContent = cycleResult.FinalContent
			finalWordCount = cycleResult.FinalWordCount
			revised = cycleResult.Revised
			auditResult = cycleResult.AuditResult
			normalizeApplied = cycleResult.NormalizeApplied
			if cycleResult.TotalUsage != nil {
				totalUsage.Add(cycleResult.TotalUsage)
			}
		}
	}

	_ = normalizeApplied

	// 持久化章节文件
	r.logInfo("WriteNextChapter: 落盘章节", zap.Int("chapter", chapterNumber))
	chapterContent := fmt.Sprintf("# %s\n\n%s", output.Title, finalContent)
	chapterPath := r.state.ChapterFilePath(bookID, chapterNumber)
	if err := state.WriteFile(chapterPath, chapterContent); err != nil {
		return nil, fmt.Errorf("write chapter file: %w", err)
	}

	// 持久化 truth files（状态结算输出）
	if output.UpdatedState != "" {
		statePath := filepath.Join(bookDir, "story", "current_state.md")
		_ = state.WriteFile(statePath, output.UpdatedState)
	}
	if output.UpdatedHooks != "" {
		hooksPath := filepath.Join(bookDir, "story", "pending_hooks.md")
		_ = state.WriteFile(hooksPath, output.UpdatedHooks)
	}
	if output.ChapterSummary != "" {
		summariesPath := filepath.Join(bookDir, "story", "chapter_summaries.md")
		existing := state.ReadFileOrDefault(summariesPath, "")
		var newContent string
		if existing != "" {
			newContent = existing + "\n" + output.ChapterSummary
		} else {
			newContent = "# 章节摘要\n\n| 章节 | 标题 | 摘要 | 字数 | 状态 |\n|------|------|------|------|------|\n" + output.ChapterSummary
		}
		_ = state.WriteFile(summariesPath, newContent)
	}
	if output.UpdatedSubplots != "" {
		_ = state.WriteFile(filepath.Join(bookDir, "story", "subplot_board.md"), output.UpdatedSubplots)
	}
	if output.UpdatedEmotionalArcs != "" {
		_ = state.WriteFile(filepath.Join(bookDir, "story", "emotional_arcs.md"), output.UpdatedEmotionalArcs)
	}
	if output.UpdatedCharacterMatrix != "" {
		_ = state.WriteFile(filepath.Join(bookDir, "story", "character_matrix.md"), output.UpdatedCharacterMatrix)
	}

	// 保存章节元数据
	status := models.ChapterStatusReadyForReview
	if !auditResult.Passed {
		status = models.ChapterStatusAuditFailed
	}
	score := 0
	if auditResult.OverallScore != nil {
		score = *auditResult.OverallScore
	}
	chapterMeta := &models.ChapterMeta{
		BookID:     bookID,
		Number:     chapterNumber,
		Title:      output.Title,
		WordCount:  finalWordCount,
		Status:     status,
		AuditScore: &score,
		Revised:    revised,
		FilePath:   chapterPath,
	}
	if err := r.state.SaveChapterMeta(chapterMeta); err != nil {
		r.logWarn("WriteNextChapter: save chapter meta failed", zap.Error(err))
	}

	// 状态快照
	if err := r.state.SnapshotState(bookID, chapterNumber); err != nil {
		r.logWarn("WriteNextChapter: snapshot failed (non-fatal)", zap.Error(err))
	}

	// 状态校验
	r.logInfo("WriteNextChapter: 状态校验", zap.Int("chapter", chapterNumber))
	validator := agents.NewStateValidatorAgent(r.agentCtx())
	oldState := "" // 已经被覆盖了，这里简化
	newState := state.ReadFileOrDefault(filepath.Join(bookDir, "story", "current_state.md"), "")
	oldHooks := ""
	newHooks := state.ReadFileOrDefault(filepath.Join(bookDir, "story", "pending_hooks.md"), "")
	validationResult, err := validator.Validate(ctx, bookDir, finalContent, chapterNumber, oldState, newState, oldHooks, newHooks, language)
	if err != nil {
		r.logWarn("WriteNextChapter: state validation failed (non-fatal)", zap.Error(err))
	} else if !validationResult.Passed {
		r.logWarn("WriteNextChapter: state validation detected contradictions", zap.Int("warnings", len(validationResult.Warnings)))
		// 标记为 state-degraded
		chapterMeta.Status = models.ChapterStatusStateDegraded
		_ = r.state.SaveChapterMeta(chapterMeta)
	}

	// 构建返回结果
	result := &models.ChapterPipelineResult{
		ChapterNumber: chapterNumber,
		Title:         output.Title,
		WordCount:     finalWordCount,
		AuditResult:   auditResult,
		Revised:       revised,
		Status:        status,
		TokenUsage:    totalUsage,
	}

	// 字数遥测
	telemetry := &models.LengthTelemetry{
		LengthSpec:  lengthSpec,
		WriterCount: output.WordCount,
		FinalCount:  finalWordCount,
	}
	result.LengthTelemetry = telemetry

	r.logInfo("WriteNextChapter: 完成",
		zap.String("bookId", bookID),
		zap.Int("chapter", chapterNumber),
		zap.Int("wordCount", finalWordCount),
		zap.Bool("revised", revised),
		zap.Int("score", score),
	)

	return result, nil
}

// writeInputHolder 内部传递的写章输入
type writeInputHolder struct {
	chapterIntent     string
	chapterMemo       *models.ChapterMemo
	chapterIntentData *models.ChapterIntent
	contextPackage    *models.ContextPackage
	ruleStack         *models.RuleStack
}

// prepareWriteInput 执行 Plan + Compose，组装写章输入
func (r *PipelineRunner) prepareWriteInput(ctx context.Context, book *models.BookConfig, bookDir string, chapterNumber int, externalContext string) (*writeInputHolder, error) {
	// Plan
	planner := agents.NewPlannerAgent(r.agentCtx())
	planOutput, err := planner.PlanChapter(ctx, &agents.PlanChapterInput{
		Book:            book,
		BookDir:         bookDir,
		ChapterNumber:   chapterNumber,
		ExternalContext: externalContext,
	})
	if err != nil {
		return nil, fmt.Errorf("planner: %w", err)
	}

	// Compose
	composer := agents.NewComposerAgent(r.agentCtx())
	composeOutput, err := composer.ComposeChapter(ctx, &agents.ComposeChapterInput{
		Book:          book,
		BookDir:       bookDir,
		ChapterNumber: chapterNumber,
		Plan:          planOutput,
	})
	if err != nil {
		return nil, fmt.Errorf("composer: %w", err)
	}

	intentCopy := planOutput.Intent
	return &writeInputHolder{
		chapterIntent:     planOutput.IntentMarkdown,
		chapterMemo:       &planOutput.Memo,
		chapterIntentData: &intentCopy,
		contextPackage:    &composeOutput.ContextPackage,
		ruleStack:         &composeOutput.RuleStack,
	}, nil
}

// ============================================================================
// WriteDraft 仅写草稿
// ============================================================================

func (r *PipelineRunner) WriteDraft(bookID string, externalContext string, wordCount int) (*DraftResult, error) {
	releaseLock := r.state.AcquireBookLock(bookID)
	defer releaseLock()

	ctx := context.Background()
	book, err := r.state.LoadBookConfig(bookID)
	if err != nil {
		return nil, fmt.Errorf("load book config: %w", err)
	}

	language := resolveLanguage(book)
	bookDir := r.state.BookDir(bookID)
	chapterNumber, err := r.state.GetNextChapterNumber(bookID)
	if err != nil {
		return nil, fmt.Errorf("get next chapter number: %w", err)
	}

	r.logInfo("WriteDraft: 准备输入", zap.String("bookId", bookID), zap.Int("chapter", chapterNumber))

	writeInput, err := r.prepareWriteInput(ctx, book, bookDir, chapterNumber, externalContext)
	if err != nil {
		return nil, fmt.Errorf("prepare write input: %w", err)
	}

	targetWordCount := wordCount
	if targetWordCount <= 0 {
		targetWordCount = book.ChapterWordCount
	}
	lengthSpec := buildLengthSpec(targetWordCount, language)

	writer := agents.NewWriterAgent(r.agentCtx())
	input := &models.WriteChapterInput{
		Book:               *book,
		BookDir:            bookDir,
		ChapterNumber:      chapterNumber,
		ChapterIntent:      writeInput.chapterIntent,
		ChapterMemo:        writeInput.chapterMemo,
		ChapterIntentData:  writeInput.chapterIntentData,
		ContextPackage:     writeInput.contextPackage,
		RuleStack:          writeInput.ruleStack,
		LengthSpec:         &lengthSpec,
	}
	if wordCount > 0 {
		wc := wordCount
		input.WordCountOverride = &wc
	}

	output, err := writer.WriteChapter(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("writer: %w", err)
	}

	// 落盘草稿
	chapterContent := fmt.Sprintf("# %s\n\n%s", output.Title, output.Content)
	chapterPath := r.state.ChapterFilePath(bookID, chapterNumber)
	if err := state.WriteFile(chapterPath, chapterContent); err != nil {
		return nil, fmt.Errorf("write chapter file: %w", err)
	}

	// 保存元数据
	chapterMeta := &models.ChapterMeta{
		BookID:    bookID,
		Number:    chapterNumber,
		Title:     output.Title,
		WordCount: output.WordCount,
		Status:    models.ChapterStatusDraft,
		FilePath:  chapterPath,
	}
	if err := r.state.SaveChapterMeta(chapterMeta); err != nil {
		r.logWarn("WriteDraft: save chapter meta failed", zap.Error(err))
	}

	return &DraftResult{
		ChapterNumber: chapterNumber,
		Title:         output.Title,
		WordCount:     output.WordCount,
		FilePath:      chapterPath,
		TokenUsage:    output.TokenUsage,
	}, nil
}

// ============================================================================
// PlanChapter 仅规划
// ============================================================================

func (r *PipelineRunner) PlanChapter(bookID string, externalContext string) (*models.PlanChapterOutput, error) {
	book, err := r.state.LoadBookConfig(bookID)
	if err != nil {
		return nil, fmt.Errorf("load book config: %w", err)
	}

	bookDir := r.state.BookDir(bookID)
	chapterNumber, err := r.state.GetNextChapterNumber(bookID)
	if err != nil {
		return nil, fmt.Errorf("get next chapter number: %w", err)
	}

	planner := agents.NewPlannerAgent(r.agentCtx())
	return planner.PlanChapter(context.Background(), &agents.PlanChapterInput{
		Book:            book,
		BookDir:         bookDir,
		ChapterNumber:   chapterNumber,
		ExternalContext: externalContext,
	})
}

// ============================================================================
// ComposeChapter 仅组装上下文
// ============================================================================

func (r *PipelineRunner) ComposeChapter(bookID string, externalContext string) (*models.ComposeChapterOutput, error) {
	book, err := r.state.LoadBookConfig(bookID)
	if err != nil {
		return nil, fmt.Errorf("load book config: %w", err)
	}

	bookDir := r.state.BookDir(bookID)
	chapterNumber, err := r.state.GetNextChapterNumber(bookID)
	if err != nil {
		return nil, fmt.Errorf("get next chapter number: %w", err)
	}

	// 先 Plan 再 Compose
	planner := agents.NewPlannerAgent(r.agentCtx())
	planOutput, err := planner.PlanChapter(context.Background(), &agents.PlanChapterInput{
		Book:            book,
		BookDir:         bookDir,
		ChapterNumber:   chapterNumber,
		ExternalContext: externalContext,
	})
	if err != nil {
		return nil, fmt.Errorf("planner: %w", err)
	}

	composer := agents.NewComposerAgent(r.agentCtx())
	return composer.ComposeChapter(context.Background(), &agents.ComposeChapterInput{
		Book:          book,
		BookDir:       bookDir,
		ChapterNumber: chapterNumber,
		Plan:          planOutput,
	})
}

// ============================================================================
// ReviseChapter 回炉重写
// ============================================================================

func (r *PipelineRunner) ReviseChapter(bookID string, chapterNumber int, mode models.ReviseMode) (*ReviseResult, error) {
	releaseLock := r.state.AcquireBookLock(bookID)
	defer releaseLock()

	ctx := context.Background()

	book, err := r.state.LoadBookConfig(bookID)
	if err != nil {
		return nil, fmt.Errorf("load book config: %w", err)
	}

	bookDir := r.state.BookDir(bookID)
	chapterPath := r.state.ChapterFilePath(bookID, chapterNumber)
	content := state.ReadFileOrDefault(chapterPath, "")
	if content == "" {
		return nil, fmt.Errorf("chapter %d not found", chapterNumber)
	}

	// 去掉标题行，只取正文
	content = stripChapterTitle(content)

	// 先审计获取 issues
	r.logInfo("ReviseChapter: 审计章节", zap.Int("chapter", chapterNumber))
	auditor := agents.NewContinuityAuditor(r.agentCtx())
	auditResult, err := auditor.AuditChapter(ctx, &agents.AuditChapterInput{
		BookDir:       bookDir,
		Content:       content,
		ChapterNumber: chapterNumber,
		Genre:         book.Genre,
	})
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}

	// 执行修订
	r.logInfo("ReviseChapter: 修订章节", zap.Int("chapter", chapterNumber), zap.String("mode", string(mode)))
	reviser := agents.NewReviserAgent(r.agentCtx())
	reviseOutput, err := reviser.ReviseChapter(ctx, &agents.ReviseChapterInput{
		BookDir:       bookDir,
		Content:       content,
		ChapterNumber: chapterNumber,
		Issues:        auditResult.Issues,
		Mode:          mode,
		Genre:         book.Genre,
	})
	if err != nil {
		return nil, fmt.Errorf("revise: %w", err)
	}

	applied := reviseOutput.RevisedContent != "" && reviseOutput.RevisedContent != content
	status := "unchanged"
	if applied {
		status = "ready-for-review"
	}

	// 如果修订成功，落盘
	if applied {
		chapterContent := fmt.Sprintf("# %s\n\n%s", extractChapterTitle(content, chapterNumber), reviseOutput.RevisedContent)
		if err := state.WriteFile(chapterPath, chapterContent); err != nil {
			return nil, fmt.Errorf("write revised chapter: %w", err)
		}

		// 更新 truth files
		if reviseOutput.UpdatedState != "" {
			_ = state.WriteFile(filepath.Join(bookDir, "story", "current_state.md"), reviseOutput.UpdatedState)
		}
		if reviseOutput.UpdatedHooks != "" {
			_ = state.WriteFile(filepath.Join(bookDir, "story", "pending_hooks.md"), reviseOutput.UpdatedHooks)
		}

		// 更新元数据
		chapters, _ := r.state.LoadChapterIndex(bookID)
		for _, ch := range chapters {
			if ch.Number == chapterNumber {
				ch.Revised = true
				ch.WordCount = reviseOutput.WordCount
				ch.Status = models.ChapterStatusRevised
				_ = r.state.SaveChapterMeta(&ch)
				break
			}
		}
	}

	return &ReviseResult{
		ChapterNumber: chapterNumber,
		WordCount:     reviseOutput.WordCount,
		FixedIssues:   reviseOutput.FixedIssues,
		Applied:       applied,
		Status:        status,
		TokenUsage:    reviseOutput.TokenUsage,
	}, nil
}

// ============================================================================
// Consolidate 卷级合并
// ============================================================================

func (r *PipelineRunner) Consolidate(bookID string) (*models.ConsolidationResult, error) {
	releaseLock := r.state.AcquireBookLock(bookID)
	defer releaseLock()

	bookDir := r.state.BookDir(bookID)
	consolidator := agents.NewConsolidatorAgent(r.agentCtx())

	r.logInfo("Consolidate: 卷级合并", zap.String("bookId", bookID))
	result, err := consolidator.Consolidate(context.Background(), bookDir)
	if err != nil {
		return nil, fmt.Errorf("consolidate: %w", err)
	}

	r.logInfo("Consolidate: 完成",
		zap.Int("archivedVolumes", result.ArchivedVolumes),
		zap.Int("retainedChapters", result.RetainedChapters),
		zap.Int("promotedHooks", result.PromotedHookCount),
	)
	return result, nil
}

// ============================================================================
// ReviseFoundation 基础设定修订
// ============================================================================

func (r *PipelineRunner) ReviseFoundation(bookID string, feedback string) error {
	releaseLock := r.state.AcquireBookLock(bookID)
	defer releaseLock()

	ctx := context.Background()
	book, err := r.state.LoadBookConfig(bookID)
	if err != nil {
		return fmt.Errorf("load book config: %w", err)
	}

	language := resolveLanguage(book)
	bookDir := r.state.BookDir(bookID)
	storyDir := filepath.Join(bookDir, "story")

	// 备份当前基础设定
	timestamp := time.Now().Format("20060102-150405")
	backupDir := filepath.Join(storyDir, fmt.Sprintf(".backup-foundation-%s", timestamp))
	_ = os.MkdirAll(backupDir, 0755)

	filesToBackup := []string{
		filepath.Join(storyDir, "outline", "story_frame.md"),
		filepath.Join(storyDir, "outline", "volume_map.md"),
		filepath.Join(storyDir, "book_rules.md"),
	}
	for _, f := range filesToBackup {
		if content, err := os.ReadFile(f); err == nil {
			rel := filepath.Base(f)
			_ = os.WriteFile(filepath.Join(backupDir, rel), content, 0644)
		}
	}

	r.logInfo("ReviseFoundation: 重新生成基础设定", zap.String("bookId", bookID))

	actx := r.agentCtx()
	architect := agents.NewArchitectAgent(actx)
	reviewer := agents.NewFoundationReviewerAgent(actx)

	// 带反馈重新生成
	foundation, err := architect.GenerateFoundation(ctx, *book, feedback, "")
	if err != nil {
		return fmt.Errorf("architect generate: %w", err)
	}

	// 审查
	review, err := reviewer.Review(ctx, foundation, "original", language)
	if err != nil {
		r.logWarn("ReviseFoundation: review failed (non-fatal)", zap.Error(err))
	} else {
		r.logInfo("ReviseFoundation: 审查结果",
			zap.Int("score", review.TotalScore),
			zap.Bool("passed", review.Passed),
		)
	}

	// 落盘新基础设定
	if err := architect.WriteFoundationFiles(bookDir, foundation); err != nil {
		return fmt.Errorf("write foundation files: %w", err)
	}

	r.logInfo("ReviseFoundation: 完成", zap.String("bookId", bookID))
	return nil
}

// ============================================================================
// ReviewChapter 仅审查（不修订）
// ============================================================================

// ReviewChapter 对指定章节执行连续性审计，返回审计结果
func (r *PipelineRunner) ReviewChapter(bookID string, chapterNumber int) (*models.AuditResult, error) {
	book, err := r.state.LoadBookConfig(bookID)
	if err != nil {
		return nil, fmt.Errorf("load book config: %w", err)
	}

	bookDir := r.state.BookDir(bookID)
	chapterPath := r.state.ChapterFilePath(bookID, chapterNumber)
	content := state.ReadFileOrDefault(chapterPath, "")
	if content == "" {
		return nil, fmt.Errorf("chapter %d not found", chapterNumber)
	}
	content = stripChapterTitle(content)

	auditor := agents.NewContinuityAuditor(r.agentCtx())
	return auditor.AuditChapter(context.Background(), &agents.AuditChapterInput{
		BookDir:       bookDir,
		Content:       content,
		ChapterNumber: chapterNumber,
		Genre:         book.Genre,
	})
}

// ============================================================================
// 辅助函数
// ============================================================================

func (r *PipelineRunner) logInfo(msg string, fields ...zap.Field) {
	if r.config.Logger != nil {
		r.config.Logger.Info(msg, fields...)
	}
}

func (r *PipelineRunner) logWarn(msg string, fields ...zap.Field) {
	if r.config.Logger != nil {
		r.config.Logger.Warn(msg, fields...)
	}
}

// stripChapterTitle 去掉章节文件的标题行
func stripChapterTitle(content string) string {
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) > 1 && strings.HasPrefix(strings.TrimSpace(lines[0]), "#") {
		return strings.TrimSpace(lines[1])
	}
	return content
}

// extractChapterTitle 从内容中提取标题
func extractChapterTitle(content string, chapterNumber int) string {
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) > 0 {
		line := strings.TrimSpace(lines[0])
		if strings.HasPrefix(line, "#") {
			title := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if title != "" {
				return title
			}
		}
	}
	return fmt.Sprintf("第%d章", chapterNumber)
}
