package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
)

// PlanChapterInput 章节规划输入。
type PlanChapterInput struct {
	Book            *models.BookConfig
	BookDir         string
	ChapterNumber   int
	ExternalContext string
}

// PlannerAgent 章节规划 Agent。
type PlannerAgent struct {
	BaseAgent
}

// NewPlannerAgent 创建规划器 Agent。
func NewPlannerAgent(ctx llm.AgentContext) *PlannerAgent {
	return &PlannerAgent{BaseAgent: NewBaseAgent(ctx, "planner")}
}

const memoRetryLimit = 3

// PlanChapter 是章节规划流程的入口。
func (a *PlannerAgent) PlanChapter(ctx context.Context, input *PlanChapterInput) (*models.PlanChapterOutput, error) {
	storyDir := filepath.Join(input.BookDir, "story")
	runtimeDir := filepath.Join(storyDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		return nil, fmt.Errorf("create runtime dir: %w", err)
	}

	// 步骤1: 加载种子材料
	seed := a.loadPlanningSeedMaterials(input.BookDir, input.ChapterNumber)

	// 步骤2: 定位大纲节点
	outlineNode := findOutlineNode(seed.VolumeOutline, input.ChapterNumber)

	// 步骤3: 推导章节目标
	goal := deriveGoal(input.ExternalContext, seed.CurrentFocus, seed.AuthorIntent, outlineNode, input.ChapterNumber)

	// 步骤4: 收集约束
	mustKeep := collectMustKeep(seed.CurrentState, seed.StoryBible)
	mustAvoid := collectMustAvoid(seed.CurrentFocus, nil)
	styleEmphasis := collectStyleEmphasis(seed.AuthorIntent, seed.CurrentFocus)

	// 步骤6: 构建确定性 Intent
	language := "zh"
	if input.Book != nil && input.Book.Language != "" {
		language = input.Book.Language
	}

	arcContext := ""
	if outlineNode != "" && seed.VolumeOutline != "(文件尚未创建)" {
		if strings.HasPrefix(strings.ToLower(language), "zh") {
			arcContext = "卷纲节点：" + outlineNode
		} else {
			arcContext = "Outline node: " + outlineNode
		}
	}

	intent := models.ChapterIntent{
		Chapter:       input.ChapterNumber,
		Goal:          goal,
		OutlineNode:   outlineNode,
		ArcContext:    arcContext,
		MustKeep:      mustKeep,
		MustAvoid:     mustAvoid,
		StyleEmphasis: styleEmphasis,
	}

	// 步骤7: LLM 生成 7 段 memo
	isGoldenOpening := isGoldenOpeningChapter(language, input.ChapterNumber)
	memo := a.planChapterMemo(ctx, &planChapterMemoInput{
		StoryDir:              storyDir,
		BookDir:               input.BookDir,
		ChapterNumber:         input.ChapterNumber,
		IsGoldenOpening:       isGoldenOpening,
		FallbackGoal:          goal,
		ChapterSummariesRaw:   seed.ChapterSummariesRaw,
		PreviousEndingExcerpt: seed.PreviousEndingExcerpt,
		Brief:                 seed.Brief,
		ChapterContext:        input.ExternalContext,
		Language:              language,
	})

	// 用 LLM memo 的 goal 覆盖推导的 fallback
	if memo.Goal != "" {
		intent.Goal = memo.Goal
	}

	// 落盘 intent.md
	runtimePath := filepath.Join(runtimeDir, fmt.Sprintf("chapter-%04d.intent.md", input.ChapterNumber))
	intentMarkdown := renderIntentMarkdown(&intent, memo, language, seed.PendingHooks, seed.ChapterSummariesRaw, 0)
	if err := writeFileSafe(runtimePath, intentMarkdown); err != nil {
		return nil, fmt.Errorf("write intent file: %w", err)
	}

	plannerInputs := []string{
		"story/author_intent.md",
		"story/current_focus.md",
		"story/outline/story_frame.md",
		"story/outline/volume_map.md",
		"story/chapter_summaries.md",
		"story/book_rules.md",
		"story/current_state.md",
		"story/pending_hooks.md",
	}

	return &models.PlanChapterOutput{
		Intent:         intent,
		Memo:           *memo,
		IntentMarkdown: intentMarkdown,
		PlannerInputs:  plannerInputs,
		RuntimePath:    runtimePath,
	}, nil
}

// planningSeedMaterials 规划种子材料。
type planningSeedMaterials struct {
	StoryDir              string
	AuthorIntent          string
	CurrentFocus          string
	StoryBible            string
	VolumeOutline         string
	BookRulesRaw          string
	CurrentState          string
	ChapterSummariesRaw   string
	Brief                 string
	PreviousEndingExcerpt string
	PendingHooks          string
}

// loadPlanningSeedMaterials 加载规划所需的种子材料。
func (a *PlannerAgent) loadPlanningSeedMaterials(bookDir string, chapterNumber int) *planningSeedMaterials {
	storyDir := filepath.Join(bookDir, "story")
	placeholder := "(文件尚未创建)"

	seed := &planningSeedMaterials{
		StoryDir:            storyDir,
		AuthorIntent:        readFileOrDefault(filepath.Join(storyDir, "author_intent.md"), placeholder),
		CurrentFocus:        readFileOrDefault(filepath.Join(storyDir, "current_focus.md"), placeholder),
		ChapterSummariesRaw: readFileOrDefault(filepath.Join(storyDir, "chapter_summaries.md"), ""),
		BookRulesRaw:        readFileOrDefault(filepath.Join(storyDir, "book_rules.md"), ""),
		CurrentState:        readFileOrDefault(filepath.Join(storyDir, "current_state.md"), placeholder),
		Brief:               readFileOrDefault(filepath.Join(storyDir, "brief.md"), ""),
		PendingHooks:        readFileOrDefault(filepath.Join(storyDir, "pending_hooks.md"), ""),
	}

	// 优先 Phase 5 路径，回退 legacy
	storyFrame := readFileOrDefault(filepath.Join(storyDir, "outline", "story_frame.md"), "")
	if storyFrame == "" {
		storyFrame = readFileOrDefault(filepath.Join(storyDir, "story_bible.md"), placeholder)
	}
	seed.StoryBible = storyFrame

	volumeMap := readFileOrDefault(filepath.Join(storyDir, "outline", "volume_map.md"), "")
	if volumeMap == "" {
		volumeMap = readFileOrDefault(filepath.Join(storyDir, "volume_outline.md"), placeholder)
	}
	seed.VolumeOutline = volumeMap

	// 读取前一章结尾摘录
	if chapterNumber > 1 {
		prevChapterPath := filepath.Join(bookDir, "chapters", fmt.Sprintf("%04d.md", chapterNumber-1))
		prevContent := readFileOrDefault(prevChapterPath, "")
		if prevContent != "" {
			seed.PreviousEndingExcerpt = extractLastChars(prevContent, 320)
		}
	}

	return seed
}

// planChapterMemoInput memo 生成输入。
type planChapterMemoInput struct {
	StoryDir              string
	BookDir               string
	ChapterNumber         int
	IsGoldenOpening       bool
	FallbackGoal          string
	ChapterSummariesRaw   string
	PreviousEndingExcerpt string
	Brief                 string
	ChapterContext        string
	Language              string
}

// planChapterMemo 调用 LLM 生成 7 段 memo，最多重试 3 次。
func (a *PlannerAgent) planChapterMemo(ctx context.Context, input *planChapterMemoInput) *models.ChapterMemo {
	systemPrompt := a.buildMemoSystemPrompt(input.Language)
	userMessage := a.buildMemoUserMessage(input)

	currentUserMessage := userMessage
	var lastError error

	for attempt := 0; attempt < memoRetryLimit; attempt++ {
		temp := 0.7
		response, err := a.Chat(ctx, []llm.Message{
			{Role: llm.RoleSystem, Content: systemPrompt},
			{Role: llm.RoleUser, Content: currentUserMessage},
		}, &llm.ChatOptions{Temperature: &temp})
		if err != nil {
			lastError = err
			a.LogWarn(fmt.Sprintf("planner memo LLM call failed (attempt %d/%d): %v", attempt+1, memoRetryLimit, err))
			continue
		}

		memo, parseErr := parseMemo(response.Content, input.ChapterNumber, input.IsGoldenOpening)
		if parseErr == nil {
			return memo
		}
		lastError = parseErr
		a.LogWarn(fmt.Sprintf("planner memo parse failed (attempt %d/%d): %s", attempt+1, memoRetryLimit, parseErr.Error()))
		currentUserMessage = userMessage + "\n\n## 上次输出的错误\n" + parseErr.Error() + "\n请修正后重新输出。"
	}

	a.LogWarn(fmt.Sprintf("planner memo fell back after %d attempts: %v", memoRetryLimit, lastError))
	return parseMemoFallback(a.buildFallbackMemoMarkdown(input), input.ChapterNumber, input.IsGoldenOpening)
}

// buildMemoSystemPrompt 构建 memo 系统提示。
func (a *PlannerAgent) buildMemoSystemPrompt(language string) string {
	if language == "en" {
		return `You are the chief editor of this novel. Your job is to produce a chapter_memo for the next chapter. You do not write prose — you only plan what this chapter must accomplish, what to pay off, and what not to do. The downstream writer will expand prose from your memo.

Your working principles (internalized, do not cite item numbers):
1. 3-5 chapters per mini-goal cycle
2. Actively shape reader expectations
3. Everything is bait — daily/transition chapters carry future foreshadowing
4. Character behavior driven by past experience + current interest + personality
5. 1 mainline + 1 subplot
6. Dense payoffs every 3-5 chapters
7. Plant foreshadowing 3-5 chapters before climax
8. Write aftermath 1-2 chapters after climax
9. Characters need core tags + contrast details
10. Five senses concretized
11. Every chapter ends with a hook
12. Hook ledger must be settled each chapter
13. Multi-POV in same scene
14. Recommend: open 2 hooks per 1 resolved
15. User content proportions must become visible scenes

## Output Format (strictly follow)

Output plain Markdown. No YAML, no JSON, no code fences.

# Chapter N memo

## Chapter goal
(<=50 chars, concrete task statement)

## Thread refs
- H03
- S004

## Current task
(one sentence: concrete action the protagonist must complete)

## What the reader is waiting for right now
(two lines: 1) reader expectation 2) what this chapter does with it)

## To pay off / to keep buried
- Pay off: X -> to what degree
- Keep buried: Y -> hold for chapter N

## What the slow / transitional beats carry
([position] -> [function] | or "N/A - no slow beats")

## Three-question check on the key choice
- Protagonist's key choice: why / fits interest / fits character
- Opponent's key choice: same

## Required end-of-chapter change
(1-3 items: information/relationship/physical/power change)

## Hook ledger for this chapter
open:
- [new] description (<=30 chars) || reason
advance:
- H007 -> how it advances
resolve:
- H003 -> how it resolves
defer:
- H009 -> reason for deferral

## Do not
(2-4 hard constraints)`
	}

	return `你是这本小说的创作总编，职责是为下一章产生一份 chapter_memo。你不写正文——你只规划这章要完成什么、兑现什么、不要做什么。下游写手会按你的 memo 扩写正文。

你的工作原则（内化，不要在 memo 里引用条目号）：
1. 3-5 章一个小目标周期
2. 主动塑造读者期待
3. 万物皆饵——日常/过渡章节的每一笔都要是未来剧情的伏笔或钩子
4. 人设防崩——角色行为由"过往经历 + 当前利益 + 性格底色"共同驱动
5. 1 主线 + 1 支线
6. 爽点密集化：每 3-5 章一个小爽点
7. 高潮前 3-5 章必须有线索埋设
8. 高潮后 1-2 章必须写出改变
9. 人物立体化：核心标签 + 反差细节 = 活人
10. 五感具体化
11. 每章章尾留钩
12. 钩子账本必须结账
13. 圆心法同场多视角
14. 揭 1 埋 2 推荐
15. 用户设定的内容比例必须落成场面

## 输出格式（严格遵守）

输出普通 Markdown，不要 YAML frontmatter，不要 JSON，不要代码块标记。

# 第 N 章 memo

## 本章目标
(<=50字，具体任务陈述)

## 关联线索
- H03
- S004

## 当前任务
(一句话：本章主角要完成的具体动作)

## 读者此刻在等什么
(两行：1) 读者期待什么 2) 本章对期待做什么)

## 该兑现的 / 暂不掀的
- 该兑现：X → 兑现到什么程度
- 暂不掀：Y → 先压住，留到第N章

## 日常/过渡承担什么任务
([段落位置] → [承担功能] | 或 "不适用 - 本章无日常过渡")

## 关键抉择过三连问
- 主角本章最关键的一次选择：为什么 / 符合利益吗 / 符合人设吗
- 对手/配角本章最关键的一次选择：同上

## 章尾必须发生的改变
(1-3条：信息/关系/物理/权力改变)

## 本章 hook 账
open:
- [new] 新钩子描述(<=30字) || 理由
advance:
- H007 → 如何推进
resolve:
- H003 → 如何回收
defer:
- H009 → 延后理由

## 不要做
(2-4条硬约束)`
}

// buildMemoUserMessage 构建 memo 用户消息。
func (a *PlannerAgent) buildMemoUserMessage(input *planChapterMemoInput) string {
	noPriorChapter := "（本章为起始章，无前章）"
	if input.PreviousEndingExcerpt != "" {
		noPriorChapter = input.PreviousEndingExcerpt
	}

	goldenOpening := "否"
	if input.IsGoldenOpening {
		goldenOpening = "是"
	}

	briefBlock := ""
	if input.Brief != "" {
		briefBlock = fmt.Sprintf("## 用户创作 brief（原始意图——最高优先级）\n%s\n", input.Brief)
	}

	chapterContextBlock := ""
	if input.ChapterContext != "" {
		chapterContextBlock = fmt.Sprintf("## 本章用户指令（本章最高优先级）\n%s\n", input.ChapterContext)
	}

	bookRules := readFileOrDefault(filepath.Join(input.StoryDir, "book_rules.md"), "（暂无 book_rules 条目）")

	return fmt.Sprintf(`# 第 %d 章 memo 请求

%s
%s

## 上一章最后一屏（原文节选）
%s

## 最近 3 章摘要
%s

## 本章卷外约束
- 是否黄金三章：%s
- 硬约束：
%s

请为第 %d 章产生 memo。严格按上面的普通 Markdown 小节格式输出。`,
		input.ChapterNumber,
		briefBlock,
		chapterContextBlock,
		noPriorChapter,
		input.ChapterSummariesRaw,
		goldenOpening,
		bookRules,
		input.ChapterNumber)
}

// buildFallbackMemoMarkdown 3 次重试都失败时生成 degraded memo。
func (a *PlannerAgent) buildFallbackMemoMarkdown(input *planChapterMemoInput) string {
	fallbackGoal := input.FallbackGoal
	if fallbackGoal == "" {
		fallbackGoal = fmt.Sprintf("按当前大纲继续推进第 %d 章", input.ChapterNumber)
	}

	return fmt.Sprintf(`# 第 %d 章 memo

## 本章目标
%s

## 关联线索
无

## 当前任务
沿用当前章节目标和权威设定推进第 %d 章，不临时改方向，也不把章节写成泛泛过渡。

## 读者此刻在等什么
延续大纲和上一章形成的读者期待，优先回应当前已经建立的压力、证据、关系或目标变化。

## 该兑现的 / 暂不掀的
只兑现已有上下文支撑的近端承诺；更大的秘密、身份、幕后主使或终局信息，除非大纲明确要求，否则继续压住。

## 日常/过渡承担什么任务
如果需要日常或过渡，它必须承担压力、证据、人物关系、目标变化或下一步行动铺垫，不能只是闲聊和气氛。

## 关键抉择过三连问
主角本章的关键选择必须有原因、符合当前利益，并且不背离已经建立的人设和行为逻辑。

## 章尾必须发生的改变
章尾至少要在信息、压力、关系、目标或风险上发生一个明确变化，避免只有剧情摘要没有推进。

## 本章 hook 账
advance: 推进当前活跃承诺；resolve: 只结清已有证据支撑的线索；defer: 大线继续保留到更合适的位置。

## 不要做
不要违背既成事实，不要无视用户当前指令，不要把 fallback memo 当成新大纲重写整本书。

## Planner warning
模型连续 %d 次没有产出合格章节 memo。`,
		input.ChapterNumber, fallbackGoal, input.ChapterNumber, memoRetryLimit)
}

// renderIntentMarkdown 渲染 intent.md 全文。
func renderIntentMarkdown(intent *models.ChapterIntent, memo *models.ChapterMemo, language string, pendingHooks string, chapterSummaries string, activeHookCount int) string {
	mustKeep := "- none"
	if len(intent.MustKeep) > 0 {
		var items []string
		for _, item := range intent.MustKeep {
			items = append(items, "- "+item)
		}
		mustKeep = strings.Join(items, "\n")
	}

	mustAvoid := "- none"
	if len(intent.MustAvoid) > 0 {
		var items []string
		for _, item := range intent.MustAvoid {
			items = append(items, "- "+item)
		}
		mustAvoid = strings.Join(items, "\n")
	}

	styleEmphasis := "- none"
	if len(intent.StyleEmphasis) > 0 {
		var items []string
		for _, item := range intent.StyleEmphasis {
			items = append(items, "- "+item)
		}
		styleEmphasis = strings.Join(items, "\n")
	}

	threadRefsLine := "- (none)"
	if len(memo.ThreadRefs) > 0 {
		var items []string
		for _, id := range memo.ThreadRefs {
			items = append(items, "- "+id)
		}
		threadRefsLine = strings.Join(items, "\n")
	}

	return fmt.Sprintf(`# Chapter Intent

## Goal
%s

## Outline Node
%s

## Arc Context
%s

## Must Keep
%s

## Must Avoid
%s

## Style Emphasis
%s

## Chapter Memo
- isGoldenOpening: %t

### Thread Refs
%s

### Body
%s

%s

## Pending Hooks Snapshot
%s

## Chapter Summaries Snapshot
%s
`,
		intent.Goal,
		defaultStr(intent.OutlineNode, "(not found)"),
		defaultStr(intent.ArcContext, "(none)"),
		mustKeep,
		mustAvoid,
		styleEmphasis,
		memo.IsGoldenOpening,
		threadRefsLine,
		strings.TrimSpace(memo.Body),
		renderHookBudget(activeHookCount, language),
		pendingHooks,
		chapterSummaries)
}

// ============================================================================
// 辅助函数
// ============================================================================

// findOutlineNode 在 volumeOutline 中定位当前章对应的卷级节点。
func findOutlineNode(volumeOutline string, chapterNumber int) string {
	lines := strings.Split(volumeOutline, "\n")
	var trimmedLines []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t != "" {
			trimmedLines = append(trimmedLines, t)
		}
	}

	// 1. 精确匹配
	for i, line := range trimmedLines {
		if content := matchExactOutlineLine(line, chapterNumber); content != "" {
			if content != "" {
				return content
			}
			if next := findNextOutlineContent(trimmedLines, i+1); next != "" {
				return next
			}
		}
	}

	// 2. 范围匹配
	for i, line := range trimmedLines {
		startCh, endCh, content, matched := matchRangeOutlineLine(line, chapterNumber)
		if !matched {
			continue
		}
		if content != "" {
			return content
		}
		_ = startCh
		_ = endCh
		if next := findNextOutlineContent(trimmedLines, i+1); next != "" {
			return next
		}
	}

	// 3. 锚点匹配
	for i, line := range trimmedLines {
		if !isOutlineAnchorLine(line) {
			continue
		}
		if next := findNextOutlineContent(trimmedLines, i+1); next != "" {
			return next
		}
		break
	}

	// 4. 兜底
	return extractFirstDirective(volumeOutline)
}

// matchExactOutlineLine 匹配精确章号行。
func matchExactOutlineLine(line string, chapterNumber int) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(fmt.Sprintf("(?i)^(?:#+\\s*)?(?:[-*]\\s+)?(?:\\*\\*)?Chapter\\s*%d(?!\\d|\\s*[-~–—]\\s*\\d)(?:[:：-])?(?:\\*\\*)?\\s*(.*)$", chapterNumber)),
		regexp.MustCompile(fmt.Sprintf("^(?:#+\\s*)?(?:[-*]\\s+)?(?:\\*\\*)?第\\s*%d\\s*章(?!\\d|\\s*[-~–—]\\s*\\d)(?:[:：-])?(?:\\*\\*)?\\s*(.*)$", chapterNumber)),
	}
	for _, p := range patterns {
		match := p.FindStringSubmatch(line)
		if len(match) >= 2 {
			return cleanOutlineContent(match[1])
		}
	}
	return ""
}

// matchRangeOutlineLine 匹配范围章号行。
func matchRangeOutlineLine(line string, chapterNumber int) (int, int, string, bool) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile("(?i)^(?:#+\\s*)?(?:[-*]\\s+)?(?:\\*\\*)?Chapter\\s*(\\d+)\\s*[-~–—]\\s*(\\d+)\\b(?:[:：-])?(?:\\*\\*)?\\s*(.*)$"),
		regexp.MustCompile("^(?:#+\\s*)?(?:[-*]\\s+)?(?:\\*\\*)?第\\s*(\\d+)\\s*[-~–—]\\s*(\\d+)\\s*章(?:[:：-])?(?:\\*\\*)?\\s*(.*)$"),
	}
	for _, p := range patterns {
		match := p.FindStringSubmatch(line)
		if len(match) < 4 {
			continue
		}
		start, _ := strconv.Atoi(match[1])
		end, _ := strconv.Atoi(match[2])
		lower := start
		upper := end
		if start > end {
			lower = end
			upper = start
		}
		if chapterNumber >= lower && chapterNumber <= upper {
			return start, end, cleanOutlineContent(match[3]), true
		}
	}
	return 0, 0, "", false
}

// isOutlineAnchorLine 判断是否为锚点行。
func isOutlineAnchorLine(line string) bool {
	exactPatterns := []*regexp.Regexp{
		regexp.MustCompile("(?i)^(?:#+\\s*)?(?:[-*]\\s+)?(?:\\*\\*)?Chapter\\s*\\d+(?!\\s*[-~–—]\\s*\\d)"),
		regexp.MustCompile("^(?:#+\\s*)?(?:[-*]\\s+)?(?:\\*\\*)?第\\s*\\d+\\s*章(?!\\s*[-~–—]\\s*\\d)"),
	}
	rangePatterns := []*regexp.Regexp{
		regexp.MustCompile("(?i)^(?:#+\\s*)?(?:[-*]\\s+)?(?:\\*\\*)?Chapter\\s*\\d+\\s*[-~–—]\\s*\\d+\\b"),
		regexp.MustCompile("^(?:#+\\s*)?(?:[-*]\\s+)?(?:\\*\\*)?第\\s*\\d+\\s*[-~–—]\\s*\\d+\\s*章"),
	}
	for _, p := range exactPatterns {
		if p.MatchString(line) {
			return true
		}
	}
	for _, p := range rangePatterns {
		if p.MatchString(line) {
			return true
		}
	}
	return false
}

// findNextOutlineContent 查找下一个非标题非空内容行。
func findNextOutlineContent(lines []string, startIndex int) string {
	for i := startIndex; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		if isOutlineAnchorLine(line) {
			return ""
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if content := cleanOutlineContent(line); content != "" {
			return content
		}
	}
	return ""
}

// cleanOutlineContent 清理大纲内容。
func cleanOutlineContent(content string) string {
	cleaned := strings.TrimSpace(content)
	if cleaned == "" {
		return ""
	}
	if regexp.MustCompile("^[*_`~:：-]+$").MatchString(cleaned) {
		return ""
	}
	return cleaned
}

// deriveGoal 目标推导优先级链。
func deriveGoal(externalContext, currentFocus, authorIntent, outlineNode string, chapterNumber int) string {
	if first := extractFirstDirective(externalContext); first != "" {
		return first
	}
	if override := extractLocalOverrideGoal(currentFocus); override != "" {
		return override
	}
	if first := extractFirstDirective(outlineNode); first != "" {
		return first
	}
	if focus := extractFocusGoal(currentFocus); focus != "" {
		return focus
	}
	if first := extractFirstDirective(authorIntent); first != "" {
		return first
	}
	return fmt.Sprintf("Advance chapter %d with clear narrative focus.", chapterNumber)
}

// extractFirstDirective 提取第一个非标题/非列表/非模板占位行的指令。
func extractFirstDirective(content string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") {
			continue
		}
		if isTemplatePlaceholder(trimmed) {
			continue
		}
		return trimmed
	}
	return ""
}

// extractLocalOverrideGoal 提取局部覆盖目标。
func extractLocalOverrideGoal(currentFocus string) string {
	overrideSection := extractSection(currentFocus, []string{"local override", "explicit override", "chapter override", "局部覆盖", "本章覆盖", "临时覆盖", "当前覆盖"})
	if overrideSection == "" {
		return ""
	}
	items := extractListItems(overrideSection, 3)
	if len(items) > 0 {
		sep := "；"
		if !containsChinese(overrideSection) {
			sep = "; "
		}
		return strings.Join(items, sep)
	}
	return extractFirstDirective(overrideSection)
}

// extractFocusGoal 提取聚焦目标。
func extractFocusGoal(currentFocus string) string {
	focusSection := extractSection(currentFocus, []string{"active focus", "focus", "当前聚焦", "当前焦点", "近期聚焦"})
	if focusSection == "" {
		focusSection = currentFocus
	}
	items := extractListItems(focusSection, 3)
	if len(items) > 0 {
		sep := "；"
		if !containsChinese(focusSection) {
			sep = "; "
		}
		return strings.Join(items, sep)
	}
	return extractFirstDirective(focusSection)
}

// collectMustKeep 收集必须保留的约束。
func collectMustKeep(currentState, storyBible string) []string {
	items := append(extractListItems(currentState, 2), extractListItems(storyBible, 2)...)
	return uniqueStrings(items)[:minInt(4, len(uniqueStrings(items)))]
}

// collectMustAvoid 收集必须避免的约束。
func collectMustAvoid(currentFocus string, prohibitions []string) []string {
	avoidSection := extractSection(currentFocus, []string{"avoid", "must avoid", "禁止", "避免", "避雷"})
	var focusAvoids []string
	if avoidSection != "" {
		focusAvoids = extractListItems(avoidSection, 10)
	} else {
		for _, line := range strings.Split(currentFocus, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "-") && regexp.MustCompile("(?i)avoid|don't|do not|不要|别|禁止").MatchString(trimmed) {
				if cleaned := cleanListItem(trimmed); cleaned != "" {
					focusAvoids = append(focusAvoids, cleaned)
				}
			}
		}
	}
	return uniqueStrings(append(focusAvoids, prohibitions...))[:minInt(6, len(uniqueStrings(append(focusAvoids, prohibitions...))))]
}

// collectStyleEmphasis 收集风格强调。
func collectStyleEmphasis(authorIntent, currentFocus string) []string {
	items := append(extractListItems(authorIntent, 2), extractListItems(currentFocus, 2)...)
	return uniqueStrings(items)[:minInt(4, len(uniqueStrings(items)))]
}

// extractSection 提取 Markdown 标题段。
func extractSection(content string, headings []string) string {
	targets := make(map[string]bool)
	for _, h := range headings {
		targets[normalizeHeading(h)] = true
	}

	lines := strings.Split(content, "\n")
	var buffer []string
	collecting := false
	sectionLevel := 0

	for _, line := range lines {
		headingMatch := regexp.MustCompile("^(#+)\\s*(.+?)\\s*$").FindStringSubmatch(line)
		if headingMatch != nil {
			level := len(headingMatch[1])
			heading := normalizeHeading(headingMatch[2])

			if collecting && level <= sectionLevel {
				break
			}
			if targets[heading] {
				collecting = true
				sectionLevel = level
				continue
			}
		}
		if collecting {
			buffer = append(buffer, line)
		}
	}

	result := strings.TrimSpace(strings.Join(buffer, "\n"))
	if result == "" {
		return ""
	}
	return result
}

// extractListItems 提取 Markdown 列表项。
func extractListItems(content string, limit int) []string {
	var items []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		if cleaned := cleanListItem(trimmed); cleaned != "" {
			items = append(items, cleaned)
		}
		if len(items) >= limit {
			break
		}
	}
	return items
}

// cleanListItem 清理列表项。
func cleanListItem(line string) string {
	cleaned := strings.TrimPrefix(strings.TrimSpace(line), "-")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return ""
	}
	if regexp.MustCompile("^[-|]+$").MatchString(cleaned) {
		return ""
	}
	if isTemplatePlaceholder(cleaned) {
		return ""
	}
	return cleaned
}

// isTemplatePlaceholder 判断是否为模板占位符。
func isTemplatePlaceholder(line string) bool {
	normalized := strings.TrimSpace(line)
	if normalized == "" {
		return false
	}
	if regexp.MustCompile("(?i)^\\((describe|briefly describe|write)\\b[\\s\\S]*\\)$").MatchString(normalized) {
		return true
	}
	if regexp.MustCompile("^（(?:在这里描述|描述|填写|写下)[\\s\\S]*）$").MatchString(normalized) {
		return true
	}
	return false
}

// isGoldenOpeningChapter 黄金开篇判定。
func isGoldenOpeningChapter(language string, chapterNumber int) bool {
	if strings.HasPrefix(strings.ToLower(language), "zh") || language == "" {
		return chapterNumber <= 3
	}
	return chapterNumber <= 5
}

// renderHookBudget 渲染伏笔预算段。
func renderHookBudget(activeCount int, language string) string {
	cap := 12
	if activeCount < 10 {
		if strings.HasPrefix(strings.ToLower(language), "en") {
			return fmt.Sprintf("### Hook Budget\n- %d active hooks (capacity: %d)", activeCount, cap)
		}
		return fmt.Sprintf("### 伏笔预算\n- 当前 %d 条活跃伏笔（容量：%d）", activeCount, cap)
	}
	remaining := maxInt(0, cap-activeCount)
	if strings.HasPrefix(strings.ToLower(language), "en") {
		return fmt.Sprintf("### Hook Budget\n- %d active hooks — approaching capacity (%d). Only %d new hook(s) allowed.", activeCount, cap, remaining)
	}
	return fmt.Sprintf("### 伏笔预算\n- 当前 %d 条活跃伏笔——接近容量上限（%d）。仅剩 %d 个新坑位。优先回收旧债，不要轻易开新线。", activeCount, cap, remaining)
}

// parseMemo 解析 LLM 产出的 memo markdown。
func parseMemo(raw string, expectedChapter int, isGoldenOpening bool) (*models.ChapterMemo, error) {
	raw = stripWrappingFence(raw)
	raw = dropLeadingProse(raw)

	// 提取 goal
	goal := extractMemoGoal(raw)
	if goal == "" {
		return nil, fmt.Errorf("goal must be a non-empty string")
	}

	// 提取 thread refs
	threadRefs := extractThreadRefs(raw)

	// 提取 body
	body := extractMemoBody(raw)

	// 检查必需 section 存在
	requiredSections := getRequiredMemoSections()
	missing := []string{}
	for _, sec := range requiredSections {
		if !sectionExists(raw, sec.titles) {
			missing = append(missing, sec.titles[0])
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing sections: %s", strings.Join(missing, ", "))
	}

	// 检查 section 内容非空
	emptySections := []string{}
	for _, sec := range requiredSections {
		content := extractSectionContent(raw, sec.titles)
		if len([]rune(content)) < sec.minChars {
			emptySections = append(emptySections, fmt.Sprintf("%s (need ≥ %d chars)", sec.titles[0], sec.minChars))
		}
	}
	if len(emptySections) > 0 {
		return nil, fmt.Errorf("empty sections: %s", strings.Join(emptySections, ", "))
	}

	return &models.ChapterMemo{
		Goal:            goal,
		ThreadRefs:      threadRefs,
		Body:            body,
		IsGoldenOpening: isGoldenOpening,
	}, nil
}

// parseMemoFallback 解析 fallback memo（不抛错，确保返回有效 memo）。
func parseMemoFallback(raw string, expectedChapter int, isGoldenOpening bool) *models.ChapterMemo {
	memo, err := parseMemo(raw, expectedChapter, isGoldenOpening)
	if err != nil {
		return &models.ChapterMemo{
			Goal:            fmt.Sprintf("推进第%d章", expectedChapter),
			ThreadRefs:      []string{},
			Body:            raw,
			IsGoldenOpening: isGoldenOpening,
		}
	}
	return memo
}

// memoSection 定义 memo 必需段。
type memoSection struct {
	titles   []string
	minChars int
}

// getRequiredMemoSections 返回必需的 memo 段列表。
func getRequiredMemoSections() []memoSection {
	return []memoSection{
		{[]string{"## 本章目标", "## Chapter goal"}, 1},
		{[]string{"## 关联线索", "## Thread refs"}, 1},
		{[]string{"## 当前任务", "## Current task"}, 20},
		{[]string{"## 读者此刻在等什么", "## What the reader is waiting for right now", "## What the reader"}, 20},
		{[]string{"## 该兑现的 / 暂不掀的", "## To pay off / to keep buried", "## To pay off"}, 20},
		{[]string{"## 日常/过渡承担什么任务", "## What the slow / transitional beats carry", "## What the slow"}, 20},
		{[]string{"## 关键抉择过三连问", "## Three-question check on the key choice", "## Three-question check"}, 20},
		{[]string{"## 章尾必须发生的改变", "## Required end-of-chapter change", "## Required end-of-chapter"}, 20},
		{[]string{"## 本章 hook 账", "## Hook ledger for this chapter", "## Hook ledger"}, 20},
		{[]string{"## 不要做", "## Do not"}, 1},
	}
}

// sectionExists 检查任一标题存在。
func sectionExists(content string, titles []string) bool {
	for _, title := range titles {
		if strings.Contains(content, title) {
			return true
		}
	}
	return false
}

// extractSectionContent 提取标题到下一个 ## 之间的内容。
func extractSectionContent(content string, titles []string) string {
	for _, title := range titles {
		idx := strings.Index(content, title)
		if idx < 0 {
			continue
		}
		start := idx + len(title)
		rest := content[start:]
		nextIdx := strings.Index(rest, "\n## ")
		if nextIdx >= 0 {
			return strings.TrimSpace(rest[:nextIdx])
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// extractMemoGoal 从 memo 提取 goal。
func extractMemoGoal(raw string) string {
	for _, title := range []string{"## 本章目标", "## Chapter goal"} {
		idx := strings.Index(raw, title)
		if idx < 0 {
			continue
		}
		start := idx + len(title)
		rest := strings.TrimSpace(raw[start:])
		nextIdx := strings.Index(rest, "\n## ")
		if nextIdx >= 0 {
			rest = rest[:nextIdx]
		}
		// 取第一句
		for _, sep := range []string{"\n", "。", ". "} {
			if idx := strings.Index(rest, sep); idx > 0 {
				rest = rest[:idx]
			}
		}
		rest = strings.TrimSpace(rest)
		// ≤50 字
		if len([]rune(rest)) > 50 {
			rest = string([]rune(rest)[:47]) + "..."
		}
		return rest
	}
	return ""
}

// extractThreadRefs 提取关联线索 ID。
func extractThreadRefs(raw string) []string {
	for _, title := range []string{"## 关联线索", "## Thread refs"} {
		idx := strings.Index(raw, title)
		if idx < 0 {
			continue
		}
		start := idx + len(title)
		rest := raw[start:]
		nextIdx := strings.Index(rest, "\n## ")
		if nextIdx >= 0 {
			rest = rest[:nextIdx]
		}
		idPattern := regexp.MustCompile("[A-Za-z][A-Za-z0-9_-]*\\d+")
		matches := idPattern.FindAllString(rest, -1)
		return uniqueStrings(matches)
	}
	return []string{}
}

// extractMemoBody 从第一个必需 section 标题开始截取。
func extractMemoBody(raw string) string {
	firstSectionIdx := len(raw)
	for _, title := range []string{"## 本章目标", "## Chapter goal"} {
		if idx := strings.Index(raw, title); idx >= 0 && idx < firstSectionIdx {
			firstSectionIdx = idx
		}
	}
	if firstSectionIdx < len(raw) {
		return strings.TrimSpace(raw[firstSectionIdx:])
	}
	return strings.TrimSpace(raw)
}

// stripWrappingFence 去掉外层代码块围栏。
func stripWrappingFence(content string) string {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 2 {
			// 去掉第一行和最后一行
			if strings.HasPrefix(lines[len(lines)-1], "```") {
				return strings.Join(lines[1:len(lines)-1], "\n")
			}
		}
	}
	return content
}

// dropLeadingProse 去掉 memo 标题前的废话。
func dropLeadingProse(content string) string {
	lines := strings.Split(content, "\n")
	startIdx := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
			startIdx = i
			break
		}
	}
	if startIdx > 0 {
		return strings.Join(lines[startIdx:], "\n")
	}
	return content
}

// ============================================================================
// 通用辅助函数
// ============================================================================

func normalizeHeading(heading string) string {
	s := strings.ToLower(heading)
	s = strings.Map(func(r rune) rune {
		if r == '*' || r == '_' || r == '`' || r == ':' || r == '#' {
			return ' '
		}
		return r
	}, s)
	s = regexp.MustCompile("\\s+").ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func containsChinese(content string) bool {
	return regexp.MustCompile("[\u4e00-\u9fff]").MatchString(content)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		result = append(result, v)
	}
	return result
}

func extractLastChars(content string, maxChars int) string {
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	return string(runes[len(runes)-maxChars:])
}

func defaultStr(s string, defaultVal string) string {
	if s == "" {
		return defaultVal
	}
	return s
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
