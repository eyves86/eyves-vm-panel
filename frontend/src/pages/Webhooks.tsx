import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, Copy, Pencil, Plus, RefreshCw, Send, Trash2, Webhook, X } from 'lucide-react'
import { useDialog } from '../components/Dialog'
import {
  createWebhook,
  deleteWebhook,
  listWebhookEventTypes,
  listWebhooks,
  testWebhook,
  updateWebhook,
  type WebhookEventType,
  type WebhookSubscription,
} from '../services/api'
import { copyToClipboard } from '../utils/clipboard'
import { useLanguage } from '../contexts/LanguageContext'

interface WebhookForm {
  name: string
  url: string
  eventTypes: string[]
  enabled: boolean
}

const EMPTY_FORM: WebhookForm = { name: '', url: '', eventTypes: [], enabled: true }

// 事件目录分类的中文标签（后端 category 字段）。
const CATEGORY_LABELS: Record<string, string> = {
  container: '容器 / 虚拟机',
  node: '被控节点',
  security: '安全',
}

// 按分类分组的复选多选；不勾选任意项 = 订阅全部事件（由父级提示文案说明）。
function EventTypePicker({
  catalog,
  value,
  onChange,
}: {
  catalog: WebhookEventType[]
  value: string[]
  onChange: (next: string[]) => void
}) {
  const { t } = useLanguage()
  const toggle = (type: string) => {
    onChange(value.includes(type) ? value.filter((v) => v !== type) : [...value, type])
  }
  const groups: { category: string; items: WebhookEventType[] }[] = []
  for (const item of catalog) {
    let g = groups.find((x) => x.category === item.category)
    if (!g) {
      g = { category: item.category, items: [] }
      groups.push(g)
    }
    g.items.push(item)
  }
  return (
    <div className="divide-y divide-gray-100 rounded-md border border-gray-200 dark:divide-gray-800 dark:border-gray-700">
      {groups.map((g) => (
        <div key={g.category} className="px-3 py-2.5">
          <div className="mb-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">
            {t(CATEGORY_LABELS[g.category] || g.category)}
          </div>
          <div className="space-y-1.5">
            {g.items.map((item) => (
              <label key={item.type} className="flex cursor-pointer items-start gap-2">
                <input
                  type="checkbox"
                  checked={value.includes(item.type)}
                  onChange={() => toggle(item.type)}
                  className="mt-0.5 h-3.5 w-3.5 shrink-0 rounded border-gray-300 text-brand-600 focus:ring-brand-500 dark:border-gray-600"
                />
                <span className="min-w-0">
                  <span className="block font-mono text-xs text-black dark:text-white">{item.type}</span>
                  <span className="block text-xs text-gray-400">{t(item.description)}</span>
                </span>
              </label>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

function isDeliverySuccess(status: string | undefined): boolean {
  if (!status) return false
  return /^(2\d\d|success|ok)$/i.test(status)
}

export default function Webhooks() {
  const { t } = useLanguage()
  const dialog = useDialog()
  const [webhooks, setWebhooks] = useState<WebhookSubscription[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  // 新建订阅（创建成功后在弹窗内展示一次 secret）
  const [createOpen, setCreateOpen] = useState(false)
  const [createForm, setCreateForm] = useState<WebhookForm>(EMPTY_FORM)
  const [creating, setCreating] = useState(false)
  const [createdSecret, setCreatedSecret] = useState<{ name: string; secret: string } | null>(null)

  // 编辑订阅
  const [editTarget, setEditTarget] = useState<WebhookSubscription | null>(null)
  const [editForm, setEditForm] = useState<WebhookForm>(EMPTY_FORM)
  const [savingEdit, setSavingEdit] = useState(false)

  const [testingId, setTestingId] = useState<string | null>(null)

  // 事件类型目录（供多选渲染）
  const [eventCatalog, setEventCatalog] = useState<WebhookEventType[]>([])
  const [catalogError, setCatalogError] = useState('')

  const fetchWebhooks = useCallback(async () => {
    try {
      const res = await listWebhooks()
      setWebhooks(res.data.data || [])
      setError('')
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '加载 Webhook 订阅失败')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  const fetchEventCatalog = useCallback(async () => {
    try {
      const res = await listWebhookEventTypes()
      setEventCatalog(res.data.data || [])
      setCatalogError('')
    } catch (err: unknown) {
      // 目录拉取失败必须显式提示（否则多选为空 = 假 UI）
      setCatalogError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '加载事件类型失败')
    }
  }, [])

  useEffect(() => { fetchWebhooks() }, [fetchWebhooks])
  useEffect(() => { fetchEventCatalog() }, [fetchEventCatalog])

  // ---- 新建 ------------------------------------------------------------------
  const openCreate = () => {
    setCreateForm(EMPTY_FORM)
    setCreatedSecret(null)
    setCreateOpen(true)
  }

  const closeCreate = () => {
    setCreateOpen(false)
    setCreateForm(EMPTY_FORM)
    setCreatedSecret(null)
  }

  const submitCreate = async () => {
    if (creating) return
    if (!createForm.name.trim()) {
      dialog.alert('创建失败', '名称不能为空')
      return
    }
    if (!createForm.url.trim()) {
      dialog.alert('创建失败', '回调 URL 不能为空')
      return
    }
    setCreating(true)
    try {
      const res = await createWebhook({
        name: createForm.name.trim(),
        url: createForm.url.trim(),
        event_types: createForm.eventTypes.length > 0 ? createForm.eventTypes : undefined,
        enabled: createForm.enabled,
      })
      const created = res.data.data
      if (created?.secret) {
        // secret 仅此一次明文返回，在弹窗中展示
        setCreatedSecret({ name: created.name || createForm.name.trim(), secret: created.secret })
      } else {
        closeCreate()
        await fetchWebhooks()
        dialog.alert('完成', '订阅已创建')
      }
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('创建失败', e.response?.data?.message || '请稍后重试')
    } finally {
      setCreating(false)
    }
  }

  const finishCreate = async () => {
    closeCreate()
    await fetchWebhooks()
  }

  const copySecret = async (secret: string) => {
    const ok = await copyToClipboard(secret)
    if (ok) dialog.alert('复制成功', 'Secret 已复制到剪贴板')
  }

  // ---- 编辑 ------------------------------------------------------------------
  const openEdit = (hook: WebhookSubscription) => {
    setEditTarget(hook)
    setEditForm({
      name: hook.name,
      url: hook.url,
      eventTypes: hook.event_types || [],
      enabled: hook.enabled,
    })
  }

  const submitEdit = async () => {
    if (!editTarget || savingEdit) return
    if (!editForm.name.trim()) {
      dialog.alert('保存失败', '名称不能为空')
      return
    }
    if (!editForm.url.trim()) {
      dialog.alert('保存失败', '回调 URL 不能为空')
      return
    }
    setSavingEdit(true)
    try {
      await updateWebhook(editTarget.id, {
        name: editForm.name.trim(),
        url: editForm.url.trim(),
        // 全部取消勾选 = 恢复订阅全部事件（后端以空数组表示全部）
        event_types: editForm.eventTypes,
        enabled: editForm.enabled,
      })
      setEditTarget(null)
      await fetchWebhooks()
      dialog.alert('完成', '订阅已保存')
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('保存失败', e.response?.data?.message || '请稍后重试')
    } finally {
      setSavingEdit(false)
    }
  }

  // ---- 测试投递 / 删除 ---------------------------------------------------------
  const test = async (hook: WebhookSubscription) => {
    if (testingId) return
    setTestingId(hook.id)
    try {
      await testWebhook(hook.id)
      dialog.alert('测试成功', `测试事件已成功投递到 ${hook.url}`)
      await fetchWebhooks()
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('测试失败', e.response?.data?.message || '测试投递失败（DELIVERY_FAILED）')
      await fetchWebhooks()
    } finally {
      setTestingId(null)
    }
  }

  const remove = async (hook: WebhookSubscription) => {
    const ok = await dialog.confirm('删除订阅', `确定删除 Webhook 订阅「${hook.name}」？删除后将不再推送任何事件。`)
    if (!ok) return
    try {
      await deleteWebhook(hook.id)
      await fetchWebhooks()
      dialog.alert('完成', '订阅已删除')
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('删除失败', e.response?.data?.message || '请稍后重试')
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-brand-600" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-black dark:text-white">
            <Webhook className="h-5 w-5" />{t("Webhook 事件订阅")}
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {t("管理事件回调订阅；连续失败达 10 次将被自动停用")}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { setRefreshing(true); fetchWebhooks() }}
            disabled={refreshing}
            className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          >
            <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />{t("刷新")}
          </button>
          <button onClick={openCreate} className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-3 py-2 text-sm font-medium text-white hover:bg-brand-700 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400">
            <Plus className="h-4 w-4" />{t("新建订阅")}
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{error}</div>
      )}

      <div className="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {webhooks.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-xl bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400">
              <Webhook className="h-7 w-7" />
            </div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-300">{t("暂无 Webhook 订阅")}</div>
            <div className="mt-1 text-xs text-gray-400">{t("点击右上角「新建订阅」创建第一个事件回调")}</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[980px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">{t("名称")}</th>
                  <th className="px-4 py-3 text-left font-medium">{t("事件类型")}</th>
                  <th className="px-4 py-3 text-left font-medium">{t("状态")}</th>
                  <th className="px-4 py-3 text-left font-medium">{t("连续失败")}</th>
                  <th className="px-4 py-3 text-left font-medium">{t("最近投递")}</th>
                  <th className="px-4 py-3 text-right font-medium">{t("操作")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {webhooks.map((hook) => (
                  <tr key={hook.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="px-4 py-3">
                      <div className="font-medium text-black dark:text-white">{hook.name}</div>
                      <div className="max-w-[280px] truncate font-mono text-xs text-gray-400" title={hook.url}>{hook.url}</div>
                    </td>
                    <td className="px-4 py-3 max-w-[220px]">
                      {(hook.event_types || []).length > 0 ? (
                        <div className="flex flex-wrap gap-1">
                          {(hook.event_types || []).map((type) => (
                            <span key={type} className="inline-flex items-center rounded bg-indigo-50 px-1.5 py-0.5 text-xs font-medium text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300">{type}</span>
                          ))}
                        </div>
                      ) : (
                        <span className="text-xs text-gray-400">{t("全部事件")}</span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      {hook.auto_disabled_reason ? (
                        <span
                          className="inline-flex items-center gap-1 rounded bg-red-50 px-2 py-0.5 text-xs font-medium text-red-600 dark:bg-red-900/30 dark:text-red-400"
                          title={hook.auto_disabled_reason}
                        >
                          <AlertTriangle className="h-3 w-3" />{t("已自动停用")}
                        </span>
                      ) : hook.enabled ? (
                        <span className="inline-flex items-center rounded bg-emerald-50 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">{t("启用")}</span>
                      ) : (
                        <span className="inline-flex items-center rounded bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-500 dark:bg-gray-800 dark:text-gray-400">{t("已停用")}</span>
                      )}
                      {hook.auto_disabled_reason && (
                        <div className="mt-1 max-w-[200px] truncate text-xs text-red-500 dark:text-red-400" title={hook.auto_disabled_reason}>{hook.auto_disabled_reason}</div>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <span className={`text-xs font-medium ${(hook.consecutive_failures || 0) >= 10 ? 'text-red-600 dark:text-red-400' : (hook.consecutive_failures || 0) > 0 ? 'text-amber-600 dark:text-amber-400' : 'text-gray-500 dark:text-gray-400'}`}>
                        {hook.consecutive_failures || 0} {t("次")}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      {hook.last_delivery_at ? (
                        <div>
                          <div className="text-xs text-gray-600 dark:text-gray-400">{hook.last_delivery_at}</div>
                          {hook.last_delivery_status && (
                            <div className={`text-xs ${isDeliverySuccess(hook.last_delivery_status) ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-500 dark:text-red-400'}`}>
                              {hook.last_delivery_status}
                            </div>
                          )}
                        </div>
                      ) : (
                        <span className="text-xs text-gray-400">{t("尚未投递")}</span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex flex-wrap items-center justify-end gap-1">
                        <button
                          onClick={() => { void test(hook) }}
                          disabled={testingId === hook.id}
                          className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/30 transition-colors disabled:opacity-50"
                          title={t("发送一次测试事件")}
                        >
                          <Send className={`h-3.5 w-3.5 ${testingId === hook.id ? 'animate-pulse' : ''}`} />
                          {testingId === hook.id ? '投递中…' : '测试'}
                        </button>
                        <button
                          onClick={() => openEdit(hook)}
                          className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 transition-colors"
                          title={t("编辑订阅")}
                        >
                          <Pencil className="h-3.5 w-3.5" />
                          {t("编辑")}
                        </button>
                        <button
                          onClick={() => { void remove(hook) }}
                          className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/30 transition-colors"
                          title={t("删除订阅")}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          {t("删除")}
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

      {/* 新建订阅弹窗 */}
      {createOpen && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-xl border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-lg max-h-[85vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">{t("新建 Webhook 订阅")}</h3>
              <button onClick={() => { if (!createdSecret) closeCreate() }} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded">
                <X className="w-4 h-4" />
              </button>
            </div>

            {createdSecret ? (
              <div className="flex-1 overflow-y-auto p-5 space-y-4">
                <div className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-700 dark:border-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
                  {t("订阅「")}{createdSecret.name}{t("」已创建。以下签名密钥（Secret）")}<span className="font-semibold">{t("仅显示这一次")}</span>{t("，请立即妥善保存，之后将不会再显示。")}
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{t("签名密钥（Secret）")}</label>
                  <div className="flex items-center gap-2">
                    <code className="min-w-0 flex-1 break-all rounded-md border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-xs text-black dark:border-gray-700 dark:bg-gray-800 dark:text-white">
                      {createdSecret.secret}
                    </code>
                    <button
                      onClick={() => { void copySecret(createdSecret.secret) }}
                      className="shrink-0 inline-flex items-center gap-1 rounded-md border border-gray-300 px-2.5 py-2 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                      title={t("复制 Secret")}
                    >
                      <Copy className="h-3.5 w-3.5" />{t("复制")}
                    </button>
                  </div>
                </div>
              </div>
            ) : (
              <>
                <div className="flex-1 overflow-y-auto p-5 space-y-4">
                  <div>
                    <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{t("名称")} <span className="text-red-500">*</span></label>
                    <input
                      type="text"
                      value={createForm.name}
                      onChange={(e) => setCreateForm((f) => ({ ...f, name: e.target.value }))}
                      placeholder={t("部署通知")}
                      className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                    />
                  </div>
                  <div>
                    <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{t("回调 URL")} <span className="text-red-500">*</span></label>
                    <input
                      type="text"
                      value={createForm.url}
                      onChange={(e) => setCreateForm((f) => ({ ...f, url: e.target.value }))}
                      placeholder="https://example.com/webhook"
                      className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                    />
                  </div>
                  <div>
                    <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{t("订阅事件类型")}</label>
                    {catalogError ? (
                      <div className="flex items-center justify-between rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
                        <span>{catalogError}</span>
                        <button type="button" onClick={() => { void fetchEventCatalog() }} className="underline">{t("重试")}</button>
                      </div>
                    ) : eventCatalog.length === 0 ? (
                      <div className="rounded-md border border-gray-200 px-3 py-3 text-xs text-gray-400 dark:border-gray-700">{t("加载事件类型…")}</div>
                    ) : (
                      <EventTypePicker catalog={eventCatalog} value={createForm.eventTypes} onChange={(next) => setCreateForm((f) => ({ ...f, eventTypes: next }))} />
                    )}
                    <p className="mt-1 text-xs text-gray-400">{t("不勾选任何类型 = 订阅全部事件")}</p>
                  </div>
                  <div className="flex items-center justify-between rounded-md border border-gray-200 dark:border-gray-700 px-3 py-2.5">
                    <div>
                      <div className="text-sm text-black dark:text-white">{t("创建后立即启用")}</div>
                      <div className="mt-0.5 text-xs text-gray-400">{t("关闭则创建为停用状态，稍后可在编辑中开启")}</div>
                    </div>
                    <button
                      type="button"
                      onClick={() => setCreateForm((f) => ({ ...f, enabled: !f.enabled }))}
                      className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors ${createForm.enabled ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600'}`}
                      title={createForm.enabled ? '启用' : '停用'}
                    >
                      <span className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${createForm.enabled ? 'translate-x-4' : 'translate-x-0.5'}`} />
                    </button>
                  </div>
                </div>
                <div className="flex items-center justify-end gap-2 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
                  <button onClick={closeCreate} disabled={creating} className="px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 rounded-md">{t("取消")}</button>
                  <button
                    onClick={() => { void submitCreate() }}
                    disabled={creating}
                    className="inline-flex items-center gap-1.5 px-3 py-2 text-sm bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
                  >
                    <Plus className="h-4 w-4" />
                    {creating ? '创建中...' : '创建'}
                  </button>
                </div>
              </>
            )}

            {createdSecret && (
              <div className="flex items-center justify-end gap-2 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
                <button
                  onClick={() => { void finishCreate() }}
                  className="inline-flex items-center gap-1.5 px-3 py-2 text-sm bg-brand-600 text-white rounded-md hover:bg-brand-700 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
                >
                  {t("我已保存，关闭")}
                </button>
              </div>
            )}
          </div>
        </div>
      )}

      {/* 编辑订阅弹窗 */}
      {editTarget && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-xl border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-lg max-h-[85vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">{t("编辑 Webhook 订阅")}</h3>
              <button onClick={() => setEditTarget(null)} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-5 space-y-4">
              <div>
                <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{t("名称")} <span className="text-red-500">*</span></label>
                <input
                  type="text"
                  value={editForm.name}
                  onChange={(e) => setEditForm((f) => ({ ...f, name: e.target.value }))}
                  className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{t("回调 URL")} <span className="text-red-500">*</span></label>
                <input
                  type="text"
                  value={editForm.url}
                  onChange={(e) => setEditForm((f) => ({ ...f, url: e.target.value }))}
                  className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">{t("订阅事件类型")}</label>
                {catalogError ? (
                  <div className="flex items-center justify-between rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
                    <span>{catalogError}</span>
                    <button type="button" onClick={() => { void fetchEventCatalog() }} className="underline">{t("重试")}</button>
                  </div>
                ) : eventCatalog.length === 0 ? (
                  <div className="rounded-md border border-gray-200 px-3 py-3 text-xs text-gray-400 dark:border-gray-700">{t("加载事件类型…")}</div>
                ) : (
                  <EventTypePicker catalog={eventCatalog} value={editForm.eventTypes} onChange={(next) => setEditForm((f) => ({ ...f, eventTypes: next }))} />
                )}
                <p className="mt-1 text-xs text-gray-400">{t("全部取消勾选 = 恢复订阅全部事件")}</p>
              </div>
              <div className="flex items-center justify-between rounded-md border border-gray-200 dark:border-gray-700 px-3 py-2.5">
                <div>
                  <div className="text-sm text-black dark:text-white">{t("启用订阅")}</div>
                  <div className="mt-0.5 text-xs text-gray-400">{t("重新启用时，后端会自动清零连续失败计数")}</div>
                </div>
                <button
                  type="button"
                  onClick={() => setEditForm((f) => ({ ...f, enabled: !f.enabled }))}
                  className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors ${editForm.enabled ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600'}`}
                  title={editForm.enabled ? '启用' : '停用'}
                >
                  <span className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${editForm.enabled ? 'translate-x-4' : 'translate-x-0.5'}`} />
                </button>
              </div>
            </div>
            <div className="flex items-center justify-end gap-2 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
              <button onClick={() => setEditTarget(null)} className="px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 rounded-md">{t("取消")}</button>
              <button
                onClick={() => { void submitEdit() }}
                disabled={savingEdit}
                className="inline-flex items-center gap-1.5 px-3 py-2 text-sm bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
              >
                <RefreshCw className={`h-4 w-4 ${savingEdit ? 'animate-spin' : ''}`} />
                {savingEdit ? '保存中...' : '保存'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
