import { FormEvent, useEffect, useState } from 'react'
import { Lock, Smartphone, User } from 'lucide-react'
import AuthLayout from '../components/AuthLayout'
import TurnstileWidget from '../components/TurnstileWidget'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { getTurnstileConfig } from '../services/api'
import AutoTranslate from '../components/AutoTranslate'
import BrowserDialogTranslator from '../components/BrowserDialogTranslator'

// Login 是**管理员入口**。用户入口在 /user/login。
// 布局采用参考站的分屏门面：左表单 + 右品牌面板（见 AuthLayout）。
export default function Login() {
  const { adminLogin, adminLoginWith2FA } = useAuth()
  const { t } = useLanguage()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [twoFACode, setTwoFACode] = useState('')
  const [twoFARequired, setTwoFARequired] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  // Cloudflare Turnstile 人机验证：后端启用后登录必须先通过验证。
  const [turnstileSiteKey, setTurnstileSiteKey] = useState('')
  const [turnstileToken, setTurnstileToken] = useState('')
  const [turnstileResetKey, setTurnstileResetKey] = useState(0)

  useEffect(() => {
    let cancelled = false
    getTurnstileConfig()
      .then((res) => {
        if (cancelled || !res.data.data?.admin_enabled) return
        setTurnstileSiteKey(res.data.data.site_key)
      })
      .catch(() => {
        /* 配置接口失败时按未启用处理，交由后端二次校验兜底 */
      })
    return () => {
      cancelled = true
    }
  }, [])

  const resetTurnstile = () => {
    setTurnstileToken('')
    setTurnstileResetKey((key) => key + 1)
  }

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')

    // 启用了 Turnstile 时先本地拦截空 token，避免浪费一次密码尝试/限流计数。
    if (turnstileSiteKey && !turnstileToken) {
      setError(t('请先完成人机验证'))
      return
    }

    setLoading(true)

    try {
      if (twoFARequired) {
        await adminLoginWith2FA(username, password, twoFACode, turnstileToken)
      } else {
        await adminLogin(username, password, turnstileToken)
      }
    } catch (err: unknown) {
      const error = err as {
        response?: {
          status?: number
          data?: { message?: string; data?: { twofa_required?: boolean } }
        }
      }
      const data = error.response?.data
      if (error.response?.status === 401 && data?.data?.twofa_required) {
        setTwoFARequired(true)
        setTwoFACode('')
        setError(t('账户已启用两步验证，请输入 6 位动态口令'))
      } else if (error.response?.status === 401) {
        setError(t('用户名或密码或验证码错误'))
      } else {
        setError(data?.message || t('登录失败，请检查用户名和密码'))
      }
      // token 已被 siteverify 一次性消费，无论成败都重置 widget 取新 token。
      if (turnstileSiteKey) resetTurnstile()
    } finally {
      setLoading(false)
    }
  }

  const handleBackToCredentials = () => {
    setTwoFARequired(false)
    setTwoFACode('')
    setError('')
  }

  const inputClass =
    'block w-full rounded-xl border border-gray-200 bg-gray-50 py-2.5 pl-10 pr-3 text-sm text-black placeholder-gray-400 transition-colors focus:border-brand-500 focus:bg-white focus:outline-none focus:ring-2 focus:ring-brand-500/30 dark:border-gray-700 dark:bg-gray-800 dark:text-white dark:focus:bg-gray-900'

  return (
    <AuthLayout
      greeting="很高兴，再次见到你"
      title="欢迎回来"
      subtitle="登录管理员控制台，管理你的云基础设施。"
      slogan="从这里连接，向更多可能出发。"
      sloganSub="容器、节点、网络与安全策略，统一在一块面板里从容调度。"
    >
      <AutoTranslate />
      <BrowserDialogTranslator />

      <form onSubmit={handleSubmit} className="space-y-5">
        {error && (
          <div className="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/30 dark:text-red-400">
            {error}
          </div>
        )}

        {!twoFARequired && (
          <div>
            <label className="mb-1.5 block text-[13px] font-medium text-gray-900 dark:text-gray-200">
              {t('管理员用户名')}
            </label>
            <div className="relative">
              <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                <User className="h-4 w-4 text-gray-400" />
              </div>
              <input
                type="text"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                className={inputClass}
                placeholder={t('输入管理员用户名')}
                required
                autoComplete="username"
              />
            </div>
          </div>
        )}

        {twoFARequired && (
          <div className="rounded-xl border border-green-200 bg-green-50 p-3 text-xs text-green-800 dark:border-green-900/60 dark:bg-green-900/30 dark:text-green-400">
            <div className="flex items-center gap-1.5 font-medium">
              <Smartphone className="h-3.5 w-3.5" />{t('两步验证')}
            </div>
            <div className="mt-1">{t('请输入身份验证器中的 6 位动态口令，或一次性备份码')}</div>
          </div>
        )}

        <div>
          <label className="mb-1.5 block text-[13px] font-medium text-gray-900 dark:text-gray-200">
            {t('密码')}
          </label>
          <div className="relative">
            <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
              <Lock className="h-4 w-4 text-gray-400" />
            </div>
            <input
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              className={inputClass}
              placeholder={t('输入密码')}
              required
              autoComplete="current-password"
            />
          </div>
        </div>

        {twoFARequired && (
          <div>
            <label className="mb-1.5 block text-[13px] font-medium text-gray-900 dark:text-gray-200">
              {t('动态口令 / 备份码')}
            </label>
            <div className="relative">
              <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                <Smartphone className="h-4 w-4 text-gray-400" />
              </div>
              <input
                type="text"
                value={twoFACode}
                onChange={(event) => setTwoFACode(event.target.value.replace(/\s+/g, ''))}
                className={`${inputClass} tracking-widest`}
                placeholder={t('6 位动态口令或备份码')}
                required
                maxLength={32}
                autoComplete="one-time-code"
              />
            </div>
            <button
              type="button"
              onClick={handleBackToCredentials}
              className="mt-1.5 text-xs text-gray-500 underline transition-colors hover:text-black dark:hover:text-white"
            >
              {t('返回重新输入密码')}
            </button>
          </div>
        )}

        {turnstileSiteKey && (
          <TurnstileWidget siteKey={turnstileSiteKey} onToken={setTurnstileToken} resetKey={turnstileResetKey} />
        )}

        <button
          type="submit"
          disabled={loading}
          className="w-full rounded-xl bg-brand-600 py-2.5 text-sm font-medium text-white transition-all hover:bg-brand-700 focus:outline-none focus:ring-2 focus:ring-brand-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-gray-950"
        >
          {loading ? t('登录中...') : t('登录管理员控制台')}
        </button>
      </form>
    </AuthLayout>
  )
}
