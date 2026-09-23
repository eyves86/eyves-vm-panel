import React, { createContext, useContext, useState, useEffect, ReactNode } from 'react'
import { useNavigate } from 'react-router'
import api, { login as apiLogin, checkAuth, LoginResponse } from '../services/api'

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
  adminLogin: (username: string, password: string) => Promise<void>
  adminLoginWith2FA: (username: string, password: string, code: string) => Promise<void>
  /** 用户入口登录：仅走 /api/sub-user/login（账号密码） */
  userLogin: (username: string, password: string) => Promise<void>
  accessCodeLogin: (code: string, password: string) => Promise<void>
  logout: () => void
  token: string | null
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
  const navigate = useNavigate()

  const saveAuth = (t: string, u: string, sub: boolean, ids: string[], readOnly = false) => {
    localStorage.setItem('eyvescloud_token', t)
    localStorage.setItem('eyvescloud_username', u)
    setToken(t)
    setUsername(u)
    setIsSubUser(sub)
    setIsReadOnly(sub && readOnly)
    setContainerIdentifiers(ids)
    setIsAuthenticated(true)
  }

  useEffect(() => {
    const savedToken = localStorage.getItem('eyvescloud_token')
    const savedUsername = localStorage.getItem('eyvescloud_username')
    if (savedToken) {
      setToken(savedToken)
      // 刷新时以服务端 /check-auth 为唯一权威重建角色/只读态/绑定容器，
      // 而非仅凭可能过期的 JWT claims，避免旧 role 导致 UI 状态失真。
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
            const payload = decodeTokenPayload(savedToken)
            setUsername(payload?.username || payload?.sub_user || savedUsername || null)
            setIsSubUser(!!payload?.sub_user)
            setIsReadOnly(!!payload?.sub_user && payload?.role === 'viewer')
            setAdminRole(payload?.sub_user ? '' : (payload?.role || ''))
            setContainerIdentifiers(Array.isArray(payload?.container_uuids) ? payload.container_uuids : [])
          }
          setIsAuthenticated(true)
        })
        .catch(() => {
          localStorage.removeItem('eyvescloud_token')
          localStorage.removeItem('eyvescloud_username')
          setToken(null)
          setUsername(null)
          setIsSubUser(false)
          setIsReadOnly(false)
          setContainerIdentifiers([])
        })
        .finally(() => setIsLoading(false))
    } else {
      setIsLoading(false)
    }
  }, [navigate])

  // 管理员入口：只认 /api/login，不尝试子用户，避免两个门户互相穿透。
  const adminLogin = async (user: string, password: string) => {
    const response = await apiLogin(user, password)
    const data = response.data.data as LoginResponse
    saveAuth(data.token, data.username, false, [])
    setAdminRole('')
    navigate('/')
  }

  // 用户入口：只认 /api/sub-user/login（账号密码）。
  const userLogin = async (user: string, password: string) => {
    const res = await api.post('/sub-user/login', { username: user, password })
    const data = res.data.data as { token: string; username: string; role?: string; container_uuids: string[] }
    saveAuth(data.token, data.username, true, data.container_uuids || [], data.role === 'viewer')
    navigate('/user')
  }

  const adminLoginWith2FA = async (user: string, password: string, code: string) => {
    const response = await apiLogin(user, password, code)
    const data = response.data.data as LoginResponse
    saveAuth(data.token, data.username, false, [])
    setAdminRole('')
    navigate('/')
  }

  const accessCodeLogin = async (code: string, password: string) => {
    const res = await api.post('/sub-user/access', { code, password })
    const data = res.data.data as { token: string; username: string; role?: string; container_uuids: string[] }
    saveAuth(data.token, data.username, true, data.container_uuids || [], data.role === 'viewer')
    navigate('/user')
  }

  const logout = () => {
    localStorage.removeItem('eyvescloud_token')
    localStorage.removeItem('eyvescloud_username')
    setToken(null)
    setUsername(null)
    setIsSubUser(false)
    setIsReadOnly(false) // 重置只读态，防止登出后残留 viewer 限制影响下一次登录
    setAdminRole('')
    setContainerIdentifiers([])
    setIsAuthenticated(false)
    navigate('/login')
  }

  return (
    <AuthContext.Provider value={{ isAuthenticated, isLoading, username, isSubUser, isAdmin: isAuthenticated && !isSubUser, adminRole, isReadOnly, containerIdentifiers, adminLogin, adminLoginWith2FA, userLogin, accessCodeLogin, logout, token }}>
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
