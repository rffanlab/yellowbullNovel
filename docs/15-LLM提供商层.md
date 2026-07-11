# 15 - LLM 提供商层

> 对应 inkos 的 `LLMClient` / `createLLMClient` / `chatCompletion` / 密钥管理。
> 参考源码：`inkos/packages/core/src/llm/provider.ts`、`llm/secrets.ts`、
> `llm/providers/`（types.ts / index.ts / lookup.ts）、`llm/service-presets.ts`、
> `llm/think-tag-stripper.ts`。

---

## 目录

- [一、流程概述](#一流程概述)
- [二、LLMClient 接口设计](#二llmclient-接口设计)
- [三、Provider 接口和注册机制](#三provider-接口和注册机制)
  - [3.1 API 协议](#31-api-协议)
  - [3.2 Endpoint 分组](#32-endpoint-分组)
  - [3.3 InkosEndpoint 定义](#33-inkosendpoint-定义)
  - [3.4 InkosModel 模型卡](#34-inkosmodel-模型卡)
  - [3.5 ProviderCompat 兼容性标记](#35-providercompat-兼容性标记)
  - [3.6 支持的提供商清单](#36-支持的提供商清单)
- [四、Message 结构体](#四message-结构体)
- [五、ChatOptions](#五chatoptions)
- [六、ChatResponse](#六chatresponse)
- [七、密钥管理](#七密钥管理)
  - [7.1 密钥查找优先级](#71-密钥查找优先级)
  - [7.2 secrets.json 格式](#72-secretsjson-格式)
  - [7.3 遗留服务 ID 迁移](#73-遗留服务-id-迁移)
  - [7.4 环境变量命名规则](#74-环境变量命名规则)
- [八、流式输出（SSE）](#八流式输出sse)
- [九、Agent 级别模型覆盖](#九agent-级别模型覆盖)
- [十、错误处理与重试](#十错误处理与重试)
- [十一、运行时配置切换](#十一运行时配置切换)
- [十二、完整 Go 接口定义](#十二完整-go-接口定义)

---

## 一、流程概述

LLM 提供商层是所有 Agent 调用 LLM 的统一入口。它封装了不同提供商（OpenAI 兼容 / Anthropic / Google Gemini / Ollama）的 API 差异，提供统一的 `ChatCompletion` 和 `ChatCompletionStream` 接口，支持流式 SSE 输出、密钥管理、Agent 级模型覆盖和运行时配置切换。

```
Agent (writer/architect/validator/...)
  │
  ├─ BaseAgent.chat(messages, options)
  │   │
  │   ├─ 查找 Agent 级模型覆盖
  │   │
  │   └─ LLMClient.ChatCompletion(messages, options)
  │       │
  │       ├─ assertWithinContextWindow（上下文窗口检查）
  │       │
  │       ├─ clampTemperatureForModel（温度夹制）
  │       │
  │       ├─ 流式 / 非流式
  │       │   ├─ 流式: chatCompletionViaStream（SSE）
  │       │   └─ 非流式: chatCompletionViaNonStream
  │       │
  │       ├─ stripLeadingThinkBlock（清除 think 标签）
  │       │
  │       └─ withTransientLLMRetry（瞬时错误重试）
  │
  └─ 返回 LLMResponse {content, usage}
```

---

## 二、LLMClient 接口设计

```go
// LLMClient LLM 客户端接口
type LLMClient interface {
    // ChatCompletion 非流式聊天补全
    ChatCompletion(
        ctx context.Context,
        messages []Message,
        options *ChatOptions,
    ) (*ChatResponse, error)

    // ChatCompletionStream 流式聊天补全（SSE）
    ChatCompletionStream(
        ctx context.Context,
        messages []Message,
        options *ChatOptions,
        onProgress OnStreamProgress,
    ) (*ChatResponse, error)

    // Provider 返回提供商类型
    Provider() string

    // Service 返回服务名
    Service() string

    // Defaults 返回默认配置
    Defaults() ClientDefaults
}
```

### ClientDefaults 默认配置

```go
type ClientDefaults struct {
    Temperature   float64                // 默认温度
    MaxTokens     int                    // 默认最大输出 tokens
    ThinkingBudget int                   // 思考预算（0=不启用）
    Extra         map[string]interface{} // 额外参数
}
```

### createLLMClient 工厂

```go
// CreateLLMClient 根据 LLMConfig 创建 LLM 客户端
func CreateLLMClient(config LLMConfig) LLMClient
```

---

## 三、Provider 接口和注册机制

### 3.1 API 协议

```go
type ApiProtocol string

const (
    ApiOpenAICompletions  ApiProtocol = "openai-completions"   // OpenAI Chat Completions
    ApiOpenAIResponses    ApiProtocol = "openai-responses"     // OpenAI Responses API
    ApiAnthropicMessages  ApiProtocol = "anthropic-messages"   // Anthropic Messages API
    ApiGoogleGenerativeAI ApiProtocol = "google-generative-ai" // Google Gemini
)
```

### 3.2 Endpoint 分组

```go
type EndpointGroup string

const (
    GroupOverseas    EndpointGroup = "overseas"    // 海外服务
    GroupChina       EndpointGroup = "china"       // 国内服务
    GroupAggregator  EndpointGroup = "aggregator"  // 聚合器
    GroupLocal       EndpointGroup = "local"       // 本地服务
    GroupCodingPlan  EndpointGroup = "codingPlan"  // CodingPlan
)
```

### 3.3 InkosEndpoint 定义

```go
type InkosEndpoint struct {
    ID               string                   // 服务 ID（如 "deepseek"、"moonshot"）
    Label            string                   // 显示名
    Group            EndpointGroup            // 分组
    API              ApiProtocol              // API 协议
    BaseURL          string                   // API 基础地址
    ModelsBaseURL    string                   // /models 接口地址（与主 BaseURL 不同时）
    CheckModel       string                   // 连通性检查用的模型 ID
    TemperatureRange [2]float64              // 温度范围
    DefaultTemp      float64                  // 默认温度
    WritingTemp      float64                  // 写作温度
    TemperatureHint  string                   // 温度提示
    Compat           *ProviderCompat          // 兼容性标记
    TransportDefaults *ProviderTransportDefaults // 传输默认值
    Models           []InkosModel             // 支持的模型列表
}
```

### 3.4 InkosModel 模型卡

```go
type InkosModel struct {
    ID                  string            // API 请求用的 model id
    MaxOutput           int               // 模型输出上限 tokens
    ContextWindowTokens int               // 上下文窗口总 tokens
    Enabled             *bool             // 默认 true
    DeploymentName      string            // CodingPlan 专用部署名
    ReleasedAt          string            // 发布日期
    Temperature         *float64          // API 硬要求的 temperature（如 Moonshot kimi-k2.5 强制 1）
    Status              string            // active / deprecated / disabled / nonText
    Replacement         string            // 替代模型
    Capabilities        *ModelCapabilities
}

type ModelCapabilities struct {
    Text        bool
    ImageInput  bool
    ImageOutput bool
    Tools       bool
    Reasoning   bool
}
```

### 3.5 ProviderCompat 兼容性标记

```go
type ProviderCompat struct {
    SupportsStore                   *bool // OpenAI Responses store 参数是否被接受
    SupportsSystemRole              *bool // 是否支持 system role
    SupportsDeveloperRole           *bool // 是否支持 developer role
    RequiresAssistantAfterToolResult *bool // 是否需要 toolResult 后加 assistant 桥接
}
```

### 3.6 支持的提供商清单

| ID | Label | Group | API | BaseURL |
|----|-------|-------|-----|---------|
| `deepseek` | DeepSeek | china | openai-completions | `https://api.deepseek.com/v1` |
| `moonshot` | Moonshot (Kimi) | china | openai-completions | `https://api.moonshot.cn/v1` |
| `zhipu` | Zhipu (Z.AI) | china | openai-completions | `https://open.bigmodel.cn/api/paas/v4` |
| `dashscope` | 百炼 (DashScope) | china | openai-completions | `https://dashscope.aliyuncs.com/compatible-mode/v1` |
| `siliconcloud` | 硅基流动 | aggregator | openai-completions | `https://api.siliconflow.cn/v1` |
| `openrouter` | OpenRouter | aggregator | openai-completions | `https://openrouter.ai/api/v1` |
| `openai` | OpenAI | overseas | openai-completions | `https://api.openai.com/v1` |
| `anthropic` | Anthropic (Claude) | overseas | anthropic-messages | `https://api.anthropic.com/v1` |
| `google` | Google Gemini | overseas | google-generative-ai | `https://generativelanguage.googleapis.com/v1beta` |
| `ollama` | Ollama (本地) | local | openai-completions | `http://localhost:11434/v1` |
| `custom` | 自定义 | - | openai-completions | 用户配置 |

### 3.7 Provider 注册机制

```go
// ProviderRegistry 提供商注册表
type ProviderRegistry struct {
    endpoints map[string]*InkosEndpoint // 按 ID 索引
}

var defaultRegistry = &ProviderRegistry{
    endpoints: make(map[string]*InkosEndpoint),
}

// RegisterEndpoint 注册一个提供商端点
func RegisterEndpoint(endpoint *InkosEndpoint)

// GetEndpoint 按 ID 获取端点
func GetEndpoint(id string) *InkosEndpoint

// LookupModel 在指定服务中查找模型卡
func LookupModel(service, modelID string) *InkosModel

// AllEndpoints 返回所有已注册端点
func AllEndpoints() []*InkosEndpoint
```

---

## 四、Message 结构体

```go
// Message LLM 消息
type Message struct {
    Role    string `json:"role"`    // "system" / "user" / "assistant"
    Content string `json:"content"`
}
```

### 消息转换

不同 API 协议对消息的处理方式不同：

| API 协议 | system 消息处理 |
|----------|----------------|
| openai-completions | system 作为 messages 数组的第一条 |
| openai-responses | system 转为 developer role |
| anthropic-messages | system 作为独立字段 |
| google-generative-ai | system 拼接到第一条 user 消息前 |

```go
// ToPiContext 将 Message 数组转换为 pi-ai Context
func ToPiContext(messages []Message) *PiContext {
    // 提取所有 system 消息合并为 systemPrompt
    systemParts := []string{}
    piMessages := []PiMessage{}
    for _, msg := range messages {
        if msg.Role == "system" {
            systemParts = append(systemParts, msg.Content)
        } else if msg.Role == "user" {
            piMessages = append(piMessages, PiMessage{
                Role:    "user",
                Content: msg.Content,
            })
        } else if msg.Role == "assistant" {
            piMessages = append(piMessages, PiMessage{
                Role:    "assistant",
                Content: []PiContentBlock{{Type: "text", Text: msg.Content}},
            })
        }
    }
    systemPrompt := strings.Join(systemParts, "\n\n")
    return &PiContext{SystemPrompt: systemPrompt, Messages: piMessages}
}
```

---

## 五、ChatOptions

```go
// ChatOptions 聊天选项
type ChatOptions struct {
    Temperature     float64        // 温度（0-2）
    MaxTokens       int            // 最大输出 tokens
    Model           string         // 模型 ID（支持 Agent 级覆盖）
    Stop            []string       // 停止序列
    WebSearch       bool           // 是否启用网页搜索
    OnStreamProgress OnStreamProgress // 流式进度回调
    OnTextDelta     func(text string) // 文本增量回调
    Signal          context.Context // 取消信号
    Retry           *bool          // 是否启用瞬时错误重试（默认 true）
}
```

### 温度夹制（Temperature Clamping）

部分 thinking 模型（如 Moonshot kimi-k2.5/k2.6）的 API 硬要求 `temperature === 1`，其他值会被直接 400 拒绝。Provider 层统一夹制：

```go
func clampTemperatureForModel(service, model string, requested float64) float64 {
    card := LookupModel(service, model)
    if card == nil || card.Temperature == nil {
        return requested
    }
    locked := *card.Temperature
    if requested == locked {
        return locked
    }
    // 首次警告
    warnOnce(model, locked, requested)
    return locked
}
```

---

## 六、ChatResponse

```go
// ChatResponse 聊天响应
type ChatResponse struct {
    Content      string      // 响应文本内容
    Usage        TokenUsage  // Token 用量
    FinishReason string      // 停止原因：stop / length / tool_calls / content_filter
}

// TokenUsage Token 用量
type TokenUsage struct {
    PromptTokens     int `json:"promptTokens"`
    CompletionTokens int `json:"completionTokens"`
    TotalTokens      int `json:"totalTokens"`
}
```

### Think Tag 清理

部分 thinking 模型在响应开头输出 `<think>...</think>` 标签。Provider 层在返回前清理：

```go
// StripLeadingThinkBlock 清除响应开头的 think 标签块
func StripLeadingThinkBlock(content string) string
```

---

## 七、密钥管理

### 7.1 密钥查找优先级

```
1. secrets.json（项目级 .inkos/secrets.json）
2. 环境变量（{SERVICE}_API_KEY）
```

```go
// GetServiceApiKey 获取服务 API Key
func GetServiceApiKey(projectRoot, service string) (string, error) {
    // 1. 尝试 secrets.json
    secrets, _ := LoadSecrets(projectRoot)
    if entry, ok := secrets.Services[service]; ok && entry.APIKey != "" {
        return entry.APIKey, nil
    }

    // 2. 尝试环境变量
    envKey := strings.ToUpper(regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(service, "_")) + "_API_KEY"
    if val := os.Getenv(envKey); val != "" {
        return val, nil
    }

    return "", fmt.Errorf("API key not found for service %s", service)
}
```

### 7.2 secrets.json 格式

```json
{
  "services": {
    "deepseek": { "apiKey": "sk-xxxxxxxx" },
    "moonshot": { "apiKey": "sk-xxxxxxxx" },
    "openai": { "apiKey": "sk-xxxxxxxx" }
  }
}
```

**存储位置**：`{projectRoot}/.inkos/secrets.json`

### 7.3 遗留服务 ID 迁移

```go
var legacyServiceIDRemap = map[string]string{
    "siliconflow": "siliconcloud",
}

// migrateLegacyServiceIds 迁移遗留服务 ID
func migrateLegacyServiceIds(secrets *SecretsFile) bool {
    changed := false
    for oldID, newID := range legacyServiceIDRemap {
        if _, hasOld := secrets.Services[oldID]; hasOld {
            if _, hasNew := secrets.Services[newID]; !hasNew {
                secrets.Services[newID] = secrets.Services[oldID]
                delete(secrets.Services, oldID)
                changed = true
            }
        }
    }
    return changed
}
```

### 7.4 环境变量命名规则

```
{SERVICE_ID 大写，非字母数字替换为下划线}_API_KEY
```

| Service ID | 环境变量 |
|------------|----------|
| `deepseek` | `DEEPSEEK_API_KEY` |
| `moonshot` | `MOONSHOT_API_KEY` |
| `zhipu` | `ZHIPU_API_KEY` |
| `openai` | `OPENAI_API_KEY` |
| `anthropic` | `ANTHROPIC_API_KEY` |
| `siliconcloud` | `SILICONCLOUD_API_KEY` |

---

## 八、流式输出（SSE）

### StreamProgress 回调

```go
// StreamProgress 流式进度
type StreamProgress struct {
    ElapsedMs    int64  // 已耗时毫秒
    TotalChars   int    // 总字符数
    ChineseChars int    // 中文字符数
    Status       string // "streaming" / "done"
}

// OnStreamProgress 流式进度回调
type OnStreamProgress func(progress StreamProgress)
```

### StreamMonitor 流式监控器

```go
// StreamMonitor 流式监控器
type StreamMonitor struct {
    totalChars   int
    chineseChars int
    startTime    time.Time
    timer        *time.Timer
    onProgress   OnStreamProgress
}

// NewStreamMonitor 创建流式监控器
func NewStreamMonitor(onProgress OnStreamProgress, intervalMs int) *StreamMonitor

// OnChunk 处理文本增量
func (m *StreamMonitor) OnChunk(text string)

// Stop 停止监控
func (m *StreamMonitor) Stop()
```

### 流式响应处理

```go
func chatCompletionViaStream(
    client LLMClient,
    model string,
    messages []Message,
    options *ChatOptions,
    onProgress OnStreamProgress,
) (*ChatResponse, error) {
    monitor := NewStreamMonitor(onProgress, 30000) // 30秒间隔
    defer monitor.Stop()

    // 调用 SSE 端点
    reader, err := callSSEEndpoint(client, model, messages, options)
    if err != nil {
        return nil, err
    }
    defer reader.Close()

    var contentBuilder strings.Builder
    var usage TokenUsage
    sawTerminal := false

    scanner := newSSEScanner(reader)
    for scanner.Scan() {
        chunk := scanner.Text()
        if chunk == "" {
            continue
        }

        // 解析 SSE 数据
        data := parseSSEChunk(chunk)
        if data == nil {
            continue
        }

        // 提取文本增量
        if delta := extractTextDelta(data); delta != "" {
            contentBuilder.WriteString(delta)
            monitor.OnChunk(delta)
            if options.OnTextDelta != nil {
                options.OnTextDelta(delta)
            }
        }

        // 检查终止
        if isTerminalChunk(data) {
            sawTerminal = true
            usage = extractUsage(data)
        }
    }

    if err := scanner.Err(); err != nil {
        return nil, &PartialResponseError{
            PartialContent: contentBuilder.String(),
            Cause:          err,
        }
    }

    finalContent := contentBuilder.String()
    if finalContent == "" {
        return nil, fmt.Errorf("LLM returned empty response from stream")
    }
    if !sawTerminal {
        return nil, &PartialResponseError{
            PartialContent: finalContent,
            Cause:          fmt.Errorf("stream closed without [DONE]/finish_reason"),
        }
    }

    return &ChatResponse{
        Content:      StripLeadingThinkBlock(finalContent),
        Usage:        usage,
        FinishReason: "stop",
    }, nil
}
```

### PartialResponseError

```go
// PartialResponseError 流式响应中途被掐断
type PartialResponseError struct {
	PartialContent string
	Cause          error
}

func (e *PartialResponseError) Error() string {
	return fmt.Sprintf("Stream interrupted after %d chars: %v", len(e.PartialContent), e.Cause)
}
```

**关键语义**：中途被掐断的内容不完整、不可信。由 `withTransientLLMRetry` 整体重新生成；重试耗尽后如实抛错。绝不把半截内容当成功返回。

---

## 九、Agent 级别模型覆盖

不同 Agent 可以使用不同的模型。Agent 级模型覆盖通过 `LLMConfig` 中的 `agentOverrides` 字段实现：

```go
// LLMConfig LLM 配置
type LLMConfig struct {
    Service         string                 `json:"service"`         // 服务名
    Model           string                 `json:"model"`           // 默认模型
    APIKey          string                 `json:"apiKey"`          // API Key
    BaseURL         string                 `json:"baseUrl"`         // 自定义 BaseURL
    Temperature     *float64               `json:"temperature"`     // 默认温度
    ThinkingBudget  int                    `json:"thinkingBudget"`  // 思考预算
    Stream          *bool                  `json:"stream"`          // 是否流式
    APIFormat       string                 `json:"apiFormat"`       // chat / responses
    Provider        string                 `json:"provider"`        // openai / anthropic
    ProxyURL        string                 `json:"proxyUrl"`        // 代理地址
    Headers         map[string]string      `json:"headers"`         // 自定义请求头
    Extra           map[string]interface{} `json:"extra"`           // 额外参数
    ConfigSource    string                 `json:"configSource"`    // 配置来源
    AgentOverrides  map[string]string      `json:"agentOverrides"`  // Agent 级模型覆盖
}
```

### 覆盖查找

```go
// ResolveModelForAgent 解析 Agent 使用的模型
func ResolveModelForAgent(config LLMConfig, agentName string) string {
    if override, ok := config.AgentOverrides[agentName]; ok {
        return override
    }
    return config.Model
}
```

### 典型配置示例

```json
{
  "service": "deepseek",
  "model": "deepseek-chat",
  "agentOverrides": {
    "writer": "deepseek-chat",
    "architect": "deepseek-reasoner",
    "validator": "deepseek-chat",
    "reviewer": "deepseek-chat",
    "reviser": "deepseek-chat",
    "detector": "deepseek-chat"
  }
}
```

### resolvePiModel

当 Agent 传入不同的 model 字符串时，基于 client 的基础 piModel 创建覆盖：

```go
func resolvePiModel(client LLMClient, model string) *PiModel {
    base := client.PiModel()
    if base.ID == model {
        return base
    }
    // 创建覆盖副本
    copy := *base
    copy.ID = model
    copy.Name = model
    return &copy
}
```

---

## 十、错误处理与重试

### 错误包装

```go
func wrapLLMError(err error, ctx *ErrorContext) error {
    msg := err.Error()

    // 400 - 请求参数错误
    if strings.Contains(msg, "400") {
        return fmt.Errorf("API 返回 400（请求参数错误）。常见原因：\n"+
            "  1. temperature / max_tokens 超出模型约束\n"+
            "  2. 模型名称不正确或未上架\n"+
            "  3. 消息格式不兼容\n"+
            "  (baseUrl: %s, model: %s)", ctx.BaseURL, ctx.Model)
    }

    // 401 - 未授权
    if strings.Contains(msg, "401") {
        return fmt.Errorf("API 返回 401 (未授权)。请检查 API Key 是否正确。(baseUrl: %s)", ctx.BaseURL)
    }

    // 403 - 被拒绝
    if strings.Contains(msg, "403") {
        return fmt.Errorf("API 返回 403 (请求被拒绝)。可能原因：API Key 无效/过期、内容审查拦截、余额不足")
    }

    // 429 - 请求过多
    if strings.Contains(msg, "429") {
        return fmt.Errorf("API 返回 429 (请求过多)。请稍后重试，或检查 API 配额。")
    }

    // 连接错误
    if isConnectionError(msg) {
        return fmt.Errorf("无法连接到 API 服务。可能原因：baseUrl 地址不正确、网络不通、API 服务暂时不可用")
    }

    // 5xx
    if strings.Contains(msg, "status code") {
        return fmt.Errorf("API 返回 5xx（上游服务异常）。可能原因：模型未上架、服务端临时故障、apikey 无权限")
    }

    return err
}
```

### 瞬时错误重试

```go
// withTransientLLMRetry 对瞬时传输错误进行重试
func withTransientLLMRetry(
    fn func() (*ChatResponse, error),
    options *RetryOptions,
) (*ChatResponse, error) {
    maxRetries := 2 // TRANSIENT_LLM_RETRIES
    var lastErr error

    for attempt := 0; attempt <= maxRetries; attempt++ {
        resp, err := fn()
        if err == nil {
            return resp, nil
        }

        if !isTransientLLMTransportError(err) {
            return nil, err // 非瞬时错误，不重试
        }

        lastErr = err
        if attempt < maxRetries {
            time.Sleep(time.Duration(attempt+1) * time.Second) // 指数退避
        }
    }

    return nil, lastErr
}
```

### 瞬时错误判定

```go
func isTransientLLMTransportError(err error) bool {
    text := collectErrorText(err)
    patterns := []string{
        "terminated",
        "UND_ERR_SOCKET",
        "ECONNRESET",
        "ETIMEDOUT",
        "EPIPE",
        "socket hang up",
        "fetch failed",
        "ECONNREFUSED",
        "ENOTFOUND",
    }
    for _, p := range patterns {
        if strings.Contains(text, p) {
            return true
        }
    }
    return false
}
```

---

## 十一、运行时配置切换

Provider 层支持运行时切换 LLM 配置，无需重启：

```go
// LLMConfigManager LLM 配置管理器
type LLMConfigManager struct {
    mu       sync.RWMutex
    config   *LLMConfig
    client   LLMClient
    projectRoot string
}

// NewLLMConfigManager 创建配置管理器
func NewLLMConfigManager(projectRoot string) *LLMConfigManager

// GetConfig 获取当前配置
func (m *LLMConfigManager) GetConfig() *LLMConfig

// SetConfig 切换配置（重建 client）
func (m *LLMConfigManager) SetConfig(config *LLMConfig) error

// GetClient 获取当前 LLM 客户端
func (m *LLMConfigManager) GetClient() LLMClient

// Reload 重新加载配置
func (m *LLMConfigManager) Reload() error
```

### 配置来源

```
1. 项目级 inkos.json（llm 字段）
2. 书籍级 book.json（llm 字段，覆盖项目级）
3. 环境变量（INKOS_LLM_* 覆盖）
```

---

## 十二、完整 Go 接口定义

```go
package llm

import (
	"context"
	"errors"
)

// ============================================================================
// LLMClient 接口
// ============================================================================

// LLMClient LLM 客户端接口
type LLMClient interface {
	// ChatCompletion 非流式聊天补全
	ChatCompletion(
		ctx context.Context,
		messages []Message,
		options *ChatOptions,
	) (*ChatResponse, error)

	// ChatCompletionStream 流式聊天补全（SSE）
	ChatCompletionStream(
		ctx context.Context,
		messages []Message,
		options *ChatOptions,
		onProgress OnStreamProgress,
	) (*ChatResponse, error)

	// Provider 返回提供商类型
	Provider() string // "openai" / "anthropic"

	// Service 返回服务名
	Service() string // "deepseek" / "moonshot" / ...

	// ConfigSource 返回配置来源
	ConfigSource() string

	// APIFormat 返回 API 格式
	APIFormat() string // "chat" / "responses"

	// Stream 返回是否流式
	Stream() bool

	// ProxyURL 返回代理地址
	ProxyURL() string

	// Defaults 返回默认配置
	Defaults() ClientDefaults
}

// ClientDefaults 客户端默认配置
type ClientDefaults struct {
	Temperature    float64                `json:"temperature"`
	MaxTokens      int                    `json:"maxTokens"`
	ThinkingBudget int                    `json:"thinkingBudget"`
	Extra          map[string]interface{} `json:"extra"`
}

// ============================================================================
// Message
// ============================================================================

// Message LLM 消息
type Message struct {
	Role    string `json:"role"`    // "system" / "user" / "assistant"
	Content string `json:"content"`
}

// ============================================================================
// ChatOptions
// ============================================================================

// ChatOptions 聊天选项
type ChatOptions struct {
	Temperature     float64         `json:"temperature,omitempty"`
	MaxTokens       int             `json:"maxTokens,omitempty"`
	Model           string          `json:"model,omitempty"`
	Stop            []string        `json:"stop,omitempty"`
	WebSearch       bool            `json:"webSearch,omitempty"`
	OnStreamProgress OnStreamProgress `json:"-"`
	OnTextDelta     func(text string) `json:"-"`
	Retry           *bool           `json:"-"`
}

// ============================================================================
// ChatResponse
// ============================================================================

// ChatResponse 聊天响应
type ChatResponse struct {
	Content      string     `json:"content"`
	Usage        TokenUsage `json:"usage"`
	FinishReason string     `json:"finishReason"` // stop / length / tool_calls / content_filter
}

// TokenUsage Token 用量
type TokenUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// ============================================================================
// 流式输出
// ============================================================================

// StreamProgress 流式进度
type StreamProgress struct {
	ElapsedMs    int64  `json:"elapsedMs"`
	TotalChars   int    `json:"totalChars"`
	ChineseChars int    `json:"chineseChars"`
	Status       string `json:"status"` // "streaming" / "done"
}

// OnStreamProgress 流式进度回调
type OnStreamProgress func(progress StreamProgress)

// StreamMonitor 流式监控器
type StreamMonitor struct {
	// 含实现细节
}

// NewStreamMonitor 创建流式监控器
func NewStreamMonitor(onProgress OnStreamProgress, intervalMs int) *StreamMonitor

// OnChunk 处理文本增量
func (m *StreamMonitor) OnChunk(text string)

// Stop 停止监控
func (m *StreamMonitor) Stop()

// ============================================================================
// LLMConfig
// ============================================================================

// LLMConfig LLM 配置
type LLMConfig struct {
	Service        string                 `json:"service"`
	Model          string                 `json:"model"`
	APIKey         string                 `json:"apiKey"`
	BaseURL        string                 `json:"baseUrl"`
	Temperature    *float64               `json:"temperature"`
	ThinkingBudget int                    `json:"thinkingBudget"`
	Stream         *bool                  `json:"stream"`
	APIFormat      string                 `json:"apiFormat"` // "chat" / "responses"
	Provider       string                 `json:"provider"`  // "openai" / "anthropic"
	ProxyURL       string                 `json:"proxyUrl"`
	Headers        map[string]string      `json:"headers"`
	Extra          map[string]interface{} `json:"extra"`
	ConfigSource   string                 `json:"configSource"`
	AgentOverrides map[string]string      `json:"agentOverrides"`
}

// CreateLLMClient 根据 LLMConfig 创建 LLM 客户端
func CreateLLMClient(config LLMConfig) LLMClient

// ============================================================================
// chatCompletion 入口函数
// ============================================================================

// ChatCompletion 统一聊天补全入口
func ChatCompletion(
	ctx context.Context,
	client LLMClient,
	model string,
	messages []Message,
	options *ChatOptions,
) (*ChatResponse, error)

// ============================================================================
// Provider 注册机制
// ============================================================================

// ApiProtocol API 协议
type ApiProtocol string

const (
	ApiOpenAICompletions  ApiProtocol = "openai-completions"
	ApiOpenAIResponses    ApiProtocol = "openai-responses"
	ApiAnthropicMessages  ApiProtocol = "anthropic-messages"
	ApiGoogleGenerativeAI ApiProtocol = "google-generative-ai"
)

// EndpointGroup Endpoint 分组
type EndpointGroup string

const (
	GroupOverseas   EndpointGroup = "overseas"
	GroupChina      EndpointGroup = "china"
	GroupAggregator EndpointGroup = "aggregator"
	GroupLocal      EndpointGroup = "local"
	GroupCodingPlan EndpointGroup = "codingPlan"
)

// InkosModel 模型卡
type InkosModel struct {
	ID                  string             `json:"id"`
	MaxOutput           int                `json:"maxOutput"`
	ContextWindowTokens int                `json:"contextWindowTokens"`
	Enabled             *bool              `json:"enabled,omitempty"`
	DeploymentName      string             `json:"deploymentName,omitempty"`
	ReleasedAt          string             `json:"releasedAt,omitempty"`
	Temperature         *float64           `json:"temperature,omitempty"`
	Status              string             `json:"status,omitempty"`
	Replacement         string             `json:"replacement,omitempty"`
	Capabilities        *ModelCapabilities `json:"capabilities,omitempty"`
}

// ModelCapabilities 模型能力
type ModelCapabilities struct {
	Text        bool `json:"text"`
	ImageInput  bool `json:"imageInput"`
	ImageOutput bool `json:"imageOutput"`
	Tools       bool `json:"tools"`
	Reasoning   bool `json:"reasoning"`
}

// ProviderCompat 兼容性标记
type ProviderCompat struct {
	SupportsStore                   *bool `json:"supportsStore,omitempty"`
	SupportsSystemRole              *bool `json:"supportsSystemRole,omitempty"`
	SupportsDeveloperRole           *bool `json:"supportsDeveloperRole,omitempty"`
	RequiresAssistantAfterToolResult *bool `json:"requiresAssistantAfterToolResult,omitempty"`
}

// ProviderTransportDefaults 传输默认值
type ProviderTransportDefaults struct {
	APIFormat string `json:"apiFormat,omitempty"` // "chat" / "responses"
	Stream    *bool  `json:"stream,omitempty"`
}

// InkosEndpoint 提供商端点
type InkosEndpoint struct {
	ID               string                    `json:"id"`
	Label            string                    `json:"label"`
	Group            EndpointGroup             `json:"group,omitempty"`
	API              ApiProtocol               `json:"api"`
	BaseURL          string                    `json:"baseUrl"`
	ModelsBaseURL    string                    `json:"modelsBaseUrl,omitempty"`
	CheckModel       string                    `json:"checkModel,omitempty"`
	TemperatureRange [2]float64               `json:"temperatureRange,omitempty"`
	DefaultTemp      float64                   `json:"defaultTemp,omitempty"`
	WritingTemp      float64                   `json:"writingTemp,omitempty"`
	TemperatureHint  string                    `json:"temperatureHint,omitempty"`
	Compat           *ProviderCompat           `json:"compat,omitempty"`
	TransportDefaults *ProviderTransportDefaults `json:"transportDefaults,omitempty"`
	Models           []InkosModel              `json:"models"`
}

// RegisterEndpoint 注册提供商端点
func RegisterEndpoint(endpoint *InkosEndpoint)

// GetEndpoint 按 ID 获取端点
func GetEndpoint(id string) *InkosEndpoint

// LookupModel 在指定服务中查找模型卡
func LookupModel(service, modelID string) *InkosModel

// AllEndpoints 返回所有已注册端点
func AllEndpoints() []*InkosEndpoint

// ============================================================================
// 密钥管理
// ============================================================================

// SecretsFile secrets.json 格式
type SecretsFile struct {
	Services map[string]SecretEntry `json:"services"`
}

// SecretEntry 密钥条目
type SecretEntry struct {
	APIKey string `json:"apiKey"`
}

// LoadSecrets 加载 secrets.json
func LoadSecrets(projectRoot string) (*SecretsFile, error)

// SaveSecrets 保存 secrets.json
func SaveSecrets(projectRoot string, secrets *SecretsFile) error

// GetServiceApiKey 获取服务 API Key
// 查找优先级：1. secrets.json  2. 环境变量 {SERVICE}_API_KEY
func GetServiceApiKey(projectRoot, service string) (string, error)

// ============================================================================
// Agent 级别模型覆盖
// ============================================================================

// ResolveModelForAgent 解析 Agent 使用的模型
func ResolveModelForAgent(config LLMConfig, agentName string) string

// ============================================================================
// 上下文窗口检查
// ============================================================================

// ContextWindowExceededError 上下文窗口超限错误
type ContextWindowExceededError struct {
	EstimatedInputTokens  int
	ReservedOutputTokens  int
	ContextWindow         int
	Model                 string
}

func (e *ContextWindowExceededError) Error() string

// EstimateTextTokens 估算文本 Token 数
func EstimateTextTokens(text string) int

// AssertWithinContextWindow 断言在上下文窗口内
func AssertWithinContextWindow(piModel *PiModel, model string, estimatedInput, reservedOutput int) error

// ============================================================================
// 错误类型
// ============================================================================

// PartialResponseError 流式响应中途被掐断
type PartialResponseError struct {
	PartialContent string
	Cause          error
}

func (e *PartialResponseError) Error() string

// WrapLLMError 包装 LLM 错误
func WrapLLMError(err error, ctx *ErrorContext) error

// ErrorContext 错误上下文
type ErrorContext struct {
	BaseURL string
	Model   string
	Service string
}

// IsTransientLLMTransportError 判断是否为瞬时传输错误
func IsTransientLLMTransportError(err error) bool

// WithTransientLLMRetry 瞬时错误重试
func WithTransientLLMRetry(
	fn func() (*ChatResponse, error),
	options *RetryOptions,
) (*ChatResponse, error)

// RetryOptions 重试选项
type RetryOptions struct {
	Enabled bool
	Cancel  context.Context
}

// ============================================================================
// Think Tag 清理
// ============================================================================

// StripLeadingThinkBlock 清除响应开头的 think 标签块
func StripLeadingThinkBlock(content string) string

// CreateLeadingThinkTagStripper 创建流式 think 标签清理器
func CreateLeadingThinkTagStripper() func(text string) string

// ============================================================================
// 配置管理器
// ============================================================================

// LLMConfigManager LLM 配置管理器
type LLMConfigManager struct {
	// 含实现细节
}

// NewLLMConfigManager 创建配置管理器
func NewLLMConfigManager(projectRoot string) *LLMConfigManager

// GetConfig 获取当前配置
func (m *LLMConfigManager) GetConfig() *LLMConfig

// SetConfig 切换配置（重建 client）
func (m *LLMConfigManager) SetConfig(config *LLMConfig) error

// GetClient 获取当前 LLM 客户端
func (m *LLMConfigManager) GetClient() LLMClient

// Reload 重新加载配置
func (m *LLMConfigManager) Reload() error

// ============================================================================
// 请求头处理
// ============================================================================

// SanitizeHTTPHeaders 清理 HTTP 请求头
func SanitizeHTTPHeaders(headers map[string]string) map[string]string

// MergeUserAgent 合并 User-Agent
func MergeUserAgent(headers map[string]string) map[string]string

// ParseEnvHeaders 从环境变量解析请求头
func ParseEnvHeaders() map[string]string

// ============================================================================
// 服务预设
// ============================================================================

// ServicePreset 服务预设
type ServicePreset struct {
	API     ApiProtocol
	BaseURL string
}

// ResolveServicePreset 解析服务预设
func ResolveServicePreset(serviceName string) *ServicePreset

// ============================================================================
// 连通性检查
// ============================================================================

// VerifyProvider 验证提供商连通性
func VerifyProvider(
	ctx context.Context,
	config LLMConfig,
	projectRoot string,
) (*VerifyResult, error)

// VerifyResult 验证结果
type VerifyResult struct {
	OK      bool
	Model   string
	Message string
}
```

---

## 附：LLM 调用完整流程

```
Agent.chat(messages, options)
  │
  ├─ ResolveModelForAgent(config, agentName) → model
  │
  ├─ ChatCompletion(ctx, client, model, messages, options)
  │   │
  │   ├─ clampTemperatureForModel(service, model, temperature)
  │   │   └─ 如果模型卡有 Temperature 硬约束 → clamp
  │   │
  │   ├─ WithTransientLLMRetry(func() {
  │   │     │
  │   │     ├─ AssertWithinContextWindow(piModel, model, inputTokens, outputTokens)
  │   │     │   └─ 超限 → ContextWindowExceededError
  │   │     │
  │   │     ├─ if client.Stream():
  │   │     │   └─ chatCompletionViaStream（SSE）
  │   │     │       ├─ NewStreamMonitor(onProgress, 30s)
  │   │     │       ├─ callSSEEndpoint
  │   │     │       ├─ 逐 chunk 解析
  │   │     │       │   ├─ extractTextDelta → OnChunk / OnTextDelta
  │   │     │       │   └─ isTerminalChunk → sawTerminal
  │   │     │       ├─ !sawTerminal → PartialResponseError
  │   │     │       └─ StripLeadingThinkBlock(finalContent)
  │   │     │
  │   │     └─ else:
  │   │         └─ chatCompletionViaNonStream
  │   │             └─ StripLeadingThinkBlock(content)
  │   │   })
  │   │
  │   └─ WrapLLMError(err, errorCtx)
  │       ├─ 400 → 请求参数错误提示
  │       ├─ 401 → 未授权提示
  │       ├─ 403 → 被拒绝提示
  │       ├─ 429 → 请求过多提示
  │       ├─ 5xx → 上游异常提示
  │       └─ 连接错误 → 网络问题提示
  │
  └─ 返回 {Content, Usage, FinishReason}
```
