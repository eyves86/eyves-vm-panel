import { useCallback, useEffect, useState } from 'react'
import { Globe, Plus, RefreshCw, Trash2 } from 'lucide-react'
import {
  createRegion,
  deleteRegion,
  getRegions,
  type Region,
} from '../services/api'

export default function Regions() {
  const [regions, setRegions] = useState<Region[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [creating, setCreating] = useState(false)
  const [draft, setDraft] = useState<{ name: string; location: string }>({ name: '', location: '' })
  const [error, setError] = useState('')

  const fetchData = useCallback(async () => {
    try {
      const res = await getRegions()
      setRegions(res.data.data || [])
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '加载区域失败')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { fetchData() }, [fetchData])

  const openCreate = () => {
    setCreating(true)
    setDraft({ name: '', location: '' })
    setError('')
  }

  const cancel = () => {
    setCreating(false)
    setDraft({ name: '', location: '' })
    setError('')
  }

  const submit = async () => {
    if (!draft.name.trim()) {
      setError('区域名称不能为空')
      return
    }
    try {
      await createRegion({
        name: draft.name.trim(),
        location: draft.location.trim() || undefined,
      })
      setError('')
      cancel()
      await fetchData()
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '保存失败，请稍后重试')
    }
  }

  const remove = async (id: string) => {
    if (!window.confirm(`确认删除区域 ${id}？`)) return
    try {
      await deleteRegion(id)
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
            <Globe className="h-5 w-5" />区域管理
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">把节点/存储/容器按地域分组管理</p>
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
            <Plus className="h-4 w-4" />新建区域
          </button>
        </div>
      </div>

      {creating && (
        <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
          <h2 className="mb-4 text-sm font-semibold text-black dark:text-white">新建区域</h2>
          {error && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>}
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">名称 <span className="text-red-500">*</span></label>
              <input type="text" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder="cn-north" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">位置（可选）</label>
              <input type="text" value={draft.location} onChange={(e) => setDraft((d) => ({ ...d, location: e.target.value }))} placeholder="华北 · 北京" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
          </div>
          <div className="mt-4 flex justify-end gap-2">
            <button onClick={cancel} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300">取消</button>
            <button onClick={submit} className="rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 dark:bg-white dark:text-black">创建区域</button>
          </div>
        </div>
      )}

      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {regions.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><Globe className="h-7 w-7" /></div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-200">暂无区域</div>
            <div className="mt-1 text-xs text-gray-400">点击「新建区域」创建第一个区域</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">ID</th>
                  <th className="px-4 py-3 text-left font-medium">名称</th>
                  <th className="px-4 py-3 text-left font-medium">位置</th>
                  <th className="px-4 py-3 text-left font-medium">创建时间</th>
                  <th className="px-4 py-3 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {regions.map((r) => (
                  <tr key={r.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="px-4 py-3 font-mono text-xs font-medium text-black dark:text-white">{r.id}</td>
                    <td className="px-4 py-3 text-gray-800 dark:text-gray-100">{r.name}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{r.location || '—'}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{r.created_at || '—'}</td>
                    <td className="px-4 py-3 text-right">
                      <button onClick={() => remove(r.id)} className="inline-flex items-center gap-1 rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400"><Trash2 className="h-3 w-3" />删除</button>
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