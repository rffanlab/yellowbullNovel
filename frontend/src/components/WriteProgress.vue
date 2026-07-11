<script setup lang="ts">
import { computed, watch, nextTick, ref } from 'vue'
import { ElIcon, ElTag } from 'element-plus'
import { Loading, CircleCheckFilled, WarningFilled } from '@element-plus/icons-vue'
import type { WritingProgress } from '@/stores/chat'

const props = defineProps<{
  progress: WritingProgress
}>()

const scrollRef = ref<HTMLElement | null>(null)

const statusLabel = computed(() => {
  if (props.progress.error) return '错误'
  if (!props.progress.active && props.progress.streamContent) return '已完成'
  return props.progress.status || '等待中...'
})

const statusType = computed(() => {
  if (props.progress.error) return 'danger'
  if (!props.progress.active && props.progress.streamContent) return 'success'
  if (props.progress.active) return 'primary'
  return 'info'
})

// 自动滚动到底部
watch(
  () => props.progress.streamContent,
  async () => {
    await nextTick()
    if (scrollRef.value) {
      scrollRef.value.scrollTop = scrollRef.value.scrollHeight
    }
  }
)
</script>

<template>
  <div class="write-progress">
    <div class="progress-header">
      <el-tag :type="statusType" size="small" effect="light">
        <el-icon v-if="progress.active" class="is-loading"><Loading /></el-icon>
        <el-icon v-else-if="progress.error"><WarningFilled /></el-icon>
        <el-icon v-else-if="!progress.active && progress.streamContent">
          <CircleCheckFilled />
        </el-icon>
        {{ statusLabel }}
      </el-tag>
      <span v-if="progress.chapterTitle" class="chapter-title">
        📖 {{ progress.chapterTitle }}
      </span>
    </div>
    <div ref="scrollRef" class="progress-content">
      <pre>{{ progress.streamContent || '（等待输出...）' }}</pre>
    </div>
  </div>
</template>

<style scoped>
.write-progress {
  background: #fafafa;
  border: 1px solid var(--ink-border);
  border-radius: 8px;
  padding: 12px;
  margin: 8px 0;
}
.progress-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 8px;
}
.chapter-title {
  font-size: 13px;
  color: var(--ink-text);
  font-weight: 500;
}
.progress-content {
  max-height: 320px;
  overflow-y: auto;
  background: #fff;
  border-radius: 6px;
  padding: 10px;
}
.progress-content pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
  font-size: 14px;
  line-height: 1.7;
  color: var(--ink-text);
}
.is-loading {
  animation: rotate 1.2s linear infinite;
}
@keyframes rotate {
  to {
    transform: rotate(360deg);
  }
}
</style>
