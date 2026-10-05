import { useState } from 'react'
import { Globe } from 'lucide-react'

const CODE_RE = /^[a-z]{2}$/

interface RegionFlagProps {
  code?: string
  /** 宽度（像素）；高度按 3:4 比例自动推算，与 flag-icons 的 4x3 集合一致。 */
  size?: number
  className?: string
  title?: string
}

// 区域国旗：渲染本地 SVG（构建期由 flag-icons 复制到 /flags/4x3）。
// 不用 emoji：Windows 的 Chrome/Edge 没有国旗 emoji 字形，🇨🇳 会退化成两个字母。
export default function RegionFlag({ code, size = 18, className = '', title }: RegionFlagProps) {
  const [failed, setFailed] = useState(false)
  const normalized = (code || '').trim().toLowerCase()

  if (!CODE_RE.test(normalized)) {
    return (
      <span
        title={title || '未设置国家/地区'}
        className={`inline-flex shrink-0 items-center justify-center rounded-[3px] bg-gray-100 text-gray-400 dark:bg-gray-800 dark:text-gray-500 ${className}`}
        style={{ width: size, height: Math.round(size * 0.75) }}
      >
        <Globe style={{ width: Math.round(size * 0.6), height: Math.round(size * 0.6) }} />
      </span>
    )
  }

  if (failed) {
    return (
      <span
        title={title || normalized.toUpperCase()}
        className={`inline-flex shrink-0 items-center justify-center rounded-[3px] bg-gray-100 font-mono text-[9px] font-semibold text-gray-500 dark:bg-gray-800 dark:text-gray-400 ${className}`}
        style={{ width: size, height: Math.round(size * 0.75) }}
      >
        {normalized.toUpperCase()}
      </span>
    )
  }

  return (
    <img
      src={`/flags/4x3/${normalized}.svg`}
      alt={title || normalized.toUpperCase()}
      title={title || normalized.toUpperCase()}
      loading="lazy"
      draggable={false}
      onError={() => setFailed(true)}
      className={`inline-block shrink-0 rounded-[2px] object-cover ring-1 ring-black/10 dark:ring-white/20 ${className}`}
      style={{ width: size, height: Math.round(size * 0.75) }}
    />
  )
}
