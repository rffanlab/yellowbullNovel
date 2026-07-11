package models

// ChapterIntent 确定性章节意图（planner 产出）
type ChapterIntent struct {
	Chapter       int      `json:"chapter"`
	Goal          string   `json:"goal"`
	OutlineNode   string   `json:"outlineNode,omitempty"`
	ArcContext    string   `json:"arcContext,omitempty"`
	MustKeep      []string `json:"mustKeep"`
	MustAvoid     []string `json:"mustAvoid"`
	StyleEmphasis []string `json:"styleEmphasis"`
}

// ChapterMemo LLM 生成的章节备忘（planner 产出，7+段 markdown）
type ChapterMemo struct {
	Goal            string   `json:"goal"`
	ThreadRefs      []string `json:"threadRefs"`
	Body            string   `json:"body"`            // 完整 markdown 正文
	IsGoldenOpening bool     `json:"isGoldenOpening"`
}

// ContextEntry 上下文包中的单条条目
type ContextEntry struct {
	Source  string `json:"source"`
	Reason  string `json:"reason"`
	Excerpt string `json:"excerpt"`
}

// ContextPackage 上下文包（composer 产出）
type ContextPackage struct {
	Chapter         int            `json:"chapter"`
	SelectedContext []ContextEntry `json:"selectedContext"`
}

// RuleStackEntry 规则栈条目
type RuleStackEntry struct {
	Level   string `json:"level"` // L1-L4
	Source  string `json:"source"`
	Rule    string `json:"rule"`
	Active  bool   `json:"active"`
}

// RuleStack 治理规则栈
type RuleStack struct {
	Chapter       int               `json:"chapter"`
	Entries       []RuleStackEntry  `json:"entries"`
	ActiveOverrides []OverrideEdge  `json:"activeOverrides,omitempty"`
}

// OverrideEdge 活动覆盖边
type OverrideEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Reason string `json:"reason"`
}

// ChapterTrace 章节追踪
type ChapterTrace struct {
	Chapter       int      `json:"chapter"`
	ComposerInputs []string `json:"composerInputs"`
	UsedSkills    []string `json:"usedSkills,omitempty"`
	PromptPacks   []string `json:"promptPacks,omitempty"`
	Notes         []string `json:"notes,omitempty"`
	Compression   *TraceCompression `json:"compression,omitempty"`
}

// TraceCompression 追踪压缩信息
type TraceCompression struct {
	CompiledSource   string   `json:"compiledSource"`
	ProtectedSources []string `json:"protectedSources"`
	CompressedSources []string `json:"compressedSources"`
	ProtectedTokens  int      `json:"protectedTokens"`
	CompressibleTokens int    `json:"compressibleTokens"`
	BudgetTokens      int     `json:"budgetTokens"`
}
