import { useCallback, useEffect, useState } from 'react'
import { Building2, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import {
  createTenant,
  deleteTenant,
  getTenants,
  updateTenant,
  type Tenant,
} from '../services/api'

const emptyDraft = { id: '', name: '', description: '', container_quota: 0, vcpu_quota: 0, ram_quota_mb: 0, disk_quota_gb: 0 }

export default function Tenants() {
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [draft, setDraft] = useState<typeof emptyDraft>({ ...emptyDraft })
  const [error, setError] = useState('')

  const fetchData = useCallback(async () => {
    try {
      const res = await getTenants()
      setTenants(res.data.data || [])
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '加载租户失败')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { fetchData() }, [fetchData])

  const openCreate = () => {
    setCreating(true)
    setEditingId(null)
    setDraft({ ...emptyDraft })
    setError('')
  }

  const openEdit = (t: Tenant) => {
    setCreating(false)
    setEditingId(t.id)
    setDraft({
      id: t.id,
      name: t.name,
      description: t.description || '',
      container_quota: t.container_quota || 0,
      vcpu_quota: t.vcpu_quota || 0,
      ram_quota_mb: t.ram_quota_mb || 0,
      disk_quota_gb: t.disk_quota_gb || 0,
    })
    setError('')
  }

  const cancel = () => {
    setCreating(false)
    setEditingId(null)
    setDraft({ ...emptyDraft })
    setError('')
  }

  const setNum = (key: string, raw: string) => {
    const v = Math.max(0, Number(raw) || 0)
    setDraft((d) => ({ ...d, [key]: v }))
  }

  const submit = async () => {
    if (!draft.name.trim()) {
      setError('租户名称不能为空')
      return
    }
    try {
      if (editingId) {
        await updateTenant(editingId, {
          name: draft.name,
          description: draft.description,
          container_quota: draft.container_quota,
          vcpu_quota: draft.vcpu_quota,
          ram_quota_mb: draft.ram_quota_mb,
          disk_quota_gb: draft.disk_quota_gb,
        })
      } else {
        if (!draft.id.trim()) {
          setError('租户 ID 不能为空')
          return
        }
        await createTenant({
          id: draft.id,
          name: draft.name,
          description: draft.description,
          container_quota: draft.container_quota,
          vcpu_quota: draft.vcpu_quota,
          ram_quota_mb: draft.ram_quota_mb,
          disk_quota_gb: draft.disk_quota_gb,
        })
      }
      setError('')
      cancel()
      await fetchData()
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '保存失败，请稍后重试')
    }
  }

  const remove = async (id: string) => {
    if (!window.confirm(`确认删除租户 ${id}？仅可删除不含容器的空租户。`)) return
    try {
      await deleteTenant(id)
    } catch (err: unknown) {
      alert((err as { response?: { data?: { message?: string } } }).response?.data?.message || '删除失败')
      return
    }
    await fetchData()
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-black" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-black dark:text-white">
            <Building2 className="h-5 w-5" />多租户管理
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">创建租户并设定资源配额，容器归属租户后将受配额约束</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { setRefreshing(true); fetchData() }}
            disabled={refreshing}
            className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          >
            <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />刷新
          </button>
          <button onClick={openCreate} className="inline-flex items-center gap-2 rounded-md bg-black px-3 py-2 text-sm text-white hover:bg-gray-800 dark:bg-white dark:text-black">
            <Plus className="h-4 w-4" />新建租户
          </button>
        </div>
      </div>

      {(creating || editingId) && (
        <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
          <h2 className="mb-4 text-sm font-semibold text-black dark:text-white">{editingId ? '编辑租户' : '新建租户'}</h2>
          {error && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>}
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">租户 ID</label>
              <input type="text" value={draft.id} disabled={!!editingId} onChange={(e) => setDraft((d) => ({ ...d, id: e.target.value.trim() }))} placeholder="tenant-a" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">名称</label>
              <input type="text" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder="租户 A" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            <div className="md:col-span-2">
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">描述</label>
              <input type="text" value={draft.description} onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            <Field label="容器配额（0=不限）" value={draft.container_quota} onChange={(v) => setNum('container_quota', v)} />
            <Field label="vCPU 配额（0=不限）" value={draft.vcpu_quota} onChange={(v) => setNum('vcpu_quota', v)} />
            <Field label="内存配额 MB（0=不限）" value={draft.ram_quota_mb} onChange={(v) => setNum('ram_quota_mb', v)} />
            <Field label="磁盘配额 GB（0=不限）" value={draft.disk_quota_gb} onChange={(v) => setNum('disk_quota_gb', v)} />
          </div>
          <div className="mt-4 flex justify-end gap-2">
            <button onClick={cancel} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300">取消</button>
            <button onClick={submit} className="rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 dark:bg-white dark:text-black">{editingId ? '保存修改' : '创建租户'}</button>
          </div>
        </div>
      )}

      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {tenants.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><Building2 className="h-7 w-7" /></div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-200">暂无租户</div>
            <div className="mt-1 text-xs text-gray-400">点击「新建租户」创建第一个租户</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[860px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">ID</th>
                  <th className="px-4 py-3 text-left font-medium">名称</th>
                  <th className="px-4 py-3 text-left font-medium">容器</th>
                  <th className="px-4 py-3 text-left font-medium">vCPU</th>
                  <th className="px-4 py-3 text-left font-medium">内存</th>
                  <th className="px-4 py-3 text-left font-medium">磁盘</th>
                  <th className="px-4 py-3 text-left font-medium">状态</th>
                  <th className="px-4 py-3 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {tenants.map((t) => (
                  <tr key={t.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="px-4 py-3 font-mono text-xs font-medium text-black dark:text-white">{t.id}</td>
                    <td className="px-4 py-3">
                      <div className="text-gray-800 dark:text-gray-100">{t.name}</div>
                      {t.description && <div className="text-xs text-gray-400">{t.description}</div>}
                    </td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{t.usage_containers ?? 0} / {t.container_quota || '∞'}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{t.usage_vcpu ?? 0} / {t.vcpu_quota || '∞'}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{formatMB(t.usage_ram_mb ?? 0)} / {t.ram_quota_mb ? formatMB(t.ram_quota_mb) : '∞'}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{t.usage_disk_gb ?? 0} / {t.disk_quota_gb ? `${t.disk_quota_gb}GB` : '∞'}</td>
                    <td className="px-4 py-3">
                      <span className={`rounded px-2 py-1 text-xs ${t.enabled ? 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-300' : 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-300'}`}>{t.enabled ? '启用' : '禁用'}</span>
                    </td>
                    <td className="px-4 py-3 text-right">
                      <button onClick={() => openEdit(t)} className="mr-2 inline-flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300"><Pencil className="h-3 w-3" />编辑</button>
                      <button onClick={() => remove(t.id)} className="inline-flex items-center gap-1 rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400"><Trash2 className="h-3 w-3" />删除</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

function Field({ label, value, onChange }: { label: string; value: number; onChange: (raw: string) => void }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{label}</label>
      <input type="number" min={0} value={value} onChange={(e) => onChange(e.target.value)} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
    </div>
  )
}

function formatMB(mb: number): string {
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)}GB`
  return `${mb}MB`
}