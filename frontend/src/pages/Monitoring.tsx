import { useState, useEffect, useCallback } from 'react'
import { RefreshCw, Activity, Server, ShieldAlert, Download } from 'lucide-react'
import { getContainerMonitoring, getUsageExport, ContainerMonitorRow } from '../services/api'

const severityClass: Record<string, string> = {
  critical: 'bg-red-100 text-red-700',
  high: 'bg-orange-100 text-orange-700',
  medium: 'bg-yellow-100 text-yellow-700',
  low: 'bg-gray-100 text-gray-600',
}

const severityLabel: Record<string, string> = {
  critical: '严重',
  high: '高危',
  medium: '中危',
  low: '低危',
}

function formatRate(value: number): string {
  if (!value || value <= 0) return '-'
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s']
  let v = value
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`
}

function formatGB(value: number): string {
  if (!value || value <= 0) return '-'
  return `${value.toFixed(2)} GB`
}

export default function Monitoring() {
  const [rows, setRows] = useState<ContainerMonitorRow[]>([])
  const [generatedAt, setGeneratedAt] = useState('')
  const [loading, setLoading] = useState(true)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const [usageExporting, setUsageExporting] = useState(false)
  const [usageTenant, setUsageTenant] = useState('')

  // 全量用量导出（GET /api/v1/usage，权限 usage:read；供财务对账/计费系统拉取）
  const exportUsage = async () => {
    if (usageExporting) return
    setUsageExporting(true)
    try {
      const res = await getUsageExport(usageTenant.trim() || undefined)
      const data = res.data.data
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `eyvescloud-usage-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.json`
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      alert(error.response?.data?.message || '导出用量失败，请稍后重试')
    } finally {
      setUsageExporting(false)
    }
  }

  const fetchData = useCallback(async () => {
    try {
      const res = await getContainerMonitoring()
      if (res.data.data) {
        setRows(res.data.data.containers || [])
        setGeneratedAt(res.data.data.generated_at || '')
      }
    } catch (err) {
      console.error(err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
    if (!autoRefresh) return
    const interval = setInterval(fetchData, 15000)
    return () => clearInterval(interval)
  }, [fetchData, autoRefresh])

  const running = rows.filter((r) => r.status === 'running').length
  const abusers = rows.filter((r) => r.abuse_alerts > 0).length
  const totalAbuse = rows.reduce((sum, r) => sum + r.abuse_alerts, 0)

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-brand-600"></div>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-xl font-semibold text-black">容器监控</h1>
        <div className="flex flex-wrap items-center gap-2">
          <input
            type="text"
            value={usageTenant}
            onChange={(e) => setUsageTenant(e.target.value)}
            placeholder="按租户过滤导出（可选）"
            className="h-9 w-48 rounded-md border border-gray-300 px-3 text-sm text-black placeholder:text-gray-400 focus:outline-none focus:ring-2 focus:ring-brand-500"
          />
          <button
            onClick={() => void exportUsage()}
            disabled={usageExporting}
            title="导出全量容器用量 JSON（含配置额度/流量计数/生命周期状态，供财务对账）"
            className="inline-flex items-center gap-2 px-3 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50 text-sm disabled:opacity-50"
          >
            <Download className="w-4 h-4" />
            {usageExporting ? '导出中...' : '导出用量'}
          </button>
          <button
            type="button"
            role="switch"
            aria-checked={autoRefresh}
            onClick={() => setAutoRefresh((v) => !v)}
            className={`inline-flex h-9 items-center gap-2 rounded-md border px-3 text-sm transition-colors ${
              autoRefresh
                ? 'border-blue-200 bg-blue-50 text-blue-700 hover:bg-blue-100'
                : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50'
            }`}
          >
            <Activity className="w-4 h-4" />
            <span>{autoRefresh ? '自动刷新已开' : '自动刷新已关'}</span>
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

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
        <div className="bg-white border border-gray-200 rounded-xl p-4">
          <div className="flex items-center gap-2 text-gray-500 text-xs">
            <Server className="w-4 h-4" />
            容器总数
          </div>
          <div className="mt-1 text-2xl font-semibold text-black">{rows.length}</div>
        </div>
        <div className="bg-white border border-gray-200 rounded-xl p-4">
          <div className="flex items-center gap-2 text-gray-500 text-xs">
            <Activity className="w-4 h-4" />
            运行中
          </div>
          <div className="mt-1 text-2xl font-semibold text-black">{running}</div>
        </div>
        <div className="bg-white border border-gray-200 rounded-xl p-4">
          <div className="flex items-center gap-2 text-gray-500 text-xs">
            <ShieldAlert className="w-4 h-4" />
            涉及滥用容器
          </div>
          <div className="mt-1 text-2xl font-semibold text-black">{abusers}</div>
        </div>
        <div className="bg-white border border-gray-200 rounded-xl p-4">
          <div className="flex items-center gap-2 text-gray-500 text-xs">
            <ShieldAlert className="w-4 h-4" />
            滥用告警总数
          </div>
          <div className="mt-1 text-2xl font-semibold text-black">{totalAbuse}</div>
        </div>
      </div>

      <div className="bg-white border border-gray-200 rounded-xl overflow-hidden">
        <div className="px-4 py-3 border-b border-gray-200 bg-gray-50 flex items-center justify-between">
          <h2 className="text-sm font-semibold text-black">全部容器实时指标（LXC / KVM）</h2>
          <span className="text-xs text-gray-500">更新于 {generatedAt || '-'}</span>
        </div>
        {rows.length === 0 ? (
          <div className="p-8 text-center text-sm text-gray-500">暂无容器</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100 text-left text-xs font-medium text-gray-500">
                  <th className="px-4 py-2.5 whitespace-nowrap">容器</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">类型</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">状态</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">归属</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">IP</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">CPU</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">内存</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">网络(入/出)</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">磁盘(读/写)</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">流量(入/出)</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">滥用</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {rows.map((row) => (
                  <tr key={row.uuid || row.id} className="hover:bg-gray-50">
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-800 whitespace-nowrap">{row.name}</td>
                    <td className="px-4 py-2.5 text-xs text-gray-600 uppercase whitespace-nowrap">
                      {row.virtualization || 'lxc'}
                    </td>
                    <td className="px-4 py-2.5 whitespace-nowrap">
                      <span
                        className={`rounded px-1.5 py-0.5 text-xs font-medium ${
                          row.status === 'running' ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'
                        }`}
                      >
                        {row.status || '-'}
                      </span>
                      {row.policy_blocked && (
                        <span className="ml-1 rounded bg-red-100 px-1.5 py-0.5 text-xs font-medium text-red-700">
                          已封禁
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-xs text-gray-600 whitespace-nowrap">
                      {row.owner || '未分配'}
                      {row.tenant ? ` / ${row.tenant}` : ''}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600 whitespace-nowrap">{row.ip || '-'}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-700 whitespace-nowrap">
                      {row.cpu ? `${row.cpu.toFixed(1)}%` : '-'}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-700 whitespace-nowrap">
                      {row.memory ? `${row.memory.toFixed(1)}%` : '-'}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600 whitespace-nowrap">
                      {formatRate(row.network_rx)} / {formatRate(row.network_tx)}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600 whitespace-nowrap">
                      {formatRate(row.disk_read)} / {formatRate(row.disk_write)}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600 whitespace-nowrap">
                      {formatGB(row.traffic_used_rx_gb)} / {formatGB(row.traffic_used_tx_gb)}
                    </td>
                    <td className="px-4 py-2.5 whitespace-nowrap">
                      {row.abuse_alerts > 0 ? (
                        <span
                          className={`rounded px-1.5 py-0.5 text-xs font-medium ${
                            severityClass[row.abuse_severity || ''] || 'bg-gray-100 text-gray-600'
                          }`}
                        >
                          {severityLabel[row.abuse_severity || ''] || row.abuse_severity} · {row.abuse_alerts}
                        </span>
                      ) : (
                        <span className="text-xs text-gray-400">-</span>
                      )}
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