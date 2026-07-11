package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
)

// ValidationWarning 校验警告。
type ValidationWarning struct {
	Category    string `json:"category"`
	Description string `json:"description"`
}

// ValidationResult 校验结果。
type ValidationResult struct {
	Warnings []ValidationWarning `json:"warnings"`
	Passed   bool                `json:"passed"`
}

// StateValidationAuthorityContext 跨真值校验上下文。
type StateValidationAuthorityContext struct {
	StoryFrame       string
	BookRules        string
	ChapterSummaries string
}

// StateValidatorAgent 状态校验 Agent。
type StateValidatorAgent struct {
	BaseAgent
}

// NewStateValidatorAgent 创建状态校验 Agent。
func NewStateValidatorAgent(ctx llm.AgentContext) *StateValidatorAgent {
	return &StateValidatorAgent{BaseAgent: NewBaseAgent(ctx, "state-validator")}
}

// ValidateTruthFiles 对比新旧 truth files，检测与正文的矛盾。
func (a *StateValidatorAgent) ValidateTruthFiles(
	ctx context.Context,
	bookDir string,
	chapterContent string,
	chapterNumber int,
	oldState string,
	newState string,
	oldHooks string,
	newHooks string,
	language string,
	authorityContext *StateValidationAuthorityContext,
) (*ValidationResult, error) {
	// 计算 diff
	stateDiff := computeDiff(oldState, newState, "current_state")
	hooksDiff := computeDiff(oldHooks, newHooks, "pending_hooks")

	// 如果 stateDiff 和 hooksDiff 都为空，直接通过
	if stateDiff == "" && hooksDiff == "" {
		return &ValidationResult{
			Passed:   true,
			Warnings: []ValidationWarning{},
		}, nil
	}

	// 构建 system prompt
	systemPrompt := a.buildValidatorSystemPrompt(language)

	// 构建 user prompt
	userPrompt := a.buildValidatorUserPrompt(chapterContent, chapterNumber, stateDiff, hooksDiff, language, authorityContext)

	// LLM 判定
	temp := 0.1
	response, err := a.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}, &llm.ChatOptions{Temperature: &temp})
	if err != nil {
		// LLM 不可用，直接通过（不阻断流水线）
		return &ValidationResult{
			Passed:   true,
			Warnings: []ValidationWarning{{Category: "validator_error", Description: fmt.Sprintf("LLM call failed: %v", err)}},
		}, nil
	}

	return parseValidationResult(response.Content), nil
}

// Validate 便捷方法：从 bookDir 加载 authority context 后校验。
func (a *StateValidatorAgent) Validate(
	ctx context.Context,
	bookDir string,
	chapterContent string,
	chapterNumber int,
	oldState string,
	newState string,
	oldHooks string,
	newHooks string,
	language string,
) (*ValidationResult, error) {
	// 加载 authority context
	storyDir := filepath.Join(bookDir, "story")
	storyFrame := readFileOrDefault(filepath.Join(storyDir, "outline", "story_frame.md"), "")
	if storyFrame == "" {
		storyFrame = readFileOrDefault(filepath.Join(storyDir, "story_bible.md"), "")
	}
	bookRules := readFileOrDefault(filepath.Join(storyDir, "book_rules.md"), "")
	chapterSummaries := readFileOrDefault(filepath.Join(storyDir, "chapter_summaries.md"), "")

	authCtx := &StateValidationAuthorityContext{
		StoryFrame:       storyFrame,
		BookRules:        bookRules,
		ChapterSummaries: chapterSummaries,
	}

	return a.ValidateTruthFiles(ctx, bookDir, chapterContent, chapterNumber, oldState, newState, oldHooks, newHooks, language, authCtx)
}

// buildValidatorSystemPrompt 构建校验器 system prompt。
func (a *StateValidatorAgent) buildValidatorSystemPrompt(language string) string {
	if language == "en" {
		return `You are a state continuity validator. Your job is to detect contradictions between the chapter text and the updated truth files (current_state.md and pending_hooks.md).

Detect these 6 types of contradictions:
1. State change without chapter support
2. Missing state change (chapter describes something but truth file didn't capture it)
3. Temporal impossibility
4. Hook anomaly (hook resolved but not mentioned in text, or new hook without basis)
5. Retroactive modification
6. Cross-truth key setting conflict

FAIL only for hard contradictions (facts that directly conflict with the chapter text).
Do NOT FAIL for:
- Slightly forward-looking inferences
- Minor omissions in state card
- Reasonable extrapolations from the chapter
- Hook management differences that don't contradict the text

Output format:
- First line: PASS or FAIL (only this word)
- Subsequent lines: one warning per line, optional [category] prefix
- No issues: output only "PASS"`
	}

	return `你是状态连续性校验器。你的工作是检测章节正文与更新后的 truth files（current_state.md 和 pending_hooks.md）之间的矛盾。

检测以下 6 类矛盾：
1. 状态变更无正文支撑
2. 遗漏状态变更（正文描述了某事但 truth file 没捕获）
3. 时间不可能性
4. 伏笔异常（伏笔被标记回收但正文未提及，或新伏笔无依据）
5. 追溯修改
6. 跨真值关键设定冲突

FAIL 只用于硬矛盾（与正文直接冲突的事实）。
不要对以下情况 FAIL：
- 略超前于正文的推断
- state card 未捕获的细节遗漏
- 基于正文的合理外推
- 不与正文矛盾的伏笔管理差异

输出格式：
- 第一行：PASS 或 FAIL（仅此词）
- 后续行：每行一条 warning，可选 [category] 前缀
- 无问题则仅输出：PASS`
}

// buildValidatorUserPrompt 构建校验器 user prompt。
func (a *StateValidatorAgent) buildValidatorUserPrompt(chapterContent string, chapterNumber int, stateDiff string, hooksDiff string, language string, authCtx *StateValidationAuthorityContext) string {
	var sb strings.Builder

	if language == "en" {
		sb.WriteString(fmt.Sprintf("## Chapter %d Text\n\n%s\n\n", chapterNumber, chapterContent))
	} else {
		sb.WriteString(fmt.Sprintf("## 第%d章正文\n\n%s\n\n", chapterNumber, chapterContent))
	}

	if stateDiff != "" {
		sb.WriteString("## State Card Diff\n\n")
		sb.WriteString(stateDiff)
		sb.WriteString("\n\n")
	}
	if hooksDiff != "" {
		sb.WriteString("## Hooks Pool Diff\n\n")
		sb.WriteString(hooksDiff)
		sb.WriteString("\n\n")
	}

	if authCtx != nil {
		sb.WriteString("## Authority / Cross-Truth Context\n\n")
		if authCtx.StoryFrame != "" {
			sb.WriteString("### story_frame excerpt\n")
			sb.WriteString(truncateRunes(authCtx.StoryFrame, 4000))
			sb.WriteString("\n\n")
		}
		if authCtx.BookRules != "" {
			sb.WriteString("### book_rules excerpt\n")
			sb.WriteString(authCtx.BookRules)
			sb.WriteString("\n\n")
		}
		if authCtx.ChapterSummaries != "" {
			sb.WriteString("### recent chapter_summaries excerpt\n")
			sb.WriteString(truncateRunes(authCtx.ChapterSummaries, 4000))
			sb.WriteString("\n\n")
		}
	}

	if language == "en" {
		sb.WriteString("Check if the truth file diffs contradict the chapter text. Output PASS or FAIL with optional warnings.")
	} else {
		sb.WriteString("检查 truth file 的变更是否与正文矛盾。输出 PASS 或 FAIL 及可选的 warnings。")
	}

	return sb.String()
}

// computeDiff 计算行级 diff。
func computeDiff(oldText, newText, label string) string {
	if oldText == newText {
		return ""
	}

	oldLines := filterNonEmptyLines(strings.Split(oldText, "\n"))
	newLines := filterNonEmptyLines(strings.Split(newText, "\n"))

	oldSet := make(map[string]bool)
	for _, line := range oldLines {
		oldSet[line] = true
	}
	newSet := make(map[string]bool)
	for _, line := range newLines {
		newSet[line] = true
	}

	var added, removed []string
	for _, line := range newLines {
		if !oldSet[line] {
			added = append(added, line)
		}
	}
	for _, line := range oldLines {
		if !newSet[line] {
			removed = append(removed, line)
		}
	}

	if len(added) == 0 && len(removed) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### %s\n", label))
	if len(removed) > 0 {
		sb.WriteString("Removed:\n")
		for _, line := range removed {
			sb.WriteString("- " + line + "\n")
		}
	}
	if len(added) > 0 {
		sb.WriteString("Added:\n")
		for _, line := range added {
			sb.WriteString("+ " + line + "\n")
		}
	}
	return sb.String()
}

// parseValidationResult 解析校验结果。
func parseValidationResult(content string) *ValidationResult {
	trimmed := strings.TrimSpace(content)

	// 尝试 JSON 格式
	if strings.HasPrefix(trimmed, "{") {
		if result := tryParseValidationJSON(trimmed); result != nil {
			return result
		}
	}

	// 极简协议解析
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return &ValidationResult{Passed: true, Warnings: []ValidationWarning{}}
	}

	firstLine := strings.TrimSpace(lines[0])
	passed := strings.ToUpper(firstLine) == "PASS"

	var warnings []ValidationWarning
	warningPattern := regexp.MustCompile(`^\[([^\]]+)\]\s*(.*)$`)
	dashPattern := regexp.MustCompile(`^-\s+(.*)$`)

	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		if match := warningPattern.FindStringSubmatch(line); len(match) >= 3 {
			warnings = append(warnings, ValidationWarning{
				Category:    match[1],
				Description: match[2],
			})
			continue
		}

		if match := dashPattern.FindStringSubmatch(line); len(match) >= 2 {
			warnings = append(warnings, ValidationWarning{
				Category:    "general",
				Description: match[1],
			})
			continue
		}

		// 纯文本行
		warnings = append(warnings, ValidationWarning{
			Category:    "general",
			Description: line,
		})
	}

	return &ValidationResult{
		Passed:   passed,
		Warnings: warnings,
	}
}

// tryParseValidationJSON 尝试解析 JSON 格式校验结果。
func tryParseValidationJSON(jsonStr string) *ValidationResult {
	extracted := extractJSON(jsonStr)
	if extracted == "" {
		extracted = jsonStr
	}

	trimmed := strings.TrimSpace(extracted)
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}

	var raw struct {
		Passed   bool `json:"passed"`
		Warnings []struct {
			Category    string `json:"category"`
			Description string `json:"description"`
		} `json:"warnings"`
	}

	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil
	}

	warnings := make([]ValidationWarning, 0, len(raw.Warnings))
	for _, w := range raw.Warnings {
		warnings = append(warnings, ValidationWarning{
			Category:    w.Category,
			Description: w.Description,
		})
	}

	return &ValidationResult{
		Passed:   raw.Passed,
		Warnings: warnings,
	}
}

// filterNonEmptyLines 过滤非空行。
func filterNonEmptyLines(lines []string) []string {
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// BuildStateValidationFeedback 构建校验失败反馈文本。
func BuildStateValidationFeedback(warnings []ValidationWarning, language string) string {
	if len(warnings) == 0 {
		if language == "en" {
			return "The previous settlement contradicted the chapter text. Reconcile truth files strictly to the body."
		}
		return "上一次状态结算与正文矛盾。请严格以正文为准修正 truth files。"
	}

	if language == "en" {
		lines := []string{"The previous settlement failed validation. Fix these contradictions against the chapter body:"}
		for _, w := range warnings {
			lines = append(lines, fmt.Sprintf("- [%s] %s", w.Category, w.Description))
		}
		return strings.Join(lines, "\n")
	}

	lines := []string{"上一次状态结算未通过校验。请对照正文修正以下矛盾："}
	for _, w := range warnings {
		lines = append(lines, fmt.Sprintf("- [%s] %s", w.Category, w.Description))
	}
	return strings.Join(lines, "\n")
}

// BuildStateDegradedIssues 构建降级问题列表。
func BuildStateDegradedIssues(warnings []ValidationWarning, language string) []string {
	var issues []string
	for _, w := range warnings {
		if language == "en" {
			issues = append(issues, fmt.Sprintf("[warning] [%s] %s", w.Category, w.Description))
		} else {
			issues = append(issues, fmt.Sprintf("[warning] [%s] %s", w.Category, w.Description))
		}
	}
	return issues
}
