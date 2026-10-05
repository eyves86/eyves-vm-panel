import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Check, ChevronDown, Loader2, Search, UserCog } from 'lucide-react'
import { listSubUsersPaged, type SubUser } from '../services/api'
import { useLanguage } from '../contexts/LanguageContext'

const PAGE_SIZE = 20

export interface SubUserPickerOption {
  value: string
  label: string
}

interface SubUserPickerProps {
  value: string
  onChange: (id: string, label: string) => void
  // 当前选中项的名称（如列表已下发 owner_username），免去额外查询即可显示。
  selectedLabel?: string
  // 触发器在无选中项时的占位文案。
  placeholder?: string
  // 顶部固定选项（如「全部属主」「未绑定」），排在子用户结果之前。
  leadingOptions?: SubUserPickerOption[]
  disabled?: boolean
  triggerClassName?: string
  // 自定义触发器内容；label 为当前显示名，bound 表示是否已选中属主。
  renderTrigger?: (label: string, bound: boolean) => ReactNode
  menuWidthClass?: string
  className?: string
}

/**
 * 子用户选择器（企业级万级可用性）：服务端搜索 + 分页，避免一次拉取全量子用户。
 * 触发按钮内联于容器列表 / 详情弹窗，弹出层内提供关键字检索与「加载更多」。
 */
export default function SubUserPicker({
  value,
  onChange,
  selectedLabel,
  placeholder,
  leadingOptions = [],
  disabled = false,
  triggerClassName,
  renderTrigger,
  menuWidthClass = 'w-64',
  className = '',
}: SubUserPickerProps) {
  const { t } = useLanguage()
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [debounced, setDebounced] = useState('')
  const [items, setItems] = useState<SubUser[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(false)
  const [picked, setPicked] = useState<{ id: string; label: string } | null>(null)
  const reqIdRef = useRef(0)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(search.trim()), 300)
    return () => window.clearTimeout(timer)
  }, [search])

  const load = useCallback(async (pageNum: number, append: boolean) => {
    const reqId = ++reqIdRef.current
    setLoading(true)
    setError(false)
    try {
      const res = await listSubUsersPaged({ page: pageNum, page_size: PAGE_SIZE, search: debounced || undefined })
      if (reqId !== reqIdRef.current) return
      const data = res.data.data
      const list = (Array.isArray(data?.items) ? data!.items : []) as SubUser[]
      setItems((prev) => (append ? [...prev, ...list] : list))
      setTotal(data?.total ?? 0)
      setPage(pageNum)
    } catch {
      if (reqId === reqIdRef.current) setError(true)
    } finally {
      if (reqId === reqIdRef.current) setLoading(false)
    }
  }, [debounced])

  // 打开时（及关键字变化时）重新加载第一页。
  useEffect(() => {
    if (!open) return
    void load(1, false)
  }, [open, load])

  // 关闭时复位关键字，避免下次打开残留上次的过滤条件。
  useEffect(() => {
    if (open) return
    setSearch('')
    setDebounced('')
  }, [open])

  // 弹出层渲染到 body（portal）并按触发器位置做 fixed 定位，
  // 避免被容器表格的 overflow 容器裁剪（否则靠近表底的行的选项会被截断不可见）。
  const reposition = useCallback(() => {
    const menu = menuRef.current
    const trigger = triggerRef.current
    if (!menu || !trigger) return
    const r = trigger.getBoundingClientRect()
    // fixed 定位下 w-full 会相对视口解析，改为与触发器等宽。
    if (menuWidthClass === 'w-full') menu.style.width = `${r.width}px`
    else menu.style.width = ''
    const w = menu.offsetWidth
    const h = menu.offsetHeight
    let left = r.left
    if (left + w > window.innerWidth - 8) left = Math.max(8, window.innerWidth - 8 - w)
    let top = r.bottom + 4
    if (top + h > window.innerHeight - 8) {
      const above = r.top - 4 - h
      top = above >= 8 ? above : Math.max(8, window.innerHeight - 8 - h)
    }
    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
  }, [menuWidthClass])

  useLayoutEffect(() => {
    if (!open) return
    reposition()
    const onMove = () => reposition()
    window.addEventListener('scroll', onMove, true)
    window.addEventListener('resize', onMove)
    return () => {
      window.removeEventListener('scroll', onMove, true)
      window.removeEventListener('resize', onMove)
    }
  }, [open, reposition])

  // 内容尺寸变化（加载/错误/结果条数）后重新定位，保证弹出层在视口内。
  useEffect(() => {
    if (open) reposition()
  }, [open, items.length, loading, error, total, reposition])

  const leading = leadingOptions.find((o) => o.value === value)
  const bound = value !== ''
  let displayLabel: string
  if (leading) {
    displayLabel = leading.label
  } else if (!value) {
    displayLabel = placeholder || t('未绑定')
  } else if (picked && picked.id === value) {
    displayLabel = picked.label
  } else {
    displayLabel = selectedLabel || value
  }

  const select = (id: string, label: string) => {
    setPicked({ id, label })
    setOpen(false)
    onChange(id, label)
  }

  return (
    <div className={`relative inline-block ${className}`}>
      <button
        type="button"
        ref={triggerRef}
        disabled={disabled}
        onClick={() => setOpen((v) => !v)}
        className={triggerClassName}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        {renderTrigger ? renderTrigger(displayLabel, bound) : (
          <span className="inline-flex items-center gap-1">
            <span className="truncate">{displayLabel}</span>
            <ChevronDown className="h-3 w-3 shrink-0 text-gray-400" />
          </span>
        )}
      </button>

      {open && createPortal(
        <>
          <div className="fixed inset-0 z-[60]" onClick={() => setOpen(false)} />
          <div
            ref={menuRef}
            style={{ position: 'fixed', left: -9999, top: -9999 }}
            className={`z-[70] ${menuWidthClass} rounded-md border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-900`}
          >
            <div className="border-b border-gray-100 p-2 dark:border-gray-800">
              <div className="flex items-center gap-1.5 rounded border border-gray-200 px-2 dark:border-gray-700">
                <Search className="h-3.5 w-3.5 shrink-0 text-gray-400" />
                <input
                  autoFocus
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Escape') setOpen(false) }}
                  placeholder={t('搜索子用户名 / 邮箱 / 租户')}
                  className="h-7 w-full bg-transparent text-xs text-gray-700 outline-none dark:text-gray-200"
                />
              </div>
            </div>
            <div className="max-h-60 overflow-y-auto py-1">
              {leadingOptions.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  onClick={() => select(o.value, o.label)}
                  className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-gray-600 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-800"
                >
                  <UserCog className="h-3 w-3 shrink-0" />
                  <span className="truncate">{o.label}</span>
                  {value === o.value && <Check className="ml-auto h-3 w-3 shrink-0 text-indigo-600" />}
                </button>
              ))}
              {leadingOptions.length > 0 && <div className="my-1 border-t border-gray-100 dark:border-gray-800" />}
              {items.map((u) => (
                <button
                  key={u.id}
                  type="button"
                  onClick={() => select(u.id, u.username)}
                  className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-gray-50 dark:hover:bg-gray-800 ${
                    u.id === value ? 'font-medium text-indigo-700 dark:text-indigo-300' : 'text-gray-700 dark:text-gray-300'
                  }`}
                >
                  <UserCog className="h-3 w-3 shrink-0" />
                  <span className="truncate">{u.username}</span>
                  {u.tenant && <span className="ml-auto shrink-0 text-[10px] text-gray-400">{u.tenant}</span>}
                  {u.id === value && <Check className="h-3 w-3 shrink-0 text-indigo-600" />}
                </button>
              ))}
              {error ? (
                <div className="flex items-center gap-2 px-3 py-2 text-xs text-red-600">
                  <span>{t('子用户加载失败')}</span>
                  <button type="button" onClick={() => { void load(page, items.length > 0) }} className="underline hover:no-underline">
                    {t('重试')}
                  </button>
                </div>
              ) : loading && items.length === 0 ? (
                <div className="flex items-center gap-2 px-3 py-2 text-xs text-gray-400">
                  <Loader2 className="h-3 w-3 animate-spin" />
                  {t('加载中')}
                </div>
              ) : items.length === 0 ? (
                <div className="px-3 py-2 text-xs text-gray-400">
                  {debounced ? t('无匹配子用户') : t('暂无子用户')}
                </div>
              ) : null}
            </div>
            {!error && items.length > 0 && items.length < total && (
              <div className="border-t border-gray-100 p-1.5 dark:border-gray-800">
                <button
                  type="button"
                  onClick={() => { void load(page + 1, true) }}
                  disabled={loading}
                  className="w-full rounded px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-50 dark:text-gray-300 dark:hover:bg-gray-800"
                >
                  {loading ? t('加载中') : `${t('加载更多')} (${items.length}/${total})`}
                </button>
              </div>
            )}
          </div>
        </>,
        document.body,
      )}
    </div>
  )
}
