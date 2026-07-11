import axios, { AxiosError, type AxiosInstance, type AxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'

// 统一后端响应结构
export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
}

const baseURL = import.meta.env.VITE_API_BASE || '/api'

const instance: AxiosInstance = axios.create({
  baseURL,
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
})

// 请求拦截器
instance.interceptors.request.use(
  (config) => {
    // 如有 token 可在此注入
    return config
  },
  (error) => Promise.reject(error)
)

// 响应拦截器：统一解包 ApiResponse
instance.interceptors.response.use(
  (response) => {
    const body = response.data
    // 兼容直接返回非标准结构的接口
    if (body && typeof body === 'object' && 'code' in body) {
      if (body.code !== 0 && body.code !== 200) {
        ElMessage.error(body.message || '请求失败')
        return Promise.reject(new Error(body.message || '请求失败'))
      }
      return body.data
    }
    return body
  },
  (error: AxiosError<{ message?: string }>) => {
    const msg =
      error.response?.data?.message ||
      error.message ||
      '网络错误，请稍后重试'
    ElMessage.error(msg)
    return Promise.reject(error)
  }
)

export function request<T = unknown>(config: AxiosRequestConfig): Promise<T> {
  return instance.request<unknown, T>(config)
}

export default instance
