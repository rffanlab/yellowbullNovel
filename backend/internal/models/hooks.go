package models

// HookStatus 伏笔状态
type HookStatus string

const (
	HookStatusSeed     HookStatus = "seed"
	HookStatusActive   HookStatus = "active"
	HookStatusAdvanced HookStatus = "advanced"
	HookStatusResolved HookStatus = "resolved"
	HookStatusDeferred HookStatus = "deferred"
)

// HookRecord 伏笔记录（13列）
type HookRecord struct {
	HookID              string     `json:"hookId"`
	Type                string     `json:"type"`                // 伏笔类型
	Status              HookStatus `json:"status"`
	StartChapter        int        `json:"startChapter"`
	LastAdvancedChapter int        `json:"lastAdvancedChapter"`
	ExpectedPayoff      string     `json:"expectedPayoff"`      // 读者承诺
	PayoffTiming        string     `json:"payoffTiming"`        // 回收时机
	Notes               string     `json:"notes"`               // 备注
	DependsOn           string     `json:"dependsOn,omitempty"` // 依赖的伏笔ID
	PaysOffInArc        string     `json:"paysOffInArc,omitempty"` // 在哪个弧线回收
	CoreHook            bool       `json:"coreHook"`            // 核心伏笔
	HalfLife            int        `json:"halfLife"`            // 半衰期（章）
	Promoted            bool       `json:"promoted"`            // 是否已晋升
}

// StoredHook 存储在数据库中的伏笔
type StoredHook struct {
	ID                  uint       `json:"-" gorm:"primaryKey;autoIncrement"`
	BookID              string     `json:"bookId" gorm:"index;not null"`
	HookID              string     `json:"hookId" gorm:"not null"`
	Type                string     `json:"type"`
	Status              HookStatus `json:"status" gorm:"default:'seed'"`
	StartChapter        int        `json:"startChapter"`
	LastAdvancedChapter int        `json:"lastAdvancedChapter"`
	ExpectedPayoff      string     `json:"expectedPayoff"`
	PayoffTiming        string     `json:"payoffTiming"`
	Notes               string     `json:"notes"`
	DependsOn           string     `json:"dependsOn"`
	PaysOffInArc        string     `json:"paysOffInArc"`
	CoreHook            bool       `json:"coreHook" gorm:"default:false"`
	HalfLife            int        `json:"halfLife" gorm:"default:30"`
	Promoted            bool       `json:"promoted" gorm:"default:false"`
}

func (StoredHook) TableName() string { return "hooks" }

// HookHealthIssue 伏笔健康问题
type HookHealthIssue struct {
	Severity   string `json:"severity"` // critical / warning / info
	Category   string `json:"category"`
	Description string `json:"description"`
	Suggestion string `json:"suggestion"`
}

// ChapterSummary 章节摘要
type ChapterSummary struct {
	Chapter       int    `json:"chapter"`
	Title         string `json:"title"`
	Events        string `json:"events"`
	StateChanges  string `json:"stateChanges"`
	HookActivity  string `json:"hookActivity"`
	Mood          string `json:"mood"`
	ChapterType   string `json:"chapterType"`
}

// StoredSummary 存储在数据库中的章节摘要
type StoredSummary struct {
	ID            uint   `json:"-" gorm:"primaryKey;autoIncrement"`
	BookID        string `json:"bookId" gorm:"index;not null"`
	Chapter       int    `json:"chapter" gorm:"not null"`
	Title         string `json:"title"`
	Events        string `json:"events"`
	StateChanges  string `json:"stateChanges"`
	HookActivity  string `json:"hookActivity"`
	Mood          string `json:"mood"`
	ChapterType   string `json:"chapterType"`
}

func (StoredSummary) TableName() string { return "chapter_summaries" }

// Fact 事实记录
type Fact struct {
	ID        uint   `json:"-" gorm:"primaryKey;autoIncrement"`
	BookID    string `json:"bookId" gorm:"index;not null"`
	Predicate string `json:"predicate"` // 主语
	Object    string `json:"object"`    // 宾语
	Chapter   int    `json:"chapter"`   // 来源章节
}

func (Fact) TableName() string { return "facts" }
