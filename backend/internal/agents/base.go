package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rffanlab/yellowbullNovel/backend/internal/llm"
)

// BaseAgent 所有 Agent 的基类，封装 LLM 客户端、日志和项目根路径。
type BaseAgent struct {
	ctx  llm.AgentContext
	name string
}

// NewBaseAgent 创建基类实例。
func NewBaseAgent(ctx llm.AgentContext, name string) BaseAgent {
	return BaseAgent{ctx: ctx, name: name}
}

// Chat 同步聊天，调用底层 LLMClient。
func (a *BaseAgent) Chat(ctx context.Context, messages []llm.Message, opts *llm.ChatOptions) (*llm.ChatResponse, error) {
	if a.ctx.Client == nil {
		return nil, fmt.Errorf("agent %s: LLM client is nil", a.name)
	}
	return a.ctx.Client.ChatCompletion(ctx, messages, opts)
}

// ChatStream 流式聊天，调用底层 LLMClient 的流式接口。
func (a *BaseAgent) ChatStream(ctx context.Context, messages []llm.Message, opts *llm.ChatOptions, onProgress llm.OnStreamProgress) (*llm.ChatResponse, error) {
	if a.ctx.Client == nil {
		return nil, fmt.Errorf("agent %s: LLM client is nil", a.name)
	}
	return a.ctx.Client.ChatCompletionStream(ctx, messages, opts, onProgress)
}

// Name 返回 Agent 名称。
func (a *BaseAgent) Name() string {
	return a.name
}

// Context 返回 Agent 上下文。
func (a *BaseAgent) Context() llm.AgentContext {
	return a.ctx
}

// ProjectRoot 返回项目根路径。
func (a *BaseAgent) ProjectRoot() string {
	return a.ctx.ProjectRoot
}

// LogInfo 记录信息日志。
func (a *BaseAgent) LogInfo(msg string, fields ...any) {
	if a.ctx.Logger != nil {
		a.ctx.Logger.Info(msg, fields...)
	}
}

// LogWarn 记录警告日志。
func (a *BaseAgent) LogWarn(msg string, fields ...any) {
	if a.ctx.Logger != nil {
		a.ctx.Logger.Warn(msg, fields...)
	}
}

// LogError 记录错误日志。
func (a *BaseAgent) LogError(msg string, fields ...any) {
	if a.ctx.Logger != nil {
		a.ctx.Logger.Error(msg, fields...)
	}
}

// LogDebug 记录调试日志。
func (a *BaseAgent) LogDebug(msg string, fields ...any) {
	if a.ctx.Logger != nil {
		a.ctx.Logger.Debug(msg, fields...)
	}
}

// readFileOrDefault 读取文件，不存在或出错时返回默认值。
func readFileOrDefault(path string, defaultVal string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return defaultVal
	}
	return string(content)
}

// writeFileSafe 安全写入文件，自动创建父目录。
func writeFileSafe(path string, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	return os.WriteFile(path, []byte(content), 0644)
}
