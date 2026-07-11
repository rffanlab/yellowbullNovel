<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElCard, ElTag, ElButton, ElProgress } from 'element-plus'
import { Reading } from '@element-plus/icons-vue'
import type { Book } from '@/api/book'

const props = defineProps<{ book: Book }>()
const emit = defineEmits<{ (e: 'delete', id: string): void }>()

const router = useRouter()

const statusType = computed(() => {
  switch (props.book.status) {
    case 'ongoing':
    case 'writing':
      return 'primary'
    case 'completed':
    case 'done':
      return 'success'
    case 'error':
      return 'danger'
    case 'paused':
      return 'info'
    default:
      return 'info'
  }
})

const statusLabel = computed(() => {
  const map: Record<string, string> = {
    ongoing: '进行中',
    writing: '写作中',
    completed: '已完成',
    done: '已完成',
    error: '错误',
    paused: '已暂停',
    draft: '草稿',
  }
  return map[props.book.status] || props.book.status
})

const progressPercent = computed(() => {
  if (!props.book.target_chapters) return 0
  return Math.min(
    100,
    Math.round((props.book.current_chapter / props.book.target_chapters) * 100)
  )
})

function enter() {
  router.push(`/books/${props.book.id}`)
}
</script>

<template>
  <el-card class="book-card" shadow="hover" :body-style="{ padding: '16px' }">
    <div class="card-header">
      <span class="book-title" :title="book.title">{{ book.title }}</span>
      <el-tag :type="statusType" size="small" effect="light">
        {{ statusLabel }}
      </el-tag>
    </div>
    <div class="card-meta">
      <span class="meta-item">题材：{{ book.genre || '未分类' }}</span>
      <span class="meta-item">平台：{{ book.platform || '通用' }}</span>
      <span class="meta-item">语言：{{ book.language || '中文' }}</span>
    </div>
    <div class="card-stats">
      <span>章节：{{ book.current_chapter }} / {{ book.target_chapters }}</span>
      <span>字数：{{ book.total_words?.toLocaleString() || 0 }}</span>
    </div>
    <el-progress
      :percentage="progressPercent"
      :stroke-width="6"
      :show-text="false"
      class="card-progress"
    />
    <div class="card-actions">
      <el-button type="primary" size="small" :icon="Reading" @click="enter">
        进入书籍
      </el-button>
      <el-button type="danger" size="small" plain @click="emit('delete', book.id)">
        删除
      </el-button>
    </div>
  </el-card>
</template>

<style scoped>
.book-card {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.book-title {
  font-size: 16px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.card-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 12px;
  color: var(--ink-text-secondary);
  margin-bottom: 8px;
}
.card-stats {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  margin-bottom: 6px;
}
.card-progress {
  margin-bottom: 12px;
}
.card-actions {
  display: flex;
  gap: 8px;
}
</style>
