import { FormEvent, useEffect, useState } from 'react'
import { Copy, Info, KeyRound, Link2, LogOut, RefreshCw, ShieldCheck } from 'lucide-react'
import { useDialog } from '../components/Dialog'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { getSubUserProfile, selfRotatePassword, SubUserProfile } from '../services/api'
import { copyToClipboard } from '../utils/clipboard'

// UserSecurity 是用户门户的「安全设置」页：子用户查看自己的访问码分享链接，
// 以及自助轮换登录密码（仅服务端随机生成，不可自定义——满足"只能随机安全密码"）。
// 访问码与密码配套使用：他人用「访问码 + 密码」即可登录并管理绑定的容器。
export default function UserSecurity() {
  const dialog = useDialog()
  const { t } = useLanguage()
  const { logout } = useAuth()

  const [profile, setProfile] = useState<SubUserProfile | null>(null)
  const [loading, setLoading] = useState(true)
  const [oldPassword, setOldPassword] = useState('')
  const [rotating, setRotating] = useState(false)
  // 轮换成功后的一次性新密码展示（关闭页面/重新登录前提醒抄存）
  const [newPassword, setNewPassword] = useState('')

  useEffect(() => {
    let cancelled = false
    getSubUserProfile()
      .then((res) => {
        if (!cancelled) setProfile(res.data.data || null)
      })
      .catch(() => {
        if (!cancelled) dialog.alert(t('错误'), t('获取账号信息失败'))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [dialog, t])

  const shareUrl = profile?.access_code
    ? `${window.location.origin}/user/login?code=${encodeURIComponent(profile.access_code)}`
    : ''

  const copyText = async (text: string) => {
    await copyToClipboard(text)
  }

  const handleRotate = async (event: FormEvent) => {
    event.preventDefault()
    if (!oldPassword) return
    const ok = await dialog.confirm(
      t('轮换密码'),
      t('将生成新的随机密码，旧密码立即失效，所有已登录设备需重新登录。继续？')
    )
    if (!ok) return
    setRotating(true)
    try {
      const res = await selfRotatePassword(oldPassword)
      setNewPassword(res.data.data?.password || '')
      setOldPassword('')
    } catch (err: unknown) {
      const e = err as { response?: { status?: number; data?: { message?: string } } }
      const msg = e.response?.status === 401
        ? t('当前密码不正确')
        : (e.response?.data?.message || t('轮换失败，请稍后重试'))
      await dialog.alert(t('轮换失败'), msg)
    } finally {
      setRotating(false)
    }
  }

  const cardClass = 'rounded-xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900'
  const labelClass = 'flex items-center gap-1.5 text-sm font-medium text-black dark:text-white'
  const mutedClass = 'text-xs text-gray-500 dark:text-gray-400'

  if (loading) {
    return (
      <div className="flex items-center justify-center py-16">
        <div className="h-7 w-7 animate-spin rounded-full border-b-2 border-brand-600" />
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <div>
        <h1 className="flex items-center gap-2 text-lg font-semibold text-black dark:text-white">
          <ShieldCheck className="h-5 w-5 text-brand-600" />
          {t('安全设置')}
        </h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {t('管理你的登录密码与访问码分享链接。')}
        </p>
      </div>

      {/* 账号信息 */}
      <section className={`${cardClass} p-5`}>
        <h2 className={labelClass}>
          <Info className="h-4 w-4 text-gray-400" />
          {t('账号信息')}
        </h2>
        <dl className="mt-3 grid gap-3 sm:grid-cols-3">
          <div>
            <dt className={mutedClass}>{t('用户名')}</dt>
            <dd className="mt-0.5 break-all font-mono text-sm text-black dark:text-white">{profile?.username || '-'}</dd>
          </div>
          <div>
            <dt className={mutedClass}>{t('角色')}</dt>
            <dd className="mt-0.5 text-sm text-black dark:text-white">
              {profile?.role === 'viewer' ? t('只读') : t('操作员')}
            </dd>
          </div>
          <div>
            <dt className={mutedClass}>{t('绑定容器')}</dt>
            <dd className="mt-0.5 text-sm text-black dark:text-white">{profile?.container_count ?? 0}</dd>
          </div>
        </dl>
      </section>

      {/* 访问码分享 */}
      <section className={`${cardClass} p-5`}>
        <h2 className={labelClass}>
          <Link2 className="h-4 w-4 text-gray-400" />
          {t('访问码分享')}
        </h2>
        <p className={`mt-1.5 ${mutedClass}`}>
          {t('把分享链接发给他人：对方打开后输入当前密码，即可登录并管理你绑定的容器。')}{' '}
          {t('该密码与你的账号密码相同，轮换密码后分享立即使用新密码。')}
        </p>
        <div className="mt-3 space-y-2.5">
          <div className="flex items-center justify-between gap-3 rounded-lg bg-gray-50 px-3 py-2.5 dark:bg-gray-800">
            <div className="min-w-0">
              <p className={mutedClass}>{t('访问码')}</p>
              <p className="mt-0.5 break-all font-mono text-sm text-black dark:text-white">{profile?.access_code || '-'}</p>
            </div>
            <button
              type="button"
              onClick={() => { void copyText(profile?.access_code || '') }}
              className="shrink-0 rounded-md border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              <Copy className="mr-1 inline h-3 w-3" />
              {t('复制')}
            </button>
          </div>
          <div className="flex items-center justify-between gap-3 rounded-lg bg-gray-50 px-3 py-2.5 dark:bg-gray-800">
            <div className="min-w-0">
              <p className={mutedClass}>{t('分享链接')}</p>
              <p className="mt-0.5 break-all font-mono text-xs text-black dark:text-white">{shareUrl || '-'}</p>
            </div>
            <button
              type="button"
              onClick={() => { void copyText(shareUrl) }}
              className="shrink-0 rounded-md border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              <Copy className="mr-1 inline h-3 w-3" />
              {t('复制')}
            </button>
          </div>
        </div>
      </section>

      {/* 密码管理：只能随机轮换 */}
      <section className={`${cardClass} p-5`}>
        <h2 className={labelClass}>
          <KeyRound className="h-4 w-4 text-gray-400" />
          {t('密码管理')}
        </h2>
        <p className={`mt-1.5 ${mutedClass}`}>
          {t('出于安全考虑，密码只能由系统随机生成（16 位），不支持自定义密码。')}
        </p>

        {newPassword ? (
          <div className="mt-4 space-y-3">
            <div className="rounded-lg border border-amber-200 bg-amber-50 px-3.5 py-3 dark:border-amber-900/60 dark:bg-amber-900/30">
              <p className="text-xs font-medium text-amber-700 dark:text-amber-300">{t('新密码（仅此一次显示，请立即抄存）')}</p>
              <div className="mt-1.5 flex items-center gap-2">
                <code className="min-w-0 flex-1 break-all rounded bg-white px-2 py-1.5 font-mono text-sm text-black dark:bg-gray-900 dark:text-white">{newPassword}</code>
                <button
                  type="button"
                  onClick={() => { void copyText(newPassword) }}
                  className="shrink-0 rounded-md border border-amber-300 bg-white px-2.5 py-1.5 text-xs text-amber-700 hover:bg-amber-100 dark:border-amber-800 dark:bg-gray-900 dark:text-amber-300 dark:hover:bg-amber-900/50"
                >
                  <Copy className="mr-1 inline h-3 w-3" />
                  {t('复制')}
                </button>
              </div>
            </div>
            <div className="flex items-center justify-between gap-3 rounded-lg bg-gray-50 px-3.5 py-3 dark:bg-gray-800">
              <p className={mutedClass}>{t('旧密码已失效，请使用新密码重新登录。')}</p>
              <button
                type="button"
                onClick={() => { logout() }}
                className="inline-flex shrink-0 items-center gap-1.5 rounded-md bg-brand-600 px-3 py-2 text-xs font-medium text-white hover:bg-brand-700 dark:bg-brand-500 dark:hover:bg-brand-400"
              >
                <LogOut className="h-3.5 w-3.5" />
                {t('重新登录')}
              </button>
            </div>
          </div>
        ) : (
          <form onSubmit={handleRotate} className="mt-3 flex items-end gap-2">
            <div className="min-w-0 flex-1">
              <label className={`mb-1 block ${mutedClass}`}>{t('当前密码')}</label>
              <input
                type="password"
                value={oldPassword}
                onChange={(e) => setOldPassword(e.target.value)}
                required
                autoComplete="current-password"
                className="w-full rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-black outline-none focus:border-brand-500 focus:bg-white dark:border-gray-700 dark:bg-gray-800 dark:text-white dark:focus:bg-gray-900"
                placeholder={t('输入当前密码')}
              />
            </div>
            <button
              type="submit"
              disabled={rotating || !oldPassword}
              className="inline-flex shrink-0 items-center gap-1.5 rounded-lg bg-brand-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-brand-500 dark:hover:bg-brand-400"
            >
              <RefreshCw className={`h-4 w-4 ${rotating ? 'animate-spin' : ''}`} />
              {rotating ? t('轮换中...') : t('生成随机新密码')}
            </button>
          </form>
        )}
      </section>
    </div>
  )
}
