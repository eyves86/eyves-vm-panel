import { Routes, Route, Navigate } from 'react-router'
import { useAuth } from './contexts/AuthContext'
import Login from './pages/Login'
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
import Routing from './pages/Routing'
import Storage from './pages/Storage'
import SubUserManagement from './pages/SubUserManagement'
import NodeMigration from './pages/NodeMigration'
import PolicyManagement from './pages/PolicyManagement'
import NodeManagement from './pages/NodeManagement'
import Layout from './components/Layout'
import Tenants from './pages/Tenants'
import Regions from './pages/Regions'
import IPGroups from './pages/IPGroups'
import ISOs from './pages/ISOs'
import MetricRetention from './pages/MetricRetention'

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading } = useAuth()

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-white dark:bg-gray-950">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-black dark:border-white"></div>
      </div>
    )
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />
  }

  return <>{children}</>
}

// AdminRoute 仅允许内置管理员访问；子用户（含仅授权容器）越权直达管理路由时
// 重定向到其容器视图，避免出现空管理员页面或泄露入口。数据接口仍由后端 scope 兜底。
function AdminRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading, isSubUser, containerIdentifiers } = useAuth()

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-white dark:bg-gray-950">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-black dark:border-white"></div>
      </div>
    )
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />
  }

  if (isSubUser) {
    const firstContainer = containerIdentifiers[0]
    return <Navigate to={firstContainer ? `/container/${encodeURIComponent(firstContainer)}` : '/containers'} replace />
  }

  return <>{children}</>
}

function HomeRoute() {
  const { isSubUser, containerIdentifiers } = useAuth()
  if (isSubUser) {
    const firstContainer = containerIdentifiers[0]
    return <Navigate to={firstContainer ? `/container/${encodeURIComponent(firstContainer)}` : '/containers'} replace />
  }
  return <Dashboard />
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        path="/"
        element={
          <ProtectedRoute>
            <Layout />
          </ProtectedRoute>
        }
      >
        <Route index element={<HomeRoute />} />
        <Route path="containers" element={<Containers />} />
        <Route path="container/:id" element={<ContainerDetail />} />

        <Route path="images" element={<AdminRoute><ImageManagement /></AdminRoute>} />
        <Route path="security" element={<AdminRoute><Security /></AdminRoute>} />
        <Route path="snapshots" element={<AdminRoute><Snapshots /></AdminRoute>} />
        <Route path="routing" element={<AdminRoute><Routing /></AdminRoute>} />
        <Route path="migration" element={<AdminRoute><NodeMigration /></AdminRoute>} />
        <Route path="nodes" element={<AdminRoute><NodeManagement /></AdminRoute>} />
        <Route path="policies" element={<AdminRoute><PolicyManagement /></AdminRoute>} />
        <Route path="storage" element={<AdminRoute><Storage /></AdminRoute>} />
        <Route path="audit-logs" element={<AdminRoute><AuditLogs /></AdminRoute>} />
        <Route path="api-integration" element={<AdminRoute><ApiIntegration /></AdminRoute>} />
        <Route path="host-report" element={<AdminRoute><HostReport /></AdminRoute>} />
        <Route path="sub-users" element={<AdminRoute><SubUserManagement /></AdminRoute>} />
        <Route path="tenants" element={<AdminRoute><Tenants /></AdminRoute>} />
        <Route path="regions" element={<AdminRoute><Regions /></AdminRoute>} />
        <Route path="ip-groups" element={<AdminRoute><IPGroups /></AdminRoute>} />
        <Route path="isos" element={<AdminRoute><ISOs /></AdminRoute>} />
        <Route path="metric-retention" element={<AdminRoute><MetricRetention /></AdminRoute>} />
        <Route path="settings" element={<AdminRoute><Settings /></AdminRoute>} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

export default App
