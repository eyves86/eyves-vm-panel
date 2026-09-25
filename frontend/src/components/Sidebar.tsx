import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import {
  ChevronLeft,
  ChevronRight,
  Code2,
  Cpu,
  Camera,
  Building2,
  Globe,
  HardDrive,
  LayoutDashboard,
  LogOut,
  Moon,
  Network,
  Package,
  Route,
  ScrollText,
  Server,
  ShieldAlert,
  SlidersHorizontal,
  Sun,
  UserCog,
  MoveRight,
  Layers,
  Disc3,
  Activity,
  X,
} from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { useTheme } from '../contexts/ThemeContext'
import { checkUpdate, getVersion, listUpdateReleases, updatePanel, type UpdateRelease } from '../services/api'
import AppIcon from './AppIcon'

interface SidebarProps {
  collapsed: boolean
  onToggle: () => void
  mobileOpen: boolean
  onMobileClose: () => void
}

function GitHubIcon({ className = '' }: { className?: string }) {
  return (
    <svg
      className={className}
      viewBox="0 0 1024 1024"
      version="1.1"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
    >
      <path
        d="M512 42.666667A464.64 464.64 0 0 0 42.666667 502.186667 460.373333 460.373333 0 0 0 363.52 938.666667c23.466667 4.266667 32-9.813333 32-22.186667v-78.08c-130.56 27.733333-158.293333-61.44-158.293333-61.44a122.026667 122.026667 0 0 0-52.053334-67.413333c-42.666667-28.16 3.413333-27.733333 3.413334-27.733334a98.56 98.56 0 0 1 71.68 47.36 101.12 101.12 0 0 0 136.533333 37.973334 99.413333 99.413333 0 0 1 29.866667-61.44c-104.106667-11.52-213.333333-50.773333-213.333334-226.986667a177.066667 177.066667 0 0 1 47.36-124.16 161.28 161.28 0 0 1 4.693334-121.173333s39.68-12.373333 128 46.933333a455.68 455.68 0 0 1 234.666666 0c89.6-59.306667 128-46.933333 128-46.933333a161.28 161.28 0 0 1 4.693334 121.173333A177.066667 177.066667 0 0 1 810.666667 477.866667c0 176.64-110.08 215.466667-213.333334 226.986666a106.666667 106.666667 0 0 1 32 85.333334v125.866666c0 14.933333 8.533333 26.88 32 22.186667A460.8 460.8 0 0 0 981.333333 502.186667 464.64 464.64 0 0 0 512 42.666667"
        fill="currentColor"
      />
    </svg>
  )
}

function LanguageIcon({ className = '' }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 1024 1024" version="1.1" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
      <path
        d="M128 170.6496A42.6496 42.6496 0 0 0 128 256V170.6496zM640 256a42.6496 42.6496 0 1 0 0-85.3504V256zM426.6496 128a42.6496 42.6496 0 0 0-85.2992 0h85.2992zM341.3504 213.3504a42.6496 42.6496 0 0 0 85.2992 0H341.3504z m56.6784 434.944a42.6496 42.6496 0 0 0 61.44-59.2896l-61.44 59.2896zM312.8832 367.4112a42.6496 42.6496 0 0 0-78.592 33.1776l78.592-33.1776z m220.4672 357.888a42.6496 42.6496 0 1 0 0 85.3504v-85.2992z m298.6496 85.3504a42.6496 42.6496 0 1 0 0-85.2992v85.2992z m-400.8448 66.2528a42.6496 42.6496 0 1 0 76.3392 38.1952l-76.288-38.1952z m251.4944-407.552l38.1952-19.0976a42.6496 42.6496 0 0 0-76.3392 0l38.144 19.0976z m175.2064 445.7472a42.6496 42.6496 0 1 0 76.288-38.1952l-76.288 38.1952zM586.1376 220.3648a42.6496 42.6496 0 1 0-84.1728-14.08l84.1728 14.08zM109.0048 735.2832a42.6496 42.6496 0 0 0 37.9904 76.4416l-37.9904-76.4416zM128 256h512V170.6496h-512V256z m213.3504-128v85.3504h85.2992V128H341.3504z m118.0672 461.0048a726.3232 726.3232 0 0 1-146.5344-221.5936l-78.592 33.1776a811.6224 811.6224 0 0 0 163.7376 247.7056l61.44-59.2896z m73.9328 221.696h298.6496v-85.3504h-298.6496v85.2992z m-25.856 104.3968l213.3504-426.7008-76.3392-38.144-213.3504 426.6496 76.3392 38.1952z m137.0112-426.7008l213.3504 426.7008 76.288-38.1952-213.2992-426.6496-76.3392 38.144zM501.9648 206.336C463.0016 438.6304 313.3952 633.7536 109.056 735.232l37.9904 76.4416c228.2496-113.4592 395.52-331.3152 439.1424-591.36L501.9648 206.336z"
        fill="currentColor"
      />
    </svg>
  )
}

export default function Sidebar({ collapsed, onToggle, mobileOpen, onMobileClose }: SidebarProps) {
  const navigate = useNavigate()
  const location = useLocation()
  const { logout, isSubUser } = useAuth()
  const { theme, toggleTheme } = useTheme()
  const { toggleLanguage, t } = useLanguage()
  const [version, setVersion] = useState('')
  const [hasUpdate, setHasUpdate] = useState(false)
  const [latestVersion, setLatestVersion] = useState('')
  const [upgrading, setUpgrading] = useState(false)
  const [upgradeMsg, setUpgradeMsg] = useState('')
  // 升级会下载→备份→就地替换→重启面板（期间连接会断开），属于不可逆的破坏性操作，
  // 必须二次确认后才触发，避免误点。
  const [confirmUpdate, setConfirmUpdate] = useState(false)

  // 面板更新管理器：选择仓库（默认官方）与目标版本（默认最新），二次确认后升级。
  const DEFAULT_REPO = 'FenhaoLost/eyves-vm-panel'
  const [updateModalOpen, setUpdateModalOpen] = useState(false)
  const [repoInput, setRepoInput] = useState(DEFAULT_REPO)
  const [releases, setReleases] = useState<UpdateRelease[]>([])
  const [releasesLoading, setReleasesLoading] = useState(false)
  const [releasesErr, setReleasesErr] = useState('')
  const [selectedTag, setSelectedTag] = useState('')

  useEffect(() => {
    getVersion()
      .then(res => {
        if (res.data?.data?.version) {
          setVersion(res.data.data.version)
        }
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    // 版本更新检测（管理员登录态才有该端点；子用户静默跳过）。
    if (isSubUser) return
    checkUpdate()
      .then(res => {
        const d = res.data?.data
        if (d && d.has_update) {
          setHasUpdate(true)
          setLatestVersion(d.latest || '')
        }
      })
      .catch(() => {})
  }, [isSubUser])

  // 打开更新管理器：重置为默认仓库并拉取版本列表（默认选中最新版）。
  const openUpdateManager = () => {
    if (isSubUser) return
    setUpdateModalOpen(true)
    setRepoInput(DEFAULT_REPO)
    setSelectedTag('')
    setReleasesErr('')
    setUpgradeMsg('')
    void fetchReleases(DEFAULT_REPO)
  }

  // 拉取指定仓库的版本列表；默认选中第一个（最新）版本。
  const fetchReleases = async (repo: string) => {
    const trimmed = repo.trim()
    if (!trimmed) {
      setReleasesErr('请输入仓库标识')
      return
    }
    setReleasesLoading(true)
    setReleasesErr('')
    try {
      const res = await listUpdateReleases(trimmed)
      const list = res.data?.data?.releases || []
      setReleases(list)
      const first = list.find(r => r.has_asset) || list[0]
      setSelectedTag(first?.tag_name || '')
    } catch (err: unknown) {
      const data = (err as { response?: { data?: { message?: string } } })?.response?.data
      setReleasesErr(data?.message || '获取版本列表失败')
      setReleases([])
    } finally {
      setReleasesLoading(false)
    }
  }

  // 面板内直接升级：确认后触发后端升级（下载→解压→备份→替换→重启）。
  // 重复点击由后端以 409 拒绝。
  const handleUpdate = async () => {
    if (upgrading) return
    setConfirmUpdate(false)
    setUpgrading(true)
    setUpgradeMsg('')
    const isDefaultRepo = repoInput.trim() === DEFAULT_REPO
    try {
      const res = await updatePanel({
        repo: isDefaultRepo ? undefined : repoInput.trim(),
        tag: selectedTag || undefined,
      })
      const msg = res.data?.data?.message || res.data?.message || '升级已开始'
      setUpgradeMsg(msg)
    } catch (err: unknown) {
      const data = (err as { response?: { data?: { message?: string } } })?.response?.data
      setUpgradeMsg(data?.message || '升级触发失败')
    } finally {
      // 升级会重启服务，连接会断开；这里保留信息提示，等刷新后重新校验版本。
      setTimeout(() => setUpgrading(false), 4000)
    }
  }

  const isContainerPage =
    location.pathname.startsWith('/containers') ||
    location.pathname.startsWith('/container')

  const isNodesPage = location.pathname.startsWith('/nodes')

  const isMonitoringPage = location.pathname.startsWith('/monitoring')

  const isImagesPage = location.pathname.startsWith('/images')

  const isSnapshotsPage = location.pathname.startsWith('/snapshots')
  const isRoutingPage = location.pathname.startsWith('/routing')
  const isStoragePage = location.pathname.startsWith('/storage')
  const isAuditLogsPage = location.pathname.startsWith('/audit-logs')
  const isApiIntegrationPage = location.pathname.startsWith('/api-integration')
  const isHostReportPage = location.pathname.startsWith('/host-report')
  const isSecurityPage = location.pathname.startsWith('/security')
  const isSettingsPage = location.pathname.startsWith('/settings')
  const isMigrationPage = location.pathname.startsWith('/migration')
  const isPolicyPage = location.pathname.startsWith('/policies')

  // 展开态：桌面端跟随 collapsed；移动端抽屉打开时（mobileOpen）强制展开，
  // 否则左下角的语言/版本/有更新/登出等文字会被 collapsed 判据隐藏掉。
  const expanded = mobileOpen || !collapsed

  return (
    <>
    <aside
      className={`fixed left-0 top-0 z-30 flex h-dvh flex-col border-r border-gray-200 bg-white transition-all duration-300 dark:border-gray-700 dark:bg-gray-900 ${
        expanded ? 'w-60' : 'w-16'
      } ${
        // 移动端默认隐藏为抽屉，桌面端(md+)始终显示
        mobileOpen ? 'translate-x-0' : '-translate-x-full'
      } md:translate-x-0`}
      role={mobileOpen ? 'dialog' : undefined}
      aria-modal={mobileOpen ? 'true' : undefined}
      aria-label={mobileOpen ? '导航菜单' : undefined}
    >
      <div className="flex h-14 shrink-0 items-center justify-between border-b border-gray-200 px-4 dark:border-gray-700">
        {expanded && (
          <div className="flex items-center gap-2">
            <div className="w-7 h-7 flex items-center justify-center">
              <AppIcon className="w-5 h-5" />
            </div>
            <span className="font-bold text-black text-sm dark:text-white">EyvesCloud</span>
          </div>
        )}
        {!expanded && (
          <div className="w-7 h-7 flex items-center justify-center mx-auto">
            <AppIcon className="w-5 h-5" />
          </div>
        )}
        {/* 移动端抽屉：显示关闭按钮（折叠 chevron 仅桌面端有意义） */}
        <button
          onClick={onMobileClose}
          className="-mr-1 rounded p-1.5 text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800 md:hidden"
          title={t('关闭菜单')}
          aria-label={t('关闭菜单')}
        >
          <X className="h-4 w-4" />
        </button>
        <button
          onClick={onToggle}
          className="hidden p-1 rounded hover:bg-gray-100 text-gray-500 dark:hover:bg-gray-800 dark:text-gray-400 md:block"
          title={t('切换侧边栏')}
        >
          {collapsed ? (
            <ChevronRight className="w-4 h-4" />
          ) : (
            <ChevronLeft className="w-4 h-4" />
          )}
        </button>
      </div>

      <nav className="flex-1 overflow-y-auto overscroll-contain py-4 px-2 space-y-1">
        {!isSubUser && (
          <button
            onClick={() => navigate('/')}
            className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
              location.pathname === '/'
                ? 'bg-black text-white dark:bg-white dark:text-black'
                : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
            }`}
          >
            <LayoutDashboard className="w-4 h-4" />
            {expanded && <span>控制面板</span>}
          </button>
        )}

        <button
          onClick={() => navigate('/containers')}
          className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
            isContainerPage
              ? 'bg-black text-white dark:bg-white dark:text-black'
              : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
          }`}
        >
          <Server className="w-4 h-4" />
          {expanded && <span>容器管理</span>}
        </button>

        {!isSubUser && (
          <button
            onClick={() => navigate('/monitoring')}
            className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
              isMonitoringPage
                ? 'bg-black text-white dark:bg-white dark:text-black'
                : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
            }`}
          >
            <Activity className="w-4 h-4" />
            {expanded && <span>容器监控</span>}
          </button>
        )}

        {!isSubUser && (
          <button
            onClick={() => navigate('/nodes')}
            className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
              isNodesPage
                ? 'bg-black text-white dark:bg-white dark:text-black'
                : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
            }`}
          >
            <Network className="w-4 h-4" />
            {expanded && <span>节点管理</span>}
          </button>
        )}

        {!isSubUser && (
          <button
            onClick={() => navigate('/images')}
            className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
              isImagesPage
                ? 'bg-black text-white dark:bg-white dark:text-black'
                : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
            }`}
          >
            <Package className="w-4 h-4" />
            {expanded && <span>镜像管理</span>}
          </button>
        )}

        {!isSubUser && (
          <>
            <button
              onClick={() => navigate('/security')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isSecurityPage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <ShieldAlert className="w-4 h-4" />
              {expanded && <span>安全告警</span>}
            </button>

            <button
              onClick={() => navigate('/snapshots')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isSnapshotsPage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <Camera className="w-4 h-4" />
              {expanded && <span>快照管理</span>}
            </button>

            <button
              onClick={() => navigate('/routing')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isRoutingPage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <Route className="w-4 h-4" />
              {expanded && <span>路由管理</span>}
            </button>

            <button
              onClick={() => navigate('/migration')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isMigrationPage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <MoveRight className="w-4 h-4" />
              {expanded && <span>节点迁移</span>}
            </button>

            <button
              onClick={() => navigate('/policies')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isPolicyPage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <SlidersHorizontal className="w-4 h-4" />
              {expanded && <span>策略管理</span>}
            </button>

            <button
              onClick={() => navigate('/storage')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isStoragePage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <HardDrive className="w-4 h-4" />
              {expanded && <span>存储管理</span>}
            </button>

            <button
              onClick={() => navigate('/audit-logs')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isAuditLogsPage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <ScrollText className="w-4 h-4" />
              {expanded && <span>操作日志</span>}
            </button>

            <button
              onClick={() => navigate('/sub-users')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                location.pathname.startsWith('/sub-users')
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <UserCog className="w-4 h-4" />
              {expanded && <span>子用户管理</span>}
            </button>

            <button
              onClick={() => navigate('/admins')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                location.pathname.startsWith('/admins')
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <ShieldAlert className="w-4 h-4" />
              {expanded && <span>管理员账号</span>}
            </button>

            <button
              onClick={() => navigate('/tenants')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                location.pathname.startsWith('/tenants')
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <Building2 className="w-4 h-4" />
              {expanded && <span>多租户</span>}
            </button>

            <button
              onClick={() => navigate('/regions')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                location.pathname.startsWith('/regions')
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <Globe className="w-4 h-4" />
              {expanded && <span>区域管理</span>}
            </button>

            <button
              onClick={() => navigate('/ip-groups')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                location.pathname.startsWith('/ip-groups')
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <Layers className="w-4 h-4" />
              {expanded && <span>IP 组</span>}
            </button>

            <button
              onClick={() => navigate('/isos')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                location.pathname.startsWith('/isos')
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <Disc3 className="w-4 h-4" />
              {expanded && <span>ISO 镜像</span>}
            </button>

            <button
              onClick={() => navigate('/metric-retention')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                location.pathname.startsWith('/metric-retention')
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <Activity className="w-4 h-4" />
              {expanded && <span>指标留存</span>}
            </button>

            <button
              onClick={() => navigate('/api-integration')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isApiIntegrationPage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <Code2 className="w-4 h-4" />
              {expanded && <span>API 集成</span>}
            </button>

            <button
              onClick={() => navigate('/host-report')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isHostReportPage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <Cpu className="w-4 h-4" />
              {expanded && <span>宿主机信息</span>}
            </button>

            <button
              onClick={() => navigate('/settings')}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors ${
                isSettingsPage
                  ? 'bg-black text-white dark:bg-white dark:text-black'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              }`}
            >
              <UserCog className="w-4 h-4" />
              {expanded && <span>面板设置</span>}
            </button>
          </>
        )}
      </nav>

      <div className="shrink-0 border-t border-gray-200 dark:border-gray-700 p-2 space-y-1">
        {/* Theme Toggle */}
        <div className={expanded ? 'flex items-center gap-1' : 'space-y-1'}>
          <button
            onClick={toggleTheme}
            className={`${expanded ? 'flex-1' : 'w-full justify-center'} flex items-center gap-3 px-3 py-2.5 rounded-md text-sm text-gray-600 hover:bg-gray-100 transition-colors dark:text-gray-400 dark:hover:bg-gray-800`}
            title={t(theme === 'dark' ? '切换亮色模式' : '切换暗黑模式')}
          >
            {theme === 'dark' ? (
              <Sun className="w-4 h-4" />
            ) : (
              <Moon className="w-4 h-4" />
            )}
            {expanded && <span>{theme === 'dark' ? '亮色模式' : '暗黑模式'}</span>}
          </button>

          <button
            onClick={() => { void toggleLanguage() }}
            className={`${expanded ? 'flex-1 justify-center' : 'w-full justify-center'} flex items-center gap-2 rounded-md px-3 py-2.5 text-sm text-gray-600 hover:bg-gray-100 transition-colors dark:text-gray-400 dark:hover:bg-gray-800`}
            title="Language"
          >
            <LanguageIcon className="h-4 w-4 shrink-0" />
            {expanded && <span>Language</span>}
          </button>
        </div>

        {/* Version */}
        {version && (
          <div className={`px-3 py-2 text-xs text-gray-400 dark:text-gray-500 ${expanded ? '' : 'text-center'}`}>
            {expanded ? (
              <div className="flex min-w-0 items-center gap-2">
                <button
                  onClick={openUpdateManager}
                  disabled={upgrading}
                  title="面板更新（选择仓库与版本）"
                  className="inline-flex min-w-0 items-center gap-1 rounded text-gray-500 transition-colors hover:text-gray-950 dark:text-gray-400 dark:hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
                >
                  <GitHubIcon className="h-3.5 w-3.5 shrink-0" />
                  <span className="truncate">EyvesCloud</span>
                </button>
                <span className="shrink-0">v{version}</span>
                {hasUpdate && (
                  <button
                    onClick={openUpdateManager}
                    disabled={upgrading}
                    title={`有可用更新：${latestVersion}（点击选择升级版本）`}
                    className="shrink-0 inline-flex items-center gap-1 rounded bg-amber-100 px-1.5 py-0.5 font-medium text-amber-700 transition-colors hover:bg-amber-200 disabled:cursor-not-allowed disabled:opacity-60 dark:bg-amber-900/50 dark:text-amber-300 dark:hover:bg-amber-900/70"
                  >
                    <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
                    {upgrading ? t('升级中') : t('有更新')}
                  </button>
                )}
                {upgradeMsg && (
                  <span className="shrink-0 text-[11px] text-amber-600 dark:text-amber-400">{upgradeMsg}</span>
                )}
              </div>
            ) : (
              <button
                onClick={openUpdateManager}
                disabled={upgrading}
                title={`EyvesCloud v${version}（点击管理更新）`}
                className="inline-flex items-center justify-center rounded text-gray-400 transition-colors hover:text-gray-900 dark:text-gray-500 dark:hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
              >
                <GitHubIcon className="h-4 w-4" />
              </button>
            )}
          </div>
        )}

        {/* Logout */}
        <button
          onClick={logout}
          className="w-full flex items-center gap-3 px-3 py-2.5 rounded-md text-sm text-gray-600 hover:bg-gray-100 transition-colors dark:text-gray-400 dark:hover:bg-gray-800"
        >
          <LogOut className="w-4 h-4" />
          {expanded && <span>退出登录</span>}
        </button>
      </div>
    </aside>

    {/* 面板更新管理器：选择仓库 → 拉取版本列表 → 选择目标版本（默认最新） */}
    {updateModalOpen && (
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4">
        <div className="max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-lg bg-white p-5 shadow-xl dark:bg-gray-900">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold text-gray-900 dark:text-white">{t('面板更新')}</h3>
            <button
              type="button"
              onClick={() => setUpdateModalOpen(false)}
              className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
              aria-label={t('关闭')}
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          <div className="mt-3 space-y-3 text-xs text-gray-600 dark:text-gray-300">
            {/* 仓库选择 */}
            <div>
              <label className="mb-1 block font-medium text-gray-700 dark:text-gray-200">{t('更新仓库')}</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={repoInput}
                  onChange={e => setRepoInput(e.target.value)}
                  placeholder="owner/name"
                  className="flex-1 rounded-md border border-gray-200 px-2.5 py-1.5 text-xs outline-none focus:border-gray-400 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100"
                />
                <button
                  type="button"
                  onClick={() => { void fetchReleases(repoInput) }}
                  disabled={releasesLoading}
                  className="shrink-0 rounded-md border border-gray-200 px-3 py-1.5 font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                >
                  {releasesLoading ? t('获取中...') : t('获取版本列表')}
                </button>
              </div>
              <p className="mt-1 text-[11px] text-gray-400">
                {t('默认官方仓库')}：https://github.com/{DEFAULT_REPO}
              </p>
              {repoInput.trim() && repoInput.trim() !== DEFAULT_REPO && (
                <p className="mt-1 text-[11px] text-red-600 dark:text-red-400">
                  {t('警告：您正在使用第三方仓库，升级包将来自该仓库并直接替换面板二进制，请仅使用您信任的仓库。')}
                </p>
              )}
            </div>

            {/* 版本列表 */}
            <div>
              <label className="mb-1 block font-medium text-gray-700 dark:text-gray-200">{t('目标版本（默认最新）')}</label>
              {releasesErr && (
                <div className="rounded-md bg-red-50 px-3 py-2 text-[11px] text-red-600 dark:bg-red-900/30 dark:text-red-400">{releasesErr}</div>
              )}
              {!releasesErr && releases.length === 0 && !releasesLoading && (
                <div className="rounded-md bg-gray-50 px-3 py-2 text-[11px] text-gray-400 dark:bg-gray-800">{t('暂无版本，请点击上方按钮获取')}</div>
              )}
              <div className="max-h-56 space-y-1 overflow-y-auto">
                {releases.map(rel => (
                  <label
                    key={rel.tag_name}
                    className={`flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 transition-colors ${
                      selectedTag === rel.tag_name
                        ? 'border-amber-400 bg-amber-50 dark:border-amber-600 dark:bg-amber-900/20'
                        : 'border-gray-200 hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800'
                    } ${rel.has_asset ? '' : 'cursor-not-allowed opacity-50'}`}
                  >
                    <input
                      type="radio"
                      name="update-target-tag"
                      checked={selectedTag === rel.tag_name}
                      disabled={!rel.has_asset}
                      onChange={() => setSelectedTag(rel.tag_name)}
                      className="h-3.5 w-3.5"
                    />
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-1.5">
                        <span className="font-medium text-gray-900 dark:text-gray-100">{rel.tag_name}</span>
                        {rel.tag_name === version && (
                          <span className="rounded bg-gray-100 px-1 py-0.5 text-[10px] text-gray-500 dark:bg-gray-800 dark:text-gray-400">{t('当前')}</span>
                        )}
                        {rel.prerelease && (
                          <span className="rounded bg-purple-100 px-1 py-0.5 text-[10px] text-purple-600 dark:bg-purple-900/40 dark:text-purple-300">{t('预发布')}</span>
                        )}
                        {!rel.has_asset && (
                          <span className="rounded bg-gray-100 px-1 py-0.5 text-[10px] text-gray-400 dark:bg-gray-800">{t('无安装包')}</span>
                        )}
                      </span>
                      {rel.published_at && (
                        <span className="mt-0.5 block text-[10px] text-gray-400">{new Date(rel.published_at).toLocaleString()}</span>
                      )}
                    </span>
                    {rel.html_url && (
                      <a
                        href={rel.html_url}
                        target="_blank"
                        rel="noreferrer"
                        onClick={e => e.stopPropagation()}
                        className="shrink-0 text-[10px] text-gray-400 underline hover:text-gray-600 dark:hover:text-gray-200"
                      >
                        {t('发布说明')}
                      </a>
                    )}
                  </label>
                ))}
              </div>
            </div>

            {upgradeMsg && (
              <div className="rounded-md bg-amber-50 px-3 py-2 text-[11px] text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">{upgradeMsg}</div>
            )}
          </div>

          <div className="mt-5 flex justify-end gap-2">
            <button
              type="button"
              onClick={() => setUpdateModalOpen(false)}
              className="rounded-md border border-gray-200 px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            >
              {t('关闭')}
            </button>
            <button
              type="button"
              onClick={() => { if (selectedTag) setConfirmUpdate(true) }}
              disabled={!selectedTag || upgrading}
              className="rounded-md bg-amber-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-amber-700 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {upgrading ? t('升级中') : t('更新到此版本')}
            </button>
          </div>
        </div>
      </div>
    )}

    {/* 升级二次确认 */}
    {confirmUpdate && (
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4">
        <div className="w-full max-w-md rounded-lg bg-white p-5 shadow-xl dark:bg-gray-900">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-white">{t('确认升级面板？')}</h3>
          <div className="mt-3 space-y-2 text-xs text-gray-600 dark:text-gray-300">
            <div className="rounded-md bg-gray-50 px-3 py-2 dark:bg-gray-800">
              <div>{t('当前版本')}：v{version}</div>
              <div>{t('目标版本')}：v{selectedTag || '-'}</div>
              <div>{t('更新仓库')}：{repoInput.trim() || DEFAULT_REPO}</div>
            </div>
            <p>{t('升级将执行：下载新版本 → 解压 → 备份当前版本 → 就地替换 → 重启面板服务。')}</p>
            {repoInput.trim() && repoInput.trim() !== DEFAULT_REPO && (
              <p className="text-red-600 dark:text-red-400">
                {t('目标来自第三方仓库，请确认您信任该来源。')}
              </p>
            )}
            <p className="text-amber-700 dark:text-amber-400">
              {t('升级期间面板会短暂断开，正在运行的任务可能中断。请确认已完成必要备份后再继续。')}
            </p>
          </div>
          <div className="mt-5 flex justify-end gap-2">
            <button
              type="button"
              onClick={() => setConfirmUpdate(false)}
              className="rounded-md border border-gray-200 px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            >
              {t('取消')}
            </button>
            <button
              type="button"
              onClick={() => { void handleUpdate() }}
              className="rounded-md bg-amber-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-amber-700"
            >
              {t('确认升级')}
            </button>
          </div>
        </div>
      </div>
    )}
    </>
  )
}
