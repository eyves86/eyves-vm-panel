import { useCallback, useEffect, useState } from 'react'
import { Copy, Eye, KeyRound, Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react'
import { useDialog } from '../components/Dialog'
import { copyToClipboard } from '../utils/clipboard'
import {
  createSSHKey,
  deleteSSHKey,
  getSSHKey,
  listSSHKeys,
  updateSSHKey,
  type SSHKey,
  type SSHKeySummary,
} from '../services/api'
import { useLanguage } from '../contexts/LanguageContext'

const inputCls = 'w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white'

function errMessage(err: unknown, fallback: string): string {
  return (err as { response?: { data?: { message?: string } } }).response?.data?.message || fallback
}

function TypeBadge({ type }: { type: string }) {
  const { t } = useLanguage()
  if (type === 'admin') {
    return <span className="inline-flex items-center rounded bg-blue-50 px-2 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">{t("管理员")}</span>
  }
  if (type === 'subuser') {
    return <span className="inline-flex items-center rounded bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-gray-800 dark:text-gray-300">{t("子用户")}</span>
  }
  return <span className="inline-flex items-center rounded bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-gray-800 dark:text-gray-300">{type || '—'}</span>
}

export default function SSHKeys() {
  const { t } = useLanguage()
  const dialog = useDialog()
  const [keys, setKeys] = useState<SSHKeySummary[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  // 新建公钥
  const [createOpen, setCreateOpen] = useState(false)
  const [createForm, setCreateForm] = useState<{ name: string; public_key: string }>({ name: '', public_key: '' })
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')

  // 重命名
  const [renameKey, setRenameKey] = useState<SSHKeySummary | null>(null)
  const [renameName, setRenameName] = useState('')
  const [savingRename, setSavingRename] = useState(false)
  const [renameError, setRenameError] = useState('')

  // 查看公钥详情
  const [viewTarget, setViewTarget] = useState<SSHKeySummary | null>(null)
  const [viewDetail, setViewDetail] = useState<SSHKey | null>(null)
  const [viewLoading, setViewLoading] = useState(false)
  const [viewError, setViewError] = useState('')

  const fetchKeys = useCallback(async () => {
    try {
      const res = await listSSHKeys()
      setKeys(res.data.data || [])
      setError('')
    } catch (err: unknown) {
      setError(errMessage(err, '加载 SSH 公钥失败'))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { fetchKeys() }, [fetchKeys])

  // ---- 新建公钥 ------------------------------------------------------------
  const openCreate = () => {
    setCreateForm({ name: '', public_key: '' })
    setCreateError('')
    setCreateOpen(true)
  }

  const submitCreate = async () => {
    if (!createForm.name.trim()) {
      setCreateError('名称不能为空')
      return
    }
    if (!createForm.public_key.trim()) {
      setCreateError('公钥内容不能为空')
      return
    }
    setCreating(true)
    setCreateError('')
    try {
      await createSSHKey({
        name: createForm.name.trim(),
        public_key: createForm.public_key.trim(),
      })
      setCreateOpen(false)
      await fetchKeys()
    } catch (err: unknown) {
      setCreateError(errMessage(err, '创建失败，请稍后重试'))
    } finally {
      setCreating(false)
    }
  }

  // ---- 重命名 --------------------------------------------------------------
  const openRename = (k: SSHKeySummary) => {
    setRenameKey(k)
    setRenameName(k.name)
    setRenameError('')
  }

  const submitRename = async () => {
    if (!renameKey) return
    if (!renameName.trim()) {
      setRenameError('名称不能为空')
      return
    }
    setSavingRename(true)
    setRenameError('')
    try {
      await updateSSHKey(renameKey.id, { name: renameName.trim() })
      setRenameKey(null)
      await fetchKeys()
    } catch (err: unknown) {
      setRenameError(errMessage(err, '重命名失败，请稍后重试'))
    } finally {
      setSavingRename(false)
    }
  }

  // ---- 删除 ----------------------------------------------------------------
  const removeKey = async (k: SSHKeySummary) => {
    const ok = await dialog.confirm('删除 SSH 公钥', `确定删除公钥「${k.name}」？删除后使用该公钥的登录将失效，该操作不可恢复。`)
    if (!ok) return
    try {
      await deleteSSHKey(k.id)
      await fetchKeys()
    } catch (err: unknown) {
      dialog.alert('删除失败', errMessage(err, '删除失败，请稍后重试'))
    }
  }

  // ---- 查看公钥详情 ----------------------------------------------------------
  const openView = async (k: SSHKeySummary) => {
    setViewTarget(k)
    setViewDetail(null)
    setViewError('')
    setViewLoading(true)
    try {
      const res = await getSSHKey(k.id)
      setViewDetail(res.data.data || null)
    } catch (err: unknown) {
      setViewError(errMessage(err, '获取公钥详情失败'))
    } finally {
      setViewLoading(false)
    }
  }

  const closeView = () => {
    setViewTarget(null)
    setViewDetail(null)
    setViewError('')
  }

  const copyPublicKey = async () => {
    if (!viewDetail?.public_key) return
    const ok = await copyToClipboard(viewDetail.public_key)
    dialog.alert(ok ? '完成' : '复制失败', ok ? '公钥已复制到剪贴板' : '复制失败，请手动选择文本复制')
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-black dark:text-white">
            <KeyRound className="h-5 w-5" />{t("SSH 公钥")}
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("托管 SSH 公钥，用于虚拟机/容器 SSH 登录授权")}</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { setRefreshing(true); fetchKeys() }}
            disabled={refreshing}
            className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          >
            <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />{t("刷新")}
          </button>
          <button onClick={openCreate} className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-3 py-2 text-sm text-white hover:bg-brand-700 dark:bg-brand-500 dark:text-white">
            <Plus className="h-4 w-4" />{t("新建公钥")}
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{error}</div>
      )}

      <div className="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {keys.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-xl bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><KeyRound className="h-7 w-7" /></div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("暂无 SSH 公钥")}</div>
            <div className="mt-1 text-xs text-gray-400">{t("点击「新建公钥」添加第一个公钥")}</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">{t("名称")}</th>
                  <th className="px-4 py-3 text-left font-medium">{t("指纹")}</th>
                  <th className="px-4 py-3 text-left font-medium">{t("类型")}</th>
                  <th className="px-4 py-3 text-left font-medium">{t("创建时间")}</th>
                  <th className="px-4 py-3 text-left font-medium">{t("最近使用")}</th>
                  <th className="px-4 py-3 text-right font-medium">{t("操作")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {keys.map((k) => (
                  <tr key={k.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="px-4 py-3">
                      <div className="text-gray-800 dark:text-gray-100">{k.name}</div>
                    </td>
                    <td className="px-4 py-3">
                      <div className="font-mono text-xs text-gray-500 dark:text-gray-400">{k.fingerprint || '—'}</div>
                    </td>
                    <td className="px-4 py-3"><TypeBadge type={k.type} /></td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{k.created_at || '—'}</td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{k.last_used_at || '从未使用'}</td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex justify-end gap-1.5">
                        <button
                          onClick={() => void openView(k)}
                          className="inline-flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                        >
                          <Eye className="h-3 w-3" />{t("查看")}
                        </button>
                        <button
                          onClick={() => openRename(k)}
                          className="inline-flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                        >
                          <Pencil className="h-3 w-3" />{t("重命名")}
                        </button>
                        <button
                          onClick={() => void removeKey(k)}
                          className="inline-flex items-center gap-1 rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950"
                        >
                          <Trash2 className="h-3 w-3" />{t("删除")}
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

      {/* 新建公钥 Modal */}
      {createOpen && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/50 p-4 dark:bg-black/70 z-50">
          <div className="flex w-full max-w-md flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">{t("新建 SSH 公钥")}</h3>
              <button onClick={() => setCreateOpen(false)} className="rounded p-1 text-gray-400 hover:text-black dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              {createError && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{createError}</div>}
              <div className="space-y-4">
                <div>
                  <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t("名称")} <span className="text-red-500">*</span></label>
                  <input
                    type="text"
                    value={createForm.name}
                    onChange={(e) => setCreateForm((d) => ({ ...d, name: e.target.value }))}
                    placeholder="my-laptop"
                    className={inputCls}
                  />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t("公钥内容")} <span className="text-red-500">*</span></label>
                  <textarea
                    value={createForm.public_key}
                    onChange={(e) => setCreateForm((d) => ({ ...d, public_key: e.target.value }))}
                    placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI..."
                    rows={5}
                    className={`${inputCls} font-mono text-xs`}
                  />
                  <p className="mt-1 text-xs text-gray-400">{t("支持 ssh-rsa / ssh-ed25519 等格式的公钥")}</p>
                </div>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button onClick={() => setCreateOpen(false)} disabled={creating} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-100 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700">{t("取消")}</button>
              <button onClick={submitCreate} disabled={creating} className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white">
                {creating && <RefreshCw className="h-4 w-4 animate-spin" />}
                {creating ? '创建中…' : '创建'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 重命名 Modal */}
      {renameKey && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/50 p-4 dark:bg-black/70 z-50">
          <div className="flex w-full max-w-md flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">{t("重命名公钥")}</h3>
              <button onClick={() => setRenameKey(null)} className="rounded p-1 text-gray-400 hover:text-black dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              {renameError && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{renameError}</div>}
              <div>
                <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t("名称")} <span className="text-red-500">*</span></label>
                <input
                  type="text"
                  value={renameName}
                  onChange={(e) => setRenameName(e.target.value)}
                  className={inputCls}
                />
                <p className="mt-1 text-xs text-gray-400">{t("仅支持修改名称，公钥内容不可变更")}</p>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button onClick={() => setRenameKey(null)} disabled={savingRename} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-100 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700">{t("取消")}</button>
              <button onClick={submitRename} disabled={savingRename} className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white">
                {savingRename && <RefreshCw className="h-4 w-4 animate-spin" />}
                {savingRename ? '保存中…' : '保存'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 公钥详情 Modal */}
      {viewTarget && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/50 p-4 dark:bg-black/70 z-50">
          <div className="flex max-h-[85vh] w-full max-w-2xl flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">{t("公钥详情 -")} {viewTarget.name}</h3>
              <button onClick={closeView} className="rounded p-1 text-gray-400 hover:text-black dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              {viewLoading ? (
                <div className="flex items-center justify-center py-12">
                  <div className="h-6 w-6 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white" />
                </div>
              ) : viewError ? (
                <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{viewError}</div>
              ) : viewDetail ? (
                <>
                  <div className="mb-4 grid gap-x-6 gap-y-2 rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-xs text-gray-600 sm:grid-cols-2 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-300">
                    <div className="flex items-center gap-2">
                      <span className="text-gray-400">{t("指纹：")}</span>
                      <span className="font-mono">{viewDetail.fingerprint || '—'}</span>
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="text-gray-400">{t("类型：")}</span>
                      <TypeBadge type={viewDetail.type} />
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="text-gray-400">{t("创建时间：")}</span>
                      <span>{viewDetail.created_at || '—'}</span>
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="text-gray-400">{t("最近使用：")}</span>
                      <span>{viewDetail.last_used_at || '从未使用'}</span>
                    </div>
                  </div>
                  <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t("公钥内容")}</label>
                  <div className="relative">
                    <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 font-mono text-xs text-gray-800 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-200">{viewDetail.public_key}</pre>
                  </div>
                </>
              ) : null}
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button onClick={closeView} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700">{t("关闭")}</button>
              <button
                onClick={() => void copyPublicKey()}
                disabled={!viewDetail?.public_key}
                className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white"
              >
                <Copy className="h-4 w-4" />{t("复制公钥")}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
