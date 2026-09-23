import { useCallback, useEffect, useState } from 'react'
import { Plus, RefreshCw, ShieldCheck, Trash2, X } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import {
  PolicyRule,
  PolicyTriggerRecord,
  createPolicy,
  deletePolicy,
  getPolicies,
  updatePolicy,
} from '../services/api'

const metricLabels: Record<string, string> = {
  cpu: 'CPU 使用率 (%)',
  memory: '内存使用率 (%)',
  network_rx: '入站带宽 (bps)',
  network_tx: '出站带宽 (bps)',
  disk_io: '磁盘 IO (B/s)',
}

const actionLabels: Record<string, string> = {
  raise_cpu: '提升 CPU 核数',
  raise_ram: '提升内存',
  adjust_bw: '调整带宽',
  shutdown: '自动关机',
  notify: '仅告警',
}

const scopeLabels: Record<string, string> = {
  all: '全部容器',
  tenant: '指定租户',
  container: '指定容器',
}

const emptyRule = (): PolicyRule => ({
  name: '',
  enabled: true,
  metric: 'cpu',
  operator: 'gt',
  threshold: 90,
  action: 'raise_cpu',
  adjust_vcpu: 1,
  adjust_ram_mb: 512,
  adjust_bw_mbps: 100,
  cooldown_minutes: 10,
  target_scope: 'all',
})

export default function PolicyManagement() {
  const { isSubUser } = useAuth()
  const [rules, setRules] = useState<PolicyRule[]>([])
  const [history, setHistory] = useState<PolicyTriggerRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<PolicyRule | null>(null)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)

  const fetchData = useCallback(async () => {
    try {
      const res = await getPolicies()
      if (res.data.data) {
        setRules(res.data.data.rules || [])
        setHistory(res.data.data.history || [])
      }
    } catch (err) {
      console.error(err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  const handleSave = async () => {
    if (!editing) return
    if (!editing.name.trim()) {
      setMessage({ type: 'err', text: '请填写策略名称。' })
      return
    }
    setSaving(true)
    setMessage(null)
    try {
      if (editing.id) {
        await updatePolicy(editing.id, editing)
        setMessage({ type: 'ok', text: '策略已更新。' })
      } else {
        await createPolicy(editing)
        setMessage({ type: 'ok', text: '策略已创建。' })
      }
      setEditing(null)
      fetchData()
    } catch (err: unknown) {
      console.error(err)
      const detail = (err as { response?: { data?: { message?: string } } })?.response?.data?.message
      setMessage({ type: 'err', text: `保存失败：${detail || '请查看控制台日志。'}` })
    } finally {
      setSaving(false)
    }
  }

  const handleToggle = async (rule: PolicyRule) => {
    const next = { ...rule, enabled: !rule.enabled }
    setRules((prev) => prev.map((r) => (r.id === rule.id ? next : r)))
    try {
      if (rule.id) await updatePolicy(rule.id, next)
    } catch (err) {
      console.error(err)
      setRules((prev) => prev.map((r) => (r.id === rule.id ? rule : r)))
    }
  }

  const handleDelete = async (rule: PolicyRule) => {
    if (!rule.id) return
    if (!window.confirm(`确认删除策略「${rule.name}」？`)) return
    try {
      await deletePolicy(rule.id)
      fetchData()
    } catch (err) {
      console.error(err)
    }
  }

  const patch = (partial: Partial<PolicyRule>) => {
    setEditing((prev) => (prev ? { ...prev, ...partial } : prev))
  }

  if (isSubUser) {
    return <div className="p-8 text-center text-sm text-gray-500">子用户无权访问策略管理功能</div>
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-xl font-semibold text-black">CPU / 带宽策略</h1>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { setEditing(emptyRule()); setMessage(null) }}
            className="inline-flex items-center gap-2 rounded-md bg-black px-3 py-2 text-sm text-white hover:bg-gray-800"
          >
            <Plus className="w-4 h-4" />
            新建策略
          </button>
          <button
            onClick={fetchData}
            className="inline-flex items-center gap-2 px-3 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50 text-sm"
          >
            <RefreshCw className="w-4 h-4" />
            刷新
          </button>
        </div>
      </div>

      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        <div className="px-4 py-3 border-b border-gray-200 bg-gray-50 flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-indigo-500" />
          <p className="text-xs text-gray-600">
            策略引擎每分钟采样运行中容器的指标，条件满足且超过冷却时间后自动执行动作（提升 CPU / 内存 / 带宽或关机）。规则与触发记录持久化保存。
          </p>
        </div>
        {loading ? (
          <div className="p-8 text-center text-sm text-gray-500">加载中...</div>
        ) : rules.length === 0 ? (
          <div className="p-8 text-center text-sm text-gray-500">暂无策略，点击右上角「新建策略」创建</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100 text-left text-xs font-medium text-gray-500">
                  <th className="px-4 py-2.5">启用</th>
                  <th className="px-4 py-2.5">名称</th>
                  <th className="px-4 py-2.5">条件</th>
                  <th className="px-4 py-2.5">动作</th>
                  <th className="px-4 py-2.5">作用范围</th>
                  <th className="px-4 py-2.5">冷却</th>
                  <th className="px-4 py-2.5">触发次数</th>
                  <th className="px-4 py-2.5">最近触发</th>
                  <th className="px-4 py-2.5">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {rules.map((rule) => (
                  <tr key={rule.id} className="hover:bg-gray-50">
                    <td className="px-4 py-2.5">
                      <button
                        role="switch"
                        aria-checked={rule.enabled}
                        onClick={() => handleToggle(rule)}
                        className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${rule.enabled ? 'bg-emerald-500' : 'bg-gray-300'}`}
                      >
                        <span className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform ${rule.enabled ? 'translate-x-4.5' : 'translate-x-1'}`} />
                      </button>
                    </td>
                    <td className="px-4 py-2.5 text-gray-800 whitespace-nowrap">{rule.name}</td>
                    <td className="px-4 py-2.5 text-gray-600 whitespace-nowrap">
                      {metricLabels[rule.metric]} {rule.operator === 'gt' ? '&gt;' : '&lt;'} {rule.threshold}
                    </td>
                    <td className="px-4 py-2.5 text-gray-600 whitespace-nowrap">
                      {actionLabels[rule.action]}
                      {rule.action === 'raise_cpu' && ` +${rule.adjust_vcpu}核`}
                      {rule.action === 'raise_ram' && ` +${rule.adjust_ram_mb}MB`}
                      {rule.action === 'adjust_bw' && ` → ${rule.adjust_bw_mbps}Mbps`}
                    </td>
                    <td className="px-4 py-2.5 text-gray-600 whitespace-nowrap">{scopeLabels[rule.target_scope.startsWith('tenant:') ? 'tenant' : rule.target_scope.startsWith('container:') ? 'container' : 'all']}</td>
                    <td className="px-4 py-2.5 text-gray-600 whitespace-nowrap">{rule.cooldown_minutes || 10} 分钟</td>
                    <td className="px-4 py-2.5 text-gray-600 whitespace-nowrap">{rule.triggered_count || 0}</td>
                    <td className="px-4 py-2.5 text-xs text-gray-500 whitespace-nowrap">{rule.last_triggered || '-'}</td>
                    <td className="px-4 py-2.5 whitespace-nowrap">
                      <div className="flex items-center gap-1">
                        <button
                          onClick={() => { setEditing(rule); setMessage(null) }}
                          className="rounded-md border border-gray-300 px-2 py-1 text-xs text-gray-700 hover:bg-gray-50"
                        >
                          编辑
                        </button>
                        <button
                          onClick={() => handleDelete(rule)}
                          className="rounded-md border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        <div className="px-4 py-3 border-b border-gray-200 bg-gray-50">
          <h2 className="text-sm font-semibold text-black">触发记录 ({history.length})</h2>
        </div>
        {history.length === 0 ? (
          <div className="p-8 text-center text-sm text-gray-500">暂无触发记录</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100 text-left text-xs font-medium text-gray-500">
                  <th className="px-4 py-2.5">时间</th>
                  <th className="px-4 py-2.5">策略</th>
                  <th className="px-4 py-2.5">容器</th>
                  <th className="px-4 py-2.5">指标值</th>
                  <th className="px-4 py-2.5">动作</th>
                  <th className="px-4 py-2.5">详情</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {history.map((h, i) => (
                  <tr key={`${h.time}-${i}`} className="hover:bg-gray-50">
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-500 whitespace-nowrap">{h.time}</td>
                    <td className="px-4 py-2.5 text-gray-800 whitespace-nowrap">{h.rule_name}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-700 whitespace-nowrap">{h.container}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600 whitespace-nowrap">{h.value.toFixed(2)}</td>
                    <td className="px-4 py-2.5 text-gray-600 whitespace-nowrap">{actionLabels[h.action] || h.action}</td>
                    <td className="px-4 py-2.5 text-xs text-gray-600">{h.detail}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {editing && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <div className="w-full max-w-lg overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl">
            <div className="flex items-center justify-between border-b border-gray-200 px-4 py-3">
              <h3 className="text-sm font-semibold text-black">{editing.id ? '编辑策略' : '新建策略'}</h3>
              <button onClick={() => setEditing(null)} className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="max-h-[70vh] space-y-3 overflow-auto p-4">
              <Field label="策略名称">
                <input
                  value={editing.name}
                  onChange={(e) => patch({ name: e.target.value })}
                  placeholder="例如：CPU 过载自动升配"
                  className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                />
              </Field>
              <div className="grid grid-cols-2 gap-3">
                <Field label="监控指标">
                  <select
                    value={editing.metric}
                    onChange={(e) => patch({ metric: e.target.value as PolicyRule['metric'] })}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                  >
                    {Object.entries(metricLabels).map(([k, v]) => (
                      <option key={k} value={k}>{v}</option>
                    ))}
                  </select>
                </Field>
                <Field label="触发动作">
                  <select
                    value={editing.action}
                    onChange={(e) => patch({ action: e.target.value as PolicyRule['action'] })}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                  >
                    {Object.entries(actionLabels).map(([k, v]) => (
                      <option key={k} value={k}>{v}</option>
                    ))}
                  </select>
                </Field>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <Field label="条件">
                  <select
                    value={editing.operator}
                    onChange={(e) => patch({ operator: e.target.value as PolicyRule['operator'] })}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                  >
                    <option value="gt">大于 (gt)</option>
                    <option value="lt">小于 (lt)</option>
                  </select>
                </Field>
                <Field label="阈值">
                  <input
                    type="number"
                    value={editing.threshold}
                    onChange={(e) => patch({ threshold: Number(e.target.value) })}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                  />
                </Field>
              </div>
              {editing.action === 'raise_cpu' && (
                <Field label="每次提升核数">
                  <input
                    type="number"
                    value={editing.adjust_vcpu}
                    onChange={(e) => patch({ adjust_vcpu: Number(e.target.value) })}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                  />
                </Field>
              )}
              {editing.action === 'raise_ram' && (
                <Field label="每次提升内存 (MB)">
                  <input
                    type="number"
                    value={editing.adjust_ram_mb}
                    onChange={(e) => patch({ adjust_ram_mb: Number(e.target.value) })}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                  />
                </Field>
              )}
              {editing.action === 'adjust_bw' && (
                <Field label="调整后带宽 (Mbps)">
                  <input
                    type="number"
                    value={editing.adjust_bw_mbps}
                    onChange={(e) => patch({ adjust_bw_mbps: Number(e.target.value) })}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                  />
                </Field>
              )}
              <div className="grid grid-cols-2 gap-3">
                <Field label="冷却时间 (分钟)">
                  <input
                    type="number"
                    value={editing.cooldown_minutes}
                    onChange={(e) => patch({ cooldown_minutes: Number(e.target.value) })}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                  />
                </Field>
                <Field label="作用范围">
                  <select
                    value={editing.target_scope}
                    onChange={(e) => patch({ target_scope: e.target.value })}
                    className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-black focus:outline-none"
                  >
                    <option value="all">全部容器</option>
                    <option value="tenant:default">租户 default</option>
                    <option value="container:">指定容器（手动填名称）</option>
                  </select>
                </Field>
              </div>
              {editing.target_scope.startsWith('container:') && editing.target_scope === 'container:' && (
                <p className="text-xs text-gray-400">在「作用范围」后手动补全容器名称，例如 container:web-01</p>
              )}
              {editing.target_scope.startsWith('tenant:') && (
                <p className="text-xs text-gray-400">当前作用于租户：{editing.target_scope.slice(7) || 'default'}</p>
              )}
              <div className="flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={editing.enabled}
                  onChange={(e) => patch({ enabled: e.target.checked })}
                  className="h-4 w-4 rounded border-gray-300"
                />
                <label className="text-sm text-gray-700">启用该策略</label>
              </div>
              {message && (
                <p className={`text-xs ${message.type === 'ok' ? 'text-emerald-600' : 'text-red-600'}`}>{message.text}</p>
              )}
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-200 px-4 py-3">
              <button
                onClick={() => setEditing(null)}
                className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
              >
                取消
              </button>
              <button
                onClick={handleSave}
                disabled={saving}
                className="rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50"
              >
                {saving ? '保存中...' : '保存'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-gray-500">{label}</span>
      {children}
    </label>
  )
}
