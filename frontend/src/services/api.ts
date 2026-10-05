import axios from 'axios'
import { adminUrl } from './panelPath'

// 审计 H-6：会话令牌不再写入 localStorage（XSS 可直接窃取 24h 管理令牌）。
// 令牌由服务端以 HttpOnly Cookie 下发，浏览器自动携带；这里仅在内存里保留
// 一份（登录响应里的 token），供需要在请求头显式带 Bearer 的场景使用。
let inMemoryToken: string | null = null

export function setAuthToken(token: string | null) {
  inMemoryToken = token
}

export function getAuthToken(): string | null {
  return inMemoryToken
}

const api = axios.create({
  baseURL: '/api',
  timeout: 30000,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
})

// Request interceptor to add auth token
api.interceptors.request.use((config) => {
  const token = inMemoryToken
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// 登出时通知服务端清除 HttpOnly Cookie。
export async function logoutRequest(): Promise<void> {
  try {
    await api.post('/logout')
  } catch {
    // 忽略：本地状态照常清理
  }
}

// Response interceptor to handle auth errors
api.interceptors.response.use(
  (response) => response,
  (error) => {
    const requestURL = String(error.config?.url || '')
    const isLoginRequest = ['/login', '/sub-user/login', '/sub-user/access']
      .some((path) => requestURL === path || requestURL.endsWith(path))
    if (error.response?.status === 401 && !isLoginRequest) {
      inMemoryToken = null
      // 管理端登录页可能挂在自定义入口路径下；用户门户固定在 /user。
      // 依据当前所在门户选择正确的登录页，避免跳错门户导致重复登录失败。
      const loginPath = window.location.pathname.startsWith('/user')
        ? '/user/login'
        : adminUrl('login') || '/login'
      if (window.location.pathname !== loginPath) {
        window.location.href = loginPath
      }
    }
    return Promise.reject(error)
  }
)

export interface LoginResponse {
  token: string
  username: string
}

export type ContainerIdentifier = number | string

export interface PortMapping {
  container_port: number
  host_port: number
  host_ip?: string
  protocol: string
  description: string
}

export interface FirewallRule {
  id: string
  network?: 'ipv4' | 'ipv6' | 'all'
  direction: 'in' | 'out'
  protocol: 'tcp' | 'udp' | 'icmp' | 'all'
  port: string
  source_ip: string
  action: 'ACCEPT' | 'DROP'
  description: string
  enabled: boolean
}

export interface PublicIPv4Assignment {
  address: string
  interface?: string
  prefix_len?: number
  gateway?: string
  rdns?: string
}

export interface IPv6Assignment {
  address: string
  prefix_len: number
  interface?: string
  rdns?: string
}

export interface ReverseDNSRecord {
  address: string
  family: 'ipv4' | 'ipv6'
  hostname: string
}

export interface Container {
  id: number
  uuid: string
  name: string
  virtualization?: string
  storage_pool_id?: string
  storage_path?: string
  template: string
  vcpu: number
  ram_mb: number
  disk_gb: number
  network_bw_mbps: number
  network_down_mbps: number
  network_up_mbps: number
  monthly_traffic_gb: number
  traffic_mode: string
  traffic_in_gb: number
  traffic_out_gb: number
  traffic_used_rx: number
  traffic_used_tx: number
  traffic_reset_date: string
  io_speed_mbps: number
  io_read_mbps: number
  io_write_mbps: number
  status: string
  ip: string
  lan_ipv4_mode?: string
  lan_interface?: string
  lan_ipv4_address?: string
  lan_ipv4_prefix_len?: number
  lan_ipv4_gateway?: string
  mac_address?: string
  public_ipv4s?: PublicIPv4Assignment[]
  ipv6: string
  ipv6_prefix_len: number
  ipv6_interface: string
  ipv6_addresses?: IPv6Assignment[]
  vnc_port: number
  ssh_port: number
  ssh_password: string
  port_mappings: PortMapping[]
  port_mapping_limit: number
  firewall_enabled: boolean
  firewall_default_action: 'ACCEPT' | 'DROP'
  firewall_rules: FirewallRule[]
  snapshot_limit: number
  tenant?: string
  created_at: string
  expires_at: string
  snapshot_schedule_enabled: boolean
  snapshot_schedule_interval_hours: number
  snapshot_schedule_time: string
  snapshot_schedule_last_run: string
  snapshot_schedule_next_run: string
  snapshot_schedule_created_by: string
  policy_blocked?: boolean
  policy_blocked_reason?: string
  policy_blocked_at?: string
  cloud_init_user_data?: string
  rescue_enabled?: boolean
  rescue_iso_id?: string
  rescue_iso_path?: string
  suspended?: boolean
  suspended_reason?: string
  suspended_at?: string
  owner_sub_user_id?: string
  /** 属主用户名（后端派生下发，免去前端拉取全量子用户） */
  owner_username?: string
  node_id?: string
  /** 客户接入端点（后端计算：节点容器指向所属节点，本机容器指向面板） */
  access_host?: string
  access_ssh_port?: number
  access_via?: 'node' | 'panel'
}

export interface Template {
  id: string
  name: string
  type?: string
  distro: string
  release: string
  arch: string
  variant?: string
  desktop?: string
  description: string
}

export interface CreateContainerRequest {
  name: string
  virtualization: string
  template_id: string
  storage_pool_id?: string
  vcpu: number
  cpu_percent: number
  ram_mb: number
  disk_gb: number
  data_disk_gb?: number
  data_disk_mount_path?: string
  network_bw_mbps: number
  network_down_mbps: number
  network_up_mbps: number
  monthly_traffic_gb: number
  traffic_mode: string
  traffic_in_gb: number
  traffic_out_gb: number
  io_speed_mbps: number
  io_read_mbps: number
  io_write_mbps: number
  extra_ports: number[]
  nat_port_mappings?: PortMapping[]
  management_port?: number
  port_mapping_count: number
  assign_nat?: boolean
  lan_ipv4_mode?: string
  lan_interface?: string
  lan_ipv4_address?: string
  lan_ipv4_prefix_len?: number
  lan_ipv4_gateway?: string
  snapshot_limit: number
  assign_ipv4?: boolean
  ipv4_count?: number
  public_ipv4s?: string[]
  assign_ipv6: boolean
  ipv6_count?: number
  ipv6_addresses?: string[]
  ssh_auth_mode?: string
  ssh_password?: string
  /** 客户接入端点（后端计算：节点容器指向所属节点，本机容器指向面板） */
  access_host?: string
  access_ssh_port?: number
  access_via?: 'node' | 'panel'
  ssh_public_key?: string
  allowed_image_ids?: string[]
  image_limit_configured?: boolean
  cloud_init_user_data?: string
  expires_at: string
}

export interface StoragePool {
  id: string
  name: string
  path: string
  content_types: string[]
  default_contents?: string[]
  enabled: boolean
  available?: boolean
  exists?: boolean
  size_bytes?: number
  used_bytes?: number
  free_bytes?: number
  mount_point?: string
  eyvescloud_used_bytes?: number
  content_usage?: StorageContentUsage[]
  error?: string
}

export interface StorageContentUsage {
  content_type: string
  size_bytes: number
}

export interface StorageDisk {
  name: string
  path: string
  type: string
  fstype: string
  mount_point: string
  model: string
  size_bytes: number
  used_bytes: number
  free_bytes: number
  storage_pool_id?: string
  storage_path?: string
  eyvescloud_used_bytes?: number
  content_usage?: StorageContentUsage[]
}

export interface StorageInfo {
  pools: StoragePool[]
  disks: StorageDisk[]
  content_types: string[]
}

export interface ReinstallContainerOptions {
  ssh_auth_mode?: string
  ssh_password?: string
  ssh_public_key?: string
  // system: 只重装系统盘（保留数据盘）；full: 全盘重装
  reinstall_mode?: 'system' | 'full'
}

export interface IPv6PrefixInfo {
  interface: string
  address: string
  prefix: string
  prefix_len: number
  gateway: string
  is_tunnel?: boolean
  source?: string
}

export interface IPv6Status {
  available: boolean
  reachable: boolean
  reason: string
  prefixes: IPv6PrefixInfo[]
}

export interface PublicIPv4Info {
  interface: string
  address: string
  prefix: string
  prefix_len?: number
  subnet_mask?: string
  gateway?: string
  is_tunnel?: boolean
  source?: string
}

export interface IPv4PrefixInfo {
  interface: string
  address: string
  prefix: string
  prefix_len: number
  subnet_mask: string
  gateway: string
  source: string
}

export interface DashboardStats {
  total_containers: number
  running: number
  stopped: number
  suspended?: number
  nodes_total?: number
  nodes_online?: number
}

export interface HostInfo {
  cpu: { cores: number; usage_pct: number }
  ram: { total_mb: number; used_mb: number; free_mb: number }
  disk: { total_gb: number; used_gb: number; free_gb: number }
  network: {
    rx_bytes: number
    tx_bytes: number
    rx_bps: number
    tx_bps: number
    public_ipv4?: string
    public_ipv4_interface?: string
    public_ipv4_addresses?: PublicIPv4Info[]
    public_ipv6?: string
    public_ipv6_interface?: string
    ipv6_prefixes?: IPv6PrefixInfo[]
  }
  disk_io: { read_bytes: number; write_bytes: number; read_bps: number; write_bps: number }
  load: { load1: number; load5: number; load15: number }
  runtime?: {
    lxc_available: boolean
    kvm_available: boolean
    dev_kvm: boolean
    nested_virtualization: boolean
    nested_detail: string
    support_mode: string
  }
}

export interface CreateSnapshotOptions {
  storage_pool_id?: string
}

export interface HostMetricPoint {
  ts: number
  cpu: number
  memory: number
  network: number
  network_rx: number
  network_tx: number
  disk_io: number
  disk_read: number
  disk_write: number
  disk_usage_pct: number
}

export interface HostProbeReport {
  generated_at: string
  hostname: string
  kernel: string
  os: string
  cpu: {
    model: string
    cores: number
    threads: number
    architecture: string
    flags: string[]
    has_integrated_gpu: boolean
    virtualization: boolean
    virtualization_key: string
  }
  memory: {
    total_mb: number
    used_mb: number
    free_mb: number
    modules: Array<{
      locator: string
      size: string
      type: string
      speed: string
      manufacturer: string
      part_number: string
      serial_number: string
    }>
  }
  disks: Array<{
    name: string
    path: string
    model: string
    serial: string
    size_bytes: number
    type: string
    virtual?: boolean
    rotational: boolean
    mountpoints: string[]
    health: string
    health_detail: string
    smart?: {
      available: boolean
      life_used_percent?: number
      power_on_hours?: number
      power_cycle_count?: number
      read_data_bytes?: number
      written_data_bytes?: number
      read_commands?: number
      write_commands?: number
      wear_leveling_count?: string
      erase_count?: string
      media_errors?: number
    }
  }>
  network_interfaces: Array<{
    name: string
    mac: string
    state: string
    speed_mbps: number
    driver: string
    model: string
    ipv4: Array<{ interface: string; address: string; prefix_len: number; scope: string; gateway?: string }>
    ipv6: Array<{ interface: string; address: string; prefix_len: number; scope: string; gateway?: string }>
  }>
  public_ipv4: string[]
  ipv4_addresses: Array<{ interface: string; address: string; prefix_len: number; scope: string; gateway?: string }>
  ipv4_prefixes: IPv4PrefixInfo[]
  ipv6_addresses: Array<{ interface: string; address: string; prefix_len: number; scope: string; gateway?: string }>
  ipv6_prefixes: IPv6PrefixInfo[]
  gateways: Array<{ family: string; interface: string; gateway: string }>
  gpus: Array<{ name: string; vendor: string; driver: string; type: string }>
  runtime: {
    lxc_available: boolean
    kvm_available: boolean
    dev_kvm: boolean
    nested_virtualization: boolean
    nested_detail: string
    support_mode: string
  }
  system: {
    uptime_seconds: number
    uptime_text: string
    process_count: number
  }
  environment: Array<{ key: string; label: string; ok: boolean; required: boolean; detail: string }>
}

export interface ContainerUsage {
  memory_usage_bytes: number
  memory_total_bytes?: number
  cpu_usage_usec: number
  cpu_usage_pct: number
  disk_usage_bytes: number
  network_rx_bytes: number
  network_tx_bytes: number
  network_rx_bps: number
  network_tx_bps: number
  disk_read_bytes: number
  disk_write_bytes: number
  disk_read_bps: number
  disk_write_bps: number
  load1: number
  load5: number
  load15: number
  guest_metrics?: boolean
}

export interface ContainerMetricPoint {
  ts: number
  cpu: number
  memory: number
  network: number
  network_rx: number
  network_tx: number
  disk_io: number
  disk_read: number
  disk_write: number
}

export interface APIResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

// Auth
export const login = (username: string, password: string, twofaCode?: string, turnstileToken?: string) =>
  api.post<APIResponse<LoginResponse>>('/login', {
    username,
    password,
    twofa_code: twofaCode ?? '',
    turnstile_token: turnstileToken ?? '',
  })

export const checkAuth = () =>
  api.get<APIResponse>('/check-auth')

export const changePassword = (oldPassword: string, newPassword: string) =>
  api.post<APIResponse>('/change-password', { old_password: oldPassword, new_password: newPassword })

// ---- 子用户自助（用户门户「安全设置」）----

export interface SubUserProfile {
  username: string
  email?: string
  role: string
  access_code: string
  container_count: number
}

export const getSubUserProfile = () =>
  api.get<APIResponse<SubUserProfile>>('/sub-user/profile')

// 自助轮换：服务端生成随机安全密码并一次性返回（TokenVersion++ 强制重新登录）。
export const selfRotatePassword = (oldPassword: string) =>
  api.post<APIResponse<{ password: string }>>('/sub-user/rotate-password', { old_password: oldPassword })

export const changeUsername = (newUsername: string, password: string) =>
  api.post<APIResponse>('/change-username', { new_username: newUsername, password })

// Two-Factor Authentication (TOTP)
export interface TwoFAStatus {
  enabled: boolean
  has_secret: boolean
}

export interface TwoFASetupResult {
  secret: string
  otpauth_uri: string
  qr_data_url?: string
}

export const get2FAStatus = () =>
  api.get<APIResponse<TwoFAStatus>>('/2fa/status')

export const setup2FA = () =>
  api.post<APIResponse<TwoFASetupResult>>('/2fa/setup')

export const enable2FA = (code: string, backupCount = 8) =>
  api.post<APIResponse<{ backup_codes: string[] }>>('/2fa/enable', { code, backup_codes_count: backupCount })

export const disable2FA = (code: string) =>
  api.post<APIResponse>('/2fa/disable', { code })

export const regenerate2FABackupCodes = (code: string, backupCount = 8) =>
  api.post<APIResponse<{ backup_codes: string[] }>>('/2fa/regenerate-backup-codes', { code, backup_codes_count: backupCount })

// Login Logs
export interface LoginLog {
  time: string
  username: string
  ip: string
  user_agent: string
  success: boolean
}

export interface AuditLog {
  time: string
  action: string
  target: string
  detail: string
  user: string
}

export const getLoginLogs = () =>
  api.get<APIResponse<LoginLog[]>>('/login-logs')

// 企业化：审计合规（导出 / 保留期）
export interface AuditSettings {
  retention_days: number
  audit_log_count?: number
  retention_notes?: string
}

export const getAuditSettings = () =>
  api.get<APIResponse<AuditSettings>>('/audit/settings')

export const updateAuditSettings = (retention_days: number) =>
  api.put<APIResponse<AuditSettings>>('/audit/settings', { retention_days })

// 企业化：容灾恢复（配置备份）
export interface BackupSettings {
  enabled: boolean
  interval_hours: number
  keep: number
  directory?: string
  last_backup_at?: string
  last_backup_file?: string
  backup_count?: number
}

export interface BackupRecord {
  id: string
  filename: string
  size_bytes: number
  kind: string
  created_at: string
}

export const getBackupSettings = () =>
  api.get<APIResponse<BackupSettings>>('/backup/settings')

export const updateBackupSettings = (settings: { enabled: boolean; interval_hours: number; keep: number }) =>
  api.put<APIResponse<BackupSettings>>('/backup/settings', settings)

export const createBackup = () =>
  api.post<APIResponse<BackupRecord>>('/backup')

export const getBackupList = () =>
  api.get<APIResponse<BackupRecord[]>>('/backup/list')

export const restoreBackup = (file: string, confirm: boolean) =>
  api.post<APIResponse>('/backup/restore', { file, confirm })

// 企业化：可观测性（健康检查）
export interface HealthDetail {
  status: string
  version: string
  uptime: number
  go_version: string
  goroutines: number
  memory_alloc_mb: number
  container_count: number
  node_count: number
  subuser_count: number
  active_tasks: number
  cpu_cores: number
}

export const getHealthDetail = () =>
  api.get<APIResponse<HealthDetail>>('/health/detail')

// 企业化：API 治理（限流 / 契约）
export interface RateLimitSettings {
  enabled: boolean
  per_minute: number
  scope?: string
}

export const getRateLimitSettings = () =>
  api.get<APIResponse<RateLimitSettings>>('/rate-limit/settings')

export const updateRateLimitSettings = (enabled: boolean, per_minute: number) =>
  api.put<APIResponse<RateLimitSettings>>('/rate-limit/settings', { enabled, per_minute })

// 企业化：多租户
export interface Tenant {
  id: string
  name: string
  description?: string
  container_quota: number
  vcpu_quota: number
  ram_quota_mb: number
  disk_quota_gb: number
  enabled: boolean
  created_at?: string
  usage_containers?: number
  usage_vcpu?: number
  usage_ram_mb?: number
  usage_disk_gb?: number
}

export const getTenants = () =>
  api.get<APIResponse<Tenant[]>>('/tenants')

export const createTenant = (tenant: Partial<Tenant>) =>
  api.post<APIResponse>('/tenants', tenant)

export const updateTenant = (id: string, tenant: Partial<Tenant>) =>
  api.put<APIResponse>(`/tenants/${id}`, tenant)

export const deleteTenant = (id: string) =>
  api.delete<APIResponse>(`/tenants/${id}`)

// ---- 批次3：区域 / IP 组 / ISO ----
export interface Region {
  id: string
  name: string
  location?: string
  created_at?: string
}

export interface IPGroup {
  id: string
  name: string
  fault_open?: string
  enable?: string[]
  standby?: string[]
  created_at?: string
}

export interface ISOFile {
  id: string
  name: string
  path: string
  size_bytes?: number
  os?: string
  created_at?: string
}

export interface MetricRetentionSettings {
  retention_days: number
  sample_interval_secs?: number
  raw_history_secs?: number
  retention_notes?: string
}

export const getRegions = () =>
  api.get<APIResponse<Region[]>>('/regions')

export const createRegion = (r: { name: string; location?: string }) =>
  api.post<APIResponse<Region>>('/regions', r)

export const deleteRegion = (id: string) =>
  api.delete<APIResponse>(`/regions/${id}`)

export const getIPGroups = () =>
  api.get<APIResponse<IPGroup[]>>('/ip-groups')

export const createIPGroup = (g: { name: string; enable?: string[]; standby?: string[] }) =>
  api.post<APIResponse<IPGroup>>('/ip-groups', g)

export const updateIPGroup = (id: string, g: { name?: string; enable?: string[]; standby?: string[] }) =>
  api.put<APIResponse>(`/ip-groups/${id}`, g)

export const deleteIPGroup = (id: string) =>
  api.delete<APIResponse>(`/ip-groups/${id}`)

export const ipGroupFailover = (id: string, ip: string) =>
  api.post<APIResponse<IPGroup>>(`/ip-groups/${id}/failover`, { ip })

export const getISOs = () =>
  api.get<APIResponse<ISOFile[]>>('/isos')

export const createISO = (r: { name: string; url: string; os?: string }) =>
  api.post<APIResponse<ISOFile>>('/isos', r)

export const uploadISO = (file: File, name: string, os?: string) => {
  const fd = new FormData()
  fd.append('file', file)
  if (name) fd.append('name', name)
  if (os) fd.append('os', os)
  // axios 会为 FormData 自动生成 multipart boundary，无需手动指定 Content-Type
  return api.post<APIResponse<ISOFile>>('/isos/upload', fd, { timeout: 0 })
}

export const deleteISO = (id: string) =>
  api.delete<APIResponse>(`/isos/${id}`)

export const containerISOAction = (containerId: number, isoId: string, attach: boolean) =>
  api.post<APIResponse>(`/isos/attach`, { container_id: containerId, iso_id: isoId, attach })

export const containerRescue = (containerId: number, enabled: boolean, isoId?: string) =>
  api.post<APIResponse>(`/containers/rescue`, { container_id: containerId, enabled, iso_id: isoId })

export const getMetricRetention = () =>
  api.get<APIResponse<MetricRetentionSettings>>('/metrics/retention')

export const setMetricRetention = (retentionDays: number) =>
  api.put<APIResponse<{ retention_days: number }>>('/metrics/retention', { retention_days: retentionDays })

export interface TaskQueueSettings {
  concurrency: number
  active: number
  pending: number
}

export const getTaskQueueSettings = () =>
  api.get<APIResponse<TaskQueueSettings>>('/task-queue/settings')

export const updateTaskQueueSettings = (concurrency: number) =>
  api.put<APIResponse<TaskQueueSettings>>('/task-queue/settings', { concurrency })

export interface OvercommitSettings {
  memory_overcommit_enabled: boolean
  memory_overcommit_ratio: number
  physical_ram_mb: number
  allocatable_ram_mb: number
  nat_subnet_oversubscription: boolean
  disk_overcommit_ratio: number
  physical_disk_gb: number
  disk_allocatable_gb: number
  ksm_tuning: {
    enabled: boolean
    pages_to_scan: number
    sleep_millisecs: number
    use_tune_ksm: boolean
  }
  notes: string
}

export const getOvercommitSettings = () =>
  api.get<APIResponse<OvercommitSettings>>('/overcommit/settings')

export const updateOvercommitSettings = (payload: {
  memory_overcommit_enabled: boolean
  memory_overcommit_ratio: number
  nat_subnet_oversubscription: boolean
  disk_overcommit_ratio: number
  ksm_tuning: { enabled: boolean; pages_to_scan: number; sleep_millisecs: number; use_tune_ksm: boolean }
}) =>
  api.put<APIResponse<OvercommitSettings>>('/overcommit/settings', payload)

export interface SSLCertificateInfo {
  subject: string
  issuer: string
  dns_names: string[]
  ip_names: string[]
  not_before: string
  not_after: string
  valid: boolean
}

export interface SSLSettings {
  enabled: boolean
  mode: 'disabled' | 'letsencrypt' | 'self_signed' | 'uploaded'
  target: string
  email?: string
  cert_path?: string
  key_path?: string
  last_issued_at?: string
  last_error?: string
  detected_host?: string
  certificate?: SSLCertificateInfo
  mode_certificates?: Record<string, SSLSettings>
  needs_restart?: boolean
  // 启用 TLS 后额外监听一个 HTTP 端口做 301 跳转到 HTTPS；
  // 0 或未设置 = 不启用（既有 HTTP 访问会在启用 TLS 后失效）。
  http_redirect_port?: number
}

export interface UpdateSSLSettingsRequest {
  enabled: boolean
  mode: 'disabled' | 'letsencrypt' | 'self_signed' | 'uploaded'
  target?: string
  email?: string
  cert_pem?: string
  key_pem?: string
  apply_now?: boolean
  http_redirect_port?: number
}

export const getSSLSettings = () =>
  api.get<APIResponse<SSLSettings>>('/ssl')

export const updateSSLSettings = (data: UpdateSSLSettingsRequest) =>
  api.put<APIResponse<SSLSettings>>('/ssl', data)

export interface WebSSHOriginSettings {
  origins: string[]
  current_origin?: string
}

export const getWebSSHOriginSettings = () =>
  api.get<APIResponse<WebSSHOriginSettings>>('/webssh-origins')

export const updateWebSSHOriginSettings = (origins: string[]) =>
  api.put<APIResponse<WebSSHOriginSettings>>('/webssh-origins', { origins })

export interface PanelAccessPolicy {
  enabled: boolean
  allowed_sources: string[]
  trusted_proxies: string[]
  current_source: string
  direct_source: string
  using_forwarded: boolean
}

export const getPanelAccessPolicy = () =>
  api.get<APIResponse<PanelAccessPolicy>>('/access-policy')

export const updatePanelAccessPolicy = (data: Pick<PanelAccessPolicy, 'enabled' | 'allowed_sources' | 'trusted_proxies'>) =>
  api.put<APIResponse<PanelAccessPolicy>>('/access-policy', data)

// 管理员入口路径（可自定义，用于隐藏管理入口；用户门户固定 /user）
export interface AdminPathSettings {
  admin_path: string
  user_path: string
}

export const getAdminPath = () =>
  api.get<APIResponse<AdminPathSettings>>('/admin-path')

export const updateAdminPath = (adminPath: string) =>
  api.put<APIResponse<AdminPathSettings>>('/admin-path', { admin_path: adminPath })

// 登录页底部版权栏（自定义文字/隐藏；GET 公开，PUT 仅管理员）
export interface LoginFooterSettings {
  text: string
  hidden: boolean
}

export const getLoginFooter = () =>
  api.get<APIResponse<LoginFooterSettings>>('/login-footer')

export const updateLoginFooter = (data: LoginFooterSettings) =>
  api.put<APIResponse<LoginFooterSettings>>('/login-footer', data)

// 面板绑定域名（对外 URL 基准：节点安装命令、agent controller 等）
export interface PanelDomainSettings {
  panel_domain: string
}

export const getPanelDomain = () =>
  api.get<APIResponse<PanelDomainSettings>>('/panel-domain')

export const updatePanelDomain = (panelDomain: string) =>
  api.put<APIResponse<PanelDomainSettings>>('/panel-domain', { panel_domain: panelDomain })

// 节点接入（被控面板上的主控注册信息：查看 / 切换主控 / 重启 agent 服务）
export interface AgentRegistration {
  registered: boolean
  controller: string
  node_id: string
  token: string
  name: string
  address: string
  allow_insecure_http: boolean
  pairing_key: string
  pairing_key_expiry: string
  pairing_key_valid: boolean
}

export const getAgentRegistration = () =>
  api.get<APIResponse<AgentRegistration>>('/agent/status')

export const generateAgentPairingKey = () =>
  api.post<APIResponse<{ pairing_key: string; expiry: string }>>('/agent/pairing-key')

export const registerAgentController = (data: {
  controller: string
  install_key: string
  name?: string
  address?: string
  allow_insecure_http: boolean
}) =>
  api.post<APIResponse<{ restart_required: boolean }>>('/agent/register', data)

export const restartAgentService = () =>
  api.post<APIResponse<{ restart_initiated: boolean }>>('/agent/restart')

// 主控侧「对接已有面板」：凭被控面板生成的对接密钥主动拉取注册。
export const adoptExistingPanel = (data: {
  panel_url: string
  pairing_key: string
  name?: string
  allow_private: boolean
}) =>
  api.post<APIResponse<{ node: { id: string; name: string } }>>('/nodes/adopt', data)

// Cloudflare Turnstile 人机验证
export interface TurnstilePublicConfig {
  site_key: string
  admin_enabled: boolean
  user_enabled: boolean
}

export interface TurnstileSettings {
  site_key: string
  /** 打码回显（如 0x4A********）；仅用于识别是否已配置/是否更换 */
  secret_key: string
  has_secret: boolean
  admin_enabled: boolean
  user_enabled: boolean
}

export const getTurnstileConfig = () =>
  api.get<APIResponse<TurnstilePublicConfig>>('/turnstile/config')

export const getTurnstileSettings = () =>
  api.get<APIResponse<TurnstileSettings>>('/turnstile/settings')

export const updateTurnstileSettings = (data: {
  site_key: string
  /** 留空 = 保留已保存的 Secret Key */
  secret_key?: string
  clear_secret?: boolean
  admin_enabled: boolean
  user_enabled: boolean
}) => api.put<APIResponse<TurnstileSettings>>('/turnstile/settings', data)

/** 用「请求内携带或已保存」的密钥对做 siteverify 冒烟测试（先测后存） */
export const testTurnstile = (data: { token: string; site_key?: string; secret_key?: string }) =>
  api.post<APIResponse>('/turnstile/verify', data)

// 外部告警推送
export interface NotificationSettings {
  security_alerts_enabled: boolean
  min_severity: string
  webhook_url?: string
  smtp_enabled: boolean
  smtp_server?: string
  smtp_port: number
  smtp_user?: string
  smtp_from?: string
  smtp_to?: string
  smtp_password_set?: boolean
}

export const getNotificationSettings = () =>
  api.get<APIResponse<NotificationSettings>>('/notifications')

export const updateNotificationSettings = (data: Partial<NotificationSettings> & { smtp_password?: string }) =>
  api.put<APIResponse<NotificationSettings>>('/notifications', data)

export const testNotification = () =>
  api.post<APIResponse>('/notifications/test')

// 租户
export const updateContainerTenant = (id: string, tenant: string) =>
  api.put<APIResponse<{ tenant: string }>>(`/containers/${id}/tenant`, { tenant })

export const updateSubUserTenant = (subUserId: string, tenant: string) =>
  api.put<APIResponse>(`/sub-users/${subUserId}/tenant`, { tenant })

// Containers
export const getContainers = () =>
  api.get<APIResponse<Container[]>>('/containers')

// 分页信封（企业级列表契约）：未传 page/page_size 时后端仍返回全量数组。
export interface PagedList<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface ContainerListQuery {
  page?: number
  page_size?: number
  search?: string
  type?: string
  system?: string
  status?: string
  tenant?: string
  owner?: string
  node?: string
  sort?: string
  order?: 'asc' | 'desc'
}

export interface ContainerFilterOptions {
  systems?: { value: string; label: string }[]
  tenants?: string[]
}

export interface ContainerListPage extends PagedList<Container> {
  filter_options?: ContainerFilterOptions
}

export const getContainersPaged = (params: ContainerListQuery = {}) =>
  api.get<APIResponse<ContainerListPage>>('/containers', { params })

export const getContainer = (id: ContainerIdentifier) =>
  api.get<APIResponse<Container>>(`/containers/${id}`)

export const createContainer = (data: CreateContainerRequest) =>
  api.post<APIResponse>('/containers', data)

export const deleteContainer = (id: ContainerIdentifier) =>
  api.delete<APIResponse>(`/containers/${id}/delete`)

export const startContainer = (id: ContainerIdentifier) =>
  api.post<APIResponse>(`/containers/${id}/start`)

export const stopContainer = (id: ContainerIdentifier) =>
  api.post<APIResponse>(`/containers/${id}/stop`)

export const restartContainer = (id: ContainerIdentifier) =>
  api.post<APIResponse>(`/containers/${id}/restart`)
// 欠费停机（挂起/恢复）：suspend=停机+标记（开机被拦直至 unsuspend）。管理员操作。
export const suspendContainer = (id: ContainerIdentifier) =>
  api.post<APIResponse>(`/containers/${id}/suspend`)
export const unsuspendContainer = (id: ContainerIdentifier) =>
  api.post<APIResponse>(`/containers/${id}/unsuspend`)

export const reinstallContainer = (id: ContainerIdentifier, templateId: string, options?: ReinstallContainerOptions) =>
  api.post<APIResponse>(`/containers/${id}/reinstall`, { template_id: templateId, ...(options || {}) })

export const resetSSHPassword = (id: ContainerIdentifier, password?: string) =>
  api.post<APIResponse<{ password: string }>>(`/containers/${id}/reset-password`, password ? { password } : {})

export const getContainerUsage = (id: ContainerIdentifier) =>
  api.get<APIResponse<ContainerUsage>>(`/containers/${id}/usage`)

export const getContainerHistory = (id: ContainerIdentifier) =>
  api.get<APIResponse<ContainerMetricPoint[]>>(`/containers/${id}/history`)

export interface TrafficInfo {
  total_used_bytes: number
  rx_used_bytes: number
  tx_used_bytes: number
  mode: string
  limit_gb: number
  in_limit_gb: number
  out_limit_gb: number
  used_pct: number
  reset_date: string
}

export const getTrafficInfo = (id: ContainerIdentifier) =>
  api.get<APIResponse<TrafficInfo>>(`/containers/${id}/traffic`)

export const resetTraffic = (id: ContainerIdentifier) =>
  api.post<APIResponse>(`/containers/${id}/traffic-reset`)

export const updateTrafficLimit = (id: ContainerIdentifier, data: {
  traffic_mode: string
  monthly_traffic_gb: number
  traffic_in_gb: number
  traffic_out_gb: number
}) =>
  api.put<APIResponse>(`/containers/${id}/traffic-limit`, data)

export const updateResourceLimit = (id: ContainerIdentifier, data: {
  vcpu: number
  ram_mb: number
  io_speed_mbps?: number
  io_read_mbps?: number
  io_write_mbps?: number
  network_bw_mbps?: number
  network_down_mbps?: number
  network_up_mbps?: number
}) =>
  api.put<APIResponse>(`/containers/${id}/resource-limit`, data)

export const addPortMapping = (id: ContainerIdentifier, data: PortMapping) =>
  api.post<APIResponse<PortMapping[]>>(`/containers/${id}/port-mappings`, data)

export const updatePortMapping = (id: ContainerIdentifier, index: number, data: PortMapping) =>
  api.put<APIResponse<PortMapping[]>>(`/containers/${id}/port-mappings/${index}`, data)

export const deletePortMapping = (id: ContainerIdentifier, index: number) =>
  api.delete<APIResponse<PortMapping[]>>(`/containers/${id}/port-mappings/${index}`)

export const getFirewall = (id: ContainerIdentifier) =>
  api.get<APIResponse<{ enabled: boolean; default_action: 'ACCEPT' | 'DROP'; rules: FirewallRule[] }>>(`/containers/${id}/firewall`)

export const updateFirewall = (id: ContainerIdentifier, data: { enabled?: boolean; default_action?: 'ACCEPT' | 'DROP'; rules?: FirewallRule[] }) =>
  api.put<APIResponse<{ enabled: boolean; default_action: 'ACCEPT' | 'DROP'; rules: FirewallRule[] }>>(`/containers/${id}/firewall`, data)

export const getReverseDNS = (id: ContainerIdentifier) =>
  api.get<APIResponse<{ records: ReverseDNSRecord[] }>>(`/containers/${id}/rdns`)

export const updateReverseDNS = (id: ContainerIdentifier, records: ReverseDNSRecord[]) =>
  api.put<APIResponse<{ records: ReverseDNSRecord[] }>>(`/containers/${id}/rdns`, { records })

export const updateContainerExpiry = (id: ContainerIdentifier, expiresAt: string) =>
  api.put<APIResponse>(`/containers/${id}/expiry`, { expires_at: expiresAt })

export const getIPv6Status = () =>
  api.get<APIResponse<IPv6Status>>('/ipv6/status')

export const assignIPv6 = (id: ContainerIdentifier) =>
  api.post<APIResponse<Container>>(`/containers/${id}/ipv6`)

export interface IPAssignmentUpdateRequest {
  mode: 'clear' | 'random' | 'custom'
  count?: number
  addresses?: string[]
}

export const updatePublicIPv4Assignments = (id: ContainerIdentifier, data: IPAssignmentUpdateRequest) =>
  api.put<APIResponse<Container>>(`/containers/${id}/public-ipv4`, data)

export const updateIPv6Assignments = (id: ContainerIdentifier, data: IPAssignmentUpdateRequest) =>
  api.put<APIResponse<Container>>(`/containers/${id}/ipv6-addresses`, data)

export interface RouteCapacity {
  used: number
  remaining: string
  total: string
}

export interface NAT4PortRange {
  start: number
  end: number
}

export interface NAT4Route {
  container_id: number
  container_name: string
  lxc_name: string
  status: string
  ip: string
  host_ip: string
  host_port: number
  container_port: number
  protocol: string
  description: string
}

export interface IPv4Route {
  container_id: number
  container_name: string
  lxc_name: string
  status: string
  address: string
  interface: string
  prefix_len?: number
  gateway?: string
}

export interface LANDHCPRoute {
  container_id: number
  container_name: string
  lxc_name: string
  status: string
  address: string
  interface: string
  prefix_len?: number
  gateway?: string
  mac_address?: string
  mode: string
}

export interface IPv6Route {
  container_id: number
  container_name: string
  lxc_name: string
  status: string
  address: string
  prefix_len: number
  interface: string
}

export interface RoutingInfo {
  nat4: RouteCapacity
  nat4_port_range: NAT4PortRange
  nat4_next_port: number
  nat4_networks: {
    lxc: NATNetworkInfo
    kvm: NATNetworkInfo
  }
  ipv4: RouteCapacity
  lan_dhcp: RouteCapacity
  ipv6: RouteCapacity
  host_public_ipv4?: PublicIPv4Info
  public_ipv4_addresses: PublicIPv4Info[]
  ipv4_assignments: IPv4Route[]
  lan_dhcp_assignments: LANDHCPRoute[]
  nat4_mappings: NAT4Route[]
  ipv6_assignments: IPv6Route[]
  ipv6_prefixes: IPv6PrefixInfo[]
}

export interface PublicIPv4ScanResult extends PublicIPv4Info {
  status: string
  usable: boolean
  reason: string
}

export const getRoutingInfo = () =>
  api.get<APIResponse<RoutingInfo>>('/routing')

export const updateRoutingPools = (payload: { items?: PublicIPv4Info[]; ipv6_prefixes?: IPv6PrefixInfo[]; nat4_port_range?: NAT4PortRange }) =>
  api.put<APIResponse<RoutingInfo>>('/routing', payload)

export const updateRoutingIPv4Pool = (items: PublicIPv4Info[]) =>
  updateRoutingPools({ items })

export const updateRoutingIPv6Prefixes = (ipv6_prefixes: IPv6PrefixInfo[]) =>
  updateRoutingPools({ ipv6_prefixes })

export const scanRoutingIPv4Segment = (payload: { cidr: string; interface: string; gateway: string; verify: boolean; limit?: number }) =>
  api.post<APIResponse<PublicIPv4ScanResult[]>>('/routing/ipv4-scan', payload)

// 主控-被控节点管理（Controller / Agent）
export interface ManagedNode {
  id: string
  name: string
  address: string
  status: string // online / offline / pending
  last_seen?: string
  version?: string
  os_name?: string
  cpu_count?: number
  ram_total_mb?: number
  ram_used_mb?: number
  disk_total_gb?: number
  disk_used_gb?: number
  container_count?: number
  region_id?: string
  created_at?: string
  // 维护模式下调度器不再把新容器放到该节点（升级/维修前开启）。
  maintenance_mode?: boolean
}

export const getNodes = () =>
  api.get<APIResponse<ManagedNode[]>>('/nodes')

// 在指定被控节点上创建容器（主控代理到该节点 agent 执行）。
// 用于创建页「目标节点」选择：把实例下发到其它节点，而不是只落在主控本机。
export const createContainerOnNode = (nodeId: string, payload: CreateContainerRequest) =>
  api.post<APIResponse<Container>>(`/nodes/${encodeURIComponent(nodeId)}/containers`, payload)

// 放置调度：按「过滤（在线/容量/存储后端）→ 评分（剩余内存 60% + 剩余磁盘 40%）」
// 自动挑选目标节点。只做决策、不创建资源，因此可安全地反复调用。
export interface NodeScheduleCandidate {
  node_id: string
  node_name?: string
  passed: boolean
  score: number
  filter_reasons?: string[]
  score_reasons?: string[]
}

export interface NodeScheduleResult {
  chosen?: ManagedNode
  reason?: string
  candidates?: NodeScheduleCandidate[]
}

export const scheduleNode = (params: {
  ram_mb?: number
  disk_gb?: number
  virt?: string
  storage?: string
  count?: number
  tenant?: string
}) => api.get<APIResponse<NodeScheduleResult>>('/nodes/schedule', { params })

// 创建被控节点：bindIP 可选（把一次性 install_key 绑定到被控出口 IP）。
// quick 模式响应包含 node（脱敏）+ install_key（一次性，注册即焚，TTL 24h）。
export interface CreateNodeResult {
  node: ManagedNode
  install_key?: string
  install_key_ttl?: string
  install_key_bound?: string
  // manual 模式：手工接入材料（agent.json 预置配置，免 install_key 注册）。
  manual_bootstrap?: ManualBootstrap
}

// 手动添加节点返回的接入材料：写入被控机 <data_dir>/agent.json 后
// 运行 eyvescloud agent 即可接入（无需安装密钥）。token 仅此一次明文下发。
export interface ManualBootstrap {
  controller_url: string
  node_id: string
  token: string
  agent_config: string
  deploy_hint: string
}

export interface CreateNodePayload {
  name?: string
  address?: string
  bind_ip?: string
  mode?: 'quick' | 'manual'
  tls_skip_verify?: boolean
  allow_private?: boolean
}

export const createNode = (payload: CreateNodePayload) =>
  api.post<APIResponse<CreateNodeResult>>('/nodes', payload)

export const deleteNode = (id: string) =>
  api.delete<APIResponse>(`/nodes/${id}`)

export const getNodeInstallScript = (id: string) =>
  api.get<string>(`/nodes/${id}/install-script`, { responseType: 'text' })

// 一行安装命令（curl | sudo bash）：面板只展示命令，不展示脚本正文。
export interface NodeInstallCommand {
  command: string
  install_key: string
  expires_at: string
  bound_ip: string
  sha256: string
}

export const getNodeInstallCommand = (id: string) =>
  api.get<APIResponse<NodeInstallCommand>>(`/nodes/${id}/install-command`)

export const getNodeContainers = (nodeId: string) =>
  api.get<APIResponse<Container[]>>(`/nodes/${nodeId}/containers`)

export const createNodeContainer = (nodeId: string, data: CreateContainerRequest) =>
  api.post<APIResponse<Container>>(`/nodes/${nodeId}/containers`, data, { timeout: 600000 })

export const nodeContainerAction = (nodeId: string, containerId: number, action: string) =>
  api.post<APIResponse>(`/nodes/${nodeId}/containers/${containerId}/${action}`)

// 节点镜像管理：查询被控镜像清单 / 主控下发镜像同步
export interface NodeCatalogImage {
  id: string
  name: string
  type?: string
  distro?: string
  release?: string
  arch?: string
  url?: string
  sha256?: string
  description?: string
  created_at?: string
}

export const getNodeImages = (nodeId: string) =>
  api.get<APIResponse<{ lxc?: NodeCatalogImage[]; kvm?: NodeCatalogImage[] }>>(`/nodes/${nodeId}/images`)

export const syncNodeImages = (nodeId: string) =>
  api.post<APIResponse<{ added?: number; updated?: number; pulled?: string[]; failed?: string[] }>>(`/nodes/${nodeId}/images/sync`, {}, { timeout: 600000 })

export const nodeColdBackup = (nodeId: string) =>
  api.post<APIResponse<{ backed_up?: number; failed?: string[]; total_bytes?: number; container_cnt?: number }>>(`/nodes/${nodeId}/backup`, {}, { timeout: 600000 })

// Templates
export const getTemplates = () =>
  api.get<APIResponse<Template[]>>('/templates')

// Images (template download/enable management)
export interface ImageInfo {
  id: string
  name: string
  type: string
  distro: string
  release: string
  arch: string
  description: string
  downloaded: boolean
  enabled: boolean
  downloading: boolean
  progress: number
  downloaded_bytes: number
  total_bytes: number
  stage?: string
  error?: string
  size_bytes: number
  manual_path?: string
  desktop?: string
  provisioner?: string
  custom?: boolean
  sha256?: string
}

export interface CustomKVMImageInput {
  type: 'lxc' | 'kvm'
  name: string
  description: string
  distro: string
  release: string
  arch: string
  url: string
  provisioner?: 'linux-cloud-init' | 'windows-10' | 'windows-11' | 'lxc-rootfs'
  sha256?: string
}

export interface CustomKVMImage extends CustomKVMImageInput {
  id: string
  created_at: string
}

export const getImages = () =>
  api.get<APIResponse<ImageInfo[]>>('/images')

export const createCustomKVMImage = (payload: CustomKVMImageInput) =>
  api.post<APIResponse<CustomKVMImage>>('/images/custom', payload)

export const removeCustomKVMImage = (id: string) =>
  api.delete<APIResponse>('/images/custom', { data: { id } })

export const downloadImage = (templateId: string) =>
  api.post<APIResponse>('/images/download', { template_id: templateId })

export const cancelImageDownload = (templateId: string) =>
  api.post<APIResponse>('/images/cancel', { template_id: templateId })

export const deleteImage = (templateId: string) =>
  api.delete<APIResponse>('/images/delete', { data: { template_id: templateId } })

export const toggleImage = (templateId: string, enabled: boolean) =>
  api.put<APIResponse>('/images/toggle', { template_id: templateId, enabled })

export const getEnabledImages = (virtualization = 'lxc', container?: ContainerIdentifier) =>
  api.get<APIResponse<Template[]>>('/images/enabled', { params: { type: virtualization, ...(container ? { container: String(container) } : {}) } })

// Dashboard
export const getDashboard = () =>
  api.get<APIResponse<DashboardStats>>('/dashboard')

export const getHostInfo = () =>
  api.get<APIResponse<HostInfo>>('/host-info')

export const getHostHistory = () =>
  api.get<APIResponse<HostMetricPoint[]>>('/host-history')

export const getHostReport = () =>
  api.get<APIResponse<HostProbeReport>>('/host-report')

export const getStorageInfo = () =>
  api.get<APIResponse<StorageInfo>>('/storage')

export const updateStoragePools = (pools: StoragePool[]) =>
  api.put<APIResponse<StorageInfo>>('/storage', { pools })

// Snapshots
export interface Snapshot {
  id: string
  container_id: number
  container_name: string
  lxc_name: string
  created_at: string
  created_by: string
  scheduled: boolean
  path: string
  size_bytes: number
}

export interface SnapshotSchedule {
  enabled: boolean
  interval_hours: number
  time: string
  last_run: string
  next_run: string
  created_by: string
}

export interface ContainerSnapshotsResponse {
  snapshots: Snapshot[]
  quota: number
  schedule: SnapshotSchedule
}

export const getSnapshots = () =>
  api.get<APIResponse<Snapshot[]>>('/snapshots')

export const getContainerSnapshots = (id: ContainerIdentifier) =>
  api.get<APIResponse<ContainerSnapshotsResponse>>(`/containers/${id}/snapshots`)

export const createContainerSnapshot = (id: ContainerIdentifier, options?: CreateSnapshotOptions) =>
  api.post<APIResponse<Snapshot>>(`/containers/${id}/snapshots`, options || {}, { timeout: 600000 })

export const deleteContainerSnapshot = (id: ContainerIdentifier, snapshotId: string) =>
  api.delete<APIResponse>(`/containers/${id}/snapshots/${snapshotId}`, { timeout: 600000 })

export const restoreContainerSnapshot = (id: ContainerIdentifier, snapshotId: string) =>
  api.post<APIResponse>(`/containers/${id}/snapshots/${snapshotId}/restore`, {}, { timeout: 600000 })

export const updateSnapshotSchedule = (id: ContainerIdentifier, enabled: boolean, intervalHours: number, time: string) =>
  api.post<APIResponse<{ container: Container; snapshot?: Snapshot }>>(
    `/containers/${id}/snapshots/schedule`,
    { enabled, interval_hours: intervalHours, time },
    { timeout: 600000 }
  )

export const updateSnapshotQuota = (id: ContainerIdentifier, snapshotLimit: number) =>
  api.put<APIResponse<{ container: Container; quota: number }>>(
    `/containers/${id}/snapshots/quota`,
    { snapshot_limit: snapshotLimit }
  )

// 实例级备份（数据安全）：完整磁盘备份，keep-N 保留 + 还原
export interface InstanceBackup {
  id: string
  container_id: number
  container_name: string
  kind: string
  created_at: string
  created_by: string
  scheduled: boolean
  path: string
  size_bytes: number
}

export const getContainerBackups = (id: ContainerIdentifier) =>
  api.get<APIResponse<InstanceBackup[]>>(`/containers/${id}/backups`)

export const createContainerBackup = (id: ContainerIdentifier, keep?: number) =>
  api.post<APIResponse<InstanceBackup>>(`/containers/${id}/backups`, { keep: keep || 0 }, { timeout: 600000 })

export const deleteContainerBackup = (id: ContainerIdentifier, backupId: string) =>
  api.delete<APIResponse>(`/containers/${id}/backups/${backupId}`, { timeout: 600000 })

export const restoreContainerBackup = (id: ContainerIdentifier, backupId: string) =>
  api.post<APIResponse>(`/containers/${id}/backups/${backupId}/restore`, {}, { timeout: 600000 })

// WebSSH URL generator
export const getWebSSHUrl = (containerName: string) => {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const params = new URLSearchParams({ container: containerName })
  return `${protocol}//${window.location.host}/api/ssh?${params.toString()}`
}

export const getWebVNCUrl = (containerName: string) => {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const params = new URLSearchParams({ container: containerName })
  return `${protocol}//${window.location.host}/api/vnc?${params.toString()}`
}

// Task Queue
export interface Task {
  id: string
  type: string
  container_id?: number
  container_name: string
  status: string
  error?: string
  stage?: string
  stage_detail?: string
  percent?: number
  created_at: string
  template_id?: string
  config?: CreateContainerRequest
}

export const getTasks = () =>
  api.get<APIResponse<Task[]>>('/tasks')

export const deleteTask = (taskId: string) =>
  api.delete<APIResponse>(`/tasks/${taskId}`)

// Task History / Center
export interface TaskHistoryEntry {
  id: string
  type: string
  container_id?: number
  container_name?: string
  status: string
  error?: string
  stage?: string
  stage_detail?: string
  percent: number
  user?: string
  ip?: string
  user_agent?: string
  created_at: string
  started_at?: string
  ended_at?: string
  duration_ms: number
}

export interface TaskLogEntry {
  level: string
  message: string
  created_at: string
}

export interface TaskHistoryPage {
  total: number
  page: number
  page_size: number
  items: TaskHistoryEntry[]
  timenow?: string
}

export interface TaskHistoryStats {
  total: number
  by_status: Record<string, number>
  by_type: Record<string, number>
  avg_duration_ms: number
}

export interface TaskStats {
  history: TaskHistoryStats
  live: { concurrency?: number; active?: number; pending?: number }
}

export interface TaskDetailData {
  task: TaskHistoryEntry & Partial<Task>
  logs: TaskLogEntry[]
  live: boolean
}

export interface TaskHistoryQuery {
  status?: string
  type?: string
  page?: number
  page_size?: number
}

export const getTaskHistory = (params: TaskHistoryQuery = {}) =>
  api.get<APIResponse<TaskHistoryPage>>('/tasks/history', { params })

export const getTaskStats = () =>
  api.get<APIResponse<TaskStats>>('/tasks/stats')

export const getTaskDetail = (taskId: string) =>
  api.get<APIResponse<TaskDetailData>>(`/tasks/${encodeURIComponent(taskId)}`)

export const cancelTask = (taskId: string) =>
  api.post<APIResponse<{ id: string; status: string; running: boolean }>>(
    `/tasks/${encodeURIComponent(taskId)}/cancel`,
  )

export const batchCreate = (containers: CreateContainerRequest[]) =>
  api.post<APIResponse<string[]>>('/batch-create', { containers })

export const batchAction = (action: string, containers: number[], templateId?: string) =>
  api.post<APIResponse>('/batch-action', { action, containers, template_id: templateId })

// Sub Users
export interface SubUser {
  id: string
  username: string
  password?: string
  tenant?: string
  role?: string
  email?: string
  container_names: string[]
  container_uuids?: string[]
  allowed_image_ids?: string[]
  image_limit_configured?: boolean
  current_image_ids?: string[]
  access_code: string
  created_at: string
  last_login?: string
  last_login_ip?: string
}

export interface NATNetworkInfo {
  subnet: string
  gateway: string
  netmask: string
  dhcp_start: string
  dhcp_end: string
  dhcp_max: number
  prefix_bits: number
}

export const createSubUser = (containerId: ContainerIdentifier) =>
  api.post<APIResponse<SubUser>>('/sub-user/create', { container_name: String(containerId) })

// 新模式创建：可建空账号，也可同时绑定多个容器（id/uuid/name 均可）
export const createSubUserAdvanced = (payload: {
  container_names?: string[]
  username?: string
  email?: string
  tenant?: string
  role?: string
  password?: string
}) => api.post<APIResponse<SubUser>>('/sub-user/create', payload)

export const listSubUsers = () =>
  api.get<APIResponse<SubUser[]>>('/sub-users')

export const listSubUsersPaged = (params: { page?: number; page_size?: number; search?: string } = {}) =>
  api.get<APIResponse<PagedList<SubUser>>>('/sub-users', { params })

export const updateSubUserImages = (id: string, allowedImageIds: string[]) =>
  api.put<APIResponse<SubUser>>(`/sub-users/${id}/images`, { allowed_image_ids: allowedImageIds })

// 编辑子用户基本信息（均可选填，只传需要修改的字段）
export const updateSubUser = (id: string, payload: { username?: string; email?: string; role?: string; password?: string }) =>
  api.put<APIResponse<SubUser>>(`/sub-users/${id}/edit`, payload)

// 删除子用户：其名下容器的归属会被自动解绑（容器本身保留）
export const deleteSubUser = (id: string) =>
  api.delete<APIResponse<{ id: string; username: string; freed_containers: string[] }>>(`/sub-users/${id}`)

// 整体替换子用户的容器绑定集（containers 支持 id/uuid/name）
export const bindSubUserContainers = (id: string, containers: string[]) =>
  api.put<APIResponse<SubUser>>(`/sub-users/${id}/bind-containers`, { containers })

export const changeContainerOwner = (id: number | string, ownerSubUserId: string) =>
  api.put<APIResponse<{ owner_sub_user_id: string; owner_username?: string }>>(`/containers/${id}/owner`, { owner_sub_user_id: ownerSubUserId })

// Audit Logs
export interface AuditLog {
  time: string
  action: string
  target: string
  detail: string
  user: string
}

export const getAuditLogs = () =>
  api.get<APIResponse<AuditLog[]>>('/audit-logs')

export const getAuditLogsPaged = (params: { page?: number; page_size?: number } = {}) =>
  api.get<APIResponse<PagedList<AuditLog>>>('/audit-logs', { params })

// Security
export interface SecurityAlert {
  id: string
  container_name: string
  kind?: string
  type: string
  severity: string
  source_ip: string
  target_ip: string
  target_port: number
  detail: string
  log_line: string
  timestamp: string
  count: number
}

export interface SecuritySummary {
  total_alerts: number
  critical: number
  high: number
  medium: number
  low: number
  conntrack_available?: boolean
  abuse_detection_enabled?: boolean
}

export interface SecuritySettings {
  auto_shutdown?: boolean
  arp_protection?: boolean
  ip_anti_spoof?: boolean
  abuse_detection?: boolean
}

export interface SecurityLog {
  src_ip: string
  dst_ip: string
  src_port: number
  dst_port: number
  protocol: string
  state: string
}

export const getSecurityAlerts = () =>
  api.get<APIResponse<SecurityAlert[]>>('/security/alerts')

export const checkContainerSecurity = (containerName: string) =>
  api.post<APIResponse>('/security/check', { container_name: containerName })

export const getSecurityLogs = (containerName: string) =>
  api.get<APIResponse<SecurityLog[]>>('/security/logs', { params: { container: containerName } })

export const getSecuritySummary = () =>
  api.get<APIResponse<SecuritySummary>>('/security/summary')

export const getSecuritySettings = () =>
  api.get<APIResponse<SecuritySettings>>('/security/settings')

export const updateSecuritySettings = (data: SecuritySettings) =>
  api.put<APIResponse<SecuritySettings>>('/security/settings', data)

// 管理端集中监控：所有容器的实时指标与滥用概况
export interface ContainerMonitorRow {
  id: number
  uuid: string
  name: string
  virtualization: string
  status: string
  tenant?: string
  owner?: string
  ip?: string
  vcpu: number
  ram_mb: number
  disk_gb: number
  cpu: number
  memory: number
  network_rx: number
  network_tx: number
  disk_read: number
  disk_write: number
  traffic_used_rx_gb: number
  traffic_used_tx_gb: number
  metric_ts?: number
  abuse_alerts: number
  abuse_severity?: string
  policy_blocked: boolean
}

export const getContainerMonitoring = (params: { page?: number; page_size?: number } = {}) =>
  api.get<APIResponse<{
    containers: ContainerMonitorRow[]
    total: number
    generated_at: string
    page?: number
    page_size?: number
    summary?: { running: number; abusers: number; abuse_alerts: number }
  }>>('/monitoring/containers', { params })

// 滥用记录：按用户/租户与类型聚合
export interface AbuseOwnerSummary {
  owner: string
  tenant?: string
  alerts: number
  containers: string[]
  types: Record<string, number>
  severity: string
  last_seen: string
}

export interface AbuseSummary {
  total_alerts: number
  by_owner: AbuseOwnerSummary[]
  by_type: Record<string, number>
}

export const getAbuseSummary = () =>
  api.get<APIResponse<AbuseSummary>>('/security/abuse-summary')

// 容器内创建新登录账号
export const createContainerAccount = (id: ContainerIdentifier, data: { username: string; password: string; sudo?: boolean }) =>
  api.post<APIResponse<{ username: string; sudo: boolean }>>(`/containers/${id}/create-account`, data)

// 实例磁盘自动备份设置
export interface InstanceBackupSettings {
  enabled: boolean
  interval_hours: number
  keep: number
  last_run_at?: string
}

export const getInstanceBackupSettings = () =>
  api.get<APIResponse<InstanceBackupSettings>>('/instance-backup/settings')

export const updateInstanceBackupSettings = (data: InstanceBackupSettings) =>
  api.put<APIResponse<InstanceBackupSettings>>('/instance-backup/settings', data)

// 异地（远程）备份目标：把实例备份归档额外复制到运营方自备的备份服务器（SSH/SCP）
export interface RemoteBackupSettings {
  enabled: boolean
  host: string
  port: number
  user: string
  remote_dir: string
  key_path?: string
  last_result?: string
  last_error?: string
  last_run_at?: string
  // 后端派生字段（只读）：实际生效的私钥路径、需安装到远端 authorized_keys 的公钥
  effective_key_path?: string
  public_key?: string
}

export const getRemoteBackupSettings = () =>
  api.get<APIResponse<RemoteBackupSettings>>('/backup/remote-settings')

export const updateRemoteBackupSettings = (data: RemoteBackupSettings) =>
  api.put<APIResponse<RemoteBackupSettings>>('/backup/remote-settings', data)

export const testRemoteBackup = (data: RemoteBackupSettings) =>
  api.post<APIResponse>('/backup/remote-test', data)

// 备份计划（定时备份）
export interface BackupPlanRun {
  at: string
  status: string
  backups: number
  failed: number
  error?: string
  duration_ms: number
}

export interface BackupPlan {
  id: string
  name: string
  container_id: number
  cron: string
  enabled: boolean
  keep: number
  created_at?: string
  last_run_at?: string
  next_run_at?: string
  last_status?: string
  last_error?: string
  runs?: BackupPlanRun[]
}

export interface BackupPlanInput {
  name?: string
  container_id: number
  cron: string
  enabled?: boolean
  keep?: number
}

export const getBackupPlans = () =>
  api.get<APIResponse<BackupPlan[]>>('/backup-plans')

export const createBackupPlan = (data: BackupPlanInput) =>
  api.post<APIResponse<BackupPlan>>('/backup-plans', data)

export const updateBackupPlan = (id: string, data: BackupPlanInput) =>
  api.put<APIResponse<BackupPlan>>(`/backup-plans/${encodeURIComponent(id)}`, data)

export const deleteBackupPlan = (id: string) =>
  api.delete<APIResponse>(`/backup-plans/${encodeURIComponent(id)}`)

export const runBackupPlan = (id: string) =>
  api.post<APIResponse>(`/backup-plans/${encodeURIComponent(id)}/run`)

export const createWebSSHTicket = (containerName: string) =>
  api.post<APIResponse<{ ticket: string }>>('/ssh-ticket', { container_name: containerName })

export const createVNCTicket = (containerName: string) =>
  api.post<APIResponse<{ ticket: string }>>('/vnc-ticket', { container_name: containerName })

// Language
export type PanelLanguage = 'zh' | 'en'

export const getLanguage = () =>
  api.get<APIResponse<{ language: PanelLanguage }>>('/language')

export const updateLanguage = (language: PanelLanguage) =>
  api.post<APIResponse<{ language: PanelLanguage }>>('/language', { language })

// Version
export const getVersion = () =>
  api.get<APIResponse<{ version: string }>>('/version')

// 面板版本检测：返回当前与最新版本（管理员；检测 + 缓存，不自动升级）
export interface CheckUpdateResult {
  current?: string
  latest?: string
  has_update?: boolean
  err?: string
}
export const checkUpdate = () =>
  api.get<APIResponse<CheckUpdateResult>>('/v1/check-update')

// 面板更新：可选版本列表（管理员），repo 为 "owner/name"，默认官方仓库
export interface UpdateRelease {
  tag_name: string
  name?: string
  html_url?: string
  published_at?: string
  prerelease?: boolean
  has_asset?: boolean
}
export const listUpdateReleases = (repo?: string) =>
  api.get<APIResponse<{ repo: string; current: string; releases: UpdateRelease[] }>>('/v1/update/releases', {
    params: repo ? { repo } : undefined,
  })

// 面板内直接升级（管理员）：触发下载→解压→备份→就地替换→重启，返回"已开始"。
// 可选指定仓库（owner/name，默认官方仓库）与目标版本 tag（默认最新版）。
export const updatePanel = (opts?: { repo?: string; tag?: string }) =>
  api.post<APIResponse<{ message?: string }>>('/v1/update', opts ?? {})

// ---- CPU/带宽策略 (Policy) ----
export interface PolicyRule {
  id?: string
  name: string
  enabled: boolean
  metric: 'cpu' | 'memory' | 'network_rx' | 'network_tx' | 'disk_io'
  operator: 'gt' | 'lt'
  threshold: number
  action: 'raise_cpu' | 'raise_ram' | 'adjust_bw' | 'shutdown' | 'notify'
  adjust_vcpu?: number
  adjust_ram_mb?: number
  adjust_bw_mbps?: number
  cooldown_minutes?: number
  target_scope: string
  created_at?: string
  last_triggered?: string
  triggered_count?: number
}

export interface PolicyTriggerRecord {
  time: string
  rule_id: string
  rule_name: string
  container: string
  metric: string
  value: number
  action: string
  detail: string
}

export const getPolicies = () =>
  api.get<APIResponse<{ rules: PolicyRule[]; history: PolicyTriggerRecord[] }>>('/policies')

export const createPolicy = (data: PolicyRule) =>
  api.post<APIResponse<PolicyRule>>('/policies', data)

export const updatePolicy = (id: string, data: PolicyRule) =>
  api.put<APIResponse<PolicyRule>>(`/policies/${id}`, data)

export const deletePolicy = (id: string) =>
  api.delete<APIResponse>(`/policies/${id}`)

// ---- 节点迁移 (Migration) ----
export const migrateExport = (id: ContainerIdentifier) =>
  api.get<MigrateBundle>(`/containers/${id}/migrate-export`)

export const migrateImport = (data: MigrateBundle) =>
  api.post<APIResponse<{ name: string }>>('/migrate/import', data)

export interface MigrateContainer {
  name: string
  virtualization: string
  template: string
  vcpu: number
  ram_mb: number
  disk_gb: number
  network_bw_mbps: number
  network_down_mbps: number
  network_up_mbps: number
  monthly_traffic_gb: number
  traffic_mode: string
  traffic_in_gb: number
  traffic_out_gb: number
  io_speed_mbps: number
  io_read_mbps: number
  io_write_mbps: number
  lan_ipv4_mode?: string
  lan_interface?: string
  lan_ipv4_address?: string
  lan_ipv4_prefix_len?: number
  lan_ipv4_gateway?: string
  public_ipv4s?: PublicIPv4Assignment[]
  ipv6_addresses?: IPv6Assignment[]
  port_mappings?: PortMapping[]
  port_mapping_limit?: number
  firewall_enabled?: boolean
  firewall_default_action?: string
  firewall_rules?: FirewallRule[]
  ssh_auth_mode?: string
  ssh_password?: string
  ssh_public_key?: string
  cloud_init_user_data?: string
  snapshot_limit?: number
  tenant?: string
  expires_at?: string
  created_at?: string
}

export interface MigrateBundle {
  format: string
  version: number
  exported_at: string
  source_node?: string
  container: MigrateContainer
}

// ---- Node Group（迁移池/策略池，对应 主流面板 Server Group / 同类面板 Node Group） ----
export interface NodeGroup {
  id: string
  name: string
  description?: string
  region_id?: string
  created_at?: string
}

export const listNodeGroups = () =>
  api.get<APIResponse<NodeGroup[]>>('/node-groups')

export const createNodeGroup = (payload: { name: string; description?: string; region_id?: string; node_ids?: string[] }) =>
  api.post<APIResponse<NodeGroup>>('/node-groups', payload)

// node_ids 非 undefined 时整体替换成员集合（空数组 = 清空全部成员）
export const updateNodeGroup = (id: string, payload: Partial<NodeGroup> & { node_ids?: string[] }) =>
  api.put<APIResponse<null>>(`/node-groups/${id}`, payload)

export const deleteNodeGroup = (id: string) =>
  api.delete<APIResponse<null>>(`/node-groups/${id}`)

// ---- Cluster（跨 NodeGroup 的高可用/迁移域，对应 主流面板 Cluster） ----
export interface Cluster {
  id: string
  name: string
  description?: string
  region_ids?: string[]
  created_at?: string
}

export const listClusters = () =>
  api.get<APIResponse<Cluster[]>>('/clusters')

export const createCluster = (payload: { name: string; description?: string; region_ids?: string[]; node_ids?: string[] }) =>
  api.post<APIResponse<Cluster>>('/clusters', payload)

// node_ids 非 undefined 时整体替换成员集合（空数组 = 清空全部成员）
export const updateCluster = (id: string, payload: Partial<Cluster> & { node_ids?: string[] }) =>
  api.put<APIResponse<null>>(`/clusters/${id}`, payload)

export const deleteCluster = (id: string) =>
  api.delete<APIResponse<null>>(`/clusters/${id}`)

// ---- 容器迁移 ----
export const migrateContainer = (id: number, targetNodeID: string, force = false) =>
  api.put<APIResponse<{ node_id: string }>>(`/containers/${id}/migrate`, { target_node_id: targetNodeID, force })

// ===================== 安全组（Security Groups） =====================
// 对应后端 /api/security-groups 与 /api/containers/{id}/security-groups
export interface SecGroup {
  id: string
  tenant_id: string
  name: string
  default_action: string // accept | drop
}

export interface SecGroupRule {
  id: string
  group_id: string
  direction: string // ingress | egress
  protocol: string // any | tcp | udp | icmp
  src_mask?: string
  dst_mask?: string
  src_port?: number // 0 = 任意
  dst_port?: number
  action: string // accept | drop | reject
  priority: number // 数值小 = 优先
  description?: string
}

export interface SecGroupDetail {
  group: SecGroup
  rules: SecGroupRule[]
}

export const listSecGroups = () =>
  api.get<APIResponse<SecGroup[]>>('/security-groups')

// 安全组强制执行状态。
// 规则默认只保存不下发（默认策略 drop，规则不全的容器会立即断网），
// 因此界面必须如实呈现「是否真的生效」，而不是让用户以为配了就有效。
export interface SecGroupEnforcement {
  enforced: boolean
  group_count: number
  rule_count: number
  note: string
}

export const getSecGroupEnforcement = () =>
  api.get<APIResponse<SecGroupEnforcement>>('/security-group-enforcement')

export const createSecGroup = (payload: { name: string; tenant_id?: string; default_action?: string }) =>
  api.post<APIResponse<SecGroup>>('/security-groups', payload)

export const getSecGroup = (id: string) =>
  api.get<APIResponse<SecGroupDetail>>(`/security-groups/${id}`)

export const updateSecGroup = (id: string, payload: { name?: string; default_action?: string }) =>
  api.put<APIResponse<null>>(`/security-groups/${id}`, payload)

export const deleteSecGroup = (id: string) =>
  api.delete<APIResponse<null>>(`/security-groups/${id}`)

export const listSecGroupRules = (groupId: string) =>
  api.get<APIResponse<SecGroupRule[]>>(`/security-groups/${groupId}/rules`)

export const createSecGroupRule = (groupId: string, payload: Omit<SecGroupRule, 'id' | 'group_id'>) =>
  api.post<APIResponse<SecGroupRule>>(`/security-groups/${groupId}/rules`, payload)

export const updateSecGroupRule = (groupId: string, ruleId: string, payload: Partial<Omit<SecGroupRule, 'id' | 'group_id'>>) =>
  api.put<APIResponse<null>>(`/security-groups/${groupId}/rules/${ruleId}`, payload)

export const deleteSecGroupRule = (groupId: string, ruleId: string) =>
  api.delete<APIResponse<null>>(`/security-groups/${groupId}/rules/${ruleId}`)

// 容器 ↔ 安全组绑定
export interface ContainerSecGroupBinding {
  container_id: number
  container: string
  sec_group_ids: string[]
}

export const getContainerSecGroups = (containerId: number | string) =>
  api.get<APIResponse<ContainerSecGroupBinding>>(`/containers/${containerId}/security-groups`)

export const setContainerSecGroups = (containerId: number | string, groupIds: string[]) =>
  api.put<APIResponse<null>>(`/containers/${containerId}/security-groups`, { group_ids: groupIds })

export const bindContainerSecGroup = (containerId: number | string, groupId: string) =>
  api.post<APIResponse<null>>(`/containers/${containerId}/security-groups/${groupId}`)

export const unbindContainerSecGroup = (containerId: number | string, groupId: string) =>
  api.delete<APIResponse<null>>(`/containers/${containerId}/security-groups/${groupId}`)

// ===================== SSH 公钥托管 =====================
// 对应后端 /api/ssh-keys（列表不返回公钥正文，详情才返回）
export interface SSHKeySummary {
  id: string
  name: string
  fingerprint: string
  type: string // admin | subuser
  owner_id?: string
  created_at: string
  last_used_at?: string
}

export interface SSHKey extends SSHKeySummary {
  public_key: string
}

export const listSSHKeys = () =>
  api.get<APIResponse<SSHKeySummary[]>>('/ssh-keys')

export const createSSHKey = (payload: { name: string; public_key: string }) =>
  api.post<APIResponse<SSHKey>>('/ssh-keys', payload)

export const getSSHKey = (id: string) =>
  api.get<APIResponse<SSHKey>>(`/ssh-keys/${id}`)

export const updateSSHKey = (id: string, payload: { name: string }) =>
  api.put<APIResponse<null>>(`/ssh-keys/${id}`, payload)

export const deleteSSHKey = (id: string) =>
  api.delete<APIResponse<null>>(`/ssh-keys/${id}`)

// ===================== Webhook 事件订阅 =====================
// 对应后端 /api/webhooks（secret 仅创建响应明文返回一次）
export interface WebhookSubscription {
  id: string
  name: string
  url: string
  secret?: string
  event_types?: string[]
  enabled: boolean
  consecutive_failures?: number
  last_delivery_at?: string
  last_delivery_status?: string
  auto_disabled_reason?: string
  created_at: string
}

export const listWebhooks = () =>
  api.get<APIResponse<WebhookSubscription[]>>('/webhooks')

export const createWebhook = (payload: { name: string; url: string; event_types?: string[]; enabled?: boolean }) =>
  api.post<APIResponse<WebhookSubscription>>('/webhooks', payload)

export const getWebhook = (id: string) =>
  api.get<APIResponse<WebhookSubscription>>(`/webhooks/${id}`)

export const updateWebhook = (id: string, payload: { name?: string; url?: string; event_types?: string[]; enabled?: boolean }) =>
  api.put<APIResponse<null>>(`/webhooks/${id}`, payload)

export const deleteWebhook = (id: string) =>
  api.delete<APIResponse<null>>(`/webhooks/${id}`)

export const testWebhook = (id: string) =>
  api.post<APIResponse<null>>(`/webhooks/${id}/test`)

// ===================== Recipes（脚本模板 / 一键应用） =====================
// 对应后端 /api/recipes（admin 可见全部；scope=shared 对子用户可见）
export interface Recipe {
  id: string
  name: string
  description: string
  script: string
  owner_id: string
  owner_type: string // admin | subuser
  scope: string // private | shared
  created_at: string
  updated_at: string
}

export const listRecipes = () =>
  api.get<APIResponse<Recipe[]>>('/recipes')

export const createRecipe = (payload: { name: string; description?: string; script: string; scope?: string }) =>
  api.post<APIResponse<Recipe>>('/recipes', payload)

export const getRecipe = (id: string) =>
  api.get<APIResponse<Recipe>>(`/recipes/${id}`)

export const updateRecipe = (id: string, payload: { name?: string; description?: string; script?: string; scope?: string }) =>
  api.put<APIResponse<null>>(`/recipes/${id}`, payload)

export const deleteRecipe = (id: string) =>
  api.delete<APIResponse<null>>(`/recipes/${id}`)

// 在指定容器上执行脚本（容器须为 running；默认超时 300s）
export const executeRecipe = (id: string, containerId: number, timeout?: number) =>
  api.post<APIResponse<{ recipe_name: string; container: string; output: string; exit_code: number; virtualization?: string }>>(`/recipes/${id}/execute`, { container_id: containerId, timeout })

// ===================== 审计日志导出 =====================
// GET /audit-logs/export?format=csv|json（另有 cef/syslog SIEM 格式与 chain=verify 校验）
export const exportAuditLogs = (format: 'csv' | 'json' | 'cef' | 'syslog') =>
  api.get<Blob>('/audit-logs/export', { params: { format }, responseType: 'blob' })

export const verifyAuditChain = () =>
  api.get<APIResponse<{ valid: boolean; checked?: number; reason?: string }>>('/audit-logs/export', { params: { chain: 'verify' } })

// ===================== 容器自服务扩展（进程 / 服务 / 定时任务 / 带宽 / HVM） =====================
// 对应后端 主流面板风格端点（backend/internal/api/container_*.go），v1.9.x 起可用。

export interface ContainerProcessInfo {
  pid: number
  user: string
  cpu_pct: number
  mem_pct: number
  rss_kb: number
  stat: string
  command: string
}

export interface ContainerProcessesData {
  container_id: number
  container_name: string
  total: number
  processes: ContainerProcessInfo[] | null
  sampled_at: string
  source: string
}

// 容器内进程列表（仅 LXC；KVM 需 qemu-guest-agent，后端返回 success=false）
export const getContainerProcesses = (id: ContainerIdentifier) =>
  api.get<APIResponse<ContainerProcessesData>>(`/containers/${id}/processes`)

// 批量终止进程：pids 最多 64 个、禁止 PID 1；signal ∈ TERM/KILL/HUP/INT
export const killContainerProcesses = (id: ContainerIdentifier, pids: number[], signal?: string) =>
  api.post<APIResponse>(`/containers/${id}/processes/kill`, { pids, signal })

export interface ContainerServiceUnitInfo {
  name: string
  state: string
  autostart: boolean
  enabled: boolean
  unit_type: string
}

export interface ContainerServicesData {
  container_id: number
  container_name: string
  total: number
  services: ContainerServiceUnitInfo[] | null
  sampled_at: string
  source: string
}

// 容器内 systemd 服务列表（仅 LXC systemd 容器）
export const getContainerServices = (id: ContainerIdentifier) =>
  api.get<APIResponse<ContainerServicesData>>(`/containers/${id}/services`)

// 单服务操作：action ∈ start/stop/restart/reload/enable/disable
export const containerServiceAction = (id: ContainerIdentifier, service: string, action: 'start' | 'stop' | 'restart' | 'reload' | 'enable' | 'disable') =>
  api.post<APIResponse>(`/containers/${id}/services`, { service, action })

export interface ContainerScheduledAction {
  id: string
  container_id: number
  container_name: string
  type: string // start/stop/restart/poweroff
  repeat: string // none/daily/weekly/monthly
  execute_at: string
  enabled: boolean
  created_at: string
  created_by: string
}

export const listContainerScheduledActions = (id: ContainerIdentifier) =>
  api.get<APIResponse<{ actions: ContainerScheduledAction[] | null; total: number }>>(`/containers/${id}/scheduled-actions`)

// execute_at 须为 RFC3339 且在未来 5 年内；单容器最多 10 条
export const createContainerScheduledAction = (id: ContainerIdentifier, payload: { type: string; execute_at: string; repeat?: string }) =>
  api.post<APIResponse<ContainerScheduledAction>>(`/containers/${id}/scheduled-actions`, payload)

export const deleteContainerScheduledAction = (id: ContainerIdentifier, actionId: string) =>
  api.delete<APIResponse>(`/containers/${id}/scheduled-actions/${actionId}`)

export interface ContainerBandwidthPoint {
  bucket: string
  in_gb: number
  out_gb: number
  total_gb: number
}

export interface ContainerBandwidthData {
  container_id: number
  container_name: string
  period: string // "yyyy-mm" / "hourly" / "yyyy-mm-dd"
  unit: string
  total: { in_gb: number; out_gb: number; total_gb: number }
  series: ContainerBandwidthPoint[] | null
  sampled_at: string
  source: string // agent / local / unavailable
}

// 流量明细：period 省略 = 当月；"hourly" = 24 小时逐时
export const getContainerBandwidth = (id: ContainerIdentifier, period?: string) =>
  api.get<APIResponse<ContainerBandwidthData>>(`/containers/${id}/bandwidth`, { params: period ? { period } : undefined })

export interface ContainerHVMSettings {
  container_id: number
  container_name: string
  boot_order: string // cda/dca/cd
  nic_driver: string // virtio/e1000/rtl8139/ne2k_pci/vmxnet3
  vnc_keymap: string
  enable_tuntap: boolean
  enable_ppp: boolean
  acceleration: string // default/host-passthrough/off
  available_keymaps: string[] | null
  available_nic_drivers: string[] | null
}

// KVM HVM 设置读取（仅 KVM 容器）
export const getContainerHVMSettings = (id: ContainerIdentifier) =>
  api.get<APIResponse<ContainerHVMSettings>>(`/containers/${id}/hvm-settings`)

// KVM HVM 设置更新（下次启动生效）
export const updateContainerHVMSettings = (id: ContainerIdentifier, payload: {
  boot_order?: string
  nic_driver?: string
  vnc_keymap?: string
  enable_tuntap?: boolean
  enable_ppp?: boolean
  acceleration?: string
}) =>
  api.put<APIResponse>(`/containers/${id}/hvm-settings`, payload)

// 在当前容器上执行 Recipe（容器级入口；容器须 running）
export const executeRecipeOnContainer = (id: ContainerIdentifier, recipeId: string, timeout?: number) =>
  api.post<APIResponse<{ recipe_id: string; container: string; output: string }>>(`/containers/${id}/recipes/execute`, { recipe_id: recipeId, timeout })

// ===================== 全量用量导出（财务对账） =====================
// GET /api/v1/usage?tenant=xxx（权限 usage:read，子用户不可用，防止跨租户枚举）
export interface UsageExportTraffic {
  monthly_limit_gb: number
  mode: string
  used_rx_bytes: number
  used_tx_bytes: number
  used_rx_gb: number
  used_tx_gb: number
  reset_date: string
}

export interface UsageExportItem {
  uuid: string
  name: string
  tenant?: string
  virtualization?: string
  node?: string
  vcpu: number
  ram_mb: number
  disk_gb: number
  status: string
  suspended: boolean
  suspended_reason?: string
  suspended_at?: string
  expires_at?: string
  created_at?: string
  traffic: UsageExportTraffic
  usage?: Record<string, unknown>
}

export interface UsageExportData {
  generated_at: string
  filter_tenant: string
  count: number
  count_by_tenant: Record<string, number>
  containers: UsageExportItem[]
}

export const getUsageExport = (tenant?: string, includeUsage = true) =>
  api.get<APIResponse<UsageExportData>>('/v1/usage', {
    params: {
      ...(tenant ? { tenant } : {}),
      ...(includeUsage ? {} : { include: 'config_only' }),
    },
  })

export default api
