// components/ServerUsageBadge.tsx —— 用户门户卡片上的用量条（月流量）。
// 数据来自 getTrafficInfo（/containers/{id}/traffic，子用户可见）；
// 失败时静默隐藏，卡片主体不受影响。
import { useEffect, useState } from 'react'
import { getTrafficInfo, type TrafficInfo } from '../services/api'
import { useLanguage } from '../contexts/LanguageContext'

function fmtBytes(n: number): string {
  if (n >= 1024 * 1024 * 1024) return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(0)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(0)} KB`
  return `${n} B`
}

export default function ServerUsageBadge({ serverId }: { serverId: string | number }) {
  const { t } = useLanguage()
  const [traffic, setTraffic] = useState<TrafficInfo | null>(null)

  useEffect(() => {
    let alive = true
    getTrafficInfo(serverId as string)
      .then((res) => { if (alive) setTraffic(res.data?.data || null) })
      .catch(() => { /* 用量条失败静默 */ })
    return () => { alive = false }
  }, [serverId])

  if (!traffic) return null
  const used = traffic.total_used_bytes ?? 0
  const limitGB = traffic.limit_gb ?? 0
  if (limitGB <= 0) {
    // 不限量：显示已用即可
    return (
      <div className="mt-2 text-[11px] text-gray-500 dark:text-gray-400">
        {t('已用流量')}：{fmtBytes(used)} / ∞
      </div>
    )
  }
  const pct = Math.min(100, Math.round((used / (limitGB * 1024 * 1024 * 1024)) * 100))
  const warn = pct >= 80
  return (
    <div className="mt-2">
      <div className="flex items-center justify-between text-[11px] text-gray-500 dark:text-gray-400">
        <span>{t('本月流量')}</span>
        <span className={warn ? 'font-medium text-amber-600' : ''}>{fmtBytes(used)} / {limitGB} GB</span>
      </div>
      <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
        <div
          className={`h-full rounded-full transition-all ${warn ? 'bg-amber-500' : 'bg-brand-500'}`}
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  )
}
