export function actionLabel(action: string): string {
  const map: Record<string, string> = {
    create: '创建',
    start: '开机',
    stop: '关机',
    restart: '重启',
    delete: '删除',
    reinstall: '重装',
  }
  return map[action] || action
}

export function taskStatusLabel(status: string): string {
  const map: Record<string, string> = {
    pending: '等待中',
    running: '执行中',
    done: '已完成',
    failed: '失败',
  }
  return map[status] || status
}

export function taskStatusClass(status: string): string {
  const map: Record<string, string> = {
    pending: 'bg-gray-100 text-gray-700',
    running: 'bg-amber-100 text-amber-700',
    done: 'bg-emerald-50 text-emerald-700',
    failed: 'bg-red-50 text-red-700',
  }
  return map[status] || 'bg-gray-100 text-gray-700'
}

export function formatMB(mb: number): string {
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`
  return `${mb} MB`
}
