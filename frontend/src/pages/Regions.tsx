import { useCallback, useEffect, useState } from 'react'
import { Globe, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import {
  v2CreateRegion,
  v2DeleteRegion,
  v2ListRegions,
  v2UpdateRegion,
  V2Error,
  type V2Region,
  type V2RegionInput,
  type V2RegionSummary,
} from '../services/apiV2'
import { useLanguage } from '../contexts/LanguageContext'
import RegionFlag from '../components/RegionFlag'
import { countryLabel, groupedCountries } from '../utils/countries'

interface RegionDraft {
  name: string
  location: string
  country: string
  maxInstances: string
  maxRamMB: string
  maxDiskGB: string
}

const emptyDraft: RegionDraft = { name: '', location: '', country: '', maxInstances: '', maxRamMB: '', maxDiskGB: '' }

function draftFromRegion(region: V2Region): RegionDraft {
  return {
    name: region.name || '',
    location: region.location || '',
    country: (region.country || '').toLowerCase(),
    maxInstances: region.max_instances ? String(region.max_instances) : '',
    maxRamMB: region.max_ram_mb ? String(region.max_ram_mb) : '',
    maxDiskGB: region.max_disk_gb ? String(region.max_disk_gb) : '',
  }
}

// usagePercent：配额占比；未设配额（0/空）返回 null，前端据此不渲染该指标。
function usagePercent(used: number | undefined, max: number | undefined): number | null {
  if (!max || max <= 0) return null
  return Math.min(100, Math.round(((used || 0) / max) * 100))
}

function errorMessage(err: unknown, fallback: string): string {
  if (err instanceof V2Error) return err.requestId ? `${err.message}（request_id=${err.requestId}）` : err.message
  return (err as { response?: { data?: { message?: string } } })?.response?.data?.message || fallback
}

export default function Regions() {
  const { language, t } = useLanguage()
  const [regions, setRegions] = useState<V2Region[]>([])
  const [summary, setSummary] = useState<V2RegionSummary>({})
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [draft, setDraft] = useState<RegionDraft>(emptyDraft)
  const [error, setError] = useState('')
  const [formError, setFormError] = useState('')

  const fetchData = useCallback(async () => {
    try {
      const res = await v2ListRegions()
      setRegions(res.items || [])
      setSummary((res.summary as V2RegionSummary) || {})
      setError('')
    } catch (err: unknown) {
      setError(errorMessage(err, t('加载区域失败')))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [t])

  useEffect(() => { fetchData() }, [fetchData])

  const openCreate = () => {
    setEditingId(null)
    setDraft(emptyDraft)
    setFormError('')
    setFormOpen(true)
  }

  const openEdit = (region: V2Region) => {
    setEditingId(region.id)
    setDraft(draftFromRegion(region))
    setFormError('')
    setFormOpen(true)
  }

  const closeForm = () => {
    setFormOpen(false)
    setEditingId(null)
    setDraft(emptyDraft)
    setFormError('')
  }

  const buildPayload = (): V2RegionInput | null => {
    const name = draft.name.trim()
    if (!name) {
      setFormError(t('区域名称不能为空'))
      return null
    }
    const quotaFields: Array<[string, string]> = [
      [t('最大实例数'), draft.maxInstances],
      [t('最大内存（MB）'), draft.maxRamMB],
      [t('最大磁盘（GB）'), draft.maxDiskGB],
    ]
    const payload: V2RegionInput = {
      name,
      location: draft.location.trim(),
      country: draft.country.trim().toUpperCase(),
    }
    for (const [label, raw] of quotaFields) {
      const trimmed = raw.trim()
      if (!trimmed) continue
      const value = Number(trimmed)
      if (!Number.isFinite(value) || value < 0) {
        setFormError(`${label}${t('必须是不小于 0 的数字')}`)
        return null
      }
      if (label === t('最大实例数')) payload.max_instances = Math.floor(value)
      else if (label === t('最大内存（MB）')) payload.max_ram_mb = Math.floor(value)
      else payload.max_disk_gb = value
    }
    return payload
  }

  const submit = async () => {
    const payload = buildPayload()
    if (!payload) return
    setSaving(true)
    setFormError('')
    try {
      if (editingId) await v2UpdateRegion(editingId, payload)
      else await v2CreateRegion(payload)
      closeForm()
      await fetchData()
    } catch (err: unknown) {
      setFormError(errorMessage(err, t('保存失败，请稍后重试')))
    } finally {
      setSaving(false)
    }
  }

  const remove = async (region: V2Region) => {
    if (!window.confirm(`${t('确认删除区域')} ${region.name}（${region.id}）？${t('该操作不可撤销')}`)) return
    try {
      await v2DeleteRegion(region.id)
      if (editingId === region.id) closeForm()
      await fetchData()
    } catch (err: unknown) {
      window.alert(errorMessage(err, t('删除失败')))
    }
  }

  const countryGroups = groupedCountries(language)

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-brand-600" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-black dark:text-white">
            <Globe className="h-5 w-5" />{t('区域管理')}
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t('把节点按地域分组管理，并可设置区域级配额与国旗标识')}</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { setRefreshing(true); fetchData() }}
            disabled={refreshing}
            className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800"
          >
            <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />{t('刷新')}
          </button>
          <button onClick={openCreate} className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-3 py-2 text-sm text-white hover:bg-brand-700 dark:bg-brand-500 dark:text-white">
            <Plus className="h-4 w-4" />{t('新建区域')}
          </button>
        </div>
      </div>

      {error && <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{error}</div>}

      {(summary.unassigned_nodes || 0) > 0 && (
        <div className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-300">
          {t('有')} {summary.unassigned_nodes} {t('台节点尚未归属任何区域（在线')} {summary.unassigned_online || 0} {t('台），可在「节点管理」中为其指定区域')}
        </div>
      )}

      {formOpen && (
        <div className="rounded-xl border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
          <h2 className="mb-4 text-sm font-semibold text-black dark:text-white">{editingId ? t('编辑区域') : t('新建区域')}</h2>
          {formError && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{formError}</div>}
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t('名称')} <span className="text-red-500">*</span></label>
              <input type="text" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder="cn-north" className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t('位置（可选）')}</label>
              <input type="text" value={draft.location} onChange={(e) => setDraft((d) => ({ ...d, location: e.target.value }))} placeholder={t('华北 · 北京')} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t('国家/地区（可选）')}</label>
              <div className="flex items-center gap-2">
                <RegionFlag code={draft.country} size={26} />
                <select
                  value={draft.country}
                  onChange={(e) => setDraft((d) => ({ ...d, country: e.target.value }))}
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                >
                  <option value="">{t('不设置')}</option>
                  {countryGroups.map((group) => (
                    <optgroup key={group.group} label={group.label}>
                      {group.items.map((item) => (
                        <option key={item.code} value={item.code}>{item.label}（{item.code.toUpperCase()}）</option>
                      ))}
                    </optgroup>
                  ))}
                </select>
              </div>
            </div>
            <div className="grid grid-cols-3 gap-3">
              <div>
                <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t('最大实例数')}</label>
                <input type="number" min={0} value={draft.maxInstances} onChange={(e) => setDraft((d) => ({ ...d, maxInstances: e.target.value }))} placeholder={t('不限')} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
              </div>
              <div>
                <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t('最大内存（MB）')}</label>
                <input type="number" min={0} value={draft.maxRamMB} onChange={(e) => setDraft((d) => ({ ...d, maxRamMB: e.target.value }))} placeholder={t('不限')} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
              </div>
              <div>
                <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t('最大磁盘（GB）')}</label>
                <input type="number" min={0} value={draft.maxDiskGB} onChange={(e) => setDraft((d) => ({ ...d, maxDiskGB: e.target.value }))} placeholder={t('不限')} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
              </div>
            </div>
          </div>
          <p className="mt-2 text-[11px] text-gray-400">{t('配额留空表示不限制；用量按「该区域下节点上运行的实例」统计')}</p>
          <div className="mt-4 flex justify-end gap-2">
            <button onClick={closeForm} disabled={saving} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">{t('取消')}</button>
            <button onClick={submit} disabled={saving} className="rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-60 dark:bg-brand-500 dark:text-white">{saving ? t('保存中…') : editingId ? t('保存修改') : t('创建区域')}</button>
          </div>
        </div>
      )}

      <div className="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {regions.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-xl bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><Globe className="h-7 w-7" /></div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-200">{t('暂无区域')}</div>
            <div className="mt-1 text-xs text-gray-400">{t('点击「新建区域」创建第一个区域')}</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[900px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">{t('名称')}</th>
                  <th className="px-4 py-3 text-left font-medium">{t('位置')}</th>
                  <th className="px-4 py-3 text-left font-medium">{t('节点（在线/总数）')}</th>
                  <th className="px-4 py-3 text-left font-medium">{t('实例配额')}</th>
                  <th className="px-4 py-3 text-left font-medium">{t('内存配额')}</th>
                  <th className="px-4 py-3 text-left font-medium">{t('磁盘配额')}</th>
                  <th className="px-4 py-3 text-left font-medium">{t('创建时间')}</th>
                  <th className="px-4 py-3 text-right font-medium">{t('操作')}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {regions.map((r) => {
                  const instPct = usagePercent(r.used_instances, r.max_instances)
                  const ramPct = usagePercent(r.used_ram_mb, r.max_ram_mb)
                  const diskPct = usagePercent(r.used_disk_gb, r.max_disk_gb)
                  return (
                    <tr key={r.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2">
                          <RegionFlag code={r.country} size={20} title={countryLabel(r.country, language)} />
                          <div className="min-w-0">
                            <div className="truncate font-medium text-black dark:text-white">{r.name}</div>
                            <div className="font-mono text-[11px] text-gray-400">{r.id}</div>
                          </div>
                        </div>
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                        {r.location || '—'}
                        {r.country ? <span className="ml-1 text-gray-400">· {countryLabel(r.country, language)}</span> : null}
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                        {r.node_online || 0} / {r.node_count || 0}
                        {r.node_container_count ? <span className="ml-1 text-gray-400">· {t('容器')} {r.node_container_count}</span> : null}
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                        {r.max_instances && r.max_instances > 0
                          ? <span className={instPct !== null && instPct >= 100 ? 'text-red-600 dark:text-red-400' : ''}>{r.used_instances || 0} / {r.max_instances}</span>
                          : t('不限')}
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                        {r.max_ram_mb && r.max_ram_mb > 0 ? `${r.used_ram_mb || 0} / ${r.max_ram_mb} MB` : t('不限')}
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                        {r.max_disk_gb && r.max_disk_gb > 0 ? `${r.used_disk_gb || 0} / ${r.max_disk_gb} GB` : t('不限')}
                      </td>
                      <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{r.created_at || '—'}</td>
                      <td className="px-4 py-3 text-right">
                        <div className="inline-flex items-center gap-2">
                          <button onClick={() => openEdit(r)} className="inline-flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"><Pencil className="h-3 w-3" />{t('编辑')}</button>
                          <button onClick={() => remove(r)} className="inline-flex items-center gap-1 rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950"><Trash2 className="h-3 w-3" />{t('删除')}</button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
