import { FormEvent, useEffect, useState } from 'react'
import { Copy, Info, KeyRound, Link2, LogOut, RefreshCw, ShieldCheck } from 'lucide-react'
import { useDialog } from '../components/Dialog'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { getSubUserProfile, selfRotateAccessCodePassword, selfRotatePassword, SubUserAccessLink, SubUserProfile } from '../services/api'
import { copyToClipboard } from '../utils/clipboard'

// UserSecurity 是用户门户的「安全设置」页：子用户查看每台机器各自的访问码分享凭据
// （机器级访问码 + 访问码密码），以及自助轮换账号密码 / 某台机器的访问码口令。
//
// 两套凭据相互独立：
//   - 账号密码（用户名/邮箱登录）：管理当前会话授权的全部容器；访问码会话禁止修改它。
//   - 访问码密码（访问码登录）：一码一机，凭它登录仅能管理对应那一台容器。
interface RotatedLink {
  container_uuid: string
  container_name: string
  access_code: string
  access_code_password: string
}

export default function UserSecurity() {
  const dialog = useDialog()
  const { t } = useLanguage()
  const { logout } = useAuth()

  const [profile, setProfile] = useState<SubUserProfile | null>(null)
  const [loading, setLoading] = useState(true)
  const [oldPassword, setOldPassword] = useState('')
  const [rotating, setRotating] = useState(false)
  // 正在重置口令的机器（container_uuid）；null 表示无进行中的重置。
  const [rotatingLink, setRotatingLink] = useState<string | null>(null)
  // 轮换成功后的一次性新凭据展示（关闭页面/重新登录前提醒抄存）
  const [newPassword, setNewPassword] = useState('')
  const [rotatedLink, setRotatedLink] = useState<RotatedLink | null>(null)

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

  const linkShareUrl = (link: SubUserAccessLink) =>
    link.login_url || `${window.location.origin}/user/login?code=${encodeURIComponent(link.access_code)}`

  // 访问码登录的会话不能修改账号密码（服务端同样强制，见 HandleSubUserChangePassword）。
  const isAccessCodeSession = profile?.via === 'access_code'

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

  const handleRotateAccessCode = async (link: SubUserAccessLink) => {
    if (rotatingLink) return
    const ok = await dialog.confirm(
      t('重置访问码口令'),
      t('将为该机器生成新的访问码口令，旧口令与已登录设备立即失效。确定继续？')
    )
    if (!ok) return
    setRotatingLink(link.container_uuid)
    try {
      const res = await selfRotateAccessCodePassword(link.container_uuid)
      const data = res.data.data
      if (data) {
        setRotatedLink({
          container_uuid: data.container_uuid || link.container_uuid,
          container_name: data.container_name || link.container_name,
          access_code: data.access_code || link.access_code,
          access_code_password: data.access_code_password,
        })
        setProfile((prev) => prev
          ? {
              ...prev,
              access_links: prev.access_links.map((item) => item.container_uuid === link.container_uuid
                ? { ...item, access_code: data.access_code || item.access_code, access_code_password: data.access_code_password }
                : item),
            }
          : prev)
      }
    } catch (err: unknown) {
      const e = err as { response?: { status?: number; data?: { message?: string } } }
      await dialog.alert(t('轮换失败'), e.response?.data?.message || t('轮换失败，请稍后重试'))
    } finally {
      setRotatingLink(null)
    }
  }

  const cardClass = 'rounded-xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900'
  const labelClass = 'flex items-center gap-1.5 text-sm font-medium text-black dark:text-white'
  const mutedClass = 'text-xs text-gray-500 dark:text-gray-400'
  const copyBtnClass = 'shrink-0 rounded-md border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700'

  if (loading) {
    return (
      <div className="flex items-center justify-center py-16">
        <div className="h-7 w-7 animate-spin rounded-full border-b-2 border-brand-600" />
      </div>
    )
  }

  const accessLinks = profile?.access_links || []

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <div>
        <h1 className="flex items-center gap-2 text-lg font-semibold text-black dark:text-white">
          <ShieldCheck className="h-5 w-5 text-brand-600" />
          {t('安全设置')}
        </h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {t('管理你的登录密码与访问码分享凭据。')}
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

      {/* 机器级访问码分享：一码一机，登录后仅能管理对应那一台 */}
      <section className={`${cardClass} p-5`}>
        <h2 className={labelClass}>
          <Link2 className="h-4 w-4 text-gray-400" />
          {t('访问码分享')}
        </h2>
        <p className={`mt-1.5 ${mutedClass}`}>
          {t('访问码是「机器级」凭据：每一台容器各自持有一个访问码，把对应链接发给他人，对方输入「访问码 + 访问码密码」登录后仅能管理该台机器。')}{' '}
          {t('访问码密码与你的账号密码相互独立，通过访问码登录的人不能修改账号密码。')}
        </p>

        {accessLinks.length === 0 ? (
          <div className="mt-3 rounded-xl bg-gray-50 px-3.5 py-3 text-xs text-gray-500 dark:bg-gray-800 dark:text-gray-400">
            {t('当前没有可分发的机器。')}
          </div>
        ) : (
          <div className="mt-3 space-y-3">
            {accessLinks.map((link) => (
              <div key={link.container_uuid} className="rounded-xl border border-gray-200 dark:border-gray-700">
                <div className="flex items-center justify-between gap-2 border-b border-gray-100 dark:border-gray-800 px-3.5 py-2">
                  <span className="truncate text-sm font-medium text-black dark:text-white">{link.container_name || link.container_uuid}</span>
                  <button
                    type="button"
                    onClick={() => void handleRotateAccessCode(link)}
                    disabled={rotatingLink !== null}
                    className="inline-flex shrink-0 items-center gap-1 rounded-md border border-gray-200 px-2 py-1 text-[11px] text-amber-700 hover:bg-amber-50 disabled:opacity-50 dark:border-gray-700 dark:text-amber-300 dark:hover:bg-amber-900/30"
                  >
                    <RefreshCw className={`h-3 w-3 ${rotatingLink === link.container_uuid ? 'animate-spin' : ''}`} />
                    {rotatingLink === link.container_uuid ? t('重置中...') : t('重置访问码口令')}
                  </button>
                </div>
                <div className="space-y-2.5 px-3.5 py-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <p className={mutedClass}>{t('访问码')}</p>
                      <p className="mt-0.5 break-all font-mono text-sm text-black dark:text-white">{link.access_code || '-'}</p>
                    </div>
                    <button type="button" onClick={() => { void copyText(link.access_code) }} className={copyBtnClass}>
                      <Copy className="mr-1 inline h-3 w-3" />
                      {t('复制')}
                    </button>
                  </div>
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <p className={mutedClass}>{t('访问码密码')}</p>
                      <p className="mt-0.5 break-all font-mono text-sm text-black dark:text-white">{link.access_code_password || '-'}</p>
                    </div>
                    <button type="button" onClick={() => { void copyText(link.access_code_password) }} className={copyBtnClass}>
                      <Copy className="mr-1 inline h-3 w-3" />
                      {t('复制')}
                    </button>
                  </div>
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <p className={mutedClass}>{t('分享链接')}</p>
                      <p className="mt-0.5 break-all font-mono text-xs text-black dark:text-white">{linkShareUrl(link)}</p>
                    </div>
                    <button type="button" onClick={() => { void copyText(linkShareUrl(link)) }} className={copyBtnClass}>
                      <Copy className="mr-1 inline h-3 w-3" />
                      {t('复制')}
                    </button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      {/* 访问码口令重置结果：一次性展示新口令，并提示重新登录 */}
      {rotatedLink && (
        <section className={`${cardClass} p-5`}>
          <h2 className={labelClass}>
            <KeyRound className="h-4 w-4 text-gray-400" />
            {t('新访问码口令（仅此一次显示）')}
          </h2>
          <p className={`mt-1.5 ${mutedClass}`}>
            {t('机器')}：{rotatedLink.container_name || rotatedLink.container_uuid}
          </p>
          <div className="mt-3 flex items-center gap-2">
            <code className="min-w-0 flex-1 break-all rounded bg-amber-50 px-2 py-1.5 font-mono text-sm text-black dark:bg-amber-900/30 dark:text-white">{rotatedLink.access_code_password}</code>
            <button
              type="button"
              onClick={() => { void copyText(rotatedLink.access_code_password) }}
              className="shrink-0 rounded-md border border-amber-300 bg-white px-2.5 py-1.5 text-xs text-amber-700 hover:bg-amber-100 dark:border-amber-800 dark:bg-gray-900 dark:text-amber-300 dark:hover:bg-amber-900/50"
            >
              <Copy className="mr-1 inline h-3 w-3" />
              {t('复制')}
            </button>
          </div>
          <div className="mt-3 flex items-center justify-between gap-3 rounded-xl bg-gray-50 px-3.5 py-3 dark:bg-gray-800">
            <p className={mutedClass}>{t('旧口令已失效，请使用新口令重新登录。')}</p>
            <button
              type="button"
              onClick={() => { logout() }}
              className="inline-flex shrink-0 items-center gap-1.5 rounded-md bg-brand-600 px-3 py-2 text-xs font-medium text-white hover:bg-brand-700 dark:bg-brand-500 dark:hover:bg-brand-400"
            >
              <LogOut className="h-3.5 w-3.5" />
              {t('重新登录')}
            </button>
          </div>
        </section>
      )}

      {/* 账号密码：仅账号登录会话可改（访问码会话被禁止） */}
      <section className={`${cardClass} p-5`}>
        <h2 className={labelClass}>
          <KeyRound className="h-4 w-4 text-gray-400" />
          {t('账号密码')}
        </h2>
        <p className={`mt-1.5 ${mutedClass}`}>
          {t('出于安全考虑，账号密码只能由系统随机生成（16 位），不支持自定义密码。')}
        </p>

        {isAccessCodeSession ? (
          <div className="mt-4 rounded-xl border border-gray-200 bg-gray-50 px-3.5 py-3 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
            {t('当前为访问码登录会话，不能修改账号密码。如需修改，请使用「用户名 + 账号密码」登录后再操作。')}
          </div>
        ) : newPassword ? (
          <div className="mt-4 space-y-3">
            <div className="rounded-xl border border-amber-200 bg-amber-50 px-3.5 py-3 dark:border-amber-900/60 dark:bg-amber-900/30">
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
            <div className="flex items-center justify-between gap-3 rounded-xl bg-gray-50 px-3.5 py-3 dark:bg-gray-800">
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
                className="w-full rounded-xl border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-black outline-none focus:border-brand-500 focus:bg-white dark:border-gray-700 dark:bg-gray-800 dark:text-white dark:focus:bg-gray-900"
                placeholder={t('输入当前密码')}
              />
            </div>
            <button
              type="submit"
              disabled={rotating || !oldPassword}
              className="inline-flex shrink-0 items-center gap-1.5 rounded-xl bg-brand-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-brand-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-brand-500 dark:hover:bg-brand-400"
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
