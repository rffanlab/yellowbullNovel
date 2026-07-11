package llm

import (
	"context"
	"fmt"
)

// MessageRole 消息角色
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
)

// Message 聊天消息
type Message struct {
	Role    MessageRole `json:"role"`
	Content string      `json:"content"`
}

// ChatOptions 聊天选项
type ChatOptions struct {
	Temperature  *float64 `json:"temperature,omitempty"`
	MaxTokens    *int     `json:"maxTokens,omitempty"`
	Model        string   `json:"model,omitempty"`
	Stop         []string `json:"stop,omitempty"`
}

// ChatResponse 聊天响应
type ChatResponse struct {
	Content     string      `json:"content"`
	TokenUsage  *TokenUsage `json:"tokenUsage,omitempty"`
	FinishReason string     `json:"finishReason,omitempty"`
}

// TokenUsage Token 使用统计
type TokenUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// OnStreamProgress 流式进度回调
type OnStreamProgress func(delta string)

// LLMClient LLM 客户端接口
type LLMClient interface {
	// ChatCompletion 同步聊天完成
	ChatCompletion(ctx context.Context, messages []Message, opts *ChatOptions) (*ChatResponse, error)

	// ChatCompletionStream 流式聊天完成
	ChatCompletionStream(ctx context.Context, messages []Message, opts *ChatOptions, onProgress OnStreamProgress) (*ChatResponse, error)

	// ModelName 返回当前模型名
	ModelName() string

	// ContextWindow 返回上下文窗口大小（tokens）
	ContextWindow() int
}

// ProviderType 提供商类型
type ProviderType string

const (
	ProviderOpenAI    ProviderType = "openai"
	ProviderAnthropic ProviderType = "anthropic"
	ProviderGoogle    ProviderType = "google"
	ProviderOllama    ProviderType = "ollama"
)

// ProviderConfig 提供商配置
type ProviderConfig struct {
	Type    ProviderType `json:"type"`
	BaseURL string       `json:"base_url"`
	APIKey  string       `json:"api_key"`
	Model   string       `json:"model"`
}

// AgentContext Agent 上下文（传递给每个 Agent）
type AgentContext struct {
	Client      LLMClient
	ProjectRoot string
	Logger      Logger
}

// Logger 日志接口
type Logger interface {
	Info(msg string, fields ...any)
	Warn(msg string, fields ...any)
	Error(msg string, fields ...any)
	Debug(msg string, fields ...any)
}

// noopLogger 空日志
type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}
func (noopLogger) Debug(string, ...any) {}

// NoopLogger 返回空日志
func NoopLogger() Logger { return noopLogger{} }

// NewLLMClient 根据 ProviderConfig 创建 LLM 客户端
func NewLLMClient(cfg ProviderConfig) (LLMClient, error) {
	switch cfg.Type {
	case ProviderOpenAI:
		return NewOpenAIClient(cfg)
	case ProviderAnthropic:
		return NewAnthropicClient(cfg)
	case ProviderOllama:
		return NewOllamaClient(cfg)
	default:
		// 默认用 OpenAI 兼容接口
		return NewOpenAIClient(cfg)
	}
}

// EstimateTokens 粗略估算 token 数（中文约1.5字/token，英文约4字符/token）
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	// 简单估算：字符数 / 3（混合中英文的粗略平均值）
	return len(text) / 3
}

// MergeMessages 合并消息（将连续的相同角色消息合并）
func MergeMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	merged := []Message{messages[0]}
	for i := 1; i < len(messages); i++ {
		last := &merged[len(merged)-1]
		if messages[i].Role == last.Role {
			last.Content += "\n\n" + messages[i].Content
		} else {
			merged = append(merged, messages[i])
		}
	}
	return merged
}

// EnsureOptions 确保 opts 不为 nil
func EnsureOptions(opts *ChatOptions) *ChatOptions {
	if opts == nil {
		return &ChatOptions{}
	}
	return opts
}

// DefaultTemperature 默认温度
func DefaultTemperature(role string) float64 {
	switch role {
	case "architect", "writer":
		return 0.8
	case "planner":
		return 0.7
	case "composer", "reviser":
		return 0.2
	case "auditor", "reviewer", "validator":
		return 0.0
	default:
		return 0.7
	}
}

// ValidateConfig 验证配置
func ValidateConfig(cfg ProviderConfig) error {
	if cfg.Type == "" {
		return fmt.Errorf("provider type is required")
	}
	if cfg.APIKey == "" && cfg.Type != ProviderOllama {
		return fmt.Errorf("api_key is required for %s provider", cfg.Type)
	}
	if cfg.Model == "" {
		return fmt.Errorf("model is required")
	}
	return nil
}
