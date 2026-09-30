// 管理员入口路径由服务端在 index.html 里按请求路径注入：
//   - 管理路径为默认 "/"  → 始终注入 "/"
//   - 请求命中管理路径    → 注入真实路径（如 "/mypanel-x9k2"）
//   - 其它请求            → 注入空串，表示当前页面**不挂载**管理端路由
//
// 这样管理入口无法通过枚举 /login、/admin 之类的常见路径被发现。
// 开发模式下保留占位符，按默认根路径处理。

type AdminPathWindow = { __EYVES_ADMIN_PATH__?: string }

const PLACEHOLDER = '__EYVES_ADMIN_PATH_VALUE__'

/**
 * 管理员入口的基路径：
 *   - `''`   管理端挂在根路径（默认）
 *   - `null` 当前页面未挂载管理端，必须完全不渲染管理路由
 */
export function adminBase(): string | null {
  const raw = (window as unknown as AdminPathWindow).__EYVES_ADMIN_PATH__
  if (raw === undefined || raw === PLACEHOLDER) return '' // 开发模式 / 未注入
  if (raw === '') return null
  const trimmed = raw.replace(/\/+$/, '')
  return trimmed === '' ? '' : trimmed
}

/** 拼接管理员入口下的路径；当前页面未挂载管理端时返回 null。 */
export function adminUrl(...segments: string[]): string | null {
  const base = adminBase()
  if (base === null) return null
  const tail = segments.filter((s) => s !== '').join('/')
  if (tail) return `${base}/${tail}`
  return base || '/'
}

/**
 * adminPath：管理端内部跳转路径（自动带上可自定义的管理入口前缀）。
 *
 * 背景（生产实测 bug）：管理入口路径可自定义（如 /admin-xxxx），但页面里曾用
 * 绝对路径 navigate('/container/5') —— 匹配不到路由会落到通配符被重定向回首页。
 * 所有管理端内部跳转必须经此函数拼接。
 */
export function adminPath(...segments: string[]): string {
  const url = adminUrl(...segments)
  if (url !== null) return url
  const tail = segments.filter((s) => s !== '').join('/')
  return tail ? `/${tail}` : '/'
}
