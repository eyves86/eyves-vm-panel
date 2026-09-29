import { FormEvent, useEffect, useState } from 'react'
import { KeyRound, Lock, User } from 'lucide-react'
import AuthLayout from '../components/AuthLayout'
import TurnstileWidget from '../components/TurnstileWidget'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { getTurnstileConfig } from '../services/api'

// UserLogin 是**用户入口**：账号密码登录，或使用管理员发放的访问码。
// 管理员入口使用随机化路径，不在本页暴露。
// 布局采用参考站的分屏门面：左表单 + 右品牌面板（见 AuthLayout）。
export default function UserLogin() {
  const { userLogin, accessCodeLogin } = useAuth()
  const { t } = useLanguage()

  const urlCode = new URLSearchParams(window.location.search).get('code') || ''
  const [mode, setMode] = useState<'account' | 'code'>(urlCode ? 'code' : 'account')
  const [username, setUsername] = useState('')
  const [code, setCode] = useState(urlCode)
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  // Cloudflare Turnstile 人机验证：账号登录与访问码登录共用一个 widget。
  const [turnstileSiteKey, setTurnstileSiteKey] = useState('')
  const [turnstileToken, setTurnstileToken] = useState('')
  const [turnstileResetKey, setTurnstileResetKey] = useState(0)

  useEffect(() => {
    let cancelled = false
    getTurnstileConfig()
      .then((res) => {
        if (cancelled || !res.data.data?.user_enabled) return
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
      if (mode === 'code') {
        await accessCodeLogin(code, password, turnstileToken)
      } else {
        await userLogin(username, password, turnstileToken)
      }
    } catch (err: unknown) {
      const e = err as { response?: { status?: number; data?: { message?: string } } }
      const status = e.response?.status
      if (status === 401) {
        setError(t(mode === 'code' ? '访问码或密码错误' : '用户名或密码错误'))
      } else if (status === 403) {
        setError(e.response?.data?.message || t('该账号暂无可用服务器，请联系管理员'))
      } else {
        setError(e.response?.data?.message || t('登录失败，请稍后重试'))
      }
      // token 已被 siteverify 一次性消费，无论成败都重置 widget 取新 token。
      if (turnstileSiteKey) resetTurnstile()
    } finally {
      setLoading(false)
    }
  }

  const switchMode = (next: 'account' | 'code') => {
    setMode(next)
    setError('')
  }

  const inputClass =
    'block w-full rounded-xl border border-gray-200 bg-gray-50 py-2.5 pl-10 pr-3 text-sm text-black placeholder-gray-400 transition-colors focus:border-brand-500 focus:bg-white focus:outline-none focus:ring-2 focus:ring-brand-500/30 dark:border-gray-700 dark:bg-gray-800 dark:text-white dark:focus:bg-gray-900'

  return (
    <AuthLayout
      greeting="欢迎加入 EyvesCloud"
      title="用户中心登录"
      subtitle="使用账号或管理员发放的访问码，进入你的云服务器。"
      slogan="你的云端旅程，从这里开始。"
      sloganSub="轻量、稳定的容器化云服务器，按需创建，随时扩展。"
    >
      {/* 登录方式切换：下划线 tab（参考站样式） */}
      <div className="mb-6 flex gap-6 border-b border-gray-200 dark:border-gray-700">
        {(['account', 'code'] as const).map((m) => (
          <button
            key={m}
            type="button"
            onClick={() => switchMode(m)}
            className={`-mb-px border-b-2 pb-2.5 text-sm transition-colors ${
              mode === m
                ? 'border-brand-600 font-medium text-gray-900 dark:border-brand-400 dark:text-white'
                : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'
            }`}
          >
            {t(m === 'account' ? '账号登录' : '访问码登录')}
          </button>
        ))}
      </div>

      <form onSubmit={handleSubmit} className="space-y-5">
        {error && (
          <div className="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/30 dark:text-red-400">
            {error}
          </div>
        )}

        {mode === 'account' ? (
          <div>
            <label className="mb-1.5 block text-[13px] font-medium text-gray-900 dark:text-gray-200">
              {t('用户名')}
            </label>
            <div className="relative">
              <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                <User className="h-4 w-4 text-gray-400" />
              </div>
              <input
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className={inputClass}
                placeholder={t('输入用户名')}
                required
                autoComplete="username"
              />
            </div>
          </div>
        ) : (
          <div>
            <label className="mb-1.5 block text-[13px] font-medium text-gray-900 dark:text-gray-200">
              {t('访问码')}
            </label>
            <div className="relative">
              <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                <KeyRound className="h-4 w-4 text-gray-400" />
              </div>
              <input
                type="text"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                disabled={!!urlCode}
                className={`${inputClass} disabled:bg-gray-100 dark:disabled:bg-gray-800/60`}
                placeholder={t('输入访问码')}
                required
                autoComplete="off"
              />
            </div>
            <p className="mt-1.5 text-xs text-gray-400">{t('访问码由管理员在「管理链接」中提供')}</p>
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
              onChange={(e) => setPassword(e.target.value)}
              className={inputClass}
              placeholder={t('输入密码')}
              required
              autoComplete="current-password"
            />
          </div>
        </div>

        {turnstileSiteKey && (
          <TurnstileWidget siteKey={turnstileSiteKey} onToken={setTurnstileToken} resetKey={turnstileResetKey} />
        )}

        <button
          type="submit"
          disabled={loading}
          className="w-full rounded-xl bg-brand-600 py-2.5 text-sm font-medium text-white transition-all hover:bg-brand-700 focus:outline-none focus:ring-2 focus:ring-brand-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-gray-950"
        >
          {loading ? t('登录中...') : t('登录')}
        </button>
      </form>
    </AuthLayout>
  )
}
