<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ElButton,
  ElCard,
  ElDescriptions,
  ElDescriptionsItem,
  ElEmpty,
  ElIcon,
  ElScrollbar,
  ElTag,
  ElMessage,
  ElMessageBox,
} from 'element-plus'
import {
  ArrowLeft,
  ArrowRight,
  Back,
  Edit,
} from '@element-plus/icons-vue'
import { useBookStore } from '@/stores/book'
import { writeApi } from '@/api/write'
import type { Chapter } from '@/api/book'

const route = useRoute()
const router = useRouter()
const bookStore = useBookStore()

const bookId = computed(() => route.params.id as string)
const chapterNum = computed(() => Number(route.params.num))
const chapter = ref<Chapter | null>(null)
const loading = ref(false)

async function loadChapter() {
  loading.value = true
  try {
    chapter.value = await bookStore.fetchChapter(bookId.value, chapterNum.value)
  } catch {
    chapter.value = null
  } finally {
    loading.value = false
  }
}

async function loadBook() {
  if (!bookStore.currentBook || bookStore.currentBook.id !== bookId.value) {
    try {
      await bookStore.fetchBook(bookId.value)
    } catch {
      // ignore
    }
  }
}

function goPrev() {
  if (chapterNum.value > 1) {
    router.push(`/books/${bookId.value}/chapters/${chapterNum.value - 1}`)
  }
}

function goNext() {
  if (bookStore.currentBook && chapterNum.value < bookStore.currentBook.current_chapter) {
    router.push(`/books/${bookId.value}/chapters/${chapterNum.value + 1}`)
  }
}

function backToBook() {
  router.push(`/books/${bookId.value}`)
}

async function rework() {
  try {
    await ElMessageBox.confirm(
      `确认对第 ${chapterNum.value} 章进行回炉重写？原内容将被覆盖。`,
      '确认回炉',
      { type: 'warning', confirmButtonText: '回炉', cancelButtonText: '取消' }
    )
    await writeApi.rework(bookId.value, chapterNum.value)
    ElMessage.success('已触发回炉')
    router.push(`/books/${bookId.value}`)
  } catch {
    // 用户取消
  }
}

async function polish() {
  try {
    await writeApi.polish(bookId.value, chapterNum.value)
    ElMessage.success('已触发润色')
    await loadChapter()
  } catch {
    // ignore
  }
}

onMounted(async () => {
  await loadBook()
  await loadChapter()
})
</script>

<template>
  <div class="page-container chapter-reader">
    <div class="reader-header">
      <el-button :icon="Back" @click="backToBook">返回书籍</el-button>
      <div class="nav-buttons">
        <el-button
          :icon="ArrowLeft"
          :disabled="chapterNum <= 1"
          @click="goPrev"
        >
          上一章
        </el-button>
        <el-button
          :disabled="!bookStore.currentBook || chapterNum >= bookStore.currentBook.current_chapter"
          @click="goNext"
        >
          下一章
          <el-icon class="el-icon--right"><ArrowRight /></el-icon>
        </el-button>
      </div>
    </div>

    <el-card v-loading="loading" class="content-card">
      <template v-if="chapter">
        <div class="chapter-title">
          第 {{ chapter.num }} 章 · {{ chapter.title || '未命名' }}
        </div>
        <el-descriptions :column="3" border size="small" class="meta-desc">
          <el-descriptions-item label="字数">
            {{ chapter.word_count || 0 }}
          </el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag size="small">{{ chapter.status }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="创建时间">
            {{ chapter.created_at }}
          </el-descriptions-item>
        </el-descriptions>

        <el-scrollbar class="content-scroll">
          <pre class="content">{{ chapter.content }}</pre>
        </el-scrollbar>

        <div v-if="chapter.audit_result" class="audit-result">
          <div class="section-title">审计结果</div>
          <div class="audit-content">{{ chapter.audit_result }}</div>
        </div>

        <div class="action-bar">
          <el-button :icon="Edit" type="warning" plain @click="rework">
            回炉重写
          </el-button>
          <el-button :icon="Edit" type="primary" plain @click="polish">
            润色
          </el-button>
        </div>
      </template>
      <el-empty v-else description="未找到章节" />
    </el-card>
  </div>
</template>

<style scoped>
.chapter-reader {
  max-width: 860px;
}
.reader-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}
.nav-buttons {
  display: flex;
  gap: 8px;
}
.content-card {
  min-height: 60vh;
}
.chapter-title {
  font-size: 22px;
  font-weight: 600;
  text-align: center;
  margin-bottom: 16px;
}
.meta-desc {
  margin-bottom: 16px;
}
.content-scroll {
  max-height: 65vh;
  background: #fafafa;
  border: 1px solid var(--ink-border);
  border-radius: 6px;
  padding: 16px;
}
.content {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
  font-size: 15px;
  line-height: 1.9;
}
.audit-result {
  margin-top: 16px;
  padding: 12px;
  background: var(--el-color-warning-light-9);
  border-radius: 6px;
}
.section-title {
  font-weight: 600;
  margin-bottom: 8px;
}
.audit-content {
  font-size: 13px;
  white-space: pre-wrap;
  line-height: 1.6;
}
.action-bar {
  display: flex;
  gap: 12px;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
