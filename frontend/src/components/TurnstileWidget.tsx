import { useEffect, useRef } from 'react'

/**
 * Cloudflare Turnstile 人机验证组件。
 *
 * 用法：父组件持有 token 状态（onToken 回调写入 / 置空），
 * 登录提交时把 token 一并 POST；服务端 siteverify 一次性消费后，
 * 通过递增 resetKey 让本组件重置 widget 以获取新 token。
 *
 * 脚本按需加载（render=explicit），未启用 Turnstile 的部署不会
 * 引入任何第三方资源；CSP 已放行 challenges.cloudflare.com。
 */

type TurnstileOptions = {
  sitekey: string
  callback?: (token: string) => void
  'expired-callback'?: () => void
  'error-callback'?: (errorCode: string) => void
  theme?: 'light' | 'dark' | 'auto'
  size?: 'normal' | 'flexible' | 'compact'
  language?: string
  retry?: 'auto' | 'never'
}

type TurnstileAPI = {
  render: (container: HTMLElement, options: TurnstileOptions) => string
  remove: (widgetId: string) => void
  reset: (widgetId?: string) => void
}

declare global {
  interface Window {
    turnstile?: TurnstileAPI
    __onTurnstileLoaded?: () => void
  }
}

const SCRIPT_SRC = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
let scriptPromise: Promise<TurnstileAPI> | null = null

/** 加载 Turnstile api.js（全局单例，幂等）。 */
function loadTurnstile(): Promise<TurnstileAPI> {
  if (window.turnstile) return Promise.resolve(window.turnstile)
  if (scriptPromise) return scriptPromise
  scriptPromise = new Promise((resolve, reject) => {
    const existing = document.querySelector<HTMLScriptElement>(`script[src="${SCRIPT_SRC}"]`)
    const script = existing ?? document.createElement('script')
    const onLoad = () => {
      if (window.turnstile) resolve(window.turnstile)
      else reject(new Error('turnstile api loaded but window.turnstile missing'))
    }
    script.addEventListener('load', onLoad, { once: true })
    script.addEventListener('error', () => reject(new Error('failed to load turnstile api')), { once: true })
    if (!existing) {
      script.src = SCRIPT_SRC
      script.async = true
      script.defer = true
      document.head.appendChild(script)
    }
    // 已存在且已加载完成（极端时序）时兜底轮询一次。
    if (existing && window.turnstile) onLoad()
  })
  return scriptPromise
}

interface TurnstileWidgetProps {
  siteKey: string
  /** 验证通过时回调一次性 token；过期/重置时回调 '' */
  onToken: (token: string) => void
  /** 递增该值以重置 widget（token 是一次性的，失败后必须重置） */
  resetKey?: number
  /** 错误提示文案（脚本加载失败 / 渲染失败时展示） */
  errorMessage?: string
}

export default function TurnstileWidget({ siteKey, onToken, resetKey = 0, errorMessage }: TurnstileWidgetProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const widgetIdRef = useRef<string | null>(null)
  const onTokenRef = useRef(onToken)
  onTokenRef.current = onToken

  useEffect(() => {
    let cancelled = false
    const container = containerRef.current
    if (!container) return

    loadTurnstile()
      .then((turnstile) => {
        if (cancelled || !containerRef.current) return
        widgetIdRef.current = turnstile.render(containerRef.current, {
          sitekey: siteKey,
          callback: (token: string) => onTokenRef.current(token),
          'expired-callback': () => onTokenRef.current(''),
          'error-callback': () => {
            onTokenRef.current('')
            return true
          },
          theme: 'auto',
          size: 'flexible',
          language: 'auto',
        })
      })
      .catch(() => {
        // 加载失败：置空 token，交由表单的「未完成验证」提示兜底。
        onTokenRef.current('')
      })

    return () => {
      cancelled = true
      if (widgetIdRef.current !== null && window.turnstile) {
        try {
          window.turnstile.remove(widgetIdRef.current)
        } catch {
          /* 组件卸载竞态时忽略 */
        }
        widgetIdRef.current = null
      }
    }
  }, [siteKey])

  // resetKey 变化 → 重置 widget 并清空 token
  useEffect(() => {
    if (resetKey > 0 && widgetIdRef.current !== null && window.turnstile) {
      try {
        window.turnstile.reset(widgetIdRef.current)
      } catch {
        /* ignore */
      }
      onTokenRef.current('')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey])

  return (
    <div className="flex flex-col gap-1">
      <div ref={containerRef} className="min-h-[65px]" />
      {errorMessage && <p className="text-xs text-amber-600 dark:text-amber-400">{errorMessage}</p>}
    </div>
  )
}
