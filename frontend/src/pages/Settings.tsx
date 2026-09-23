import { Dispatch, SetStateAction, useCallback, useEffect, useState } from 'react'
import { Bell, Clock, Copy, Database, Download, Gauge, Globe, KeyRound, ListTodo, Lock, LogIn, Minus, Monitor, Plus, RefreshCw, Save, Shield, ShieldCheck, Smartphone, Terminal, Trash2, TrendingUp, Upload, UserCog } from 'lucide-react'
import {
  BackupRecord,
  changePassword,
  changeUsername,
  createBackup,
  disable2FA,
  enable2FA,
  get2FAStatus,
  getAuditSettings,
  getBackupList,
  getBackupSettings,
  getHealthDetail,
  getLoginLogs,
  getOvercommitSettings,
  getNotificationSettings,
  getPanelAccessPolicy,
  getRateLimitSettings,
  getSSLSettings,
  getTaskQueueSettings,
  getWebSSHOriginSettings,
  HealthDetail,
  regenerate2FABackupCodes,
  restoreBackup,
  setup2FA,
  TwoFAStatus,
  updateAuditSettings,
  updateBackupSettings,
  updateRateLimitSettings,
  LoginLog,
  NotificationSettings,
  OvercommitSettings,
  PanelAccessPolicy,
  SSLSettings,
  TaskQueueSettings,
  testNotification,
  updateNotificationSettings,
  updateOvercommitSettings,
  updateTaskQueueSettings,
  updateSSLSettings,
  updatePanelAccessPolicy,
  updateWebSSHOriginSettings,
  WebSSHOriginSettings,
} from '../services/api'
import { useDialog } from '../components/Dialog'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'

type SettingsSection = 'tasks' | 'account' | 'security' | 'audit' | 'backup' | 'ratelimit' | 'access' | 'webssh' | 'ssl' | 'logs' | 'notify' | 'overcommit'

const settingsSections = [
  { id: 'tasks', label: '任务队列', icon: ListTodo },
  { id: 'account', label: '账号设置', icon: UserCog },
  { id: 'security', label: '两步验证', icon: Smartphone },
  { id: 'audit', label: '审计合规', icon: Clock },
  { id: 'backup', label: '容灾备份', icon: Database },
  { id: 'ratelimit', label: 'API 限流', icon: Gauge },
  { id: 'access', label: '访问来源', icon: Shield },
  { id: 'webssh', label: 'WebSSH 访问', icon: Terminal },
  { id: 'ssl', label: 'SSL 证书', icon: ShieldCheck },
  { id: 'notify', label: '告警推送', icon: Bell },
  { id: 'overcommit', label: '资源超售', icon: TrendingUp },
  { id: 'logs', label: '登录日志', icon: LogIn },
] as const

export default function Settings() {
  const dialog = useDialog()
  const { username } = useAuth()
  const { t } = useLanguage()
  const [logs, setLogs] = useState<LoginLog[]>([])
  const [loading, setLoading] = useState(true)
  const [logPage, setLogPage] = useState(1)
  const pageSize = 10

  const [oldPwd, setOldPwd] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [newUsername, setNewUsername] = useState('')

  const [ssl, setSSL] = useState<SSLSettings | null>(null)
  const [sslEnabled, setSSLEnabled] = useState(false)
  const [sslMode, setSSLMode] = useState<SSLSettings['mode']>('disabled')
  const [sslTarget, setSSLTarget] = useState('')
  const [sslEmail, setSSLEmail] = useState('')
  const [certPEM, setCertPEM] = useState('')
  const [keyPEM, setKeyPEM] = useState('')
  const [applyNow, setApplyNow] = useState(true)
  const [savingSSL, setSavingSSL] = useState(false)
  const [webSSHOrigins, setWebSSHOrigins] = useState<WebSSHOriginSettings | null>(null)
  const [webSSHOriginsText, setWebSSHOriginsText] = useState('')
  const [savingWebSSHOrigins, setSavingWebSSHOrigins] = useState(false)
  const [taskQueue, setTaskQueue] = useState<TaskQueueSettings | null>(null)
  const [taskConcurrency, setTaskConcurrency] = useState(2)
  const [savingTaskQueue, setSavingTaskQueue] = useState(false)
  const [accessPolicy, setAccessPolicy] = useState<PanelAccessPolicy | null>(null)
  const [accessEnabled, setAccessEnabled] = useState(false)
  const [allowedSourcesText, setAllowedSourcesText] = useState('')
  const [trustedProxiesText, setTrustedProxiesText] = useState('')
  const [savingAccessPolicy, setSavingAccessPolicy] = useState(false)
  const [activeSection, setActiveSection] = useState<SettingsSection>('tasks')

  // 两步验证 (TOTP)
  const [twoFA, setTwoFA] = useState<TwoFAStatus | null>(null)
  const [twoFASetup, setTwoFASetup] = useState<{ secret: string; otpauth_uri: string; qr_data_url?: string } | null>(null)
  const [twoFACode, setTwoFACode] = useState('')
  const [backupCodes, setBackupCodes] = useState<string[]>([])
  const [verifyCode, setVerifyCode] = useState('')
  const [disableCode, setDisableCode] = useState('')

  // 审计合规
  const [auditDays, setAuditDays] = useState(90)
  const [savingAudit, setSavingAudit] = useState(false)

  // 容灾备份
  const [backupEnabled, setBackupEnabled] = useState(false)
  const [backupInterval, setBackupInterval] = useState(24)
  const [backupKeep, setBackupKeep] = useState(14)
  const [backups, setBackups] = useState<BackupRecord[]>([])
  const [creatingBackup, setCreatingBackup] = useState(false)

  // API 限流
  const [rateLimitEnabled, setRateLimitEnabled] = useState(false)
  const [rateLimitPerMinute, setRateLimitPerMinute] = useState(120)
  const [savingRateLimit, setSavingRateLimit] = useState(false)

  // 健康详情
  const [health, setHealth] = useState<HealthDetail | null>(null)

  // 告警推送
  const [notify, setNotify] = useState<NotificationSettings | null>(null)
  const [notifyEnabled, setNotifyEnabled] = useState(false)
  const [notifyMinSeverity, setNotifyMinSeverity] = useState('medium')
  const [notifyWebhookURL, setNotifyWebhookURL] = useState('')
  const [smtpEnabled, setSMTPEnabled] = useState(false)
  const [smtpServer, setSMTPServer] = useState('')
  const [smtpPort, setSMTPPort] = useState(465)
  const [smtpUser, setSMTPUser] = useState('')
  const [smtpPassword, setSMTPPassword] = useState('')
  const [smtpFrom, setSMTPFrom] = useState('')
  const [smtpTo, setSMTPTo] = useState('')
  const [smtpPasswordSet, setSMTPPasswordSet] = useState(false)
  const [savingNotify, setSavingNotify] = useState(false)
  const [testingNotify, setTestingNotify] = useState(false)

  // 资源超售
  const [overcommit, setOvercommit] = useState<OvercommitSettings | null>(null)
  const [savingOvercommit, setSavingOvercommit] = useState(false)

  const fetchLogs = useCallback(async () => {
    try {
      const res = await getLoginLogs()
      if (res.data.data) setLogs(res.data.data)
    } catch (err) {
      console.error(err)
    } finally {
      setLoading(false)
    }
  }, [])

  const fetchSSL = useCallback(async () => {
    try {
      const res = await getSSLSettings()
      const data = res.data.data
      if (!data) return
      setSSL(data)
      setSSLEnabled(data.enabled)
      setSSLMode(data.mode || 'disabled')
      setSSLTarget(data.target || data.detected_host || '')
      setSSLEmail(data.email || '')
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetchWebSSHOrigins = useCallback(async () => {
    try {
      const res = await getWebSSHOriginSettings()
      const data = res.data.data
      if (!data) return
      setWebSSHOrigins(data)
      setWebSSHOriginsText((data.origins || []).join('\n'))
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetchTaskQueue = useCallback(async () => {
    try {
      const res = await getTaskQueueSettings()
      const data = res.data.data
      if (!data) return
      setTaskQueue(data)
      setTaskConcurrency(data.concurrency)
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetchAccessPolicy = useCallback(async () => {
    try {
      const res = await getPanelAccessPolicy()
      const data = res.data.data
      if (!data) return
      setAccessPolicy(data)
      setAccessEnabled(data.enabled)
      setAllowedSourcesText((data.allowed_sources || []).join('\n'))
      setTrustedProxiesText((data.trusted_proxies || []).join('\n'))
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetchNotifications = useCallback(async () => {
    try {
      const res = await getNotificationSettings()
      const data = res.data.data
      if (!data) return
      setNotify(data)
      setNotifyEnabled(!!data.security_alerts_enabled)
      setNotifyMinSeverity(data.min_severity || 'medium')
      setNotifyWebhookURL(data.webhook_url || '')
      setSMTPEnabled(!!data.smtp_enabled)
      setSMTPServer(data.smtp_server || '')
      setSMTPPort(data.smtp_port || 465)
      setSMTPUser(data.smtp_user || '')
      setSMTPFrom(data.smtp_from || '')
      setSMTPTo(data.smtp_to || '')
      setSMTPPasswordSet(!!data.smtp_password_set)
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetch2FA = useCallback(async () => {
    try {
      const res = await get2FAStatus()
      const data = res.data.data
      if (data) setTwoFA(data)
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetchAudit = useCallback(async () => {
    try {
      const res = await getAuditSettings()
      const data = res.data.data
      if (data) setAuditDays(data.retention_days)
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetchBackup = useCallback(async () => {
    try {
      const res = await getBackupSettings()
      const data = res.data.data
      if (data) {
        setBackupEnabled(data.enabled)
        setBackupInterval(data.interval_hours)
        setBackupKeep(data.keep)
      }
    } catch (err) {
      console.error(err)
    }
    try {
      const res2 = await getBackupList()
      if (res2.data.data) setBackups(res2.data.data)
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetchRateLimit = useCallback(async () => {
    try {
      const res = await getRateLimitSettings()
      const data = res.data.data
      if (data) {
        setRateLimitEnabled(data.enabled)
        setRateLimitPerMinute(data.per_minute)
      }
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetchHealth = useCallback(async () => {
    try {
      const res = await getHealthDetail()
      if (res.data.data) setHealth(res.data.data)
    } catch (err) {
      console.error(err)
    }
  }, [])

  const fetchOvercommit = useCallback(async () => {
    try {
      const res = await getOvercommitSettings()
      const data = res.data.data
      if (data) setOvercommit(data)
      else setOvercommit(null)
    } catch (err) {
      console.error(err)
      setOvercommit(null)
    }
  }, [])

  useEffect(() => {
    fetchLogs()
    fetchSSL()
    fetchWebSSHOrigins()
    fetchTaskQueue()
    fetchAccessPolicy()
    fetchNotifications()
    fetch2FA()
    fetchAudit()
    fetchBackup()
    fetchRateLimit()
    fetchHealth()
    fetchOvercommit()
    const logTimer = setInterval(fetchLogs, 15000)
    const taskTimer = setInterval(fetchTaskQueue, 5000)
    return () => {
      clearInterval(logTimer)
      clearInterval(taskTimer)
    }
  }, [fetch2FA, fetchAccessPolicy, fetchAudit, fetchBackup, fetchHealth, fetchLogs, fetchNotifications, fetchOvercommit, fetchRateLimit, fetchSSL, fetchTaskQueue, fetchWebSSHOrigins])

  const handleSaveTaskQueue = async () => {
    const concurrency = Math.max(1, Math.min(16, Math.round(taskConcurrency || 1)))
    setSavingTaskQueue(true)
    try {
      const res = await updateTaskQueueSettings(concurrency)
      const data = res.data.data
      if (data) {
        setTaskQueue(data)
        setTaskConcurrency(data.concurrency)
      }
      dialog.alert('完成', '任务队列并发设置已保存并立即生效')
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('失败', e.response?.data?.message || '任务队列设置保存失败')
    } finally {
      setSavingTaskQueue(false)
    }
  }

  const handleSSLModeChange = (mode: SSLSettings['mode']) => {
    setSSLMode(mode)
    const saved = ssl?.mode_certificates?.[mode]
    setSSLTarget(saved?.target || ssl?.detected_host || sslTarget)
    setSSLEmail(saved?.email || '')
  }

  const handleSaveSSL = async () => {
    setSavingSSL(true)
    try {
      const enabled = sslEnabled && sslMode !== 'disabled'
      const res = await updateSSLSettings({
        enabled,
        mode: enabled ? sslMode : 'disabled',
        target: sslTarget,
        email: sslEmail,
        cert_pem: certPEM,
        key_pem: keyPEM,
        apply_now: applyNow,
      })
      if (res.data.data) {
        setSSL(res.data.data)
        setCertPEM('')
        setKeyPEM('')
      }
      dialog.alert('完成', applyNow ? 'SSL 设置已保存，服务正在重启。稍后请用新的协议重新打开面板。' : 'SSL 设置已保存，重启 eyvescloud 服务后生效。')
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('失败', e.response?.data?.message || 'SSL 设置保存失败')
    } finally {
      setSavingSSL(false)
    }
  }

  const handleSaveWebSSHOrigins = async () => {
    setSavingWebSSHOrigins(true)
    try {
      const origins = webSSHOriginsText.split(/\r?\n/).map(item => item.trim()).filter(Boolean)
      const res = await updateWebSSHOriginSettings(origins)
      const data = res.data.data
      if (data) {
        setWebSSHOrigins(data)
        setWebSSHOriginsText((data.origins || []).join('\n'))
      }
      dialog.alert('完成', 'Origin 白名单已保存')
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('失败', e.response?.data?.message || 'Origin 白名单保存失败')
    } finally {
      setSavingWebSSHOrigins(false)
    }
  }

  const handleAccessEnabledChange = (enabled: boolean) => {
    setAccessEnabled(enabled)
    if (enabled && !allowedSourcesText.trim() && accessPolicy?.current_source) {
      setAllowedSourcesText(accessPolicy.current_source)
    }
  }

  const handleSaveAccessPolicy = async () => {
    const splitEntries = (value: string) => value.split(/[\s,;]+/).map(item => item.trim()).filter(Boolean)
    setSavingAccessPolicy(true)
    try {
      const res = await updatePanelAccessPolicy({
        enabled: accessEnabled,
        allowed_sources: splitEntries(allowedSourcesText),
        trusted_proxies: splitEntries(trustedProxiesText),
      })
      const data = res.data.data
      if (data) {
        setAccessPolicy(data)
        setAccessEnabled(data.enabled)
        setAllowedSourcesText((data.allowed_sources || []).join('\n'))
        setTrustedProxiesText((data.trusted_proxies || []).join('\n'))
      }
      dialog.alert('完成', accessEnabled ? '面板访问来源策略已保存并立即生效' : '面板访问来源限制已关闭')
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('失败', e.response?.data?.message || '面板访问来源策略保存失败')
    } finally {
      setSavingAccessPolicy(false)
    }
  }

  const handleSaveNotifications = async () => {
    if (notifyEnabled && !notifyWebhookURL.trim() && !smtpEnabled) {
      dialog.alert('提示', '请至少配置 Webhook 地址或启用邮件推送')
      return
    }
    setSavingNotify(true)
    try {
      const res = await updateNotificationSettings({
        security_alerts_enabled: notifyEnabled,
        min_severity: notifyMinSeverity,
        webhook_url: notifyWebhookURL.trim(),
        smtp_enabled: smtpEnabled,
        smtp_server: smtpServer.trim(),
        smtp_port: Number(smtpPort) || 465,
        smtp_user: smtpUser.trim(),
        smtp_from: smtpFrom.trim(),
        smtp_to: smtpTo.trim(),
        smtp_password: smtpPassword,
      })
      if (res.data.data) {
        setNotify(res.data.data)
        setSMTPPasswordSet(!!res.data.data.smtp_password_set)
        setSMTPPassword('')
      }
      dialog.alert('完成', '告警推送设置已保存')
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('失败', e.response?.data?.message || '告警推送设置保存失败')
    } finally {
      setSavingNotify(false)
    }
  }

  const handleTestNotification = async () => {
    setTestingNotify(true)
    try {
      const res = await testNotification()
      dialog.alert(res.data.success ? '完成' : '失败', res.data.message || '测试告警已发送')
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('失败', e.response?.data?.message || '测试告警发送失败')
    } finally {
      setTestingNotify(false)
    }
  }

  const handleSaveAccount = async () => {
    if (!oldPwd) {
      dialog.alert('提示', '请输入当前密码以确认修改')
      return
    }
    if (!newPwd && !newUsername) {
      dialog.alert('提示', '至少填写新密码或新用户名中的一项')
      return
    }
    if (newPwd && newPwd.length < 6) {
      dialog.alert('提示', '新密码至少 6 位')
      return
    }
    if (newUsername && newUsername.length < 3) {
      dialog.alert('提示', '用户名至少 3 位')
      return
    }

    const results: string[] = []
    try {
      if (newUsername) {
        const res = await changeUsername(newUsername, oldPwd)
        results.push(res.data.success ? '用户名已修改' : '用户名修改失败')
      }
      if (newPwd) {
        const res = await changePassword(oldPwd, newPwd)
        results.push(res.data.success ? '密码已修改' : '密码修改失败')
      }
      if (results.length > 0) {
        dialog.alert('完成', `${results.join('，')}。下次登录生效`)
        setOldPwd('')
        setNewPwd('')
        setNewUsername('')
      }
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('失败', e.response?.data?.message || '修改失败')
    }
  }

  const apiError = (err: unknown) => {
    const e = err as { response?: { data?: { message?: string } } }
    return e.response?.data?.message || '操作失败'
  }

  const handle2FASetup = async () => {
    setTwoFASetup(null)
    setBackupCodes([])
    try {
      const res = await setup2FA()
      setTwoFASetup(res.data.data ?? null)
      dialog.alert('开始设置', '请在身份验证器中扫描或手动输入密钥，然后输入 6 位动态口令完成启用。')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    }
  }

  const handle2FAEnable = async () => {
    if (!verifyCode.trim()) {
      dialog.alert('提示', '请输入身份验证器中的 6 位动态口令')
      return
    }
    try {
      const res = await enable2FA(verifyCode)
      setTwoFA({ enabled: true, has_secret: true })
      setBackupCodes(res.data.data?.backup_codes || [])
      setTwoFASetup(null)
      setVerifyCode('')
      dialog.alert('已启用', '两步验证已启用，请务必保存下方的一次性备份码。')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    }
  }

  const handle2FARegenerate = async () => {
    if (!twoFACode.trim()) {
      dialog.alert('提示', '请输入当前动态口令以确认')
      return
    }
    try {
      const res = await regenerate2FABackupCodes(twoFACode)
      setBackupCodes(res.data.data?.backup_codes || [])
      setTwoFACode('')
      dialog.alert('完成', '已生成一批新的备份码，请妥善保存。')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    }
  }

  const handle2FADisable = async () => {
    if (!disableCode.trim()) {
      dialog.alert('提示', '请输入动态口令或一次性备份码以确认关闭')
      return
    }
    try {
      await disable2FA(disableCode)
      setTwoFA({ enabled: false, has_secret: false })
      setDisableCode('')
      setBackupCodes([])
      dialog.alert('已关闭', '两步验证已关闭。')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    }
  }

  const handleSaveAudit = async () => {
    const days = Math.max(0, Math.min(3650, Math.round(auditDays || 0)))
    setSavingAudit(true)
    try {
      await updateAuditSettings(days)
      setAuditDays(days)
      dialog.alert('完成', '审计保留期已保存，超期日志将被后台定时清理')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    } finally {
      setSavingAudit(false)
    }
  }

  const handleSaveBackup = async () => {
    try {
      await updateBackupSettings({
        enabled: backupEnabled,
        interval_hours: Math.max(1, Math.round(backupInterval || 24)),
        keep: Math.max(1, Math.round(backupKeep || 14)),
      })
      dialog.alert('完成', backupEnabled ? '自动备份已启用' : '自动备份已关闭')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    }
  }

  const handleCreateBackup = async () => {
    setCreatingBackup(true)
    try {
      const res = await createBackup()
      if (res.data.data) setBackups((items) => [res.data.data as BackupRecord, ...items])
      dialog.alert('完成', '配置备份已创建')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    } finally {
      setCreatingBackup(false)
    }
  }

  const handleRestoreBackup = async (filename: string) => {
    const ok = window.confirm('还原会以备份内容覆盖当前全部配置，且为破坏性操作。确认继续？')
    if (!ok) return
    try {
      const res = await restoreBackup(filename, true)
      dialog.alert(res.data.success ? '完成' : '失败', res.data.message || '配置已还原')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    }
  }

  const handleSaveRateLimit = async () => {
    setSavingRateLimit(true)
    try {
      await updateRateLimitSettings(rateLimitEnabled, Math.max(1, Math.round(rateLimitPerMinute || 1)))
      dialog.alert('完成', 'API 限流设置已保存')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    } finally {
      setSavingRateLimit(false)
    }
  }

  const handleSaveOvercommit = async (payload: {
    memory_overcommit_enabled: boolean
    memory_overcommit_ratio: number
    nat_subnet_oversubscription: boolean
    disk_overcommit_ratio: number
    ksm_tuning: { enabled: boolean; pages_to_scan: number; sleep_millisecs: number; use_tune_ksm: boolean }
  }) => {
    setSavingOvercommit(true)
    try {
      const res = await updateOvercommitSettings(payload)
      const data = res.data.data
      if (data) setOvercommit(data)
      dialog.alert('完成', '资源超售设置已保存')
    } catch (err: unknown) {
      dialog.alert('失败', apiError(err))
    } finally {
      setSavingOvercommit(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-black"></div>
      </div>
    )
  }

  const totalPages = Math.ceil(logs.length / pageSize)

  return (
    <div className="space-y-5">
      <div>
        <h1 className="text-2xl font-bold text-black dark:text-white">面板设置</h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">任务队列、账号、访问控制、安全证书与访问记录</p>
      </div>

      <div className="grid items-start gap-4 lg:grid-cols-[210px_minmax(0,1fr)]">
        <aside className="overflow-x-auto rounded-lg border border-gray-200 bg-white p-2 dark:border-gray-700 dark:bg-gray-900 lg:sticky lg:top-4">
          <nav className="flex min-w-max gap-1 lg:min-w-0 lg:flex-col" aria-label="设置分类">
            {settingsSections.map((section) => {
              const Icon = section.icon
              const active = activeSection === section.id
              return (
                <button
                  key={section.id}
                  type="button"
                  onClick={() => setActiveSection(section.id)}
                  className={`flex items-center gap-2 rounded-md px-3 py-2.5 text-left text-sm font-medium transition-colors ${active ? 'bg-black text-white dark:bg-white dark:text-black' : 'text-gray-600 hover:bg-gray-100 hover:text-black dark:text-gray-300 dark:hover:bg-gray-800 dark:hover:text-white'}`}
                >
                  <Icon className="h-4 w-4 flex-shrink-0" />
                  <span>{t(section.label)}</span>
                </button>
              )
            })}
          </nav>
        </aside>

        <section className="min-w-0">
          {activeSection === 'tasks' && (
            <TaskQueueCard
              settings={taskQueue}
              concurrency={taskConcurrency}
              saving={savingTaskQueue}
              onConcurrencyChange={setTaskConcurrency}
              onRefresh={fetchTaskQueue}
              onSave={handleSaveTaskQueue}
            />
          )}

          {activeSection === 'account' && (
            <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
              <h2 className="mb-4 flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
                <UserCog className="h-4 w-4" />账号设置
              </h2>
              <div className="grid gap-4 md:grid-cols-2">
                <div>
                  <label className="mb-1 block text-xs text-gray-500">当前用户名</label>
                  <input type="text" value={username || ''} disabled className="w-full rounded-md border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-gray-400 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-500" />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-gray-500">新用户名，留空则不修改</label>
                  <input type="text" value={newUsername} onChange={(e) => setNewUsername(e.target.value)} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" placeholder="至少 3 位" />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-gray-500">新密码，留空则不修改</label>
                  <input type="password" value={newPwd} onChange={(e) => setNewPwd(e.target.value)} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" placeholder="至少 6 位" />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-gray-500">当前密码，验证身份</label>
                  <input type="password" value={oldPwd} onChange={(e) => setOldPwd(e.target.value)} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" placeholder="输入当前密码以确认修改" />
                </div>
              </div>
              <div className="mt-4 flex justify-end">
                <button onClick={handleSaveAccount} className="rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 dark:bg-white dark:text-black dark:hover:bg-gray-200">保存修改</button>
              </div>
            </div>
          )}

          {activeSection === 'security' && (
            <TwoFactorCard
              twoFA={twoFA}
              twoFASetup={twoFASetup}
              verifyCode={verifyCode}
              twoFACode={twoFACode}
              disableCode={disableCode}
              backupCodes={backupCodes}
              onSetVerifyCode={setVerifyCode}
              onSetTwoFACode={setTwoFACode}
              onSetDisableCode={setDisableCode}
              onRefresh={fetch2FA}
              onSetup={handle2FASetup}
              onEnable={handle2FAEnable}
              onRegenerate={handle2FARegenerate}
              onDisable={handle2FADisable}
            />
          )}

          {activeSection === 'audit' && (
            <AuditComplianceCard
              auditDays={auditDays}
              saving={savingAudit}
              onDaysChange={setAuditDays}
              onSave={handleSaveAudit}
            />
          )}

          {activeSection === 'backup' && (
            <BackupCard
              enabled={backupEnabled}
              interval={backupInterval}
              keep={backupKeep}
              backups={backups}
              creating={creatingBackup}
              onEnabledChange={setBackupEnabled}
              onIntervalChange={setBackupInterval}
              onKeepChange={setBackupKeep}
              onRefresh={fetchBackup}
              onSave={handleSaveBackup}
              onCreate={handleCreateBackup}
              onRestore={handleRestoreBackup}
            />
          )}

          {activeSection === 'ratelimit' && (
            <RateLimitCard
              enabled={rateLimitEnabled}
              perMinute={rateLimitPerMinute}
              health={health}
              saving={savingRateLimit}
              onEnabledChange={setRateLimitEnabled}
              onPerMinuteChange={setRateLimitPerMinute}
              onSave={handleSaveRateLimit}
              onRefreshHealth={fetchHealth}
            />
          )}

          {activeSection === 'webssh' && (
            <WebSSHOriginCard
              settings={webSSHOrigins}
              originsText={webSSHOriginsText}
              saving={savingWebSSHOrigins}
              onOriginsTextChange={setWebSSHOriginsText}
              onRefresh={fetchWebSSHOrigins}
              onSave={handleSaveWebSSHOrigins}
            />
          )}

          {activeSection === 'access' && (
            <PanelAccessPolicyCard
              policy={accessPolicy}
              enabled={accessEnabled}
              allowedSourcesText={allowedSourcesText}
              trustedProxiesText={trustedProxiesText}
              saving={savingAccessPolicy}
              onEnabledChange={handleAccessEnabledChange}
              onAllowedSourcesTextChange={setAllowedSourcesText}
              onTrustedProxiesTextChange={setTrustedProxiesText}
              onRefresh={fetchAccessPolicy}
              onSave={handleSaveAccessPolicy}
            />
          )}

          {activeSection === 'ssl' && (
            <SSLCard
              ssl={ssl}
              sslEnabled={sslEnabled}
              sslMode={sslMode}
              sslTarget={sslTarget}
              sslEmail={sslEmail}
              certPEM={certPEM}
              keyPEM={keyPEM}
              applyNow={applyNow}
              savingSSL={savingSSL}
              onRefresh={fetchSSL}
              onEnabledChange={setSSLEnabled}
              onModeChange={handleSSLModeChange}
              onTargetChange={setSSLTarget}
              onEmailChange={setSSLEmail}
              onCertChange={setCertPEM}
              onKeyChange={setKeyPEM}
              onApplyNowChange={setApplyNow}
              onSave={handleSaveSSL}
            />
          )}

          {activeSection === 'logs' && (
            <LoginLogCard logs={logs} logPage={logPage} pageSize={pageSize} totalPages={totalPages} setLogPage={setLogPage} />
          )}

          {activeSection === 'notify' && (
            <NotificationCard
              enabled={notifyEnabled}
              minSeverity={notifyMinSeverity}
              webhookURL={notifyWebhookURL}
              smtpEnabled={smtpEnabled}
              smtpServer={smtpServer}
              smtpPort={smtpPort}
              smtpUser={smtpUser}
              smtpPassword={smtpPassword}
              smtpFrom={smtpFrom}
              smtpTo={smtpTo}
              smtpPasswordSet={smtpPasswordSet}
              saving={savingNotify}
              testing={testingNotify}
              onEnabledChange={setNotifyEnabled}
              onMinSeverityChange={setNotifyMinSeverity}
              onWebhookURLChange={setNotifyWebhookURL}
              onSMTPEnabledChange={setSMTPEnabled}
              onSMTPServerChange={setSMTPServer}
              onSMTPPortChange={setSMTPPort}
              onSMTPUserChange={setSMTPUser}
              onSMTPPasswordChange={setSMTPPassword}
              onSMTPFromChange={setSMTPFrom}
              onSMTPToChange={setSMTPTo}
              onSave={handleSaveNotifications}
              onTest={handleTestNotification}
            />
          )}

          {activeSection === 'overcommit' && (
            <OvercommitCard
              settings={overcommit}
              saving={savingOvercommit}
              onSave={handleSaveOvercommit}
            />
          )}
        </section>
      </div>
    </div>
  )
}

interface TaskQueueCardProps {
  settings: TaskQueueSettings | null
  concurrency: number
  saving: boolean
  onConcurrencyChange: (value: number) => void
  onRefresh: () => void
  onSave: () => void
}

interface PanelAccessPolicyCardProps {
  policy: PanelAccessPolicy | null
  enabled: boolean
  allowedSourcesText: string
  trustedProxiesText: string
  saving: boolean
  onEnabledChange: (enabled: boolean) => void
  onAllowedSourcesTextChange: (value: string) => void
  onTrustedProxiesTextChange: (value: string) => void
  onRefresh: () => void
  onSave: () => void
}

function PanelAccessPolicyCard(props: PanelAccessPolicyCardProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
            <Shield className="h-4 w-4" />访问来源策略
          </h2>
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">限制可访问面板、登录和 API 的来源地址</p>
        </div>
        <button type="button" onClick={props.onRefresh} className="rounded-md border border-gray-200 p-1.5 text-gray-500 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-400 dark:hover:bg-gray-800" title="刷新">
          <RefreshCw className="h-4 w-4" />
        </button>
      </div>

      <div className="flex items-center justify-between gap-4 border-y border-gray-100 py-3 dark:border-gray-800">
        <div>
          <div className="text-sm font-medium text-gray-800 dark:text-gray-200">启用访问白名单</div>
          <div className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">关闭后不限制访问来源</div>
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={props.enabled}
          onClick={() => props.onEnabledChange(!props.enabled)}
          className={`access-policy-switch relative inline-flex h-6 w-11 flex-shrink-0 items-center rounded-full border transition-colors focus:outline-none focus:ring-2 focus:ring-black focus:ring-offset-2 dark:focus:ring-white dark:focus:ring-offset-gray-900 ${
            props.enabled
              ? 'border-black bg-black dark:border-white dark:bg-white'
              : 'border-gray-300 bg-gray-300 dark:border-gray-600 dark:bg-gray-700'
          }`}
          title={props.enabled ? '关闭访问白名单' : '启用访问白名单'}
        >
          <span
            aria-hidden="true"
            className={`access-policy-switch-thumb pointer-events-none absolute left-0.5 top-0.5 h-5 w-5 rounded-full shadow-sm ring-1 ring-black/5 transition-[transform,background-color] duration-200 ${
              props.enabled
                ? 'translate-x-5 bg-white dark:bg-gray-900'
                : 'translate-x-0 bg-white dark:bg-gray-200'
            }`}
          />
        </button>
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <div>
          <label className="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">允许的 IP / CIDR</label>
          <textarea
            value={props.allowedSourcesText}
            onChange={(event) => props.onAllowedSourcesTextChange(event.target.value)}
            rows={6}
            disabled={!props.enabled}
            className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-black outline-none focus:border-black focus:ring-1 focus:ring-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:focus:border-white dark:focus:ring-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500"
            placeholder={'203.0.113.10\n192.168.1.0/24\n2001:db8::/32'}
          />
        </div>
        <div>
          <label className="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-300">可信代理 IP / CIDR</label>
          <textarea
            value={props.trustedProxiesText}
            onChange={(event) => props.onTrustedProxiesTextChange(event.target.value)}
            rows={6}
            disabled={!props.enabled}
            className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-black outline-none focus:border-black focus:ring-1 focus:ring-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:focus:border-white dark:focus:ring-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500"
            placeholder={'127.0.0.1\n10.0.0.0/8'}
          />
          <p className="mt-1.5 text-xs text-gray-500 dark:text-gray-400">仅可信代理可提供真实客户端地址；未使用反向代理时留空</p>
        </div>
      </div>

      <div className="mt-4 grid gap-2 rounded-md border border-gray-100 bg-gray-50 p-3 text-xs dark:border-gray-800 dark:bg-gray-950 sm:grid-cols-2">
        <div>
          <span className="text-gray-500 dark:text-gray-400">当前识别来源</span>
          <div className="mt-0.5 break-all font-mono text-gray-800 dark:text-gray-200">{props.policy?.current_source || '-'}</div>
        </div>
        <div>
          <span className="text-gray-500 dark:text-gray-400">直接连接来源</span>
          <div className="mt-0.5 break-all font-mono text-gray-800 dark:text-gray-200">{props.policy?.direct_source || '-'}</div>
        </div>
      </div>

      <div className="mt-4 flex justify-end">
        <button type="button" onClick={props.onSave} disabled={props.saving} className="inline-flex items-center justify-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black dark:hover:bg-gray-200">
          <Save className="h-4 w-4" />
          {props.saving ? '保存中...' : '保存访问策略'}
        </button>
      </div>
    </div>
  )
}

function TaskQueueCard(props: TaskQueueCardProps) {
  const setBounded = (value: number) => props.onConcurrencyChange(Math.max(1, Math.min(16, value)))
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4">
      <div className="mb-4 flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-black">
          <ListTodo className="h-4 w-4" />任务队列
        </h2>
        <button onClick={props.onRefresh} className="rounded-md border border-gray-200 p-1.5 text-gray-500 hover:bg-gray-50" title="刷新">
          <RefreshCw className="h-4 w-4" />
        </button>
      </div>
      <div className="grid grid-cols-2 divide-x divide-gray-200 border-y border-gray-100 bg-gray-50">
        <div className="px-3 py-2">
          <div className="text-[11px] text-gray-500">运行中</div>
          <div className="mt-0.5 text-lg font-semibold text-gray-900">{props.settings?.active ?? 0}</div>
        </div>
        <div className="px-3 py-2">
          <div className="text-[11px] text-gray-500">等待中</div>
          <div className="mt-0.5 text-lg font-semibold text-gray-900">{props.settings?.pending ?? 0}</div>
        </div>
      </div>
      <div className="mt-4">
        <label className="mb-1.5 block text-xs text-gray-500">总并发上限</label>
        <div className="flex h-9 items-stretch">
          <button type="button" onClick={() => setBounded(props.concurrency - 1)} disabled={props.concurrency <= 1} className="flex w-10 items-center justify-center rounded-l-md border border-gray-300 text-gray-600 hover:bg-gray-50 disabled:opacity-30" title="减少并发">
            <Minus className="h-4 w-4" />
          </button>
          <input
            type="number"
            min={1}
            max={16}
            value={props.concurrency}
            onChange={(event) => setBounded(Number(event.target.value) || 1)}
            className="min-w-0 flex-1 border-y border-gray-300 px-2 text-center text-sm font-medium text-black outline-none focus:ring-2 focus:ring-inset focus:ring-black"
          />
          <button type="button" onClick={() => setBounded(props.concurrency + 1)} disabled={props.concurrency >= 16} className="flex w-10 items-center justify-center rounded-r-md border border-gray-300 text-gray-600 hover:bg-gray-50 disabled:opacity-30" title="增加并发">
            <Plus className="h-4 w-4" />
          </button>
        </div>
      </div>
      <div className="mt-4 flex justify-end">
        <button onClick={props.onSave} disabled={props.saving} className="inline-flex items-center justify-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50">
          <Save className="h-4 w-4" />
          {props.saving ? '保存中...' : '保存队列设置'}
        </button>
      </div>
    </div>
  )
}

interface SSLCardProps {
  ssl: SSLSettings | null
  sslEnabled: boolean
  sslMode: SSLSettings['mode']
  sslTarget: string
  sslEmail: string
  certPEM: string
  keyPEM: string
  applyNow: boolean
  savingSSL: boolean
  onRefresh: () => void
  onEnabledChange: (enabled: boolean) => void
  onModeChange: (mode: SSLSettings['mode']) => void
  onTargetChange: (target: string) => void
  onEmailChange: (email: string) => void
  onCertChange: (cert: string) => void
  onKeyChange: (key: string) => void
  onApplyNowChange: (apply: boolean) => void
  onSave: () => void
}

interface AuditComplianceCardProps {
  auditDays: number
  saving: boolean
  onDaysChange: (value: number) => void
  onSave: () => void
}

function AuditComplianceCard(props: AuditComplianceCardProps) {
  const dialog = useDialog()
  const download = async (format: 'csv' | 'json') => {
    try {
      await downloadWithAuth(`/api/audit-logs/export?format=${format}`, `audit-logs.${format}`)
    } catch (err: unknown) {
      const e = err as { message?: string }
      dialog.alert('失败', e.message || '导出失败')
    }
  }
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
      <h2 className="mb-1 flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
        <Clock className="h-4 w-4" />审计合规
      </h2>
      <p className="mb-4 text-xs text-gray-500 dark:text-gray-400">控制审计/登录日志的保留周期，并可导出审计日志归档</p>

      <div className="grid gap-4 md:grid-cols-2">
        <div>
          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">保留天数（0 表示永久保留）</label>
          <input
            type="number"
            min={0}
            max={3650}
            value={props.auditDays}
            onChange={(e) => props.onDaysChange(Number(e.target.value))}
            className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
          />
        </div>
        <div className="flex items-end justify-end gap-2">
          <button onClick={() => download('csv')} className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">
            <Download className="h-4 w-4" />导出 CSV
          </button>
          <button onClick={() => download('json')} className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">
            <Download className="h-4 w-4" />导出 JSON
          </button>
        </div>
      </div>

      <div className="mt-4 flex justify-end">
        <button onClick={props.onSave} disabled={props.saving} className="inline-flex items-center justify-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black dark:hover:bg-gray-200">
          <Save className="h-4 w-4" />
          {props.saving ? '保存中...' : '保存保留期'}
        </button>
      </div>
    </div>
  )
}

interface BackupCardProps {
  enabled: boolean
  interval: number
  keep: number
  backups: BackupRecord[]
  creating: boolean
  onEnabledChange: (value: boolean) => void
  onIntervalChange: (value: number) => void
  onKeepChange: (value: number) => void
  onRefresh: () => void
  onSave: () => void
  onCreate: () => void
  onRestore: (filename: string) => void
}

function BackupCard(props: BackupCardProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
      <div className="mb-4 flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
          <Database className="h-4 w-4" />容灾备份（配置快照）
        </h2>
        <button type="button" onClick={props.onRefresh} className="rounded-md border border-gray-200 p-1.5 text-gray-500 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-400 dark:hover:bg-gray-800" title="刷新">
          <RefreshCw className="h-4 w-4" />
        </button>
      </div>

      <p className="mb-4 rounded-md border border-gray-100 bg-gray-50 p-3 text-xs text-gray-600 dark:border-gray-800 dark:bg-gray-950 dark:text-gray-300">
        备份会以 JSON 快照保存全部配置（容器/子用户/API Key/策略/租户等），可用于整机级配置还原。
      </p>

      <div className="flex items-center justify-between gap-4 border-y border-gray-100 py-3 dark:border-gray-800">
        <div>
          <div className="text-sm font-medium text-gray-800 dark:text-gray-200">启用自动备份</div>
          <div className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">按设定间隔自动创建配置快照</div>
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={props.enabled}
          onClick={() => props.onEnabledChange(!props.enabled)}
          className={`relative inline-flex h-6 w-11 flex-shrink-0 items-center rounded-full border transition-colors ${props.enabled ? 'border-black bg-black dark:border-white dark:bg-white' : 'border-gray-300 bg-gray-300 dark:border-gray-600 dark:bg-gray-700'}`}
        >
          <span className={`pointer-events-none absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow-sm transition-transform ${props.enabled ? 'translate-x-5 dark:bg-gray-900' : 'translate-x-0 dark:bg-gray-200'}`} />
        </button>
      </div>

      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <div>
          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">备份间隔（小时）</label>
          <input type="number" min={1} value={props.interval} onChange={(e) => props.onIntervalChange(Number(e.target.value))} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
        </div>
        <div>
          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">保留份数</label>
          <input type="number" min={1} value={props.keep} onChange={(e) => props.onKeepChange(Number(e.target.value))} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
        </div>
      </div>

      <div className="mt-4 flex items-center justify-between gap-3 border-t border-gray-100 pt-4 dark:border-gray-800">
        <button onClick={props.onSave} className="inline-flex items-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 dark:bg-white dark:text-black dark:hover:bg-gray-200">
          <Save className="h-4 w-4" />保存设置
        </button>
        <button onClick={props.onCreate} disabled={props.creating} className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800">
          <Plus className="h-4 w-4" />立即备份
        </button>
      </div>

      <div className="mt-4">
        <div className="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">备份记录（{props.backups.length}）</div>
        {props.backups.length === 0 ? (
          <p className="text-sm text-gray-400">暂无备份记录</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-gray-100 text-gray-400">
                  <th className="py-2 text-left font-medium">文件名</th>
                  <th className="py-2 text-left font-medium">创建时间</th>
                  <th className="py-2 text-left font-medium">大小</th>
                  <th className="py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-50">
                {props.backups.map((b) => (
                  <tr key={b.id}>
                    <td className="py-2 font-mono text-gray-700 dark:text-gray-200">{b.filename}</td>
                    <td className="py-2 text-gray-500">{b.created_at}</td>
                    <td className="py-2 text-gray-500">{(b.size_bytes / 1024).toFixed(1)} KB</td>
                    <td className="py-2 text-right">
                      <button
                        type="button"
                        onClick={() => downloadWithAuth(`/api/backup/download?file=${encodeURIComponent(b.filename)}`, b.filename).catch((e: { message?: string }) => alert(e.message || '下载失败'))}
                        className="mr-2 inline-flex items-center gap-1 text-gray-600 hover:text-black dark:text-gray-300"
                      >
                        <Download className="h-3.5 w-3.5" />下载
                      </button>
                      <button onClick={() => props.onRestore(b.filename)} className="inline-flex items-center gap-1 text-red-600 hover:underline">
                        <Trash2 className="h-3.5 w-3.5" />还原
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

interface RateLimitCardProps {
  enabled: boolean
  perMinute: number
  health: HealthDetail | null
  saving: boolean
  onEnabledChange: (value: boolean) => void
  onPerMinuteChange: (value: number) => void
  onSave: () => void
  onRefreshHealth: () => void
}

function RateLimitCard(props: RateLimitCardProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
            <Gauge className="h-4 w-4" />API 治理 · 限流
          </h2>
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">对版本化接口 /api/v1 按客户端 IP 限制每分钟请求次数</p>
        </div>
        <button type="button" onClick={props.onRefreshHealth} className="rounded-md border border-gray-200 p-1.5 text-gray-500 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-400 dark:hover:bg-gray-800" title="刷新健康详情">
          <RefreshCw className="h-4 w-4" />
        </button>
      </div>

      <div className="flex items-center justify-between gap-4 border-y border-gray-100 py-3 dark:border-gray-800">
        <div>
          <div className="text-sm font-medium text-gray-800 dark:text-gray-200">启用 API 限流</div>
          <div className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">超出配额返回 429</div>
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={props.enabled}
          onClick={() => props.onEnabledChange(!props.enabled)}
          className={`relative inline-flex h-6 w-11 flex-shrink-0 items-center rounded-full border transition-colors ${props.enabled ? 'border-black bg-black dark:border-white dark:bg-white' : 'border-gray-300 bg-gray-300 dark:border-gray-600 dark:bg-gray-700'}`}
        >
          <span className={`pointer-events-none absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow-sm transition-transform ${props.enabled ? 'translate-x-5 dark:bg-gray-900' : 'translate-x-0 dark:bg-gray-200'}`} />
        </button>
      </div>

      <div className="mt-4 grid gap-4 sm:grid-cols-2">
        <div>
          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">每分钟请求上限</label>
          <input type="number" min={1} value={props.perMinute} onChange={(e) => props.onPerMinuteChange(Number(e.target.value))} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white" />
        </div>
        <div>
          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">OpenAPI 契约</label>
          <a href="/api/v1/openapi.json" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800">
            <CodeIcon className="h-4 w-4" />查看 openapi.json
          </a>
        </div>
      </div>

      {props.health && (
        <div className="mt-4 grid gap-2 rounded-md border border-gray-100 bg-gray-50 p-3 text-xs dark:border-gray-800 dark:bg-gray-950 sm:grid-cols-3">
          <div><span className="text-gray-500 dark:text-gray-400">运行时长</span><div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{formatUptime(props.health.uptime)}</div></div>
          <div><span className="text-gray-500 dark:text-gray-400">Goroutines</span><div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{props.health.goroutines}</div></div>
          <div><span className="text-gray-500 dark:text-gray-400">内存</span><div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{props.health.memory_alloc_mb.toFixed(1)} MB</div></div>
          <div><span className="text-gray-500 dark:text-gray-400">容器数</span><div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{props.health.container_count}</div></div>
          <div><span className="text-gray-500 dark:text-gray-400">节点数</span><div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{props.health.node_count}</div></div>
          <div><span className="text-gray-500 dark:text-gray-400">活跃任务</span><div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{props.health.active_tasks}</div></div>
        </div>
      )}

      <div className="mt-4 flex justify-end">
        <button onClick={props.onSave} disabled={props.saving} className="inline-flex items-center justify-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black dark:hover:bg-gray-200">
          <Save className="h-4 w-4" />
          {props.saving ? '保存中...' : '保存限流设置'}
        </button>
      </div>
    </div>
  )
}

function CodeIcon({ className = '' }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <polyline points="16 18 22 12 16 6" />
      <polyline points="8 6 2 12 8 18" />
    </svg>
  )
}

// downloadWithAuth fetches a protected endpoint with the Bearer token and
// triggers a browser download. Plain <a href> navigation cannot carry the
// Authorization header, so downloads of protected resources must go through
// fetch() and a Blob object URL.
async function downloadWithAuth(url: string, fallbackName: string) {
  const token = localStorage.getItem('eyvescloud_token')
  const res = await fetch(url, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!res.ok) {
    throw new Error(`下载失败（HTTP ${res.status}）`)
  }
  const blob = await res.blob()
  const objURL = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = objURL
  a.download = fallbackName
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(objURL)
}

// copyText copies text with fallback and reports success.
async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      /* fall through */
    }
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

function formatUptime(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds || 0))
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}天 ${h}时 ${m}分`
  if (h > 0) return `${h}时 ${m}分`
  return `${m}分`
}

interface TwoFactorCardProps {
  twoFA: TwoFAStatus | null
  twoFASetup: { secret: string; otpauth_uri: string; qr_data_url?: string } | null
  verifyCode: string
  twoFACode: string
  disableCode: string
  backupCodes: string[]
  onSetVerifyCode: (value: string) => void
  onSetTwoFACode: (value: string) => void
  onSetDisableCode: (value: string) => void
  onRefresh: () => void
  onSetup: () => void
  onEnable: () => void
  onRegenerate: () => void
  onDisable: () => void
}

function TwoFactorCard(props: TwoFactorCardProps) {
  const dialog = useDialog()
  const enabled = !!props.twoFA?.enabled
  const hasSecret = !!props.twoFA?.has_secret

  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
            <Smartphone className="h-4 w-4" />两步验证（TOTP）
          </h2>
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">用于管理员登录的 One-Time Password，支持主流身份验证器 App</p>
        </div>
        <button type="button" onClick={props.onRefresh} className="rounded-md border border-gray-200 p-1.5 text-gray-500 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-400 dark:hover:bg-gray-800" title="刷新状态">
          <RefreshCw className="h-4 w-4" />
        </button>
      </div>

      <div className="flex items-center justify-between gap-4 rounded-md border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-gray-950">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium text-gray-800 dark:text-gray-200">
            <ShieldCheck className={`h-4 w-4 ${enabled ? 'text-green-600 dark:text-green-400' : 'text-gray-400'}`} />
            两步验证 {enabled ? '已启用' : '未启用'}
          </div>
          <div className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {enabled ? '登录时需额外输入 6 位动态口令或一次性备份码' : '启用后管理员登录将要求额外身份验证'}
          </div>
        </div>
      </div>

      {!enabled && !props.twoFASetup && (
        <div className="mt-4">
          <button type="button" onClick={props.onSetup} className="inline-flex items-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 dark:bg-white dark:text-black dark:hover:bg-gray-200">
            <KeyRound className="h-4 w-4" />开始设置
          </button>
        </div>
      )}

      {props.twoFASetup && !enabled && (
        <div className="mt-4 space-y-4">
          <div className="rounded-md border border-green-200 bg-green-50 p-3 text-xs text-green-800 dark:border-green-800 dark:bg-green-900/20 dark:text-green-300">
            用 <strong>Google Authenticator</strong>（或 Microsoft Authenticator、1Password 等兼容 App）扫描下方二维码，或手动输入密钥。
          </div>
          {props.twoFASetup.qr_data_url && (
            <div className="flex justify-center rounded-md border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-950">
              {/* eslint-disable-next-line jsx-a11y/alt-text */}
              <img
                src={props.twoFASetup.qr_data_url}
                alt="TOTP 二维码"
                className="h-48 w-48 rounded"
              />
            </div>
          )}
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">otpauth:// 链接（或二维码中内容）</label>
              <div className="flex items-center gap-2">
                <input
                  readOnly
                  value={props.twoFASetup.otpauth_uri}
                  className="w-full rounded-md border border-gray-300 bg-gray-50 px-3 py-2 font-mono text-xs text-gray-600 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-300"
                />
                <button
                  type="button"
                  onClick={() => { void copyText(props.twoFASetup?.otpauth_uri || '').then(ok => ok ? dialog.alert('已复制', '密钥链接已复制到剪贴板') : dialog.alert('复制失败', '无法访问剪贴板，请手动复制')) }}
                  className="rounded-md border border-gray-300 p-2 text-gray-500 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-400 dark:hover:bg-gray-800"
                  title="复制链接"
                >
                  <Copy className="h-4 w-4" />
                </button>
              </div>
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">手动输入密钥</label>
              <div className="flex items-center gap-2">
                <input
                  readOnly
                  value={props.twoFASetup.secret}
                  className="w-full rounded-md border border-gray-300 bg-gray-50 px-3 py-2 font-mono text-sm text-gray-700 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-200"
                />
                <button
                  type="button"
                  onClick={() => { void copyText(props.twoFASetup?.secret || '').then(ok => ok ? dialog.alert('已复制', '密钥已复制到剪贴板') : dialog.alert('复制失败', '无法访问剪贴板，请手动复制')) }}
                  className="rounded-md border border-gray-300 p-2 text-gray-500 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-400 dark:hover:bg-gray-800"
                  title="复制密钥"
                >
                  <Copy className="h-4 w-4" />
                </button>
              </div>
            </div>
          </div>
          <div>
            <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">输入当前 6 位动态口令以启用</label>
            <div className="flex items-center gap-2">
              <input
                type="text"
                inputMode="numeric"
                maxLength={6}
                value={props.verifyCode}
                onChange={(e) => props.onSetVerifyCode(e.target.value.replace(/\D/g, ''))}
                placeholder="123456"
                className="w-40 rounded-md border border-gray-300 bg-white px-3 py-2 text-center font-mono text-base tracking-widest text-black outline-none focus:border-black focus:ring-1 focus:ring-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
              />
              <button type="button" onClick={props.onEnable} className="inline-flex items-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 dark:bg-white dark:text-black dark:hover:bg-gray-200">
                <ShieldCheck className="h-4 w-4" />启用两步验证
              </button>
            </div>
          </div>
        </div>
      )}

      {enabled && (
        <div className="mt-4 space-y-4">
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">输入动态口令换发新备份码</label>
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  inputMode="numeric"
                  maxLength={6}
                  value={props.twoFACode}
                  onChange={(e) => props.onSetTwoFACode(e.target.value.replace(/\D/g, ''))}
                  placeholder="123456"
                  className="w-32 rounded-md border border-gray-300 bg-white px-3 py-2 text-center font-mono text-sm text-black outline-none focus:border-black focus:ring-1 focus:ring-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
                <button type="button" onClick={props.onRegenerate} className="rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800">
                  重新生成备份码
                </button>
              </div>
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">输入动态口令或备份码以关闭</label>
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  value={props.disableCode}
                  onChange={(e) => props.onSetDisableCode(e.target.value)}
                  placeholder="6 位动态码或备份码"
                  className="w-40 rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-red-500 focus:ring-1 focus:ring-red-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
                <button type="button" onClick={props.onDisable} className="rounded-md border border-red-300 px-3 py-2 text-sm text-red-600 hover:bg-red-50 dark:border-red-700 dark:text-red-400 dark:hover:bg-red-900/20">
                  关闭两步验证
                </button>
              </div>
            </div>
          </div>

          {props.backupCodes.length > 0 && (
            <div className="rounded-md border border-amber-200 bg-amber-50 p-4 dark:border-amber-800 dark:bg-amber-900/20">
              <div className="mb-2 flex items-center justify-between">
                <span className="text-xs font-medium text-amber-800 dark:text-amber-300">一次性备份码（请立即保存，仅显示一次）</span>
                <button
                  type="button"
                  onClick={() => { void copyText(props.backupCodes.join('\n')).then(ok => ok ? dialog.alert('已复制', '备份码已复制到剪贴板') : dialog.alert('复制失败', '无法访问剪贴板，请手动复制')) }}
                  className="rounded-md border border-amber-300 px-2 py-1 text-xs text-amber-800 hover:bg-amber-100 dark:border-amber-700 dark:text-amber-300 dark:hover:bg-amber-900/30"
                >
                  复制
                </button>
              </div>
              <div className="grid grid-cols-2 gap-1.5 font-mono text-xs text-amber-900 dark:text-amber-200 sm:grid-cols-4">
                {props.backupCodes.map((code) => (
                  <div key={code} className="rounded bg-amber-100/70 px-2 py-1 text-center dark:bg-amber-900/40">{code}</div>
                ))}
              </div>
            </div>
          )}

          {hasSecret && (
            <p className="text-xs text-gray-400 dark:text-gray-500">每个备份码仅可使用一次；换发或恢复备份码需要输入当前动态口令。</p>
          )}
        </div>
      )}
    </div>
  )
}

interface WebSSHOriginCardProps {
  settings: WebSSHOriginSettings | null
  originsText: string
  saving: boolean
  onOriginsTextChange: (value: string) => void
  onRefresh: () => void
  onSave: () => void
}

function WebSSHOriginCard(props: WebSSHOriginCardProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4">
      <div className="mb-4 flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-black">
          <Terminal className="h-4 w-4" />WebSSH Origin 白名单
        </h2>
        <button onClick={props.onRefresh} className="rounded-md border border-gray-200 p-1.5 text-gray-500 hover:bg-gray-50" title="刷新">
          <RefreshCw className="h-4 w-4" />
        </button>
      </div>
      <div className="space-y-3">
        <div>
          <label className="mb-1 block text-xs text-gray-500">允许的 Origin</label>
          <textarea
            value={props.originsText}
            onChange={(e) => props.onOriginsTextChange(e.target.value)}
            rows={4}
            className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-black"
          />
        </div>
        <div className="rounded-md border border-gray-100 bg-gray-50 p-3 text-xs text-gray-600">
          <div className="truncate font-mono" title={props.settings?.current_origin || ''}>当前面板来源：{props.settings?.current_origin || '-'}</div>
          <div className="mt-1">默认允许当前面板来源和本机回环来源；额外域名每行填写一个完整 Origin。</div>
        </div>
        <div className="flex justify-end">
          <button onClick={props.onSave} disabled={props.saving} className="inline-flex items-center justify-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50">
            <Upload className="h-4 w-4" />
            {props.saving ? '保存中...' : '保存 Origin 白名单'}
          </button>
        </div>
      </div>
    </div>
  )
}

function SSLCard(props: SSLCardProps) {
  const selectedSSL = props.ssl?.mode_certificates?.[props.sslMode]
  const modeOptions: Array<{ value: SSLSettings['mode']; label: string }> = [
    { value: 'letsencrypt', label: 'Let’s Encrypt' },
    { value: 'self_signed', label: '自签证书' },
    { value: 'uploaded', label: '上传证书' },
  ]

  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5">
      <div className="mb-4 flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-black">
          <ShieldCheck className="h-4 w-4" />SSL 证书
        </h2>
        <button onClick={props.onRefresh} className="rounded-md border border-gray-200 p-1.5 text-gray-500 hover:bg-gray-50" title="刷新">
          <RefreshCw className="h-4 w-4" />
        </button>
      </div>
      <div className="space-y-4">
        <label className="flex items-center gap-2 text-sm text-gray-700">
          <input type="checkbox" checked={props.sslEnabled} onChange={(e) => props.onEnabledChange(e.target.checked)} className="h-4 w-4 rounded border-gray-300" />
          启用 HTTPS / WSS
        </label>

        <div className="grid gap-2 sm:grid-cols-3">
          {modeOptions.map((option) => (
            <button
              key={option.value}
              onClick={() => props.onModeChange(option.value)}
              className={`rounded-md border px-3 py-2 text-sm ${props.sslMode === option.value ? 'border-black bg-black text-white' : 'border-gray-200 text-gray-700 hover:bg-gray-50'}`}
            >
              {option.label}
            </button>
          ))}
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-xs text-gray-500">IP / 域名</label>
            <input
              type="text"
              value={props.sslTarget}
              onChange={(e) => props.onTargetChange(e.target.value)}
              className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black"
              placeholder={props.ssl?.detected_host || '服务器公网 IP 或域名'}
            />
          </div>
          {props.sslMode === 'letsencrypt' && (
            <div>
              <label className="mb-1 block text-xs text-gray-500">邮箱，可选</label>
              <input type="email" value={props.sslEmail} onChange={(e) => props.onEmailChange(e.target.value)} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black" placeholder="admin@example.com" />
            </div>
          )}
        </div>

        {props.sslMode === 'letsencrypt' && (
          <div className="rounded-md border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800">
            纯 IP 证书需要服务器安装 Certbot 5.4+，且验证时 80 端口必须能被 Let’s Encrypt 访问。IP 证书是短有效期证书，certbot 需要保持自动续签。
          </div>
        )}

        {props.sslMode === 'self_signed' && (
          <div className="rounded-md border border-gray-100 bg-gray-50 p-3 text-xs text-gray-600">
            自签证书可以加密面板和 VNC，但浏览器会提示证书不受信任；证书快到期时系统会自动重新签发。
          </div>
        )}

        {props.sslMode === 'uploaded' && (
          <div className="grid gap-3 lg:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs text-gray-500">证书 PEM / fullchain.pem</label>
              <textarea value={props.certPEM} onChange={(e) => props.onCertChange(e.target.value)} rows={7} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-black" placeholder="-----BEGIN CERTIFICATE-----" />
            </div>
            <div>
              <label className="mb-1 block text-xs text-gray-500">私钥 PEM / privkey.pem</label>
              <textarea value={props.keyPEM} onChange={(e) => props.onKeyChange(e.target.value)} rows={7} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 font-mono text-xs text-black" placeholder="-----BEGIN PRIVATE KEY-----" />
            </div>
          </div>
        )}

        {selectedSSL?.certificate ? (
          <div className="rounded-md border border-gray-100 bg-gray-50 p-3 text-xs text-gray-600">
            <div className="flex items-center gap-2 text-gray-800">
              <Lock className="h-3.5 w-3.5" />
              当前证书：{selectedSSL.certificate.valid ? '有效' : '已过期或未生效'}
            </div>
            <div className="mt-1 font-mono">到期时间：{selectedSSL.certificate.not_after}</div>
            <div className="mt-1 truncate font-mono" title={selectedSSL.cert_path}>证书路径：{selectedSSL.cert_path || '-'}</div>
            {selectedSSL.last_error && <div className="mt-1 text-red-600">最近错误：{selectedSSL.last_error}</div>}
          </div>
        ) : (
          <div className="rounded-md border border-gray-100 bg-gray-50 p-3 text-xs text-gray-600">
            {props.sslMode === 'uploaded' ? '上传来源还没有保存证书，请粘贴证书和私钥后保存。' : '当前来源还没有保存证书，保存 SSL 设置时会自动生成或申请。'}
          </div>
        )}

        <label className="flex items-center gap-2 text-xs text-gray-500">
          <input type="checkbox" checked={props.applyNow} onChange={(e) => props.onApplyNowChange(e.target.checked)} className="h-4 w-4 rounded border-gray-300" />
          保存后自动重启服务并立即生效
        </label>

        <div className="flex justify-end">
          <button onClick={props.onSave} disabled={props.savingSSL} className="inline-flex items-center justify-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50">
            <Upload className="h-4 w-4" />
            {props.savingSSL ? '保存中...' : '保存 SSL 设置'}
          </button>
        </div>
      </div>
    </div>
  )
}

interface LoginLogCardProps {
  logs: LoginLog[]
  logPage: number
  pageSize: number
  totalPages: number
  setLogPage: Dispatch<SetStateAction<number>>
}

function LoginLogCard({ logs, logPage, pageSize, totalPages, setLogPage }: LoginLogCardProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5">
      <h2 className="mb-4 flex items-center gap-2 text-sm font-semibold text-black">
        <LogIn className="h-4 w-4" />登录日志
      </h2>
      {logs.length === 0 ? (
        <p className="text-sm text-gray-400">暂无登录记录</p>
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-gray-100 text-gray-400">
                  <th className="w-40 py-2 text-left font-medium"><span className="inline-flex items-center gap-1"><Clock className="h-3 w-3" />时间</span></th>
                  <th className="py-2 text-left font-medium">用户名</th>
                  <th className="py-2 text-left font-medium"><span className="inline-flex items-center gap-1"><Globe className="h-3 w-3" />IP</span></th>
                  <th className="py-2 text-left font-medium"><span className="inline-flex items-center gap-1"><Monitor className="h-3 w-3" />设备</span></th>
                  <th className="py-2 text-left font-medium">结果</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-50">
                {logs.slice((logPage - 1) * pageSize, logPage * pageSize).map((log, index) => (
                  <tr key={`${log.time}-${index}`}>
                    <td className="whitespace-nowrap py-1.5 font-mono text-gray-500">{log.time}</td>
                    <td className="py-1.5 text-gray-700">{log.username}</td>
                    <td className="py-1.5 font-mono text-gray-500">{log.ip}</td>
                    <td className="max-w-[180px] truncate py-1.5 text-gray-500" title={log.user_agent}>{formatUA(log.user_agent)}</td>
                    <td className="py-1.5">
                      <span className={`rounded px-1.5 py-0.5 text-xs ${log.success ? 'bg-gray-100 text-gray-700' : 'bg-red-50 text-red-600'}`}>
                        {log.success ? '成功' : '失败'}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {logs.length > pageSize && (
            <div className="mt-3 flex items-center justify-between border-t border-gray-100 pt-3">
              <span className="text-xs text-gray-400">共 {logs.length} 条，第 {logPage}/{totalPages} 页</span>
              <div className="flex items-center gap-1">
                <button onClick={() => setLogPage(1)} disabled={logPage === 1} className="rounded border border-gray-200 px-2 py-1 text-xs hover:bg-gray-50 disabled:opacity-30">首页</button>
                <button onClick={() => setLogPage(p => Math.max(1, p - 1))} disabled={logPage === 1} className="rounded border border-gray-200 px-2 py-1 text-xs hover:bg-gray-50 disabled:opacity-30">上一页</button>
                <button onClick={() => setLogPage(p => Math.min(totalPages, p + 1))} disabled={logPage >= totalPages} className="rounded border border-gray-200 px-2 py-1 text-xs hover:bg-gray-50 disabled:opacity-30">下一页</button>
                <button onClick={() => setLogPage(totalPages)} disabled={logPage >= totalPages} className="rounded border border-gray-200 px-2 py-1 text-xs hover:bg-gray-50 disabled:opacity-30">末页</button>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}

function formatUA(ua: string): string {
  const parts: string[] = []
  if (ua.includes('Windows NT')) parts.push('Windows')
  else if (ua.includes('Mac OS X')) parts.push('macOS')
  else if (ua.includes('Linux')) parts.push('Linux')
  else if (ua.includes('Android')) parts.push('Android')
  else if (ua.includes('iPhone') || ua.includes('iPad')) parts.push('iOS')

  if (ua.includes('Chrome') && !ua.includes('Edg')) parts.push('Chrome')
  else if (ua.includes('Firefox')) parts.push('Firefox')
  else if (ua.includes('Edg')) parts.push('Edge')
  else if (ua.includes('Safari') && !ua.includes('Chrome')) parts.push('Safari')

  return parts.join(' / ') || ua.substring(0, 40)
}

interface NotificationCardProps {
  enabled: boolean
  minSeverity: string
  webhookURL: string
  smtpEnabled: boolean
  smtpServer: string
  smtpPort: number
  smtpUser: string
  smtpPassword: string
  smtpFrom: string
  smtpTo: string
  smtpPasswordSet: boolean
  saving: boolean
  testing: boolean
  onEnabledChange: (enabled: boolean) => void
  onMinSeverityChange: (value: string) => void
  onWebhookURLChange: (value: string) => void
  onSMTPEnabledChange: (enabled: boolean) => void
  onSMTPServerChange: (value: string) => void
  onSMTPPortChange: (value: number) => void
  onSMTPUserChange: (value: string) => void
  onSMTPPasswordChange: (value: string) => void
  onSMTPFromChange: (value: string) => void
  onSMTPToChange: (value: string) => void
  onSave: () => void
  onTest: () => void
}

function NotificationCard(props: NotificationCardProps) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
      <div className="mb-4">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
          <Bell className="h-4 w-4" />外部告警推送
        </h2>
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">安全告警通过 Webhook / 邮件推送到外部，同一类告警 5 分钟内最多推送一次</p>
      </div>

      <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
        <input type="checkbox" checked={props.enabled} onChange={(e) => props.onEnabledChange(e.target.checked)} className="h-4 w-4 rounded border-gray-300" />
        启用安全告警推送
      </label>

      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <div>
          <label className="mb-1 block text-xs text-gray-500">最低推送级别</label>
          <select
            value={props.minSeverity}
            onChange={(e) => props.onMinSeverityChange(e.target.value)}
            disabled={!props.enabled}
            className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500"
          >
            <option value="low">low（全部）</option>
            <option value="medium">medium 及以上</option>
            <option value="high">high 及以上</option>
            <option value="critical">critical 仅严重</option>
          </select>
        </div>
        <div>
          <label className="mb-1 block text-xs text-gray-500">Webhook 地址</label>
          <input
            type="url"
            value={props.webhookURL}
            onChange={(e) => props.onWebhookURLChange(e.target.value)}
            disabled={!props.enabled}
            className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500"
            placeholder="https://example.com/hook/security"
          />
        </div>
      </div>

      <div className="mt-4 rounded-md border border-gray-100 bg-gray-50 p-3 dark:border-gray-800 dark:bg-gray-950">
        <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
          <input type="checkbox" checked={props.smtpEnabled} onChange={(e) => props.onSMTPEnabledChange(e.target.checked)} disabled={!props.enabled} className="h-4 w-4 rounded border-gray-300" />
          邮件推送（SMTP）
        </label>
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-xs text-gray-500">SMTP 服务器</label>
            <input type="text" value={props.smtpServer} onChange={(e) => props.onSMTPServerChange(e.target.value)} disabled={!props.enabled || !props.smtpEnabled} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500" placeholder="smtp.example.com" />
          </div>
          <div>
            <label className="mb-1 block text-xs text-gray-500">端口（465 隐式 TLS / 587 STARTTLS）</label>
            <input type="number" value={props.smtpPort} onChange={(e) => props.onSMTPPortChange(Number(e.target.value))} disabled={!props.enabled || !props.smtpEnabled} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500" />
          </div>
          <div>
            <label className="mb-1 block text-xs text-gray-500">用户名</label>
            <input type="text" value={props.smtpUser} onChange={(e) => props.onSMTPUserChange(e.target.value)} disabled={!props.enabled || !props.smtpEnabled} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500" autoComplete="off" />
          </div>
          <div>
            <label className="mb-1 block text-xs text-gray-500">密码{props.smtpPasswordSet ? '（已设置，留空保持不变）' : ''}</label>
            <input type="password" value={props.smtpPassword} onChange={(e) => props.onSMTPPasswordChange(e.target.value)} disabled={!props.enabled || !props.smtpEnabled} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500" placeholder={props.smtpPasswordSet ? '••••••••' : ''} autoComplete="new-password" />
          </div>
          <div>
            <label className="mb-1 block text-xs text-gray-500">发件人（From）</label>
            <input type="text" value={props.smtpFrom} onChange={(e) => props.onSMTPFromChange(e.target.value)} disabled={!props.enabled || !props.smtpEnabled} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500" placeholder="noreply@example.com" />
          </div>
          <div>
            <label className="mb-1 block text-xs text-gray-500">收件人（逗号分隔）</label>
            <input type="text" value={props.smtpTo} onChange={(e) => props.onSMTPToChange(e.target.value)} disabled={!props.enabled || !props.smtpEnabled} className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500" placeholder="admin@example.com" />
          </div>
        </div>
      </div>

      <div className="mt-4 flex justify-end gap-2">
        <button type="button" onClick={props.onTest} disabled={props.testing || !props.enabled} className="inline-flex items-center justify-center gap-2 rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">
          <SendIcon className="h-4 w-4" />
          {props.testing ? '发送中...' : '发送测试告警'}
        </button>
        <button type="button" onClick={props.onSave} disabled={props.saving} className="inline-flex items-center justify-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black dark:hover:bg-gray-200">
          <Save className="h-4 w-4" />
          {props.saving ? '保存中...' : '保存推送设置'}
        </button>
      </div>
    </div>
  )
}

function SendIcon({ className = '' }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="m22 2-7 20-4-9-9-4Z" />
      <path d="M22 2 11 13" />
    </svg>
  )
}

interface OvercommitCardProps {
  settings: OvercommitSettings | null
  saving: boolean
  onSave: (payload: {
    memory_overcommit_enabled: boolean
    memory_overcommit_ratio: number
    nat_subnet_oversubscription: boolean
    disk_overcommit_ratio: number
    ksm_tuning: { enabled: boolean; pages_to_scan: number; sleep_millisecs: number; use_tune_ksm: boolean }
  }) => void
}

function OvercommitCard(props: OvercommitCardProps) {
  const [draft, setDraft] = useState<OvercommitSettings | null>(null)

  useEffect(() => {
    if (props.settings && !draft) setDraft(props.settings)
  }, [props.settings, draft])

  if (!draft) {
    return (
      <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
        <p className="text-sm text-gray-400">资源超售设置尚未加载</p>
      </div>
    )
  }

  const set = (patch: Partial<OvercommitSettings>) => setDraft((prev) => (prev ? { ...prev, ...patch } : prev))
  const setKSM = (patch: Partial<OvercommitSettings['ksm_tuning']>) =>
    setDraft((prev) => (prev ? { ...prev, ksm_tuning: { ...prev.ksm_tuning, ...patch } } : prev))
  const setBounded = (value: number) => set({ memory_overcommit_ratio: Math.max(1, Math.min(16, value)) })
  const setDiskBounded = (value: number) => set({ disk_overcommit_ratio: Math.max(1, Math.min(100, value)) })

  const toggleRow = (
    enabled: boolean,
    onChange: (value: boolean) => void,
    title: string,
    subtitle: string
  ) => (
    <div className="flex items-center justify-between gap-4 border-y border-gray-100 py-3 dark:border-gray-800">
      <div>
        <div className="text-sm font-medium text-gray-800 dark:text-gray-200">{title}</div>
        <div className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{subtitle}</div>
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={enabled}
        onClick={() => onChange(!enabled)}
        className={`relative inline-flex h-6 w-11 flex-shrink-0 items-center rounded-full border transition-colors ${enabled ? 'border-black bg-black dark:border-white dark:bg-white' : 'border-gray-300 bg-gray-300 dark:border-gray-600 dark:bg-gray-700'}`}
      >
        <span className={`pointer-events-none absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow-sm transition-transform ${enabled ? 'translate-x-5 dark:bg-gray-900' : 'translate-x-0 dark:bg-gray-200'}`} />
      </button>
    </div>
  )

  const handleSave = () => {
    props.onSave({
      memory_overcommit_enabled: draft.memory_overcommit_enabled,
      memory_overcommit_ratio: draft.memory_overcommit_ratio,
      nat_subnet_oversubscription: draft.nat_subnet_oversubscription,
      disk_overcommit_ratio: draft.disk_overcommit_ratio,
      ksm_tuning: {
        enabled: draft.ksm_tuning.enabled,
        pages_to_scan: draft.ksm_tuning.pages_to_scan,
        sleep_millisecs: draft.ksm_tuning.sleep_millisecs,
        use_tune_ksm: draft.ksm_tuning.use_tune_ksm,
      },
    })
  }

  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-900">
      <div className="mb-4">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
          <TrendingUp className="h-4 w-4" />资源超售
        </h2>
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">内存、磁盘与 NAT 子网的超额分配及 KSM 内存去重调优</p>
      </div>

      {toggleRow(
        draft.memory_overcommit_enabled,
        (v) => set({ memory_overcommit_enabled: v }),
        '启用内存超售',
        '按超售比向租户超额分配内存'
      )}

      <div className="mt-4 grid gap-4 sm:grid-cols-3">
        <div>
          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">内存超售比（1.0~16.0）</label>
          <input
            type="number"
            min={1}
            max={16}
            step={0.5}
            value={draft.memory_overcommit_ratio}
            onChange={(e) => setBounded(Number(e.target.value) || 1)}
            className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
          />
        </div>
        <div className="rounded-md border border-gray-100 bg-gray-50 p-3 text-xs dark:border-gray-800 dark:bg-gray-950">
          <span className="text-gray-500 dark:text-gray-400">物理内存</span>
          <div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{draft.physical_ram_mb} MB</div>
        </div>
        <div className="rounded-md border border-gray-100 bg-gray-50 p-3 text-xs dark:border-gray-800 dark:bg-gray-950">
          <span className="text-gray-500 dark:text-gray-400">可分配内存</span>
          <div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{draft.allocatable_ram_mb} MB</div>
        </div>
      </div>

      <div className="mt-4 grid gap-4 sm:grid-cols-3">
        <div>
          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">磁盘超售比（1.0~100.0）</label>
          <input
            type="number"
            min={1}
            max={100}
            step={1}
            value={draft.disk_overcommit_ratio}
            onChange={(e) => setDiskBounded(Number(e.target.value) || 1)}
            className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
          />
        </div>
        <div className="rounded-md border border-gray-100 bg-gray-50 p-3 text-xs dark:border-gray-800 dark:bg-gray-950">
          <span className="text-gray-500 dark:text-gray-400">物理磁盘</span>
          <div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{draft.physical_disk_gb} GB</div>
        </div>
        <div className="rounded-md border border-gray-100 bg-gray-50 p-3 text-xs dark:border-gray-800 dark:bg-gray-950">
          <span className="text-gray-500 dark:text-gray-400">可分配磁盘</span>
          <div className="mt-0.5 font-mono text-gray-800 dark:text-gray-200">{draft.disk_allocatable_gb} GB</div>
        </div>
      </div>

      <div className="mt-4">
        {toggleRow(
          draft.nat_subnet_oversubscription,
          (v) => set({ nat_subnet_oversubscription: v }),
          'NAT 子网超售',
          '允许超出物理网络容量分配 NAT 子网'
        )}
      </div>

      <div className="mt-4 rounded-md border border-gray-100 bg-gray-50 p-3 dark:border-gray-800 dark:bg-gray-950">
        {toggleRow(
          draft.ksm_tuning.enabled,
          (v) => setKSM({ enabled: v }),
          'KSM 调优',
          '启用内核同页合并以回收重复内存页'
        )}
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">pages_to_scan（每周期扫描页数）</label>
            <input
              type="number"
              min={0}
              value={draft.ksm_tuning.pages_to_scan}
              onChange={(e) => setKSM({ pages_to_scan: Number(e.target.value) })}
              disabled={!draft.ksm_tuning.enabled}
              className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">sleep_millisecs（扫描间隔毫秒）</label>
            <input
              type="number"
              min={0}
              value={draft.ksm_tuning.sleep_millisecs}
              onChange={(e) => setKSM({ sleep_millisecs: Number(e.target.value) })}
              disabled={!draft.ksm_tuning.enabled}
              className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black disabled:bg-gray-50 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-950 dark:text-white dark:disabled:bg-gray-800 dark:disabled:text-gray-500"
            />
          </div>
        </div>
      </div>

      {draft.notes && (
        <p className="mt-4 rounded-md border border-gray-100 bg-gray-50 p-3 text-xs text-gray-600 dark:border-gray-800 dark:bg-gray-950 dark:text-gray-300">
          {draft.notes}
        </p>
      )}

      <div className="mt-4 flex justify-end">
        <button onClick={handleSave} disabled={props.saving} className="inline-flex items-center justify-center gap-2 rounded-md bg-black px-4 py-2 text-sm text-white hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black dark:hover:bg-gray-200">
          <Save className="h-4 w-4" />
          {props.saving ? '保存中...' : '保存超售设置'}
        </button>
      </div>
    </div>
  )
}
