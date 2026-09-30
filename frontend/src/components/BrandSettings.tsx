// components/BrandSettings.tsx —— 面板设置里的"白标品牌"区块（管理员）。
//
// 能力：品牌名称 / Logo / favicon（data URL 上传，≤2MB）/ 登录页欢迎语 /
// 隐藏 "Powered by"。保存走 POST /api/brand，保存后 refreshBrand 让全站即时生效。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ImagePlus, Loader2, Save } from 'lucide-react'
import { useLanguage } from '../contexts/LanguageContext'
import { refreshBrand, useBrand, type BrandConfig } from '../utils/brand'

const MAX_IMAGE_BYTES = 2 * 1024 * 1024

async function fileToDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(new Error('读取文件失败'))
    reader.readAsDataURL(file)
  })
}

export default function BrandSettings() {
  const { t } = useLanguage()
  const current = useBrand()
  const [name, setName] = useState(current.name === 'EyvesCloud' ? '' : current.name)
  const [logo, setLogo] = useState(current.logo)
  const [favicon, setFavicon] = useState(current.favicon)
  const [loginTitle, setLoginTitle] = useState(current.login_title)
  const [poweredHidden, setPoweredHidden] = useState(current.powered_hidden)
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState('')
  const logoInput = useRef<HTMLInputElement>(null)
  const faviconInput = useRef<HTMLInputElement>(null)

  useEffect(() => {
    // 品牌加载完成后同步一次表单（仅当用户还没开始编辑）。
    if (current.name !== 'EyvesCloud' && !name) setName(current.name)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current.name])

  const pickImage = async (file: File | undefined, setter: (v: string) => void) => {
    if (!file) return
    if (!/^image\/(png|jpeg|svg\+xml|x-icon|ico)$/i.test(file.type)) {
      setMsg('仅支持 PNG/JPEG/SVG/ICO')
      return
    }
    if (file.size > MAX_IMAGE_BYTES) {
      setMsg('图片超过 2MB')
      return
    }
    setMsg('')
    const dataURL = await fileToDataURL(file)
    setter(dataURL)
  }

  const save = useCallback(async () => {
    setSaving(true)
    setMsg('')
    try {
      const res = await fetch('/api/brand', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: name.trim(),
          logo: typeof logo === 'string' ? logo : '',
          favicon: typeof favicon === 'string' ? favicon : '',
          login_title: loginTitle.trim(),
          powered_hidden: poweredHidden,
        }),
      })
      const j = await res.json()
      if (!j.success) {
        setMsg(j.message || '保存失败')
        return
      }
      await refreshBrand()
      setMsg(t('已保存'))
    } catch (e) {
      setMsg((e as Error).message || t('保存失败'))
    } finally {
      setSaving(false)
    }
  }, [name, logo, favicon, loginTitle, poweredHidden, t])

  const imgField = (
    label: string,
    value: string | Promise<string> | null,
    setter: (v: string) => void,
    inputRef: React.RefObject<HTMLInputElement | null>,
    hint: string
  ) => (
    <div>
      <label className="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-300">{label}</label>
      <div className="flex items-center gap-3">
        {typeof value === 'string' && value
          ? <img src={value} alt="" className="h-10 w-10 rounded border border-gray-200 object-contain" />
          : <div className="flex h-10 w-10 items-center justify-center rounded border border-dashed border-gray-300 text-gray-400"><ImagePlus className="h-4 w-4" /></div>}
        <button
          type="button"
          onClick={() => inputRef.current?.click()}
          className="rounded-md border border-gray-300 px-2.5 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50"
        >{t('选择图片')}</button>
        {typeof value === 'string' && value && (
          <button type="button" onClick={() => setter('')} className="text-xs text-red-500 hover:underline">{t('清除')}</button>
        )}
        <input ref={inputRef} type="file" accept="image/png,image/jpeg,image/svg+xml,image/x-icon" className="hidden"
          onChange={(e) => { void pickImage(e.target.files?.[0], setter); e.target.value = '' }} />
      </div>
      <p className="mt-1 text-[11px] text-gray-400">{hint}</p>
    </div>
  )

  return (
    <section className="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900">
      <h3 className="mb-3 text-sm font-semibold text-black dark:text-white">{t('白标品牌')}</h3>
      <div className="grid gap-4 sm:grid-cols-2">
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-300">{t('品牌名称')}</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="EyvesCloud"
            maxLength={60}
            className="w-full rounded-md border border-gray-300 px-2.5 py-1.5 text-sm dark:border-gray-700 dark:bg-gray-800"
          />
          <p className="mt-1 text-[11px] text-gray-400">{t('留空使用默认')}</p>
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-300">{t('登录页欢迎语')}</label>
          <input
            value={loginTitle}
            onChange={(e) => setLoginTitle(e.target.value)}
            maxLength={200}
            className="w-full rounded-md border border-gray-300 px-2.5 py-1.5 text-sm dark:border-gray-700 dark:bg-gray-800"
          />
        </div>
        {imgField(t('品牌 Logo'), logo, setLogo, logoInput, 'PNG/SVG ≤2MB · 登录页/侧边栏')}
        {imgField(t('Favicon'), favicon, setFavicon, faviconInput, 'ICO/PNG ≤2MB · 浏览器标签图标')}
      </div>
      <label className="mt-3 flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
        <input type="checkbox" checked={poweredHidden} onChange={(e) => setPoweredHidden(e.target.checked)} />
        {t('隐藏 "Powered by EyvesCloud" 字样')}
      </label>
      <div className="mt-3 flex items-center gap-3">
        <button
          onClick={() => { void save() }}
          disabled={saving}
          className="rounded-md bg-black px-3 py-1.5 text-xs font-medium text-white hover:bg-gray-800 disabled:opacity-50"
        >{saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : t('保存')}</button>
        {msg && <span className="text-xs text-gray-500">{msg}</span>}
      </div>
    </section>
  )
}
