/**
 * SSE 客户端：封装 EventSource 监听后端写作流
 * 后端接口：GET /api/books/:id/write-stream?action=xxx&instruction=xxx
 */
export interface SSEEvent {
  event: string
  data: string
}

export interface SSEHandlers {
  onMessage?: (data: string) => void
  onEvent?: (event: string, data: string) => void
  onError?: (err: Event) => void
  onOpen?: () => void
  onClose?: () => void
}

export class SSEClient {
  private source: EventSource | null = null
  private handlers: SSEHandlers
  private url: string

  constructor(url: string, handlers: SSEHandlers) {
    this.url = url
    this.handlers = handlers
  }

  open() {
    this.close()
    const source = new EventSource(this.url)
    this.source = source

    source.onopen = () => {
      this.handlers.onOpen?.()
    }

    // 默认 message 事件
    source.onmessage = (ev: MessageEvent) => {
      this.handlers.onMessage?.(ev.data)
    }

    // 监听具名事件（后端通过 event: xxx 发送）
    // 常见事件：progress / chunk / done / error / chapter_title
    const namedEvents = [
      'progress',
      'chunk',
      'content',
      'done',
      'error',
      'chapter_title',
      'status',
      'thinking',
    ]
    namedEvents.forEach((evt) => {
      source.addEventListener(evt, (ev: MessageEvent) => {
        this.handlers.onEvent?.(evt, ev.data)
      })
    })

    source.onerror = (err) => {
      this.handlers.onError?.(err)
      // EventSource 会自动重连，但遇到 done 后已 close 不会再触发
    }
  }

  close() {
    if (this.source) {
      this.source.close()
      this.source = null
      this.handlers.onClose?.()
    }
  }

  get readyState(): number {
    return this.source?.readyState ?? EventSource.CLOSED
  }
}

/**
 * 构造写作流 URL
 */
export function buildWriteStreamUrl(
  bookId: string,
  params: {
    action: string
    instruction?: string
    chapter_num?: number
  }
): string {
  const base = import.meta.env.VITE_API_BASE || '/api'
  const search = new URLSearchParams()
  search.set('action', params.action)
  if (params.instruction) search.set('instruction', params.instruction)
  if (params.chapter_num !== undefined)
    search.set('chapter_num', String(params.chapter_num))
  return `${base}/books/${bookId}/write-stream?${search.toString()}`
}
