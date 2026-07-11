import { defineStore } from 'pinia'
import { ref } from 'vue'
import { request } from '@/api'

export interface LLMConfig {
  provider: string
  api_key: string
  base_url: string
  model: string
}

export interface SchedulerConfig {
  enabled: boolean
  max_concurrent: number
  interval_seconds: number
}

export interface DBConfig {
  path: string
  type: string
}

export interface SystemConfig {
  llm: LLMConfig
  default_model: string
  db: DBConfig
  scheduler: SchedulerConfig
}

export const useConfigStore = defineStore('config', () => {
  const config = ref<SystemConfig>({
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
  const loading = ref(false)

  async function fetchConfig() {
    loading.value = true
    try {
      const data = await request<SystemConfig>({
        url: '/config',
        method: 'get',
      })
      if (data) config.value = data
    } finally {
      loading.value = false
    }
  }

  async function saveConfig(payload: SystemConfig) {
    await request({ url: '/config', method: 'put', data: payload })
    config.value = payload
  }

  async function controlScheduler(action: 'start' | 'stop') {
    await request({
      url: '/scheduler/control',
      method: 'post',
      data: { action },
    })
    config.value.scheduler.enabled = action === 'start'
  }

  return {
    config,
    loading,
    fetchConfig,
    saveConfig,
    controlScheduler,
  }
})
