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

// ArchitectAgent 架构师 Agent，负责生成散文密度的基础设定。
type ArchitectAgent struct {
	BaseAgent
}

// NewArchitectAgent 创建架构师 Agent。
func NewArchitectAgent(ctx llm.AgentContext) *ArchitectAgent {
	return &ArchitectAgent{BaseAgent: NewBaseAgent(ctx, "architect")}
}

// ArchitectIncompleteFoundationError 表示 LLM 输出始终不完整（修复重试后仍失败）。
type ArchitectIncompleteFoundationError struct {
	Missing        []string
	PartialContent string
	Msg            string
}

func (e *ArchitectIncompleteFoundationError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("architect foundation incomplete; missing sections: %s", strings.Join(e.Missing, ", "))
}

// missingArchitectSectionsError 内部错误，表示当前轮次解析缺 section。
type missingArchitectSectionsError struct {
	Missing []string
	Content string
}

func (e *missingArchitectSectionsError) Error() string {
	return fmt.Sprintf("architect output missing required section(s): %s", strings.Join(e.Missing, ", "))
}

// GenerateFoundation 调用 LLM 生成 5 段 SECTION 基础设定，解析后返回。
func (a *ArchitectAgent) GenerateFoundation(
	ctx context.Context,
	book models.BookConfig,
	externalContext string,
	reviewFeedback string,
) (*models.ArchitectOutput, error) {
	language := book.Language
	if language == "" {
		language = "zh"
	}

	contextBlock := ""
	if externalContext != "" {
		contextBlock = fmt.Sprintf("\n\n## 外部指令\n以下是来自外部系统的创作指令，请将其融入设定中：\n\n%s\n", externalContext)
	}

	reviewFeedbackBlock := a.buildReviewFeedbackBlock(reviewFeedback, language)

	systemPrompt := a.buildChineseFoundationPrompt(book, contextBlock, reviewFeedbackBlock)

	userMessage := fmt.Sprintf("请为标题为\"%s\"的小说生成完整基础设定。", book.Title)

	temp := 0.8
	response, err := a.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userMessage},
	}, &llm.ChatOptions{Temperature: &temp})
	if err != nil {
		return nil, fmt.Errorf("architect LLM call: %w", err)
	}

	return a.parseSectionsWithRepair(ctx, response.Content, language)
}

// WriteFoundationFiles 将 ArchitectOutput 落盘到 bookDir。
func (a *ArchitectAgent) WriteFoundationFiles(bookDir string, output *models.ArchitectOutput) error {
	storyDir := filepath.Join(bookDir, "story")
	outlineDir := filepath.Join(storyDir, "outline")
	rolesMajorDir := filepath.Join(storyDir, "roles", "主要角色")
	rolesMinorDir := filepath.Join(storyDir, "roles", "次要角色")

	dirs := []string{storyDir, outlineDir, rolesMajorDir, rolesMinorDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}

	storyFrame := output.StoryFrame
	if storyFrame == "" {
		storyFrame = output.StoryBible
	}
	volumeMap := output.VolumeMap
	if volumeMap == "" {
		volumeMap = output.VolumeOutline
	}

	// Phase 5 权威文件
	if storyFrame != "" {
		if err := writeFileSafe(filepath.Join(outlineDir, "story_frame.md"), storyFrame); err != nil {
			return err
		}
	}
	if volumeMap != "" {
		if err := writeFileSafe(filepath.Join(outlineDir, "volume_map.md"), volumeMap); err != nil {
			return err
		}
	}
	if output.RhythmPrinciples != "" {
		if err := writeFileSafe(filepath.Join(outlineDir, "节奏原则.md"), output.RhythmPrinciples); err != nil {
			return err
		}
	}

	// 角色卡一人一文件
	for _, role := range output.Roles {
		targetDir := rolesMajorDir
		if role.Tier == "minor" {
			targetDir = rolesMinorDir
		}
		safeName := sanitizeFileName(role.Name)
		if safeName == "" {
			continue
		}
		if err := writeFileSafe(filepath.Join(targetDir, safeName+".md"), role.Content); err != nil {
			return err
		}
	}

	// 兼容指针文件
	if storyFrame != "" {
		shim := fmt.Sprintf("# 故事圣经（兼容指针——已废弃）\n\n> 权威来源已迁移至 outline/story_frame.md\n\n## story_frame 摘录\n\n%s\n",
			truncateRunes(storyFrame, 2000))
		if err := writeFileSafe(filepath.Join(storyDir, "story_bible.md"), shim); err != nil {
			return err
		}
	}

	// book_rules
	if output.BookRules != "" {
		if err := writeFileSafe(filepath.Join(storyDir, "book_rules.md"), strings.TrimRight(output.BookRules, "\n")+"\n"); err != nil {
			return err
		}
	}

	// current_state.md 种子占位
	currentStateSeed := output.CurrentState
	if strings.TrimSpace(currentStateSeed) == "" {
		currentStateSeed = "# 当前状态\n\n> 建书时占位。运行时每章之后由 consolidator 追加最新状态。\n"
	}
	if err := writeFileSafe(filepath.Join(storyDir, "current_state.md"), currentStateSeed); err != nil {
		return err
	}

	// pending_hooks.md
	if output.PendingHooks != "" {
		if err := writeFileSafe(filepath.Join(storyDir, "pending_hooks.md"), output.PendingHooks); err != nil {
			return err
		}
	}

	// emotional_arcs.md 空表头
	emotionalArcsSeed := "# 情感弧线\n\n| 角色 | 章节 | 情绪状态 | 触发事件 | 强度(1-10) | 弧线方向 |\n|------|------|----------|----------|------------|----------|\n"
	if err := writeFileSafe(filepath.Join(storyDir, "emotional_arcs.md"), emotionalArcsSeed); err != nil {
		return err
	}

	return nil
}

// parseSectionsWithRepair 先尝试解析，失败则调 LLM 修复后重试一次。
func (a *ArchitectAgent) parseSectionsWithRepair(ctx context.Context, content string, language string) (*models.ArchitectOutput, error) {
	output, err := a.parseSections(content, language)
	if err == nil {
		return output, nil
	}

	missingErr, ok := err.(*missingArchitectSectionsError)
	if !ok {
		return nil, err
	}

	a.LogWarn(fmt.Sprintf("architect parse failed, attempting repair; missing: %s", strings.Join(missingErr.Missing, ", ")))

	repaired, repairErr := a.repairMissingSections(ctx, missingErr.Missing, missingErr.Content, language)
	if repairErr != nil {
		return nil, repairErr
	}

	output, err = a.parseSections(repaired, language)
	if err != nil {
		if missingErr2, ok := err.(*missingArchitectSectionsError); ok {
			missingList := strings.Join(missingErr2.Missing, "、")
			msg := fmt.Sprintf("基础设定没有生成完整(缺少:%s)。这通常是模型一次没把所有部分写全，不是你的输入有问题。点重试，或换更强的模型再生成一次。", missingList)
			return nil, &ArchitectIncompleteFoundationError{
				Missing:        missingErr2.Missing,
				PartialContent: missingErr2.Content,
				Msg:            msg,
			}
		}
		return nil, err
	}
	return output, nil
}

// repairMissingSections 调 LLM 补齐缺失 section。
func (a *ArchitectAgent) repairMissingSections(ctx context.Context, missing []string, content string, language string) (string, error) {
	missingList := strings.Join(missing, ", ")
	system := strings.Join([]string{
		"你负责修复 architect 的输出格式。",
		"上一轮草稿有可用内容，但缺少必需的 SECTION 块。",
		"不要重新发明一本书；保留已有可用内容，只补齐缺失部分并整理成完整输出。",
		"必须按顺序返回完整 5 段 SECTION：story_frame、volume_map、roles、book_rules、pending_hooks。",
		"book_rules 必须是普通 Markdown，不要 YAML；pending_hooks 必须是 Markdown 表格。",
		"不要解释修复过程。",
	}, "\n")

	userMsg := fmt.Sprintf("缺失 section：%s\n\n原始不完整输出如下：\n\n%s", missingList, content)

	temp := 0.2
	response, err := a.Chat(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: userMsg},
	}, &llm.ChatOptions{Temperature: &temp})
	if err != nil {
		return "", fmt.Errorf("repair LLM call: %w", err)
	}
	return response.Content, nil
}

// parseSections 解析 5 段 SECTION，缺段抛 missingArchitectSectionsError。
func (a *ArchitectAgent) parseSections(content string, language string) (*models.ArchitectOutput, error) {
	sectionMap := parseArchitectSectionMap(content)

	storyFrame := getMapValue(sectionMap, "story_frame")
	legacyStoryBible := getMapValue(sectionMap, "story_bible")
	volumeMap := getMapValue(sectionMap, "volume_map")
	legacyVolumeOutline := getMapValue(sectionMap, "volume_outline")
	rhythmPrinciples := getMapValue(sectionMap, "rhythm_principles")
	rolesRaw := getMapValue(sectionMap, "roles")
	bookRules := getMapValue(sectionMap, "book_rules")
	currentStateLegacy := getMapValue(sectionMap, "current_state")
	pendingHooksRaw := getMapValue(sectionMap, "pending_hooks")

	effectiveStoryFrame := storyFrame
	if effectiveStoryFrame == "" {
		effectiveStoryFrame = legacyStoryBible
	}
	effectiveVolumeMap := volumeMap
	if effectiveVolumeMap == "" {
		effectiveVolumeMap = legacyVolumeOutline
	}

	usingLegacyOutlineNames := storyFrame == "" && volumeMap == "" && (legacyStoryBible != "" || legacyVolumeOutline != "")

	var missing []string
	if effectiveStoryFrame == "" {
		missing = append(missing, "story_frame")
	}
	if effectiveVolumeMap == "" {
		missing = append(missing, "volume_map")
	}
	if strings.TrimSpace(rolesRaw) == "" && !usingLegacyOutlineNames {
		missing = append(missing, "roles")
	}
	if bookRules == "" {
		missing = append(missing, "book_rules")
	}
	if pendingHooksRaw == "" {
		missing = append(missing, "pending_hooks")
	}
	if len(missing) > 0 {
		return nil, &missingArchitectSectionsError{Missing: missing, Content: content}
	}

	roles := parseRoles(rolesRaw)
	pendingHooks := stripTrailingAssistantCoda(pendingHooksRaw)

	storyBible := legacyStoryBible
	if storyBible == "" {
		storyBible = buildStoryBibleShim(effectiveStoryFrame, language)
	}
	volumeOutline := legacyVolumeOutline
	if volumeOutline == "" {
		volumeOutline = effectiveVolumeMap
	}

	return &models.ArchitectOutput{
		StoryBible:        storyBible,
		VolumeOutline:     volumeOutline,
		BookRules:         bookRules,
		CurrentState:      currentStateLegacy,
		PendingHooks:      pendingHooks,
		StoryFrame:        effectiveStoryFrame,
		VolumeMap:         effectiveVolumeMap,
		RhythmPrinciples:  rhythmPrinciples,
		Roles:             roles,
	}, nil
}

// buildReviewFeedbackBlock 构建审核反馈段。
func (a *ArchitectAgent) buildReviewFeedbackBlock(reviewFeedback string, language string) string {
	trimmed := strings.TrimSpace(reviewFeedback)
	if trimmed == "" {
		return ""
	}
	return fmt.Sprintf("\n\n## 上一轮审核反馈\n上一轮基础设定未通过审核。你必须在这次重生中明确修复以下问题，不能只换措辞重写同一套方案：\n\n%s\n", trimmed)
}

// buildChineseFoundationPrompt 构建中文版架构师 system prompt。
func (a *ArchitectAgent) buildChineseFoundationPrompt(book models.BookConfig, contextBlock string, reviewFeedbackBlock string) string {
	return fmt.Sprintf(`你是这本书的总架构师。你的唯一输出是**散文密度的基础设定**——不是表格、不是 schema、不是条目化 bullet。你的散文密度决定了后面 planner 能不能读出"稀疏 memo"，writer 能不能写出活人，reviewer 能不能校准硬伤。%s%s

## 书籍元信息
- 平台：%s
- 题材：%s
- 目标章数：%d章
- 每章字数：%d字
- 标题：%s

## 输出结构（5 个 SECTION，严格按 === SECTION: === 分块，不要漏任何一块）

## 去重铁律（必读）
禁止在多段里重复同一事实。主角弧线只写在 roles；世界铁律只写在 story_frame.世界观底色；节奏原则只写在 volume_map 最后一段；角色当前现状只写在 roles.当前现状；初始钩子只写在 pending_hooks（startChapter=0 行）。如果一个段落写了另一段的内容，删掉。

## 预算（超预算必删）
- story_frame ≤ 3000 chars
- volume_map ≤ 5000 chars
- roles 总 ≤ 8000 chars
- book_rules ≤ 1000 chars（普通 Markdown 规则卡）
- pending_hooks ≤ 2000 chars

=== SECTION: story_frame ===

这是散文骨架。4 段，每段约 600-900 字，不要写表格，不要写 bullet list，写成能被人读下去的段落。主角弧线不写在本 section；它的权威来源是 roles/主要角色/<主角>.md。本段只需一句指针："本书主角是 X，完整弧线详见 roles/主要角色/X.md"。

### 段 1：主题与基调
写这本书到底讲的是什么——具体的命题。主题下面跟着基调——温情冷冽悲壮肃杀，哪一种？

### 段 2：核心冲突、对手定性、前台/后台双层故事
主要矛盾是什么？至少 2 个对手（显性 + 结构性）。必须显式写出"前台故事 / 后台故事"两条线，且有因果关联。

### 段 3：世界观底色（铁律 + 质感 + 本书专属规则）
3-5 条不可违反的铁律（prose）。感官质感锚。

### 段 4：终局方向 + 全书 Objective
最后一个镜头。末尾必须明确写出全书 Objective 一句话：可验证的终局状态。

=== SECTION: volume_map ===

分卷散文地图，5 段主体 + 1 段节奏原则尾段。只写到卷级 prose，禁止指定具体章号。

### 段 1：各卷主题与情绪曲线
### 段 2：卷间钩子与回收承诺（前台+后台双层）
### 段 3：各卷 OKR（Objective + 3 Key Results）
### 段 4：卷尾必须发生的改变
### 段 5：节奏原则（6条，至少3条具体化到本书）

=== SECTION: roles ===

一人一卡 prose。主角卡是本书角色弧线的唯一权威来源。

---ROLE---
tier: major
name: <角色名>
---CONTENT---
（散文角色卡：核心标签 / 反差细节 / 人物小传 / 主角弧线(起点→终点→代价) / 当前现状 / 关系网络 / 内在驱动 / 成长弧光）

---ROLE---
tier: minor
name: <次要角色名>
---CONTENT---
（简化角色卡：核心标签 / 反差细节 / 当前现状 / 与主角关系）

=== SECTION: book_rules ===

普通 Markdown 规则卡，不写 YAML/JSON/代码块。

## 主角
- 名字：<主角名>
- 性格锁：<3-5个性格关键词>
- 行为约束：<3-5条主角不能违背的行为边界>

## 题材锁
- 主类型：%s
- 禁止混入：<2-3种禁止混入的文风/体系>

## 禁止事项
- <3-5条本书禁忌>

=== SECTION: pending_hooks ===

初始伏笔池（Markdown表格），13列：
| hook_id | start_chapter | type | status | last_advanced_chapter | expected_payoff | payoff_timing | depends_on | pays_off_in_arc | core_hook | half_life | notes |

建书阶段 last_advanced_chapter 统一填 0。普通种子行状态写「暂缓」。

## 硬性完结检查（生成前读一遍）
必须依次输出全部 5 个 SECTION 块：story_frame → volume_map → roles → book_rules → pending_hooks，不允许因为 story_frame 或 volume_map 写长了就不写后 3 段。只有写完 pending_hooks 最后一行才算交付。`,
		contextBlock, reviewFeedbackBlock,
		book.Platform, book.Genre, book.TargetChapters, book.ChapterWordCount, book.Title,
		book.Genre)
}

// ============================================================================
// 解析辅助函数
// ============================================================================

// parseArchitectSectionMap 把原文切成 map[sectionName]sectionContent。
func parseArchitectSectionMap(content string) map[string]string {
	result := make(map[string]string)

	// 先匹配 === SECTION: xxx === 标记
	sectionPattern := regexp.MustCompile("(?im)^\\s{0,3}(?:#{1,6}\\s*)?===\\s*SECTION\\s*[：:]\\s*([^\\n=]+?)\\s*===\\s*(?:#+\\s*)?$")
	matches := sectionPattern.FindAllStringSubmatchIndex(content, -1)

	if len(matches) > 0 {
		type marker struct {
			name        string
			start       int
			markerEnd   int
		}
		var markers []marker
		for _, m := range matches {
			name := content[m[2]:m[3]]
			normalized := normalizeSectionName(name)
			markers = append(markers, marker{
				name:      normalized,
				start:     m[0],
				markerEnd: m[1],
			})
		}
		for i := 0; i < len(markers); i++ {
			start := markers[i].markerEnd
			end := len(content)
			if i+1 < len(markers) {
				end = markers[i+1].start
			}
			result[markers[i].name] = strings.TrimSpace(content[start:end])
		}
		return result
	}

	// 回退到 Markdown 标题匹配
	headingPattern := regexp.MustCompile("(?im)^\\s{0,3}#{1,3}\\s+(.+?)\\s*$")
	headingMatches := headingPattern.FindAllStringSubmatchIndex(content, -1)
	for i, m := range headingMatches {
		heading := content[m[2]:m[3]]
		canonical := canonicalSectionNameFromHeading(heading)
		if canonical == "" {
			continue
		}
		start := m[1]
		end := len(content)
		if i+1 < len(headingMatches) {
			end = headingMatches[i+1][0]
		}
		result[canonical] = strings.TrimSpace(content[start:end])
	}
	return result
}

// normalizeSectionName 规范化 section 名。
func normalizeSectionName(name string) string {
	s := strings.TrimSpace(name)
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
	s = regexp.MustCompile("_+").ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

// canonicalSectionNameFromHeading 从 Markdown 标题推导规范 section 名。
func canonicalSectionNameFromHeading(heading string) string {
	normalized := strings.ToLower(heading)
	checks := map[string]string{
		"story_frame":       "story_frame",
		"story_bible":       "story_frame",
		"故事框架":             "story_frame",
		"故事圣经":             "story_frame",
		"基础设定":             "story_frame",
		"volume_map":        "volume_map",
		"volume_outline":    "volume_map",
		"分卷地图":             "volume_map",
		"卷纲":               "volume_map",
		"分卷大纲":             "volume_map",
		"roles":             "roles",
		"characters":        "roles",
		"角色设定":             "roles",
		"人物设定":             "roles",
		"角色卡":              "roles",
		"book_rules":        "book_rules",
		"rules":             "book_rules",
		"本书规则":             "book_rules",
		"写作规则":             "book_rules",
		"规则卡":              "book_rules",
		"pending_hooks":     "pending_hooks",
		"hooks":             "pending_hooks",
		"待回收钩子":            "pending_hooks",
		"待回收伏笔":            "pending_hooks",
		"伏笔表":              "pending_hooks",
		"钩子表":              "pending_hooks",
		"伏笔":               "pending_hooks",
		"rhythm_principles": "rhythm_principles",
		"节奏原则":             "rhythm_principles",
		"current_state":     "current_state",
		"当前状态":             "current_state",
		"初始状态":             "current_state",
	}
	for keyword, canonical := range checks {
		if strings.Contains(normalized, keyword) {
			return canonical
		}
	}
	return ""
}

// parseRoles 解析 ---ROLE---/---CONTENT--- 块为角色列表。
func parseRoles(raw string) []models.ArchitectRole {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []models.ArchitectRole{}
	}

	roleSplitPattern := regexp.MustCompile("(?m)^---ROLE---$")
	blocks := roleSplitPattern.Split(raw, -1)
	var roles []models.ArchitectRole

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		contentSplitPattern := regexp.MustCompile("(?m)^---CONTENT---$")
		contentParts := contentSplitPattern.Split(block, 2)
		if len(contentParts) < 2 {
			continue
		}

		headerRaw := strings.TrimSpace(contentParts[0])
		content := strings.TrimSpace(contentParts[1])
		if headerRaw == "" || content == "" {
			continue
		}

		tierMatch := regexp.MustCompile("(?i)tier\\s*[:：]\\s*(major|minor|主要|次要)").FindStringSubmatch(headerRaw)
		nameMatch := regexp.MustCompile("(?i)name\\s*[:：]\\s*(.+)").FindStringSubmatch(headerRaw)
		if tierMatch == nil || nameMatch == nil {
			continue
		}

		tierValue := strings.ToLower(tierMatch[1])
		tier := "major"
		if tierValue == "minor" || tierValue == "次要" {
			tier = "minor"
		}
		name := strings.TrimSpace(nameMatch[1])

		roles = append(roles, models.ArchitectRole{
			Tier:    tier,
			Name:    name,
			Content: content,
		})
	}
	return roles
}

// stripTrailingAssistantCoda 去掉 LLM 末尾的"如果你愿意..."之类的尾注。
func stripTrailingAssistantCoda(section string) string {
	lines := strings.Split(section, "\n")
	cutoff := -1
	codaPattern := regexp.MustCompile("(?i)^(如果(?:你愿意|需要|想要|希望)|If (?:you(?:'d)? like|you want|needed)|I can (?:continue|next))")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if codaPattern.MatchString(trimmed) {
			cutoff = i
			break
		}
	}
	if cutoff < 0 {
		return section
	}
	return strings.TrimRight(strings.Join(lines[:cutoff], "\n"), " \t\r\n")
}

// buildStoryBibleShim 构建 story_bible.md 兼容指针内容。
func buildStoryBibleShim(storyFrame string, language string) string {
	if language == "en" {
		return fmt.Sprintf("# Story Bible (compat pointer — deprecated)\n\n> Authoritative source: outline/story_frame.md\n\n## Excerpt from story_frame\n\n%s\n", truncateRunes(storyFrame, 2000))
	}
	return fmt.Sprintf("# 故事圣经（兼容指针——已废弃）\n\n> 权威来源已迁移至 outline/story_frame.md\n\n## story_frame 摘录\n\n%s\n", truncateRunes(storyFrame, 2000))
}

// getMapValue 安全获取 map 值。
func getMapValue(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return ""
}

// sanitizeFileName 清理文件名中的非法字符。
func sanitizeFileName(name string) string {
	safe := regexp.MustCompile(`[/\\:*?"<>|]`).ReplaceAllString(name, "_")
	return strings.TrimSpace(safe)
}

// truncateRunes 按 rune 截断字符串。
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
