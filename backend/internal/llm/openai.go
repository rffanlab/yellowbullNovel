package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIClient OpenAI 兼容接口客户端
// 支持 OpenAI / DeepSeek / Moonshot / Zhipu / 硅基流动 / OpenRouter 等
type OpenAIClient struct {
	baseURL       string
	apiKey        string
	model         string
	contextWindow int
	httpClient    *http.Client
}

// NewOpenAIClient 创建 OpenAI 兼容客户端
func NewOpenAIClient(cfg ProviderConfig) (*OpenAIClient, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	// 确保以 /v1 结尾或类似格式
	if !strings.HasSuffix(baseURL, "/v1") && !strings.HasSuffix(baseURL, "/v1/") {
		if !strings.HasSuffix(baseURL, "/") {
			baseURL += "/"
		}
	}
	return &OpenAIClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		httpClient: &http.Client{Timeout: 300 * time.Second},
	}, nil
}

// SetContextWindow 设置上下文窗口大小
func (c *OpenAIClient) SetContextWindow(tokens int) {
	c.contextWindow = tokens
}

func (c *OpenAIClient) ModelName() string    { return c.model }
func (c *OpenAIClient) ContextWindow() int   { return c.contextWindow }

// openaiRequest OpenAI API 请求体
type openaiRequest struct {
	Model       string        `json:"model"`
	Messages    []openaiMsg   `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
}

type openaiMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openaiResponse OpenAI API 响应体
type openaiResponse struct {
	Choices []struct {
		Message      openaiMsg `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// openaiStreamChunk SSE 流式响应块
type openaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

func (c *OpenAIClient) ChatCompletion(ctx context.Context, messages []Message, opts *ChatOptions) (*ChatResponse, error) {
	opts = EnsureOptions(opts)
	model := opts.Model
	if model == "" {
		model = c.model
	}

	reqBody := openaiRequest{
		Model:    model,
		Messages: toOpenAIMessages(messages),
	}
	if opts.Temperature != nil {
		reqBody.Temperature = opts.Temperature
	}
	if opts.MaxTokens != nil {
		reqBody.MaxTokens = opts.MaxTokens
	}
	if len(opts.Stop) > 0 {
		reqBody.Stop = opts.Stop
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	if !strings.Contains(url, "/v1/chat/completions") {
		url = c.baseURL + "/v1/chat/completions"
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api error %d: %s", resp.StatusCode, string(respBody))
	}

	var result openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	return &ChatResponse{
		Content:      result.Choices[0].Message.Content,
		FinishReason: result.Choices[0].FinishReason,
		TokenUsage: &TokenUsage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		},
	}, nil
}

func (c *OpenAIClient) ChatCompletionStream(ctx context.Context, messages []Message, opts *ChatOptions, onProgress OnStreamProgress) (*ChatResponse, error) {
	opts = EnsureOptions(opts)
	model := opts.Model
	if model == "" {
		model = c.model
	}

	reqBody := openaiRequest{
		Model:    model,
		Messages: toOpenAIMessages(messages),
		Stream:   true,
	}
	if opts.Temperature != nil {
		reqBody.Temperature = opts.Temperature
	}
	if opts.MaxTokens != nil {
		reqBody.MaxTokens = opts.MaxTokens
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	if !strings.Contains(url, "/v1/chat/completions") {
		url = c.baseURL + "/v1/chat/completions"
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api error %d: %s", resp.StatusCode, string(respBody))
	}

	var fullContent strings.Builder
	finishReason := ""

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				fullContent.WriteString(choice.Delta.Content)
				if onProgress != nil {
					onProgress(choice.Delta.Content)
				}
			}
			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan stream: %w", err)
	}

	return &ChatResponse{
		Content:      fullContent.String(),
		FinishReason: finishReason,
		TokenUsage: &TokenUsage{
			PromptTokens:     EstimateTokens(messagesToString(messages)),
			CompletionTokens: EstimateTokens(fullContent.String()),
			TotalTokens:      EstimateTokens(messagesToString(messages)) + EstimateTokens(fullContent.String()),
		},
	}, nil
}

func toOpenAIMessages(messages []Message) []openaiMsg {
	result := make([]openaiMsg, len(messages))
	for i, msg := range messages {
		result[i] = openaiMsg{Role: string(msg.Role), Content: msg.Content}
	}
	return result
}

func messagesToString(messages []Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(string(msg.Role))
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}
