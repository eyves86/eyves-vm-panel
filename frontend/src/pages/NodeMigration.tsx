import { useCallback, useEffect, useState } from 'react'
import { Download, FileJson, Info, RefreshCw, Upload } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import {
  Container,
  getContainers,
  migrateExport,
  migrateImport,
  MigrateBundle,
} from '../services/api'

export default function NodeMigration() {
  const { isSubUser } = useAuth()
  const [containers, setContainers] = useState<Container[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)
  const [importBundle, setImportBundle] = useState<MigrateBundle | null>(null)
  const [fileName, setFileName] = useState('')

  const fetchContainers = useCallback(async () => {
    try {
      const res = await getContainers()
      if (res.data.data) setContainers(res.data.data)
    } catch (err) {
      console.error(err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchContainers()
  }, [fetchContainers])

  const handleExport = async (container: Container) => {
    setBusy(true)
    setMessage(null)
    try {
      const res = await migrateExport(container.id)
      const bundle: MigrateBundle = res.data
      const blob = new Blob([JSON.stringify(bundle, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${container.name}.migrate.json`
      a.click()
      URL.revokeObjectURL(url)
      setMessage({ type: 'ok', text: `已导出 ${container.name} 的迁移包，下载到目标节点后导入即可。` })
    } catch (err) {
      console.error(err)
      setMessage({ type: 'err', text: '导出失败，请查看控制台日志。' })
    } finally {
      setBusy(false)
    }
  }

  const handleFile = (file: File) => {
    setFileName(file.name)
    const reader = new FileReader()
    reader.onload = () => {
      try {
        const parsed = JSON.parse(String(reader.result))
        if (parsed && parsed.format === 'eyvescloud-migrate' && parsed.container) {
          setImportBundle(parsed as MigrateBundle)
          setMessage({ type: 'ok', text: `已载入迁移包：${parsed.container.name}` })
        } else {
          setImportBundle(null)
          setMessage({ type: 'err', text: '文件不是有效的 EyvesCloud 迁移包。' })
        }
      } catch {
        setImportBundle(null)
        setMessage({ type: 'err', text: '无法解析迁移包文件。' })
      }
    }
    reader.readAsText(file)
  }

  const handleImport = async () => {
    if (!importBundle) return
    setBusy(true)
    setMessage(null)
    try {
      const res = await migrateImport(importBundle)
      setMessage({ type: 'ok', text: `导入成功：${res.data?.data?.name ?? importBundle.container.name}。` })
      setImportBundle(null)
      setFileName('')
      fetchContainers()
    } catch (err: unknown) {
      console.error(err)
      const detail = (err as { response?: { data?: { message?: string } } })?.response?.data?.message
      setMessage({ type: 'err', text: `导入失败：${detail || '请查看控制台日志。'}` })
    } finally {
      setBusy(false)
    }
  }

  if (isSubUser) {
    return (
      <div className="p-8 text-center text-sm text-gray-500">子用户无权访问节点迁移功能</div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-xl font-semibold text-black">节点迁移</h1>
        <button
          onClick={fetchContainers}
          className="inline-flex items-center gap-2 px-3 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50 text-sm"
        >
          <RefreshCw className="w-4 h-4" />
          刷新
        </button>
      </div>

      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        <div className="px-4 py-3 border-b border-gray-200 bg-gray-50 flex items-center gap-2">
          <Info className="w-4 h-4 text-gray-500" />
          <p className="text-xs text-gray-600">
            迁移流程：在本节点导出容器迁移包 → 在目标节点（已装 EyvesCloud 且已启用对应模板镜像）导入 →
            确认无误后在原节点删除容器。迁移包包含容器配置、网络与安全策略，不包含系统盘数据。
          </p>
        </div>
        {loading ? (
          <div className="p-8 text-center text-sm text-gray-500">加载中...</div>
        ) : containers.length === 0 ? (
          <div className="p-8 text-center text-sm text-gray-500">暂无容器</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100 text-left text-xs font-medium text-gray-500">
                  <th className="px-4 py-2.5">名称</th>
                  <th className="px-4 py-2.5">类型</th>
                  <th className="px-4 py-2.5">模板</th>
                  <th className="px-4 py-2.5">CPU/内存</th>
                  <th className="px-4 py-2.5">IP</th>
                  <th className="px-4 py-2.5">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {containers.map((c) => (
                  <tr key={c.id} className="hover:bg-gray-50">
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-800">{c.name}</td>
                    <td className="px-4 py-2.5 text-xs text-gray-600">{c.virtualization?.toUpperCase()}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600">{c.template}</td>
                    <td className="px-4 py-2.5 text-xs text-gray-600">{c.vcpu}核/{c.ram_mb}MB</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600">{c.ip || '-'}</td>
                    <td className="px-4 py-2.5">
                      <button
                        onClick={() => handleExport(c)}
                        disabled={busy}
                        className="inline-flex items-center gap-1 rounded-md border border-gray-300 px-2.5 py-1.5 text-xs text-gray-700 hover:bg-gray-50 disabled:opacity-60"
                        title="导出迁移包"
                      >
                        <Download className="h-3.5 w-3.5" />
                        导出迁移包
                      </button>
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
          <h2 className="text-sm font-semibold text-black">导入迁移包</h2>
        </div>
        <div className="p-4 space-y-3">
          <label className="flex items-center justify-center gap-3 rounded-lg border-2 border-dashed border-gray-300 px-4 py-6 text-sm text-gray-500 hover:border-gray-400 hover:bg-gray-50 cursor-pointer">
            <FileJson className="w-5 h-5" />
            {fileName ? <span className="font-mono">{fileName}</span> : <span>点击选择 .migrate.json 迁移包文件</span>}
            <input
              type="file"
              accept=".json,application/json"
              className="hidden"
              onChange={(e) => {
                const file = e.target.files?.[0]
                if (file) handleFile(file)
              }}
            />
          </label>
          {importBundle && (
            <div className="rounded-md border border-indigo-200 bg-indigo-50 p-3 text-xs text-indigo-800">
              待导入：<span className="font-mono">{importBundle.container.name}</span>（{importBundle.container.virtualization?.toUpperCase()}，
              {importBundle.container.vcpu}核/{importBundle.container.ram_mb}MB，
              模板 <span className="font-mono">{importBundle.container.template}</span>）
            </div>
          )}
          <button
            onClick={handleImport}
            disabled={!importBundle || busy}
            className="inline-flex items-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50"
          >
            <Upload className="w-4 h-4" />
            导入容器
          </button>
          {message && (
            <p className={`text-xs ${message.type === 'ok' ? 'text-emerald-600' : 'text-red-600'}`}>{message.text}</p>
          )}
        </div>
      </div>
    </div>
  )
}
