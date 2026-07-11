# 13 - AIGC 检测流程

> 对应 inkos 的 `detectChapter` / `detectAndRewrite` / `analyzeAITells`。
> 参考源码：`inkos/packages/core/src/pipeline/detection-runner.ts`、
> `agents/detector.ts`、`agents/ai-tells.ts`、`agents/detection-insights.ts`、
> `agents/reviser.ts`（`anti-detect` 模式）。

---

## 目录

- [一、流程概述](#一流程概述)
- [二、检测配置](#二检测配置)
- [三、detectChapter：单章检测](#三detectchapter单章检测)
  - [3.1 detectAIContent：外部 AIGC 检测 API](#31-detectaicontent外部-aigc-检测-api)
  - [3.2 三种检测提供商](#32-三种检测提供商)
  - [3.3 analyzeAITells：结构化 AI 痕迹分析](#33-analyzeaitells结构化-ai-痕迹分析)
  - [3.4 四个检测维度](#34-四个检测维度)
- [四、detectAndRewrite：检测后自动重写](#四detectandrewrite检测后自动重写)
  - [4.1 重写循环](#41-重写循环)
  - [4.2 ReviserAgent anti-detect 模式](#42-reviseragent-anti-detect-模式)
- [五、反检测改写 9 种手法详解](#五反检测改写-9-种手法详解)
- [六、检测历史与洞察](#六检测历史与洞察)
- [七、完整 Go 接口定义](#七完整-go-接口定义)

---

## 一、流程概述

AIGC 检测流程在章节写完并通过审查后执行，检测章节正文的 AI 生成痕迹比例。当 AI 比例超过阈值时，自动触发 ReviserAgent 以 `anti-detect` 模式重写，降低 AI 可检测性。检测-重写循环最多执行 `maxRetries` 次，直到分数降至阈值以下或重试耗尽。

```
章节写作完成 + 审查通过
  │
  ├─ detection.enabled? → 否则跳过
  │
  ├─ detectAndRewrite(config, ctx, bookDir, content, chapterNumber, genre)
  │   │
  │   ├─ 首次检测：detectAIContent(content)
  │   │   └─ score <= threshold? → 直接通过，记录 detect 历史
  │   │
  │   ├─ score > threshold? → 进入重写循环
  │   │   └─ for i in 0..maxRetries:
  │   │       ├─ ReviserAgent.reviseChapter(mode="anti-detect", issues=[AIGC检测分数超标])
  │   │       ├─ 重新检测：detectAIContent(revisedContent)
  │   │       ├─ 记录 rewrite 历史
  │   │       └─ score <= threshold? → break
  │   │
  │   └─ 返回 {originalScore, finalScore, attempts, passed, finalContent}
  │
  └─ 写入最终正文到磁盘
```

---

## 二、检测配置

```go
type DetectionConfig struct {
    Enabled   bool    `json:"enabled"`     // 是否启用检测
    Provider  string  `json:"provider"`    // gptzero / originality / custom
    ApiURL    string  `json:"apiUrl"`      // 检测 API 地址
    ApiKeyEnv string  `json:"apiKeyEnv"`   // API Key 环境变量名
    Threshold float64 `json:"threshold"`   // AI 比例阈值（0-1），超过则触发重写
    MaxRetries int    `json:"maxRetries"`  // 最大重写重试次数
}
```

---

## 三、detectChapter：单章检测

### 3.1 detectAIContent：外部 AIGC 检测 API

`detectAIContent` 调用外部 AIGC 检测 API，返回归一化的 AI 分数（0=人类，1=AI）。

**注意**：DetectorAgent **不是** BaseAgent 的子类，因为它不使用 LLM provider，而是直接调用外部检测 API。

```go
func detectAIContent(config DetectionConfig, content string) (*DetectionResult, error) {
    apiKey := os.Getenv(config.ApiKeyEnv)
    if apiKey == "" {
        return nil, fmt.Errorf("Detection API key not found. Set %s in your environment.", config.ApiKeyEnv)
    }

    detectedAt := time.Now().UTC().Format(time.RFC3339)

    switch config.Provider {
    case "gptzero":
        return detectGPTZero(config.ApiURL, apiKey, content, detectedAt)
    case "originality":
        return detectOriginality(config.ApiURL, apiKey, content, detectedAt)
    case "custom":
        return detectCustom(config.ApiURL, apiKey, content, detectedAt)
    default:
        return nil, fmt.Errorf("unknown detection provider: %s", config.Provider)
    }
}
```

### 3.2 三种检测提供商

#### GPTZero

```
POST {apiUrl}
Headers: Content-Type: application/json, X-Api-Key: {apiKey}
Body: { "document": "{content}" }

Response:
{
  "documents": [
    { "completely_generated_prob": 0.87 }
  ]
}

score = documents[0].completely_generated_prob
```

#### Originality

```
POST {apiUrl}
Headers: Content-Type: application/json, Authorization: Bearer {apiKey}
Body: { "content": "{content}" }

Response:
{
  "score": { "ai": 0.92 }
}

score = response.score.ai
```

#### Custom

```
POST {apiUrl}
Headers: Content-Type: application/json, Authorization: Bearer {apiKey}
Body: { "content": "{content}" }

Response: { "score": 0.75 }  // 自定义端点必须返回 score 数字字段

score = response.score
```

### 3.3 analyzeAITells：结构化 AI 痕迹分析

`analyzeAITells` 是**纯规则驱动**的分析（不调用 LLM），检测 AI 生成文本中常见的结构化模式。它独立于外部检测 API，可以在任何环境下运行。

```go
func analyzeAITells(content string, language AITellLanguage) *AITellResult {
    issues := []AITellIssue{}

    // dim 20: 段落长度均匀性
    // dim 21: 套话/模糊词密度
    // dim 22: 公式化转折词重复
    // dim 23: 列表式结构

    return &AITellResult{Issues: issues}
}
```

### 3.4 四个检测维度

#### dim 20：段落长度均匀性（Paragraph Uniformity）

检测段落长度是否过于均匀——AI 生成文本常呈现等长段落。

```
1. 按空行分割段落
2. 计算每段字符数
3. 计算变异系数 CV = stdDev / mean
4. 如果段落数 >= 3 且 CV < 0.15 → 报警
```

| 指标 | 值 |
|------|-----|
| 最少段落数 | 3 |
| CV 阈值 | < 0.15 |
| 严重度 | warning |

**建议**：增加段落长度差异——短段落用于节奏加速或冲击，长段落用于沉浸描写。

#### dim 21：套话/模糊词密度（Hedge Word Density）

检测模糊修饰词密度——AI 文本常使用"似乎""可能""或许"等模糊表达。

```
1. 定义套话词表：
   zh: ["似乎", "可能", "或许", "大概", "某种程度上", "一定程度上", "在某种意义上"]
   en: ["seems", "seemed", "perhaps", "maybe", "apparently", "in some ways", "to some extent"]
2. 统计每个词出现次数
3. 计算 density = count / (totalChars / 1000)
4. 如果 density > 3 → 报警
```

| 指标 | 值 |
|------|-----|
| 密度阈值 | > 3 次/千字 |
| 严重度 | warning |

**建议**：用确定性叙述替代模糊表达——去掉"似乎"直接描述状态，用具体细节替代"可能"。

#### dim 22：公式化转折词重复（Formulaic Transitions）

检测转折词重复使用——AI 文本常重复"然而""不过""与此同时"等转折模式。

```
1. 定义转折词表：
   zh: ["然而", "不过", "与此同时", "另一方面", "尽管如此", "话虽如此", "但值得注意的是"]
   en: ["however", "meanwhile", "on the other hand", "nevertheless", "even so", "still"]
2. 统计每个词出现次数
3. 如果某词出现 >= 3 次 → 报警
```

| 指标 | 值 |
|------|-----|
| 重复阈值 | >= 3 次 |
| 严重度 | warning |

**建议**：用情节自然转折替代转折词，或换用不同过渡手法（动作切入、时间跳跃、视角切换）。

#### dim 23：列表式结构（List-like Structure）

检测连续同前缀句式——AI 文本常出现"他...他...他..."的列表式句式。

```
1. 按句号/感叹号/问号/换行分割句子
2. 取每句前 2 字（中文）或第一个空格前单词（英文）作为前缀
3. 统计连续相同前缀的最大长度
4. 如果 maxConsecutive >= 3 → 报警
```

| 指标 | 值 |
|------|-----|
| 最少连续数 | 3 |
| 严重度 | info |

**建议**：变换句式开头——用不同主语、时间词、动作词开头，打破列表感。

---

## 四、detectAndRewrite：检测后自动重写

### 4.1 重写循环

```go
func detectAndRewrite(
    config DetectionConfig,
    ctx AgentContext,
    bookDir string,
    content string,
    chapterNumber int,
    genre string,
) (*DetectAndRewriteResult, error) {
    maxRetries := config.MaxRetries

    currentContent := content
    firstDetection, err := detectAIContent(config, currentContent)
    if err != nil {
        return nil, err
    }
    originalScore := firstDetection.Score

    // 首次检测就通过
    if originalScore <= config.Threshold {
        recordHistory(bookDir, DetectionHistoryEntry{
            ChapterNumber: chapterNumber,
            Timestamp:     firstDetection.DetectedAt,
            Provider:      firstDetection.Provider,
            Score:         firstDetection.Score,
            Action:        "detect",
            Attempt:       0,
        })
        return &DetectAndRewriteResult{
            ChapterNumber: chapterNumber,
            OriginalScore: originalScore,
            FinalScore:    firstDetection.Score,
            Attempts:      0,
            Passed:        true,
            FinalContent:  currentContent,
        }, nil
    }

    // 重写循环
    finalScore := originalScore
    attempts := 0

    for i := 0; i < maxRetries; i++ {
        attempts = i + 1

        // anti-detect 模式重写
        reviser := NewReviserAgent(ctx)
        reviseOutput, err := reviser.ReviseChapter(
            bookDir, currentContent, chapterNumber,
            []AuditIssue{{
                Severity:   "warning",
                Category:   "AIGC检测",
                Description: fmt.Sprintf("AI检测分数 %.2f 超过阈值 %.2f", finalScore, config.Threshold),
                Suggestion: "降低AI生成痕迹：增加段落长度差异、减少套话、用口语化表达替代书面语",
            }},
            "anti-detect",
            genre,
        )
        if err != nil {
            break
        }
        if len(reviseOutput.RevisedContent) == 0 {
            break
        }
        currentContent = reviseOutput.RevisedContent

        // 重新检测
        reDetection, err := detectAIContent(config, currentContent)
        if err != nil {
            break
        }
        finalScore = reDetection.Score

        recordHistory(bookDir, DetectionHistoryEntry{
            ChapterNumber: chapterNumber,
            Timestamp:     reDetection.DetectedAt,
            Provider:      reDetection.Provider,
            Score:         reDetection.Score,
            Action:        "rewrite",
            Attempt:       attempts,
        })

        if finalScore <= config.Threshold {
            break
        }
    }

    return &DetectAndRewriteResult{
        ChapterNumber: chapterNumber,
        OriginalScore: originalScore,
        FinalScore:    finalScore,
        Attempts:      attempts,
        Passed:        finalScore <= config.Threshold,
        FinalContent:  currentContent,
    }, nil
}
```

### 4.2 ReviserAgent anti-detect 模式

ReviserAgent 的 `anti-detect` 模式是专门为降低 AI 可检测性设计的改写模式。其 System Prompt 包含 9 种反检测改写手法，要求在保持剧情不变的前提下降低 AI 生成痕迹。

```go
modeDescription := `反检测改写：在保持剧情不变的前提下，降低AI生成可检测性。

改写手法（附正例）：
1. 打破句式规律：连续短句 → 长短交替，句式不可预测
2. 口语化替代：✗"然而事情并没有那么简单" → ✓"哪有那么便宜的事"
3. 减少"了"字密度：✗"他走了过去，拿了杯子" → ✓"他走过去，端起杯子"
4. 转折词降频：✗"虽然…但是…" → ✓ 用角色内心吐槽或直接动作切换
5. 情绪外化：✗"他感到愤怒" → ✓"他捏碎了茶杯，滚烫的茶水流过指缝"
6. 删掉叙述者结论：✗"这一刻他终于明白了力量" → ✓ 只写行动，让读者自己感受
7. 群像反应具体化：✗"全场震惊" → ✓"老陈的烟掉在裤子上，烫得他跳起来"
8. 段落长度差异化：不再等长段落，有的段只有一句话，有的段七八行
9. 消灭"不禁""仿佛""宛如"等AI标记词：换成具体感官描写`
```

**关键约束**：anti-detect 模式只改表达和节奏，不改剧情、事实、人名、地名、物品名。

---

## 五、反检测改写 9 种手法详解

### 手法 1：打破句式规律

**问题**：AI 生成的文本常呈现连续短句或连续长句的规律节奏。

**改法**：连续短句 → 长短交替，句式不可预测。

**示例**：
```
✗ AI：他站了起来。他走向门口。他打开了门。他看到了外面的雨。
✓ 人：他站起来，走向门口时犹豫了一瞬——门外的雨声比想象中更大。
```

### 手法 2：口语化替代

**问题**：AI 倾向使用书面化、正式化的表达。

**改法**：用口语化表达替代书面套话。

**示例**：
```
✗ AI："然而事情并没有那么简单"
✓ 人："哪有那么便宜的事"
```

### 手法 3：减少"了"字密度

**问题**：AI 生成中文时过度使用"了"字作为动作完成标记。

**改法**：用其他动词替代"了"字结构。

**示例**：
```
✗ AI："他走了过去，拿了杯子"
✓ 人："他走过去，端起杯子"
```

### 手法 4：转折词降频

**问题**：AI 文本中"虽然...但是...""然而"等转折词密度过高。

**改法**：用角色内心吐槽或直接动作切换替代转折词。

**示例**：
```
✗ AI："虽然他很累，但是他还是继续走了下去。"
✓ 人："腿像灌了铅，他骂了一声，还是往前挪。"
```

### 手法 5：情绪外化

**问题**：AI 倾向直接陈述情绪（"他感到愤怒"），而非通过行为展示。

**改法**：将情绪转化为具体的身体动作和感官细节。

**示例**：
```
✗ AI："他感到愤怒"
✓ 人："他捏碎了茶杯，滚烫的茶水流过指缝"
```

### 手法 6：删掉叙述者结论

**问题**：AI 常在段落末尾添加总结性结论，替读者解读。

**改法**：只写行动，让读者自己感受，删除"终于明白了""这一刻他懂了"等结论句。

**示例**：
```
✗ AI："这一刻他终于明白了力量的意义"
✓ 人：（只写行动，不写结论）
```

### 手法 7：群像反应具体化

**问题**：AI 描写群体反应时常用"全场震惊""众人哗然"等抽象概括。

**改法**：用具体个人的具体反应替代抽象群体描写。

**示例**：
```
✗ AI："全场震惊"
✓ 人："老陈的烟掉在裤子上，烫得他跳起来"
```

### 手法 8：段落长度差异化

**问题**：AI 生成的段落长度趋于均匀（对应 dim 20 检测）。

**改法**：刻意制造段落长度差异——有的段只有一句话，有的段七八行。

**示例**：
```
✗ AI：（五段都是 3-4 行，长度均匀）
✓ 人：
  第一段：一句话。

  第二段：七八行的沉浸描写，详细铺展环境氛围、人物动作和内心活动，
  让读者完全沉浸在场景中，感受角色的处境和情绪波动。

  第三段：又是一句话。
```

### 手法 9：消灭 AI 标记词

**问题**：AI 生成中文时高频使用"不禁""仿佛""宛如""不由得""似乎"等标记词。

**改法**：用具体感官描写替代标记词。

**示例**：
```
✗ AI："他不禁笑了起来" / "仿佛时间静止了" / "宛如一幅画"
✓ 人："他笑出了声" / "时间像是黏住了" / "画面定格在那里"
```

**常见 AI 标记词清单**（应在文本中尽量消灭）：

| 标记词 | 替代方向 |
|--------|----------|
| 不禁 | 直接写动作 |
| 仿佛 | 用比喻或直接描写 |
| 宛如 | 用比喻或直接描写 |
| 不由得 | 直接写动作 |
| 似乎 | 去掉或用确定描述 |
| 不约而同 | 写具体不同人的反应 |
| 莫名其妙 | 写具体原因或不解释 |
| 心头一震 | 写具体身体反应 |
| 若有所思 | 写具体表情或动作 |

---

## 六、检测历史与洞察

### 6.1 检测历史记录

每次检测和重写都会记录到 `story/detection_history.json`：

```go
type DetectionHistoryEntry struct {
    ChapterNumber int       `json:"chapterNumber"`
    Timestamp     string    `json:"timestamp"`  // ISO 8601
    Provider      string    `json:"provider"`   // gptzero / originality / custom
    Score         float64   `json:"score"`      // 0-1
    Action        string    `json:"action"`     // "detect" 或 "rewrite"
    Attempt       int       `json:"attempt"`    // 0=首次检测，1+=重写轮次
}
```

### 6.2 检测洞察统计

`analyzeDetectionInsights` 聚合历史数据，产出统计报告：

```go
type DetectionStats struct {
    TotalDetections     int                          `json:"totalDetections"`
    TotalRewrites       int                          `json:"totalRewrites"`
    AvgOriginalScore    float64                      `json:"avgOriginalScore"`
    AvgFinalScore       float64                      `json:"avgFinalScore"`
    AvgScoreReduction   float64                      `json:"avgScoreReduction"`
    PassRate            float64                      `json:"passRate"`
    ChapterBreakdown    []ChapterDetectionBreakdown  `json:"chapterBreakdown"`
}

type ChapterDetectionBreakdown struct {
    ChapterNumber   int     `json:"chapterNumber"`
    OriginalScore   float64 `json:"originalScore"`
    FinalScore      float64 `json:"finalScore"`
    RewriteAttempts int     `json:"rewriteAttempts"`
}
```

**统计逻辑**：
- 按章节分组历史记录
- 每章取首次检测分数为 `originalScore`，最后一次为 `finalScore`
- `passRate` = finalScore <= originalScore 的章节比例
- `avgScoreReduction` = avgOriginalScore - avgFinalScore

---

## 七、完整 Go 接口定义

```go
package detection

import (
	"context"
	"time"
)

// ============================================================================
// 检测配置
// ============================================================================

// DetectionConfig AIGC 检测配置
type DetectionConfig struct {
	Enabled    bool    `json:"enabled"`
	Provider   string  `json:"provider"`   // gptzero / originality / custom
	ApiURL     string  `json:"apiUrl"`
	ApiKeyEnv  string  `json:"apiKeyEnv"`
	Threshold  float64 `json:"threshold"`  // 0-1，超过则触发重写
	MaxRetries int     `json:"maxRetries"`
}

// ============================================================================
// 检测结果
// ============================================================================

// DetectionResult AIGC 检测结果
type DetectionResult struct {
	Score      float64                `json:"score"`      // 0-1，越高越像 AI
	Provider   string                 `json:"provider"`   // gptzero / originality / custom
	DetectedAt string                 `json:"detectedAt"` // ISO 8601
	Raw        map[string]interface{} `json:"raw,omitempty"`
}

// ============================================================================
// detectChapter
// ============================================================================

// DetectChapterResult 单章检测结果
type DetectChapterResult struct {
	ChapterNumber int              `json:"chapterNumber"`
	Detection     DetectionResult  `json:"detection"`
	Passed        bool             `json:"passed"`
}

// DetectChapter 检测单章 AI 比例
func DetectChapter(
	ctx context.Context,
	config DetectionConfig,
	content string,
	chapterNumber int,
) (*DetectChapterResult, error)

// ============================================================================
// detectAIContent
// ============================================================================

// DetectAIContent 调用外部 AIGC 检测 API
func DetectAIContent(config DetectionConfig, content string) (*DetectionResult, error)

// detectGPTZero GPTZero 检测
func detectGPTZero(apiURL, apiKey, content, detectedAt string) (*DetectionResult, error)

// detectOriginality Originality 检测
func detectOriginality(apiURL, apiKey, content, detectedAt string) (*DetectionResult, error)

// detectCustom 自定义端点检测
func detectCustom(apiURL, apiKey, content, detectedAt string) (*DetectionResult, error)

// ============================================================================
// analyzeAITells（结构化 AI 痕迹分析）
// ============================================================================

// AITellLanguage 分析语言
type AITellLanguage string

const (
	AITellLangZH AITellLanguage = "zh"
	AITellLangEN AITellLanguage = "en"
)

// AITellIssue AI 痕迹问题
type AITellIssue struct {
	Severity   string `json:"severity"`   // warning / info
	Category   string `json:"category"`
	Description string `json:"description"`
	Suggestion string `json:"suggestion"`
}

// AITellResult AI 痕迹分析结果
type AITellResult struct {
	Issues []AITellIssue `json:"issues"`
}

// AnalyzeAITells 分析文本中的结构化 AI 痕迹模式（纯规则，不调用 LLM）
func AnalyzeAITells(content string, language AITellLanguage) *AITellResult

// ============================================================================
// detectAndRewrite
// ============================================================================

// DetectAndRewriteResult 检测后自动重写结果
type DetectAndRewriteResult struct {
	ChapterNumber int     `json:"chapterNumber"`
	OriginalScore float64 `json:"originalScore"`
	FinalScore    float64 `json:"finalScore"`
	Attempts      int     `json:"attempts"`
	Passed        bool    `json:"passed"`
	FinalContent  string  `json:"finalContent"`
}

// DetectAndRewrite 检测后自动重写循环
func DetectAndRewrite(
	ctx context.Context,
	config DetectionConfig,
	agentCtx AgentContext,
	bookDir string,
	content string,
	chapterNumber int,
	genre string,
) (*DetectAndRewriteResult, error)

// ============================================================================
// ReviserAgent（anti-detect 模式）
// ============================================================================

// ReviseMode 改写模式
type ReviseMode string

const (
	ReviseModeAuto       ReviseMode = "auto"
	ReviseModePolish     ReviseMode = "polish"
	ReviseModeRewrite    ReviseMode = "rewrite"
	ReviseModeRework     ReviseMode = "rework"
	ReviseModeAntiDetect ReviseMode = "anti-detect"
	ReviseModeSpotFix    ReviseMode = "spot-fix"
)

// ReviseOutput 改写输出
type ReviseOutput struct {
	RevisedContent string `json:"revisedContent"`
	WordCount      int    `json:"wordCount"`
	FixedIssues    []string `json:"fixedIssues"`
	UpdatedState   string `json:"updatedState"`
	UpdatedLedger  string `json:"updatedLedger"`
	UpdatedHooks   string `json:"updatedHooks"`
	TokenUsage     *TokenUsage `json:"tokenUsage,omitempty"`
}

// TokenUsage Token 用量
type TokenUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// ReviserAgent 改写 Agent
type ReviserAgent interface {
	// ReviseChapter 改写章节
	ReviseChapter(
		bookDir string,
		chapterContent string,
		chapterNumber int,
		issues []AuditIssue,
		mode ReviseMode,
		genre string,
	) (*ReviseOutput, error)
}

// ============================================================================
// 检测历史
// ============================================================================

// DetectionHistoryEntry 检测历史条目
type DetectionHistoryEntry struct {
	ChapterNumber int     `json:"chapterNumber"`
	Timestamp     string  `json:"timestamp"`
	Provider      string  `json:"provider"`
	Score         float64 `json:"score"`
	Action        string  `json:"action"`  // "detect" 或 "rewrite"
	Attempt       int     `json:"attempt"`
}

// RecordHistory 追加检测历史条目
func RecordHistory(bookDir string, entry DetectionHistoryEntry) error

// LoadDetectionHistory 加载检测历史
func LoadDetectionHistory(bookDir string) ([]DetectionHistoryEntry, error)

// ============================================================================
// 检测洞察
// ============================================================================

// DetectionStats 检测统计
type DetectionStats struct {
	TotalDetections   int                          `json:"totalDetections"`
	TotalRewrites     int                          `json:"totalRewrites"`
	AvgOriginalScore  float64                      `json:"avgOriginalScore"`
	AvgFinalScore     float64                      `json:"avgFinalScore"`
	AvgScoreReduction float64                      `json:"avgScoreReduction"`
	PassRate          float64                      `json:"passRate"`
	ChapterBreakdown  []ChapterDetectionBreakdown  `json:"chapterBreakdown"`
}

// ChapterDetectionBreakdown 单章检测分解
type ChapterDetectionBreakdown struct {
	ChapterNumber   int     `json:"chapterNumber"`
	OriginalScore   float64 `json:"originalScore"`
	FinalScore      float64 `json:"finalScore"`
	RewriteAttempts int     `json:"rewriteAttempts"`
}

// AnalyzeDetectionInsights 分析检测历史并产出统计
func AnalyzeDetectionInsights(history []DetectionHistoryEntry) *DetectionStats

// ============================================================================
// 依赖接口
// ============================================================================

// AgentContext Agent 上下文
type AgentContext struct {
	ProjectRoot string
	LLMClient   LLMClient
	Logger      Logger
}

// LLMClient LLM 客户端接口
type LLMClient interface {
	ChatCompletion(
		ctx context.Context,
		messages []LLMMessage,
		options *ChatOptions,
	) (*LLMResponse, error)
}

// LLMMessage LLM 消息
type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatOptions 聊天选项
type ChatOptions struct {
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"maxTokens"`
	Model       string  `json:"model"`
}

// LLMResponse LLM 响应
type LLMResponse struct {
	Content string       `json:"content"`
	Usage   *TokenUsage  `json:"usage,omitempty"`
}

// Logger 日志接口
type Logger interface {
	Warn(format string, args ...interface{})
	Info(format string, args ...interface{})
}

// AuditIssue 审查问题
type AuditIssue struct {
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion"`
}
```

---

## 附：检测流程状态机

```
                    ┌──────────────────────┐
                    │  章节写作完成+审查通过  │
                    └──────────┬───────────┘
                               │
                    ┌──────────▼───────────┐
                    │ detection.enabled?   │
                    └──────────┬───────────┘
                          No   │   Yes
                    ┌──────────┘   └──────────┐
                    │                         │
              ┌─────▼─────┐         ┌─────────▼─────────┐
              │ 跳过检测   │         │ detectAIContent   │
              │ 直接保存   │         │ (首次检测)        │
              └───────────┘         └─────────┬─────────┘
                                             │
                                   ┌─────────▼─────────┐
                                   │ score <= threshold?│
                                   └─────────┬─────────┘
                                    Yes      │     No
                              ┌──────────────┘   └──────────────┐
                              │                                 │
                    ┌─────────▼─────────┐           ┌───────────▼───────────┐
                    │ 记录 detect 历史   │           │ for i in maxRetries:  │
                    │ passed=true       │           │  ReviserAgent         │
                    └───────────────────┘           │  (anti-detect 模式)   │
                                                    │  → detectAIContent   │
                                                    │  → 记录 rewrite 历史  │
                                                    │  → score<=threshold? │
                                                    │     break            │
                                                    └───────────┬───────────┘
                                                                │
                                                    ┌───────────▼───────────┐
                                                    │ 返回 finalScore,      │
                                                    │ attempts, passed,     │
                                                    │ finalContent          │
                                                    └───────────────────────┘
```
