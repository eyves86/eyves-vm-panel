import { useCallback, useEffect, useState } from 'react'
import { ChevronRight, Link2, Pencil, Plus, RefreshCw, ShieldCheck, Trash2, X } from 'lucide-react'
import { useDialog } from '../components/Dialog'
import {
  createSecGroup,
  createSecGroupRule,
  deleteSecGroup,
  deleteSecGroupRule,
  getContainerSecGroups,
  getContainers,
  getSecGroup,
  getTenants,
  listSecGroupRules,
  listSecGroups,
  setContainerSecGroups,
  updateSecGroup,
  type Container,
  type SecGroup,
  type SecGroupRule,
  type Tenant,
} from '../services/api'

const inputCls = 'w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black dark:border-gray-700 dark:bg-gray-950 dark:text-white'

const DIRECTIONS = [
  { value: 'ingress', label: '入站 (ingress)' },
  { value: 'egress', label: '出站 (egress)' },
]

const PROTOCOLS = [
  { value: 'any', label: '任意 (any)' },
  { value: 'tcp', label: 'TCP' },
  { value: 'udp', label: 'UDP' },
  { value: 'icmp', label: 'ICMP' },
]

const RULE_ACTIONS = [
  { value: 'accept', label: '放行 (accept)' },
  { value: 'drop', label: '丢弃 (drop)' },
  { value: 'reject', label: '拒绝 (reject)' },
]

const DEFAULT_ACTIONS = [
  { value: 'accept', label: '放行 (accept)' },
  { value: 'drop', label: '丢弃 (drop)' },
]

interface GroupDraft {
  name: string
  tenant_id: string
  default_action: string
}

interface RuleDraft {
  direction: string
  protocol: string
  src_mask: string
  dst_mask: string
  src_port: string
  dst_port: string
  action: string
  priority: string
  description: string
}

const EMPTY_RULE: RuleDraft = {
  direction: 'ingress',
  protocol: 'tcp',
  src_mask: '',
  dst_mask: '',
  src_port: '',
  dst_port: '',
  action: 'accept',
  priority: '100',
  description: '',
}

function errMessage(err: unknown, fallback: string): string {
  return (err as { response?: { data?: { message?: string } } }).response?.data?.message || fallback
}

function actionLabel(action: string): string {
  if (action === 'accept') return '放行'
  if (action === 'drop') return '丢弃'
  if (action === 'reject') return '拒绝'
  return action || '—'
}

function directionLabel(direction: string): string {
  if (direction === 'ingress') return '入站'
  if (direction === 'egress') return '出站'
  return direction || '—'
}

function protocolLabel(protocol: string): string {
  if (protocol === 'any') return '任意'
  return protocol.toUpperCase()
}

function ActionBadge({ action }: { action: string }) {
  const cls =
    action === 'accept'
      ? 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400'
      : action === 'reject'
        ? 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400'
        : 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400'
  return (
    <span className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${cls}`} title={action}>
      {actionLabel(action)}
    </span>
  )
}

function DirectionBadge({ direction }: { direction: string }) {
  const cls =
    direction === 'ingress'
      ? 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
      : 'bg-purple-50 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400'
  return (
    <span className={`inline-flex items-center rounded px-2 py-0.5 text-xs font-medium ${cls}`} title={direction}>
      {directionLabel(direction)}
    </span>
  )
}

function fmtPort(port: number | undefined): string {
  if (port === undefined || port === 0) return '任意'
  return String(port)
}

function parsePort(value: string): number {
  const n = Number.parseInt(value, 10)
  return Number.isNaN(n) ? 0 : n
}

export default function SecurityGroups() {
  const dialog = useDialog()
  const [groups, setGroups] = useState<SecGroup[]>([])
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [ruleCounts, setRuleCounts] = useState<Record<string, number>>({})
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  // 新建安全组
  const [createOpen, setCreateOpen] = useState(false)
  const [createForm, setCreateForm] = useState<GroupDraft>({ name: '', tenant_id: '', default_action: 'accept' })
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState('')

  // 编辑安全组
  const [editGroup, setEditGroup] = useState<SecGroup | null>(null)
  const [editForm, setEditForm] = useState<{ name: string; default_action: string }>({ name: '', default_action: 'accept' })
  const [savingEdit, setSavingEdit] = useState(false)
  const [editError, setEditError] = useState('')

  // 安全组详情（含规则）
  const [detailId, setDetailId] = useState<string | null>(null)
  const [detail, setDetail] = useState<{ group: SecGroup; rules: SecGroupRule[] } | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailError, setDetailError] = useState('')
  const [showRuleForm, setShowRuleForm] = useState(false)
  const [ruleDraft, setRuleDraft] = useState<RuleDraft>(EMPTY_RULE)
  const [addingRule, setAddingRule] = useState(false)
  const [ruleError, setRuleError] = useState('')

  // 容器绑定管理
  const [bindOpen, setBindOpen] = useState(false)
  const [bindContainers, setBindContainers] = useState<Container[]>([])
  const [bindContainersLoading, setBindContainersLoading] = useState(false)
  const [bindContainerId, setBindContainerId] = useState('')
  const [bindSelected, setBindSelected] = useState<string[]>([])
  const [bindLoadingCurrent, setBindLoadingCurrent] = useState(false)
  const [bindSaving, setBindSaving] = useState(false)
  const [bindError, setBindError] = useState('')

  const tenantLabel = (tenantId: string): string => {
    if (!tenantId) return '—'
    const hit = tenants.find((t) => t.id === tenantId)
    return hit ? hit.name : tenantId
  }

  const countRules = async (groupId: string) => {
    try {
      const res = await listSecGroupRules(groupId)
      setRuleCounts((prev) => ({ ...prev, [groupId]: (res.data.data || []).length }))
    } catch {
      // 单个组规则数获取失败时忽略，展示为 “—”
    }
  }

  const fetchData = useCallback(async () => {
    try {
      const res = await listSecGroups()
      const list = res.data.data || []
      setGroups(list)
      setError('')
      const results = await Promise.all(
        list.map((g) => listSecGroupRules(g.id).then((r) => (r.data.data || []).length).catch(() => -1)),
      )
      const counts: Record<string, number> = {}
      list.forEach((g, idx) => {
        if (results[idx] >= 0) counts[g.id] = results[idx]
      })
      setRuleCounts(counts)
    } catch (err: unknown) {
      setError(errMessage(err, '加载安全组失败'))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { fetchData() }, [fetchData])

  useEffect(() => {
    getTenants()
      .then((res) => setTenants(res.data.data || []))
      .catch(() => setTenants([]))
  }, [])

  // ---- 新建安全组 ------------------------------------------------------------
  const openCreate = async () => {
    setCreateForm({ name: '', tenant_id: '', default_action: 'accept' })
    setCreateError('')
    setCreateOpen(true)
    if (tenants.length === 0) {
      try {
        const res = await getTenants()
        setTenants(res.data.data || [])
      } catch {
        // 拉取租户列表失败时，可保持“不指定”提交
      }
    }
  }

  const submitCreate = async () => {
    if (!createForm.name.trim()) {
      setCreateError('名称不能为空')
      return
    }
    setCreating(true)
    setCreateError('')
    try {
      await createSecGroup({
        name: createForm.name.trim(),
        tenant_id: createForm.tenant_id || undefined,
        default_action: createForm.default_action,
      })
      setCreateOpen(false)
      await fetchData()
    } catch (err: unknown) {
      setCreateError(errMessage(err, '创建失败，请稍后重试'))
    } finally {
      setCreating(false)
    }
  }

  // ---- 编辑安全组 ------------------------------------------------------------
  const openEdit = (g: SecGroup) => {
    setEditGroup(g)
    setEditForm({ name: g.name, default_action: g.default_action || 'accept' })
    setEditError('')
  }

  const submitEdit = async () => {
    if (!editGroup) return
    if (!editForm.name.trim()) {
      setEditError('名称不能为空')
      return
    }
    setSavingEdit(true)
    setEditError('')
    try {
      await updateSecGroup(editGroup.id, {
        name: editForm.name.trim(),
        default_action: editForm.default_action,
      })
      setEditGroup(null)
      await fetchData()
    } catch (err: unknown) {
      setEditError(errMessage(err, '保存失败，请稍后重试'))
    } finally {
      setSavingEdit(false)
    }
  }

  // ---- 删除安全组 ------------------------------------------------------------
  const removeGroup = async (g: SecGroup) => {
    const ok = await dialog.confirm('删除安全组', `确定删除安全组「${g.name}」？该操作不可恢复。`)
    if (!ok) return
    try {
      await deleteSecGroup(g.id)
      await fetchData()
    } catch (err: unknown) {
      dialog.alert('删除失败', errMessage(err, '删除失败，请稍后重试'))
    }
  }

  // ---- 详情与规则 ------------------------------------------------------------
  const loadDetail = async (id: string) => {
    setDetailLoading(true)
    try {
      const res = await getSecGroup(id)
      setDetail(res.data.data || null)
      setDetailError('')
    } catch (err: unknown) {
      setDetail(null)
      setDetailError(errMessage(err, '加载安全组详情失败'))
    } finally {
      setDetailLoading(false)
    }
  }

  const openDetail = (g: SecGroup) => {
    setDetailId(g.id)
    setDetail(null)
    setDetailError('')
    setShowRuleForm(false)
    setRuleDraft(EMPTY_RULE)
    setRuleError('')
    void loadDetail(g.id)
  }

  const closeDetail = () => {
    setDetailId(null)
    setDetail(null)
    setDetailError('')
    setShowRuleForm(false)
    setRuleDraft(EMPTY_RULE)
    setRuleError('')
  }

  const addRule = async () => {
    if (!detailId) return
    const priority = Number.parseInt(ruleDraft.priority, 10)
    if (Number.isNaN(priority)) {
      setRuleError('优先级必须是整数')
      return
    }
    setAddingRule(true)
    setRuleError('')
    try {
      await createSecGroupRule(detailId, {
        direction: ruleDraft.direction,
        protocol: ruleDraft.protocol,
        action: ruleDraft.action,
        priority,
        src_mask: ruleDraft.src_mask.trim() || undefined,
        dst_mask: ruleDraft.dst_mask.trim() || undefined,
        src_port: parsePort(ruleDraft.src_port),
        dst_port: parsePort(ruleDraft.dst_port),
        description: ruleDraft.description.trim() || undefined,
      })
      setRuleDraft(EMPTY_RULE)
      setShowRuleForm(false)
      await loadDetail(detailId)
      void countRules(detailId)
    } catch (err: unknown) {
      setRuleError(errMessage(err, '添加规则失败，请稍后重试'))
    } finally {
      setAddingRule(false)
    }
  }

  const removeRule = async (rule: SecGroupRule) => {
    if (!detailId) return
    const ok = await dialog.confirm('删除规则', `确定删除这条「${directionLabel(rule.direction)} / ${protocolLabel(rule.protocol)}」规则？`)
    if (!ok) return
    try {
      await deleteSecGroupRule(detailId, rule.id)
      await loadDetail(detailId)
      void countRules(detailId)
    } catch (err: unknown) {
      dialog.alert('删除失败', errMessage(err, '删除规则失败，请稍后重试'))
    }
  }

  // ---- 容器绑定管理 ----------------------------------------------------------
  const openBind = async () => {
    setBindOpen(true)
    setBindContainerId('')
    setBindSelected([])
    setBindError('')
    setBindContainersLoading(true)
    try {
      const res = await getContainers()
      setBindContainers(res.data.data || [])
    } catch (err: unknown) {
      setBindContainers([])
      setBindError(errMessage(err, '获取容器列表失败'))
    } finally {
      setBindContainersLoading(false)
    }
  }

  const closeBind = () => {
    setBindOpen(false)
    setBindContainerId('')
    setBindSelected([])
    setBindError('')
  }

  const pickContainer = async (id: string) => {
    setBindContainerId(id)
    setBindSelected([])
    setBindError('')
    if (!id) return
    setBindLoadingCurrent(true)
    try {
      const res = await getContainerSecGroups(id)
      setBindSelected(res.data.data?.sec_group_ids || [])
    } catch (err: unknown) {
      setBindError(errMessage(err, '获取容器已绑定安全组失败'))
    } finally {
      setBindLoadingCurrent(false)
    }
  }

  const toggleBind = (groupId: string) => {
    setBindSelected((prev) => (prev.includes(groupId) ? prev.filter((id) => id !== groupId) : [...prev, groupId]))
  }

  const submitBind = async () => {
    if (!bindContainerId) {
      setBindError('请选择容器')
      return
    }
    setBindSaving(true)
    setBindError('')
    try {
      await setContainerSecGroups(bindContainerId, bindSelected)
      closeBind()
      dialog.alert('完成', '容器安全组绑定已更新')
    } catch (err: unknown) {
      setBindError(errMessage(err, '保存失败，请稍后重试'))
    } finally {
      setBindSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-black dark:text-white">
            <ShieldCheck className="h-5 w-5" />安全组
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">管理安全组与出入站规则，并绑定到容器控制流量策略</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { setRefreshing(true); fetchData() }}
            disabled={refreshing}
            className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          >
            <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />刷新
          </button>
          <button onClick={openBind} className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">
            <Link2 className="h-4 w-4" />容器绑定
          </button>
          <button onClick={openCreate} className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-3 py-2 text-sm text-white hover:bg-brand-700 dark:bg-brand-500 dark:text-white">
            <Plus className="h-4 w-4" />新建安全组
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{error}</div>
      )}

      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {groups.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400"><ShieldCheck className="h-7 w-7" /></div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-200">暂无安全组</div>
            <div className="mt-1 text-xs text-gray-400">点击「新建安全组」创建第一个安全组</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">名称</th>
                  <th className="px-4 py-3 text-left font-medium">租户</th>
                  <th className="px-4 py-3 text-left font-medium">默认动作</th>
                  <th className="px-4 py-3 text-left font-medium">规则数</th>
                  <th className="px-4 py-3 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {groups.map((g) => (
                  <tr key={g.id} onClick={() => openDetail(g)} className="cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="px-4 py-3">
                      <div className="text-gray-800 dark:text-gray-100">{g.name}</div>
                      <div className="font-mono text-xs text-gray-400">{g.id}</div>
                    </td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">{tenantLabel(g.tenant_id)}</td>
                    <td className="px-4 py-3"><ActionBadge action={g.default_action} /></td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                      <span className="inline-flex items-center gap-1 font-medium text-black dark:text-white">
                        <ChevronRight className="h-3.5 w-3.5 text-gray-400" />
                        {ruleCounts[g.id] ?? '—'}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex justify-end gap-1.5">
                        <button
                          onClick={(e) => { e.stopPropagation(); openDetail(g) }}
                          className="inline-flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                        >
                          规则
                        </button>
                        <button
                          onClick={(e) => { e.stopPropagation(); openEdit(g) }}
                          className="inline-flex items-center gap-1 rounded border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                        >
                          <Pencil className="h-3 w-3" />编辑
                        </button>
                        <button
                          onClick={(e) => { e.stopPropagation(); void removeGroup(g) }}
                          className="inline-flex items-center gap-1 rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950"
                        >
                          <Trash2 className="h-3 w-3" />删除
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* 新建安全组 Modal */}
      {createOpen && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/50 p-4 dark:bg-black/70 z-50">
          <div className="flex w-full max-w-md flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">新建安全组</h3>
              <button onClick={() => setCreateOpen(false)} className="rounded p-1 text-gray-400 hover:text-black dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              {createError && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{createError}</div>}
              <div className="space-y-4">
                <div>
                  <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">名称 <span className="text-red-500">*</span></label>
                  <input
                    type="text"
                    value={createForm.name}
                    onChange={(e) => setCreateForm((d) => ({ ...d, name: e.target.value }))}
                    placeholder="web-servers"
                    className={inputCls}
                  />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">租户（可选）</label>
                  <select
                    value={createForm.tenant_id}
                    onChange={(e) => setCreateForm((d) => ({ ...d, tenant_id: e.target.value }))}
                    className={inputCls}
                  >
                    <option value="">不指定</option>
                    {tenants.map((t) => (
                      <option key={t.id} value={t.id}>{t.name}</option>
                    ))}
                  </select>
                </div>
                <div>
                  <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">默认动作 <span className="text-red-500">*</span></label>
                  <select
                    value={createForm.default_action}
                    onChange={(e) => setCreateForm((d) => ({ ...d, default_action: e.target.value }))}
                    className={inputCls}
                  >
                    {DEFAULT_ACTIONS.map((a) => (
                      <option key={a.value} value={a.value}>{a.label}</option>
                    ))}
                  </select>
                </div>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button onClick={() => setCreateOpen(false)} disabled={creating} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-100 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700">取消</button>
              <button onClick={submitCreate} disabled={creating} className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white">
                {creating && <RefreshCw className="h-4 w-4 animate-spin" />}
                {creating ? '创建中…' : '创建'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 编辑安全组 Modal */}
      {editGroup && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/50 p-4 dark:bg-black/70 z-50">
          <div className="flex w-full max-w-md flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">编辑安全组</h3>
              <button onClick={() => setEditGroup(null)} className="rounded p-1 text-gray-400 hover:text-black dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              {editError && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{editError}</div>}
              <div className="space-y-4">
                <div>
                  <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">名称 <span className="text-red-500">*</span></label>
                  <input
                    type="text"
                    value={editForm.name}
                    onChange={(e) => setEditForm((d) => ({ ...d, name: e.target.value }))}
                    className={inputCls}
                  />
                </div>
                <div>
                  <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">默认动作 <span className="text-red-500">*</span></label>
                  <select
                    value={editForm.default_action}
                    onChange={(e) => setEditForm((d) => ({ ...d, default_action: e.target.value }))}
                    className={inputCls}
                  >
                    {DEFAULT_ACTIONS.map((a) => (
                      <option key={a.value} value={a.value}>{a.label}</option>
                    ))}
                  </select>
                </div>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button onClick={() => setEditGroup(null)} disabled={savingEdit} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-100 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700">取消</button>
              <button onClick={submitEdit} disabled={savingEdit} className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white">
                {savingEdit && <RefreshCw className="h-4 w-4 animate-spin" />}
                {savingEdit ? '保存中…' : '保存'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 安全组详情（规则列表）Modal */}
      {detailId && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/50 p-4 dark:bg-black/70 z-50">
          <div className="flex max-h-[85vh] w-full max-w-4xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">
                安全组详情{detail ? ` - ${detail.group.name}` : ''}
              </h3>
              <button onClick={closeDetail} className="rounded p-1 text-gray-400 hover:text-black dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              {detailLoading ? (
                <div className="flex items-center justify-center py-12">
                  <div className="h-6 w-6 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white" />
                </div>
              ) : detailError ? (
                <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{detailError}</div>
              ) : detail ? (
                <>
                  <div className="mb-4 flex flex-wrap items-center gap-x-6 gap-y-2 rounded-lg border border-gray-200 bg-gray-50 px-4 py-3 text-xs text-gray-600 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-300">
                    <span>租户：<span className="font-medium text-black dark:text-white">{tenantLabel(detail.group.tenant_id)}</span></span>
                    <span>默认动作：<ActionBadge action={detail.group.default_action} /></span>
                    <span className="font-mono text-gray-400">{detail.group.id}</span>
                  </div>

                  <div className="mb-2 flex items-center justify-between">
                    <h4 className="text-sm font-semibold text-black dark:text-white">规则（{detail.rules.length}）</h4>
                    <button onClick={() => { setShowRuleForm((v) => !v); setRuleError('') }} className="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-1.5 text-xs text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800">
                      <Plus className="h-3.5 w-3.5" />{showRuleForm ? '收起表单' : '添加规则'}
                    </button>
                  </div>

                  {showRuleForm && (
                    <div className="mb-4 rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-800/60">
                      {ruleError && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{ruleError}</div>}
                      <div className="grid gap-3 md:grid-cols-3">
                        <div>
                          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">方向 <span className="text-red-500">*</span></label>
                          <select value={ruleDraft.direction} onChange={(e) => setRuleDraft((d) => ({ ...d, direction: e.target.value }))} className={inputCls}>
                            {DIRECTIONS.map((d) => (<option key={d.value} value={d.value}>{d.label}</option>))}
                          </select>
                        </div>
                        <div>
                          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">协议 <span className="text-red-500">*</span></label>
                          <select value={ruleDraft.protocol} onChange={(e) => setRuleDraft((d) => ({ ...d, protocol: e.target.value }))} className={inputCls}>
                            {PROTOCOLS.map((p) => (<option key={p.value} value={p.value}>{p.label}</option>))}
                          </select>
                        </div>
                        <div>
                          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">动作 <span className="text-red-500">*</span></label>
                          <select value={ruleDraft.action} onChange={(e) => setRuleDraft((d) => ({ ...d, action: e.target.value }))} className={inputCls}>
                            {RULE_ACTIONS.map((a) => (<option key={a.value} value={a.value}>{a.label}</option>))}
                          </select>
                        </div>
                        <div>
                          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">源掩码</label>
                          <input type="text" value={ruleDraft.src_mask} onChange={(e) => setRuleDraft((d) => ({ ...d, src_mask: e.target.value }))} placeholder="0.0.0.0/0，留空表示任意" className={inputCls} />
                        </div>
                        <div>
                          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">目的掩码</label>
                          <input type="text" value={ruleDraft.dst_mask} onChange={(e) => setRuleDraft((d) => ({ ...d, dst_mask: e.target.value }))} placeholder="0.0.0.0/0，留空表示任意" className={inputCls} />
                        </div>
                        <div className="grid grid-cols-2 gap-3">
                          <div>
                            <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">源端口</label>
                            <input type="number" min={0} value={ruleDraft.src_port} onChange={(e) => setRuleDraft((d) => ({ ...d, src_port: e.target.value }))} placeholder="0 = 任意" className={inputCls} />
                          </div>
                          <div>
                            <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">目的端口</label>
                            <input type="number" min={0} value={ruleDraft.dst_port} onChange={(e) => setRuleDraft((d) => ({ ...d, dst_port: e.target.value }))} placeholder="0 = 任意" className={inputCls} />
                          </div>
                        </div>
                        <div>
                          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">优先级 <span className="text-red-500">*</span></label>
                          <input type="number" value={ruleDraft.priority} onChange={(e) => setRuleDraft((d) => ({ ...d, priority: e.target.value }))} placeholder="100（数值小优先）" className={inputCls} />
                        </div>
                        <div className="md:col-span-2">
                          <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">说明</label>
                          <input type="text" value={ruleDraft.description} onChange={(e) => setRuleDraft((d) => ({ ...d, description: e.target.value }))} placeholder="放行 HTTP 流量" className={inputCls} />
                        </div>
                      </div>
                      <div className="mt-3 flex justify-end gap-2">
                        <button onClick={() => { setShowRuleForm(false); setRuleError('') }} disabled={addingRule} className="rounded-md border border-gray-300 px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-100 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700">取消</button>
                        <button onClick={addRule} disabled={addingRule} className="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-1.5 text-xs text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white">
                          {addingRule && <RefreshCw className="h-3.5 w-3.5 animate-spin" />}
                          {addingRule ? '添加中…' : '添加规则'}
                        </button>
                      </div>
                    </div>
                  )}

                  <div className="overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
                    <table className="w-full min-w-[760px] text-sm">
                      <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                        <tr>
                          <th className="px-3 py-2.5 text-left font-medium">方向</th>
                          <th className="px-3 py-2.5 text-left font-medium">协议</th>
                          <th className="px-3 py-2.5 text-left font-medium">源掩码</th>
                          <th className="px-3 py-2.5 text-left font-medium">目的掩码</th>
                          <th className="px-3 py-2.5 text-left font-medium">端口</th>
                          <th className="px-3 py-2.5 text-left font-medium">动作</th>
                          <th className="px-3 py-2.5 text-left font-medium">优先级</th>
                          <th className="px-3 py-2.5 text-left font-medium">说明</th>
                          <th className="px-3 py-2.5 text-right font-medium">操作</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                        {detail.rules.length === 0 ? (
                          <tr>
                            <td colSpan={9} className="px-3 py-8 text-center text-sm text-gray-400">暂无规则，点击「添加规则」创建第一条规则</td>
                          </tr>
                        ) : detail.rules.map((rule) => (
                          <tr key={rule.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                            <td className="px-3 py-2.5"><DirectionBadge direction={rule.direction} /></td>
                            <td className="px-3 py-2.5 text-xs text-gray-700 dark:text-gray-300">{protocolLabel(rule.protocol)}</td>
                            <td className="px-3 py-2.5 font-mono text-xs text-gray-600 dark:text-gray-300">{rule.src_mask || '任意'}</td>
                            <td className="px-3 py-2.5 font-mono text-xs text-gray-600 dark:text-gray-300">{rule.dst_mask || '任意'}</td>
                            <td className="px-3 py-2.5 whitespace-nowrap text-xs text-gray-600 dark:text-gray-300">{fmtPort(rule.src_port)} → {fmtPort(rule.dst_port)}</td>
                            <td className="px-3 py-2.5"><ActionBadge action={rule.action} /></td>
                            <td className="px-3 py-2.5 text-xs font-medium text-black dark:text-white">{rule.priority}</td>
                            <td className="px-3 py-2.5 text-xs text-gray-600 dark:text-gray-300">{rule.description || '—'}</td>
                            <td className="px-3 py-2.5 text-right">
                              <button onClick={() => void removeRule(rule)} className="inline-flex items-center gap-1 rounded border border-red-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-950">
                                <Trash2 className="h-3 w-3" />删除
                              </button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </>
              ) : null}
            </div>
          </div>
        </div>
      )}

      {/* 容器绑定管理 Modal */}
      {bindOpen && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/50 p-4 dark:bg-black/70 z-50">
          <div className="flex max-h-[85vh] w-full max-w-lg flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-200 px-5 py-3 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">容器安全组绑定</h3>
              <button onClick={closeBind} className="rounded p-1 text-gray-400 hover:text-black dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              {bindError && <div className="mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{bindError}</div>}
              <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">选择容器</label>
              <select value={bindContainerId} onChange={(e) => void pickContainer(e.target.value)} className={inputCls} disabled={bindContainersLoading}>
                {bindContainersLoading ? (
                  <option value="">容器列表加载中…</option>
                ) : (
                  <>
                    <option value="">请选择容器</option>
                    {bindContainers.map((c) => (
                      <option key={c.id} value={String(c.id)}>{c.name || c.uuid}</option>
                    ))}
                  </>
                )}
              </select>

              {bindContainerId && (
                <div className="mt-4">
                  <label className="mb-1 block text-xs text-gray-500 dark:text-gray-400">
                    勾选要绑定的安全组（取消勾选即解绑，保存后生效）
                  </label>
                  {bindLoadingCurrent ? (
                    <div className="flex items-center justify-center py-6">
                      <div className="h-5 w-5 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white" />
                    </div>
                  ) : groups.length === 0 ? (
                    <div className="py-4 text-center text-xs text-gray-400">暂无安全组，请先创建安全组</div>
                  ) : (
                    <div className="space-y-2">
                      {groups.map((g) => (
                        <label key={g.id} className="flex cursor-pointer items-center gap-3 rounded-md border border-gray-200 px-3 py-2 text-sm hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800">
                          <input
                            type="checkbox"
                            checked={bindSelected.includes(g.id)}
                            onChange={() => toggleBind(g.id)}
                            className="h-4 w-4"
                          />
                          <span className="flex-1 text-gray-800 dark:text-gray-100">{g.name}</span>
                          <ActionBadge action={g.default_action} />
                        </label>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button onClick={closeBind} disabled={bindSaving} className="rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-600 hover:bg-gray-100 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-700">取消</button>
              <button onClick={submitBind} disabled={bindSaving || !bindContainerId} className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white">
                {bindSaving && <RefreshCw className="h-4 w-4 animate-spin" />}
                {bindSaving ? '保存中…' : '保存绑定'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
