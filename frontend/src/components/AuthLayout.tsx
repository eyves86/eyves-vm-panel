import { ReactNode, useEffect, useState } from 'react'
import AppIcon from './AppIcon'
import { useLanguage } from '../contexts/LanguageContext'
import { getLoginFooter } from '../services/api'
import { useBrand } from '../utils/brand'

function LanguageIcon({ className = '' }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 1024 1024" version="1.1" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
      <path d="M213.333333 640v85.333333a85.333333 85.333333 0 0 0 78.933334 85.12L298.666667 810.666667h128v85.333333H298.666667a170.666667 170.666667 0 0 1-170.666667-170.666667v-85.333333h85.333333z m554.666667-213.333333l187.733333 469.333333h-91.946666l-51.242667-128h-174.506667l-51.157333 128h-91.904L682.666667 426.666667h85.333333z m-42.666667 123.093333L672.128 682.666667h106.325333L725.333333 549.76zM341.333333 85.333333v85.333334h170.666667v298.666666H341.333333v128H256v-128H85.333333V170.666667h170.666667V85.333333h85.333333z m384 42.666667a170.666667 170.666667 0 0 1 170.666667 170.666667v85.333333h-85.333333V298.666667a85.333333 85.333333 0 0 0-85.333334-85.333334h-128V128h128zM256 256H170.666667v128h85.333333V256z m170.666667 0H341.333333v128h85.333334V256z" fill="currentColor" />
    </svg>
  )
}

interface AuthLayoutProps {
  /** 问候语标题上方的小字（紫色圆点 + 短句） */
  greeting: string
  /** 主标题（如「欢迎回来」） */
  title: string
  /** 副标题说明 */
  subtitle: string
  /** 右侧品牌面板的口号 */
  slogan: string
  /** 右侧品牌面板的辅助文案 */
  sloganSub: string
  /** 表单内容 */
  children: ReactNode
}

// AuthLayout 是登录页共享的**分屏门面**（参考站 /login 的布局）：
// 左侧白底表单区（无卡片容器，表单直接铺在留白上），
// 右侧紫色渐变品牌面板（点阵网格 + 轨道环 + 悬浮 LOGO 卡片），
// 窄屏（<lg）自动退化为单栏表单。
export default function AuthLayout({ greeting, title, subtitle, slogan, sloganSub, children }: AuthLayoutProps) {
  const { language, toggleLanguage, t } = useLanguage()

  // 底部版权栏：管理员可在设置中自定义文字或隐藏（GET 为公开端点）。
  // hidden=true 不渲染；text 为空显示默认版权；加载失败静默回退默认。
  const [footer, setFooter] = useState<{ text: string; hidden: boolean }>({ text: '', hidden: false })

  useEffect(() => {
    getLoginFooter()
      .then(res => {
        const d = res.data?.data
        if (d) setFooter({ text: d.text || '', hidden: !!d.hidden })
      })
      .catch(() => {})
  }, [])

  // 白标品牌：登录页从 /api/brand 取名称/logo/欢迎语（缓存命中即无闪烁）。
  const brand = useBrand()
  const brandName = brand.name
  const footerText = footer.text || (brand.footer_text || `© ${new Date().getFullYear()} ${brandName}. All rights reserved.`)

  return (
    <div className="flex min-h-screen bg-white dark:bg-gray-950">
      {/* ===== 左侧：表单区 ===== */}
      <div className="relative flex min-h-screen w-full flex-col px-6 sm:px-10 lg:px-16">
        {/* 顶栏：品牌 + 语言切换 */}
        <div className="flex h-16 shrink-0 items-center justify-between">
          <div className="flex items-center gap-2">
            {brand.logo
              ? <img src={brand.logo} alt={brandName} className="h-6 w-auto" />
              : <AppIcon className="h-6 w-6" />}
            <span className="text-[15px] font-bold text-gray-900 dark:text-white">{brandName}</span>
          </div>
          <button
            type="button"
            onClick={() => { void toggleLanguage() }}
            className="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-gray-500 transition-colors hover:bg-gray-100 hover:text-brand-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-brand-300"
          >
            <LanguageIcon className="h-3.5 w-3.5" />
            {language === 'en' ? '中文' : 'English'}
          </button>
        </div>

        {/* 表单主体：垂直居中 */}
        <div className="flex flex-1 items-center justify-center py-10">
          <div className="w-full max-w-sm">
            <div className="mb-2 flex items-center gap-2 text-xs font-medium text-brand-600 dark:text-brand-400">
              <span className="h-1.5 w-1.5 rounded-full bg-brand-600 dark:bg-brand-400" />
              {t(greeting)}
            </div>
            <h1 className="text-2xl font-bold tracking-tight text-gray-900 sm:text-3xl dark:text-white">{t(title)}</h1>
            <p className="mt-2 text-sm text-gray-500 dark:text-gray-400">{t(subtitle)}</p>
            <div className="mt-8">{children}</div>
          </div>
        </div>

        {/* 底部版权栏（可在管理端设置中自定义文字或隐藏） */}
        {!footer.hidden && (
          <p className="shrink-0 pb-5 text-center text-xs text-gray-400 dark:text-gray-600">{footerText}</p>
        )}
      </div>

      {/* ===== 右侧：品牌面板（窄屏隐藏） ===== */}
      <div className="relative hidden w-[46%] max-w-2xl overflow-hidden rounded-l-[2rem] bg-gradient-to-br from-brand-400 via-brand-600 to-brand-800 lg:block dark:from-brand-600 dark:via-brand-800 dark:to-brand-950">
        {/* 点阵网格 */}
        <div className="auth-dot-grid absolute inset-0" aria-hidden="true" />
        {/* 轨道装饰环 */}
        <div className="absolute -right-32 top-1/2 h-[560px] w-[560px] -translate-y-1/2 rounded-full border border-white/15" aria-hidden="true" />
        <div className="absolute -right-16 top-1/2 h-[380px] w-[380px] -translate-y-1/2 rounded-full border border-white/10" aria-hidden="true" />
        <div className="absolute -right-48 top-1/2 h-[740px] w-[740px] -translate-y-1/2 rounded-full border border-white/[0.07]" aria-hidden="true" />
        {/* 中央柔光 */}
        <div className="absolute left-1/2 top-1/2 h-[460px] w-[460px] -translate-x-1/2 -translate-y-1/2 rounded-full bg-white/10 blur-3xl" aria-hidden="true" />

        <div className="relative flex h-full flex-col justify-between p-10 xl:p-14">
          {/* 右上角标语 */}
          <div className="flex justify-end">
            <span className="inline-flex items-center gap-2 text-xs text-white/80">
              <span className="h-1.5 w-1.5 rounded-full bg-white/80" />
              {t('每个想法，都有下一步')}
            </span>
          </div>

          {/* 中央：悬浮 LOGO 卡片 + 口号 */}
          <div className="flex flex-col items-start">
            <div className="mb-8 flex h-20 w-20 rotate-6 items-center justify-center rounded-2xl bg-white shadow-2xl shadow-brand-950/30">
              <AppIcon className="h-11 w-11" />
            </div>
            <h2 className="max-w-md text-3xl font-bold leading-snug text-white drop-shadow-sm xl:text-4xl">
              {t(slogan)}
            </h2>
            <p className="mt-4 max-w-md text-sm leading-relaxed text-white/75">
              {t(sloganSub)}
            </p>
          </div>

          {/* 底部小字（powered_hidden 时隐藏） */}
          {!brand.powered_hidden && <p className="text-xs text-white/50">© {brandName}</p>}
        </div>
      </div>
    </div>
  )
}
