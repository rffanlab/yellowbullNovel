<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElEmpty, ElScrollbar } from 'element-plus'
import type { Chapter } from '@/api/book'

const props = defineProps<{
  chapters: Chapter[]
  bookId: string
  currentNum?: number
}>()

const router = useRouter()

const sorted = computed(() =>
  [...props.chapters].sort((a, b) => a.num - b.num)
)

function open(num: number) {
  router.push(`/books/${props.bookId}/chapters/${num}`)
}
</script>

<template>
  <div class="chapter-list">
    <el-scrollbar v-if="sorted.length" height="100%">
      <div
        v-for="ch in sorted"
        :key="ch.num"
        class="chapter-item"
        :class="{ active: ch.num === currentNum }"
        @click="open(ch.num)"
      >
        <div class="num">第 {{ ch.num }} 章</div>
        <div class="title" :title="ch.title">{{ ch.title || '（未命名）' }}</div>
        <div class="meta">
          <span>{{ ch.word_count || 0 }} 字</span>
          <span v-if="ch.status" class="status">{{ ch.status }}</span>
        </div>
      </div>
    </el-scrollbar>
    <el-empty v-else description="暂无章节" :image-size="80" />
  </div>
</template>

<style scoped>
.chapter-list {
  height: 100%;
}
.chapter-item {
  padding: 10px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.2s;
  border-bottom: 1px solid var(--ink-border);
}
.chapter-item:hover {
  background: var(--el-color-primary-light-9);
}
.chapter-item.active {
  background: var(--el-color-primary-light-8);
}
.chapter-item .num {
  font-size: 12px;
  color: var(--ink-text-secondary);
}
.chapter-item .title {
  font-size: 14px;
  font-weight: 500;
  margin: 2px 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.chapter-item .meta {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: var(--ink-text-secondary);
}
.chapter-item .meta .status {
  color: var(--el-color-primary);
}
</style>
