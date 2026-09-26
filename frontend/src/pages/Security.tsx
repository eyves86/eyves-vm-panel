import { useState, useEffect, useCallback } from 'react'
import { Activity, FileText, Power, RefreshCw, SearchCheck, ShieldAlert, ShieldCheck, X } from 'lucide-react'
import { checkContainerSecurity, getAbuseSummary, getContainers, getSecurityAlerts, getSecurityLogs, getSecuritySettings, getSecuritySummary, AbuseSummary, Container, SecurityAlert, SecurityLog, updateSecuritySettings } from '../services/api'

const typeLabels: Record<string, string> = {
  port_scan: '端口扫描',
  horizontal_scan: '横向扫描',
  brute_force: '暴力破解',
  inbound_brute_force: '被暴力破解',
  inbound_ddos: '被DDoS攻击',
  inbound_scan: '被端口扫描',
  ddos: 'DDoS/大规模扫描',
  cc: 'CC/HTTP洪水',
  p2p: 'BT/PT下载',
  spam: '垃圾邮件',
  malware: '恶意软件/C2',
  mining: '挖矿',
  proxy: '代理/VPN/Tor',
  reflection: 'UDP反射放大',
  arp_spoof: 'ARP欺骗/地址冲突',
  lateral_movement: '内网横向移动',
  backdoor: '后门/远控监听',
  compromise: '疑似被入侵',
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
  const [ipAntiSpoof, setIpAntiSpoof] = useState(false)
  const [abuseDetection, setAbuseDetection] = useState(true)
  const [abuse, setAbuse] = useState<AbuseSummary | null>(null)
  const [conntrackAvailable, setConntrackAvailable] = useState<boolean | null>(null)
  const [loading, setLoading] = useState(true)
  const [savingSettings, setSavingSettings] = useState(false)
  const [logAlert, setLogAlert] = useState<SecurityAlert | null>(null)
  const [logs, setLogs] = useState<SecurityLog[]>([])
  const [logsLoading, setLogsLoading] = useState(false)

  // 单容器安全检查
  const [checkOpen, setCheckOpen] = useState(false)
  const [checkContainers, setCheckContainers] = useState<Container[]>([])
  const [checkContainersLoading, setCheckContainersLoading] = useState(false)
  const [checkLoadError, setCheckLoadError] = useState('')
  const [checkTarget, setCheckTarget] = useState('')
  const [checking, setChecking] = useState(false)
  const [checkResult, setCheckResult] = useState<{ ok: boolean; message: string } | null>(null)
  const [checkAlerts, setCheckAlerts] = useState<SecurityAlert[] | null>(null)

  const fetchData = useCallback(async () => {
    try {
      const [alertRes, settingsRes, abuseRes, summaryRes] = await Promise.all([
        getSecurityAlerts(),
        getSecuritySettings(),
        getAbuseSummary().catch(() => null),
        getSecuritySummary().catch(() => null),
      ])
      if (alertRes.data.data) setAlerts(alertRes.data.data)
      if (settingsRes.data.data) {
        setAutoShutdown(settingsRes.data.data.auto_shutdown ?? false)
        setArpProtection(settingsRes.data.data.arp_protection ?? false)
        setIpAntiSpoof(settingsRes.data.data.ip_anti_spoof ?? false)
        setAbuseDetection(settingsRes.data.data.abuse_detection ?? true)
      }
      if (abuseRes?.data.data) setAbuse(abuseRes.data.data)
      if (summaryRes?.data.data) setConntrackAvailable(summaryRes.data.data.conntrack_available ?? null)
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

  const handleIpAntiSpoofChange = async () => {
    const next = !ipAntiSpoof
    setIpAntiSpoof(next)
    setSavingSettings(true)
    try {
      const res = await updateSecuritySettings({ ip_anti_spoof: next })
      if (res.data.data) setIpAntiSpoof(res.data.data.ip_anti_spoof ?? next)
    } catch (err) {
      console.error(err)
      setIpAntiSpoof(!next)
    } finally {
      setSavingSettings(false)
    }
  }

  const handleAbuseDetectionChange = async () => {
    const next = !abuseDetection
    setAbuseDetection(next)
    setSavingSettings(true)
    try {
      const res = await updateSecuritySettings({ abuse_detection: next })
      if (res.data.data) setAbuseDetection(res.data.data.abuse_detection ?? next)
    } catch (err) {
      console.error(err)
      setAbuseDetection(!next)
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

  // 打开单容器安全检查弹窗：只列运行中的容器（后端只支持对运行中的容器检查）
  const openCheck = async () => {
    setCheckOpen(true)
    setCheckTarget('')
    setCheckResult(null)
    setCheckAlerts(null)
    setCheckLoadError('')
    setCheckContainers([])
    setCheckContainersLoading(true)
    try {
      const res = await getContainers()
      setCheckContainers((res.data.data || []).filter((c) => c.status === 'running'))
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      setCheckLoadError(error.response?.data?.message || '获取容器列表失败')
    } finally {
      setCheckContainersLoading(false)
    }
  }

  const runCheck = async () => {
    if (!checkTarget || checking) return
    setChecking(true)
    setCheckResult(null)
    setCheckAlerts(null)
    try {
      const res = await checkContainerSecurity(checkTarget)
      setCheckResult({ ok: !!res.data.success, message: res.data.message || (res.data.success ? '安全检查已完成' : '安全检查失败') })
      // 检查后刷新该容器的风险项与页面告警列表
      try {
        const [alertRes] = await Promise.all([getSecurityAlerts()])
        setCheckAlerts((alertRes.data.data || []).filter((a) => a.container_name === checkTarget))
      } catch (err) {
        console.error(err)
        setCheckAlerts([])
      }
      void fetchData()
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      setCheckResult({ ok: false, message: error.response?.data?.message || '安全检查失败，请稍后重试' })
    } finally {
      setChecking(false)
    }
  }

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
        <h1 className="text-xl font-semibold text-black">安全告警</h1>
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            role="switch"
            aria-checked={abuseDetection}
            onClick={handleAbuseDetectionChange}
            disabled={savingSettings}
            title="滥用行为检测：检测挖矿、BT/PT、VPN/代理/Tor、25端口垃圾邮件、DDoS/CC、爆破、后门、内网横向移动与疑似被入侵等行为"
            className={`inline-flex h-9 items-center gap-2 rounded-md border px-3 text-sm transition-colors disabled:opacity-60 ${
              abuseDetection
                ? 'border-sky-200 bg-sky-50 text-sky-700 hover:bg-sky-100'
                : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50'
            }`}
          >
            <Activity className="w-4 h-4" />
            <span>{abuseDetection ? '滥用检测已开' : '滥用检测已关'}</span>
          </button>
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
            type="button"
            role="switch"
            aria-checked={ipAntiSpoof}
            onClick={handleIpAntiSpoofChange}
            disabled={savingSettings}
            title="IP 防盗：把平台分配的公网 IP 与容器 MAC 绑定，阻止容器盗用其它 IP"
            className={`inline-flex h-9 items-center gap-2 rounded-md border px-3 text-sm transition-colors disabled:opacity-60 ${
              ipAntiSpoof
                ? 'border-emerald-200 bg-emerald-50 text-emerald-700 hover:bg-emerald-100'
                : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50'
            }`}
          >
            <ShieldAlert className="w-4 h-4" />
            <span>{ipAntiSpoof ? 'IP防盗已开' : 'IP防盗已关'}</span>
          </button>
          <button
            onClick={() => { void openCheck() }}
            className="inline-flex items-center gap-2 px-3 py-2 border border-gray-300 text-gray-700 rounded-md hover:bg-gray-50 text-sm"
          >
            <SearchCheck className="w-4 h-4" />
            容器安全检查
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

      {!abuseDetection && (
        <div className="rounded-lg border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-600">
          滥用行为检测已关闭：不会再产生挖矿、BT/PT、VPN/代理、25 端口、DDoS/CC、爆破、后门、内网横向移动等告警。
        </div>
      )}

      {abuseDetection && conntrackAvailable === false && (
        <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
          未检测到连接跟踪数据源（conntrack / <span className="font-mono">/proc/net/nf_conntrack</span>）。
          基于出站/入站连接的滥用检测（挖矿、VPN/代理、BT/PT、CC、25 端口、爆破、后门等）<strong>当前不会生效</strong>，
          请安装 <span className="font-mono">conntrack</span> 工具或启用内核 nf_conntrack 模块。
        </div>
      )}

      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        <div className="px-4 py-3 border-b border-gray-200 bg-gray-50 flex items-center justify-between">
          <h2 className="text-sm font-semibold text-black">滥用用户记录（按用户 / 租户归因）</h2>
          <span className="text-xs text-gray-500">共 {abuse?.total_alerts ?? 0} 条告警</span>
        </div>
        {!abuse || abuse.by_owner.length === 0 ? (
          <div className="p-8 text-center text-sm text-gray-500">暂无滥用记录</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100 text-left text-xs font-medium text-gray-500">
                  <th className="px-4 py-2.5 whitespace-nowrap">用户</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">租户</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">最高等级</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">告警数</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">类型分布</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">涉及容器</th>
                  <th className="px-4 py-2.5 whitespace-nowrap">最近</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {abuse.by_owner.map((row, index) => (
                  <tr key={`${row.owner}-${row.tenant}-${index}`} className="hover:bg-gray-50">
                    <td className="px-4 py-2.5 text-gray-800 whitespace-nowrap">{row.owner || '未分配'}</td>
                    <td className="px-4 py-2.5 text-gray-600 whitespace-nowrap">{row.tenant || '-'}</td>
                    <td className="px-4 py-2.5 whitespace-nowrap">
                      <SeverityBadge severity={row.severity} />
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-700">{row.alerts}</td>
                    <td className="px-4 py-2.5 text-xs text-gray-600">
                      {Object.entries(row.types)
                        .map(([key, count]) => `${typeLabels[key] || key}×${count}`)
                        .join('、')}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-600">{row.containers.join(', ')}</td>
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-500 whitespace-nowrap">{row.last_seen || '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
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
                    <td className="px-4 py-2.5 font-mono text-xs text-gray-700 whitespace-nowrap">
                      {alert.container_name}
                      {alert.kind && (
                        <span className="ml-1.5 rounded bg-gray-100 px-1 py-0.5 font-sans text-[10px] text-gray-500">
                          {alert.kind.toUpperCase()}
                        </span>
                      )}
                    </td>
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

      {checkOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <div className="w-full max-w-2xl overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl">
            <div className="flex items-start justify-between gap-3 border-b border-gray-200 px-4 py-3">
              <div>
                <h3 className="text-sm font-semibold text-black">容器安全检查</h3>
                <p className="mt-1 text-xs text-gray-500">选择运行中的容器，立即执行一次滥用行为检测并查看风险项</p>
              </div>
              <button
                onClick={() => setCheckOpen(false)}
                className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black"
                title="关闭"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            <div className="max-h-[70vh] space-y-4 overflow-auto p-4">
              {checkLoadError && (
                <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">{checkLoadError}</div>
              )}
              <div className="flex flex-wrap items-end gap-3">
                <div className="min-w-[240px] flex-1">
                  <label className="mb-1 block text-xs font-medium text-gray-500">容器</label>
                  <select
                    value={checkTarget}
                    onChange={(e) => setCheckTarget(e.target.value)}
                    disabled={checking || checkContainersLoading}
                    className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black focus:outline-none focus:ring-1 focus:ring-brand-500 disabled:opacity-50"
                  >
                    <option value="">{checkContainersLoading ? '加载容器中...' : '请选择容器'}</option>
                    {checkContainers.map((c) => (
                      <option key={c.id} value={c.name}>{c.name}（运行中）</option>
                    ))}
                  </select>
                </div>
                <button
                  onClick={() => { void runCheck() }}
                  disabled={!checkTarget || checking}
                  className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-3 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50"
                >
                  {checking ? <RefreshCw className="h-4 w-4 animate-spin" /> : <SearchCheck className="h-4 w-4" />}
                  {checking ? '检查中...' : '立即检查'}
                </button>
              </div>
              {checkContainers.length === 0 && !checkContainersLoading && !checkLoadError && (
                <div className="rounded-md border border-gray-200 px-3 py-6 text-center text-sm text-gray-500">暂无运行中的容器</div>
              )}
              {checkResult && (
                <div className={`rounded-md border px-3 py-2 text-sm ${checkResult.ok ? 'border-green-200 bg-green-50 text-green-700' : 'border-red-200 bg-red-50 text-red-700'}`}>
                  {checkResult.message}
                </div>
              )}
              {checkAlerts !== null && (
                <div>
                  <div className="mb-2 text-xs font-medium text-gray-500">该容器当前风险项（{checkAlerts.length}）</div>
                  {checkAlerts.length === 0 ? (
                    <div className="rounded-md border border-gray-200 px-3 py-6 text-center text-sm text-gray-500">未发现风险项</div>
                  ) : (
                    <div className="overflow-x-auto">
                      <table className="w-full text-sm">
                        <thead>
                          <tr className="border-b border-gray-100 bg-gray-50 text-left text-xs font-medium text-gray-500">
                            <th className="px-3 py-2 whitespace-nowrap">等级</th>
                            <th className="px-3 py-2 whitespace-nowrap">类型</th>
                            <th className="px-3 py-2">详情</th>
                            <th className="px-3 py-2 whitespace-nowrap">时间</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-gray-100">
                          {checkAlerts.map((alert) => (
                            <tr key={alert.id} className="hover:bg-gray-50">
                              <td className="px-3 py-2 whitespace-nowrap"><SeverityBadge severity={alert.severity} /></td>
                              <td className="px-3 py-2 text-gray-800 whitespace-nowrap">{typeLabels[alert.type] || alert.type}</td>
                              <td className="px-3 py-2 text-gray-600">{alert.detail}</td>
                              <td className="px-3 py-2 font-mono text-xs text-gray-500 whitespace-nowrap">{alert.timestamp}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                </div>
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
  const containerIP = alert.source_ip
  return logs.filter((log) => {
    // 出站告警容器为源地址，入站告警容器为目的地址，两者都算相关。
    if (containerIP && log.src_ip !== containerIP && log.dst_ip !== containerIP) return false
    if (alert.target_ip && alert.target_ip !== '*' && log.src_ip !== alert.target_ip && log.dst_ip !== alert.target_ip) {
      return false
    }
    if (alert.target_port > 0 && log.dst_port !== alert.target_port && log.src_port !== alert.target_port) {
      return false
    }
    return true
  })
}

function formatEndpoint(ip: string, port: number): string {
  if (!ip) return '-'
  return port > 0 ? `${ip}:${port}` : ip
}
