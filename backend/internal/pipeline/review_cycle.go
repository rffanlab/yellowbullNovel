package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/rffanlab/yellowbullNovel/backend/internal/agents"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
	"go.uber.org/zap"
)

// 审查循环常量
const (
	PassScoreThreshold       = 85 // 通过分数阈值
	NetImprovementEpsilon    = 3  // 净提升阈值
	DefaultMaxReviewIterations = 1 // 默认最大修复轮次
)

// ReviewCycleParams 审查循环参数
type ReviewCycleParams struct {
	BookDir             string
	ChapterNumber       int
	Genre               string
	InitialContent      string
	InitialWordCount    int
	LengthSpec          *models.LengthSpec
	PostWriteErrors     []models.PostWriteViolation
	Auditor             *agents.ContinuityAuditor
	CreateReviser       func() *agents.ReviserAgent
	MaxReviewIterations int
	Logger              *zap.Logger
}

// ReviewCycleResult 审查循环结果
type ReviewCycleResult struct {
	FinalContent    string
	FinalWordCount  int
	Revised         bool
	AuditResult     models.AuditResult
	NormalizeApplied bool
	TotalUsage      *models.TokenUsage
	PostReviseCount int
}

// reviewSnapshot 版本快照
type reviewSnapshot struct {
	Content       string
	WordCount     int
	AuditResult   models.AuditResult
	Score         int
	LengthInRange bool
}

// assessment 审计评估
type assessment struct {
	AuditResult   models.AuditResult
	Score         int
	LengthInRange bool
}

// RunChapterReviewCycle 执行章节审查循环
//
// 流程：字数归一化 → 首轮审计 → 循环修复（审计→修订→重审） → 选择最佳版本
// 通过条件：passed && score>=85 && lengthInRange
// 退出策略：通过退出/净提升<3退出/无新内容退出/回退最高分
func RunChapterReviewCycle(ctx context.Context, params ReviewCycleParams) (*ReviewCycleResult, error) {
	if params.LengthSpec == nil {
		return nil, fmt.Errorf("length spec is required")
	}
	if params.Auditor == nil {
		return nil, fmt.Errorf("auditor is required")
	}
	if params.CreateReviser == nil {
		return nil, fmt.Errorf("createReviser is required")
	}

	maxIter := params.MaxReviewIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxReviewIterations
	}

	totalUsage := &models.TokenUsage{}

	// 步骤1: 字数归一化（简化版：仅在硬偏离时标记，不调用 LLM）
	finalContent := params.InitialContent
	finalWordCount := countLength(finalContent, *params.LengthSpec)
	normalizeApplied := false

	// 步骤2: 首轮审计
	initial := assess(ctx, params, finalContent, totalUsage)

	snapshots := []reviewSnapshot{{
		Content:       finalContent,
		WordCount:     finalWordCount,
		AuditResult:   initial.AuditResult,
		Score:         initial.Score,
		LengthInRange: initial.LengthInRange,
	}}

	// parseFailed 特殊处理：跳过自动修复
	if initial.AuditResult.ParseFailed {
		logWarn(params.Logger, "审查输出解析失败，跳过自动修复")
		return &ReviewCycleResult{
			FinalContent:     finalContent,
			FinalWordCount:   finalWordCount,
			Revised:          false,
			AuditResult:      initial.AuditResult,
			NormalizeApplied: normalizeApplied,
			TotalUsage:       totalUsage,
			PostReviseCount:  0,
		}, nil
	}

	// 步骤3+4: 循环修复
	currentAudit := initial
	postReviseCount := 0

	if !isPassed(initial) {
		for iteration := 0; iteration < maxIter; iteration++ {
			// 4a. ReviserAgent 修复（mode="auto"）
			logInfo(params.Logger, fmt.Sprintf("审查循环: 第 %d 轮修复", iteration+1))
			reviser := params.CreateReviser()
			reviseOutput, err := reviser.ReviseChapter(ctx, &agents.ReviseChapterInput{
				BookDir:       params.BookDir,
				Content:       finalContent,
				ChapterNumber: params.ChapterNumber,
				Issues:        currentAudit.AuditResult.Issues,
				Mode:          models.ReviseModeAuto,
				Genre:         params.Genre,
			})
			if err != nil {
				logWarn(params.Logger, fmt.Sprintf("修订失败 (轮次 %d): %v", iteration+1, err))
				break
			}

			// 累计 token
			if reviseOutput.TokenUsage != nil {
				totalUsage.Add(reviseOutput.TokenUsage)
			}

			// 4b. 修复未产出新内容 → 退出
			if reviseOutput.RevisedContent == "" || reviseOutput.RevisedContent == finalContent {
				logInfo(params.Logger, "修复未产出新内容，退出循环")
				break
			}

			// 4c. 重新审计
			revisedContent := reviseOutput.RevisedContent
			revisedWordCount := countLength(revisedContent, *params.LengthSpec)
			nextAssessment := assess(ctx, params, revisedContent, totalUsage)

			// 4d. 记录快照
			snapshots = append(snapshots, reviewSnapshot{
				Content:       revisedContent,
				WordCount:     revisedWordCount,
				AuditResult:   nextAssessment.AuditResult,
				Score:         nextAssessment.Score,
				LengthInRange: nextAssessment.LengthInRange,
			})
			postReviseCount++

			// 4e. 通过 → 退出
			if isPassed(nextAssessment) {
				finalContent = revisedContent
				finalWordCount = revisedWordCount
				currentAudit = nextAssessment
				logInfo(params.Logger, "审查通过，退出循环")
				break
			}

			// 4f. 净提升 >= 3 → 继续
			if nextAssessment.Score >= currentAudit.Score+NetImprovementEpsilon {
				finalContent = revisedContent
				finalWordCount = revisedWordCount
				currentAudit = nextAssessment
				logInfo(params.Logger, fmt.Sprintf("净提升 %d 分，继续下一轮", nextAssessment.Score-snapshots[len(snapshots)-2].Score))
			} else {
				// 4g. 无净提升 → 退出
				logInfo(params.Logger, fmt.Sprintf("净提升 < %d 分，退出循环", NetImprovementEpsilon))
				break
			}
		}
	}

	// 步骤5: 选择最佳版本
	best, shouldRestore := selectBestSnapshot(snapshots, currentAudit, finalContent)
	if shouldRestore {
		logInfo(params.Logger, fmt.Sprintf("回退到最佳版本 (score=%d)", best.Score))
		finalContent = best.Content
		finalWordCount = best.WordCount
		currentAudit = assessment{
			AuditResult:   best.AuditResult,
			Score:         best.Score,
			LengthInRange: best.LengthInRange,
		}
	}

	revised := len(snapshots) > 1 && finalContent != params.InitialContent

	return &ReviewCycleResult{
		FinalContent:     finalContent,
		FinalWordCount:   finalWordCount,
		Revised:          revised,
		AuditResult:      currentAudit.AuditResult,
		NormalizeApplied: normalizeApplied,
		TotalUsage:       totalUsage,
		PostReviseCount:  postReviseCount,
	}, nil
}

// assess 对章节内容做全面审计
func assess(ctx context.Context, params ReviewCycleParams, content string, totalUsage *models.TokenUsage) assessment {
	// LLM 审计
	auditResult, err := params.Auditor.AuditChapter(ctx, &agents.AuditChapterInput{
		BookDir:       params.BookDir,
		Content:       content,
		ChapterNumber: params.ChapterNumber,
		Genre:         params.Genre,
	})
	if err != nil {
		logWarn(params.Logger, fmt.Sprintf("审计失败: %v", err))
		auditResult = &models.AuditResult{
			Passed: false,
			ParseFailed: true,
			Issues: []models.AuditIssue{{
				Severity:    models.SeverityCritical,
				Category:    "系统错误",
				Description: fmt.Sprintf("审计调用失败: %v", err),
				Suggestion:  "请重试",
			}},
			Summary: "审计调用失败",
		}
	}

	// 累计 token
	if auditResult.TokenUsage != nil {
		totalUsage.Add(auditResult.TokenUsage)
	}

	// 合并 postWriteErrors
	for _, e := range params.PostWriteErrors {
		severity := models.SeverityWarning
		if e.Severity == "error" {
			severity = models.SeverityCritical
		}
		auditResult.Issues = append(auditResult.Issues, models.AuditIssue{
			Severity:    severity,
			Category:    e.Rule,
			Description: e.Description,
			Suggestion:  e.Suggestion,
		})
	}

	// 重新判定 passed（有 critical 级 postWrite 问题则 false）
	hasCritical := false
	for _, issue := range auditResult.Issues {
		if issue.Severity == models.SeverityCritical {
			hasCritical = true
			break
		}
	}
	if hasCritical {
		auditResult.Passed = false
	}

	// 字数硬门
	wordCount := countLength(content, *params.LengthSpec)
	lengthInRange := isLengthInRange(wordCount, params.LengthSpec)

	// 分数
	score := 0
	if auditResult.OverallScore != nil {
		score = *auditResult.OverallScore
	}

	return assessment{
		AuditResult:   *auditResult,
		Score:         score,
		LengthInRange: lengthInRange,
	}
}

// isPassed 判定审计是否通过
func isPassed(a assessment) bool {
	return a.AuditResult.Passed && a.Score >= PassScoreThreshold && a.LengthInRange
}

// isLengthInRange 判断字数是否在硬区间内
func isLengthInRange(wordCount int, spec *models.LengthSpec) bool {
	if spec == nil {
		return true
	}
	hardMin := spec.Target * 70 / 100
	hardMax := spec.Target * 150 / 100
	return wordCount >= hardMin && wordCount <= hardMax
}

// selectBestSnapshot 从所有快照中选择最佳版本
func selectBestSnapshot(snapshots []reviewSnapshot, currentAudit assessment, currentContent string) (reviewSnapshot, bool) {
	if len(snapshots) == 0 {
		return reviewSnapshot{}, false
	}

	best := snapshots[0]
	for _, snap := range snapshots[1:] {
		// 1. lengthInRange 优先
		if snap.LengthInRange != best.LengthInRange {
			if snap.LengthInRange {
				best = snap
			}
			continue
		}
		// 2. score 更高者优先（需 >= best + 3）
		if snap.Score >= best.Score+NetImprovementEpsilon {
			best = snap
		}
	}

	// 如果最佳版本 != 当前版本，且回退条件满足 → 回退
	shouldRestore := best.Content != currentContent && (
		(best.LengthInRange && !currentAudit.LengthInRange) ||
			best.Score >= currentAudit.Score+NetImprovementEpsilon)
	return best, shouldRestore
}

// ============================================================================
// 辅助日志函数
// ============================================================================

func logInfo(logger *zap.Logger, msg string) {
	if logger != nil {
		logger.Info("[review-cycle] " + msg)
	}
}

func logWarn(logger *zap.Logger, msg string) {
	if logger != nil {
		logger.Warn("[review-cycle] " + msg)
	}
}

// countLength 按计数模式计算字数（复用 runner.go 中的版本）
// 注意：这里不能直接引用 runner.go 的 countLength，因为它是同包的
// 实际上同包可以引用，所以这里不需要重复定义
// 但为了文件独立性，保留一个 wrapper

var _ = strings.TrimSpace // 避免 unused import
