import { useState, useEffect, useCallback } from 'react'
import { FileText, Power, RefreshCw, ShieldCheck, X } from 'lucide-react'
import { getSecurityAlerts, getSecurityLogs, getSecuritySettings, SecurityAlert, SecurityLog, updateSecuritySettings } from '../services/api'

const typeLabels: Record<string, string> = {
  port_scan: '端口扫描',
  horizontal_scan: '横向扫描',
  brute_force: '暴力破解',
  ddos: 'DDoS/大规模扫描',
  spam: '垃圾邮件',
  malware: '恶意软件',
  mining: '挖矿连接',
  proxy: '代理/VPN/Tor',
  reflection: 'UDP反射放大',
  arp_spoof: 'ARP欺骗/地址冲突',
}

const severityLabels: Record<string, string> = {
  critical: '严重',
  high: '高危',
  medium: '中危',
  low: '低危',
}

export default function Security() {
  const [alerts, setAlerts] = useState<SecurityAlert[]>([])
  const [autoShutdown, setAutoShutdown] = useState(false)
  const [arpProtection, setArpProtection] = useState(false)
  const [loading, setLoading] = useState(true)
  const [savingSettings, setSavingSettings] = useState(false)
  const [logAlert, setLogAlert] = useState<SecurityAlert | null>(null)
  const [logs, setLogs] = useState<SecurityLog[]>([])
  const [logsLoading, setLogsLoading] = useState(false)

  const fetchData = useCallback(async () => {
    try {
      const [alertRes, settingsRes] = await Promise.all([getSecurityAlerts(), getSecuritySettings()])
      if (alertRes.data.data) setAlerts(alertRes.data.data)
      if (settingsRes.data.data) {
        setAutoShutdown(settingsRes.data.data.auto_shutdown ?? false)
        setArpProtection(settingsRes.data.data.arp_protection ?? false)
      }
    } catch (err) {
      console.error(err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 10000)
    return () => clearInterval(interval)
  }, [fetchData])

  const handleAutoShutdownChange = async () => {
    const next = !autoShutdown
    setAutoShutdown(next)
    setSavingSettings(true)
    try {
      const res = await updateSecuritySettings({ auto_shutdown: next })
      if (res.data.data) setAutoShutdown(res.data.data.auto_shutdown ?? next)
    } catch (err) {
      console.error(err)
      setAutoShutdown(!next)
    } finally {
      setSavingSettings(false)
    }
  }

  const handleArpProtectionChange = async () => {
    const next = !arpProtection
    setArpProtection(next)
    setSavingSettings(true)
    try {
      const res = await updateSecuritySettings({ arp_protection: next })
      if (res.data.data) setArpProtection(res.data.data.arp_protection ?? next)
    } catch (err) {
      console.error(err)
      setArpProtection(!next)
    } finally {
      setSavingSettings(false)
    }
  }

  const openLogs = async (alert: SecurityAlert) => {
    setLogAlert(alert)
    setLogs([])
    setLogsLoading(true)
    try {
      const res = await getSecurityLogs(alert.container_name)
      setLogs(filterRelatedLogs(res.data.data || [], alert))
    } catch (err) {
      console.error(err)
      setLogs([])
    } finally {
      setLogsLoading(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-black"></div>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-xl font-semibold text-black">安全告警</h1>
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            role="switch"
            aria-checked={autoShutdown}
            onClick={handleAutoShutdownChange}
            disabled={savingSettings}
            title="告警自动关机"
            className={`inline-flex h-9 items-center gap-2 rounded-md border px-3 text-sm transition-colors disabled:opacity-60 ${
              autoShutdown
                ? 'border-red-200 bg-red-50 text-red-700 hover:bg-red-100'
                : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50'
            }`}
          >
            <Power className="w-4 h-4" />
            <span>{autoShutdown ? '自动关机已开' : '自动关机已关'}</span>
          </button>
          <button
            type="button"
            role="switch"
            aria-checked={arpProtection}
            onClick={handleArpProtectionChange}
            disabled={savingSettings}
            title="公网 IP-MAC 绑定防护（检测 ARP 地址冲突/欺骗）"
            className={`inline-flex h-9 items-center gap-2 rounded-md border px-3 text-sm transition-colors disabled:opacity-60 ${
              arpProtection
                ? 'border-indigo-200 bg-indigo-50 text-indigo-700 hover:bg-indigo-100'
                : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50'
            }`}
          >
            <ShieldCheck className="w-4 h-4" />
            <span>{arpProtection ? 'ARP防护已开' : 'ARP防护已关'}</span>
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
        <div className="px-4 py-3 border-b border-gray-200 bg-gray-50">
          <h2 className="text-sm font-semibold text-black">告警列表 ({alerts.length})</h2>
        </div>
        {alerts.length === 0 ? (
          <div className="p-8 text-center text-sm text-gray-500">暂无安全告警</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100 text-left text-xs font-medium text-gray-500">
                  <th className="px-4 py-2.5 whitespace-nowrap">时间</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">等级</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">类型</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">容器</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">源IP</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">目标</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">次数</th>
                  <th className="px-4 py-2.5">详情</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {alerts.map((alert) => (
                  <tr key={alert.id} className="hover:bg-gray-50">
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-500 whitespace-nowrap">{alert.timestamp}</td>
                    <td className="px-4 py-2.5 whitespace-nowrap">
                      <SeverityBadge severity={alert.severity} />
                    </td>
                    <td className="px-4 py-2.5 text-gray-800 whitespace-nowrap">{typeLabels[alert.type] || alert.type}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-700 whitespace-nowrap">{alert.container_name}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600 whitespace-nowrap">{alert.source_ip || '-'}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600 whitespace-nowrap">
                      {formatTarget(alert)}
                    </td>
                    <td className="px-4 py-2.5 text-gray-600 whitespace-nowrap">{alert.count}</td>
                    <td className="px-4 py-2.5 text-gray-600 min-w-[300px]">
                      <div className="flex items-center gap-2">
                        <span className="min-w-0 flex-1">{alert.detail}</span>
                        <button
                          onClick={() => openLogs(alert)}
                          className="inline-flex shrink-0 items-center gap-1 rounded-md border border-gray-300 px-2 py-1 text-xs text-gray-700 hover:bg-gray-50"
                          title="查看相关记录"
                        >
                          <FileText className="h-3.5 w-3.5" />
                          查看
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

      {logAlert && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <div className="w-full max-w-4xl overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl">
            <div className="flex items-start justify-between gap-3 border-b border-gray-200 px-4 py-3">
              <div>
                <h3 className="text-sm font-semibold text-black">相关连接记录</h3>
                <p className="mt-1 text-xs text-gray-500">
                  {logAlert.container_name} · {typeLabels[logAlert.type] || logAlert.type} · {formatTarget(logAlert)}
                </p>
              </div>
              <button
                onClick={() => setLogAlert(null)}
                className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black"
                title="关闭"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            <div className="max-h-[70vh] overflow-auto">
              {logAlert.log_line && (
                <div className="border-b border-gray-100 bg-gray-50 px-4 py-3">
                  <div className="mb-1 text-xs font-medium text-gray-600">告警原始记录</div>
                  <pre className="whitespace-pre-wrap break-all rounded border border-gray-200 bg-white p-3 text-xs text-gray-700">{logAlert.log_line}</pre>
                </div>
              )}
              {logsLoading ? (
                <div className="p-8 text-center text-sm text-gray-500">正在加载连接记录...</div>
              ) : logs.length === 0 ? (
                <div className="p-8 text-center text-sm text-gray-500">
                  暂无可用连接记录。历史告警对应的 conntrack 记录可能已经过期。
                </div>
              ) : (
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-gray-100 bg-gray-50 text-left text-xs font-medium text-gray-500">
                      <th className="px-4 py-2.5">协议</th>
                      <th className="px-4 py-2.5">状态</th>
                      <th className="px-4 py-2.5">源地址</th>
                      <th className="px-4 py-2.5">目标地址</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {logs.map((log, index) => (
                      <tr key={`${log.src_ip}-${log.src_port}-${log.dst_ip}-${log.dst_port}-${index}`}>
                        <td className="px-4 py-2.5 font-mono text-xs text-gray-700">{log.protocol || '-'}</td>
                        <td className="px-4 py-2.5 font-mono text-xs text-gray-700">{log.state || '-'}</td>
                        <td className="px-4 py-2.5 font-mono text-xs text-gray-600">
                          {formatEndpoint(log.src_ip, log.src_port)}
                        </td>
                        <td className="px-4 py-2.5 font-mono text-xs text-gray-600">
                          {formatEndpoint(log.dst_ip, log.dst_port)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function SeverityBadge({ severity }: { severity: string }) {
  const colors: Record<string, string> = {
    critical: 'bg-red-100 text-red-700',
    high: 'bg-amber-100 text-amber-700',
    medium: 'bg-gray-100 text-gray-700',
    low: 'bg-gray-50 text-gray-500',
  }
  return (
    <span className={`px-1.5 py-0.5 rounded text-xs font-medium ${colors[severity] || 'bg-gray-100 text-gray-700'}`}>
      {severityLabels[severity] || severity}
    </span>
  )
}

function formatTarget(alert: SecurityAlert): string {
  if (alert.target_ip === '*') return '*'
  if (!alert.target_ip) return '-'
  return alert.target_port > 0 ? `${alert.target_ip}:${alert.target_port}` : alert.target_ip
}

function filterRelatedLogs(logs: SecurityLog[], alert: SecurityAlert): SecurityLog[] {
  return logs.filter((log) => {
    if (alert.source_ip && log.src_ip !== alert.source_ip) return false
    if (alert.target_ip && alert.target_ip !== '*' && log.dst_ip !== alert.target_ip) return false
    if (alert.target_port > 0 && log.dst_port !== alert.target_port) return false
    return true
  })
}

function formatEndpoint(ip: string, port: number): string {
  if (!ip) return '-'
  return port > 0 ? `${ip}:${port}` : ip
}
