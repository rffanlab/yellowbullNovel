package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
)

// ComposeChapterInput 上下文组装输入。
type ComposeChapterInput struct {
	Book          *models.BookConfig
	BookDir       string
	ChapterNumber int
	Plan          *models.PlanChapterOutput
}

// ComposerAgent 上下文组装 Agent。
type ComposerAgent struct {
	BaseAgent
}

// NewComposerAgent 创建上下文组装 Agent。
func NewComposerAgent(ctx llm.AgentContext) *ComposerAgent {
	return &ComposerAgent{BaseAgent: NewBaseAgent(ctx, "composer")}
}

// ComposeChapter 是上下文组装流程的入口。
func (a *ComposerAgent) ComposeChapter(ctx context.Context, input *ComposeChapterInput) (*models.ComposeChapterOutput, error) {
	if input.Plan == nil {
		return nil, fmt.Errorf("plan output is nil")
	}

	storyDir := filepath.Join(input.BookDir, "story")
	runtimeDir := filepath.Join(storyDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		return nil, fmt.Errorf("create runtime dir: %w", err)
	}

	// 步骤1: 收集 12 类上下文源
	entries := a.collectSelectedContext(storyDir, input.Plan, input.ChapterNumber)

	// 构建 ContextPackage
	contextPackage := &models.ContextPackage{
		Chapter:         input.ChapterNumber,
		SelectedContext: entries,
	}

	// 步骤2: 预算控制（跳过，当前简化实现不启用 LLM 编译压缩）

	// 步骤3: 构建治理规则栈
	ruleStack := buildGovernedRuleStack(input.Plan, input.ChapterNumber)

	// 步骤4: 构建章节追踪
	trace := a.buildGovernedTrace(input.Plan, contextPackage, input.ChapterNumber)

	// 落盘
	contextPath := filepath.Join(runtimeDir, fmt.Sprintf("chapter-%04d.context.md", input.ChapterNumber))
	ruleStackPath := filepath.Join(runtimeDir, fmt.Sprintf("chapter-%04d.rulestack.json", input.ChapterNumber))
	tracePath := filepath.Join(runtimeDir, fmt.Sprintf("chapter-%04d.trace.json", input.ChapterNumber))

	if err := writeFileSafe(contextPath, renderContextMarkdown(contextPackage)); err != nil {
		return nil, fmt.Errorf("write context file: %w", err)
	}

	ruleStackJSON, err := json.MarshalIndent(ruleStack, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal rule stack: %w", err)
	}
	if err := writeFileSafe(ruleStackPath, string(ruleStackJSON)); err != nil {
		return nil, fmt.Errorf("write rule stack file: %w", err)
	}

	traceJSON, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal trace: %w", err)
	}
	if err := writeFileSafe(tracePath, string(traceJSON)); err != nil {
		return nil, fmt.Errorf("write trace file: %w", err)
	}

	return &models.ComposeChapterOutput{
		ContextPackage: *contextPackage,
		RuleStack:      *ruleStack,
		Trace:          *trace,
		ContextPath:    contextPath,
		RuleStackPath:  ruleStackPath,
		TracePath:      tracePath,
	}, nil
}

// collectSelectedContext 收集 12 类上下文源。
func (a *ComposerAgent) collectSelectedContext(storyDir string, plan *models.PlanChapterOutput, chapterNumber int) []models.ContextEntry {
	var entries []models.ContextEntry

	// 源 1: chapter_memo
	memoExcerpt := fmt.Sprintf("goal=%s | golden-opening=%t | %s", plan.Memo.Goal, plan.Memo.IsGoldenOpening, plan.Memo.Body)
	entries = append(entries, models.ContextEntry{
		Source:  "runtime/chapter_memo",
		Reason:  "Carry the planner's chapter memo into governed writing.",
		Excerpt: memoExcerpt,
	})

	// 源 2: current_focus
	currentFocus := readFileOrDefault(filepath.Join(storyDir, "current_focus.md"), "")
	if currentFocus != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/current_focus.md",
			Reason:  "Current task focus for this chapter.",
			Excerpt: currentFocus,
		})
	}

	// 源 3: author_intent
	authorIntent := readFileOrDefault(filepath.Join(storyDir, "author_intent.md"), "")
	if authorIntent != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/author_intent.md",
			Reason:  "User's long-term authorial intent and direction — binding, overrides model defaults.",
			Excerpt: authorIntent,
		})
	}

	// 源 4: audit_drift
	auditDrift := readFileOrDefault(filepath.Join(storyDir, "audit_drift.md"), "")
	if auditDrift != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/audit_drift.md",
			Reason:  "Carry forward audit drift guidance from the previous chapter.",
			Excerpt: auditDrift,
		})
	}

	// 源 5: current_state
	currentState := readFileOrDefault(filepath.Join(storyDir, "current_state.md"), "")
	if currentState != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/current_state.md",
			Reason:  "Preserve hard state facts referenced by the active chapter brief or hard constraints.",
			Excerpt: currentState,
		})
	}

	// 源 6: story_frame
	storyFrame := readFileOrDefault(filepath.Join(storyDir, "outline", "story_frame.md"), "")
	if storyFrame == "" {
		storyFrame = readFileOrDefault(filepath.Join(storyDir, "story_bible.md"), "")
	}
	if storyFrame != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/outline/story_frame.md",
			Reason:  "Preserve canon constraints referenced by the active chapter brief.",
			Excerpt: storyFrame,
		})
	}

	// 源 7: volume_map
	volumeMap := readFileOrDefault(filepath.Join(storyDir, "outline", "volume_map.md"), "")
	if volumeMap == "" {
		volumeMap = readFileOrDefault(filepath.Join(storyDir, "volume_outline.md"), "")
	}
	if volumeMap != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/outline/volume_map.md",
			Reason:  "Anchor the default planning node for this chapter.",
			Excerpt: volumeMap,
		})
	}

	// 源 8: parent_canon / fanfic_canon
	parentCanon := readFileOrDefault(filepath.Join(storyDir, "parent_canon.md"), "")
	if parentCanon != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/parent_canon.md",
			Reason:  "Preserve parent canon constraints for governed continuation.",
			Excerpt: parentCanon,
		})
	}
	fanficCanon := readFileOrDefault(filepath.Join(storyDir, "fanfic_canon.md"), "")
	if fanficCanon != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/fanfic_canon.md",
			Reason:  "Preserve fanfic canon constraints for fanfic writing.",
			Excerpt: fanficCanon,
		})
	}

	// 源 9: 近 5 章摘要 trail
	chapterSummaries := readFileOrDefault(filepath.Join(storyDir, "chapter_summaries.md"), "")
	if chapterSummaries != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/chapter_summaries.md#recent_titles",
			Reason:  "Keep recent title history visible to avoid repetitive chapter naming.",
			Excerpt: chapterSummaries,
		})
	}

	// 源 10: 伏笔债务（hook debt）
	pendingHooks := readFileOrDefault(filepath.Join(storyDir, "pending_hooks.md"), "")
	if pendingHooks != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/pending_hooks.md",
			Reason:  "Carry forward unresolved hooks that match the chapter focus.",
			Excerpt: pendingHooks,
		})
	}

	// 源 11: book_rules
	bookRules := readFileOrDefault(filepath.Join(storyDir, "book_rules.md"), "")
	if bookRules != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/book_rules.md",
			Reason:  "Carry forward book-level rules and prohibitions.",
			Excerpt: bookRules,
		})
	}

	// 源 12: emotional_arcs
	emotionalArcs := readFileOrDefault(filepath.Join(storyDir, "emotional_arcs.md"), "")
	if emotionalArcs != "" {
		entries = append(entries, models.ContextEntry{
			Source:  "story/emotional_arcs.md",
			Reason:  "Keep emotional arc progression visible for character consistency.",
			Excerpt: emotionalArcs,
		})
	}

	return entries
}

// applyContextBudgetIfNeeded 上下文预算控制（简化版：仅估算 token，不启用 LLM 编译）。
func (a *ComposerAgent) applyContextBudgetIfNeeded(entries []models.ContextEntry, budgetTokens int) ([]models.ContextEntry, *models.TraceCompression) {
	if budgetTokens <= 0 {
		return entries, nil
	}

	totalTokens := 0
	for _, entry := range entries {
		totalTokens += llm.EstimateTokens(entry.Source + "\n" + entry.Reason + "\n" + entry.Excerpt)
	}

	if totalTokens <= budgetTokens {
		return entries, nil
	}

	// 简化处理：按 protected/compressible 拆分，protected 不可压缩
	var protectedEntries, compressibleEntries []models.ContextEntry
	for _, entry := range entries {
		if isProtectedContextSource(entry.Source) {
			protectedEntries = append(protectedEntries, entry)
		} else {
			compressibleEntries = append(compressibleEntries, entry)
		}
	}

	protectedTokens := 0
	for _, entry := range protectedEntries {
		protectedTokens += llm.EstimateTokens(entry.Source + "\n" + entry.Reason + "\n" + entry.Excerpt)
	}

	if protectedTokens > budgetTokens {
		// protected 超预算，无法压缩，直接返回
		a.LogWarn(fmt.Sprintf("protected context (%d tokens) exceeds budget (%d tokens)", protectedTokens, budgetTokens))
		return entries, nil
	}

	// 简化：截断 compressible entries 的 excerpt
	remainingBudget := budgetTokens - protectedTokens
	for i := range compressibleEntries {
		entryTokens := llm.EstimateTokens(compressibleEntries[i].Source + "\n" + compressibleEntries[i].Reason + "\n" + compressibleEntries[i].Excerpt)
		if entryTokens > remainingBudget/len(compressibleEntries) {
			maxChars := (remainingBudget / len(compressibleEntries)) * 3
			if maxChars < len(compressibleEntries[i].Excerpt) {
				compressibleEntries[i].Excerpt = truncateRunes(compressibleEntries[i].Excerpt, maxChars) + "\n...(truncated)"
			}
		}
	}

	return append(protectedEntries, compressibleEntries...), &models.TraceCompression{
		CompiledSource:    "",
		ProtectedTokens:   protectedTokens,
		CompressibleTokens: totalTokens - protectedTokens,
		BudgetTokens:      budgetTokens,
	}
}

// buildGovernedRuleStack 构建治理规则栈。
func buildGovernedRuleStack(plan *models.PlanChapterOutput, chapterNumber int) *models.RuleStack {
	stack := &models.RuleStack{
		Chapter: chapterNumber,
		Entries: []models.RuleStackEntry{
			{Level: "L1", Source: "hard_facts", Rule: "story_frame + current_state + book_rules + roles", Active: true},
			{Level: "L2", Source: "author_intent", Rule: "author_intent.md + current_focus.md + volume_map", Active: true},
			{Level: "L3", Source: "planning", Rule: "chapter intent + outline node", Active: true},
			{Level: "L4", Source: "current_task", Rule: "chapter memo + user instruction", Active: true},
		},
	}

	// 从 intent 生成活动覆盖
	for _, item := range plan.Intent.MustAvoid {
		stack.ActiveOverrides = append(stack.ActiveOverrides, models.OverrideEdge{
			From:   "L4",
			To:     "L3",
			Reason: truncateForOverrideReason(item),
		})
	}
	for _, item := range plan.Intent.StyleEmphasis {
		stack.ActiveOverrides = append(stack.ActiveOverrides, models.OverrideEdge{
			From:   "L4",
			To:     "L3",
			Reason: truncateForOverrideReason(item),
		})
	}

	return stack
}

// buildGovernedTrace 构建章节追踪。
func (a *ComposerAgent) buildGovernedTrace(plan *models.PlanChapterOutput, contextPackage *models.ContextPackage, chapterNumber int) *models.ChapterTrace {
	var selectedSources []string
	var protectedSources, compressibleSources []string
	for _, entry := range contextPackage.SelectedContext {
		selectedSources = append(selectedSources, entry.Source)
		if isProtectedContextSource(entry.Source) {
			protectedSources = append(protectedSources, entry.Source)
		} else {
			compressibleSources = append(compressibleSources, entry.Source)
		}
	}

	return &models.ChapterTrace{
		Chapter:        chapterNumber,
		ComposerInputs: []string{plan.RuntimePath},
		Notes:          selectedSources,
		Compression: &models.TraceCompression{
			CompiledSource:     "",
			ProtectedSources:   protectedSources,
			CompressedSources:  compressibleSources,
			ProtectedTokens:    0,
			CompressibleTokens: 0,
			BudgetTokens:       0,
		},
	}
}

// isProtectedContextSource 判断上下文源是否受保护（不可压缩）。
func isProtectedContextSource(source string) bool {
	switch {
	case source == "runtime/chapter_memo":
		return true
	case source == "story/current_focus.md":
		return true
	case source == "story/author_intent.md":
		return true
	case source == "story/audit_drift.md":
		return true
	case source == "story/outline/story_frame.md":
		return true
	case strings.HasPrefix(source, "story/outline/story_frame.md#"):
		return true
	case source == "story/story_bible.md":
		return true
	case source == "story/outline/volume_map.md":
		return true
	case strings.HasPrefix(source, "story/outline/volume_map.md#"):
		return true
	case source == "story/volume_outline.md":
		return true
	case source == "story/parent_canon.md":
		return true
	case source == "story/fanfic_canon.md":
		return true
	case strings.HasPrefix(source, "story/current_state.md"):
		return true
	case strings.HasPrefix(source, "story/pending_hooks.md"):
		return true
	case strings.HasPrefix(source, "runtime/hook_debt#"):
		return true
	case source == "story/book_rules.md":
		return true
	default:
		return false
	}
}

// renderContextMarkdown 渲染 ContextPackage 为 Markdown。
func renderContextMarkdown(cp *models.ContextPackage) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Chapter %d Context Package\n\n", cp.Chapter))
	sb.WriteString("## Selected Context\n\n")
	for _, entry := range cp.SelectedContext {
		sb.WriteString(fmt.Sprintf("### %s\n", entry.Source))
		sb.WriteString(fmt.Sprintf("Reason: %s\n", entry.Reason))
		sb.WriteString(entry.Excerpt + "\n\n")
	}
	return sb.String()
}

// truncateForOverrideReason 截断 override reason 到 80 字符。
func truncateForOverrideReason(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 80 {
		return string([]rune(s)[:79]) + "…"
	}
	return s
}
