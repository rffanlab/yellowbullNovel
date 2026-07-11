<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import {
  ElButton,
  ElCard,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElOption,
  ElSelect,
  ElSwitch,
  ElDivider,
  ElMessage,
  ElDescriptions,
  ElDescriptionsItem,
  ElTag,
} from 'element-plus'
import { useConfigStore, type SystemConfig } from '@/stores/config'

const configStore = useConfigStore()
const form = reactive<SystemConfig>({
  llm: {
    provider: 'openai',
    api_key: '',
    base_url: 'https://api.openai.com/v1',
    model: 'gpt-4o',
  },
  default_model: 'gpt-4o',
  db: {
    path: './inkos.db',
    type: 'sqlite',
  },
  scheduler: {
    enabled: false,
    max_concurrent: 1,
    interval_seconds: 60,
  },
})

const saving = ref(false)
const schedulerLoading = ref(false)

const providers = [
  { label: 'OpenAI', value: 'openai' },
  { label: 'Anthropic Claude', value: 'anthropic' },
  { label: 'DeepSeek', value: 'deepseek' },
  { label: '通义千问', value: 'qwen' },
  { label: '智谱 GLM', value: 'zhipu' },
  { label: 'Moonshot Kimi', value: 'moonshot' },
  { label: '本地 Ollama', value: 'ollama' },
  { label: '自定义', value: 'custom' },
]

const dbTypes = [
  { label: 'SQLite', value: 'sqlite' },
  { label: 'PostgreSQL', value: 'postgres' },
  { label: 'MySQL', value: 'mysql' },
]

async function load() {
  try {
    await configStore.fetchConfig()
    Object.assign(form, configStore.config)
  } catch {
    // 使用默认值
  }
}

async function save() {
  saving.value = true
  try {
    await configStore.saveConfig(form)
    ElMessage.success('保存成功')
  } catch {
    // ignore
  } finally {
    saving.value = false
  }
}

async function toggleScheduler(val: string | number | boolean) {
  const enabled = Boolean(val)
  schedulerLoading.value = true
  try {
    await configStore.controlScheduler(enabled ? 'start' : 'stop')
    ElMessage.success(enabled ? '调度器已启动' : '调度器已停止')
    form.scheduler.enabled = enabled
  } catch {
    form.scheduler.enabled = !enabled
  } finally {
    schedulerLoading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="page-container settings-page">
    <h1 class="page-title">系统设置</h1>

    <!-- LLM 配置 -->
    <el-card class="section-card" v-loading="configStore.loading">
      <template #header>
        <span class="card-title">LLM 提供商配置</span>
      </template>
      <el-form :model="form.llm" label-width="120px">
        <el-form-item label="提供商">
          <el-select v-model="form.llm.provider">
            <el-option
              v-for="p in providers"
              :key="p.value"
              :label="p.label"
              :value="p.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="Base URL">
          <el-input
            v-model="form.llm.base_url"
            placeholder="https://api.openai.com/v1"
          />
        </el-form-item>
        <el-form-item label="API Key">
          <el-input
            v-model="form.llm.api_key"
            type="password"
            show-password
            placeholder="sk-..."
          />
        </el-form-item>
        <el-form-item label="模型">
          <el-input v-model="form.llm.model" placeholder="gpt-4o" />
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 默认模型 -->
    <el-card class="section-card">
      <template #header>
        <span class="card-title">默认模型设置</span>
      </template>
      <el-form :model="form" label-width="120px">
        <el-form-item label="默认模型">
          <el-input v-model="form.default_model" placeholder="gpt-4o" />
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 数据库配置 -->
    <el-card class="section-card">
      <template #header>
        <span class="card-title">数据库配置</span>
      </template>
      <el-form :model="form.db" label-width="120px">
        <el-form-item label="数据库类型">
          <el-select v-model="form.db.type">
            <el-option
              v-for="t in dbTypes"
              :key="t.value"
              :label="t.label"
              :value="t.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="数据库路径">
          <el-input
            v-model="form.db.path"
            placeholder="./inkos.db 或 postgres://..."
          />
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 调度器控制 -->
    <el-card class="section-card">
      <template #header>
        <div class="card-header-row">
          <span class="card-title">调度器控制</span>
          <el-tag
            :type="form.scheduler.enabled ? 'success' : 'info'"
            size="small"
          >
            {{ form.scheduler.enabled ? '运行中' : '已停止' }}
          </el-tag>
        </div>
      </template>
      <el-form :model="form.scheduler" label-width="120px">
        <el-form-item label="启用调度器">
          <el-switch
            v-model="form.scheduler.enabled"
            :loading="schedulerLoading"
            @change="toggleScheduler"
          />
        </el-form-item>
        <el-form-item label="最大并发">
          <el-input-number
            v-model="form.scheduler.max_concurrent"
            :min="1"
            :max="10"
          />
        </el-form-item>
        <el-form-item label="调度间隔(秒)">
          <el-input-number
            v-model="form.scheduler.interval_seconds"
            :min="10"
            :max="3600"
            :step="10"
          />
        </el-form-item>
      </el-form>
    </el-card>

    <el-divider />

    <!-- 当前运行状态 -->
    <el-card class="section-card">
      <template #header>
        <span class="card-title">当前运行状态</span>
      </template>
      <el-descriptions :column="2" border>
        <el-descriptions-item label="LLM 提供商">
          {{ form.llm.provider }}
        </el-descriptions-item>
        <el-descriptions-item label="当前模型">
          {{ form.llm.model }}
        </el-descriptions-item>
        <el-descriptions-item label="数据库类型">
          {{ form.db.type }}
        </el-descriptions-item>
        <el-descriptions-item label="调度器状态">
          <el-tag :type="form.scheduler.enabled ? 'success' : 'info'" size="small">
            {{ form.scheduler.enabled ? '运行中' : '已停止' }}
          </el-tag>
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <div class="footer-actions">
      <el-button type="primary" :loading="saving" @click="save">
        保存配置
      </el-button>
    </div>
  </div>
</template>

<style scoped>
.settings-page {
  max-width: 760px;
}
.section-card {
  margin-bottom: 16px;
}
.card-title {
  font-weight: 600;
}
.card-header-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.footer-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
