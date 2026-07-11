import { request } from './index'

// ===== 类型定义 =====
export interface Book {
  id: string
  title: string
  genre: string
  platform: string
  language: string
  author_intent: string
  target_chapters: number
  words_per_chapter: number
  current_chapter: number
  total_words: number
  status: string
  created_at: string
  updated_at: string
}

export interface CreateBookRequest {
  title: string
  genre: string
  platform: string
  target_chapters: number
  words_per_chapter: number
  language: string
  author_intent: string
}

export interface Chapter {
  num: number
  title: string
  content: string
  word_count: number
  status: string
  audit_result?: string
  created_at: string
}

export interface Character {
  id: string
  name: string
  role: string
  description: string
}

export interface Hook {
  id: string
  name: string
  content: string
  status: string
  created_chapter: number
  resolved_chapter?: number
}

export interface TruthFile {
  id: string
  name: string
  type: string
  content: string
  updated_at: string
}

// ===== API =====
export const bookApi = {
  list: () => request<Book[]>({ url: '/books', method: 'get' }),
  get: (id: string) => request<Book>({ url: `/books/${id}`, method: 'get' }),
  create: (data: CreateBookRequest) =>
    request<Book>({ url: '/books', method: 'post', data }),
  delete: (id: string) =>
    request<void>({ url: `/books/${id}`, method: 'delete' }),
  chapters: (id: string) =>
    request<Chapter[]>({ url: `/books/${id}/chapters`, method: 'get' }),
  chapter: (id: string, num: number) =>
    request<Chapter>({ url: `/books/${id}/chapters/${num}`, method: 'get' }),
  characters: (id: string) =>
    request<Character[]>({ url: `/books/${id}/characters`, method: 'get' }),
  hooks: (id: string) =>
    request<Hook[]>({ url: `/books/${id}/hooks`, method: 'get' }),
  truthFiles: (id: string) =>
    request<TruthFile[]>({ url: `/books/${id}/truth-files`, method: 'get' }),
  progress: (id: string) =>
    request<{ current_chapter: number; total_words: number }>({
      url: `/books/${id}/progress`,
      method: 'get',
    }),
}
