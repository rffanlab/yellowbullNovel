package models

// AuditSeverity 审计问题严重度
type AuditSeverity string

const (
	SeverityCritical AuditSeverity = "critical"
	SeverityWarning  AuditSeverity = "warning"
	SeverityInfo     AuditSeverity = "info"
)

// RepairScope 修复范围
type RepairScope string

const (
	ScopeLocal      RepairScope = "local"
	ScopeStructural RepairScope = "structural"
	ScopeUnknown    RepairScope = "unknown"
)

// AuditIssue 审计问题
type AuditIssue struct {
	Severity   AuditSeverity `json:"severity"`
	Category   string        `json:"category"`
	Description string       `json:"description"`
	Suggestion string        `json:"suggestion"`
	RepairScope *RepairScope `json:"repairScope,omitempty"`
}

// AuditResult 审计结果
type AuditResult struct {
	Passed      bool         `json:"passed"`
	Issues      []AuditIssue `json:"issues"`
	Summary     string       `json:"summary"`
	ParseFailed bool         `json:"parseFailed,omitempty"`
	OverallScore *int        `json:"overallScore,omitempty"`
	TokenUsage  *TokenUsage  `json:"tokenUsage,omitempty"`
}

// LengthSpec 字数规格
type LengthSpec struct {
	Target       int    `json:"target"`
	Min          int    `json:"min"`
	Max          int    `json:"max"`
	CountingMode string `json:"countingMode"` // zh_chars / en_words
}

// TokenUsage Token 使用统计
type TokenUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// Add 累加 Token 使用
func (t *TokenUsage) Add(other *TokenUsage) {
	if other == nil {
		return
	}
	t.PromptTokens += other.PromptTokens
	t.CompletionTokens += other.CompletionTokens
	t.TotalTokens += other.TotalTokens
}

// PostWriteViolation 写后校验违规
type PostWriteViolation struct {
	Rule        string `json:"rule"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion"`
	Severity    string `json:"severity"` // error / warning
}

// RuntimeStateDelta 运行时状态增量（settler 产出）
type RuntimeStateDelta struct {
	UpdatedState          string `json:"updatedState"`
	UpdatedLedger         string `json:"updatedLedger"`
	UpdatedHooks          string `json:"updatedHooks"`
	UpdatedChapterSummaries string `json:"updatedChapterSummaries"`
	UpdatedSubplots       string `json:"updatedSubplots"`
	UpdatedEmotionalArcs  string `json:"updatedEmotionalArcs"`
	UpdatedCharacterMatrix string `json:"updatedCharacterMatrix"`
	ChapterSummary        string `json:"chapterSummary"`
}

// WriteChapterOutput 写手 Agent 输出
type WriteChapterOutput struct {
	ChapterNumber        int                   `json:"chapterNumber"`
	Title                string                `json:"title"`
	Content              string                `json:"content"`
	WordCount            int                   `json:"wordCount"`
	PreWriteCheck        string                `json:"preWriteCheck"`
	PostSettlement       string                `json:"postSettlement"`
	RuntimeStateDelta    *RuntimeStateDelta    `json:"runtimeStateDelta,omitempty"`
	UpdatedState         string                `json:"updatedState"`
	UpdatedLedger        string                `json:"updatedLedger"`
	UpdatedHooks         string                `json:"updatedHooks"`
	ChapterSummary       string                `json:"chapterSummary"`
	UpdatedChapterSummaries string              `json:"updatedChapterSummaries,omitempty"`
	UpdatedSubplots      string                `json:"updatedSubplots"`
	UpdatedEmotionalArcs string                `json:"updatedEmotionalArcs"`
	UpdatedCharacterMatrix string              `json:"updatedCharacterMatrix"`
	PostWriteErrors      []PostWriteViolation  `json:"postWriteErrors"`
	PostWriteWarnings    []PostWriteViolation  `json:"postWriteWarnings"`
	HookHealthIssues     []HookHealthIssue     `json:"hookHealthIssues,omitempty"`
	TokenUsage           *TokenUsage           `json:"tokenUsage,omitempty"`
}

// ReviseMode 修订模式
type ReviseMode string

const (
	ReviseModeAuto       ReviseMode = "auto"
	ReviseModePolish     ReviseMode = "polish"
	ReviseModeRewrite    ReviseMode = "rewrite"
	ReviseModeRework     ReviseMode = "rework"
	ReviseModeAntiDetect ReviseMode = "anti-detect"
	ReviseModeSpotFix    ReviseMode = "spot-fix"
)

// ReviseOutput 修订器输出
type ReviseOutput struct {
	RevisedContent string      `json:"revisedContent"`
	WordCount      int         `json:"wordCount"`
	FixedIssues    []string    `json:"fixedIssues"`
	UpdatedState   string      `json:"updatedState"`
	UpdatedLedger  string      `json:"updatedLedger"`
	UpdatedHooks   string      `json:"updatedHooks"`
	TokenUsage     *TokenUsage `json:"tokenUsage,omitempty"`
}

// ChapterPipelineResult 写章流水线结果
type ChapterPipelineResult struct {
	ChapterNumber    int          `json:"chapterNumber"`
	Title            string       `json:"title"`
	WordCount        int          `json:"wordCount"`
	AuditResult      AuditResult  `json:"auditResult"`
	Revised          bool         `json:"revised"`
	Status           ChapterStatus `json:"status"`
	LengthWarnings   []string     `json:"lengthWarnings,omitempty"`
	LengthTelemetry  *LengthTelemetry `json:"lengthTelemetry,omitempty"`
	TokenUsage       *TokenUsage  `json:"tokenUsage,omitempty"`
}

// LengthTelemetry 字数遥测
type LengthTelemetry struct {
	LengthSpec             LengthSpec `json:"lengthSpec"`
	WriterCount            int        `json:"writerCount"`
	PostWriterNormalizeCount int      `json:"postWriterNormalizeCount"`
	PostReviseCount        int        `json:"postReviseCount"`
	FinalCount             int        `json:"finalCount"`
	NormalizeApplied       bool       `json:"normalizeApplied"`
	LengthWarning          bool       `json:"lengthWarning"`
}
