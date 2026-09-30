import { Link, NavLink, Outlet } from 'react-router'
import { HardDrive, LogOut, ShieldCheck } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { useBrand } from '../utils/brand'
import { useLanguage } from '../contexts/LanguageContext'
import AppIcon from './AppIcon'

// UserLayout 是**用户门户**（/user/*）的极简外壳：品牌、门户导航（我的服务器 /
// 安全设置）、用户信息与登出，不暴露任何管理端导航（管理端在可自定义的管理员路径下）。
export default function UserLayout() {
  const { username, logout } = useAuth()
  const { t } = useLanguage()

  const navLinkClass = ({ isActive }: { isActive: boolean }) =>
    `inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm transition-colors ${
      isActive
        ? 'bg-brand-50 font-medium text-brand-600 dark:bg-brand-900/30 dark:text-brand-400'
        : 'text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200'
    }`

  return (
    <div className="min-h-screen bg-gray-50 dark:bg-gray-950">
      <header className="sticky top-0 z-20 border-b border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900">
        <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-4">
          <div className="flex min-w-0 items-center gap-4">
            <Link to="/user" className="flex shrink-0 items-center gap-2">
              <AppIcon className="h-6 w-6" />
              <span className="text-base font-semibold text-brand-600">{useBrand().name}</span>
              <span className="ml-1 rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-500 dark:bg-gray-800 dark:text-gray-400">
                {t('用户中心')}
              </span>
            </Link>
            <nav className="flex items-center gap-1">
              <NavLink to="/user" end className={navLinkClass}>
                <HardDrive className="h-3.5 w-3.5" />
                {t('我的服务器')}
              </NavLink>
              <NavLink to="/user/security" className={navLinkClass}>
                <ShieldCheck className="h-3.5 w-3.5" />
                {t('安全设置')}
              </NavLink>
            </nav>
          </div>
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
        {/* 不在此处暴露管理入口链接：管理员路径是自定义的，且不应被枚举。 */}
        <span>{useBrand().name}</span>
      </footer>
    </div>
  )
}
