<script setup lang="ts">
import { reactive, ref, computed } from 'vue'
import {
  ElButton,
  ElCard,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElProgress,
  ElSteps,
  ElStep,
  ElTag,
  ElMessage,
} from 'element-plus'
import { Promotion, VideoPause } from '@element-plus/icons-vue'
import { writeApi } from '@/api/write'
import { SSEClient, buildWriteStreamUrl } from '@/api/sse'

const form = reactive({
  direction: '',
  chapters: 5,
  words_per_chapter: 1000,
})

const running = ref(false)
const progressText = ref('')
const streamContent = ref('')
const currentStep = ref(0)
const errorMsg = ref<string | null>(null)

let client: SSEClient | null = null

const progressPercent = computed(() => {
  if (!form.chapters) return 0
  return Math.min(100, Math.round((currentStep.value / form.chapters) * 100))
})

function start() {
  if (!form.direction.trim()) {
    ElMessage.warning('请输入短篇方向')
    return
  }
  running.value = true
  progressText.value = '启动中...'
  streamContent.value = ''
  currentStep.value = 0
  errorMsg.value = null

  const params = new URLSearchParams({
    action: 'short_fiction',
    direction: form.direction,
    chapters: String(form.chapters),
    words_per_chapter: String(form.words_per_chapter),
  })
  const base = import.meta.env.VITE_API_BASE || '/api'
  const url = `${base}/short-fiction/stream?${params.toString()}`

  client = new SSEClient(url, {
    onOpen: () => {
      progressText.value = '已连接，开始生成...'
    },
    onMessage: (data) => {
      streamContent.value += data
    },
    onEvent: (event, data) => {
      switch (event) {
        case 'progress':
          progressText.value = data
          break
        case 'chapter_done':
        case 'step':
          currentStep.value++
          progressText.value = data || `第 ${currentStep.value} 章完成`
          break
        case 'chunk':
        case 'content':
          streamContent.value += data
          break
        case 'done':
          progressText.value = '全部完成'
          running.value = false
          client?.close()
          break
        case 'error':
          errorMsg.value = data
          progressText.value = `错误：${data}`
          running.value = false
          client?.close()
          break
      }
    },
    onError: () => {
      if (running.value) {
        errorMsg.value = '连接中断'
        progressText.value = '连接中断'
        running.value = false
      }
    },
  })
  client.open()
}

function stop() {
  client?.close()
  running.value = false
  progressText.value = '已停止'
}

async function createNonStream() {
  try {
    const res = await writeApi.createShortFiction({
      direction: form.direction,
      chapters: form.chapters,
      words_per_chapter: form.words_per_chapter,
    })
    ElMessage.success(`已创建：${res.id}`)
  } catch {
    // ignore
  }
}
</script>

<template>
  <div class="page-container short-fiction">
    <h1 class="page-title">短篇创建</h1>

    <el-card class="form-card">
      <el-form :model="form" label-width="120px">
        <el-form-item label="方向描述">
          <el-input
            v-model="form.direction"
            type="textarea"
            :rows="4"
            placeholder="例如：都市青年偶然获得预知能力，卷入一场商战..."
          />
        </el-form-item>
        <el-form-item label="章节数">
          <el-input-number
            v-model="form.chapters"
            :min="1"
            :max="20"
          />
        </el-form-item>
        <el-form-item label="每章字数">
          <el-input-number
            v-model="form.words_per_chapter"
            :min="500"
            :max="5000"
            :step="500"
          />
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            :icon="Promotion"
            :loading="running"
            @click="start"
          >
            开始运行
          </el-button>
          <el-button
            v-if="running"
            type="danger"
            :icon="VideoPause"
            @click="stop"
          >
            停止
          </el-button>
          <el-button @click="createNonStream" :disabled="running">
            非流式创建
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card v-if="running || streamContent || progressText" class="progress-card">
      <div class="progress-head">
        <span class="status">{{ progressText || '等待中...' }}</span>
        <el-tag v-if="errorMsg" type="danger" size="small">
          {{ errorMsg }}
        </el-tag>
      </div>
      <el-progress
        :percentage="progressPercent"
        :stroke-width="10"
        :format="(p: number) => `${currentStep}/${form.chapters} 章`"
      />
      <el-steps
        v-if="form.chapters <= 10"
        :active="currentStep"
        align-center
        class="steps"
      >
        <el-step
          v-for="n in form.chapters"
          :key="n"
          :title="`第${n}章`"
        />
      </el-steps>
      <div v-if="streamContent" class="stream-output">
        <pre>{{ streamContent }}</pre>
      </div>
    </el-card>
  </div>
</template>

<style scoped>
.short-fiction {
  max-width: 860px;
}
.form-card {
  margin-bottom: 16px;
}
.progress-card .progress-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}
.progress-card .status {
  font-size: 14px;
  font-weight: 500;
}
.progress-card .steps {
  margin: 16px 0;
}
.stream-output {
  max-height: 400px;
  overflow-y: auto;
  background: #fafafa;
  border: 1px solid var(--ink-border);
  border-radius: 6px;
  padding: 12px;
  margin-top: 12px;
}
.stream-output pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
  font-size: 14px;
  line-height: 1.7;
}
</style>
