import { useEffect, useState } from 'react'
import { Outlet, useLocation } from 'react-router'
import { Menu } from 'lucide-react'
import Sidebar from './Sidebar'
import AutoTranslate from './AutoTranslate'
import BrowserDialogTranslator from './BrowserDialogTranslator'

export default function Layout() {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const location = useLocation()

  // 移动端路由切换后自动收起抽屉
  useEffect(() => {
    setMobileOpen(false)
  }, [location.pathname])

  return (
    <div className="min-h-screen bg-gray-50 dark:bg-gray-950">
      <AutoTranslate />
      <BrowserDialogTranslator />
      <Sidebar
        collapsed={sidebarCollapsed}
        onToggle={() => setSidebarCollapsed(!sidebarCollapsed)}
        mobileOpen={mobileOpen}
      />
      {mobileOpen && (
        <div
          className="fixed inset-0 z-20 bg-black/50 md:hidden"
          onClick={() => setMobileOpen(false)}
          aria-hidden="true"
        />
      )}
      <main
        className={`min-h-screen min-w-0 flex-1 transition-all duration-300 ${
          sidebarCollapsed ? 'md:ml-16' : 'md:ml-60'
        }`}
      >
        {/* 移动端顶部栏：汉堡按钮 */}
        <div className="sticky top-0 z-10 flex items-center gap-3 border-b border-gray-200 bg-gray-50/95 px-4 py-3 backdrop-blur dark:border-gray-700 dark:bg-gray-950/95 md:hidden">
          <button
            onClick={() => setMobileOpen(true)}
            className="inline-flex h-9 w-9 items-center justify-center rounded-md border border-gray-200 text-gray-600 dark:border-gray-700 dark:text-gray-300"
            aria-label="打开菜单"
          >
            <Menu className="h-5 w-5" />
          </button>
          <span className="text-sm font-bold text-black dark:text-white">EyvesCloud</span>
        </div>
        <div className="p-4 md:p-6">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
