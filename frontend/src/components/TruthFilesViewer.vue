<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElEmpty, ElScrollbar, ElSelect, ElOption, ElButton } from 'element-plus'
import { View } from '@element-plus/icons-vue'
import type { TruthFile } from '@/api/book'

const props = defineProps<{ files: TruthFile[] }>()

const currentId = ref<string>('')
const current = ref<TruthFile | null>(null)

watch(
  () => props.files,
  (files) => {
    if (files.length && !currentId.value) {
      currentId.value = files[0].id
      current.value = files[0]
    } else if (!files.length) {
      currentId.value = ''
      current.value = null
    }
  },
  { immediate: true }
)

watch(currentId, (id) => {
  current.value = props.files.find((f) => f.id === id) || null
})
</script>

<template>
  <div class="truth-viewer">
    <div class="selector">
      <el-select v-model="currentId" placeholder="选择文件" size="small">
        <el-option
          v-for="f in files"
          :key="f.id"
          :label="f.name"
          :value="f.id"
        />
      </el-select>
    </div>
    <el-scrollbar v-if="current" height="100%" class="content-scroll">
      <pre class="content">{{ current.content }}</pre>
    </el-scrollbar>
    <el-empty v-else description="暂无 truth files" :image-size="60" />
  </div>
</template>

<style scoped>
.truth-viewer {
  display: flex;
  flex-direction: column;
  height: 100%;
  gap: 8px;
}
.selector {
  flex-shrink: 0;
}
.content-scroll {
  flex: 1;
  background: #fafafa;
  border: 1px solid var(--ink-border);
  border-radius: 6px;
  padding: 8px;
}
.content {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 12px;
  line-height: 1.6;
}
</style>
