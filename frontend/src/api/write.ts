import { request } from './index'

export type WriteAction = 'next_chapter' | 'draft' | 'plan' | 'assemble'

export interface WriteRequest {
  action: WriteAction
  instruction?: string
  chapter_num?: number
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'system'
  content: string
  timestamp: string
  action?: WriteAction
  chapter_num?: number
  status?: string
}

export const writeApi = {
  /** 非流式写作（一次性返回结果） */
  write: (bookId: string, data: WriteRequest) =>
    request<ChatMessage>({
      url: `/books/${bookId}/write`,
      method: 'post',
      data,
    }),
  /** 历史消息 */
  history: (bookId: string) =>
    request<ChatMessage[]>({ url: `/books/${bookId}/messages`, method: 'get' }),
  /** 回炉重写 */
  rework: (bookId: string, chapterNum: number) =>
    request<void>({
      url: `/books/${bookId}/chapters/${chapterNum}/rework`,
      method: 'post',
    }),
  /** 润色 */
  polish: (bookId: string, chapterNum: number) =>
    request<void>({
      url: `/books/${bookId}/chapters/${chapterNum}/polish`,
      method: 'post',
    }),
  /** 短篇创建 */
  createShortFiction: (data: {
    direction: string
    chapters: number
    words_per_chapter: number
  }) =>
    request<{ id: string }>({
      url: '/short-fiction',
      method: 'post',
      data,
    }),
}
