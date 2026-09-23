import { useEffect } from 'react'
import { useLanguage } from '../contexts/LanguageContext'
import { useDialog } from './Dialog'

export default function BrowserDialogTranslator() {
  const { t } = useLanguage()
  const { alert: showAlert } = useDialog()

  useEffect(() => {
    const originalAlert = window.alert
    const originalConfirm = window.confirm
    window.alert = (message?: unknown) => { void showAlert('提示', String(message ?? '')) }
    window.confirm = (message?: string) => originalConfirm(t(String(message ?? '')))
    return () => {
      window.alert = originalAlert
      window.confirm = originalConfirm
    }
  }, [showAlert, t])

  return null
}
