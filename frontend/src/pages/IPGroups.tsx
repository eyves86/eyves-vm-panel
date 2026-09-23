import { useCallback, useEffect, useState } from 'react'
import { ArrowLeftRight, Network, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import {
  createIPGroup,
  deleteIPGroup,
  getIPGroups,
  ipGroupFailover,
  updateIPGroup,
  type IPGroup,
} from '../services/api'

export default function IPGroups() {
  const [groups, setGroups] = useState<IPGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [draft, setDraft] = useState<{ name: string; enable: string; standby: string }>({ name: '', enable: '', standby: '' })
  const [error, setError] = useState('')

  const fetchData = useCallback(async () => {
    try {
      const res = await getIPGroups()
      setGroups(res.data.data || [])
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '加载 IP 组失败')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { fetchData() }, [fetchData])

  const splitList = (raw: string): string[] =>
    raw.split(',').map((s) => s.trim()).filter((s) => s.length > 0)

  const openCreate = () => {
    setCreating(true)
    setEditingId(null)
    setDraft({ name: '', enable: '', standby: '' })
    setError('')
  }

  const openEdit = (g: IPGroup) => {
    setCreating(false)
    setEditingId(g.id)
    setDraft({
      name: g.name,
      enable: (g.enable || []).join(', '),
      standby: (g.standby || []).join(', '),
    })
    setError('')
  }

  const cancel = () => {
    setCreating(false)
    setEditingId(null)
    setDraft({ name: '', enable: '', standby: '' })
    setError('')
  }

  const submit = async () => {
    if (!draft.name.trim()) {
      setError('名称不能为空')
      return
    }
    const payload = {
      name: draft.name.trim(),
      enable: splitList(draft.enable),
      standby: splitList(draft.standby),
    }
    try {
      if (editingId) {
        await updateIPGroup(editingId, payload)
      } else {
        await createIPGroup(payload)
      }
      setError('')
      cancel()
      await fetchData()
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '保存失败，请稍后重试')
    }
  }

  const remove = async (id: string, name: string) => {
    if (!window.confirm(`确认删除 IP 组 ${name}？`)) return
    try {
      await deleteIPGroup(id)
    } catch (err: unknown) {
      alert((err as { response?: { data?: { message?: string } } }).response?.data?.message || '删除失败')
      return
    }
    await fetchData()
  }

  const failover = async (id: string, ip: string) => {
    try {
      await ipGroupFailover(id, ip)
    } catch (err: unknown) {
      alert((err as { response?: { data?: { message?: string } } }).response?.data?.message || '故障切换失败')
      return
    }
    await fetchData()
    alert(`已切换到 ${ip}`)
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
            <Network className="h-5 w-5" />IP 组 / 故障切换
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">分组管理公网 IP，支持主备 IP 手动故障切换</p>
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
            <Plus className="h-4 w-4" />新建 IP 组
          </button>
        </div>
      </div>

      {(creating || editingId) && (
        <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
          <h2 className="mb-4 text-sm font-semibold text-black dark:text-white">{editingId ? '编辑 IP 组' : '新建 IP 组'}</h2>
          {error && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>}
          <div className="grid gap-4 md:grid-cols-3">
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">名称 <span className="text-red-500">*</span></label>
              <input type="text" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder="web-lb" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">生效 IP（逗号分隔）</label>
              <input type="text" value={draft.enable} onChange={(e) => setDraft((d) => ({ ...d, enable: e.target.value }))} placeholder="1.2.3.4, 1.2.3.5" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">备用 IP（逗号分隔）</label>
              <input type="text" value={draft.standby} onChange={(e) => setDraft((d) => ({ ...d, standby: e.target.value }))} placeholder="1.2.3.6, 1.2.3.7" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
          </div>
          <div className="mt-4 flex justify-end gap-2">
            <button onClick={cancel} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300">取消</button>
            <button onClick={submit} className="rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 dark:bg-white dark:text-black">{editingId ? '保存修改' : '创建 IP 组'}</button>
          </div>
        </div>
      )}

      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {groups.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><Network className="h-7 w-7" /></div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-200">暂无 IP 组</div>
            <div className="mt-1 text-xs text-gray-400">点击「新建 IP 组」创建第一个 IP 组</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[960px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">名称</th>
                  <th className="px-4 py-3 text-left font-medium">生效 IP</th>
                  <th className="px-4 py-3 text-left font-medium">备用 IP</th>
                  <th className="px-4 py-3 text-left font-medium">当前状态</th>
                  <th className="px-4 py-3 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {groups.map((g) => (
                  <tr key={g.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="px-4 py-3 font-mono text-xs font-medium text-black dark:text-black">{g.name}</td>
                    <td className="px-4 py-3 text-gray-800 dark:text-gray-100">{(g.enable || []).join(', ') || '—'}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{(g.standby || []).join(', ') || '—'}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{g.fault_open || '—'}</td>
                    <td className="px-4 py-3 text-right">
                      <button onClick={() => openEdit(g)} className="mr-2 inline-flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300"><Pencil className="h-3 w-3" />编辑</button>
                      <button onClick={() => remove(g.id, g.name)} className="mr-2 inline-flex items-center gap-1 rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400"><Trash2 className="h-3 w-3" />删除</button>
                      {(g.standby || []).map((ip) => (
                        <button key={ip} onClick={() => failover(g.id, ip)} className="inline-flex items-center gap-1 rounded border border-blue-200 px-2 py-1 text-xs text-blue-600 hover:bg-blue-50 dark:border-blue-800 dark:text-blue-400"><ArrowLeftRight className="h-3 w-3" />切换到 {ip}</button>
                      ))}
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