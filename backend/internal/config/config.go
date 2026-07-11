package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 全局配置
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	LLM      LLMConfig      `mapstructure:"llm"`
	Writing  WritingConfig  `mapstructure:"writing"`
	Scheduler SchedulerConfig `mapstructure:"scheduler"`
	Notify   NotifyConfig   `mapstructure:"notify"`
}

// ServerConfig HTTP 服务器配置
type ServerConfig struct {
	Port         int    `mapstructure:"port"`
	FrontendDir  string `mapstructure:"frontend_dir"`
	AllowOrigins []string `mapstructure:"allow_origins"`
}

// DatabaseConfig 数据库配置（支持 SQLite/MySQL/PostgreSQL）
type DatabaseConfig struct {
	Driver string `mapstructure:"driver"` // sqlite / mysql / postgres
	DSN    string `mapstructure:"dsn"`
}

// LLMConfig LLM 提供商配置
type LLMConfig struct {
	DefaultProvider string                   `mapstructure:"default_provider"`
	DefaultModel    string                   `mapstructure:"default_model"`
	Providers       map[string]ProviderConfig `mapstructure:"providers"`
	// Agent 级别模型覆盖：agent_name -> {provider, model}
	AgentOverrides map[string]AgentLLMOverride `mapstructure:"agent_overrides"`
}

// ProviderConfig 单个 LLM 提供商配置
type ProviderConfig struct {
	Type    string `mapstructure:"type"`    // openai / anthropic / google / ollama
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

// AgentLLMOverride Agent 级别模型覆盖
type AgentLLMOverride struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
}

// WritingConfig 写作配置
type WritingConfig struct {
	ProjectRoot        string `mapstructure:"project_root"`
	ChapterReviewMode  string `mapstructure:"chapter_review_mode"` // auto / manual
	RevisionGate       string `mapstructure:"revision_gate"`       // strict / lenient / always
	MaxReviewIterations int   `mapstructure:"max_review_iterations"`
	FoundationReviewRetries int `mapstructure:"foundation_review_retries"`
}

// SchedulerConfig 调度器配置
type SchedulerConfig struct {
	WriteCron          string `mapstructure:"write_cron"`
	RadarCron          string `mapstructure:"radar_cron"`
	MaxConcurrentBooks int    `mapstructure:"max_concurrent_books"`
	ChaptersPerCycle   int    `mapstructure:"chapters_per_cycle"`
	MaxChaptersPerDay  int    `mapstructure:"max_chapters_per_day"`
	CooldownMs         int    `mapstructure:"cooldown_ms"`
}

// NotifyConfig 通知配置
type NotifyConfig struct {
	TelegramBotToken string `mapstructure:"telegram_bot_token"`
	TelegramChatID   string `mapstructure:"telegram_chat_id"`
	FeishuWebhook    string `mapstructure:"feishu_webhook"`
	WechatWebhook    string `mapstructure:"wechat_webhook"`
	GenericWebhook   string `mapstructure:"generic_webhook"`
}

// Load 加载配置
func Load(configPath string) (*Config, error) {
	v := viper.New()

	v.SetConfigFile(configPath)

	// 默认值
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.frontend_dir", "../frontend/dist")
	v.SetDefault("server.allow_origins", []string{"http://localhost:5173"})
	v.SetDefault("database.driver", "sqlite")
	v.SetDefault("database.dsn", "data/yellowbull.db")
	v.SetDefault("writing.chapter_review_mode", "auto")
	v.SetDefault("writing.revision_gate", "strict")
	v.SetDefault("writing.max_review_iterations", 1)
	v.SetDefault("writing.foundation_review_retries", 2)
	v.SetDefault("scheduler.write_cron", "0 */2 * * *")
	v.SetDefault("scheduler.radar_cron", "0 */6 * * *")
	v.SetDefault("scheduler.max_concurrent_books", 3)
	v.SetDefault("scheduler.chapters_per_cycle", 1)
	v.SetDefault("scheduler.max_chapters_per_day", 10)
	v.SetDefault("scheduler.cooldown_ms", 30000)

	// 尝试读取配置文件
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
		// 配置文件不存在时使用默认值 + 环境变量
	}

	// 环境变量覆盖
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// 如果没有配置项目根目录，使用默认值
	if cfg.Writing.ProjectRoot == "" {
		cfg.Writing.ProjectRoot = "data"
	}

	return cfg, nil
}

// LoadOrDefault 加载配置或使用默认配置
func LoadOrDefault(configPath string) *Config {
	cfg, err := Load(configPath)
	if err != nil {
		// 返回最小可用配置
		return &Config{
			Server: ServerConfig{
				Port:         8080,
				FrontendDir:  "../frontend/dist",
				AllowOrigins: []string{"http://localhost:5173"},
			},
			Database: DatabaseConfig{
				Driver: "sqlite",
				DSN:    "data/yellowbull.db",
			},
			Writing: WritingConfig{
				ProjectRoot:             "data",
				ChapterReviewMode:       "auto",
				RevisionGate:            "strict",
				MaxReviewIterations:     1,
				FoundationReviewRetries: 2,
			},
			Scheduler: SchedulerConfig{
				WriteCron:          "0 */2 * * *",
				RadarCron:          "0 */6 * * *",
				MaxConcurrentBooks: 3,
				ChaptersPerCycle:   1,
				MaxChaptersPerDay:  10,
				CooldownMs:         30000,
			},
		}
	}
	return cfg
}
