package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
)

// 审查常量
const (
	FoundationPassThreshold  = 80
	FoundationDimensionFloor = 60
)

// FoundationReviewerAgent 基础设定审查 Agent。
type FoundationReviewerAgent struct {
	BaseAgent
}

// NewFoundationReviewerAgent 创建审查 Agent。
func NewFoundationReviewerAgent(ctx llm.AgentContext) *FoundationReviewerAgent {
	return &FoundationReviewerAgent{BaseAgent: NewBaseAgent(ctx, "foundation-reviewer")}
}

// Review 审查基础设定，返回多维度评分结果。
func (r *FoundationReviewerAgent) Review(
	ctx context.Context,
	foundation *models.ArchitectOutput,
	mode string,
	language string,
) (*models.FoundationReviewResult, error) {
	if foundation == nil {
		return nil, fmt.Errorf("foundation output is nil")
	}

	if mode == "" {
		mode = "original"
	}
	if language == "" {
		language = "zh"
	}

	dimensions := r.buildDimensions(mode, language, 0)
	systemPrompt := r.buildReviewPrompt(dimensions, language)
	userPrompt := r.buildFoundationExcerpt(foundation, language)

	temp := 0.3
	response, err := r.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}, &llm.ChatOptions{Temperature: &temp})
	if err != nil {
		return nil, fmt.Errorf("reviewer LLM call: %w", err)
	}

	return r.parseReviewResult(response.Content, dimensions, language), nil
}

// buildDimensions 根据模式构建审查维度列表。
func (r *FoundationReviewerAgent) buildDimensions(mode string, language string, targetChapters int) []string {
	if mode == "fanfic" || mode == "series" {
		if language == "en" {
			return []string{
				"Canon DNA Preservation",
				"New Narrative Space",
				"Core Conflict",
				"Opening Pacing",
				"Rhythm Feasibility",
			}
		}
		return []string{
			"原作DNA保留",
			"新叙事空间",
			"核心冲突",
			"开篇节奏",
			"节奏可行性",
		}
	}

	if language == "en" {
		return []string{
			"Core Conflict",
			"Opening Pacing",
			"World Consistency",
			"Character Differentiation",
			"Rhythm Feasibility",
		}
	}
	return []string{
		"核心冲突",
		"开篇节奏",
		"世界一致性",
		"角色区分度",
		"节奏可行性",
	}
}

// buildReviewPrompt 构建审查 system prompt。
func (r *FoundationReviewerAgent) buildReviewPrompt(dimensions []string, language string) string {
	var dimLines []string
	for i, dim := range dimensions {
		dimLines = append(dimLines, fmt.Sprintf("%d. %s", i+1, dim))
	}
	dimText := strings.Join(dimLines, "\n")

	if language == "en" {
		return fmt.Sprintf(`You are a senior novel editor reviewing a new book's foundation design.

Score each dimension below (0-100) with specific feedback:

%s

## Scoring Standard
- 80+: Pass, ready to start writing
- 60-79: Has notable issues, needs revision
- <60: Directional error, needs redesign

## Output Format (strictly follow)
=== DIMENSION: 1 ===
Score: {0-100}
Feedback: {specific feedback}
...
=== OVERALL ===
Total: {weighted average}
Pass: {yes/no}
Summary: {1-2 paragraph summary}

Be strict. 80 means "ready to write without changes".`, dimText)
	}

	return fmt.Sprintf(`你是一位资深小说编辑，正在审核一本新书的基础设定。

你需要从以下维度逐项打分（0-100），并给出具体意见：

%s

## 评分标准
- 80+ 通过，可以开始写作
- 60-79 有明显问题，需要修改
- <60 方向性错误，需要重新设计

## 输出格式（严格遵守）
=== DIMENSION: 1 ===
分数：{0-100}
意见：{具体反馈}
...
=== OVERALL ===
总分：{加权平均}
通过：{是/否}
总评：{1-2段总结}

审核时要严格。不要因为"还行"就给高分。80分意味着"可以直接开写，不需要改"。`, dimText)
}

// buildFoundationExcerpt 把 ArchitectOutput 拼成审查 user 消息。
func (r *FoundationReviewerAgent) buildFoundationExcerpt(foundation *models.ArchitectOutput, language string) string {
	storyFrame := foundation.StoryFrame
	if storyFrame == "" {
		storyFrame = foundation.StoryBible
	}
	volumeMap := foundation.VolumeMap
	if volumeMap == "" {
		volumeMap = foundation.VolumeOutline
	}

	var parts []string
	if storyFrame != "" {
		parts = append(parts, "## 世界设定\n"+storyFrame)
	}
	if volumeMap != "" {
		parts = append(parts, "## 卷纲\n"+volumeMap)
	}
	if foundation.BookRules != "" {
		parts = append(parts, "## 规则\n"+foundation.BookRules)
	}
	if foundation.CurrentState != "" {
		parts = append(parts, "## 初始状态\n"+foundation.CurrentState)
	}
	if foundation.PendingHooks != "" {
		parts = append(parts, "## 初始伏笔\n"+foundation.PendingHooks)
	}

	if len(parts) == 0 {
		return "(foundation is empty)"
	}
	return strings.Join(parts, "\n\n")
}

// parseReviewResult 解析 LLM 审查输出为 FoundationReviewResult。
func (r *FoundationReviewerAgent) parseReviewResult(content string, dimensions []string, language string) *models.FoundationReviewResult {
	result := &models.FoundationReviewResult{
		Dimensions: make([]models.ReviewDimension, 0, len(dimensions)),
	}

	// 解析每个维度
	for i, dim := range dimensions {
		dimNum := i + 1
		score, feedback := extractDimensionScore(content, dimNum, language)
		result.Dimensions = append(result.Dimensions, models.ReviewDimension{
			Name:  dim,
			Score: score,
			Note:  feedback,
		})
	}

	// 计算总分（平均分）
	totalScore := 0
	for _, dim := range result.Dimensions {
		totalScore += dim.Score
	}
	if len(result.Dimensions) > 0 {
		result.TotalScore = totalScore / len(result.Dimensions)
	}

	// 解析总评
	result.OverallFeedback = extractOverallSummary(content, language)

	// 判定通过
	anyBelowFloor := false
	for _, dim := range result.Dimensions {
		if dim.Score < FoundationDimensionFloor {
			anyBelowFloor = true
			break
		}
	}
	result.Passed = result.TotalScore >= FoundationPassThreshold && !anyBelowFloor

	return result
}

// extractDimensionScore 提取指定维度的分数和反馈。
func extractDimensionScore(content string, dimNum int, language string) (int, string) {
	// 匹配 === DIMENSION: N === 块
	dimPattern := regexp.MustCompile(fmt.Sprintf("(?s)===\\s*DIMENSION\\s*[:：]\\s*%d\\s*===\\s*(.*?)(?====\\s*(?:DIMENSION|OVERALL)\\s*[=:：]|$)", dimNum))
	match := dimPattern.FindStringSubmatch(content)
	if len(match) < 2 {
		return 50, "(parse failed)"
	}

	block := match[1]

	// 提取分数
	scorePattern := regexp.MustCompile("(?i)(?:分数|Score)\\s*[:：]\\s*(\\d+)")
	scoreMatch := scorePattern.FindStringSubmatch(block)
	score := 50
	if len(scoreMatch) >= 2 {
		if s, err := strconv.Atoi(scoreMatch[1]); err == nil {
			score = s
		}
	}

	// 提取反馈
	feedbackPattern := regexp.MustCompile("(?s)(?:意见|Feedback)\\s*[:：]\\s*(.*)")
	feedbackMatch := feedbackPattern.FindStringSubmatch(block)
	feedback := "(parse failed)"
	if len(feedbackMatch) >= 2 {
		feedback = strings.TrimSpace(feedbackMatch[1])
	}

	return score, feedback
}

// extractOverallSummary 提取总评。
func extractOverallSummary(content string, language string) string {
	overallPattern := regexp.MustCompile("(?s)===\\s*OVERALL\\s*===\\s*(.*)")
	match := overallPattern.FindStringSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	block := match[1]

	// 尝试提取总评/Summary 行
	summaryPattern := regexp.MustCompile("(?s)(?:总评|Summary)\\s*[:：]\\s*(.*)")
	summaryMatch := summaryPattern.FindStringSubmatch(block)
	if len(summaryMatch) >= 2 {
		return strings.TrimSpace(summaryMatch[1])
	}
	return strings.TrimSpace(block)
}

// FormatReviewFeedback 把审查结果格式化为下一轮 ArchitectAgent 的 reviewFeedback。
func FormatReviewFeedback(review *models.FoundationReviewResult, language string) string {
	if review == nil {
		return ""
	}

	var lines []string
	for _, dim := range review.Dimensions {
		lines = append(lines, fmt.Sprintf("维度 %s：%d 分\n意见：%s", dim.Name, dim.Score, dim.Note))
	}
	lines = append(lines, fmt.Sprintf("\n总分：%d（未通过）", review.TotalScore))
	if review.OverallFeedback != "" {
		lines = append(lines, fmt.Sprintf("总评：%s", review.OverallFeedback))
	}

	passedText := "未通过"
	if review.Passed {
		passedText = "通过"
	}
	lines = append(lines, fmt.Sprintf("判定：%s", passedText))

	return strings.Join(lines, "\n\n")
}

// parseReviewResultJSON 尝试从 JSON 格式解析审查结果（兼容路径）。
func parseReviewResultJSON(content string) *models.FoundationReviewResult {
	// 尝试提取 JSON
	jsonStr := extractJSON(content)
	if jsonStr == "" {
		return nil
	}

	var raw struct {
		Passed         bool     `json:"passed"`
		TotalScore     int      `json:"totalScore"`
		OverallFeedback string  `json:"overallFeedback"`
		Dimensions     []struct {
			Name  string `json:"name"`
			Score int    `json:"score"`
			Note  string `json:"note"`
		} `json:"dimensions"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil
	}

	result := &models.FoundationReviewResult{
		Passed:          raw.Passed,
		TotalScore:      raw.TotalScore,
		OverallFeedback: raw.OverallFeedback,
	}
	for _, d := range raw.Dimensions {
		result.Dimensions = append(result.Dimensions, models.ReviewDimension{
			Name:  d.Name,
			Score: d.Score,
			Note:  d.Note,
		})
	}
	return result
}

// extractJSON 从文本中提取 JSON 对象。
func extractJSON(content string) string {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "{") {
		return trimmed
	}

	// 尝试提取 ```json ... ``` 代码块
	codeBlockPattern := regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
	match := codeBlockPattern.FindStringSubmatch(content)
	if len(match) >= 2 {
		return match[1]
	}

	// 尝试提取第一个 { 到最后一个 } 之间的内容
	firstBrace := strings.Index(content, "{")
	lastBrace := strings.LastIndex(content, "}")
	if firstBrace >= 0 && lastBrace > firstBrace {
		return content[firstBrace : lastBrace+1]
	}
	return ""
}
