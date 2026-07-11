import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { writeApi, type ChatMessage, type WriteAction } from '@/api/write'
import {
  SSEClient,
  buildWriteStreamUrl,
} from '@/api/sse'

export interface WritingProgress {
  active: boolean
  action: WriteAction | null
  chapterTitle: string
  streamContent: string
  status: string
  error: string | null
}

export const useChatStore = defineStore('chat', () => {
  // ===== state =====
  const messages = ref<ChatMessage[]>([])
  const writing = ref<WritingProgress>({
    active: false,
    action: null,
    chapterTitle: '',
    streamContent: '',
    status: '',
    error: null,
  })
  const sseClient = ref<SSEClient | null>(null)

  // ===== getters =====
  const isWriting = computed(() => writing.value.active)
  const messageCount = computed(() => messages.value.length)

  // ===== actions =====
  async function fetchHistory(bookId: string) {
    try {
      const data = await writeApi.history(bookId)
      messages.value = Array.isArray(data) ? data : []
    } catch {
      // 静默处理，避免历史加载失败阻塞 UI
      messages.value = []
    }
  }

  function pushUserMessage(content: string, action?: WriteAction) {
    messages.value.push({
      role: 'user',
      content,
      timestamp: new Date().toISOString(),
      action,
    })
  }

  function pushAssistantMessage(content: string, status?: string) {
    messages.value.push({
      role: 'assistant',
      content,
      timestamp: new Date().toISOString(),
      status,
    })
  }

  /**
   * 通过 SSE 流式写作
   */
  function startStream(
    bookId: string,
    params: { action: WriteAction; instruction?: string; chapter_num?: number },
    onComplete?: (fullText: string) => void
  ) {
    stopStream()
    const url = buildWriteStreamUrl(bookId, params)
    writing.value = {
      active: true,
      action: params.action,
      chapterTitle: '',
      streamContent: '',
      status: '连接中...',
      error: null,
    }

    const client = new SSEClient(url, {
      onOpen: () => {
        writing.value.status = '已连接，开始写作...'
      },
      onMessage: (data) => {
        // 默认消息：拼接到流内容
        writing.value.streamContent += data
      },
      onEvent: (event, data) => {
        switch (event) {
          case 'chapter_title':
            writing.value.chapterTitle = data
            break
          case 'chunk':
          case 'content':
            writing.value.streamContent += data
            break
          case 'progress':
            writing.value.status = data
            break
          case 'status':
            writing.value.status = data
            break
          case 'thinking':
            // 可选展示思考过程
            writing.value.status = data
            break
          case 'done':
            writing.value.status = '完成'
            finishStream()
            if (onComplete) onComplete(writing.value.streamContent)
            break
          case 'error':
            writing.value.error = data
            writing.value.status = `错误：${data}`
            finishStream()
            break
        }
      },
      onError: (err) => {
        // EventSource 在连接断开时也会触发，仅在真正异常时记录
        if (writing.value.active) {
          writing.value.error = '连接中断'
          writing.value.status = '连接中断'
          finishStream()
        }
      },
    })

    sseClient.value = client
    client.open()
  }

  function finishStream() {
    if (writing.value.streamContent) {
      pushAssistantMessage(
        writing.value.streamContent,
        writing.value.status
      )
    }
    writing.value.active = false
  }

  function stopStream() {
    if (sseClient.value) {
      sseClient.value.close()
      sseClient.value = null
    }
    if (writing.value.active) {
      finishStream()
    }
    writing.value.active = false
  }

  async function writeOnce(
    bookId: string,
    params: { action: WriteAction; instruction?: string; chapter_num?: number }
  ) {
    const data = await writeApi.write(bookId, params)
    pushAssistantMessage(data.content, data.status)
    return data
  }

  function clearMessages() {
    messages.value = []
    writing.value = {
      active: false,
      action: null,
      chapterTitle: '',
      streamContent: '',
      status: '',
      error: null,
    }
  }

  return {
    messages,
    writing,
    isWriting,
    messageCount,
    fetchHistory,
    pushUserMessage,
    pushAssistantMessage,
    startStream,
    stopStream,
    writeOnce,
    clearMessages,
  }
})
