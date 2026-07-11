import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  bookApi,
  type Book,
  type CreateBookRequest,
  type Chapter,
  type Character,
  type Hook,
  type TruthFile,
} from '@/api/book'

export const useBookStore = defineStore('book', () => {
  // ===== state =====
  const books = ref<Book[]>([])
  const currentBook = ref<Book | null>(null)
  const chapters = ref<Chapter[]>([])
  const characters = ref<Character[]>([])
  const hooks = ref<Hook[]>([])
  const truthFiles = ref<TruthFile[]>([])
  const loading = ref(false)

  // ===== getters =====
  const totalBooks = computed(() => books.value.length)
  const currentChapterNum = computed(
    () => currentBook.value?.current_chapter ?? 0
  )

  // ===== actions =====
  async function fetchBooks() {
    loading.value = true
    try {
      const data = await bookApi.list()
      books.value = Array.isArray(data) ? data : []
    } finally {
      loading.value = false
    }
  }

  async function fetchBook(id: string) {
    const data = await bookApi.get(id)
    currentBook.value = data
    return data
  }

  async function createBook(payload: CreateBookRequest) {
    const data = await bookApi.create(payload)
    books.value.unshift(data)
    return data
  }

  async function deleteBook(id: string) {
    await bookApi.delete(id)
    books.value = books.value.filter((b) => b.id !== id)
  }

  async function fetchChapters(id: string) {
    const data = await bookApi.chapters(id)
    chapters.value = Array.isArray(data) ? data : []
  }

  async function fetchChapter(bookId: string, num: number) {
    return await bookApi.chapter(bookId, num)
  }

  async function fetchCharacters(id: string) {
    const data = await bookApi.characters(id)
    characters.value = Array.isArray(data) ? data : []
  }

  async function fetchHooks(id: string) {
    const data = await bookApi.hooks(id)
    hooks.value = Array.isArray(data) ? data : []
  }

  async function fetchTruthFiles(id: string) {
    const data = await bookApi.truthFiles(id)
    truthFiles.value = Array.isArray(data) ? data : []
  }

  async function fetchAll(id: string) {
    await Promise.all([
      fetchBook(id),
      fetchChapters(id),
      fetchCharacters(id),
      fetchHooks(id),
      fetchTruthFiles(id),
    ])
  }

  function clearCurrent() {
    currentBook.value = null
    chapters.value = []
    characters.value = []
    hooks.value = []
    truthFiles.value = []
  }

  return {
    books,
    currentBook,
    chapters,
    characters,
    hooks,
    truthFiles,
    loading,
    totalBooks,
    currentChapterNum,
    fetchBooks,
    fetchBook,
    createBook,
    deleteBook,
    fetchChapters,
    fetchChapter,
    fetchCharacters,
    fetchHooks,
    fetchTruthFiles,
    fetchAll,
    clearCurrent,
  }
})
