import { useCallback, useEffect, useState } from 'react'
import { Disc3, Plus, RefreshCw, Trash2, Upload, ArrowDownToLine } from 'lucide-react'
import {
  createISO,
  deleteISO,
  getISOs,
  uploadISO,
  type ISOFile,
} from '../services/api'

export default function ISOs() {
  const [isos, setIsos] = useState<ISOFile[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [creating, setCreating] = useState(false)
  const [busy, setBusy] = useState(false)
  const [mode, setMode] = useState<'url' | 'upload'>('url')
  const [draft, setDraft] = useState<{ name: string; os: string; url: string }>({ name: '', os: '', url: '' })
  const [file, setFile] = useState<File | null>(null)
  const [error, setError] = useState('')

  const fetchData = useCallback(async () => {
    try {
      const res = await getISOs()
      setIsos(res.data.data || [])
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '加载 ISO 失败')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { fetchData() }, [fetchData])

  const openCreate = () => {
    setCreating(true)
    setMode('url')
    setDraft({ name: '', os: '', url: '' })
    setFile(null)
    setError('')
  }

  const cancel = () => {
    setCreating(false)
    setDraft({ name: '', os: '', url: '' })
    setFile(null)
    setError('')
  }

  const submitUrl = async () => {
    if (!draft.name.trim()) {
      setError('名称不能为空')
      return
    }
    if (!draft.url.trim()) {
      setError('下载地址不能为空')
      return
    }
    setBusy(true)
    setError('')
    try {
      await createISO({
        name: draft.name.trim(),
        os: draft.os.trim() || undefined,
        url: draft.url.trim(),
      })
      cancel()
      await fetchData()
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '下载失败，请稍后重试')
    } finally {
      setBusy(false)
    }
  }

  const submitUpload = async () => {
    if (!draft.name.trim()) {
      setError('名称不能为空')
      return
    }
    if (!file) {
      setError('请选择要上传的文件')
      return
    }
    setBusy(true)
    setError('')
    try {
      await uploadISO(file, draft.name.trim(), draft.os.trim() || undefined)
      cancel()
      await fetchData()
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '上传失败，请稍后重试')
    } finally {
      setBusy(false)
    }
  }

  const switchMode = (m: 'url' | 'upload') => {
    setMode(m)
    setError('')
  }

  const remove = async (id: string, name: string) => {
    if (!window.confirm(`确认删除 ISO ${name}？将同时移除本地文件。`)) return
    try {
      await deleteISO(id)
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
            <Disc3 className="h-5 w-5" />ISO 镜像
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">管理 KVM 安装/驱动 ISO，可挂载到虚拟机</p>
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
            <Plus className="h-4 w-4" />添加 ISO
          </button>
        </div>
      </div>

      {creating && (
        <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
          <h2 className="mb-4 text-sm font-semibold text-black dark:text-white">添加 ISO</h2>
          {error && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>}
          <div className="mb-4 flex gap-2">
            <button
              onClick={() => switchMode('url')}
              className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm ${mode === 'url' ? 'bg-black text-white dark:bg-white dark:text-black' : 'border border-gray-300 text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300'}`}
            >
              <ArrowDownToLine className="h-4 w-4" />在线下载
            </button>
            <button
              onClick={() => switchMode('upload')}
              className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm ${mode === 'upload' ? 'bg-black text-white dark:bg-white dark:text-black' : 'border border-gray-300 text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300'}`}
            >
              <Upload className="h-4 w-4" />本地上传
            </button>
          </div>
          <div className="grid gap-4 md:grid-cols-3">
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">名称 <span className="text-red-500">*</span></label>
              <input type="text" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder="ubuntu-server-24" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">OS（可选）</label>
              <input type="text" value={draft.os} onChange={(e) => setDraft((d) => ({ ...d, os: e.target.value }))} placeholder="linux" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            {mode === 'url' ? (
              <div>
                <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">下载地址 <span className="text-red-500">*</span></label>
                <input type="text" value={draft.url} onChange={(e) => setDraft((d) => ({ ...d, url: e.target.value }))} placeholder="https://..." className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
              </div>
            ) : (
              <div>
                <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">选择文件 <span className="text-red-500">*</span></label>
                <input
                  type="file"
                  onChange={(e) => setFile(e.target.files?.[0] || null)}
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black file:mr-2 file:rounded file:border-0 file:bg-gray-100 file:px-3 file:py-1 file:text-xs dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:file:bg-gray-800"
                />
              </div>
            )}
          </div>
          <div className="mt-4 flex justify-end gap-2">
            <button onClick={cancel} disabled={busy} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300">取消</button>
            <button onClick={mode === 'url' ? submitUrl : submitUpload} disabled={busy} className="inline-flex items-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black">
              {busy && <RefreshCw className="h-4 w-4 animate-spin" />}
              {busy ? (mode === 'url' ? '下载中…' : '上传中…') : (mode === 'url' ? '创建并下载' : '上传')}
            </button>
          </div>
        </div>
      )}

      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {isos.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><Disc3 className="h-7 w-7" /></div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-200">暂无 ISO</div>
            <div className="mt-1 text-xs text-gray-400">点击「新建 ISO」添加第一个镜像</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">名称</th>
                  <th className="px-4 py-3 text-left font-medium">OS</th>
                  <th className="px-4 py-3 text-left font-medium">大小</th>
                  <th className="px-4 py-3 text-left font-medium">创建时间</th>
                  <th className="px-4 py-3 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {isos.map((iso) => (
                  <tr key={iso.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="px-4 py-3">
                      <div className="text-gray-800 dark:text-gray-100">{iso.name}</div>
                      <div className="font-mono text-xs text-gray-400">{iso.path}</div>
                    </td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{iso.os || '—'}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{formatSize(iso.size_bytes)}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{iso.created_at || '—'}</td>
                    <td className="px-4 py-3 text-right">
                      <button onClick={() => remove(iso.id, iso.name)} className="inline-flex items-center gap-1 rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400"><Trash2 className="h-3 w-3" />删除</button>
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

function formatSize(bytes: number | undefined): string {
  if (!bytes || bytes <= 0) return '—'
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)}GB`
  return `${(bytes / 1024 ** 2).toFixed(1)}MB`
}