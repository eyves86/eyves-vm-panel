import { useEffect } from 'react'

// utils/brand.ts —— 白标品牌：前端启动时从 /api/brand 拉取一次，全局生效。
// 后端全部为空时返回默认 EyvesCloud（开源/未授权形态），密钥不变。
export interface BrandConfig {
  name: string
  logo: string
  favicon: string
  login_title: string
  powered_hidden: boolean
  footer_text: string
  footer_hidden: boolean
}

let brand: BrandConfig = {
  name: 'EyvesCloud',
  logo: '',
  favicon: '',
  login_title: '',
  powered_hidden: false,
  footer_text: '',
  footer_hidden: false,
}

let loaded = false
const listeners = new Set<() => void>()
const CACHE_KEY = 'eyvescloud.brand'

// 立即读取缓存（避免闪烁旧品牌），后台再刷新。
try {
  const cached = localStorage.getItem(CACHE_KEY)
  if (cached) {
    const parsed = JSON.parse(cached) as BrandConfig
    if (parsed && typeof parsed.name === 'string') {
      brand = { ...brand, ...parsed }
      loaded = true
    }
  }
} catch { /* 忽略缓存错误 */ }

export async function loadBrand(): Promise<BrandConfig> {
  try {
    const res = await fetch('/api/brand', { credentials: 'include' })
    const j = await res.json()
    if (j?.success && j.data) {
      brand = { ...brand, ...j.data }
      loaded = true
      try { localStorage.setItem(CACHE_KEY, JSON.stringify(brand)) } catch { /* 隐私模式 */ }
      // 动态替换 favicon 与 document.title（index.html 静态默认值 → 品牌自适应）。
      if (brand.favicon) {
        let link = document.querySelector<HTMLLinkElement>('link[rel~="icon"]')
        if (!link) {
          link = document.createElement('link')
          link.rel = 'icon'
          document.head.appendChild(link)
        }
        link.href = brand.favicon
      }
      document.title = `${brand.name} - Cloud Container Manager`
      listeners.forEach((fn) => fn())
    }
  } catch { /* 网络错误保持默认 */ }
  return brand
}

// 应用启动时调用一次（main.tsx）；后续设置保存后可在 UI 里再调。
export async function initBrand() {
  if (loaded) applyBrandSync(brand)
  await loadBrand()
}

function applyBrandSync(cfg: BrandConfig) {
  if (cfg.favicon) {
    let link = document.querySelector('link[rel~="icon"]') as HTMLLinkElement | null
    if (!link) {
      link = document.createElement('link')
      link.rel = 'icon'
      document.head.appendChild(link)
    }
    link.href = cfg.favicon
  }
  document.title = `${cfg.name} - Cloud Container Manager`
}

export function useBrand(): BrandConfig {
  return brand
}

export function subscribeBrand(fn: () => void) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function useBrandName(): string {
  return brand.name
}

// 品牌加载完成标志（登录页用它决定是否等 brand 再渲染）。
export function isBrandLoaded(): boolean { return loaded }

export function useEffectBrand() {
  useEffect(() => { void initBrand() }, [])
}

// 管理员设置页保存后强制刷新品牌（重新拉取）。
export async function refreshBrand() {
  loaded = false
  await loadBrand()
}
