import { useState } from 'react'
import { BadgeCheck, ChevronDown, ChevronUp, Play, Terminal } from 'lucide-react'
import {
  V2Error,
  v2ListInstances,
  v2ListNodes,
  v2MetricsHost,
  v2MetricsSummary,
  v2Schedule,
  v2SystemHealth,
  v2SystemInfo,
} from '../services/apiV2'

// APIV2Panel —— 面板内展示并试跑 EyvesCloud API v2（集成专用，契约稳定）。
//
// 设计意图：让运维/集成方在面板里就能确认 v2 的可用性与返回结构，不必先写脚本；
// 「试跑」只调用只读端点（列表/指标/系统信息/调度决策），不产生任何副作用。

type V2Module = { title: string; endpoints: Array<[string, string, string]> }

const V2_MODULES: V2Module[] = [
  {
    title: '认证（3）',
    endpoints: [
      ['POST', '/api/v2/auth/login', '登录换取 access_token（支持两步验证 code）'],
      ['POST', '/api/v2/auth/logout', '退出登录'],
      ['GET', '/api/v2/auth/me', '当前身份 / 角色 / 16 个功能点（前端按钮据此渲染）'],
    ],
  },
  {
    title: '实例（29）',
    endpoints: [
      ['GET', '/api/v2/instances', '列表：status/runtime/node_id/owner/q 过滤 + pagination + summary'],
      ['POST', '/api/v2/instances', '创建：runtime/template_id/node_id(auto)/count/cpu/memory/disk/带宽/流量/NAT/登录方式'],
      ['GET', '/api/v2/instances/{id}', '详情（含快照、备份、防火墙规则）'],
      ['PATCH', '/api/v2/instances/{id}', '部分更新（vcpu/memory_mb/disk_gb/备注/到期/租户）'],
      ['DELETE', '/api/v2/instances/{id}', '删除（异步任务）'],
      ['POST', '/api/v2/instances/{id}/power', '电源：start/stop/shutdown/restart/hard-stop/hard-restart'],
      ['POST', '/api/v2/instances/{id}/reinstall', '重装系统'],
      ['POST', '/api/v2/instances/{id}/reset-password', '重置 root 密码（留空自动生成，仅返回一次）'],
      ['POST', '/api/v2/instances/{id}/console', '控制台票据（ssh/vnc，60s 一次性）'],
      ['GET', '/api/v2/instances/{id}/metrics', '实时指标（CPU/内存/网络/磁盘）'],
      ['GET', '/api/v2/instances/{id}/usage', '流量用量与配额'],
      ['GET', '/api/v2/instances/{id}/events', '实例审计事件'],
      ['GET', '/api/v2/instances/{id}/xml', 'KVM libvirt XML'],
      ['GET/POST', '/api/v2/instances/{id}/snapshots', '快照列表 / 创建'],
      ['POST', '/api/v2/instances/{id}/snapshots/{sid}/restore', '从快照恢复'],
      ['DELETE', '/api/v2/instances/{id}/snapshots/{sid}', '删除快照'],
      ['GET/POST', '/api/v2/instances/{id}/backups', '备份列表 / 创建'],
      ['DELETE', '/api/v2/instances/{id}/backups/{bid}', '删除备份'],
      ['POST', '/api/v2/instances/{id}/clone', '克隆实例'],
      ['POST/DELETE', '/api/v2/instances/{id}/rescue', '进入 / 退出救援模式'],
      ['POST/DELETE', '/api/v2/instances/{id}/lock', '锁定 / 解锁'],
      ['PUT', '/api/v2/instances/{id}/network', 'NAT 端口 / 公网 IPv4 / IPv6 数量调整'],
      ['GET/PUT', '/api/v2/instances/{id}/security-groups', '查询 / 设置安全组绑定'],
      ['POST', '/api/v2/instances/batch', '批量操作：power / delete / reinstall'],
    ],
  },
  {
    title: '节点 / 分组 / 区域 / 调度（20）',
    endpoints: [
      ['GET', '/api/v2/nodes', '节点列表（状态/维护/分组/区域过滤 + summary）'],
      ['POST', '/api/v2/nodes', '添加节点（quick 一键接入 / manual 手工接入）'],
      ['GET', '/api/v2/nodes/schedule', '放置调度决策（过滤+评分+候选理由）'],
      ['GET/PATCH/DELETE', '/api/v2/nodes/{id}', '详情 / 修改 / 删除'],
      ['POST', '/api/v2/nodes/{id}/maintenance', '维护模式开关'],
      ['POST', '/api/v2/nodes/{id}/install-key', '换发一次性安装密钥（返回接入命令）'],
      ['GET', '/api/v2/nodes/{id}/metrics', '节点资源指标'],
      ['GET', '/api/v2/nodes/{id}/instances', '该节点上的实例'],
      ['GET/POST', '/api/v2/node-groups', '节点分组列表 / 创建'],
      ['PUT', '/api/v2/node-groups/{id}/nodes', '设置分组成员（整体替换）'],
      ['GET/POST', '/api/v2/regions', '区域列表 / 创建'],
    ],
  },
  {
    title: '镜像 / ISO / 存储 / SSH 密钥 / 安全组 / IP 池（23）',
    endpoints: [
      ['GET/POST', '/api/v2/images', '镜像目录 / 新增自定义源（URL+SHA256）'],
      ['POST', '/api/v2/images/{id}/download', '触发镜像下载（异步）'],
      ['PATCH/DELETE', '/api/v2/images/{id}', '启用停用 / 删除自定义源'],
      ['GET/DELETE', '/api/v2/iso-images', 'ISO 目录 / 删除'],
      ['GET', '/api/v2/storage-pools', '存储池列表（含路径可用性）'],
      ['GET/POST/DELETE', '/api/v2/ssh-keys', 'SSH 公钥管理（服务端计算指纹）'],
      ['GET/POST', '/api/v2/security-groups', '安全组列表 / 创建'],
      ['GET/POST/DELETE', '/api/v2/security-groups/{id}/rules', '安全组规则管理'],
      ['GET/POST/DELETE', '/api/v2/ip-pools', '公网 IPv4 池 + IPv6 前缀管理'],
    ],
  },
  {
    title: '任务 / 备份 / Webhook / 用户 / 管理员 / API Key / 审计 / 监控 / 系统（30）',
    endpoints: [
      ['GET', '/api/v2/tasks', '任务列表（状态/类型/实例过滤 + summary）'],
      ['GET', '/api/v2/tasks/{id}', '任务详情（含执行日志）'],
      ['GET', '/api/v2/backups', '备份总览 / 恢复 / 删除'],
      ['GET/POST/PATCH/DELETE', '/api/v2/webhooks', '事件订阅管理（密钥仅返回一次）'],
      ['GET/POST/PATCH/DELETE', '/api/v2/users', '子用户管理（含容器绑定、轮换口令）'],
      ['GET/POST/PATCH/DELETE', '/api/v2/admins', '管理员账号管理（仅主管理员）'],
      ['GET/POST/DELETE', '/api/v2/api-keys', 'API Key 管理（授予范围不超调用方权限）'],
      ['GET', '/api/v2/audit-logs', '审计日志（操作人/动作/仅失败过滤）'],
      ['GET', '/api/v2/metrics/host|instances|summary', '监控指标'],
      ['GET', '/api/v2/system/info|health|update-check', '系统信息 / 健康检查 / 版本检测'],
    ],
  },
]

type ProbeEndpoint = { label: string; run: () => Promise<unknown> }

const PROBES: ProbeEndpoint[] = [
  { label: 'GET /instances（前 5 条）', run: () => v2ListInstances({ page_size: 5 }) },
  { label: 'GET /nodes', run: () => v2ListNodes() },
  { label: 'GET /nodes/schedule?ram_mb=1024&virt=lxc', run: () => v2Schedule({ ram_mb: 1024, virt: 'lxc' }) },
  { label: 'GET /metrics/summary', run: () => v2MetricsSummary() },
  { label: 'GET /metrics/host', run: () => v2MetricsHost() },
  { label: 'GET /system/info', run: () => v2SystemInfo() },
  { label: 'GET /system/health', run: () => v2SystemHealth() },
]

export default function APIV2Panel() {
  const [open, setOpen] = useState(false)
  const [running, setRunning] = useState('')
  const [result, setResult] = useState('')
  const [error, setError] = useState('')

  const runProbe = async (probe: ProbeEndpoint) => {
    setRunning(probe.label)
    setError('')
    setResult('')
    try {
      const data = await probe.run()
      setResult(JSON.stringify(data, null, 2))
    } catch (err) {
      if (err instanceof V2Error) {
        setError(`[${err.status}] ${err.code}：${err.message}${err.requestId ? `（request_id=${err.requestId}）` : ''}`)
      } else {
        setError(err instanceof Error ? err.message : '调用失败')
      }
    } finally {
      setRunning('')
    }
  }

  return (
    <div className="rounded-lg border border-gray-200 bg-white">
      <button
        onClick={() => setOpen((value) => !value)}
        className="flex w-full items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 text-left"
      >
        <h2 className="flex items-center gap-2 text-sm font-semibold text-black">
          <BadgeCheck className="h-4 w-4" />
          API v2（集成专用 · 契约稳定 · 105 个端点）
        </h2>
        {open ? <ChevronUp className="h-4 w-4 text-gray-400" /> : <ChevronDown className="h-4 w-4 text-gray-400" />}
      </button>

      {open && (
        <div className="space-y-6 p-5">
          <div className="rounded-lg bg-gray-900 p-4 font-mono text-xs text-gray-100">
            <div className="flex items-center gap-2 text-gray-400">
              <Terminal className="h-3.5 w-3.5" />
              统一约定：路径 /api/v2/*，认证 Bearer access_token 或 X-API-Key，
              响应 {`{success, code, message, data, request_id}`}，列表 data.items + data.pagination
            </div>
            <div className="mt-2">curl -s {window.location.origin}/api/v2/instances?page_size=5 -H "Authorization: Bearer &lt;access_token&gt;"</div>
          </div>

          <section>
            <h3 className="mb-2 text-sm font-semibold text-black">试跑只读端点（不产生副作用）</h3>
            <div className="flex flex-wrap gap-2">
              {PROBES.map((probe) => (
                <button
                  key={probe.label}
                  onClick={() => void runProbe(probe)}
                  disabled={running !== ''}
                  className="inline-flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs text-gray-700 hover:border-brand-500 hover:bg-brand-50 disabled:opacity-50"
                >
                  <Play className="h-3 w-3" />
                  {probe.label}
                </button>
              ))}
            </div>
            {running && <div className="mt-2 text-xs text-gray-500">调用中：{running}</div>}
            {error && <div className="mt-2 rounded border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700">{error}</div>}
            {result && (
              <pre className="mt-2 max-h-72 overflow-auto rounded border border-gray-200 bg-gray-50 p-3 font-mono text-[11px] leading-relaxed text-gray-800">
                {result}
              </pre>
            )}
          </section>

          {V2_MODULES.map((module) => (
            <section key={module.title}>
              <h3 className="mb-2 text-sm font-semibold text-black">{module.title}</h3>
              <div className="overflow-hidden rounded-lg border border-gray-200">
                {module.endpoints.map(([method, path, desc]) => (
                  <div
                    key={`${method}-${path}`}
                    className="grid gap-2 border-b border-gray-100 px-3 py-2 text-xs last:border-b-0 md:grid-cols-[110px_minmax(260px,1fr)_minmax(200px,1.4fr)]"
                  >
                    <span className="w-fit rounded border border-blue-200 bg-blue-50 px-1.5 py-0.5 font-mono font-bold text-blue-700">
                      {method}
                    </span>
                    <code className="min-w-0 break-all font-mono text-gray-800">{path}</code>
                    <span className="text-gray-500">{desc}</span>
                  </div>
                ))}
              </div>
            </section>
          ))}
        </div>
      )}
    </div>
  )
}
