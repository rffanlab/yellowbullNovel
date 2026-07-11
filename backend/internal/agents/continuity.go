package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
)

// ContinuityAuditor 连续性审计 Agent。
type ContinuityAuditor struct {
	BaseAgent
}

// NewContinuityAuditor 创建连续性审计 Agent。
func NewContinuityAuditor(ctx llm.AgentContext) *ContinuityAuditor {
	return &ContinuityAuditor{BaseAgent: NewBaseAgent(ctx, "continuity-auditor")}
}

// AuditChapterInput 审计输入。
type AuditChapterInput struct {
	BookDir       string
	Content       string
	ChapterNumber int
	Genre         string
}

// AuditChapter 执行 33+ 维度质量审查。
func (a *ContinuityAuditor) AuditChapter(ctx context.Context, input *AuditChapterInput) (*models.AuditResult, error) {
	storyDir := filepath.Join(input.BookDir, "story")

	// 加载 truth files
	currentState := readFileOrDefault(filepath.Join(storyDir, "current_state.md"), "")
	hooks := readFileOrDefault(filepath.Join(storyDir, "pending_hooks.md"), "")
	chapterSummaries := readFileOrDefault(filepath.Join(storyDir, "chapter_summaries.md"), "")
	storyFrame := readFileOrDefault(filepath.Join(storyDir, "outline", "story_frame.md"), "")
	if storyFrame == "" {
		storyFrame = readFileOrDefault(filepath.Join(storyDir, "story_bible.md"), "")
	}
	bookRules := readFileOrDefault(filepath.Join(storyDir, "book_rules.md"), "")
	subplotBoard := readFileOrDefault(filepath.Join(storyDir, "subplot_board.md"), "")
	emotionalArcs := readFileOrDefault(filepath.Join(storyDir, "emotional_arcs.md"), "")
	characterMatrix := readFileOrDefault(filepath.Join(storyDir, "character_matrix.md"), "")

	// 构建审查维度列表
	dimensions := buildAuditDimensions()

	// 构建 system prompt
	systemPrompt := a.buildAuditorSystemPrompt(dimensions)

	// 构建 user prompt
	userPrompt := a.buildAuditorUserPrompt(input, currentState, hooks, chapterSummaries, storyFrame, bookRules, subplotBoard, emotionalArcs, characterMatrix)

	// LLM 审计
	temp := 0.3
	response, err := a.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}, &llm.ChatOptions{Temperature: &temp})
	if err != nil {
		return nil, fmt.Errorf("auditor LLM call: %w", err)
	}

	// 解析 JSON 输出
	result := parseAuditResult(response.Content)

	// 附带 token usage
	if response.TokenUsage != nil {
		result.TokenUsage = &models.TokenUsage{
			PromptTokens:     response.TokenUsage.PromptTokens,
			CompletionTokens: response.TokenUsage.CompletionTokens,
			TotalTokens:      response.TokenUsage.TotalTokens,
		}
	}

	return result, nil
}

// buildAuditorSystemPrompt 构建审计 system prompt。
func (a *ContinuityAuditor) buildAuditorSystemPrompt(dimensions []string) string {
	var dimLines []string
	for i, dim := range dimensions {
		dimLines = append(dimLines, fmt.Sprintf("%d. %s", i+1, dim))
	}

	return fmt.Sprintf(`你是一位资深小说审稿编辑，正在审核一章网络小说的完成度和结构连续性。

你需要从以下 %d 个维度逐项检查，发现问题后给出 severity（critical/warning/info）和 repairScope（local/structural/unknown）：

%s

## 评分校准
- 95-100：可直接发布
- 85-94：有小瑕疵但整体流畅
- 75-84：有明显问题但主干完整
- 65-74：多处影响阅读的问题
- <65：结构性问题，需要大幅重写

## 审稿边界
- 只审完成度 + 结构，不审文笔
- 文笔问题只能标 severity="info"
- 稀疏 memo 合法（喘息章/后效章），不判 incomplete

## 输出格式（严格 JSON）
{
  "passed": true/false,
  "overallScore": 0-100,
  "summary": "一句话审计结论",
  "issues": [
    {
      "severity": "critical/warning/info",
      "category": "维度名称",
      "description": "具体问题描述",
      "suggestion": "修改建议",
      "repairScope": "local/structural/unknown"
    }
  ]
}

passed 判定：无 critical 级问题则 passed=true。`, len(dimensions), strings.Join(dimLines, "\n"))
}

// buildAuditorUserPrompt 构建审计 user prompt。
func (a *ContinuityAuditor) buildAuditorUserPrompt(input *AuditChapterInput, currentState, hooks, summaries, storyFrame, bookRules, subplots, arcs, matrix string) string {
	return fmt.Sprintf(`## 待审章节（第%d章）

%s

## 当前状态卡
%s

## 伏笔池
%s

## 章节摘要
%s

## 世界设定
%s

## 本书规则
%s

## 支线进度板
%s

## 情感弧线
%s

## 角色矩阵
%s

请严格按 JSON 格式输出审计结果。`,
		input.ChapterNumber,
		input.Content,
		currentState,
		hooks,
		summaries,
		storyFrame,
		bookRules,
		subplots,
		arcs,
		matrix)
}

// buildAuditDimensions 构建 33+ 审查维度列表。
func buildAuditDimensions() []string {
	return []string{
		"OOC检查",
		"时间线检查",
		"设定冲突",
		"战力崩坏",
		"数值检查",
		"伏笔检查",
		"节奏检查",
		"文风检查",
		"信息越界",
		"词汇疲劳",
		"利益链断裂",
		"年代考据",
		"配角降智",
		"配角工具人化",
		"爽点虚化",
		"台词失真",
		"流水账",
		"知识库污染",
		"视角一致性",
		"段落等长",
		"套话密度",
		"公式化转折",
		"列表式结构",
		"支线停滞",
		"弧线平坦",
		"节奏单调",
		"敏感词检查",
		"正传事件冲突",
		"未来信息泄露",
		"世界规则跨书一致性",
		"番外伏笔隔离",
		"读者期待管理",
		"章节备忘偏离",
		"角色还原度",
		"世界规则遵守",
		"关系动态",
		"正典事件一致性",
	}
}

// parseAuditResult 解析审计输出（多策略容错）。
func parseAuditResult(content string) *models.AuditResult {
	// 策略1: 提取 JSON 对象
	if balanced := extractBalancedJSON(content); balanced != "" {
		if result := tryParseAuditJSON(balanced); result != nil {
			return result
		}
	}

	// 策略2: 整个内容作为 JSON
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "{") {
		if result := tryParseAuditJSON(trimmed); result != nil {
			return result
		}
	}

	// 策略3: 提取 ```json 代码块
	codeBlockPattern := regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")
	if match := codeBlockPattern.FindStringSubmatch(content); len(match) >= 2 {
		if result := tryParseAuditJSON(match[1]); result != nil {
			return result
		}
	}

	// 策略4: 全部失败
	return &models.AuditResult{
		Passed:      false,
		ParseFailed: true,
		Issues: []models.AuditIssue{
			{
				Severity:   models.SeverityCritical,
				Category:   "系统错误",
				Description: "审稿输出解析失败",
				Suggestion:  "请重试或检查 LLM 输出格式",
			},
		},
		Summary: "审稿输出解析失败",
	}
}

// tryParseAuditJSON 尝试解析审计 JSON。
func tryParseAuditJSON(jsonStr string) *models.AuditResult {
	var raw struct {
		Passed       bool     `json:"passed"`
		OverallScore int      `json:"overallScore"`
		Summary      string   `json:"summary"`
		Issues       []struct {
			Severity     string `json:"severity"`
			Category     string `json:"category"`
			Description  string `json:"description"`
			Suggestion   string `json:"suggestion"`
			RepairScope  string `json:"repairScope"`
		} `json:"issues"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil
	}

	result := &models.AuditResult{
		Passed:  raw.Passed,
		Summary: raw.Summary,
		Issues:  make([]models.AuditIssue, 0, len(raw.Issues)),
	}
	if raw.OverallScore > 0 {
		score := raw.OverallScore
		result.OverallScore = &score
	}

	for _, issue := range raw.Issues {
		ai := models.AuditIssue{
			Severity:    models.AuditSeverity(issue.Severity),
			Category:    issue.Category,
			Description: issue.Description,
			Suggestion:  issue.Suggestion,
		}
		if issue.RepairScope != "" {
			rs := models.RepairScope(issue.RepairScope)
			ai.RepairScope = &rs
		}
		result.Issues = append(result.Issues, ai)
	}

	return result
}

// extractBalancedJSON 提取平衡的 JSON 对象。
func extractBalancedJSON(content string) string {
	firstBrace := strings.Index(content, "{")
	if firstBrace < 0 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false

	for i := firstBrace; i < len(content); i++ {
		ch := content[i]

		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				return content[firstBrace : i+1]
			}
		}
	}
	return ""
}
