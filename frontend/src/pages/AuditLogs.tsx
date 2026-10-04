import { useCallback, useEffect, useState } from 'react'
import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Download, FileJson, RefreshCw, ShieldCheck } from 'lucide-react'
import { AuditLog, exportAuditLogs, getAuditLogsPaged, verifyAuditChain } from '../services/api'
import { actionLabel } from '../utils/labels'

const PAGE_SIZE = 10

export default function AuditLogs() {
  const [logs, setLogs] = useState<AuditLog[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [page, setPage] = useState(1)
  const [exporting, setExporting] = useState<'csv' | 'json' | null>(null)
  const [verifying, setVerifying] = useState(false)
  const [notice, setNotice] = useState<{ ok: boolean; text: string } | null>(null)

  const fetchData = useCallback(async () => {
    try {
      const res = await getAuditLogsPaged({ page, page_size: PAGE_SIZE })
      const data = res.data.data
      setLogs(data?.items || [])
      setTotal(data?.total ?? 0)
    } catch (err) {
      console.error(err)
    } finally {
      setLoading(false)
    }
  }, [page])

  useEffect(() => {
    fetchData()
    const timer = window.setInterval(fetchData, 10000)
    return () => window.clearInterval(timer)
  }, [fetchData])

  // Clamp the active page when the log list shrinks after a refresh.
  useEffect(() => {
    setPage(p => Math.min(p, Math.max(1, Math.ceil(total / PAGE_SIZE))))
  }, [total])

  // 导出审计日志（CSV / JSON），后端以 Blob 流返回
  const downloadAuditLogs = async (format: 'csv' | 'json') => {
    if (exporting) return
    setExporting(format)
    setNotice(null)
    try {
      const res = await exportAuditLogs(format)
      const url = URL.createObjectURL(res.data)
      const a = document.createElement('a')
      a.href = url
      a.download = `audit-logs.${format}`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
    } catch (err: unknown) {
      setNotice({ ok: false, text: await blobErrorMessage(err, `导出 ${format.toUpperCase()} 失败，请稍后重试`) })
    } finally {
      setExporting(null)
    }
  }

  // 校验审计日志哈希链完整性
  const verifyChain = async () => {
    if (verifying) return
    setVerifying(true)
    setNotice(null)
    try {
      const res = await verifyAuditChain()
      // 兼容两种返回：{ valid, checked, reason } 与后端实际的 { entries }
      const data = res.data.data as { valid?: boolean; checked?: number; entries?: number; reason?: string } | undefined
      const valid = res.data.success && data?.valid !== false
      if (valid) {
        const count = data?.checked ?? data?.entries
        setNotice({ ok: true, text: count ? `哈希链校验通过，共 ${count} 条记录` : '哈希链校验通过' })
      } else {
        setNotice({ ok: false, text: `哈希链校验失败：${res.data.message || data?.reason || '校验未通过'}` })
      }
    } catch (err: any) {
      setNotice({ ok: false, text: err?.response?.data?.message || '哈希链校验失败，请稍后重试' })
    } finally {
      setVerifying(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-brand-600"></div>
      </div>
    )
  }

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const safePage = Math.min(page, totalPages)
  const pageLogs = logs

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-black">操作日志</h1>
          <p className="text-sm text-gray-500 mt-1">共 {total} 条操作记录</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button
            onClick={() => { void downloadAuditLogs('csv') }}
            disabled={exporting !== null || verifying}
            className="inline-flex items-center gap-2 px-3 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50 text-sm disabled:opacity-50"
          >
            <Download className="w-4 h-4" />
            {exporting === 'csv' ? '导出中...' : '导出 CSV'}
          </button>
          <button
            onClick={() => { void downloadAuditLogs('json') }}
            disabled={exporting !== null || verifying}
            className="inline-flex items-center gap-2 px-3 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50 text-sm disabled:opacity-50"
          >
            <FileJson className="w-4 h-4" />
            {exporting === 'json' ? '导出中...' : '导出 JSON'}
          </button>
          <button
            onClick={() => { void verifyChain() }}
            disabled={verifying || exporting !== null}
            className="inline-flex items-center gap-2 px-3 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50 text-sm disabled:opacity-50"
          >
            <ShieldCheck className="w-4 h-4" />
            {verifying ? '校验中...' : '校验哈希链'}
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

      {notice && (
        <div className={`rounded-md border px-3 py-2 text-sm ${notice.ok ? 'border-green-200 bg-green-50 text-green-700' : 'border-red-200 bg-red-50 text-red-700'}`}>
          {notice.text}
        </div>
      )}

      <div className="bg-white border border-gray-200 rounded-xl overflow-hidden">
        {total === 0 ? (
          <div className="p-8 text-center text-sm text-gray-500">暂无操作日志</div>
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-gray-100 bg-gray-50 text-left text-xs font-medium text-gray-500">
                    <th className="px-4 py-2.5 whitespace-nowrap">时间</th>
                    <th className="px-4 py-2.5 whitespace-nowrap">用户</th>
                    <th className="px-4 py-2.5 whitespace-nowrap">操作</th>
                    <th className="px-4 py-2.5 whitespace-nowrap">目标</th>
                    <th className="px-4 py-2.5">详情</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {pageLogs.map((log, index) => (
                    <tr key={`${log.time}-${index}`} className="hover:bg-gray-50">
                      <td className="px-4 py-2.5 font-mono text-xs text-gray-500 whitespace-nowrap">{log.time}</td>
                      <td className="px-4 py-2.5 whitespace-nowrap">
                        {log.user === 'admin' ? (
                          <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] font-medium bg-brand-600 text-white">管理员</span>
                        ) : log.user?.startsWith('user:') ? (
                          <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] font-medium bg-gray-100 text-gray-700">用户</span>
                        ) : (
                          <span className="text-xs text-gray-500">{log.user || '-'}</span>
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-gray-800 whitespace-nowrap">{actionLabel(log.action)}</td>
                      <td className="px-4 py-2.5 font-mono text-xs text-gray-700 whitespace-nowrap">{log.target || '-'}</td>
                      <td className="px-4 py-2.5 text-gray-600 min-w-[280px]">{log.detail || '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {total > PAGE_SIZE && (
              <div className="flex items-center justify-between px-4 py-3 border-t border-gray-100 bg-gray-50">
                <span className="text-xs text-gray-400">第 {safePage}/{totalPages} 页</span>
                <div className="flex items-center gap-1">
                  <button onClick={() => setPage(1)} disabled={safePage === 1} className="p-1 text-gray-400 hover:text-black disabled:opacity-20" title="首页"><ChevronsLeft className="w-4 h-4" /></button>
                  <button onClick={() => setPage(p => Math.max(1, p - 1))} disabled={safePage === 1} className="p-1 text-gray-400 hover:text-black disabled:opacity-20" title="上一页"><ChevronLeft className="w-4 h-4" /></button>
                  {getPageNumbers(safePage, totalPages).map(n => (
                    <button key={n} onClick={() => setPage(n)} className={`w-7 h-7 text-xs rounded ${n === safePage ? 'bg-brand-600 text-white' : 'border border-gray-200 hover:bg-gray-100'}`}>{n}</button>
                  ))}
                  <button onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={safePage >= totalPages} className="p-1 text-gray-400 hover:text-black disabled:opacity-20" title="下一页"><ChevronRight className="w-4 h-4" /></button>
                  <button onClick={() => setPage(totalPages)} disabled={safePage >= totalPages} className="p-1 text-gray-400 hover:text-black disabled:opacity-20" title="末页"><ChevronsRight className="w-4 h-4" /></button>
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}

function getPageNumbers(current: number, total: number): number[] {
  if (total <= 5) return Array.from({ length: total }, (_, i) => i + 1)
  let start = Math.max(1, current - 2)
  if (start + 4 > total) start = total - 4
  return Array.from({ length: 5 }, (_, i) => start + i)
}

// 导出接口失败时响应体是 Blob（JSON 错误被当作 blob 接收），尝试解析出后端 message
async function blobErrorMessage(err: unknown, fallback: string): Promise<string> {
  const data = (err as { response?: { data?: unknown } })?.response?.data
  if (data instanceof Blob) {
    try {
      const parsed = JSON.parse(await data.text()) as { message?: string }
      if (parsed?.message) return parsed.message
    } catch {
      // 忽略解析失败，走兜底文案
    }
  }
  const message = (err as { response?: { data?: { message?: string } } })?.response?.data?.message
  return message || fallback
}
