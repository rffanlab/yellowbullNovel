package models

import (
	"time"
)

// BookStatus 书籍状态
type BookStatus string

const (
	BookStatusActive   BookStatus = "active"
	BookStatusPaused   BookStatus = "paused"
	BookStatusComplete BookStatus = "complete"
	BookStatusDraft    BookStatus = "draft"
)

// ChapterReviewMode 章节审查模式
type ChapterReviewMode string

const (
	ReviewModeAuto   ChapterReviewMode = "auto"
	ReviewModeManual ChapterReviewMode = "manual"
)

// RevisionGate 修订门
type RevisionGate string

const (
	GateStrict  RevisionGate = "strict"
	GateLenient RevisionGate = "lenient"
	GateAlways  RevisionGate = "always"
)

// FanficMode 同人模式
type FanficMode string

const (
	FanficModeStrict  FanficMode = "strict"
	FanficModeOOC     FanficMode = "ooc"
	FanficModeRewrite FanficMode = "rewrite"
)

// BookConfig 书籍配置
type BookConfig struct {
	ID                string            `json:"id" gorm:"primaryKey"`
	Title             string            `json:"title" gorm:"not null"`
	Genre             string            `json:"genre" gorm:"not null;default:'other'"`
	Platform          string            `json:"platform" gorm:"default:'番茄小说'"`
	Language          string            `json:"language" gorm:"default:'zh'"`
	Status            BookStatus        `json:"status" gorm:"default:'draft'"`
	TargetChapters    int               `json:"targetChapters" gorm:"default:100"`
	ChapterWordCount  int               `json:"chapterWordCount" gorm:"default:3000"`
	FanficMode        *FanficMode       `json:"fanficMode,omitempty"`
	ChapterReviewMode ChapterReviewMode `json:"chapterReviewMode" gorm:"default:'auto'"`
	RevisionGate      RevisionGate      `json:"revisionGate" gorm:"default:'strict'"`
	EnableFullCastTracking bool         `json:"enableFullCastTracking" gorm:"default:false"`
	CreatedAt         time.Time         `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt         time.Time         `json:"updatedAt" gorm:"autoUpdateTime"`
}

func (BookConfig) TableName() string { return "books" }

// ChapterStatus 章节状态
type ChapterStatus string

const (
	ChapterStatusDraft          ChapterStatus = "draft"
	ChapterStatusReadyForReview ChapterStatus = "ready-for-review"
	ChapterStatusAccepted       ChapterStatus = "accepted"
	ChapterStatusRevised        ChapterStatus = "revised"
	ChapterStatusAuditFailed    ChapterStatus = "audit-failed"
	ChapterStatusStateDegraded  ChapterStatus = "state-degraded"
)

// ChapterMeta 章节元数据
type ChapterMeta struct {
	ID          uint           `json:"-" gorm:"primaryKey;autoIncrement"`
	BookID      string         `json:"bookId" gorm:"index;not null"`
	Number      int            `json:"number" gorm:"not null"`
	Title       string         `json:"title" gorm:"not null"`
	WordCount   int            `json:"wordCount" gorm:"default:0"`
	Status      ChapterStatus  `json:"status" gorm:"default:'draft'"`
	AuditScore  *int           `json:"auditScore,omitempty"`
	Revised     bool           `json:"revised" gorm:"default:false"`
	FilePath    string         `json:"filePath"`
	CreatedAt   time.Time      `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt   time.Time      `json:"updatedAt" gorm:"autoUpdateTime"`
}

func (ChapterMeta) TableName() string { return "chapters" }
