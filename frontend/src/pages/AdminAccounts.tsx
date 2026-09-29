import { useCallback, useEffect, useState } from 'react'
import { KeyRound, Plus, ShieldCheck, Trash2, UserCog } from 'lucide-react'
import api from '../services/api'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'

type AdminAccount = {
  id: string
  username: string
  role: string
  disabled: boolean
  primary: boolean
  created_at?: string
  last_login_at?: string
}

const ROLE_LABELS: Record<string, string> = {
  admin: '管理员（全权）',
  operator: '运维（禁平台级）',
  readonly: '只读',
}

// AdminAccounts 管理额外管理员账号（多管理员）。主管理员由配置文件承载，
// 这里只读展示、不可改角色/删除。
export default function AdminAccounts() {
  const { adminRole } = useAuth()
  const { t } = useLanguage()
  const [accounts, setAccounts] = useState<AdminAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ username: '', password: '', role: 'operator' })
  const [pwdTarget, setPwdTarget] = useState<AdminAccount | null>(null)
  const [newPwd, setNewPwd] = useState('')

  // 只有全权管理员（含旧令牌，role 为空）能管理管理员账号。
  const canManage = adminRole === '' || adminRole === 'admin'

  const load = useCallback(async () => {
    try {
      const res = await api.get('/admins')
      setAccounts((res.data.data as AdminAccount[]) || [])
      setError('')
    } catch (e: unknown) {
      const err = e as { response?: { status?: number; data?: { message?: string } } }
      setError(err.response?.data?.message || t('加载管理员列表失败'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    void load()
  }, [load])

  const create = async () => {
    setError('')
    setNotice('')
    try {
      await api.post('/admins', form)
      setNotice(t('管理员已创建'))
      setShowCreate(false)
      setForm({ username: '', password: '', role: 'operator' })
      await load()
    } catch (e: unknown) {
      const err = e as { response?: { data?: { message?: string } } }
      setError(err.response?.data?.message || t('创建失败'))
    }
  }

  const patch = async (acct: AdminAccount, body: Record<string, unknown>) => {
    setError('')
    setNotice('')
    try {
      await api.patch(`/admins/${acct.id}`, body)
      setNotice(t('已更新'))
      await load()
    } catch (e: unknown) {
      const err = e as { response?: { data?: { message?: string } } }
      setError(err.response?.data?.message || t('更新失败'))
    }
  }

  const remove = async (acct: AdminAccount) => {
    setError('')
    setNotice('')
    try {
      await api.delete(`/admins/${acct.id}`)
      setNotice(t('已删除'))
      await load()
    } catch (e: unknown) {
      const err = e as { response?: { data?: { message?: string } } }
      setError(err.response?.data?.message || t('删除失败'))
    }
  }

  return (
    <div className="p-6">
      <div className="mb-5 flex items-center justify-between">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold text-gray-900 dark:text-white">
            <UserCog className="h-5 w-5" />
            {t('管理员账号')}
          </h1>
          <p className="mt-1 text-sm text-gray-500">
            {t('主管理员由系统配置承载；这里可添加额外管理员并按最小权限分配角色。')}
          </p>
        </div>
        <button
          type="button"
          disabled={!canManage}
          onClick={() => setShowCreate((v) => !v)}
          className="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-2 text-xs font-medium text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-40"
        >
          <Plus className="h-3.5 w-3.5" />
          {t('新建管理员')}
        </button>
      </div>

      {!canManage && (
        <div className="mb-4 rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
          {t('当前角色无权管理管理员账号，仅可查看。')}
        </div>
      )}
      {error && <div className="mb-4 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>}
      {notice && <div className="mb-4 rounded-md border border-green-200 bg-green-50 px-4 py-3 text-sm text-green-700">{notice}</div>}

      {showCreate && canManage && (
        <div className="mb-5 rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-4">
            <input
              className="rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-700 dark:bg-gray-800"
              placeholder={t('用户名')}
              value={form.username}
              onChange={(e) => setForm({ ...form, username: e.target.value })}
            />
            <input
              type="password"
              className="rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-700 dark:bg-gray-800"
              placeholder={t('初始密码（≥10 位含字母与数字）')}
              value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })}
            />
            <select
              className="rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-700 dark:bg-gray-800"
              value={form.role}
              onChange={(e) => setForm({ ...form, role: e.target.value })}
            >
              <option value="operator">{t(ROLE_LABELS.operator)}</option>
              <option value="readonly">{t(ROLE_LABELS.readonly)}</option>
              <option value="admin">{t(ROLE_LABELS.admin)}</option>
            </select>
            <button
              type="button"
              onClick={() => { void create() }}
              className="rounded-md bg-brand-600 px-3 py-2 text-sm font-medium text-white hover:bg-brand-700"
            >
              {t('创建')}
            </button>
          </div>
        </div>
      )}

      {loading ? (
        <div className="flex justify-center py-16">
          <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white" />
        </div>
      ) : (
        <div className="overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800">
          <table className="min-w-full divide-y divide-gray-200 text-sm dark:divide-gray-800">
            <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-900">
              <tr>
                <th className="px-4 py-2.5">{t('用户名')}</th>
                <th className="px-4 py-2.5">{t('角色')}</th>
                <th className="px-4 py-2.5">{t('状态')}</th>
                <th className="px-4 py-2.5">{t('最近登录')}</th>
                <th className="px-4 py-2.5 text-right">{t('操作')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 bg-white dark:divide-gray-800 dark:bg-gray-950">
              {accounts.map((a) => (
                <tr key={a.id || a.username}>
                  <td className="px-4 py-2.5 font-medium text-gray-900 dark:text-white">
                    {a.username}
                    {a.primary && (
                      <span className="ml-2 inline-flex items-center gap-1 rounded bg-brand-50 px-1.5 py-0.5 text-[11px] text-brand-600">
                        <ShieldCheck className="h-3 w-3" />{t('主管理员')}
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-2.5">
                    {a.primary ? (
                      <span className="text-gray-600 dark:text-gray-300">{t(ROLE_LABELS.admin)}</span>
                    ) : (
                      <select
                        disabled={!canManage}
                        value={a.role}
                        onChange={(e) => { void patch(a, { role: e.target.value }) }}
                        className="rounded border border-gray-300 px-2 py-1 text-xs disabled:opacity-60 dark:border-gray-700 dark:bg-gray-800"
                      >
                        <option value="admin">{t(ROLE_LABELS.admin)}</option>
                        <option value="operator">{t(ROLE_LABELS.operator)}</option>
                        <option value="readonly">{t(ROLE_LABELS.readonly)}</option>
                      </select>
                    )}
                  </td>
                  <td className="px-4 py-2.5">
                    {a.disabled ? (
                      <span className="rounded border border-gray-200 bg-gray-100 px-2 py-0.5 text-xs text-gray-600">{t('已禁用')}</span>
                    ) : (
                      <span className="rounded border border-green-200 bg-green-50 px-2 py-0.5 text-xs text-green-700">{t('正常')}</span>
                    )}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-gray-500">{a.last_login_at || '-'}</td>
                  <td className="px-4 py-2.5">
                    <div className="flex items-center justify-end gap-2">
                      <button
                        type="button"
                        disabled={!canManage}
                        onClick={() => { setPwdTarget(a); setNewPwd('') }}
                        className="inline-flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                      >
                        <KeyRound className="h-3 w-3" />{t('改密')}
                      </button>
                      {!a.primary && (
                        <>
                          <button
                            type="button"
                            disabled={!canManage}
                            onClick={() => { void patch(a, { disabled: !a.disabled }) }}
                            className="rounded border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                          >
                            {a.disabled ? t('启用') : t('禁用')}
                          </button>
                          <button
                            type="button"
                            disabled={!canManage}
                            onClick={() => { void remove(a) }}
                            className="inline-flex items-center gap-1 rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 disabled:opacity-40"
                          >
                            <Trash2 className="h-3 w-3" />{t('删除')}
                          </button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {pwdTarget && (
        <div className="fixed inset-0 z-30 flex items-center justify-center bg-black/40 px-4">
          <div className="w-full max-w-sm rounded-xl bg-white p-5 shadow-lg dark:bg-gray-900">
            <h3 className="text-sm font-semibold text-gray-900 dark:text-white">
              {t('重置密码')}：{pwdTarget.username}
            </h3>
            <input
              type="password"
              className="mt-3 w-full rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-gray-700 dark:bg-gray-800"
              placeholder={t('新密码（≥10 位含字母与数字）')}
              value={newPwd}
              onChange={(e) => setNewPwd(e.target.value)}
            />
            <p className="mt-2 text-xs text-gray-400">{t('改密会立即吊销该账号已签发的所有登录令牌。')}</p>
            <div className="mt-4 flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setPwdTarget(null)}
                className="rounded-md border border-gray-200 px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300"
              >
                {t('取消')}
              </button>
              <button
                type="button"
                onClick={() => {
                  const target = pwdTarget
                  setPwdTarget(null)
                  void patch(target, { password: newPwd })
                }}
                className="rounded-md bg-brand-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-brand-700"
              >
                {t('确认重置')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
