import { useCallback, useEffect, useState } from 'react'
import { Copy, HardDrive, KeyRound, Link2, LogIn, Pencil, Plus, RefreshCw, Save, ScrollText, Search, Trash2, UserCog, X } from 'lucide-react'
import { useDialog } from '../components/Dialog'
import { useLanguage } from '../contexts/LanguageContext'
import api, {
  AuditLog,
  Container,
  ImageInfo,
  LoginLog,
  Tenant,
  bindSubUserContainers,
  createSubUserAdvanced,
  deleteSubUser,
  getContainers,
  getImages,
  getTenants,
  updateSubUser,
  updateSubUserImages,
  updateSubUserTenant,
} from '../services/api'
import { copyToClipboard } from '../utils/clipboard'

interface SubUserItem {
  id: string
  username: string
  email?: string
  role?: string
  tenant?: string
  container_names: string[]
  container_uuids: string[]
  allowed_image_ids?: string[]
  image_limit_configured?: boolean
  current_image_ids?: string[]
  container_name: string
  container_uuid: string
  access_code: string
  password?: string
  created_at: string
  last_login: string
  last_login_ip: string
  last_login_ua: string
}

interface AuditLogExt extends AuditLog {
  ip?: string
  user_agent?: string
  success?: boolean
  error?: string
}

interface SubUserForm {
  username: string
  email: string
  password: string
  role: 'operator' | 'viewer'
  tenant: string
}

const EMPTY_FORM: SubUserForm = { username: '', email: '', password: '', role: 'operator', tenant: '' }

export default function SubUserManagement() {
  const dialog = useDialog()
  const { t } = useLanguage()
  const [users, setUsers] = useState<SubUserItem[]>([])
  const [loading, setLoading] = useState(true)
  const [auditLogs, setAuditLogs] = useState<AuditLogExt[] | null>(null)
  const [loginLogs, setLoginLogs] = useState<LoginLog[] | null>(null)
  const [modalTitle, setModalTitle] = useState('')
  const [passwordUser, setPasswordUser] = useState<SubUserItem | null>(null)
  const [imageUser, setImageUser] = useState<SubUserItem | null>(null)
  const [images, setImages] = useState<ImageInfo[]>([])
  const [selectedImageIDs, setSelectedImageIDs] = useState<string[]>([])
  const [imagesLoading, setImagesLoading] = useState(false)
  const [savingImages, setSavingImages] = useState(false)
  const [rotatingPassword, setRotatingPassword] = useState(false)
  const [logPage, setLogPage] = useState(1)
  const [logPageSize, setLogPageSize] = useState(10)

  // 新建子用户
  const [createOpen, setCreateOpen] = useState(false)
  const [createForm, setCreateForm] = useState<SubUserForm>(EMPTY_FORM)
  const [createContainers, setCreateContainers] = useState<Container[]>([])
  const [createSelected, setCreateSelected] = useState<string[]>([])
  const [creating, setCreating] = useState(false)
  const [createSearch, setCreateSearch] = useState('')

  // 编辑子用户
  const [editUser, setEditUser] = useState<SubUserItem | null>(null)
  const [editForm, setEditForm] = useState<SubUserForm>(EMPTY_FORM)
  const [savingEdit, setSavingEdit] = useState(false)
  const [tenantOptions, setTenantOptions] = useState<Tenant[]>([])

  // 管理容器绑定
  const [bindUser, setBindUser] = useState<SubUserItem | null>(null)
  const [bindContainers, setBindContainers] = useState<Container[]>([])
  const [bindSelected, setBindSelected] = useState<string[]>([])
  const [bindLoading, setBindLoading] = useState(false)
  const [bindSaving, setBindSaving] = useState(false)
  const [bindSearch, setBindSearch] = useState('')

  // 容器多时的绑定检索：按 名称 / 数字 ID / UUID 过滤（大小写不敏感）。
  const filterContainers = (list: Container[], query: string): Container[] => {
    const q = query.trim().toLowerCase()
    if (!q) return list
    return list.filter((c) =>
      (c.name || '').toLowerCase().includes(q) ||
      String(c.id || '').includes(q) ||
      (c.uuid || '').toLowerCase().includes(q))
  }

  const fetchUsers = useCallback(async () => {
    try {
      const res = await api.get<{ success: boolean; data: SubUserItem[] }>('/sub-users')
      setUsers(res.data.data || [])
    } catch (err) {
      console.error(err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { fetchUsers() }, [fetchUsers])

  const managementUrl = (user: SubUserItem) => `${window.location.origin}/user/login?code=${user.access_code}`

  const copyText = async (text: string) => {
    await copyToClipboard(text)
  }

  // ---- 新建子用户 ------------------------------------------------------------
  const openCreate = async () => {
    setCreateForm(EMPTY_FORM)
    setCreateSelected([])
    setCreateSearch('')
    setCreateOpen(true)
    try {
      const res = await getContainers()
      setCreateContainers((res.data.data || []).filter((c) => !c.owner_sub_user_id))
    } catch {
      setCreateContainers([])
    }
  }

  const submitCreate = async () => {
    if (creating) return
    if (createForm.username.trim() && createForm.password && createForm.password.length < 8) {
      dialog.alert('创建失败', '密码至少 8 位')
      return
    }
    setCreating(true)
    try {
      const res = await createSubUserAdvanced({
        container_names: createSelected,
        username: createForm.username.trim() || undefined,
        email: createForm.email.trim() || undefined,
        password: createForm.password || undefined,
        role: createForm.role,
        tenant: createForm.tenant.trim() || undefined,
      })
      setCreateOpen(false)
      await fetchUsers()
      const created = res.data.data
      if (created?.password) {
        setUsers((prev) => prev.map((u) => (u.id === created.id ? { ...u, password: created.password } : u)))
        setPasswordUser({ ...created, last_login: '', last_login_ip: '', last_login_ua: '' } as SubUserItem)
      } else {
        dialog.alert('完成', '子用户已创建')
      }
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      dialog.alert('创建失败', error.response?.data?.message || '请稍后重试')
    } finally {
      setCreating(false)
    }
  }

  // ---- 编辑子用户 ------------------------------------------------------------
  const openEdit = async (user: SubUserItem) => {
    setEditUser(user)
    setEditForm({
      username: user.username,
      email: user.email || '',
      password: '',
      role: (user.role === 'viewer' ? 'viewer' : 'operator'),
      tenant: user.tenant || '',
    })
    // 变更租户：下拉选择已有租户
    try {
      const res = await getTenants()
      setTenantOptions(res.data.data || [])
    } catch {
      setTenantOptions([])
    }
  }

  const submitEdit = async () => {
    if (!editUser || savingEdit) return
    if (editForm.password && editForm.password.length < 8) {
      dialog.alert('保存失败', '密码至少 8 位')
      return
    }
    setSavingEdit(true)
    try {
      await updateSubUser(editUser.id, {
        username: editForm.username.trim() || undefined,
        email: editForm.email.trim() || undefined,
        role: editForm.role,
        password: editForm.password || undefined,
      })
      // 租户走独立端点（变更租户）
      if ((editForm.tenant || '') !== (editUser.tenant || '')) {
        await updateSubUserTenant(editUser.id, editForm.tenant.trim())
      }
      setEditUser(null)
      await fetchUsers()
      dialog.alert('完成', '子用户信息已保存')
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      dialog.alert('保存失败', error.response?.data?.message || '请稍后重试')
    } finally {
      setSavingEdit(false)
    }
  }

  // ---- 删除子用户 ------------------------------------------------------------
  const removeUser = async (user: SubUserItem) => {
    const ok = await dialog.confirm('删除子用户', `确定删除「${user.username}」？其名下 ${user.container_names.length} 个容器将被解绑归属（容器本身保留）`)
    if (!ok) return
    try {
      const res = await deleteSubUser(user.id)
      await fetchUsers()
      const freed = res.data.data?.freed_containers || []
      dialog.alert('完成', `已删除「${user.username}」${freed.length > 0 ? `，解绑容器：${freed.join('、')}` : ''}`)
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      dialog.alert('删除失败', error.response?.data?.message || '请稍后重试')
    }
  }

  // ---- 管理容器绑定 ----------------------------------------------------------
  const openBind = async (user: SubUserItem) => {
    setBindUser(user)
    setBindSelected(user.container_uuids || [])
    setBindSearch('')
    setBindLoading(true)
    try {
      const res = await getContainers()
      setBindContainers(res.data.data || [])
    } catch {
      setBindContainers([])
    } finally {
      setBindLoading(false)
    }
  }

  const toggleCreateSelect = (uuid: string) => {
    setCreateSelected((prev) => prev.includes(uuid) ? prev.filter((item) => item !== uuid) : [...prev, uuid])
  }

  const toggleBind = (uuid: string, disabled: boolean) => {
    if (disabled || bindSaving) return
    setBindSelected((prev) => prev.includes(uuid) ? prev.filter((item) => item !== uuid) : [...prev, uuid])
  }

  const submitBind = async () => {
    if (!bindUser || bindSaving) return
    setBindSaving(true)
    try {
      await bindSubUserContainers(bindUser.id, bindSelected)
      setBindUser(null)
      await fetchUsers()
      dialog.alert('完成', '容器绑定已更新')
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      dialog.alert('保存失败', error.response?.data?.message || '请稍后重试')
    } finally {
      setBindSaving(false)
    }
  }

  // ---- 既有功能 --------------------------------------------------------------
  const rotatePassword = async (user: SubUserItem) => {
    setRotatingPassword(true)
    try {
      const res = await api.post(`/sub-users/${user.id}/rotate-password`)
      const data = res.data.data
      const updatedUser = {
        ...user,
        username: data?.username || user.username,
        access_code: data?.access_code || user.access_code,
        password: data?.password || '',
      }
      setUsers((prev) => prev.map((item) => (item.id === user.id ? updatedUser : item)))
      setPasswordUser(updatedUser)
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      dialog.alert('轮换失败', error.response?.data?.message || '请稍后重试')
    } finally {
      setRotatingPassword(false)
    }
  }

  const toggleRole = async (user: SubUserItem) => {
    const nextRole = user.role === 'viewer' ? 'operator' : 'viewer'
    try {
      const res = await api.put(`/sub-users/${user.id}/role`, { role: nextRole })
      const role = res.data.data?.role || nextRole
      setUsers((prev) => prev.map((item) => (item.id === user.id ? { ...item, role } : item)))
      dialog.alert('完成', nextRole === 'viewer' ? '已设为只读角色：该子用户只能查看，不能执行开机/关机/删除等操作' : '已设为操作角色：该子用户可以执行全部容器操作')
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      dialog.alert('切换失败', error.response?.data?.message || '请稍后重试')
    }
  }

  const openImageLimit = async (user: SubUserItem) => {
    setImageUser(user)
    setSelectedImageIDs(user.allowed_image_ids || [])
    setImagesLoading(true)
    try {
      const res = await getImages()
      const currentIDs = new Set(user.current_image_ids || [])
      setImages((res.data.data || []).filter((image) => image.downloaded && (image.enabled || currentIDs.has(image.id))))
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      dialog.alert('加载失败', error.response?.data?.message || '获取镜像列表失败')
    } finally {
      setImagesLoading(false)
    }
  }

  const toggleImageID = (id: string) => {
    setSelectedImageIDs((prev) => prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id])
  }

  const saveImageLimit = async () => {
    if (!imageUser) return
    setSavingImages(true)
    try {
      const res = await updateSubUserImages(imageUser.id, selectedImageIDs)
      const updated = {
        ...imageUser,
        allowed_image_ids: res.data.data?.allowed_image_ids || selectedImageIDs,
        image_limit_configured: true,
      }
      setUsers((prev) => prev.map((item) => (item.id === imageUser.id ? { ...item, allowed_image_ids: updated.allowed_image_ids, image_limit_configured: true } : item)))
      setImageUser(null)
    } catch (err: unknown) {
      const error = err as { response?: { data?: { message?: string } } }
      dialog.alert('保存失败', error.response?.data?.message || '保存可用镜像失败')
    } finally {
      setSavingImages(false)
    }
  }

  const showAuditLogs = async (user: SubUserItem) => {
    try {
      const res = await api.get(`/sub-users/${user.id}/audit-logs`)
      setAuditLogs(res.data.data || [])
      setLoginLogs(null)
      setModalTitle(`${user.username} - 操作日志`)
      setLogPage(1)
    } catch {
      dialog.alert('错误', '获取操作日志失败')
    }
  }

  const showLoginLogs = async (user: SubUserItem) => {
    try {
      const res = await api.get(`/sub-users/${user.id}/login-logs`)
      setLoginLogs(res.data.data || [])
      setAuditLogs(null)
      setModalTitle(`${user.username} - 登录日志`)
      setLogPage(1)
    } catch {
      dialog.alert('错误', '获取登录日志失败')
    }
  }

  const closeModal = () => {
    setAuditLogs(null)
    setLoginLogs(null)
  }

  const currentLogTotal = auditLogs?.length ?? loginLogs?.length ?? 0
  const logTotalPages = Math.max(1, Math.ceil(currentLogTotal / logPageSize))
  const currentLogPage = Math.min(logPage, logTotalPages)
  const logStart = (currentLogPage - 1) * logPageSize
  const currentAuditLogs = auditLogs?.slice(logStart, logStart + logPageSize)
  const currentLoginLogs = loginLogs?.slice(logStart, logStart + logPageSize)

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-brand-600" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-black dark:text-white">{t('子用户管理')}</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {t('容器分配的子用户列表，共')} {users.length} {t('个')}
          </p>
        </div>
        <button
          onClick={() => { void openCreate() }}
          className="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-2 text-sm font-medium text-white hover:bg-brand-700 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
        >
          <Plus className="h-4 w-4" />
          {t('新建子用户')}
        </button>
      </div>

      <div className="overflow-hidden rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900">
        {users.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-lg bg-gray-100 dark:bg-gray-800">
              <UserCog className="h-7 w-7 text-gray-400" />
            </div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-300">暂无子用户</div>
            <div className="mt-1 text-xs text-gray-400">点击右上角「新建子用户」创建，可先建空账号再绑定容器</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
          <table className="w-full min-w-[980px] text-sm">
            <thead className="border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 text-xs text-gray-500 dark:text-gray-400">
              <tr>
                <th className="px-4 py-3 text-left font-medium w-12">#</th>
                <th className="px-4 py-3 text-left font-medium">用户名</th>
                <th className="px-4 py-3 text-left font-medium">绑定容器</th>
                <th className="px-4 py-3 text-left font-medium">角色</th>
                <th className="px-4 py-3 text-left font-medium">租户</th>
                <th className="px-4 py-3 text-left font-medium">最后登录</th>
                <th className="px-4 py-3 text-center font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
              {users.map((user, index) => (
                <tr key={user.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                  <td className="px-4 py-3 text-gray-400 dark:text-gray-500">{index + 1}</td>
                  <td className="px-4 py-3">
                    <div className="font-medium text-black dark:text-white">{user.username}</div>
                    {user.email && <div className="text-xs text-gray-400">{user.email}</div>}
                  </td>
                  <td className="px-4 py-3 max-w-[260px]">
                    {user.container_names.length > 0 ? (
                      <div className="flex flex-wrap gap-1">
                        {user.container_names.map((name) => (
                          <span key={name} className="inline-flex items-center rounded bg-indigo-50 px-1.5 py-0.5 text-xs font-medium text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300">{name}</span>
                        ))}
                      </div>
                    ) : (
                      <span className="text-xs text-gray-400">未绑定（空账号）</span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <span className={`inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs font-medium ${user.role === 'viewer' ? 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300' : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'}`}>
                      {user.role === 'viewer' ? '只读' : '操作'}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    {user.tenant ? (
                      <span className="inline-flex items-center rounded bg-indigo-50 px-2 py-0.5 text-xs font-medium text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300">{user.tenant}</span>
                    ) : (
                      <span className="text-gray-400">按容器授权</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-gray-600 dark:text-gray-400">
                    {user.last_login ? (
                      <div>
                        <div className="text-xs">{user.last_login}</div>
                        <div className="text-xs text-gray-400 dark:text-gray-500">{user.last_login_ip}</div>
                      </div>
                    ) : (
                      <span className="text-gray-400">从未登录</span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap items-center justify-center gap-1">
                      <button
                        onClick={() => setPasswordUser(user)}
                        className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-amber-600 hover:bg-amber-50 dark:hover:bg-amber-900/30 transition-colors"
                        title="查看密码"
                      >
                        <KeyRound className="w-3.5 h-3.5" />
                        密码
                      </button>
                      <button
                        onClick={() => { void openEdit(user) }}
                        className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 transition-colors"
                        title="编辑用户名 / 邮箱 / 密码 / 角色 / 变更租户"
                      >
                        <Pencil className="w-3.5 h-3.5" />
                        编辑
                      </button>
                      <button
                        onClick={() => { void openBind(user) }}
                        className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-blue-600 hover:bg-blue-50 dark:hover:bg-blue-900/30 transition-colors"
                        title="管理容器绑定"
                      >
                        <Link2 className="w-3.5 h-3.5" />
                        容器
                      </button>
                      <button
                        onClick={() => showAuditLogs(user)}
                        className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-blue-600 hover:bg-blue-50 dark:hover:bg-blue-900/30 transition-colors"
                        title="查看操作日志"
                      >
                        <ScrollText className="w-3.5 h-3.5" />
                        操作日志
                      </button>
                      <button
                        onClick={() => showLoginLogs(user)}
                        className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-green-600 hover:bg-green-50 dark:hover:bg-green-900/30 transition-colors"
                        title="查看登录日志"
                      >
                        <LogIn className="w-3.5 h-3.5" />
                        登录日志
                      </button>
                      <button
                        onClick={() => openImageLimit(user)}
                        className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-purple-600 hover:bg-purple-50 dark:hover:bg-purple-900/30 transition-colors"
                        title="可用镜像"
                      >
                        <HardDrive className="w-3.5 h-3.5" />
                        可用镜像
                      </button>
                      <button
                        onClick={() => toggleRole(user)}
                        className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-teal-600 hover:bg-teal-50 dark:hover:bg-teal-900/30 transition-colors"
                        title={user.role === 'viewer' ? '切换为操作角色' : '切换为只读角色'}
                      >
                        <UserCog className="w-3.5 h-3.5" />
                        {user.role === 'viewer' ? '设为操作' : '设为只读'}
                      </button>
                      <button
                        onClick={() => { void removeUser(user) }}
                        className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-red-600 hover:bg-red-50 dark:hover:bg-red-900/30 transition-colors"
                        title="删除子用户（容器将被解绑归属）"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                        删除
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
        )}
      </div>

      {/* 新建子用户弹窗 */}
      {createOpen && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-lg border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-xl max-h-[85vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">新建子用户</h3>
              <button onClick={() => setCreateOpen(false)} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-5 space-y-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">用户名（留空自动生成）</label>
                  <input
                    type="text"
                    value={createForm.username}
                    onChange={(e) => setCreateForm((f) => ({ ...f, username: e.target.value }))}
                    placeholder="user01"
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  />
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">邮箱（可选）</label>
                  <input
                    type="text"
                    value={createForm.email}
                    onChange={(e) => setCreateForm((f) => ({ ...f, email: e.target.value }))}
                    placeholder="user@example.com"
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  />
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">密码（留空自动生成 16 位）</label>
                  <input
                    type="text"
                    value={createForm.password}
                    onChange={(e) => setCreateForm((f) => ({ ...f, password: e.target.value }))}
                    placeholder="至少 8 位"
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  />
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">角色</label>
                  <select
                    value={createForm.role}
                    onChange={(e) => setCreateForm((f) => ({ ...f, role: e.target.value as 'operator' | 'viewer' }))}
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  >
                    <option value="operator">操作（可开关机等全部操作）</option>
                    <option value="viewer">只读（仅查看）</option>
                  </select>
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">租户（可选）</label>
                  <input
                    type="text"
                    value={createForm.tenant}
                    onChange={(e) => setCreateForm((f) => ({ ...f, tenant: e.target.value }))}
                    placeholder="留空按容器单独授权"
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  />
                </div>
              </div>
              <div>
                <div className="mb-1 flex items-center justify-between gap-2">
                  <label className="block text-xs font-medium text-gray-500 dark:text-gray-400">
                    绑定容器（可选，可创建后再绑定）
                    <span className="ml-1 text-gray-400">已选 {createSelected.length} / 共 {createContainers.length}</span>
                  </label>
                  <div className="flex items-center gap-1.5 text-[11px]">
                    <button
                      type="button"
                      onClick={() => {
                        const visible = filterContainers(createContainers, createSearch)
                        setCreateSelected(Array.from(new Set([...createSelected, ...visible.map((c) => c.uuid)])))
                      }}
                      className="rounded px-1.5 py-0.5 text-brand-600 hover:bg-brand-50 dark:text-brand-400 dark:hover:bg-brand-900/30"
                    >
                      全选筛选结果
                    </button>
                    <button
                      type="button"
                      onClick={() => setCreateSelected([])}
                      className="rounded px-1.5 py-0.5 text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800"
                    >
                      清空
                    </button>
                  </div>
                </div>
                <div className="relative mb-2">
                  <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-gray-400" />
                  <input
                    type="text"
                    value={createSearch}
                    onChange={(e) => setCreateSearch(e.target.value)}
                    placeholder="搜索容器名称 / ID / UUID"
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 pl-8 pr-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  />
                </div>
                <div className="max-h-48 overflow-y-auto rounded-md border border-gray-200 dark:border-gray-700 divide-y divide-gray-100 dark:divide-gray-800">
                  {createContainers.length === 0 ? (
                    <div className="px-3 py-6 text-center text-xs text-gray-400">暂无可绑定的未归属容器</div>
                  ) : filterContainers(createContainers, createSearch).length === 0 ? (
                    <div className="px-3 py-6 text-center text-xs text-gray-400">没有匹配「{createSearch.trim()}」的容器</div>
                  ) : filterContainers(createContainers, createSearch).map((c) => (
                    <label key={c.uuid || c.id} className="flex cursor-pointer items-center gap-2.5 px-3 py-2 text-sm hover:bg-gray-50 dark:hover:bg-gray-800">
                      <input
                        type="checkbox"
                        checked={createSelected.includes(c.uuid)}
                        onChange={() => toggleCreateSelect(c.uuid)}
                        className="h-4 w-4 rounded border-gray-300 accent-brand-600"
                      />
                      <span className="font-medium text-black dark:text-white">{c.name}</span>
                      <span className="text-xs text-gray-400">#{c.id}</span>
                    </label>
                  ))}
                </div>
              </div>
            </div>
            <div className="flex items-center justify-end gap-2 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
              <button onClick={() => setCreateOpen(false)} className="px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 rounded-md">取消</button>
              <button
                onClick={() => { void submitCreate() }}
                disabled={creating}
                className="inline-flex items-center gap-1.5 px-3 py-2 text-sm bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
              >
                <Plus className="h-4 w-4" />
                {creating ? '创建中...' : '创建'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 编辑子用户弹窗 */}
      {editUser && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-lg border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-md overflow-hidden">
            <div className="flex items-center justify-between px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">编辑子用户</h3>
              <button onClick={() => setEditUser(null)} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="p-5 space-y-3">
              <div>
                <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">用户名</label>
                <input
                  type="text"
                  value={editForm.username}
                  onChange={(e) => setEditForm((f) => ({ ...f, username: e.target.value }))}
                  className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">邮箱</label>
                <input
                  type="text"
                  value={editForm.email}
                  onChange={(e) => setEditForm((f) => ({ ...f, email: e.target.value }))}
                  placeholder="可选"
                  className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">新密码（留空不修改）</label>
                <input
                  type="text"
                  value={editForm.password}
                  onChange={(e) => setEditForm((f) => ({ ...f, password: e.target.value }))}
                  placeholder="至少 8 位"
                  className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">角色</label>
                  <select
                    value={editForm.role}
                    onChange={(e) => setEditForm((f) => ({ ...f, role: e.target.value as 'operator' | 'viewer' }))}
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  >
                    <option value="operator">操作</option>
                    <option value="viewer">只读</option>
                  </select>
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">租户（变更租户）</label>
                  <select
                    value={editForm.tenant}
                    onChange={(e) => setEditForm((f) => ({ ...f, tenant: e.target.value }))}
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  >
                    <option value="">留空按容器授权</option>
                    {tenantOptions.map((t) => (
                      <option key={t.id} value={t.name}>{t.name}</option>
                    ))}
                    {editForm.tenant && !tenantOptions.some((t) => t.name === editForm.tenant) && (
                      <option value={editForm.tenant}>{editForm.tenant}</option>
                    )}
                  </select>
                </div>
              </div>
            </div>
            <div className="flex items-center justify-end gap-2 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
              <button onClick={() => setEditUser(null)} className="px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 rounded-md">取消</button>
              <button
                onClick={() => { void submitEdit() }}
                disabled={savingEdit}
                className="inline-flex items-center gap-1.5 px-3 py-2 text-sm bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
              >
                <Save className="h-4 w-4" />
                {savingEdit ? '保存中...' : '保存'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 管理容器绑定弹窗 */}
      {bindUser && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-lg border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-xl max-h-[85vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <div>
                <h3 className="text-sm font-semibold text-black dark:text-white">管理容器绑定</h3>
                <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{bindUser.username} · 勾选该子用户可访问的容器</p>
              </div>
              <button onClick={() => setBindUser(null)} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="border-b border-gray-200 dark:border-gray-700 px-5 py-3 space-y-2">
              <div className="flex items-center gap-2">
                <div className="relative flex-1">
                  <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-gray-400" />
                  <input
                    type="text"
                    value={bindSearch}
                    onChange={(e) => setBindSearch(e.target.value)}
                    placeholder="搜索容器名称 / ID / UUID"
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 pl-8 pr-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  />
                </div>
                <button
                  type="button"
                  onClick={() => {
                    const visible = filterContainers(bindContainers, bindSearch).filter((c) => !c.owner_sub_user_id || c.owner_sub_user_id === bindUser.id)
                    setBindSelected(Array.from(new Set([...bindSelected, ...visible.map((c) => c.uuid)])))
                  }}
                  className="shrink-0 rounded-md border border-gray-200 dark:border-gray-700 px-2.5 py-1.5 text-xs text-brand-600 hover:bg-brand-50 dark:text-brand-400 dark:hover:bg-brand-900/30"
                >
                  全选筛选结果
                </button>
                <button
                  type="button"
                  onClick={() => setBindSelected([])}
                  className="shrink-0 rounded-md border border-gray-200 dark:border-gray-700 px-2.5 py-1.5 text-xs text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800"
                >
                  清空
                </button>
              </div>
              <p className="text-[11px] text-gray-400">
                共 {bindContainers.length} 个容器{bindSearch.trim() ? ` · 筛选出 ${filterContainers(bindContainers, bindSearch).length} 个` : ''} · 已选 {bindSelected.length}
              </p>
            </div>
            <div className="flex-1 overflow-y-auto p-5">
              {bindLoading ? (
                <div className="flex items-center justify-center py-12">
                  <div className="h-7 w-7 animate-spin rounded-full border-b-2 border-brand-600" />
                </div>
              ) : bindContainers.length === 0 ? (
                <div className="rounded-lg border border-dashed border-gray-300 px-4 py-10 text-center text-sm text-gray-500">暂无容器</div>
              ) : filterContainers(bindContainers, bindSearch).length === 0 ? (
                <div className="rounded-lg border border-dashed border-gray-300 px-4 py-10 text-center text-sm text-gray-500">没有匹配「{bindSearch.trim()}」的容器</div>
              ) : (
                <div className="space-y-1">
                  {filterContainers(bindContainers, bindSearch).map((c) => {
                    const ownedByOther = !!c.owner_sub_user_id && c.owner_sub_user_id !== bindUser.id
                    const checked = bindSelected.includes(c.uuid)
                    return (
                      <label
                        key={c.uuid || c.id}
                        className={`flex items-center gap-2.5 rounded-md border px-3 py-2 text-sm transition-colors ${
                          ownedByOther
                            ? 'cursor-not-allowed border-gray-100 dark:border-gray-800 opacity-50'
                            : checked
                              ? 'cursor-pointer border-brand-600 bg-gray-50 dark:border-white dark:bg-gray-800'
                              : 'cursor-pointer border-gray-200 hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800'
                        }`}
                        title={ownedByOther ? '已绑定给其他子用户，请先在容器上变更属主' : undefined}
                      >
                        <input
                          type="checkbox"
                          checked={checked}
                          disabled={ownedByOther}
                          onChange={() => toggleBind(c.uuid, ownedByOther)}
                          className="h-4 w-4 rounded border-gray-300 accent-brand-600"
                        />
                        <span className="min-w-0 flex-1">
                          <span className="font-medium text-black dark:text-white">{c.name}</span>
                          <span className="ml-1.5 text-xs text-gray-400">#{c.id}</span>
                        </span>
                        {ownedByOther && (
                          <span className="shrink-0 text-[10px] text-gray-400">其他属主</span>
                        )}
                      </label>
                    )
                  })}
                </div>
              )}
            </div>
            <div className="flex items-center justify-between gap-3 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
              <span className="text-xs text-gray-500 dark:text-gray-400">已绑定 {bindSelected.length} 个容器</span>
              <div className="flex items-center gap-2">
                <button onClick={() => setBindUser(null)} className="px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 rounded-md">取消</button>
                <button
                  onClick={() => { void submitBind() }}
                  disabled={bindSaving || bindLoading}
                  className="inline-flex items-center gap-1.5 px-3 py-2 text-sm bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
                >
                  <Save className="h-4 w-4" />
                  {bindSaving ? '保存中...' : '保存'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {passwordUser && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-lg border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-lg max-h-[85vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between gap-3 px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <div>
                <h3 className="text-sm font-semibold text-black dark:text-white">登录凭据</h3>
                <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">账号登录管理绑定的全部容器；访问码用于分享登录</p>
              </div>
              <div className="flex items-center gap-2">
                <button
                  onClick={() => rotatePassword(passwordUser)}
                  disabled={rotatingPassword}
                  className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded text-xs text-amber-700 bg-amber-50 hover:bg-amber-100 dark:text-amber-300 dark:bg-amber-900/30 dark:hover:bg-amber-900/50 disabled:opacity-50"
                  title="生成新的随机密码（旧密码立即失效，所有已登录会话需重新登录）"
                >
                  <RefreshCw className={`w-3.5 h-3.5 ${rotatingPassword ? 'animate-spin' : ''}`} />
                  {rotatingPassword ? '轮换中...' : '轮换密码'}
                </button>
                <button onClick={() => setPasswordUser(null)} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded">
                  <X className="w-4 h-4" />
                </button>
              </div>
            </div>
            <div className="flex-1 overflow-y-auto p-5 space-y-4">
              {/* 区块 1：账号登录（子用户本人，用户名 + 密码） */}
              <div className="rounded-lg border border-gray-200 dark:border-gray-700 overflow-hidden">
                <div className="flex items-center gap-1.5 bg-gray-50 dark:bg-gray-800 px-3.5 py-2 text-xs font-medium text-gray-600 dark:text-gray-300">
                  <LogIn className="h-3.5 w-3.5" />
                  账号登录
                  <span className="font-normal text-gray-400">（该子用户本人，可管理绑定的全部容器）</span>
                </div>
                <div className="px-3.5 py-3 text-sm space-y-2.5">
                  <div className="flex items-start justify-between gap-3">
                    <span className="shrink-0 text-gray-500 dark:text-gray-400">用户名</span>
                    <div className="flex min-w-0 items-center gap-1">
                      <span className="font-mono text-xs text-black dark:text-white break-all">{passwordUser.username}</span>
                      <button onClick={() => copyText(passwordUser.username)} className="shrink-0 p-0.5 text-gray-400 hover:text-black dark:hover:text-white rounded" title="复制">
                        <Copy className="w-3 h-3" />
                      </button>
                    </div>
                  </div>
                  <div className="flex items-start justify-between gap-3">
                    <span className="shrink-0 text-gray-500 dark:text-gray-400">密码</span>
                    <div className="flex min-w-0 items-center gap-1">
                      {passwordUser.password ? (
                        <>
                          <span className="font-mono text-xs text-black dark:text-white break-all">{passwordUser.password}</span>
                          <button onClick={() => copyText(passwordUser.password || '')} className="shrink-0 p-0.5 text-gray-400 hover:text-black dark:hover:text-white rounded" title="复制">
                            <Copy className="w-3 h-3" />
                          </button>
                        </>
                      ) : (
                        <span className="text-right text-xs text-gray-400">密码经加密存储无法查看，点击「轮换密码」生成新密码</span>
                      )}
                    </div>
                  </div>
                </div>
              </div>

              {/* 区块 2：访问码登录（分享给他人：访问码 + 同一密码） */}
              <div className="rounded-lg border border-gray-200 dark:border-gray-700 overflow-hidden">
                <div className="flex items-center gap-1.5 bg-gray-50 dark:bg-gray-800 px-3.5 py-2 text-xs font-medium text-gray-600 dark:text-gray-300">
                  <KeyRound className="h-3.5 w-3.5" />
                  访问码登录（分享）
                  <span className="font-normal text-gray-400">（访问码 + 上述密码，登录后仅能管理已绑定的容器）</span>
                </div>
                <div className="px-3.5 py-3 text-sm space-y-2.5">
                  <div className="flex items-start justify-between gap-3">
                    <span className="shrink-0 text-gray-500 dark:text-gray-400">访问码</span>
                    <div className="flex min-w-0 items-center gap-1">
                      <span className="font-mono text-xs text-black dark:text-white break-all">{passwordUser.access_code}</span>
                      <button onClick={() => copyText(passwordUser.access_code)} className="shrink-0 p-0.5 text-gray-400 hover:text-black dark:hover:text-white rounded" title="复制">
                        <Copy className="w-3 h-3" />
                      </button>
                    </div>
                  </div>
                  <div className="flex items-start justify-between gap-3">
                    <span className="shrink-0 text-gray-500 dark:text-gray-400">分享链接</span>
                    <div className="flex min-w-0 items-center gap-1">
                      <span className="font-mono text-xs text-black dark:text-white break-all">{managementUrl(passwordUser)}</span>
                      <button onClick={() => copyText(managementUrl(passwordUser))} className="shrink-0 p-0.5 text-gray-400 hover:text-black dark:hover:text-white rounded" title="复制">
                        <Copy className="w-3 h-3" />
                      </button>
                    </div>
                  </div>
                  <div className="text-xs text-gray-400">
                    当前授权范围：{passwordUser.container_names?.length ? `${passwordUser.container_names.length} 个容器（${passwordUser.container_names.join('、')}）` : '未绑定容器'}
                  </div>
                </div>
              </div>

              <p className="text-xs text-gray-400">
                如需把<strong>单个容器</strong>分享给他人，建议为该容器单独创建子用户并只绑定此容器，再分享其访问码。
              </p>
            </div>
          </div>
        </div>
      )}

      {imageUser && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-lg border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-2xl max-h-[85vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between gap-3 px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <div>
                <h3 className="text-sm font-semibold text-black dark:text-white">可用镜像</h3>
                <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{imageUser.username} · 默认勾选当前系统，取消后将禁止重装该系统</p>
              </div>
              <button onClick={() => setImageUser(null)} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-5">
              {imagesLoading ? (
                <div className="flex items-center justify-center py-12">
                  <div className="h-7 w-7 animate-spin rounded-full border-b-2 border-brand-600" />
                </div>
              ) : images.length === 0 ? (
                <div className="rounded-lg border border-dashed border-gray-300 px-4 py-10 text-center text-sm text-gray-500">
                  暂无已下载并启用的镜像
                </div>
              ) : (
                <div className="grid gap-2 sm:grid-cols-2">
                  {images.map((image) => {
                    const checked = selectedImageIDs.includes(image.id)
                    const current = (imageUser.current_image_ids || []).includes(image.id)
                    return (
                      <label
                        key={image.id}
                        className={`flex cursor-pointer items-start gap-3 rounded-lg border px-3 py-3 text-sm transition-colors ${checked ? 'border-brand-600 bg-gray-50 dark:border-white dark:bg-gray-800' : 'border-gray-200 hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800'}`}
                      >
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={() => toggleImageID(image.id)}
                          className="mt-1 h-4 w-4 rounded border-gray-300 text-black focus:ring-brand-500"
                        />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate font-medium text-black dark:text-white">{image.name}{current ? '（当前系统）' : ''}</span>
                          <span className="mt-1 block text-xs text-gray-500 dark:text-gray-400">
                            {image.type.toUpperCase()} · {image.arch} · {image.distro} {image.release}
                          </span>
                        </span>
                      </label>
                    )
                  })}
                </div>
              )}
            </div>
            <div className="flex items-center justify-between gap-3 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
              <span className="text-xs text-gray-500 dark:text-gray-400">已选择 {selectedImageIDs.length} 个镜像</span>
              <div className="flex items-center gap-2">
                <button onClick={() => setImageUser(null)} className="px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 rounded-md">
                  取消
                </button>
                <button
                  onClick={saveImageLimit}
                  disabled={savingImages || imagesLoading}
                  className="inline-flex items-center gap-1.5 px-3 py-2 text-sm bg-brand-600 text-white rounded-md hover:bg-brand-700 disabled:opacity-50"
                >
                  <Save className="h-4 w-4" />
                  {savingImages ? '保存中...' : '保存'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Log Modal */}
      {(auditLogs || loginLogs) && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-lg border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-3xl max-h-[85vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">{modalTitle}</h3>
              <button onClick={closeModal} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="overflow-auto flex-1">
              {auditLogs && (
                <table className="w-full text-sm">
                  <thead className="border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 text-xs text-gray-500 dark:text-gray-400 sticky top-0">
                    <tr>
                      <th className="px-4 py-2 text-left">操作时间</th>
                      <th className="px-4 py-2 text-left">操作</th>
                      <th className="px-4 py-2 text-left">IP</th>
                      <th className="px-4 py-2 text-left">UA</th>
                      <th className="px-4 py-2 text-center">结果</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                    {auditLogs.length === 0 ? (
                      <tr><td colSpan={5} className="px-4 py-8 text-center text-gray-400">暂无操作日志</td></tr>
                    ) : currentAuditLogs?.map((log, i) => (
                      <tr key={i} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                        <td className="px-4 py-2 text-xs text-gray-600 dark:text-gray-400 whitespace-nowrap">{log.time}</td>
                        <td className="px-4 py-2 text-xs text-gray-700 dark:text-gray-300">{log.action}</td>
                        <td className="px-4 py-2 text-xs font-mono text-gray-500 dark:text-gray-400">{log.ip || '-'}</td>
                        <td className="px-4 py-2 text-xs text-gray-500 dark:text-gray-400 max-w-[200px] truncate" title={log.user_agent}>{log.user_agent || '-'}</td>
                        <td className="px-4 py-2 text-center">
                          {log.success !== undefined ? (
                            log.success ? (
                              <span className="inline-flex px-2 py-0.5 rounded text-xs bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400">成功</span>
                            ) : (
                              <span className="inline-flex px-2 py-0.5 rounded text-xs bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400" title={log.error}>{log.error ? '失败' : '失败'}</span>
                            )
                          ) : (
                            <span className="text-gray-400">-</span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              {loginLogs && (
                <table className="w-full text-sm">
                  <thead className="border-b border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 text-xs text-gray-500 dark:text-gray-400 sticky top-0">
                    <tr>
                      <th className="px-4 py-2 text-left">登录时间</th>
                      <th className="px-4 py-2 text-left">登录 IP</th>
                      <th className="px-4 py-2 text-left">UA</th>
                      <th className="px-4 py-2 text-center">结果</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                    {loginLogs.length === 0 ? (
                      <tr><td colSpan={4} className="px-4 py-8 text-center text-gray-400">暂无登录日志</td></tr>
                    ) : currentLoginLogs?.map((log, i) => (
                      <tr key={i} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                        <td className="px-4 py-2 text-xs text-gray-600 dark:text-gray-400 whitespace-nowrap">{log.time}</td>
                        <td className="px-4 py-2 text-xs font-mono text-gray-500 dark:text-gray-400">{log.ip}</td>
                        <td className="px-4 py-2 text-xs text-gray-500 dark:text-gray-400 max-w-[250px] truncate" title={log.user_agent}>{log.user_agent}</td>
                        <td className="px-4 py-2 text-center">
                          {log.success ? (
                            <span className="inline-flex px-2 py-0.5 rounded text-xs bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400">成功</span>
                          ) : (
                            <span className="inline-flex px-2 py-0.5 rounded text-xs bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400">失败</span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
            {currentLogTotal > 0 && (
              <div className="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
                <div className="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                  <span>
                    显示 {logStart + 1}-{Math.min(logStart + logPageSize, currentLogTotal)} / {currentLogTotal}
                  </span>
                  <select
                    value={logPageSize}
                    onChange={(event) => {
                      setLogPageSize(Number(event.target.value))
                      setLogPage(1)
                    }}
                    className="h-7 rounded border border-gray-300 bg-white px-2 text-xs text-gray-700 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-300"
                  >
                    <option value={10}>10 / 页</option>
                    <option value={20}>20 / 页</option>
                    <option value={50}>50 / 页</option>
                  </select>
                </div>
                <div className="flex items-center gap-1">
                  <button onClick={() => setLogPage(1)} disabled={currentLogPage === 1} className="rounded border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">首页</button>
                  <button onClick={() => setLogPage((page) => Math.max(1, page - 1))} disabled={currentLogPage === 1} className="rounded border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">上一页</button>
                  <span className="px-2 text-xs text-gray-500 dark:text-gray-400">{currentLogPage} / {logTotalPages}</span>
                  <button onClick={() => setLogPage((page) => Math.min(logTotalPages, page + 1))} disabled={currentLogPage === logTotalPages} className="rounded border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">下一页</button>
                  <button onClick={() => setLogPage(logTotalPages)} disabled={currentLogPage === logTotalPages} className="rounded border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">末页</button>
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
