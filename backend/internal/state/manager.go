package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rffanlab/yellowbullNovel/backend/internal/config"
	"github.com/rffanlab/yellowbullNovel/backend/internal/models"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// StateManager 状态管理器
type StateManager struct {
	db          *gorm.DB
	projectRoot string
	logger      *zap.Logger
	bookLocks   sync.Map // bookID -> *sync.Mutex
}

// NewStateManager 创建状态管理器
func NewStateManager(cfg config.DatabaseConfig, projectRoot string, log *zap.Logger) (*StateManager, error) {
	db, err := initDB(cfg)
	if err != nil {
		return nil, fmt.Errorf("init db: %w", err)
	}

	if err := db.AutoMigrate(
		&models.BookConfig{},
		&models.ChapterMeta{},
		&models.StoredHook{},
		&models.StoredSummary{},
		&models.Fact{},
	); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}

	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		return nil, fmt.Errorf("create project root: %w", err)
	}

	return &StateManager{
		db:          db,
		projectRoot: projectRoot,
		logger:      log,
	}, nil
}

func initDB(cfg config.DatabaseConfig) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	}

	switch cfg.Driver {
	case "sqlite":
		dir := filepath.Dir(cfg.DSN)
		if dir != "" && dir != "." {
			os.MkdirAll(dir, 0755)
		}
		return gorm.Open(sqlite.Open(cfg.DSN), gormCfg)
	case "mysql":
		return gorm.Open(mysql.Open(cfg.DSN), gormCfg)
	case "postgres":
		return gorm.Open(postgres.Open(cfg.DSN), gormCfg)
	default:
		return gorm.Open(sqlite.Open(cfg.DSN), gormCfg)
	}
}

func (sm *StateManager) DB() *gorm.DB { return sm.db }

func (sm *StateManager) BooksDir() string {
	return filepath.Join(sm.projectRoot, "books")
}

func (sm *StateManager) BookDir(bookID string) string {
	return filepath.Join(sm.BooksDir(), bookID)
}

func (sm *StateManager) StoryDir(bookID string) string {
	return filepath.Join(sm.BookDir(bookID), "story")
}

func (sm *StateManager) ChaptersDir(bookID string) string {
	return filepath.Join(sm.BookDir(bookID), "chapters")
}

func (sm *StateManager) OutlineDir(bookID string) string {
	return filepath.Join(sm.StoryDir(bookID), "outline")
}

func (sm *StateManager) RolesDir(bookID string) string {
	return filepath.Join(sm.StoryDir(bookID), "roles")
}

func (sm *StateManager) RuntimeDir(bookID string) string {
	return filepath.Join(sm.StoryDir(bookID), "runtime")
}

// AcquireBookLock 获取书籍写锁
func (sm *StateManager) AcquireBookLock(bookID string) func() {
	val, _ := sm.bookLocks.LoadOrStore(bookID, &sync.Mutex{})
	mu := val.(*sync.Mutex)
	mu.Lock()
	return func() { mu.Unlock() }
}

// SaveBookConfig 保存书籍配置
func (sm *StateManager) SaveBookConfig(book *models.BookConfig) error {
	return sm.db.Save(book).Error
}

// LoadBookConfig 加载书籍配置
func (sm *StateManager) LoadBookConfig(bookID string) (*models.BookConfig, error) {
	var book models.BookConfig
	if err := sm.db.Where("id = ?", bookID).First(&book).Error; err != nil {
		return nil, fmt.Errorf("load book config: %w", err)
	}
	return &book, nil
}

// ListBooks 列出所有书籍
func (sm *StateManager) ListBooks() ([]models.BookConfig, error) {
	var books []models.BookConfig
	if err := sm.db.Order("created_at DESC").Find(&books).Error; err != nil {
		return nil, err
	}
	return books, nil
}

// DeleteBook 删除书籍
func (sm *StateManager) DeleteBook(bookID string) error {
	if err := sm.db.Where("book_id = ?", bookID).Delete(&models.ChapterMeta{}).Error; err != nil {
		return err
	}
	if err := sm.db.Where("book_id = ?", bookID).Delete(&models.StoredHook{}).Error; err != nil {
		return err
	}
	if err := sm.db.Where("book_id = ?", bookID).Delete(&models.StoredSummary{}).Error; err != nil {
		return err
	}
	if err := sm.db.Where("book_id = ?", bookID).Delete(&models.Fact{}).Error; err != nil {
		return err
	}
	if err := sm.db.Where("id = ?", bookID).Delete(&models.BookConfig{}).Error; err != nil {
		return err
	}
	return os.RemoveAll(sm.BookDir(bookID))
}

// SaveChapterMeta 保存章节元数据
func (sm *StateManager) SaveChapterMeta(meta *models.ChapterMeta) error {
	return sm.db.Save(meta).Error
}

// LoadChapterIndex 加载章节索引
func (sm *StateManager) LoadChapterIndex(bookID string) ([]models.ChapterMeta, error) {
	var chapters []models.ChapterMeta
	if err := sm.db.Where("book_id = ?", bookID).Order("number ASC").Find(&chapters).Error; err != nil {
		return nil, err
	}
	return chapters, nil
}

// GetNextChapterNumber 获取下一章编号
func (sm *StateManager) GetNextChapterNumber(bookID string) (int, error) {
	var maxNum int
	if err := sm.db.Model(&models.ChapterMeta{}).
		Where("book_id = ?", bookID).
		Select("COALESCE(MAX(number), 0)").
		Scan(&maxNum).Error; err != nil {
		return 0, err
	}
	return maxNum + 1, nil
}

// EnsureBookDirs 确保书籍目录结构存在
func (sm *StateManager) EnsureBookDirs(bookID string) error {
	dirs := []string{
		sm.BookDir(bookID),
		sm.ChaptersDir(bookID),
		sm.StoryDir(bookID),
		sm.OutlineDir(bookID),
		filepath.Join(sm.RolesDir(bookID), "主要角色"),
		filepath.Join(sm.RolesDir(bookID), "次要角色"),
		sm.RuntimeDir(bookID),
		filepath.Join(sm.StoryDir(bookID), "snapshots"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}

// EnsureControlDocuments 确保控制文档存在
func (sm *StateManager) EnsureControlDocuments(bookID string, language, authorIntent string) error {
	storyDir := sm.StoryDir(bookID)

	aiPath := filepath.Join(storyDir, "author_intent.md")
	if _, err := os.Stat(aiPath); os.IsNotExist(err) {
		content := authorIntent
		if content == "" {
			if language == "en" {
				content = "# Author Intent\n\n(Describe your long-term vision for this book)\n"
			} else {
				content = "# 作者意图\n\n（描述你对这本书的长期愿景）\n"
			}
		}
		if err := os.WriteFile(aiPath, []byte(content), 0644); err != nil {
			return err
		}
	}

	cfPath := filepath.Join(storyDir, "current_focus.md")
	if _, err := os.Stat(cfPath); os.IsNotExist(err) {
		var content string
		if language == "en" {
			content = "# Current Focus\n\n## Active Focus\n- (What should happen in the near-term chapters)\n\n## Must Avoid\n- (What to avoid)\n"
		} else {
			content = "# 当前聚焦\n\n## 近期聚焦\n-（近几章需要发生什么）\n\n## 避雷\n-（需要避免什么）\n"
		}
		if err := os.WriteFile(cfPath, []byte(content), 0644); err != nil {
			return err
		}
	}

	return nil
}

// ReadFileOrDefault 读取文件，不存在返回默认值
func ReadFileOrDefault(path string, defaultVal string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return defaultVal
	}
	return string(content)
}

// WriteFile 写入文件
func WriteFile(path string, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// ChapterFilePath 返回章节文件路径
func (sm *StateManager) ChapterFilePath(bookID string, chapterNum int) string {
	return filepath.Join(sm.ChaptersDir(bookID), fmt.Sprintf("%04d.md", chapterNum))
}

// SnapshotState 保存状态快照
func (sm *StateManager) SnapshotState(bookID string, chapterNum int) error {
	snapDir := filepath.Join(sm.StoryDir(bookID), "snapshots")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return err
	}
	timestamp := time.Now().Format("20060102-150405")
	snapFile := filepath.Join(snapDir, fmt.Sprintf("ch%04d-%s.json", chapterNum, timestamp))

	storyDir := sm.StoryDir(bookID)
	files := []string{"current_state.md", "pending_hooks.md", "chapter_summaries.md"}
	snapshot := map[string]string{}
	for _, f := range files {
		snapshot[f] = ReadFileOrDefault(filepath.Join(storyDir, f), "")
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return os.WriteFile(snapFile, data, 0644)
}

// SaveHooks 保存伏笔到数据库
func (sm *StateManager) SaveHooks(bookID string, hooks []models.HookRecord) error {
	for _, h := range hooks {
		stored := models.StoredHook{
			BookID:              bookID,
			HookID:              h.HookID,
			Type:                h.Type,
			Status:              h.Status,
			StartChapter:        h.StartChapter,
			LastAdvancedChapter: h.LastAdvancedChapter,
			ExpectedPayoff:      h.ExpectedPayoff,
			PayoffTiming:        h.PayoffTiming,
			Notes:               h.Notes,
			DependsOn:           h.DependsOn,
			PaysOffInArc:        h.PaysOffInArc,
			CoreHook:            h.CoreHook,
			HalfLife:            h.HalfLife,
			Promoted:            h.Promoted,
		}
		if err := sm.db.Save(&stored).Error; err != nil {
			return err
		}
	}
	return nil
}

// LoadHooks 从数据库加载伏笔
func (sm *StateManager) LoadHooks(bookID string) ([]models.StoredHook, error) {
	var hooks []models.StoredHook
	if err := sm.db.Where("book_id = ?", bookID).Find(&hooks).Error; err != nil {
		return nil, err
	}
	return hooks, nil
}

// SaveSummary 保存章节摘要
func (sm *StateManager) SaveSummary(bookID string, summary models.ChapterSummary) error {
	stored := models.StoredSummary{
		BookID:       bookID,
		Chapter:      summary.Chapter,
		Title:        summary.Title,
		Events:       summary.Events,
		StateChanges: summary.StateChanges,
		HookActivity: summary.HookActivity,
		Mood:         summary.Mood,
		ChapterType:  summary.ChapterType,
	}
	return sm.db.Save(&stored).Error
}

// LoadSummaries 加载章节摘要
func (sm *StateManager) LoadSummaries(bookID string) ([]models.StoredSummary, error) {
	var summaries []models.StoredSummary
	if err := sm.db.Where("book_id = ?", bookID).Order("chapter ASC").Find(&summaries).Error; err != nil {
		return nil, err
	}
	return summaries, nil
}

// GetBookStatusInfo 获取书籍状态信息
func (sm *StateManager) GetBookStatusInfo(bookID string) (*models.BookStatusInfo, error) {
	book, err := sm.LoadBookConfig(bookID)
	if err != nil {
		return nil, err
	}
	chapters, err := sm.LoadChapterIndex(bookID)
	if err != nil {
		return nil, err
	}
	totalWords := 0
	for _, ch := range chapters {
		totalWords += ch.WordCount
	}
	return &models.BookStatusInfo{
		BookID:          book.ID,
		Title:           book.Title,
		Genre:           book.Genre,
		Platform:        book.Platform,
		Status:          string(book.Status),
		ChaptersWritten: len(chapters),
		TotalWords:      totalWords,
		NextChapter:     len(chapters) + 1,
		Chapters:        chapters,
	}, nil
}
