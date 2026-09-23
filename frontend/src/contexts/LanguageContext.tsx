import { ReactNode, createContext, useContext, useEffect, useMemo, useState } from 'react'
import { translateText } from '../utils/i18n'
import { getLanguage, updateLanguage } from '../services/api'

export type Language = 'zh' | 'en'

interface LanguageContextValue {
  language: Language
  setLanguage: (language: Language) => void
  toggleLanguage: () => Promise<void>
  t: (value: string) => string
}

const LanguageContext = createContext<LanguageContextValue | undefined>(undefined)
function initialLanguage(): Language {
  return 'zh'
}

export function LanguageProvider({ children }: { children: ReactNode }) {
  const [language, setLanguageState] = useState<Language>(initialLanguage)

  const setLanguageLocal = (next: Language) => {
    setLanguageState(next)
  }

  const setLanguage = (next: Language) => {
    setLanguageLocal(next)
    updateLanguage(next).catch(() => {})
  }

  const value = useMemo<LanguageContextValue>(() => ({
    language,
    setLanguage,
    toggleLanguage: async () => {
      const next = language === 'zh' ? 'en' : 'zh'
      setLanguageLocal(next)
      try {
        const res = await updateLanguage(next)
        setLanguageLocal(res.data.data?.language || next)
      } catch {
        setLanguageLocal(language)
      }
    },
    t: (text: string) => language === 'en' ? translateText(text) : text,
  }), [language])

  useEffect(() => {
    getLanguage()
      .then((res) => {
        const serverLanguage = res.data.data?.language
        if (serverLanguage === 'zh' || serverLanguage === 'en') {
          setLanguageLocal(serverLanguage)
        }
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    document.documentElement.lang = language === 'en' ? 'en' : 'zh-CN'
    document.documentElement.dataset.language = language
  }, [language])

  return <LanguageContext.Provider value={value}>{children}</LanguageContext.Provider>
}

export function useLanguage() {
  const context = useContext(LanguageContext)
  if (!context) {
    throw new Error('useLanguage must be used within LanguageProvider')
  }
  return context
}
