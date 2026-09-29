// services/apiV2.ts —— EyvesCloud API v2 客户端（集成专用，契约稳定）
//
// 设计要点（与 docs/API-V2.md 一致）：
//   - 统一信封 { success, code, message, data, request_id }
//   - 列表统一 data.items + data.pagination
//   - 失败时抛出带 code 的错误，调用方可按错误码分支
//
// 认证沿用面板凭据：浏览器内直接用登录会话（Cookie / 内存 token），
// 外部集成用 Bearer access_token 或 X-API-Key。

import axios, { AxiosError } from 'axios'

export const API_V2_BASE = '/api/v2'

export interface V2Envelope<T> {
  success: boolean
  code: string
  message?: string
  data?: T
  details?: Record<string, string>
  request_id?: string
}

export interface V2Pagination {
  page: number
  page_size: number
  total: number
  pages: number
  all?: boolean
}

export interface V2List<T> {
  items: T[]
  pagination: V2Pagination
  summary?: Record<string, number>
}

/** V2Error 携带稳定错误码，便于调用方分支处理（不解析 message 文案）。 */
export class V2Error extends Error {
  code: string
  status: number
  details?: Record<string, string>
  requestId?: string

  constructor(envelope: V2Envelope<unknown>, status: number) {
    super(envelope.message || envelope.code || 'API v2 调用失败')
    this.name = 'V2Error'
    this.code = envelope.code || 'UNKNOWN'
    this.status = status
    this.details = envelope.details
    this.requestId = envelope.request_id
  }
}

const client = axios.create({
  baseURL: API_V2_BASE,
  timeout: 30000,
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

// 请求拦截：若外部集成在本地存了 access_token，则带上（浏览器会话仍走 Cookie）
client.interceptors.request.use((config) => {
  const token = typeof window !== 'undefined' ? window.sessionStorage.getItem('eyvescloud_access_token') : null
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

async function call<T>(promise: Promise<{ data: V2Envelope<T>; status: number }>): Promise<T> {
  try {
    const response = await promise
    const envelope = response.data
    if (!envelope || envelope.success !== true) {
      throw new V2Error(envelope || { success: false, code: 'UNKNOWN' }, response.status)
    }
    return envelope.data as T
  } catch (error) {
    const axiosError = error as AxiosError<V2Envelope<unknown>>
    if (axiosError.response?.data) {
      throw new V2Error(axiosError.response.data, axiosError.response.status)
    }
    throw error
  }
}

// ---- 认证 ----

export interface V2TokenPayload {
  access_token: string
  token_type: string
  expires_in: number
  expires_at: string
  username: string
  type: 'admin' | 'client'
  role: string
}

export interface V2Me {
  type: string
  username: string
  role: string
  scopes: string[]
  features: Record<string, boolean>
  container_uuids?: string[]
  api_version: string
  panel_version: string
}

export const v2Login = (username: string, password: string, code?: string) =>
  call<V2TokenPayload>(client.post('/auth/login', { username, password, code }))

export const v2Logout = () => client.post('/auth/logout')

export const v2Me = () => call<V2Me>(client.get('/auth/me'))

// ---- 实例 ----

export interface V2Instance {
  id: number
  uuid: string
  name: string
  status: string
  runtime: string
  node_id: string
  node_name: string
  template_id: string
  vcpu: number
  memory_mb: number
  disk_gb: number
  primary_ip: string
  locked: boolean
  remark: string
  owner: string
  created_at: string
  expires_at: string
}

export interface V2InstanceQuery {
  page?: number
  page_size?: number
  status?: string
  runtime?: string
  node_id?: string
  owner?: string
  q?: string
  sort?: string
  order?: 'asc' | 'desc'
  all?: boolean
}

export const v2ListInstances = (query: V2InstanceQuery = {}) =>
  call<V2List<V2Instance>>(client.get('/instances', { params: query }))

export const v2GetInstance = (id: number | string) => call<Record<string, unknown>>(client.get(`/instances/${id}`))

export const v2CreateInstances = (payload: Record<string, unknown>) =>
  call<Record<string, unknown>>(client.post('/instances', payload))

export const v2InstancePower = (id: number | string, action: string) =>
  call<Record<string, unknown>>(client.post(`/instances/${id}/power`, { action }))

export const v2InstanceBatch = (payload: Record<string, unknown>) =>
  call<Record<string, unknown>>(client.post('/instances/batch', payload))

// ---- 节点与调度 ----

export interface V2Node {
  id: string
  name: string
  address: string
  status: string
  maintenance: boolean
  cpu_count?: number
  ram_total_mb?: number
  ram_used_mb?: number
  disk_total_gb?: number
  disk_used_gb?: number
  container_count?: number
  memory_used_percent?: number
}

export const v2ListNodes = (query: Record<string, unknown> = {}) =>
  call<V2List<V2Node>>(client.get('/nodes', { params: query }))

export interface V2ScheduleCandidate {
  node_id: string
  node_name?: string
  passed: boolean
  score: number
  filter_reasons?: string[]
  score_reasons?: string[]
}

export interface V2ScheduleResult {
  chosen?: V2Node
  reason?: string
  candidates: V2ScheduleCandidate[]
}

export const v2Schedule = (params: { ram_mb?: number; disk_gb?: number; virt?: string; node_priority?: number }) =>
  call<V2ScheduleResult>(client.get('/nodes/schedule', { params }))

// ---- 目录与运维 ----

export const v2ListImages = (query: Record<string, unknown> = {}) =>
  call<V2List<Record<string, unknown>>>(client.get('/images', { params: query }))

export const v2ListTasks = (query: Record<string, unknown> = {}) =>
  call<V2List<Record<string, unknown>>>(client.get('/tasks', { params: query }))

export const v2ListSecurityGroups = () =>
  call<V2List<Record<string, unknown>>>(client.get('/security-groups'))

export const v2MetricsSummary = () => call<Record<string, unknown>>(client.get('/metrics/summary'))
export const v2MetricsHost = () => call<Record<string, unknown>>(client.get('/metrics/host'))
export const v2SystemInfo = () => call<Record<string, unknown>>(client.get('/system/info'))
export const v2SystemHealth = () => call<Record<string, unknown>>(client.get('/system/health'))

export const v2ListAuditLogs = (query: Record<string, unknown> = {}) =>
  call<V2List<Record<string, unknown>>>(client.get('/audit-logs', { params: query }))

export const v2ListAPIKeys = () => call<V2List<Record<string, unknown>>>(client.get('/api-keys'))
