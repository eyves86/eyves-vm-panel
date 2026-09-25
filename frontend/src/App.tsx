import { Routes, Route, Navigate } from 'react-router'
import { useAuth } from './contexts/AuthContext'
import { adminUrl } from './services/panelPath'
import Login from './pages/Login'
import UserLogin from './pages/UserLogin'
import MyServers from './pages/MyServers'
import UserLayout from './components/UserLayout'
import Dashboard from './pages/Dashboard'
import Containers from './pages/Containers'
import ContainerDetail from './pages/ContainerDetail'

import Security from './pages/Security'
import AuditLogs from './pages/AuditLogs'
import ApiIntegration from './pages/ApiIntegration'
import HostReport from './pages/HostReport'
import Settings from './pages/Settings'
import ImageManagement from './pages/ImageManagement'
import Snapshots from './pages/Snapshots'
import BackupPlans from './pages/BackupPlans'
import Routing from './pages/Routing'
import Storage from './pages/Storage'
import SubUserManagement from './pages/SubUserManagement'
import AdminAccounts from './pages/AdminAccounts'
import NodeMigration from './pages/NodeMigration'
import PolicyManagement from './pages/PolicyManagement'
import NodeManagement from './pages/NodeManagement'
import Layout from './components/Layout'
import Tenants from './pages/Tenants'
import Regions from './pages/Regions'
import IPGroups from './pages/IPGroups'
import ISOs from './pages/ISOs'
import MetricRetention from './pages/MetricRetention'
import Monitoring from './pages/Monitoring'
import TaskCenter from './pages/TaskCenter'

function LoadingScreen() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-white dark:bg-gray-950">
      <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-black dark:border-white"></div>
    </div>
  )
}

// AdminPortalRoute 保护**管理员门户**：未登录去管理员登录页（自定义路径）；
// 子用户被送到用户门户，避免出现空管理页或入口混淆（数据接口仍由后端 scope 兜底）。
function AdminPortalRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading, isSubUser } = useAuth()
  if (isLoading) return <LoadingScreen />
  if (!isAuthenticated) return <Navigate to={adminUrl('login') || '/user/login'} replace />
  if (isSubUser) return <Navigate to="/user" replace />
  return <>{children}</>
}

// UserPortalRoute 保护**用户门户**（固定 /user/*）：未登录去用户登录页；
// 管理员被送回管理门户（管理员不需要用户视图）。
function UserPortalRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading, isSubUser } = useAuth()
  if (isLoading) return <LoadingScreen />
  if (!isAuthenticated) return <Navigate to="/user/login" replace />
  if (!isSubUser) return <Navigate to={adminUrl() || '/user/login'} replace />
  return <>{children}</>
}

function App() {
  // 管理员入口路径由服务端注入；为 null 表示当前页面不挂载管理端路由。
  const adminLoginPath = adminUrl('login')
  const adminRootPath = adminUrl()
  const fallback = adminRootPath && adminRootPath === '/' ? '/' : '/user'

  return (
    <Routes>
      {/* 管理员入口（路径可自定义） */}
      {adminLoginPath && <Route path={adminLoginPath} element={<Login />} />}
      {adminRootPath && (
        <Route path={adminRootPath} element={<AdminPortalRoute><Layout /></AdminPortalRoute>}>
          <Route index element={<Dashboard />} />
          <Route path="containers" element={<Containers />} />
          <Route path="monitoring" element={<Monitoring />} />
          <Route path="tasks" element={<TaskCenter />} />
          <Route path="container/:id" element={<ContainerDetail />} />

          <Route path="images" element={<ImageManagement />} />
          <Route path="security" element={<Security />} />
          <Route path="snapshots" element={<Snapshots />} />
          <Route path="backup-plans" element={<BackupPlans />} />
          <Route path="routing" element={<Routing />} />
          <Route path="migration" element={<NodeMigration />} />
          <Route path="nodes" element={<NodeManagement />} />
          <Route path="policies" element={<PolicyManagement />} />
          <Route path="storage" element={<Storage />} />
          <Route path="audit-logs" element={<AuditLogs />} />
          <Route path="api-integration" element={<ApiIntegration />} />
          <Route path="host-report" element={<HostReport />} />
          <Route path="sub-users" element={<SubUserManagement />} />
          <Route path="admins" element={<AdminAccounts />} />
          <Route path="tenants" element={<Tenants />} />
          <Route path="regions" element={<Regions />} />
          <Route path="ip-groups" element={<IPGroups />} />
          <Route path="isos" element={<ISOs />} />
          <Route path="metric-retention" element={<MetricRetention />} />
          <Route path="settings" element={<Settings />} />
        </Route>
      )}

      {/* 用户入口（固定 /user） */}
      <Route path="/user/login" element={<UserLogin />} />
      <Route path="/user" element={<UserPortalRoute><UserLayout /></UserPortalRoute>}>
        <Route index element={<MyServers />} />
        <Route path="container/:id" element={<ContainerDetail />} />
      </Route>

      <Route path="*" element={<Navigate to={fallback} replace />} />
    </Routes>
  )
}

export default App
