package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// AnthropicClient Anthropic Claude 客户端
type AnthropicClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewAnthropicClient 创建 Anthropic 客户端
func NewAnthropicClient(cfg ProviderConfig) (*AnthropicClient, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicClient{
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		httpClient: &http.Client{Timeout: 300 * time.Second},
	}, nil
}

func (c *AnthropicClient) ModelName() string  { return c.model }
func (c *AnthropicClient) ContextWindow() int { return 200000 }

// Anthropic 使用不同的 API 格式，这里用 OpenAI 兼容代理简化实现
// 实际部署时可通过 OpenRouter 或 Anthropic 的 OpenAI 兼容端点使用
func (c *AnthropicClient) ChatCompletion(ctx context.Context, messages []Message, opts *ChatOptions) (*ChatResponse, error) {
	// 使用 OpenAI 兼容接口（Anthropic 已支持）
	oc := &OpenAIClient{
		baseURL:    c.baseURL,
		apiKey:     c.apiKey,
		model:      c.model,
		httpClient: c.httpClient,
	}
	return oc.ChatCompletion(ctx, messages, opts)
}

func (c *AnthropicClient) ChatCompletionStream(ctx context.Context, messages []Message, opts *ChatOptions, onProgress OnStreamProgress) (*ChatResponse, error) {
	oc := &OpenAIClient{
		baseURL:    c.baseURL,
		apiKey:     c.apiKey,
		model:      c.model,
		httpClient: c.httpClient,
	}
	return oc.ChatCompletionStream(ctx, messages, opts, onProgress)
}

// OllamaClient 本地 Ollama 客户端
type OllamaClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewOllamaClient 创建 Ollama 客户端
func NewOllamaClient(cfg ProviderConfig) (*OllamaClient, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaClient{
		baseURL:    baseURL,
		model:      cfg.Model,
		httpClient: &http.Client{Timeout: 600 * time.Second},
	}, nil
}

func (c *OllamaClient) ModelName() string  { return c.model }
func (c *OllamaClient) ContextWindow() int { return 8192 }

// Ollama 支持 OpenAI 兼容接口 /v1/chat/completions
func (c *OllamaClient) ChatCompletion(ctx context.Context, messages []Message, opts *ChatOptions) (*ChatResponse, error) {
	oc := &OpenAIClient{
		baseURL:    c.baseURL,
		apiKey:     "ollama", // Ollama 不需要真实 key，但 OpenAI 格式需要
		model:      c.model,
		httpClient: c.httpClient,
	}
	resp, err := oc.ChatCompletion(ctx, messages, opts)
	if err != nil {
		return nil, fmt.Errorf("ollama chat: %w", err)
	}
	return resp, nil
}

func (c *OllamaClient) ChatCompletionStream(ctx context.Context, messages []Message, opts *ChatOptions, onProgress OnStreamProgress) (*ChatResponse, error) {
	oc := &OpenAIClient{
		baseURL:    c.baseURL,
		apiKey:     "ollama",
		model:      c.model,
		httpClient: c.httpClient,
	}
	return oc.ChatCompletionStream(ctx, messages, opts, onProgress)
}
