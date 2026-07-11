package models

// GenreProfile 题材档案
type GenreProfile struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Language      string   `json:"language"` // zh / en
	NumericalSystem bool   `json:"numericalSystem"` // 是否有数值系统
	PowerScaling    bool   `json:"powerScaling"`    // 是否有战力等级
	EraResearch     bool   `json:"eraResearch"`     // 是否需要年代考据
	FatigueWords    []string `json:"fatigueWords"`   // 疲劳词列表
	Tags           []string `json:"tags,omitempty"`
}

// BookRules 书籍规则
type BookRules struct {
	Protagonist          string   `json:"protagonist,omitempty"`
	NarrativePerson      string   `json:"narrativePerson,omitempty"` // first / third / third-limited
	Prohibitions         []string `json:"prohibitions,omitempty"`
	EnableFullCastTracking bool   `json:"enableFullCastTracking,omitempty"`
	FatigueWordsOverride  []string `json:"fatigueWordsOverride,omitempty"`
	NumericalSystem       bool     `json:"numericalSystem,omitempty"`
}

// ArchitectOutput 架构师输出
type ArchitectOutput struct {
	StoryBible    string           `json:"storyBible"`    // 兼容字段
	VolumeOutline string           `json:"volumeOutline"` // 兼容字段
	BookRules     string           `json:"bookRules"`
	CurrentState  string           `json:"currentState"`
	PendingHooks  string           `json:"pendingHooks"`
	// Phase 5 新字段
	StoryFrame       string           `json:"storyFrame,omitempty"`
	VolumeMap        string           `json:"volumeMap,omitempty"`
	RhythmPrinciples string           `json:"rhythmPrinciples,omitempty"`
	Roles            []ArchitectRole  `json:"roles,omitempty"`
}

// ArchitectRole 架构师角色
type ArchitectRole struct {
	Tier    string `json:"tier"` // major / minor
	Name    string `json:"name"`
	Content string `json:"content"`
}

// ArchitectSection 架构师输出的 SECTION
type ArchitectSection struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// FoundationReviewResult 基础设定审查结果
type FoundationReviewResult struct {
	Passed         bool                    `json:"passed"`
	TotalScore     int                     `json:"totalScore"`
	Dimensions     []ReviewDimension       `json:"dimensions"`
	OverallFeedback string                 `json:"overallFeedback"`
	ReviewFeedback  string                 `json:"reviewFeedback"` // 注入重新生成的反馈
}

// ReviewDimension 审查维度
type ReviewDimension struct {
	Name   string `json:"name"`
	Score  int    `json:"score"`
	Note   string `json:"note"`
}

// PlanChapterOutput 规划器输出
type PlanChapterOutput struct {
	Intent         ChapterIntent `json:"intent"`
	Memo           ChapterMemo   `json:"memo"`
	IntentMarkdown string        `json:"intentMarkdown"`
	PlannerInputs  []string      `json:"plannerInputs"`
	RuntimePath    string        `json:"runtimePath"`
}

// ComposeChapterOutput 上下文组装器输出
type ComposeChapterOutput struct {
	ContextPackage ContextPackage `json:"contextPackage"`
	RuleStack      RuleStack      `json:"ruleStack"`
	Trace          ChapterTrace   `json:"trace"`
	ContextPath    string         `json:"contextPath"`
	RuleStackPath  string         `json:"ruleStackPath"`
	TracePath      string         `json:"tracePath"`
}

// WriteChapterInput 写手 Agent 输入
type WriteChapterInput struct {
	Book               BookConfig       `json:"book"`
	BookDir            string           `json:"bookDir"`
	ChapterNumber      int              `json:"chapterNumber"`
	ExternalContext    string           `json:"externalContext,omitempty"`
	ChapterIntent      string           `json:"chapterIntent,omitempty"`
	ChapterMemo        *ChapterMemo     `json:"chapterMemo,omitempty"`
	ChapterIntentData  *ChapterIntent   `json:"chapterIntentData,omitempty"`
	ContextPackage     *ContextPackage  `json:"contextPackage,omitempty"`
	RuleStack          *RuleStack       `json:"ruleStack,omitempty"`
	LengthSpec         *LengthSpec      `json:"lengthSpec,omitempty"`
	WordCountOverride  *int             `json:"wordCountOverride,omitempty"`
	TemperatureOverride *float64        `json:"temperatureOverride,omitempty"`
}

// ConsolidationResult 卷级合并结果
type ConsolidationResult struct {
	VolumeSummaries    string `json:"volumeSummaries"`
	ArchivedVolumes    int    `json:"archivedVolumes"`
	RetainedChapters   int    `json:"retainedChapters"`
	PromotedHookCount  int    `json:"promotedHookCount"`
}

// BookStatusInfo 书籍状态信息
type BookStatusInfo struct {
	BookID         string        `json:"bookId"`
	Title          string        `json:"title"`
	Genre          string        `json:"genre"`
	Platform       string        `json:"platform"`
	Status         string        `json:"status"`
	ChaptersWritten int          `json:"chaptersWritten"`
	TotalWords     int           `json:"totalWords"`
	NextChapter    int           `json:"nextChapter"`
	Chapters       []ChapterMeta `json:"chapters"`
}
