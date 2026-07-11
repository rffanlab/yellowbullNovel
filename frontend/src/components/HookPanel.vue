<script setup lang="ts">
import { computed } from 'vue'
import { ElEmpty, ElTag, ElScrollbar } from 'element-plus'
import type { Hook } from '@/api/book'

const props = defineProps<{ hooks: Hook[] }>()

const statusType = (status: string) => {
  switch (status) {
    case 'planted':
    case 'open':
      return 'warning'
    case 'resolved':
    case 'closed':
      return 'success'
    case 'abandoned':
      return 'info'
    default:
      return 'info'
  }
}

const statusLabel = (status: string) => {
  const map: Record<string, string> = {
    planted: '已埋设',
    open: '待回收',
    resolved: '已回收',
    closed: '已关闭',
    abandoned: '已废弃',
  }
  return map[status] || status
}

const sorted = computed(() =>
  [...props.hooks].sort(
    (a, b) => a.created_chapter - b.created_chapter
  )
)
</script>

<template>
  <div class="hook-panel">
    <el-scrollbar v-if="sorted.length" height="100%">
      <div v-for="h in sorted" :key="h.id" class="hook-item">
        <div class="hook-head">
          <span class="name">{{ h.name }}</span>
          <el-tag :type="statusType(h.status)" size="small" effect="light">
            {{ statusLabel(h.status) }}
          </el-tag>
        </div>
        <p class="content">{{ h.content }}</p>
        <div class="meta">
          <span>埋设：第 {{ h.created_chapter }} 章</span>
          <span v-if="h.resolved_chapter">
            回收：第 {{ h.resolved_chapter }} 章
          </span>
        </div>
      </div>
    </el-scrollbar>
    <el-empty v-else description="暂无伏笔" :image-size="80" />
  </div>
</template>

<style scoped>
.hook-panel {
  height: 100%;
}
.hook-item {
  padding: 10px 12px;
  border-bottom: 1px solid var(--ink-border);
}
.hook-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}
.hook-item .name {
  font-size: 14px;
  font-weight: 500;
}
.hook-item .content {
  font-size: 13px;
  color: var(--ink-text);
  margin: 4px 0;
  line-height: 1.5;
}
.hook-item .meta {
  display: flex;
  gap: 16px;
  font-size: 12px;
  color: var(--ink-text-secondary);
}
</style>
