package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
)

// WriterAgent 章节写手 Agent。
type WriterAgent struct {
	BaseAgent
}

// NewWriterAgent 创建写手 Agent。
func NewWriterAgent(ctx llm.AgentContext) *WriterAgent {
	return &WriterAgent{BaseAgent: NewBaseAgent(ctx, "writer")}
}

// WriteChapter 是章节写作流程的入口。
// 完成 Phase 1（创作正文）+ Phase 2（状态结算）+ 写后校验，返回 WriteChapterOutput。
func (a *WriterAgent) WriteChapter(ctx context.Context, input *models.WriteChapterInput) (*models.WriteChapterOutput, error) {
	if input.BookDir == "" {
		return nil, fmt.Errorf("book dir is empty")
	}

	language := input.Book.Language
	if language == "" {
		language = "zh"
	}

	// 步骤1: 加载全部上下文
	wctx := a.loadAllContext(input.BookDir, language)

	// 步骤2: 构建 system prompt
	systemPrompt := a.buildWriterSystemPrompt(input, wctx, language)

	// 步骤3: 构建 user prompt + LLM 生成
	userPrompt := a.buildWriterUserPrompt(input, wctx, language)

	temp := 0.7
	if input.TemperatureOverride != nil {
		temp = *input.TemperatureOverride
	}

	response, err := a.ChatStream(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}, &llm.ChatOptions{Temperature: &temp}, nil)
	if err != nil {
		return nil, fmt.Errorf("writer LLM call: %w", err)
	}

	// 步骤4: 解析创作输出
	creative := parseCreativeOutput(input.ChapterNumber, response.Content)

	// Phase 2: Settler 状态结算
	settlement := a.settleChapterState(ctx, input, creative, wctx, language)

	// 写后校验
	postWriteErrors, postWriteWarnings := a.validatePostWrite(creative.Content, wctx, language)

	// 构建 token usage
	var tokenUsage *models.TokenUsage
	if response.TokenUsage != nil {
		tokenUsage = &models.TokenUsage{
			PromptTokens:     response.TokenUsage.PromptTokens,
			CompletionTokens: response.TokenUsage.CompletionTokens,
			TotalTokens:      response.TokenUsage.TotalTokens,
		}
	}

	return &models.WriteChapterOutput{
		ChapterNumber:         input.ChapterNumber,
		Title:                 creative.Title,
		Content:               creative.Content,
		WordCount:             creative.WordCount,
		PreWriteCheck:         creative.PreWriteCheck,
		PostSettlement:        settlement.PostSettlement,
		UpdatedState:          settlement.UpdatedState,
		UpdatedLedger:         settlement.UpdatedLedger,
		UpdatedHooks:          settlement.UpdatedHooks,
		ChapterSummary:        settlement.ChapterSummary,
		UpdatedSubplots:       settlement.UpdatedSubplots,
		UpdatedEmotionalArcs:  settlement.UpdatedEmotionalArcs,
		UpdatedCharacterMatrix: settlement.UpdatedCharacterMatrix,
		PostWriteErrors:       postWriteErrors,
		PostWriteWarnings:     postWriteWarnings,
		TokenUsage:            tokenUsage,
	}, nil
}

// SaveChapter 将 WriteChapterOutput 持久化到磁盘。
func (a *WriterAgent) SaveChapter(bookDir string, output *models.WriteChapterOutput) error {
	chaptersDir := filepath.Join(bookDir, "chapters")
	if err := os.MkdirAll(chaptersDir, 0755); err != nil {
		return fmt.Errorf("create chapters dir: %w", err)
	}

	// 写入章节文件
	chapterFile := filepath.Join(chaptersDir, fmt.Sprintf("%04d.md", output.ChapterNumber))
	content := fmt.Sprintf("# %s\n\n%s", output.Title, output.Content)
	if err := writeFileSafe(chapterFile, content); err != nil {
		return fmt.Errorf("write chapter file: %w", err)
	}

	// 写入 truth files
	storyDir := filepath.Join(bookDir, "story")
	if output.UpdatedState != "" {
		if err := writeFileSafe(filepath.Join(storyDir, "current_state.md"), output.UpdatedState); err != nil {
			return err
		}
	}
	if output.UpdatedHooks != "" {
		if err := writeFileSafe(filepath.Join(storyDir, "pending_hooks.md"), output.UpdatedHooks); err != nil {
			return err
		}
	}
	if output.ChapterSummary != "" {
		summariesPath := filepath.Join(storyDir, "chapter_summaries.md")
		existing := readFileOrDefault(summariesPath, "")
		var newContent string
		if existing != "" {
			newContent = existing + "\n" + output.ChapterSummary
		} else {
			newContent = "# 章节摘要\n\n| 章节 | 标题 | 摘要 | 字数 | 状态 |\n|------|------|------|------|------|\n" + output.ChapterSummary
		}
		if err := writeFileSafe(summariesPath, newContent); err != nil {
			return err
		}
	}
	if output.UpdatedSubplots != "" {
		if err := writeFileSafe(filepath.Join(storyDir, "subplot_board.md"), output.UpdatedSubplots); err != nil {
			return err
		}
	}
	if output.UpdatedEmotionalArcs != "" {
		if err := writeFileSafe(filepath.Join(storyDir, "emotional_arcs.md"), output.UpdatedEmotionalArcs); err != nil {
			return err
		}
	}
	if output.UpdatedCharacterMatrix != "" {
		if err := writeFileSafe(filepath.Join(storyDir, "character_matrix.md"), output.UpdatedCharacterMatrix); err != nil {
			return err
		}
	}

	return nil
}

// writerContext 写手加载的全部上下文。
type writerContext struct {
	StoryBible        string
	VolumeOutline     string
	StyleGuide        string
	CurrentState      string
	Ledger            string
	Hooks             string
	ChapterSummaries  string
	SubplotBoard      string
	EmotionalArcs     string
	CharacterMatrix   string
	StyleProfileRaw   string
	ParentCanon       string
	FanficCanon       string
	RecentChapters    string
	BookRules         string
	HasParentCanon    bool
	HasFanficCanon    bool
}

// loadAllContext 加载全部上下文（13 个文件源）。
func (a *WriterAgent) loadAllContext(bookDir string, language string) *writerContext {
	storyDir := filepath.Join(bookDir, "story")
	placeholder := "(文件尚未创建)"

	wctx := &writerContext{
		StoryBible:       readFileOrDefault(filepath.Join(storyDir, "outline", "story_frame.md"), readFileOrDefault(filepath.Join(storyDir, "story_bible.md"), placeholder)),
		VolumeOutline:    readFileOrDefault(filepath.Join(storyDir, "outline", "volume_map.md"), readFileOrDefault(filepath.Join(storyDir, "volume_outline.md"), placeholder)),
		StyleGuide:       readFileOrDefault(filepath.Join(storyDir, "style_guide.md"), ""),
		CurrentState:     readFileOrDefault(filepath.Join(storyDir, "current_state.md"), placeholder),
		Ledger:           readFileOrDefault(filepath.Join(storyDir, "particle_ledger.md"), ""),
		Hooks:            readFileOrDefault(filepath.Join(storyDir, "pending_hooks.md"), ""),
		ChapterSummaries: readFileOrDefault(filepath.Join(storyDir, "chapter_summaries.md"), ""),
		SubplotBoard:     readFileOrDefault(filepath.Join(storyDir, "subplot_board.md"), ""),
		EmotionalArcs:    readFileOrDefault(filepath.Join(storyDir, "emotional_arcs.md"), ""),
		CharacterMatrix:  readFileOrDefault(filepath.Join(storyDir, "character_matrix.md"), ""),
		StyleProfileRaw:  readFileOrDefault(filepath.Join(storyDir, "style_profile.json"), ""),
		ParentCanon:      readFileOrDefault(filepath.Join(storyDir, "parent_canon.md"), ""),
		FanficCanon:      readFileOrDefault(filepath.Join(storyDir, "fanfic_canon.md"), ""),
		BookRules:        readFileOrDefault(filepath.Join(storyDir, "book_rules.md"), ""),
	}
	wctx.HasParentCanon = wctx.ParentCanon != ""
	wctx.HasFanficCanon = wctx.FanficCanon != ""

	return wctx
}

// buildWriterSystemPrompt 构建写手 system prompt。
func (a *WriterAgent) buildWriterSystemPrompt(input *models.WriteChapterInput, wctx *writerContext, language string) string {
	var sections []string

	// 1. 角色设定
	if language == "en" {
		sections = append(sections, fmt.Sprintf("You are a professional novel writer. You write for the %s platform.", input.Book.Platform))
	} else {
		sections = append(sections, fmt.Sprintf("你是一位专业的网络小说作家。你为%s平台写作。", input.Book.Platform))
	}

	// 2. 核心规则
	if language == "en" {
		sections = append(sections, a.buildCoreRulesEN())
	} else {
		sections = append(sections, a.buildCoreRulesZH())
	}

	// 3. 输入治理契约（governed 模式）
	if input.ChapterMemo != nil && input.ContextPackage != nil && input.RuleStack != nil {
		sections = append(sections, a.buildGovernedInputContract(language))
		sections = append(sections, a.buildChapterMemoContract(language))
	}

	// 4. 字数指导
	if input.LengthSpec != nil {
		sections = append(sections, fmt.Sprintf("## 字数指导\n目标：%d字，允许区间：%d-%d字\n", input.LengthSpec.Target, input.LengthSpec.Min, input.LengthSpec.Max))
	}

	// 5. 黄金开篇纪律
	if input.ChapterNumber <= 3 {
		sections = append(sections, a.buildGoldenOpeningDiscipline(input.ChapterNumber, language))
	}

	// 6. 输出格式
	sections = append(sections, a.buildOutputFormat(input, language))

	return strings.Join(sections, "\n\n")
}

// buildWriterUserPrompt 构建写手 user prompt。
func (a *WriterAgent) buildWriterUserPrompt(input *models.WriteChapterInput, wctx *writerContext, language string) string {
	var parts []string

	// governed 路径
	if input.ChapterMemo != nil && input.ContextPackage != nil {
		parts = append(parts, "## 章节备忘\n"+input.ChapterMemo.Body)
		parts = append(parts, "## 已选上下文\n"+renderContextPackageExcerpt(input.ContextPackage))
	} else {
		// legacy 路径
		parts = append(parts, "## 当前状态卡\n"+wctx.CurrentState)
		if wctx.Ledger != "" {
			parts = append(parts, "## 资源账本\n"+wctx.Ledger)
		}
		if wctx.Hooks != "" {
			parts = append(parts, "## 伏笔池\n"+wctx.Hooks)
		}
		if wctx.ChapterSummaries != "" {
			parts = append(parts, "## 章节摘要\n"+wctx.ChapterSummaries)
		}
		if wctx.SubplotBoard != "" {
			parts = append(parts, "## 支线进度板\n"+wctx.SubplotBoard)
		}
		if wctx.EmotionalArcs != "" {
			parts = append(parts, "## 情感弧线\n"+wctx.EmotionalArcs)
		}
		if wctx.CharacterMatrix != "" {
			parts = append(parts, "## 角色矩阵\n"+wctx.CharacterMatrix)
		}
		parts = append(parts, "## 世界观设定\n"+wctx.StoryBible)
		parts = append(parts, "## 卷纲\n"+wctx.VolumeOutline)
	}

	if input.ExternalContext != "" {
		parts = append(parts, "## 本章用户指令\n"+input.ExternalContext)
	}
	if input.ChapterIntent != "" {
		parts = append(parts, "## 章节意图\n"+input.ChapterIntent)
	}
	if wctx.BookRules != "" {
		parts = append(parts, "## 本书规则\n"+wctx.BookRules)
	}
	if input.LengthSpec != nil {
		parts = append(parts, fmt.Sprintf("## 字数要求\n目标%d字，允许区间%d-%d字", input.LengthSpec.Target, input.LengthSpec.Min, input.LengthSpec.Max))
	}

	return strings.Join(parts, "\n\n")
}

// buildCoreRulesZH 构建中文核心规则。
func (a *WriterAgent) buildCoreRulesZH() string {
	return `## 核心规则
1. 手机阅读节奏：段落3-5行，每300字一个小看点
2. 伏笔呼应：每章章尾留钩，每3-5章推进一次
3. 人设铁律：角色行为由过往经历+当前利益+性格底色驱动
4. 叙事技法：Show don't tell，用细节堆出真实
5. 看点密度：每300字一个小爽点
6. 80-20断章：章末20%处放悬念或反转
7. 逻辑自洽：不降智、不圣母、不机械降神
8. 语言约束：去AI味，消灭"不禁""仿佛""宛如"等标记词
9. 硬性禁令：禁止"不是…而是…"句式、禁止破折号"——"`
}

// buildCoreRulesEN 构建英文核心规则。
func (a *WriterAgent) buildCoreRulesEN() string {
	return `## Core Rules
1. Mobile reading rhythm: paragraphs 3-5 lines, one hook per 300 chars
2. Foreshadowing: every chapter ends with a hook, advance every 3-5 chapters
3. Character consistency: behavior driven by past experience + current interest + personality
4. Show don't tell: use concrete details
5. Payoff density: one small payoff per 300 chars
6. 80-20 chapter break: suspense or twist in the last 20%
7. Logical consistency: no dumb characters, no deus ex machina
8. Language: avoid AI-tell words and formulaic patterns
9. Prohibitions: no "not...but..." constructions, no em-dashes`
}

// buildGovernedInputContract 构建输入治理契约。
func (a *WriterAgent) buildGovernedInputContract(language string) string {
	if language == "en" {
		return `## Input Governance Contract
- What to write in this chapter follows the chapter intent and composed context package.
- The volume outline is the default plan, not the highest-level rule.
- When the runtime rule stack records an L4 -> L3 active override, execute the current task intent first.
- The only hard guardrails: world settings, continuity facts, explicit prohibitions.`
	}
	return `## 输入治理契约
- 本章具体写什么，以提供给你的 chapter intent 和 composed context package 为准。
- 卷纲是默认规划，不是全局最高规则。
- 当 runtime rule stack 明确记录了 L4 -> L3 的 active override 时，优先执行当前任务意图。
- 真正不能突破的只有硬护栏：世界设定、连续性事实、显式禁令。`
}

// buildChapterMemoContract 构建章节备忘对齐契约。
func (a *WriterAgent) buildChapterMemoContract(language string) string {
	return `## 章节备忘对齐
你将收到本章的 chapter_memo，由 7 段 markdown 组成：
- ## 当前任务 → 本章必须完成的具体动作
- ## 读者此刻在等什么 → 控制情绪缺口的制造/延迟/兑现
- ## 该兑现的 / 暂不掀的 → 必须兑现的伏笔 + 必须压住的底牌
- ## 日常/过渡承担什么任务 → 非冲突段落的功能映射
- ## 关键抉择过三连问 → 关键人物选择的检查
- ## 章尾必须发生的改变 → 结尾落地的1-3条改变
- ## 本章 hook 账 → advance/resolve 的 hook_id 必须有具体兑现段
- ## 不要做 → 硬约束红线`
}

// buildGoldenOpeningDiscipline 构建黄金开篇纪律。
func (a *WriterAgent) buildGoldenOpeningDiscipline(chapterNumber int, language string) string {
	return fmt.Sprintf(`## 黄金三章写作纪律 — 第 %d 章
这是开篇三章中的第 %d 章——你写出的每一句话都直接决定读者是否留下来。
第1章：主角出场800字以内必须触发主线冲突。前300字最后一句必须带戏剧性反转。
第2章：金手指必须"做出来"——具体使用事件，而非旁白介绍。
第3章：让主角下一个可量化的短期目标浮上水面。
段落3-5行，场景≤3个，人物≤3个，信息分层强制。`, chapterNumber, chapterNumber)
}

// buildOutputFormat 构建输出格式段。
func (a *WriterAgent) buildOutputFormat(input *models.WriteChapterInput, language string) string {
	target := 0
	if input.LengthSpec != nil {
		target = input.LengthSpec.Target
	}
	if input.WordCountOverride != nil && *input.WordCountOverride > 0 {
		target = *input.WordCountOverride
	}
	if target == 0 {
		target = input.Book.ChapterWordCount
	}

	return fmt.Sprintf(`## 输出格式（严格遵守）

=== PRE_WRITE_CHECK ===
（必须输出Markdown表格，全部检查项对齐 chapter_memo 七段）
| 检查项 | 本章记录 | 备注 |
|--------|----------|------|
| 当前任务 | 复述并写出执行动作 | 必须具体 |
| 章尾改变 | 列出1-3条具体改变 | 必须落地 |
| 章节类型 | 主线推进/支线/过渡/高潮/收束 | |
| 风险扫描 | OOC/信息越界/设定冲突/节奏 | |

=== CHAPTER_TITLE ===
(章节标题，不含"第X章")

=== CHAPTER_CONTENT ===
(正文内容，目标%d字)

【重要】本次只需输出以上三个区块。状态卡、伏笔池等追踪文件将由后续结算阶段处理。`, target)
}

// ============================================================================
// CreativeOutput 解析
// ============================================================================

// CreativeOutput 创作阶段输出。
type CreativeOutput struct {
	Title         string
	Content       string
	WordCount     int
	PreWriteCheck string
}

// parseCreativeOutput 解析 LLM 创作输出，提取标题+正文。
func parseCreativeOutput(chapterNumber int, content string) *CreativeOutput {
	extract := func(tag string) string {
		re := regexp.MustCompile(fmt.Sprintf("=== %s ===\\s*([\\s\\S]*?)(?==== [A-Z_]+ ===|$)", tag))
		match := re.FindStringSubmatch(content)
		if len(match) > 1 {
			return strings.TrimSpace(match[1])
		}
		return ""
	}

	chapterContent := extract("CHAPTER_CONTENT")
	if chapterContent == "" {
		chapterContent = fallbackExtractContent(content)
	}

	title := extract("CHAPTER_TITLE")
	if title == "" {
		title = fallbackExtractTitle(content, chapterNumber)
	}

	preWriteCheck := extract("PRE_WRITE_CHECK")

	return &CreativeOutput{
		Title:         title,
		Content:       chapterContent,
		WordCount:     countChapterLength(chapterContent),
		PreWriteCheck: preWriteCheck,
	}
}

// fallbackExtractContent 回退内容提取。
func fallbackExtractContent(content string) string {
	// 尝试 # 第N章 标题格式后的内容
	re := regexp.MustCompile("(?s)#{1,3}\\s+第\\d+\\s*章.*?\\n\\s*(.*)")
	if match := re.FindStringSubmatch(content); len(match) >= 2 {
		return strings.TrimSpace(match[1])
	}
	// 尝试 正文： 标签
	re = regexp.MustCompile("(?s)正文[：:]\\s*(.*)")
	if match := re.FindStringSubmatch(content); len(match) >= 2 {
		return strings.TrimSpace(match[1])
	}
	// 取最长 prose block
	return strings.TrimSpace(content)
}

// fallbackExtractTitle 回退标题提取。
func fallbackExtractTitle(content string, chapterNumber int) string {
	re := regexp.MustCompile("#{1,3}\\s+第\\d+\\s*章\\s*(.+)")
	if match := re.FindStringSubmatch(content); len(match) >= 2 {
		return strings.TrimSpace(match[1])
	}
	re = regexp.MustCompile("章节标题[：:]\\s*(.+)")
	if match := re.FindStringSubmatch(content); len(match) >= 2 {
		return strings.TrimSpace(match[1])
	}
	return fmt.Sprintf("第%d章", chapterNumber)
}

// countChapterLength 计算章节字数（中文按字符数）。
func countChapterLength(content string) int {
	return len([]rune(content))
}

// ============================================================================
// Settler 状态结算（Observer + Reflector）
// ============================================================================

// settlementResult 结算结果。
type settlementResult struct {
	PostSettlement        string
	UpdatedState          string
	UpdatedLedger         string
	UpdatedHooks          string
	ChapterSummary        string
	UpdatedSubplots       string
	UpdatedEmotionalArcs  string
	UpdatedCharacterMatrix string
}

// settleChapterState 执行状态结算（Observer + Reflector 双 Agent）。
func (a *WriterAgent) settleChapterState(ctx context.Context, input *models.WriteChapterInput, creative *CreativeOutput, wctx *writerContext, language string) *settlementResult {
	// Phase 2a: Observer 提取事实
	observations := a.observe(ctx, input, creative, wctx, language)

	// Phase 2b: Reflector 回写
	return a.reflect(ctx, input, creative, wctx, observations, language)
}

// observe Observer 提取事实。
func (a *WriterAgent) observe(ctx context.Context, input *models.WriteChapterInput, creative *CreativeOutput, wctx *writerContext, language string) string {
	systemPrompt := `你是事实观察员。从章节正文中提取9类事实，宁多勿少。
只从正文提取，不推测。输出格式：
=== OBSERVATIONS ===
[角色行为] - 角色: 行为/状态变化
[位置变化] - 角色从A到B
[资源变化] - 角色获得/失去物品
[关系变化] - 角色A→角色B: 变化
[情绪变化] - 角色: 之前→之后
[信息流动] - 角色得知/仍不知
[剧情线索] - 新埋/推进/回收
[时间] - 时间标记
[身体状态] - 角色: 状态`

	userPrompt := fmt.Sprintf("## 第%d章：%s\n\n%s", input.ChapterNumber, creative.Title, creative.Content)

	temp := 0.5
	response, err := a.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}, &llm.ChatOptions{Temperature: &temp})
	if err != nil {
		a.LogWarn(fmt.Sprintf("observer LLM call failed: %v", err))
		return "(observer failed)"
	}
	return response.Content
}

// reflect Reflector 状态回写。
func (a *WriterAgent) reflect(ctx context.Context, input *models.WriteChapterInput, creative *CreativeOutput, wctx *writerContext, observations string, language string) *settlementResult {
	systemPrompt := `你是状态追踪分析师。基于观察日志和当前 truth files，生成增量更新。
铁律：只记录正文中实际发生的事，不推断/不预测。

输出格式：
=== POST_SETTLEMENT ===
（简要说明本章状态变动）

=== UPDATED_STATE ===
（更新后的完整状态卡）

=== UPDATED_HOOKS ===
（更新后的完整伏笔池）

=== CHAPTER_SUMMARY ===
（本章摘要行：| 章节号 | 标题 | 摘要 | 字数 | 状态 |）

=== UPDATED_SUBPLOTS ===
（更新后的支线进度板）

=== UPDATED_EMOTIONAL_ARCS ===
（更新后的情感弧线）

=== UPDATED_CHARACTER_MATRIX ===
（更新后的角色交互矩阵）`

	userPrompt := fmt.Sprintf(`## 观察日志
%s

## 本章正文
第%d章：%s
%s

## 当前状态卡
%s

## 当前伏笔池
%s

## 章节摘要
%s

## 支线进度板
%s

## 情感弧线
%s

请严格按照 === TAG === 格式输出结算结果。`,
		observations,
		input.ChapterNumber, creative.Title, creative.Content,
		wctx.CurrentState,
		wctx.Hooks,
		wctx.ChapterSummaries,
		wctx.SubplotBoard,
		wctx.EmotionalArcs)

	temp := 0.3
	response, err := a.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}, &llm.ChatOptions{Temperature: &temp})
	if err != nil {
		a.LogWarn(fmt.Sprintf("reflector LLM call failed: %v", err))
		return &settlementResult{}
	}

	return parseSettlementOutput(response.Content)
}

// parseSettlementOutput 解析结算输出。
func parseSettlementOutput(content string) *settlementResult {
	extract := func(tag string) string {
		re := regexp.MustCompile(fmt.Sprintf("=== %s ===\\s*([\\s\\S]*?)(?==== [A-Z_]+ ===|$)", tag))
		match := re.FindStringSubmatch(content)
		if len(match) > 1 {
			return strings.TrimSpace(match[1])
		}
		return ""
	}

	return &settlementResult{
		PostSettlement:         extract("POST_SETTLEMENT"),
		UpdatedState:           extract("UPDATED_STATE"),
		UpdatedLedger:          extract("UPDATED_LEDGER"),
		UpdatedHooks:           extract("UPDATED_HOOKS"),
		ChapterSummary:         extract("CHAPTER_SUMMARY"),
		UpdatedSubplots:        extract("UPDATED_SUBPLOTS"),
		UpdatedEmotionalArcs:   extract("UPDATED_EMOTIONAL_ARCS"),
		UpdatedCharacterMatrix: extract("UPDATED_CHARACTER_MATRIX"),
	}
}

// ============================================================================
// 写后校验
// ============================================================================

// validatePostWrite 写后校验（零 LLM 成本）。
func (a *WriterAgent) validatePostWrite(content string, wctx *writerContext, language string) ([]models.PostWriteViolation, []models.PostWriteViolation) {
	var violations []models.PostWriteViolation

	// 1. 禁止"不是…而是…"句式
	if regexp.MustCompile("不是.*?而是").MatchString(content) {
		violations = append(violations, models.PostWriteViolation{
			Rule:        "禁止句式",
			Description: "正文中出现\"不是…而是…\"句式",
			Suggestion:  "改用直接陈述",
			Severity:    "error",
		})
	}

	// 2. 禁止破折号
	if strings.Contains(content, "——") {
		violations = append(violations, models.PostWriteViolation{
			Rule:        "禁止破折号",
			Description: "正文中出现破折号\"——\"",
			Suggestion:  "改用逗号或其他标点",
			Severity:    "error",
		})
	}

	// 3. 转折词密度
	fatigueWords := []string{"仿佛", "忽然", "竟", "竟然", "猛地", "猛然", "不禁", "宛如"}
	for _, word := range fatigueWords {
		count := strings.Count(content, word)
		if count > 1 {
			violations = append(violations, models.PostWriteViolation{
				Rule:        "转折词密度",
				Description: fmt.Sprintf("\"%s\"出现%d次（每3000字≤1次）", word, count),
				Suggestion:  "减少该词的使用频率",
				Severity:    "warning",
			})
		}
	}

	// 4. 段落过长
	paragraphs := strings.Split(content, "\n\n")
	longParaCount := 0
	for _, p := range paragraphs {
		if len([]rune(p)) > 300 {
			longParaCount++
		}
	}
	if longParaCount >= 2 {
		violations = append(violations, models.PostWriteViolation{
			Rule:        "段落过长",
			Description: fmt.Sprintf("%d个段落超过300字", longParaCount),
			Suggestion:  "拆分长段落为3-5行",
			Severity:    "warning",
		})
	}

	// 5. 章节号指称
	if regexp.MustCompile("第\\d+章|chapter\\s+\\d+").MatchString(content) {
		violations = append(violations, models.PostWriteViolation{
			Rule:        "章节号指称",
			Description: "正文中出现\"第N章\"/\"chapter N\"",
			Suggestion:  "删除章节号指称",
			Severity:    "error",
		})
	}

	// 6. 作者说教
	authorVoice := []string{"显然", "毋庸置疑", "不言而喻", "众所周知", "不难看出"}
	for _, word := range authorVoice {
		if strings.Contains(content, word) {
			violations = append(violations, models.PostWriteViolation{
				Rule:        "作者说教",
				Description: fmt.Sprintf("出现说教词\"%s\"", word),
				Suggestion:  "用行动和细节代替直接陈述",
				Severity:    "warning",
			})
		}
	}

	// 7. 集体反应
	collectiveWords := []string{"全场震惊", "众人惊呆", "一片哗然"}
	for _, word := range collectiveWords {
		if strings.Contains(content, word) {
			violations = append(violations, models.PostWriteViolation{
				Rule:        "集体反应",
				Description: fmt.Sprintf("出现集体反应词\"%s\"", word),
				Suggestion:  "改写为具体个人的反应",
				Severity:    "warning",
			})
		}
	}

	// 分离 error / warning
	var errors, warnings []models.PostWriteViolation
	for _, v := range violations {
		if v.Severity == "error" {
			errors = append(errors, v)
		} else {
			warnings = append(warnings, v)
		}
	}

	return errors, warnings
}

// renderContextPackageExcerpt 渲染上下文包摘录。
func renderContextPackageExcerpt(cp *models.ContextPackage) string {
	var sb strings.Builder
	for _, entry := range cp.SelectedContext {
		sb.WriteString(fmt.Sprintf("### %s\n%s\n\n", entry.Source, entry.Excerpt))
	}
	return sb.String()
}
