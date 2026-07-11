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

// ConsolidatorAgent 卷级合并 Agent。
type ConsolidatorAgent struct {
	BaseAgent
}

// NewConsolidatorAgent 创建卷级合并 Agent。
func NewConsolidatorAgent(ctx llm.AgentContext) *ConsolidatorAgent {
	return &ConsolidatorAgent{BaseAgent: NewBaseAgent(ctx, "consolidator")}
}

// VolumeBoundary 卷边界。
type VolumeBoundary struct {
	Name    string
	StartCh int
	EndCh   int
}

// SummaryRow 章节摘要表行。
type SummaryRow struct {
	Chapter int
	Raw     string
}

// CompletedVolume 已完成卷（含摘要行）。
type CompletedVolume struct {
	Name    string
	StartCh int
	EndCh   int
	Rows    []SummaryRow
}

// Consolidate 执行卷级合并。
func (a *ConsolidatorAgent) Consolidate(ctx context.Context, bookDir string) (*models.ConsolidationResult, error) {
	storyDir := filepath.Join(bookDir, "story")
	summariesPath := filepath.Join(storyDir, "chapter_summaries.md")
	volumeSummariesPath := filepath.Join(storyDir, "volume_summaries.md")

	// 读取 chapter_summaries.md + volume_map.md
	summariesRaw, _ := os.ReadFile(summariesPath)
	outlineRaw := readVolumeMap(bookDir)

	// 伏笔晋升检查（独立于摘要合并，总是执行）
	promotedHookCount := a.rerunAdvancedCountPromotion(storyDir)

	// 如果任一文件缺失，直接返回
	if len(summariesRaw) == 0 || len(outlineRaw) == 0 {
		return &models.ConsolidationResult{
			VolumeSummaries:   "",
			ArchivedVolumes:   0,
			RetainedChapters:  0,
			PromotedHookCount: promotedHookCount,
		}, nil
	}

	// 解析卷边界
	volumeBoundaries := parseVolumeBoundaries(string(outlineRaw))
	if len(volumeBoundaries) == 0 {
		return &models.ConsolidationResult{
			VolumeSummaries:   "",
			ArchivedVolumes:   0,
			RetainedChapters:  0,
			PromotedHookCount: promotedHookCount,
		}, nil
	}

	// 解析摘要表
	header, rows := parseSummaryTable(string(summariesRaw))
	if len(rows) == 0 {
		return &models.ConsolidationResult{
			VolumeSummaries:   "",
			ArchivedVolumes:   0,
			RetainedChapters:  0,
			PromotedHookCount: promotedHookCount,
		}, nil
	}

	// 计算最大章节号
	maxChapter := 0
	for _, r := range rows {
		if r.Chapter > maxChapter {
			maxChapter = r.Chapter
		}
	}

	// 分类：已完成卷 vs 当前卷
	var completedVolumes []CompletedVolume
	var currentVolumeRows []SummaryRow

	for _, vol := range volumeBoundaries {
		var volRows []SummaryRow
		for _, r := range rows {
			if r.Chapter >= vol.StartCh && r.Chapter <= vol.EndCh {
				volRows = append(volRows, r)
			}
		}
		if vol.EndCh <= maxChapter && len(volRows) > 0 {
			completedVolumes = append(completedVolumes, CompletedVolume{
				Name: vol.Name, StartCh: vol.StartCh, EndCh: vol.EndCh, Rows: volRows,
			})
		} else {
			currentVolumeRows = append(currentVolumeRows, volRows...)
		}
	}

	// 保留不被任何卷覆盖的行
	coveredChapters := make(map[int]bool)
	for _, vol := range volumeBoundaries {
		for ch := vol.StartCh; ch <= vol.EndCh; ch++ {
			coveredChapters[ch] = true
		}
	}
	for _, r := range rows {
		if !coveredChapters[r.Chapter] {
			currentVolumeRows = append(currentVolumeRows, r)
		}
	}

	// 没有已完成卷则返回
	if len(completedVolumes) == 0 {
		return &models.ConsolidationResult{
			VolumeSummaries:   "",
			ArchivedVolumes:   0,
			RetainedChapters:  len(currentVolumeRows),
			PromotedHookCount: promotedHookCount,
		}, nil
	}

	// 读取已有的 volume_summaries.md
	existingVolSummaries, _ := os.ReadFile(volumeSummariesPath)
	var newSummaries []string
	if len(existingVolSummaries) > 0 {
		newSummaries = append(newSummaries, strings.TrimSpace(string(existingVolSummaries)))
	} else {
		newSummaries = append(newSummaries, "# Volume Summaries")
	}

	// 对每个已完成卷调用 LLM 压缩
	for _, vol := range completedVolumes {
		var volSummaryRows []string
		for _, r := range vol.Rows {
			volSummaryRows = append(volSummaryRows, r.Raw)
		}

		systemPrompt := "You are a narrative summarizer. Compress chapter-by-chapter summaries into a single coherent paragraph (max 500 words) that captures the key events, character developments, and plot progression of this volume. Preserve specific names, locations, and plot points. Write in the same language as the input."

		userPrompt := fmt.Sprintf("Volume: %s (Chapters %d-%d)\n\nChapter summaries:\n%s\n%s",
			vol.Name, vol.StartCh, vol.EndCh, header, strings.Join(volSummaryRows, "\n"))

		temp := 0.3
		response, err := a.Chat(ctx, []llm.Message{
			{Role: llm.RoleSystem, Content: systemPrompt},
			{Role: llm.RoleUser, Content: userPrompt},
		}, &llm.ChatOptions{Temperature: &temp})
		if err != nil {
			a.LogWarn(fmt.Sprintf("consolidator LLM call failed for volume %s: %v", vol.Name, err))
			continue
		}

		newSummaries = append(newSummaries, fmt.Sprintf("\n## %s (Ch.%d-%d)\n\n%s",
			vol.Name, vol.StartCh, vol.EndCh, strings.TrimSpace(response.Content)))
	}

	// 写入 volume_summaries.md
	volSummariesContent := strings.Join(newSummaries, "\n")
	if err := writeFileSafe(volumeSummariesPath, volSummariesContent); err != nil {
		return nil, fmt.Errorf("write volume_summaries.md: %w", err)
	}

	// 归档已完成卷的详细摘要
	archiveDir := filepath.Join(storyDir, "summaries_archive")
	os.MkdirAll(archiveDir, 0755)
	for _, vol := range completedVolumes {
		archivePath := filepath.Join(archiveDir, fmt.Sprintf("vol_%d-%d.md", vol.StartCh, vol.EndCh))
		var volRows []string
		for _, r := range vol.Rows {
			volRows = append(volRows, r.Raw)
		}
		archiveContent := fmt.Sprintf("# %s\n\n%s\n%s", vol.Name, header, strings.Join(volRows, "\n"))
		writeFileSafe(archivePath, archiveContent)
	}

	// 重写 chapter_summaries.md（仅保留当前卷行）
	var retainedContent string
	if len(currentVolumeRows) > 0 {
		var rowsContent []string
		for _, r := range currentVolumeRows {
			rowsContent = append(rowsContent, r.Raw)
		}
		retainedContent = fmt.Sprintf("%s\n%s\n", header, strings.Join(rowsContent, "\n"))
	} else {
		retainedContent = fmt.Sprintf("%s\n", header)
	}
	writeFileSafe(summariesPath, retainedContent)

	return &models.ConsolidationResult{
		VolumeSummaries:   volSummariesContent,
		ArchivedVolumes:   len(completedVolumes),
		RetainedChapters:  len(currentVolumeRows),
		PromotedHookCount: promotedHookCount,
	}, nil
}

// rerunAdvancedCountPromotion 重新运行伏笔晋升检查。
func (a *ConsolidatorAgent) rerunAdvancedCountPromotion(storyDir string) int {
	ledgerPath := filepath.Join(storyDir, "pending_hooks.md")
	raw, err := os.ReadFile(ledgerPath)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return 0
	}

	hooks := parsePendingHooksMarkdown(string(raw))
	if len(hooks) == 0 {
		return 0
	}

	summariesRaw, _ := os.ReadFile(filepath.Join(storyDir, "chapter_summaries.md"))
	summariesContent := string(summariesRaw)

	flipped := 0
	for i := range hooks {
		if hooks[i].Promoted {
			continue
		}
		advancedCount := countHookAdvancements(hooks[i].HookID, summariesContent)
		if advancedCount >= 2 {
			hooks[i].Promoted = true
			flipped++
		}
	}

	if flipped > 0 {
		language := "zh"
		if !containsChinese(string(raw)) {
			language = "en"
		}
		rendered := renderHookSnapshotSimple(hooks, language)
		os.WriteFile(ledgerPath, []byte(rendered), 0644)
	}

	return flipped
}

// parseVolumeBoundaries 从 volume_map.md 解析卷边界。
func parseVolumeBoundaries(outline string) []VolumeBoundary {
	var volumes []VolumeBoundary

	volumeHeader := regexp.MustCompile("(?i)^(第[一二三四五六七八九十百千万零〇\\d]+卷|Volume\\s+\\d+)")
	rangePattern := regexp.MustCompile("(?i)[（(]\\s*(?:第|[Cc]hapters?\\s+)?(\\d+)\\s*[-–~～—]\\s*(\\d+)\\s*(?:章)?\\s*[）)]|(?:第|[Cc]hapters?\\s+)(\\d+)\\s*[-–~～—]\\s*(\\d+)\\s*(?:章)?")

	for _, rawLine := range strings.Split(outline, "\n") {
		line := strings.TrimSpace(rawLine)
		line = regexp.MustCompile("^#+\\s*").ReplaceAllString(line, "")
		line = strings.TrimSpace(line)
		if !volumeHeader.MatchString(line) {
			continue
		}

		rangeMatch := rangePattern.FindStringSubmatch(line)
		if rangeMatch == nil {
			continue
		}

		startStr := rangeMatch[1]
		if startStr == "" {
			startStr = rangeMatch[3]
		}
		endStr := rangeMatch[2]
		if endStr == "" {
			endStr = rangeMatch[4]
		}

		startCh, _ := strconv.Atoi(startStr)
		endCh, _ := strconv.Atoi(endStr)
		if startCh <= 0 || endCh <= 0 {
			continue
		}

		rangeIndex := rangePattern.FindStringIndex(line)[0]
		name := strings.TrimSpace(strings.TrimRight(line[:rangeIndex], "（( "))
		if name != "" {
			volumes = append(volumes, VolumeBoundary{Name: name, StartCh: startCh, EndCh: endCh})
		}
	}
	return volumes
}

// parseSummaryTable 解析章节摘要表。
func parseSummaryTable(raw string) (string, []SummaryRow) {
	lines := strings.Split(raw, "\n")

	var headerLines []string
	var dataLines []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		if strings.Contains(line, "章节") || strings.Contains(line, "Chapter") || strings.Contains(line, "---") {
			headerLines = append(headerLines, line)
		} else {
			dataLines = append(dataLines, line)
		}
	}
	header := strings.Join(headerLines, "\n")

	chapterPattern := regexp.MustCompile(`\|\s*(\d+)\s*\|`)
	var rows []SummaryRow
	for _, line := range dataLines {
		match := chapterPattern.FindStringSubmatch(line)
		if match != nil {
			chNum, _ := strconv.Atoi(match[1])
			if chNum > 0 {
				rows = append(rows, SummaryRow{Chapter: chNum, Raw: line})
			}
		}
	}
	return header, rows
}

// readVolumeMap 读取 volume_map.md（fallback: volume_outline.md）。
func readVolumeMap(bookDir string) []byte {
	storyDir := filepath.Join(bookDir, "story")
	volumeMapPath := filepath.Join(storyDir, "outline", "volume_map.md")
	if data, err := os.ReadFile(volumeMapPath); err == nil && len(data) > 0 {
		return data
	}
	// fallback
	legacyPath := filepath.Join(storyDir, "volume_outline.md")
	data, _ := os.ReadFile(legacyPath)
	return data
}

// parsePendingHooksMarkdown 解析 pending_hooks.md 为伏笔列表。
func parsePendingHooksMarkdown(raw string) []models.StoredHook {
	lines := strings.Split(raw, "\n")
	var hooks []models.StoredHook

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		if strings.Contains(line, "---") || strings.Contains(line, "hook_id") || strings.Contains(line, "Hook") {
			continue
		}

		cells := strings.Split(line, "|")
		if len(cells) < 5 {
			continue
		}
		// 去掉首尾空 cell
		var cellValues []string
		for _, c := range cells {
			c = strings.TrimSpace(c)
			if c != "" {
				cellValues = append(cellValues, c)
			}
		}
		if len(cellValues) < 3 {
			continue
		}

		hookID := cellValues[0]
		if hookID == "" {
			continue
		}

		startCh := 0
		if len(cellValues) > 1 {
			startCh, _ = strconv.Atoi(cellValues[1])
		}

		hookType := ""
		if len(cellValues) > 2 {
			hookType = cellValues[2]
		}

		status := ""
		if len(cellValues) > 3 {
			status = cellValues[3]
		}

		hooks = append(hooks, models.StoredHook{
			HookID:       hookID,
			StartChapter: startCh,
			Type:         hookType,
			Status:       models.HookStatus(status),
		})
	}
	return hooks
}

// countHookAdvancements 统计伏笔在摘要中被提及的章节数。
func countHookAdvancements(hookID string, summariesContent string) int {
	if summariesContent == "" {
		return 0
	}
	return strings.Count(summariesContent, hookID)
}

// renderHookSnapshotSimple 将伏笔列表渲染为 markdown 表格。
func renderHookSnapshotSimple(hooks []models.StoredHook, language string) string {
	if language == "en" {
		var sb strings.Builder
		sb.WriteString("| hook_id | start_chapter | type | status | last_advanced_chapter | expected_payoff | payoff_timing | depends_on | pays_off_in_arc | core_hook | half_life | notes |\n")
		sb.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
		for _, h := range hooks {
			coreHook := "false"
			if h.CoreHook {
				coreHook = "true"
			}
			promoted := ""
			if h.Promoted {
				promoted = " (promoted)"
			}
			sb.WriteString(fmt.Sprintf("| %s | %d | %s | %s%s | %d | %s | %s | %s | %s | %s | %d | %s |\n",
				h.HookID, h.StartChapter, h.Type, h.Status, promoted,
				h.LastAdvancedChapter, h.ExpectedPayoff, h.PayoffTiming,
				h.DependsOn, h.PaysOffInArc, coreHook, h.HalfLife, h.Notes))
		}
		return sb.String()
	}

	var sb strings.Builder
	sb.WriteString("| hook_id | start_chapter | type | status | last_advanced_chapter | expected_payoff | payoff_timing | depends_on | pays_off_in_arc | core_hook | half_life | notes |\n")
	sb.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, h := range hooks {
		coreHook := "false"
		if h.CoreHook {
			coreHook = "true"
		}
		promoted := ""
		if h.Promoted {
			promoted = " (promoted)"
		}
		sb.WriteString(fmt.Sprintf("| %s | %d | %s | %s%s | %d | %s | %s | %s | %s | %s | %d | %s |\n",
			h.HookID, h.StartChapter, h.Type, h.Status, promoted,
			h.LastAdvancedChapter, h.ExpectedPayoff, h.PayoffTiming,
			h.DependsOn, h.PaysOffInArc, coreHook, h.HalfLife, h.Notes))
	}
	return sb.String()
}
