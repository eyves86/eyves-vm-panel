import { Link, Outlet, useNavigate } from 'react-router'
import { LogOut, Server } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import AppIcon from './AppIcon'

// UserLayout 是**用户门户**（/user/*）的极简外壳：只有品牌、用户信息与登出，
// 不暴露任何管理端导航（管理端在 /）。
export default function UserLayout() {
  const { username, logout } = useAuth()
  const { t } = useLanguage()
  const navigate = useNavigate()

  return (
    <div className="min-h-screen bg-gray-50 dark:bg-gray-950">
      <header className="sticky top-0 z-20 border-b border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-4">
          <Link to="/user" className="flex items-center gap-2">
            <AppIcon className="h-6 w-6" />
            <span className="text-base font-semibold text-brand-600">EyvesCloud</span>
            <span className="ml-1 rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-500 dark:bg-gray-800 dark:text-gray-400">
              {t('用户中心')}
            </span>
          </Link>
          <div className="flex items-center gap-3">
            <span className="hidden text-sm text-gray-600 sm:inline dark:text-gray-300">{username}</span>
            <button
              type="button"
              onClick={() => { logout() }}
              className="inline-flex items-center gap-1.5 rounded-md border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            >
              <LogOut className="h-3.5 w-3.5" />
              {t('退出登录')}
            </button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-6">
        <Outlet />
      </main>

      <footer className="mx-auto max-w-6xl px-4 pb-8 pt-2 text-center text-xs text-gray-400">
        <button
          type="button"
          className="inline-flex items-center gap-1 underline hover:text-gray-600"
          onClick={() => navigate('/login')}
        >
          <Server className="h-3 w-3" />
          {t('管理员入口')}
        </button>
      </footer>
    </div>
  )
}
