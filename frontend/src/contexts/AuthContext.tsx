import React, { createContext, useContext, useState, useEffect, ReactNode } from 'react'
import { useNavigate } from 'react-router'
import api, { getAuthToken, setAuthToken, logoutRequest, login as apiLogin, checkAuth, LoginResponse } from '../services/api'
import { v2Me } from '../services/apiV2'
import { adminUrl } from '../services/panelPath'

// CheckAuthData 与服务端 /check-auth 返回的解析结果对应。
type CheckAuthData = {
  type?: string
  username?: string
  sub_user?: boolean
  role?: string
  admin_role?: string
  container_uuids?: string[]
  permission_scopes?: string[]
}

interface AuthContextType {
  isAuthenticated: boolean
  isLoading: boolean
  username: string | null
  isSubUser: boolean
  isAdmin: boolean
  /** 管理员角色：admin | operator | readonly（仅管理员会话有效） */
  adminRole: string
  isReadOnly: boolean
  containerIdentifiers: string[]
  /** 管理员入口登录：仅走 /api/login（含两步验证） */
  adminLogin: (username: string, password: string, turnstileToken?: string) => Promise<void>
  adminLoginWith2FA: (username: string, password: string, code: string, turnstileToken?: string) => Promise<void>
  /** 用户入口登录：仅走 /api/sub-user/login（账号密码） */
  userLogin: (username: string, password: string, turnstileToken?: string) => Promise<void>
  accessCodeLogin: (code: string, password: string, turnstileToken?: string) => Promise<void>
  logout: () => void
  token: string | null
  /** API v2 身份返回的功能点（按钮显隐依据）；v2 不可用时为空对象，前端退回角色判断。 */
  features: Record<string, boolean>
  /** can 判断当前会话是否具备某功能点（如 instance_create / node_manage）。 */
  can: (feature: string) => boolean
}

const AuthContext = createContext<AuthContextType | undefined>(undefined)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [isAuthenticated, setIsAuthenticated] = useState(false)
  const [isLoading, setIsLoading] = useState(true)
  const [username, setUsername] = useState<string | null>(null)
  const [isSubUser, setIsSubUser] = useState(false)
  const [adminRole, setAdminRole] = useState('')
  const [isReadOnly, setIsReadOnly] = useState(false)
  const [containerIdentifiers, setContainerIdentifiers] = useState<string[]>([])
  const [token, setToken] = useState<string | null>(null)
  // 功能点（来自 API v2 /auth/me）：驱动按钮显隐与禁用，避免"点了才报 403"。
  const [features, setFeatures] = useState<Record<string, boolean>>({})
  const navigate = useNavigate()

  // loadFeatures 拉取功能点；失败静默（v2 未启用时前端退回角色判断，不阻塞使用）。
  const loadFeatures = async () => {
    try {
      const me = await v2Me()
      setFeatures(me.features || {})
    } catch {
      setFeatures({})
    }
  }

  const can = (feature: string) => features[feature] === true

  const saveAuth = (t: string, u: string, sub: boolean, ids: string[], readOnly = false) => {
    // 审计 H-6：不再写入 localStorage。令牌由服务端 HttpOnly Cookie 承载
    // （刷新页面仍保持登录），内存副本仅用于需要在头部显式带 Bearer 的场景。
    setAuthToken(t)
    setToken(t)
    setUsername(u)
    setIsSubUser(sub)
    setIsReadOnly(sub && readOnly)
    setContainerIdentifiers(ids)
    setIsAuthenticated(true)
    void loadFeatures()
  }

  useEffect(() => {
    // 刷新后：Cookie 仍在（HttpOnly），直接向服务端确认会话有效性并由其返回
    // 权威的角色/只读态/绑定容器；不再依赖 localStorage 中的令牌。
    const savedUsername = localStorage.getItem('eyvescloud_username')
    checkAuth()
      .then((res) => {
        const data = (res.data as { data?: CheckAuthData }).data
        if (data) {
          setUsername(data.username || savedUsername || null)
          setIsSubUser(!!data.sub_user)
          setIsReadOnly(!!data.sub_user && (data.role || '') === 'viewer')
          setAdminRole(data.admin_role || '')
          setContainerIdentifiers(Array.isArray(data.container_uuids) ? data.container_uuids : [])
        } else {
          setUsername(savedUsername || null)
        }
        setIsAuthenticated(true)
        void loadFeatures()
      })
      .catch(() => {
        setAuthToken(null)
        localStorage.removeItem('eyvescloud_username')
        setToken(null)
        setUsername(null)
        setIsSubUser(false)
        setIsReadOnly(false)
        setContainerIdentifiers([])
      })
      .finally(() => setIsLoading(false))
  }, [navigate])

  // 管理员入口：只认 /api/login，不尝试子用户，避免两个门户互相穿透。
  const adminLogin = async (user: string, password: string, turnstileToken?: string) => {
    const response = await apiLogin(user, password, undefined, turnstileToken)
    const data = response.data.data as LoginResponse
    saveAuth(data.token, data.username, false, [])
    setAdminRole('')
    navigate(adminUrl() || '/')
  }

  // 用户入口：只认 /api/sub-user/login（账号密码）。
  const userLogin = async (user: string, password: string, turnstileToken?: string) => {
    const res = await api.post('/sub-user/login', { username: user, password, turnstile_token: turnstileToken ?? '' })
    const data = res.data.data as { token: string; username: string; role?: string; container_uuids: string[] }
    saveAuth(data.token, data.username, true, data.container_uuids || [], data.role === 'viewer')
    navigate('/user')
  }

  const adminLoginWith2FA = async (user: string, password: string, code: string, turnstileToken?: string) => {
    const response = await apiLogin(user, password, code, turnstileToken)
    const data = response.data.data as LoginResponse
    saveAuth(data.token, data.username, false, [])
    setAdminRole('')
    navigate(adminUrl() || '/')
  }

  const accessCodeLogin = async (code: string, password: string, turnstileToken?: string) => {
    const res = await api.post('/sub-user/access', { code, password, turnstile_token: turnstileToken ?? '' })
    const data = res.data.data as { token: string; username: string; role?: string; container_uuids: string[] }
    saveAuth(data.token, data.username, true, data.container_uuids || [], data.role === 'viewer')
    navigate('/user')
  }

  const logout = () => {
    const wasSubUser = isSubUser
    // 清除服务端 HttpOnly Cookie（审计 H-6）并丢弃内存令牌。
    void logoutRequest()
    setAuthToken(null)
    localStorage.removeItem('eyvescloud_username')
    setToken(null)
    setUsername(null)
    setIsSubUser(false)
    setIsReadOnly(false) // 重置只读态，防止登出后残留 viewer 限制影响下一次登录
    setAdminRole('')
    setContainerIdentifiers([])
    setIsAuthenticated(false)
    // 子用户回用户登录页；管理员回（可自定义的）管理登录页。
    navigate(wasSubUser ? '/user/login' : (adminUrl('login') || '/user/login'))
  }

  return (
    <AuthContext.Provider value={{ isAuthenticated, isLoading, username, isSubUser, isAdmin: isAuthenticated && !isSubUser, adminRole, isReadOnly, containerIdentifiers, adminLogin, adminLoginWith2FA, userLogin, accessCodeLogin, logout, token, features, can }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const context = useContext(AuthContext)
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return context
}

type TokenPayload = {
  username?: string
  sub_user?: string
  role?: string
  container_uuids?: string[]
}

function decodeTokenPayload(token: string): TokenPayload | null {
  try {
    const payload = token.split('.')[1]
    if (!payload) return null
    const normalized = payload.replace(/-/g, '+').replace(/_/g, '/')
    const padded = normalized.padEnd(normalized.length + ((4 - (normalized.length % 4)) % 4), '=')
    const json = decodeURIComponent(
      atob(padded)
        .split('')
        .map((char) => `%${(`00${char.charCodeAt(0).toString(16)}`).slice(-2)}`)
        .join('')
    )
    return JSON.parse(json) as TokenPayload
  } catch {
    return null
  }
}

function subUserTargetPath(containerIdentifiers: string[]) {
  const firstContainer = containerIdentifiers[0]
  return firstContainer ? `/container/${encodeURIComponent(firstContainer)}` : '/containers'
}
