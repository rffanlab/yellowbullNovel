package agents

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
)

// AutoOutputMode auto 模式的输出路径。
type AutoOutputMode string

const (
	AutoOutputPatchOnly   AutoOutputMode = "patch-only"
	AutoOutputRewriteOnly AutoOutputMode = "rewrite-only"
	AutoOutputAllowFull   AutoOutputMode = "allow-full"
)

// ReviserAgent 修订 Agent。
type ReviserAgent struct {
	BaseAgent
}

// NewReviserAgent 创建修订 Agent。
func NewReviserAgent(ctx llm.AgentContext) *ReviserAgent {
	return &ReviserAgent{BaseAgent: NewBaseAgent(ctx, "reviser")}
}

// ReviseChapterInput 修订输入。
type ReviseChapterInput struct {
	BookDir       string
	Content       string
	ChapterNumber int
	Issues        []models.AuditIssue
	Mode          models.ReviseMode
	Genre         string
}

// ReviseChapter 执行章节修订，支持 6 种修订模式。
func (a *ReviserAgent) ReviseChapter(ctx context.Context, input *ReviseChapterInput) (*models.ReviseOutput, error) {
	if input.Mode == "" {
		input.Mode = models.ReviseModeAuto
	}

	// 确定 autoOutputMode
	autoOutputMode := AutoOutputAllowFull
	if input.Mode == models.ReviseModeAuto {
		autoOutputMode = resolveAutoOutputMode(input.Issues)
	}

	// 构建 issues 列表
	issueList := buildIssueList(input.Issues, input.Mode)

	// 构建 system prompt
	systemPrompt := a.buildSystemPrompt(input.Mode, autoOutputMode)

	// 构建 user prompt
	userPrompt := a.buildUserPrompt(input, issueList)

	// LLM 修订
	temp := 0.3
	response, err := a.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}, &llm.ChatOptions{Temperature: &temp})
	if err != nil {
		return nil, fmt.Errorf("reviser LLM call: %w", err)
	}

	// 解析输出
	output := a.parseOutput(response.Content, input.Mode, input.Content, autoOutputMode)

	// 附带 token usage
	if response.TokenUsage != nil {
		output.TokenUsage = &models.TokenUsage{
			PromptTokens:     response.TokenUsage.PromptTokens,
			CompletionTokens: response.TokenUsage.CompletionTokens,
			TotalTokens:      response.TokenUsage.TotalTokens,
		}
	}

	return output, nil
}

// buildSystemPrompt 构建修订 system prompt。
func (a *ReviserAgent) buildSystemPrompt(mode models.ReviseMode, autoOutputMode AutoOutputMode) string {
	if mode == models.ReviseModeAuto {
		return a.buildAutoSystemPrompt(autoOutputMode)
	}

	desc := getModeDescription(mode)
	return fmt.Sprintf(`你是专业的小说修订编辑。

## 修订模式
%s

## 修稿原则
1. 修根因不修表面
2. 伏笔同步更新
3. 不改走向
4. 保持风格一致
5. 情绪外化不标签
6. 对话有区分度
7. 升级叠加不替换

## 输出格式
=== FIXED_ISSUES ===
(逐条说明修正了什么，一行一条)

=== REVISED_CONTENT ===
(修正后的完整正文)

=== UPDATED_STATE ===
(更新后的完整状态卡)

=== UPDATED_HOOKS ===
(更新后的完整伏笔池)`, desc)
}

// buildAutoSystemPrompt 构建 auto 模式 system prompt。
func (a *ReviserAgent) buildAutoSystemPrompt(autoOutputMode AutoOutputMode) string {
	routingDirective := ""
	switch autoOutputMode {
	case AutoOutputRewriteOnly:
		routingDirective = "分流指令：reviewer 报告的阻塞问题属于结构/语义错。你必须输出 REVISED_CONTENT——禁止输出 PATCHES。"
	case AutoOutputPatchOnly:
		routingDirective = "分流指令：reviewer 报告的阻塞问题属于局部错。你必须只输出 PATCHES——不要整章改写。"
	case AutoOutputAllowFull:
		routingDirective = "你可以输出 PATCHES 或 REVISED_CONTENT，根据问题性质自行选择。"
	}

	return fmt.Sprintf(`你是专业的小说修订编辑，使用自动模式。

## 修稿原则
1. 修根因不修表面
2. 伏笔同步更新
3. 不改走向
4. 保持风格一致
5. 情绪外化不标签
6. 对话有区分度
7. 升级叠加不替换

%s

## 输出格式
=== FIXED_ISSUES ===
(逐条说明修正了什么；如果无法安全修复，也在这里说明)

=== PATCHES ===
(局部补丁——仅用于局部文字问题。有全章级问题时省略此区块)
--- PATCH 1 ---
TARGET_TEXT:
(从原文中精确引用要修改的段落)
REPLACEMENT_TEXT:
(替换后的文本)
--- END PATCH ---

=== REVISED_CONTENT ===
(修正后的完整正文——用于字数/结构/节奏等全章级问题。仅局部问题时省略此区块)

=== UPDATED_STATE ===
(更新后的完整状态卡)

=== UPDATED_HOOKS ===
(更新后的完整伏笔池)`, routingDirective)
}

// buildUserPrompt 构建修订 user prompt。
func (a *ReviserAgent) buildUserPrompt(input *ReviseChapterInput, issueList string) string {
	storyDir := filepath.Join(input.BookDir, "story")
	currentState := readFileOrDefault(filepath.Join(storyDir, "current_state.md"), "")
	hooks := readFileOrDefault(filepath.Join(storyDir, "pending_hooks.md"), "")

	return fmt.Sprintf(`## 审稿意见

%s

## 待修订正文（第%d章）

%s

## 当前状态卡
%s

## 当前伏笔池
%s

请根据审稿意见修订正文，严格按 === TAG === 格式输出。`,
		issueList,
		input.ChapterNumber,
		input.Content,
		currentState,
		hooks)
}

// parseOutput 解析修订输出。
func (a *ReviserAgent) parseOutput(content string, mode models.ReviseMode, originalChapter string, autoOutputMode AutoOutputMode) *models.ReviseOutput {
	extract := func(tag string) string {
		re := regexp.MustCompile(fmt.Sprintf("=== %s ===\\s*([\\s\\S]*?)(?==== [A-Z_]+ ===|$)", tag))
		match := re.FindStringSubmatch(content)
		if len(match) > 1 {
			return strings.TrimSpace(match[1])
		}
		return ""
	}

	fixedRaw := extract("FIXED_ISSUES")
	fixedIssues := splitNonEmptyLines(fixedRaw)

	makeResult := func(revisedContent string, applied bool) *models.ReviseOutput {
		result := &models.ReviseOutput{
			RevisedContent: revisedContent,
			WordCount:      countChapterLength(revisedContent),
			UpdatedState:   extract("UPDATED_STATE"),
			UpdatedHooks:   extract("UPDATED_HOOKS"),
		}
		if applied {
			result.FixedIssues = fixedIssues
		} else {
			result.FixedIssues = []string{}
		}
		return result
	}

	// auto 模式：按 autoOutputMode 路由
	if mode == models.ReviseModeAuto {
		switch autoOutputMode {
		case AutoOutputPatchOnly:
			patchesRaw := extract("PATCHES")
			patches := parseSpotFixPatches(patchesRaw)
			if len(patches) == 0 {
				return makeResult(originalChapter, false)
			}
			patchResult := applySpotFixPatches(originalChapter, patches)
			if patchResult.Applied && float64(patchResult.AppliedCount)/float64(len(patches)) >= 0.5 {
				return makeResult(patchResult.RevisedContent, true)
			}
			return makeResult(originalChapter, false)

		case AutoOutputRewriteOnly:
			revisedContent := extract("REVISED_CONTENT")
			if revisedContent == "" {
				return makeResult(originalChapter, false)
			}
			return makeResult(revisedContent, true)

		case AutoOutputAllowFull:
			revisedContent := extract("REVISED_CONTENT")
			if revisedContent != "" {
				return makeResult(revisedContent, true)
			}
			patchesRaw := extract("PATCHES")
			patches := parseSpotFixPatches(patchesRaw)
			if len(patches) > 0 {
				patchResult := applySpotFixPatches(originalChapter, patches)
				if patchResult.Applied && float64(patchResult.AppliedCount)/float64(len(patches)) >= 0.5 {
					return makeResult(patchResult.RevisedContent, true)
				}
			}
			return makeResult(originalChapter, false)
		}
	}

	// spot-fix 模式：仅 PATCHES
	if mode == models.ReviseModeSpotFix {
		patches := parseSpotFixPatches(extract("PATCHES"))
		patchResult := applySpotFixPatches(originalChapter, patches)
		return makeResult(patchResult.RevisedContent, patchResult.Applied)
	}

	// legacy 模式（polish/rewrite/rework/anti-detect）：完整正文
	revisedContent := extract("REVISED_CONTENT")
	if revisedContent == "" {
		return makeResult(originalChapter, false)
	}
	return makeResult(revisedContent, true)
}

// resolveAutoOutputMode 决定 auto 模式的输出路径。
func resolveAutoOutputMode(issues []models.AuditIssue) AutoOutputMode {
	if len(issues) == 0 {
		return AutoOutputAllowFull
	}

	// 优先看 repairScope
	var scopedBlocking []models.AuditIssue
	for _, issue := range issues {
		if issue.Severity != "info" && issue.RepairScope != nil {
			scopedBlocking = append(scopedBlocking, issue)
		}
	}
	if len(scopedBlocking) > 0 {
		for _, issue := range scopedBlocking {
			if issue.RepairScope != nil && *issue.RepairScope == models.ScopeStructural {
				return AutoOutputRewriteOnly
			}
		}
		// 检查是否全部 local
		blockingCount := 0
		for _, issue := range issues {
			if issue.Severity != "info" {
				blockingCount++
			}
		}
		if len(scopedBlocking) == blockingCount {
			allLocal := true
			for _, issue := range scopedBlocking {
				if issue.RepairScope == nil || *issue.RepairScope != models.ScopeLocal {
					allLocal = false
					break
				}
			}
			if allLocal {
				return AutoOutputPatchOnly
			}
		}
	}

	// 回退到 category 正则匹配
	var blocking []models.AuditIssue
	for _, issue := range issues {
		if issue.Severity != "info" {
			blocking = append(blocking, issue)
		}
	}
	if len(blocking) == 0 {
		return AutoOutputPatchOnly
	}

	structuralCount := 0
	localOnlyCount := 0
	for _, issue := range blocking {
		if isStructural(issue.Category) {
			structuralCount++
		} else if isLocalOnly(issue.Category) {
			localOnlyCount++
		}
	}

	if structuralCount > 0 {
		return AutoOutputRewriteOnly
	}
	if localOnlyCount == len(blocking) {
		return AutoOutputPatchOnly
	}
	return AutoOutputAllowFull
}

// isStructural 判断是否结构级问题。
func isStructural(category string) bool {
	patterns := []string{
		`OOC|人设|Character Fidelity|Character Matrix|Character.*Consistency`,
		`Mainline.*Drift|主线偏离|Outline Drift|大纲偏离|Chapter Memo Drift|章节备忘偏离`,
		`Conflict|冲突乏力|Payoff Dilution|爽点虚化`,
		`Timeline|时间线`,
		`Hook Check|伏笔检查|Hook.*Debt|伏笔.*债|未兑现`,
		`Power Scaling|战力崩坏|金手指`,
		`Pacing|节奏`,
		`POV Consistency|视角`,
		`Subplot Stagnation|支线停滞|Arc Flatline|弧线平坦`,
		`Relationship Dynamics|关系动态|情感表达`,
		`Incentive Chain|利益链`,
		`Canon Event|正典|Mainline Canon`,
	}
	for _, p := range patterns {
		if regexp.MustCompile(p).MatchString(category) {
			return true
		}
	}
	return false
}

// isLocalOnly 判断是否局部级问题。
func isLocalOnly(category string) bool {
	patterns := []string{
		`Paragraph uniformity|段落等长`,
		`Hedge density|套话密度`,
		`Formulaic transitions|公式化转折`,
		`List-like structure|列表式结构`,
		`Cross-chapter repetition|跨章重复`,
		`AI-tell word density`,
		`Fatigue word|高疲劳词`,
		`Information Boundary Check|信息越界`,
		`Knowledge Base Pollution|知识库污染`,
	}
	for _, p := range patterns {
		if regexp.MustCompile(p).MatchString(category) {
			return true
		}
	}
	return false
}

// getModeDescription 返回模式描述。
func getModeDescription(mode models.ReviseMode) string {
	switch mode {
	case models.ReviseModePolish:
		return "润色：只改表达、节奏、段落呼吸，不改事实与剧情结论。\n禁止：增删段落、改变人名/地名/物品名、增加新情节或新对话、改变因果关系。\n只允许：替换用词、调整句序、修改标点节奏"
	case models.ReviseModeRewrite:
		return "改写：允许重组问题段落、调整画面和叙述力度，但优先保留原文的绝大部分句段。除非问题跨越整章，否则禁止整章推倒重写；只能围绕问题段落及其直接上下文改写，同时保留核心事实与人物动机"
	case models.ReviseModeRework:
		return "重写：可重构场景推进和冲突组织，但不改主设定和大事件结果"
	case models.ReviseModeAntiDetect:
		return "反检测改写：在保持剧情不变的前提下，降低AI生成可检测性。\n\n改写手法：\n1. 打破句式规律\n2. 口语化替代\n3. 减少\"了\"字密度\n4. 转折词降频\n5. 情绪外化\n6. 删叙述者结论\n7. 群像反应具体化\n8. 段落长度差异化\n9. 消灭AI标记词"
	case models.ReviseModeSpotFix:
		return "定点修复：只修改审稿意见指出的具体句子或段落，其余所有内容必须原封不动保留。修改范围限定在问题句子及其前后各一句。禁止改动无关段落"
	default:
		return ""
	}
}

// buildIssueList 构建 issues 列表。
func buildIssueList(issues []models.AuditIssue, mode models.ReviseMode) string {
	if mode == models.ReviseModeAuto {
		return buildTieredIssueList(issues)
	}
	return buildFlatIssueList(issues)
}

// buildTieredIssueList 分层展示问题（auto 模式）。
func buildTieredIssueList(issues []models.AuditIssue) string {
	var critical, high, medium []string
	for _, issue := range issues {
		line := fmt.Sprintf("- %s: %s", issue.Category, issue.Description)
		switch issue.Severity {
		case "critical":
			critical = append(critical, line)
		case "warning":
			high = append(high, line)
		default:
			medium = append(medium, line)
		}
	}
	var parts []string
	if len(critical) > 0 {
		parts = append(parts, "## Critical（必须解决）\n"+strings.Join(critical, "\n"))
	}
	if len(high) > 0 {
		parts = append(parts, "## High（应当改善）\n"+strings.Join(high, "\n"))
	}
	if len(medium) > 0 {
		parts = append(parts, "## Medium（参考建议）\n"+strings.Join(medium, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// buildFlatIssueList 平铺展示问题。
func buildFlatIssueList(issues []models.AuditIssue) string {
	var lines []string
	for _, issue := range issues {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", issue.Severity, issue.Category, issue.Description))
	}
	return strings.Join(lines, "\n")
}

// ============================================================================
// SpotFix Patch 解析与应用
// ============================================================================

// SpotFixPatch 定点修复补丁。
type SpotFixPatch struct {
	TargetText      string
	ReplacementText string
}

// PatchResult 补丁应用结果。
type PatchResult struct {
	RevisedContent    string
	Applied           bool
	AppliedCount      int
}

// parseSpotFixPatches 解析补丁列表。
func parseSpotFixPatches(raw string) []SpotFixPatch {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	patchSplitPattern := regexp.MustCompile("(?m)^--- PATCH \\d+ ---$")
	blocks := patchSplitPattern.Split(raw, -1)
	var patches []SpotFixPatch

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" || strings.HasPrefix(block, "--- END PATCH") {
			continue
		}

		target := extractPatchSection(block, "TARGET_TEXT:")
		replacement := extractPatchSection(block, "REPLACEMENT_TEXT:")
		if target != "" && replacement != "" {
			patches = append(patches, SpotFixPatch{
				TargetText:      target,
				ReplacementText: replacement,
			})
		}
	}
	return patches
}

// extractPatchSection 提取补丁内的段落。
func extractPatchSection(block string, label string) string {
	idx := strings.Index(block, label)
	if idx < 0 {
		return ""
	}
	start := idx + len(label)
	rest := block[start:]
	endIdx := strings.Index(rest, "--- ")
	if endIdx < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:endIdx])
}

// applySpotFixPatches 应用补丁到原文。
func applySpotFixPatches(original string, patches []SpotFixPatch) *PatchResult {
	result := original
	appliedCount := 0
	for _, patch := range patches {
		count := strings.Count(result, patch.TargetText)
		if count == 1 {
			result = strings.Replace(result, patch.TargetText, patch.ReplacementText, 1)
			appliedCount++
		}
	}
	return &PatchResult{
		RevisedContent: result,
		Applied:        appliedCount > 0,
		AppliedCount:   appliedCount,
	}
}

// splitNonEmptyLines 分割非空行。
func splitNonEmptyLines(raw string) []string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
