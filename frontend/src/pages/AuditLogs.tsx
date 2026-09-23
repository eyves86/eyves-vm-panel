import { useCallback, useEffect, useState } from 'react'
import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, RefreshCw } from 'lucide-react'
import { AuditLog, getAuditLogs } from '../services/api'
import { actionLabel } from '../utils/labels'

const PAGE_SIZE = 10

export default function AuditLogs() {
  const [logs, setLogs] = useState<AuditLog[]>([])
  const [loading, setLoading] = useState(true)
  const [page, setPage] = useState(1)

  const fetchData = useCallback(async () => {
    try {
      const res = await getAuditLogs()
      setLogs(res.data.data || [])
    } catch (err) {
      console.error(err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
    const timer = window.setInterval(fetchData, 10000)
    return () => window.clearInterval(timer)
  }, [fetchData])

  // Clamp the active page when the log list shrinks after a refresh.
  useEffect(() => {
    setPage(p => Math.min(p, Math.max(1, Math.ceil(logs.length / PAGE_SIZE))))
  }, [logs.length])

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-black"></div>
      </div>
    )
  }

  const totalPages = Math.max(1, Math.ceil(logs.length / PAGE_SIZE))
  const safePage = Math.min(page, totalPages)
  const pageLogs = logs.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE)

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-black">操作日志</h1>
          <p className="text-sm text-gray-500 mt-1">共 {logs.length} 条操作记录</p>
        </div>
        <button
          onClick={fetchData}
          className="inline-flex items-center gap-2 px-3 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50 text-sm"
        >
          <RefreshCw className="w-4 h-4" />
          刷新
        </button>
      </div>

      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        {logs.length === 0 ? (
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
                          <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] font-medium bg-black text-white">管理员</span>
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
            {logs.length > PAGE_SIZE && (
              <div className="flex items-center justify-between px-4 py-3 border-t border-gray-100 bg-gray-50">
                <span className="text-xs text-gray-400">第 {page}/{totalPages} 页</span>
                <div className="flex items-center gap-1">
                  <button onClick={() => setPage(1)} disabled={page === 1} className="p-1 text-gray-400 hover:text-black disabled:opacity-20" title="首页"><ChevronsLeft className="w-4 h-4" /></button>
                  <button onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1} className="p-1 text-gray-400 hover:text-black disabled:opacity-20" title="上一页"><ChevronLeft className="w-4 h-4" /></button>
                  {getPageNumbers(page, totalPages).map(n => (
                    <button key={n} onClick={() => setPage(n)} className={`w-7 h-7 text-xs rounded ${n === page ? 'bg-black text-white' : 'border border-gray-200 hover:bg-gray-100'}`}>{n}</button>
                  ))}
                  <button onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={page >= totalPages} className="p-1 text-gray-400 hover:text-black disabled:opacity-20" title="下一页"><ChevronRight className="w-4 h-4" /></button>
                  <button onClick={() => setPage(totalPages)} disabled={page >= totalPages} className="p-1 text-gray-400 hover:text-black disabled:opacity-20" title="末页"><ChevronsRight className="w-4 h-4" /></button>
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
