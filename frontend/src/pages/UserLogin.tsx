import { FormEvent, useState } from 'react'
import { KeyRound, Lock, User } from 'lucide-react'
import { Link } from 'react-router'
import AppIcon from '../components/AppIcon'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'

// UserLogin 是**用户入口**（/user/login）：账号密码登录，或使用管理员发放的访问码。
// 管理员入口在 /login。
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

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    setLoading(true)
    try {
      if (mode === 'code') {
        await accessCodeLogin(code, password)
      } else {
        await userLogin(username, password)
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
    } finally {
      setLoading(false)
    }
  }

  const switchMode = (next: 'account' | 'code') => {
    setMode(next)
    setError('')
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 px-4 dark:bg-gray-950">
      <div className="w-full max-w-md">
        <div className="rounded-lg border border-gray-200 bg-white p-8 shadow-sm dark:border-gray-800 dark:bg-gray-900">
          <div className="mb-8 flex flex-col items-center">
            <div className="mb-4 flex h-16 w-16 items-center justify-center">
              <AppIcon className="h-10 w-10" />
            </div>
            <h1 className="text-2xl font-bold text-brand-600">EyvesCloud</h1>
            <p className="mt-1 text-sm text-gray-500">{t('用户中心登录')}</p>
          </div>

          <div className="mb-5 grid grid-cols-2 gap-2 rounded-md bg-gray-100 p-1 text-sm dark:bg-gray-800">
            <button
              type="button"
              onClick={() => switchMode('account')}
              className={`rounded px-3 py-1.5 font-medium transition-colors ${mode === 'account' ? 'bg-white text-black shadow-sm dark:bg-gray-700 dark:text-white' : 'text-gray-500'}`}
            >
              {t('账号登录')}
            </button>
            <button
              type="button"
              onClick={() => switchMode('code')}
              className={`rounded px-3 py-1.5 font-medium transition-colors ${mode === 'code' ? 'bg-white text-black shadow-sm dark:bg-gray-700 dark:text-white' : 'text-gray-500'}`}
            >
              {t('访问码登录')}
            </button>
          </div>

          <form onSubmit={handleSubmit} className="space-y-5">
            {error && (
              <div className="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
            )}

            {mode === 'account' ? (
              <div>
                <label className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">{t('用户名')}</label>
                <div className="relative">
                  <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                    <User className="h-4 w-4 text-gray-400" />
                  </div>
                  <input
                    type="text"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    className="block w-full rounded-md border border-gray-300 bg-white py-2.5 pl-10 pr-3 text-sm text-black placeholder-gray-400 focus:border-black focus:outline-none focus:ring-2 focus:ring-black dark:border-gray-700 dark:bg-gray-800 dark:text-white"
                    placeholder={t('输入用户名')}
                    required
                    autoComplete="username"
                  />
                </div>
              </div>
            ) : (
              <div>
                <label className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">{t('访问码')}</label>
                <div className="relative">
                  <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                    <KeyRound className="h-4 w-4 text-gray-400" />
                  </div>
                  <input
                    type="text"
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    disabled={!!urlCode}
                    className="block w-full rounded-md border border-gray-300 bg-white py-2.5 pl-10 pr-3 text-sm text-black placeholder-gray-400 focus:border-black focus:outline-none focus:ring-2 focus:ring-black disabled:bg-gray-100 dark:border-gray-700 dark:bg-gray-800 dark:text-white"
                    placeholder={t('输入访问码')}
                    required
                    autoComplete="off"
                  />
                </div>
                <p className="mt-1.5 text-xs text-gray-400">{t('访问码由管理员在「管理链接」中提供')}</p>
              </div>
            )}

            <div>
              <label className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">{t('密码')}</label>
              <div className="relative">
                <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3">
                  <Lock className="h-4 w-4 text-gray-400" />
                </div>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  className="block w-full rounded-md border border-gray-300 bg-white py-2.5 pl-10 pr-3 text-sm text-black placeholder-gray-400 focus:border-black focus:outline-none focus:ring-2 focus:ring-black dark:border-gray-700 dark:bg-gray-800 dark:text-white"
                  placeholder={t('输入密码')}
                  required
                  autoComplete="current-password"
                />
              </div>
            </div>

            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-md bg-black py-2.5 text-sm font-medium text-white transition-colors hover:bg-gray-800 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {loading ? t('登录中...') : t('登录')}
            </button>

            <Link to="/login" className="block w-full text-center text-xs text-gray-500 underline hover:text-black">
              {t('我是管理员，前往管理入口')}
            </Link>
          </form>
        </div>

        <p className="mt-6 text-center text-xs text-gray-400">EyvesCloud v1.2.0</p>
      </div>
    </div>
  )
}
