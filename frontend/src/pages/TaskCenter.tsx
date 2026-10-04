import { useCallback, useEffect, useState } from 'react'
import {
  Ban,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  CircleAlert,
  FileText,
  ListChecks,
  Loader2,
  RefreshCw,
  Timer,
  X,
} from 'lucide-react'
import {
  cancelTask,
  getTaskDetail,
  getTaskHistory,
  getTasks,
  getTaskStats,
  type Task,
  type TaskDetailData,
  type TaskHistoryEntry,
  type TaskStats,
} from '../services/api'
import { useDialog } from '../components/Dialog'
import { useLanguage } from '../contexts/LanguageContext'

const PAGE_SIZE = 20
const STATUS_FILTERS = [
  { value: '', label: '全部状态' },
  { value: 'completed', label: '已完成' },
  { value: 'failed', label: '失败' },
  { value: 'cancelled', label: '已取消' },
]
const TYPE_FILTERS = [
  { value: '', label: '全部类型' },
  { value: 'create', label: '创建' },
  { value: 'start', label: '开机' },
  { value: 'stop', label: '关机' },
  { value: 'restart', label: '重启' },
  { value: 'delete', label: '删除' },
  { value: 'reinstall', label: '重装' },
]

const TYPE_LABELS: Record<string, string> = {
  create: '创建',
  start: '开机',
  stop: '关机',
  restart: '重启',
  delete: '删除',
  reinstall: '重装',
}

// statusMeta 把任务状态映射为展示文案与配色（历史与实时任务共用）。
function statusMeta(status: string) {
  switch (status) {
    case 'completed':
    case 'done':
      return { label: '已完成', className: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' }
    case 'running':
      return { label: '运行中', className: 'bg-blue-50 text-blue-700 dark:bg-blue-950 dark:text-blue-300' }
    case 'pending':
      return { label: '排队中', className: 'bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300' }
    case 'failed':
      return { label: '失败', className: 'bg-red-50 text-red-700 dark:bg-red-950 dark:text-red-300' }
    case 'cancelled':
      return { label: '已取消', className: 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300' }
    default:
      return { label: status || '-', className: 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300' }
  }
}

function typeLabel(type: string) {
  return TYPE_LABELS[type] || type || '-'
}

// formatDuration 把毫秒转为易读时长；不足 1 秒显示毫秒。
function formatDuration(ms: number) {
  if (!ms || ms < 0) return '—'
  if (ms < 1000) return `${ms} ms`
  const secs = ms / 1000
  if (secs < 60) return `${secs.toFixed(1)} s`
  const mins = Math.floor(secs / 60)
  const rem = Math.round(secs % 60)
  if (mins < 60) return `${mins} 分 ${rem} 秒`
  const hours = Math.floor(mins / 60)
  return `${hours} 时 ${mins % 60} 分`
}

function formatTime(value?: string) {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleString()
}

export default function TaskCenter() {
  const { t } = useLanguage()
  const { confirm, alert } = useDialog()
  const [stats, setStats] = useState<TaskStats | null>(null)
  const [liveTasks, setLiveTasks] = useState<Task[]>([])
  const [history, setHistory] = useState<TaskHistoryEntry[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [statusFilter, setStatusFilter] = useState('')
  const [typeFilter, setTypeFilter] = useState('')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const [detail, setDetail] = useState<TaskDetailData | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  const fetchAll = useCallback(async () => {
    try {
      const [statsRes, liveRes, historyRes] = await Promise.all([
        getTaskStats(),
        getTasks(),
        getTaskHistory({
          status: statusFilter || undefined,
          type: typeFilter || undefined,
          page,
          page_size: PAGE_SIZE,
        }),
      ])
      setStats(statsRes.data.data ?? null)
      setLiveTasks(liveRes.data.data || [])
      const pageData = historyRes.data.data
      setHistory(pageData?.items || [])
      setTotal(pageData?.total || 0)
      setError('')
    } catch (err: unknown) {
      setError(
        (err as { response?: { data?: { message?: string } } }).response?.data?.message ||
          '加载任务数据失败，请稍后重试',
      )
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [statusFilter, typeFilter, page])

  useEffect(() => {
    void fetchAll()
  }, [fetchAll])

  // 运行中/排队任务需要接近实时，按固定间隔轮询刷新。
  useEffect(() => {
    const timer = window.setInterval(() => {
      void fetchAll()
    }, 5000)
    return () => window.clearInterval(timer)
  }, [fetchAll])

  const refresh = () => {
    setRefreshing(true)
    void fetchAll()
  }

  const openDetail = async (id: string) => {
    setDetailLoading(true)
    try {
      const res = await getTaskDetail(id)
      setDetail(res.data.data ?? null)
    } catch (err: unknown) {
      await alert(
        '加载失败',
        (err as { response?: { data?: { message?: string } } }).response?.data?.message ||
          '无法获取任务详情',
      )
    } finally {
      setDetailLoading(false)
    }
  }

  const doCancel = async (task: Task) => {
    const ok = await confirm(
      '取消任务',
      `确认取消任务 ${task.id}（${typeLabel(task.type)} / ${task.container_name || task.container_id}）？`,
    )
    if (!ok) return
    try {
      const res = await cancelTask(task.id)
      await alert('已提交', res.data.message || '任务已取消')
      void fetchAll()
    } catch (err: unknown) {
      await alert(
        '取消失败',
        (err as { response?: { data?: { message?: string } } }).response?.data?.message || '取消任务失败',
      )
    }
  }

  // 只展示尚未结束的任务（终态任务已在历史列表中）。
  const activeTasks = liveTasks.filter((t) => t.status === 'pending' || t.status === 'running')
  const historyStats = stats?.history
  const live = stats?.live || {}

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const safePage = Math.min(page, totalPages)

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-black dark:text-white">
            <ListChecks className="h-5 w-5" />
            {t("任务中心")}
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {t("查看任务队列实时进度、历史执行记录与过程日志")}
          </p>
        </div>
        <button
          onClick={refresh}
          disabled={refreshing}
          className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
        >
          <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
          {t("刷新")}
        </button>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
          {error}
        </div>
      )}

      {/* 统计概览 */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
        <StatCard label={t("历史任务")} value={historyStats?.total ?? 0} />
        <StatCard label={t("已完成")} value={historyStats?.by_status?.completed ?? 0} tone="ok" />
        <StatCard label={t("失败")} value={historyStats?.by_status?.failed ?? 0} tone="bad" />
        <StatCard label={t("已取消")} value={historyStats?.by_status?.cancelled ?? 0} />
        <StatCard label={t("平均耗时")} value={formatDuration(historyStats?.avg_duration_ms ?? 0)} icon={Timer} />
        <StatCard
          label={`队列（并发 ${live.concurrency ?? '-'}）`}
          value={`${live.active ?? 0} 运行 / ${live.pending ?? 0} 排队`}
        />
      </div>

      {/* 运行中 / 排队 */}
      <section className="rounded-xl border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        <div className="flex items-center justify-between border-b border-gray-100 px-4 py-3 dark:border-gray-700">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
            <Loader2 className={`h-4 w-4 ${activeTasks.length ? 'animate-spin text-blue-500' : 'text-gray-400'}`} />
            {t("队列中的任务")}
          </h2>
          <span className="text-xs text-gray-400">{activeTasks.length} {t("个")}</span>
        </div>
        {activeTasks.length === 0 ? (
          <div className="px-4 py-6 text-center text-sm text-gray-400">{t("当前没有运行或排队的任务")}</div>
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-800">
            {activeTasks.map((task) => {
              const meta = statusMeta(task.status)
              const percent = Math.max(0, Math.min(100, task.percent ?? 0))
              return (
                <li key={task.id} className="flex flex-col gap-2 px-4 py-3 md:flex-row md:items-center md:gap-4">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${meta.className}`}>{meta.label}</span>
                      <span className="text-sm font-medium text-black dark:text-white">{typeLabel(task.type)}</span>
                      <span className="truncate font-mono text-xs text-gray-500">
                        {task.container_name || task.container_id || '-'}
                      </span>
                      <span className="font-mono text-[11px] text-gray-400">{task.id}</span>
                    </div>
                    <div className="mt-1.5 flex items-center gap-2">
                      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                        <div
                          className={`h-full rounded-full transition-all ${task.status === 'running' ? 'bg-blue-500' : 'bg-amber-400'}`}
                          style={{ width: `${percent}%` }}
                        />
                      </div>
                      <span className="w-10 text-right text-[11px] text-gray-400">{percent}%</span>
                    </div>
                    {(task.stage_detail || task.stage) && (
                      <p className="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
                        {task.stage_detail || task.stage}
                      </p>
                    )}
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <button
                      onClick={() => openDetail(task.id)}
                      className="rounded-md border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                    >
                      {t("详情")}
                    </button>
                    <button
                      onClick={() => doCancel(task)}
                      className="inline-flex items-center gap-1 rounded-md border border-red-200 px-2.5 py-1.5 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-300 dark:hover:bg-red-950"
                    >
                      <Ban className="h-3.5 w-3.5" />
                      {t("取消")}
                    </button>
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </section>

      {/* 历史记录 */}
      <section className="rounded-xl border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-gray-700">
          <h2 className="text-sm font-semibold text-black dark:text-white">{t("历史执行记录")}</h2>
          <div className="flex items-center gap-2">
            <select
              value={statusFilter}
              onChange={(e) => {
                setStatusFilter(e.target.value)
                setPage(1)
              }}
              className="rounded-md border border-gray-300 bg-white px-2 py-1.5 text-xs text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
            >
              {STATUS_FILTERS.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label}
                </option>
              ))}
            </select>
            <select
              value={typeFilter}
              onChange={(e) => {
                setTypeFilter(e.target.value)
                setPage(1)
              }}
              className="rounded-md border border-gray-300 bg-white px-2 py-1.5 text-xs text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white"
            >
              {TYPE_FILTERS.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        {history.length === 0 ? (
          <div className="px-4 py-8 text-center text-sm text-gray-400">{t("暂无历史任务记录")}</div>
        ) : (
          <>
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-gray-100 bg-gray-50 text-left text-xs font-medium text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                    <th className="whitespace-nowrap px-4 py-2.5">{t("任务")}</th>
                    <th className="whitespace-nowrap px-4 py-2.5">{t("类型")}</th>
                    <th className="whitespace-nowrap px-4 py-2.5">{t("目标")}</th>
                    <th className="whitespace-nowrap px-4 py-2.5">{t("状态")}</th>
                    <th className="whitespace-nowrap px-4 py-2.5">{t("触发者")}</th>
                    <th className="whitespace-nowrap px-4 py-2.5">{t("耗时")}</th>
                    <th className="whitespace-nowrap px-4 py-2.5">{t("结束时间")}</th>
                    <th className="whitespace-nowrap px-4 py-2.5"></th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                  {history.map((item) => {
                    const meta = statusMeta(item.status)
                    return (
                      <tr
                        key={item.id}
                        className="cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800/60"
                        onClick={() => openDetail(item.id)}
                      >
                        <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-gray-500">{item.id}</td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-gray-800 dark:text-gray-200">
                          {typeLabel(item.type)}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-gray-700 dark:text-gray-300">
                          {item.container_name || item.container_id || '-'}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5">
                          <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${meta.className}`}>
                            {meta.label}
                          </span>
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-xs text-gray-500">
                          {item.user === 'admin' ? '管理员' : item.user || '-'}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-xs text-gray-500">
                          {formatDuration(item.duration_ms)}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-xs text-gray-500">
                          {formatTime(item.ended_at || item.created_at)}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-right">
                          <button
                            onClick={(e) => {
                              e.stopPropagation()
                              openDetail(item.id)
                            }}
                            className="inline-flex items-center gap-1 text-xs text-gray-500 hover:text-black dark:hover:text-white"
                          >
                            <FileText className="h-3.5 w-3.5" />
                            {t("日志")}
                          </button>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>

            {total > PAGE_SIZE && (
              <div className="flex items-center justify-between border-t border-gray-100 bg-gray-50 px-4 py-3 dark:border-gray-700 dark:bg-gray-800">
                <span className="text-xs text-gray-400">
                  {t("共")} {total} {t("条 · 第")} {safePage}/{totalPages}
                </span>
                <div className="flex items-center gap-1">
                  <button
                    onClick={() => setPage(1)}
                    disabled={safePage === 1}
                    className="p-1 text-gray-400 hover:text-black disabled:opacity-20 dark:hover:text-white"
                    title={t("首页")}
                  >
                    <ChevronsLeft className="h-4 w-4" />
                  </button>
                  <button
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                    disabled={safePage === 1}
                    className="p-1 text-gray-400 hover:text-black disabled:opacity-20 dark:hover:text-white"
                    title={t("上一页")}
                  >
                    <ChevronLeft className="h-4 w-4" />
                  </button>
                  <span className="px-2 text-xs text-gray-500">{safePage}</span>
                  <button
                    onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                    disabled={safePage >= totalPages}
                    className="p-1 text-gray-400 hover:text-black disabled:opacity-20 dark:hover:text-white"
                    title={t("下一页")}
                  >
                    <ChevronRight className="h-4 w-4" />
                  </button>
                  <button
                    onClick={() => setPage(totalPages)}
                    disabled={safePage >= totalPages}
                    className="p-1 text-gray-400 hover:text-black disabled:opacity-20 dark:hover:text-white"
                    title={t("末页")}
                  >
                    <ChevronsRight className="h-4 w-4" />
                  </button>
                </div>
              </div>
            )}
          </>
        )}
      </section>

      {/* 详情 / 日志弹窗 */}
      {(detail || detailLoading) && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
          onClick={() => !detailLoading && setDetail(null)}
        >
          <div
            className="max-h-[85vh] w-full max-w-2xl overflow-hidden rounded-xl bg-white shadow-xl dark:bg-gray-900"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-3.5 dark:border-gray-700">
              <h3 className="flex items-center gap-2 text-sm font-semibold text-black dark:text-white">
                <ListChecks className="h-4 w-4" />
                {t("任务详情")}
              </h3>
              <button
                onClick={() => setDetail(null)}
                className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white"
                aria-label={t("关闭")}
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {detailLoading || !detail ? (
              <div className="flex items-center justify-center py-16">
                <div className="h-7 w-7 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white" />
              </div>
            ) : (
              <div className="max-h-[70vh] overflow-y-auto px-5 py-4">
                {(() => {
                  const t = detail.task
                  const meta = statusMeta(t.status)
                  return (
                    <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
                      <DetailRow label="任务 ID" value={t.id} mono />
                      <DetailRow label="状态">
                        <span className={`rounded px-1.5 py-0.5 text-[11px] font-medium ${meta.className}`}>
                          {meta.label}
                        </span>
                      </DetailRow>
                      <DetailRow label="类型" value={typeLabel(t.type)} />
                      <DetailRow label="进度" value={`${t.percent ?? 0}%`} />
                      <DetailRow label="目标" value={t.container_name || String(t.container_id || '-')} mono />
                      <DetailRow label="触发者" value={t.user === 'admin' ? '管理员' : t.user || '-'} />
                      <DetailRow label="创建时间" value={formatTime(t.created_at)} />
                      <DetailRow label="开始时间" value={formatTime(t.started_at)} />
                      <DetailRow label="结束时间" value={formatTime(t.ended_at)} />
                      <DetailRow label="耗时" value={formatDuration(t.duration_ms ?? 0)} />
                      {t.ip && <DetailRow label="来源 IP" value={t.ip} mono />}
                      {!detail.live && t.percent !== undefined && t.stage_detail && (
                        <DetailRow label="最后阶段" value={t.stage_detail} />
                      )}
                      {detail.live && (t.stage_detail || t.stage) && (
                        <DetailRow label="当前阶段" value={t.stage_detail || t.stage || ''} />
                      )}
                    </div>
                  )
                })()}

                {detail.task.error && (
                  <div className="mt-4 flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
                    <CircleAlert className="mt-0.5 h-4 w-4 shrink-0" />
                    <span className="break-all">{detail.task.error}</span>
                  </div>
                )}

                <div className="mt-5">
                  <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">{t("过程日志")}</h4>
                  {detail.logs.length === 0 ? (
                    <div className="rounded-md border border-gray-200 bg-gray-50 px-3 py-3 text-xs text-gray-400 dark:border-gray-700 dark:bg-gray-800">
                      {t("该任务没有记录过程日志")}
                    </div>
                  ) : (
                    <ul className="space-y-1">
                      {detail.logs.map((log, index) => (
                        <li
                          key={`${log.created_at}-${index}`}
                          className="flex items-start gap-2 rounded-md border border-gray-100 bg-gray-50 px-3 py-1.5 dark:border-gray-800 dark:bg-gray-800/60"
                        >
                          <LogLevel level={log.level} />
                          <span className="whitespace-nowrap font-mono text-[11px] text-gray-400">
                            {formatTime(log.created_at)}
                          </span>
                          <span className="min-w-0 flex-1 break-words text-xs text-gray-700 dark:text-gray-200">
                            {log.message}
                          </span>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function StatCard({
  label,
  value,
  tone,
  icon: Icon,
}: {
  label: string
  value: string | number
  tone?: 'ok' | 'bad'
  icon?: React.ComponentType<{ className?: string }>
}) {
  const valueClass =
    tone === 'ok'
      ? 'text-emerald-600 dark:text-emerald-400'
      : tone === 'bad'
        ? 'text-red-600 dark:text-red-400'
        : 'text-black dark:text-white'
  return (
    <div className="rounded-xl border border-gray-200 bg-white px-3 py-2.5 dark:border-gray-700 dark:bg-gray-900">
      <div className="flex items-center gap-1 text-[11px] text-gray-500 dark:text-gray-400">
        {Icon && <Icon className="h-3.5 w-3.5" />}
        {label}
      </div>
      <div className={`mt-1 truncate text-sm font-semibold ${valueClass}`}>{value}</div>
    </div>
  )
}

function DetailRow({
  label,
  value,
  mono,
  children,
}: {
  label: string
  value?: string
  mono?: boolean
  children?: React.ReactNode
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 shrink-0 text-gray-400">{label}</span>
      {children ?? (
        <span className={`min-w-0 flex-1 truncate text-gray-800 dark:text-gray-200 ${mono ? 'font-mono' : ''}`}>
          {value}
        </span>
      )}
    </div>
  )
}

function LogLevel({ level }: { level: string }) {
  const upper = (level || 'INFO').toUpperCase()
  const className =
    upper === 'ERROR'
      ? 'bg-red-50 text-red-700 dark:bg-red-950 dark:text-red-300'
      : upper === 'WARN' || upper === 'WARNING'
        ? 'bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300'
        : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
  return <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${className}`}>{upper}</span>
}