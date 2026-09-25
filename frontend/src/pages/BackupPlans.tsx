import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  CalendarClock,
  History,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Save,
  Trash2,
  X,
} from 'lucide-react'
import {
  createBackupPlan,
  deleteBackupPlan,
  getBackupPlans,
  getContainers,
  runBackupPlan,
  updateBackupPlan,
  type BackupPlan,
  type BackupPlanInput,
  type Container,
} from '../services/api'
import { useDialog } from '../components/Dialog'

// cron 预设：常见周期一键选择，避免用户手写 5 字段表达式。
const CRON_PRESETS: { label: string; cron: string }[] = [
  { label: '每日 03:00', cron: '0 3 * * *' },
  { label: '每周日 03:00', cron: '0 3 * * 0' },
  { label: '每月 1 日 03:00', cron: '0 3 1 * *' },
  { label: '每 6 小时', cron: '0 */6 * * *' },
  { label: '每 12 小时', cron: '0 */12 * * *' },
]

interface FormState {
  id: string
  name: string
  containerId: number
  cron: string
  keep: number
  enabled: boolean
}

const EMPTY_FORM: FormState = { id: '', name: '', containerId: 0, cron: '0 3 * * *', keep: 7, enabled: true }

function statusMeta(status?: string) {
  switch (status) {
    case 'success':
      return { label: '成功', className: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' }
    case 'partial':
      return { label: '部分成功', className: 'bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300' }
    case 'failed':
      return { label: '失败', className: 'bg-red-50 text-red-700 dark:bg-red-950 dark:text-red-300' }
    default:
      return { label: '—', className: 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400' }
  }
}

function formatTime(value?: string) {
  return value && value.trim() ? value : '—'
}

function formatDuration(ms: number) {
  if (!ms || ms < 0) return '—'
  if (ms < 1000) return `${ms} ms`
  const secs = ms / 1000
  if (secs < 60) return `${secs.toFixed(1)} s`
  const mins = Math.floor(secs / 60)
  return `${mins} 分 ${Math.round(secs % 60)} 秒`
}

export default function BackupPlans() {
  const { confirm, alert } = useDialog()
  const [plans, setPlans] = useState<BackupPlan[]>([])
  const [containers, setContainers] = useState<Container[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const [form, setForm] = useState<FormState | null>(null)
  const [saving, setSaving] = useState(false)
  const [historyPlan, setHistoryPlan] = useState<BackupPlan | null>(null)

  const fetchData = useCallback(async () => {
    try {
      const [planRes, containerRes] = await Promise.all([getBackupPlans(), getContainers()])
      setPlans(planRes.data.data || [])
      setContainers(containerRes.data.data || [])
      setError('')
    } catch (err: unknown) {
      setError(
        (err as { response?: { data?: { message?: string } } }).response?.data?.message || '加载备份计划失败',
      )
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => {
    void fetchData()
  }, [fetchData])

  const containerName = useMemo(() => {
    const map = new Map<number, string>()
    containers.forEach((c) => map.set(c.id, c.name))
    return (id: number) => (id > 0 ? map.get(id) || `#${id}` : '全部运行中容器')
  }, [containers])

  const openCreate = () => setForm({ ...EMPTY_FORM })
  const openEdit = (plan: BackupPlan) =>
    setForm({
      id: plan.id,
      name: plan.name,
      containerId: plan.container_id,
      cron: plan.cron,
      keep: plan.keep,
      enabled: plan.enabled,
    })

  const submit = async () => {
    if (!form) return
    if (!form.cron.trim()) {
      await alert('提示', '请填写 cron 表达式')
      return
    }
    const payload: BackupPlanInput = {
      name: form.name.trim() || undefined,
      container_id: form.containerId,
      cron: form.cron.trim(),
      keep: form.keep,
      enabled: form.enabled,
    }
    setSaving(true)
    try {
      if (form.id) {
        await updateBackupPlan(form.id, payload)
      } else {
        await createBackupPlan(payload)
      }
      setForm(null)
      await fetchData()
      await alert('已保存', '备份计划已保存')
    } catch (err: unknown) {
      await alert(
        '保存失败',
        (err as { response?: { data?: { message?: string } } }).response?.data?.message || '保存备份计划失败',
      )
    } finally {
      setSaving(false)
    }
  }

  const remove = async (plan: BackupPlan) => {
    const ok = await confirm('删除备份计划', `确认删除「${plan.name}」？该操作不会删除已生成的备份文件。`)
    if (!ok) return
    try {
      await deleteBackupPlan(plan.id)
      await fetchData()
      await alert('已删除', '备份计划已删除')
    } catch (err: unknown) {
      await alert(
        '删除失败',
        (err as { response?: { data?: { message?: string } } }).response?.data?.message || '删除失败',
      )
    }
  }

  const runNow = async (plan: BackupPlan) => {
    try {
      await runBackupPlan(plan.id)
      await alert('已开始', '备份计划已开始执行，稍后刷新查看结果')
    } catch (err: unknown) {
      await alert(
        '执行失败',
        (err as { response?: { data?: { message?: string } } }).response?.data?.message || '触发备份计划失败',
      )
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-black dark:border-white" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-black dark:text-white">
            <CalendarClock className="h-5 w-5" />
            备份计划
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            按 cron 定时为指定容器或全部运行中容器创建磁盘备份，并按保留份数自动轮换
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => {
              setRefreshing(true)
              void fetchData()
            }}
            disabled={refreshing}
            className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          >
            <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
            刷新
          </button>
          <button
            onClick={openCreate}
            className="inline-flex items-center gap-2 rounded-md bg-black px-3 py-2 text-sm text-white hover:bg-gray-800 dark:bg-white dark:text-black dark:hover:bg-gray-200"
          >
            <Plus className="h-4 w-4" />
            新建计划
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
          {error}
        </div>
      )}

      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {plans.length === 0 ? (
          <div className="px-4 py-10 text-center text-sm text-gray-400">
            暂无备份计划，点击「新建计划」创建第一个定时备份任务
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100 bg-gray-50 text-left text-xs font-medium text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                  <th className="whitespace-nowrap px-4 py-2.5">名称</th>
                  <th className="whitespace-nowrap px-4 py-2.5">目标</th>
                  <th className="whitespace-nowrap px-4 py-2.5">cron</th>
                  <th className="whitespace-nowrap px-4 py-2.5">保留</th>
                  <th className="whitespace-nowrap px-4 py-2.5">启用</th>
                  <th className="whitespace-nowrap px-4 py-2.5">上次运行</th>
                  <th className="whitespace-nowrap px-4 py-2.5">下次运行</th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-right">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {plans.map((plan) => {
                  const meta = statusMeta(plan.last_status)
                  return (
                    <tr key={plan.id} className="hover:bg-gray-50 dark:hover:bg-gray-800/60">
                      <td className="whitespace-nowrap px-4 py-2.5 font-medium text-black dark:text-white">
                        {plan.name}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-xs text-gray-600 dark:text-gray-300">
                        {containerName(plan.container_id)}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-gray-600 dark:text-gray-300">
                        {plan.cron}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-xs text-gray-600 dark:text-gray-300">
                        {plan.keep} 份
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5">
                        <span
                          className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${
                            plan.enabled
                              ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300'
                              : 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400'
                          }`}
                        >
                          {plan.enabled ? '已启用' : '已停用'}
                        </span>
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-xs">
                        <div className="flex items-center gap-1.5">
                          <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${meta.className}`}>
                            {meta.label}
                          </span>
                          <span className="text-gray-500">{formatTime(plan.last_run_at)}</span>
                        </div>
                        {plan.last_error && (
                          <p className="mt-1 max-w-[240px] truncate text-[11px] text-red-600 dark:text-red-400" title={plan.last_error}>
                            {plan.last_error}
                          </p>
                        )}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-xs text-gray-500">
                        {formatTime(plan.next_run_at)}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5">
                        <div className="flex items-center justify-end gap-1">
                          <button
                            onClick={() => runNow(plan)}
                            title="立即运行"
                            className="rounded p-1.5 text-gray-500 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white"
                          >
                            <Play className="h-4 w-4" />
                          </button>
                          <button
                            onClick={() => setHistoryPlan(plan)}
                            title="运行记录"
                            className="rounded p-1.5 text-gray-500 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white"
                          >
                            <History className="h-4 w-4" />
                          </button>
                          <button
                            onClick={() => openEdit(plan)}
                            title="编辑"
                            className="rounded p-1.5 text-gray-500 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white"
                          >
                            <Pencil className="h-4 w-4" />
                          </button>
                          <button
                            onClick={() => remove(plan)}
                            title="删除"
                            className="rounded p-1.5 text-red-500 hover:bg-red-50 dark:hover:bg-red-950"
                          >
                            <Trash2 className="h-4 w-4" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* 新建 / 编辑弹窗 */}
      {form && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={() => !saving && setForm(null)}>
          <div
            className="max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-lg bg-white p-5 shadow-xl dark:bg-gray-900"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-semibold text-black dark:text-white">
                {form.id ? '编辑备份计划' : '新建备份计划'}
              </h3>
              <button
                onClick={() => setForm(null)}
                className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white"
                aria-label="关闭"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            <div className="mt-4 space-y-3 text-xs">
              <div>
                <label className="mb-1 block font-medium text-gray-700 dark:text-gray-200">名称</label>
                <input
                  type="text"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="留空自动生成"
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
              </div>

              <div>
                <label className="mb-1 block font-medium text-gray-700 dark:text-gray-200">备份目标</label>
                <select
                  value={form.containerId}
                  onChange={(e) => setForm({ ...form, containerId: Number(e.target.value) })}
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                >
                  <option value={0}>全部运行中容器</option>
                  {containers.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}（#{c.id}）
                    </option>
                  ))}
                </select>
              </div>

              <div>
                <label className="mb-1 block font-medium text-gray-700 dark:text-gray-200">cron 表达式（分 时 日 月 周）</label>
                <input
                  type="text"
                  value={form.cron}
                  onChange={(e) => setForm({ ...form, cron: e.target.value })}
                  placeholder="0 3 * * *"
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 font-mono text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
                <div className="mt-1.5 flex flex-wrap gap-1.5">
                  {CRON_PRESETS.map((p) => (
                    <button
                      key={p.cron}
                      type="button"
                      onClick={() => setForm({ ...form, cron: p.cron })}
                      className={`rounded border px-2 py-0.5 text-[11px] ${
                        form.cron === p.cron
                          ? 'border-black bg-black text-white dark:border-white dark:bg-white dark:text-black'
                          : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800'
                      }`}
                    >
                      {p.label}
                    </button>
                  ))}
                </div>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="mb-1 block font-medium text-gray-700 dark:text-gray-200">保留份数</label>
                  <input
                    type="number"
                    min={1}
                    max={365}
                    value={form.keep}
                    onChange={(e) => setForm({ ...form, keep: Number(e.target.value) || 1 })}
                    className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                  />
                </div>
                <div className="flex items-end">
                  <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
                    <input
                      type="checkbox"
                      checked={form.enabled}
                      onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
                      className="h-4 w-4"
                    />
                    启用该计划
                  </label>
                </div>
              </div>
            </div>

            <div className="mt-5 flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setForm(null)}
                disabled={saving}
                className="rounded-md border border-gray-200 px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
              >
                取消
              </button>
              <button
                type="button"
                onClick={submit}
                disabled={saving}
                className="inline-flex items-center gap-1.5 rounded-md bg-black px-3 py-1.5 text-xs font-medium text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black"
              >
                <Save className="h-3.5 w-3.5" />
                {saving ? '保存中…' : '保存'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 运行记录弹窗 */}
      {historyPlan && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={() => setHistoryPlan(null)}>
          <div
            className="max-h-[85vh] w-full max-w-xl overflow-hidden rounded-lg bg-white shadow-xl dark:bg-gray-900"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-3.5 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">
                运行记录 · {historyPlan.name}
              </h3>
              <button
                onClick={() => setHistoryPlan(null)}
                className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white"
                aria-label="关闭"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="max-h-[70vh] overflow-y-auto px-5 py-4">
              {!historyPlan.runs || historyPlan.runs.length === 0 ? (
                <div className="rounded-md border border-gray-200 bg-gray-50 px-3 py-3 text-xs text-gray-400 dark:border-gray-700 dark:bg-gray-800">
                  该计划还没有运行记录
                </div>
              ) : (
                <ul className="space-y-2">
                  {historyPlan.runs.map((run, index) => {
                    const meta = statusMeta(run.status)
                    return (
                      <li
                        key={`${run.at}-${index}`}
                        className="rounded-md border border-gray-100 bg-gray-50 px-3 py-2 text-xs dark:border-gray-800 dark:bg-gray-800/60"
                      >
                        <div className="flex items-center justify-between gap-2">
                          <div className="flex items-center gap-2">
                            <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${meta.className}`}>
                              {meta.label}
                            </span>
                            <span className="font-mono text-gray-500">{formatTime(run.at)}</span>
                          </div>
                          <span className="text-gray-500">耗时 {formatDuration(run.duration_ms)}</span>
                        </div>
                        <div className="mt-1 text-gray-600 dark:text-gray-300">
                          成功 {run.backups} 份{run.failed > 0 ? ` · 失败 ${run.failed} 份` : ''}
                        </div>
                        {run.error && (
                          <p className="mt-1 break-all text-[11px] text-red-600 dark:text-red-400">{run.error}</p>
                        )}
                      </li>
                    )
                  })}
                </ul>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}