import { useCallback, useEffect, useState } from 'react'
import { Database, Save } from 'lucide-react'
import {
  getMetricRetention,
  setMetricRetention,
  type MetricRetentionSettings,
} from '../services/api'

export default function MetricRetention() {
  const [settings, setSettings] = useState<MetricRetentionSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [days, setDays] = useState('0')
  const [error, setError] = useState('')

  const fetchData = useCallback(async () => {
    try {
      const res = await getMetricRetention()
      const s = res.data.data
      setSettings(s ?? null)
      if (s) setDays(String(s.retention_days))
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '加载指标留存设置失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { fetchData() }, [fetchData])

  const save = async () => {
    const v = Math.max(0, Math.min(3650, Math.round(Number(days) || 0)))
    setSaving(true)
    setError('')
    try {
      await setMetricRetention(v)
      setDays(String(v))
      fetchData()
      alert('已保存指标留存策略')
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '保存失败，请稍后重试')
    } finally {
      setSaving(false)
    }
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
            <Database className="h-5 w-5" />指标留存策略
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">控制采样指标的保留天数与原始数据保留策略</p>
        </div>
      </div>

      <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
        {error && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700">{error}</div>}
        <div className="grid gap-4 md:grid-cols-2">
          <div>
            <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">保留天数</label>
            <input type="number" min={0} max={3650} value={days} onChange={(e) => setDays(e.target.value)} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
          </div>
          <div className="rounded-md border border-gray-200 bg-gray-50 p-3 text-xs text-gray-600 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-300">
            <div>当前保留：{settings?.retention_days ?? 0} 天（0=永久）</div>
            <div>采样间隔：{settings?.sample_interval_secs ?? '—'} 秒</div>
            <div>原始采样保留：{settings?.raw_history_secs ?? 0} 秒（{Math.round((settings?.raw_history_secs ?? 0) / 3600)} 小时）</div>
          </div>
        </div>
        {settings?.retention_notes && (
          <div className="mt-4 rounded-md border border-gray-200 bg-gray-50 px-3 py-2 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
            {settings.retention_notes}
          </div>
        )}
        <div className="mt-4 flex justify-end">
          <button onClick={save} disabled={saving} className="inline-flex items-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black">
            <Save className="h-4 w-4" />{saving ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}