import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { Cpu, Globe, HardDrive, Play, RotateCw, Server, Square } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { getContainers, startContainer, stopContainer, restartContainer } from '../services/api'
import ServerUsageBadge from '../components/ServerUsageBadge'

// MyServer 只声明本页用到的字段，避免与全局 Container 类型强耦合。
type MyServer = {
  id: number | string
  uuid?: string
  name: string
  status: string
  virtualization?: string
  ip?: string
  ipv6?: string
  vcpu?: number
  ram_mb?: number
  disk_gb?: number
}

function statusBadge(status: string, t: (s: string) => string) {
  const running = status === 'running'
  const initializing = status === 'initializing'
  const cls = running
    ? 'bg-green-50 text-green-700 border-green-200'
    : initializing
      ? 'bg-amber-50 text-amber-700 border-amber-200'
      : 'bg-gray-100 text-gray-600 border-gray-200'
  const label = running ? t('运行中') : initializing ? t('初始化中') : t('已停止')
  return <span className={`rounded border px-2 py-0.5 text-xs font-medium ${cls}`}>{label}</span>
}

// MyServers 是用户门户首页：列出当前账号被授权的服务器，并提供基础电源操作。
export default function MyServers() {
  const { isReadOnly } = useAuth()
  const { t } = useLanguage()
  const [servers, setServers] = useState<MyServer[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<string>('')
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const res = await getContainers()
      setServers(((res.data.data as MyServer[]) || []).slice())
      setError('')
    } catch {
      setError(t('加载服务器列表失败，请刷新重试'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    void load()
  }, [load])

  const runAction = async (key: string, action: () => Promise<unknown>) => {
    setBusy(key)
    setError('')
    try {
      await action()
      // 电源操作是异步任务，稍等片刻再刷新状态。
      window.setTimeout(() => { void load() }, 1200)
    } catch {
      setError(t('操作失败，请稍后重试'))
    } finally {
      setBusy('')
    }
  }

  const keyOf = (s: MyServer) => String(s.uuid || s.id)

  return (
    <div>
      <div className="mb-5 flex items-center justify-between">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold text-gray-900 dark:text-white">
            <Server className="h-5 w-5" />
            {t('我的服务器')}
          </h1>
          <p className="mt-1 text-sm text-gray-500">
            {t('共')} {servers.length} {t('台')}
            {isReadOnly && <span className="ml-2 text-xs text-amber-600">{t('（只读账号，仅可查看）')}</span>}
          </p>
        </div>
        <button
          type="button"
          onClick={() => { void load() }}
          className="rounded-md border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
        >
          {t('刷新')}
        </button>
      </div>

      {error && (
        <div className="mb-4 rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>
      )}

      {loading ? (
        <div className="flex justify-center py-16">
          <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white" />
        </div>
      ) : servers.length === 0 ? (
        <div className="rounded-xl border border-dashed border-gray-300 bg-white py-16 text-center text-sm text-gray-500 dark:border-gray-700 dark:bg-gray-900">
          {t('暂无被授权的服务器，请联系管理员开通')}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
          {servers.map((s) => {
            const key = keyOf(s)
            const running = s.status === 'running'
            const working = busy === key
            return (
              <div
                key={key}
                className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm transition-shadow hover:shadow-md dark:border-gray-800 dark:bg-gray-900"
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="truncate text-sm font-semibold text-gray-900 dark:text-white">{s.name}</div>
                    <div className="mt-0.5 text-xs uppercase text-gray-400">{s.virtualization || 'lxc'}</div>
                  </div>
                  {statusBadge(s.status, t)}
                </div>

                <div className="mt-3 space-y-1.5 text-xs text-gray-600 dark:text-gray-400">
                  <div className="flex items-center gap-1.5">
                    <Globe className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate">{s.ip || '-'}{s.ipv6 ? ` / ${s.ipv6}` : ''}</span>
                  </div>
                  <div className="flex items-center gap-3">
                    <span className="flex items-center gap-1.5">
                      <Cpu className="h-3.5 w-3.5" />{s.vcpu ?? '-'} vCPU
                    </span>
                    <span className="flex items-center gap-1.5">
                      <HardDrive className="h-3.5 w-3.5" />{s.disk_gb ?? '-'} GB
                    </span>
                    <span>{s.ram_mb ?? '-'} MB</span>
                  </div>
                  {(s as MyServer & { expires_at?: string }).expires_at && (
                    <div className="text-gray-500 dark:text-gray-500">
                      {t('到期')}：{String((s as MyServer & { expires_at?: string }).expires_at).slice(0, 10)}
                    </div>
                  )}
                </div>

                {!isReadOnly && <ServerUsageBadge serverId={key} />}

                <div className="mt-4 flex items-center gap-2">
                  {running ? (
                    <button
                      type="button"
                      disabled={isReadOnly || working}
                      onClick={() => { void runAction(key, () => stopContainer(s.uuid || s.id)) }}
                      className="inline-flex flex-1 items-center justify-center gap-1.5 rounded-md border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800"
                    >
                      <Square className="h-3.5 w-3.5" />{t('关机')}
                    </button>
                  ) : (
                    <button
                      type="button"
                      disabled={isReadOnly || working}
                      onClick={() => { void runAction(key, () => startContainer(s.uuid || s.id)) }}
                      className="inline-flex flex-1 items-center justify-center gap-1.5 rounded-md border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800"
                    >
                      <Play className="h-3.5 w-3.5" />{t('开机')}
                    </button>
                  )}
                  <button
                    type="button"
                    disabled={isReadOnly || working || !running}
                    onClick={() => { void runAction(key, () => restartContainer(s.uuid || s.id)) }}
                    className="inline-flex flex-1 items-center justify-center gap-1.5 rounded-md border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800"
                  >
                    <RotateCw className="h-3.5 w-3.5" />{t('重启')}
                  </button>
                </div>

                <Link
                  to={`/user/container/${encodeURIComponent(String(s.uuid || s.id))}`}
                  className="mt-2 block text-center text-xs text-gray-500 underline hover:text-black dark:hover:text-white"
                >
                  {t('查看详情')}
                </Link>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
