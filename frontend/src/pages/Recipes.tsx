import { useCallback, useEffect, useState } from 'react'
import { BookOpen, Pencil, Play, Plus, RefreshCw, Save, Terminal, Trash2, X } from 'lucide-react'
import { useDialog } from '../components/Dialog'
import {
  createRecipe,
  deleteRecipe,
  executeRecipe,
  getContainers,
  listRecipes,
  updateRecipe,
  type Container,
  type Recipe,
} from '../services/api'

interface RecipeForm {
  name: string
  description: string
  script: string
  scope: string
}

const EMPTY_FORM: RecipeForm = { name: '', description: '', script: '', scope: 'private' }

interface ExecResult {
  recipe_name: string
  container: string
  output: string
  exit_code: number
  virtualization?: string
}

export default function Recipes() {
  const dialog = useDialog()
  const [recipes, setRecipes] = useState<Recipe[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  // 新建 / 编辑
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<Recipe | null>(null)
  const [form, setForm] = useState<RecipeForm>(EMPTY_FORM)
  const [saving, setSaving] = useState(false)

  // 执行
  const [execRecipe, setExecRecipe] = useState<Recipe | null>(null)
  const [execContainers, setExecContainers] = useState<Container[]>([])
  const [execContainersLoading, setExecContainersLoading] = useState(false)
  const [execContainerId, setExecContainerId] = useState<number | ''>('')
  const [execTimeout, setExecTimeout] = useState(300)
  const [executing, setExecuting] = useState(false)
  const [execResult, setExecResult] = useState<ExecResult | null>(null)

  const fetchRecipes = useCallback(async () => {
    try {
      const res = await listRecipes()
      setRecipes(res.data.data || [])
      setError('')
    } catch (err: unknown) {
      setError((err as { response?: { data?: { message?: string } } }).response?.data?.message || '加载脚本模板失败')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { fetchRecipes() }, [fetchRecipes])

  // ---- 新建 / 编辑 -------------------------------------------------------------
  const openCreate = () => {
    setEditing(null)
    setForm(EMPTY_FORM)
    setFormOpen(true)
  }

  const openEdit = (recipe: Recipe) => {
    setEditing(recipe)
    setForm({
      name: recipe.name,
      description: recipe.description || '',
      script: recipe.script,
      scope: recipe.scope === 'shared' ? 'shared' : 'private',
    })
    setFormOpen(true)
  }

  const closeForm = () => {
    setFormOpen(false)
    setEditing(null)
    setForm(EMPTY_FORM)
  }

  const submitForm = async () => {
    if (saving) return
    if (!form.name.trim()) {
      dialog.alert(editing ? '保存失败' : '创建失败', '名称不能为空')
      return
    }
    if (!form.script.trim()) {
      dialog.alert(editing ? '保存失败' : '创建失败', '脚本正文不能为空')
      return
    }
    setSaving(true)
    try {
      const payload = {
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        script: form.script,
        scope: form.scope,
      }
      if (editing) {
        await updateRecipe(editing.id, payload)
        dialog.alert('完成', '脚本模板已保存')
      } else {
        await createRecipe(payload)
        dialog.alert('完成', '脚本模板已创建')
      }
      closeForm()
      await fetchRecipes()
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert(editing ? '保存失败' : '创建失败', e.response?.data?.message || '请稍后重试')
    } finally {
      setSaving(false)
    }
  }

  // ---- 删除 --------------------------------------------------------------------
  const remove = async (recipe: Recipe) => {
    const ok = await dialog.confirm('删除脚本模板', `确定删除「${recipe.name}」？该操作不可恢复。`)
    if (!ok) return
    try {
      await deleteRecipe(recipe.id)
      await fetchRecipes()
      dialog.alert('完成', '脚本模板已删除')
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('删除失败', e.response?.data?.message || '请稍后重试')
    }
  }

  // ---- 执行 --------------------------------------------------------------------
  const openExec = async (recipe: Recipe) => {
    setExecRecipe(recipe)
    setExecContainerId('')
    setExecTimeout(300)
    setExecResult(null)
    setExecContainersLoading(true)
    try {
      const res = await getContainers()
      // 后端要求容器必须为 running 状态
      setExecContainers((res.data.data || []).filter((c) => c.status === 'running'))
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      setExecContainers([])
      dialog.alert('加载失败', e.response?.data?.message || '获取容器列表失败')
    } finally {
      setExecContainersLoading(false)
    }
  }

  const closeExec = () => {
    if (executing) return
    setExecRecipe(null)
    setExecContainers([])
    setExecContainerId('')
    setExecTimeout(300)
    setExecResult(null)
  }

  const submitExec = async () => {
    if (!execRecipe || executing) return
    if (execContainerId === '' || execContainerId <= 0) {
      dialog.alert('执行失败', '请选择一个运行中的容器')
      return
    }
    const timeout = Number(execTimeout)
    setExecuting(true)
    setExecResult(null)
    try {
      const res = await executeRecipe(execRecipe.id, execContainerId, Number.isFinite(timeout) && timeout > 0 ? timeout : undefined)
      setExecResult(res.data.data || null)
    } catch (err: unknown) {
      const e = err as { response?: { data?: { message?: string } } }
      dialog.alert('执行失败', e.response?.data?.message || '脚本执行失败，请稍后重试')
    } finally {
      setExecuting(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-black" />
      </div>
    )
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-black dark:text-white">
            <BookOpen className="h-5 w-5" />脚本模板
          </h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            管理可一键应用到容器的脚本模板（Recipe），shared 模板对子用户可见
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { setRefreshing(true); fetchRecipes() }}
            disabled={refreshing}
            className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          >
            <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />刷新
          </button>
          <button onClick={openCreate} className="inline-flex items-center gap-2 rounded-md bg-black px-3 py-2 text-sm font-medium text-white hover:bg-gray-800 dark:bg-white dark:text-black dark:hover:bg-gray-200">
            <Plus className="h-4 w-4" />新建模板
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">{error}</div>
      )}

      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        {recipes.length === 0 ? (
          <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-lg bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400">
              <BookOpen className="h-7 w-7" />
            </div>
            <div className="text-sm font-medium text-gray-700 dark:text-gray-300">暂无脚本模板</div>
            <div className="mt-1 text-xs text-gray-400">点击右上角「新建模板」创建第一个脚本</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[900px] text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">名称</th>
                  <th className="px-4 py-3 text-left font-medium">说明</th>
                  <th className="px-4 py-3 text-left font-medium">归属</th>
                  <th className="px-4 py-3 text-left font-medium">可见范围</th>
                  <th className="px-4 py-3 text-left font-medium">更新时间</th>
                  <th className="px-4 py-3 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                {recipes.map((recipe) => (
                  <tr key={recipe.id} className="hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="px-4 py-3">
                      <div className="font-medium text-black dark:text-white">{recipe.name}</div>
                      <div className="max-w-[220px] truncate font-mono text-xs text-gray-400" title={recipe.script.slice(0, 200)}>
                        {recipe.script.split('\n')[0].slice(0, 60) || '—'}
                      </div>
                    </td>
                    <td className="px-4 py-3 max-w-[240px]">
                      <span className="text-xs text-gray-600 dark:text-gray-300">{recipe.description || '—'}</span>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-1.5">
                        <span className={`inline-flex items-center rounded px-1.5 py-0.5 text-xs font-medium ${recipe.owner_type === 'admin' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'}`}>
                          {recipe.owner_type === 'admin' ? '管理员' : '子用户'}
                        </span>
                        <span className="font-mono text-xs text-gray-400" title={recipe.owner_id}>{recipe.owner_id}</span>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      {recipe.scope === 'shared' ? (
                        <span className="inline-flex items-center rounded bg-indigo-50 px-2 py-0.5 text-xs font-medium text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300">共享</span>
                      ) : (
                        <span className="inline-flex items-center rounded bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-500 dark:bg-gray-800 dark:text-gray-400">私有</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-400">{recipe.updated_at || recipe.created_at || '—'}</td>
                    <td className="px-4 py-3">
                      <div className="flex flex-wrap items-center justify-end gap-1">
                        <button
                          onClick={() => { void openExec(recipe) }}
                          className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-emerald-600 hover:bg-emerald-50 dark:text-emerald-400 dark:hover:bg-emerald-900/30 transition-colors"
                          title="在容器上执行此脚本"
                        >
                          <Play className="h-3.5 w-3.5" />
                          执行
                        </button>
                        <button
                          onClick={() => openEdit(recipe)}
                          className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 transition-colors"
                          title="编辑脚本模板"
                        >
                          <Pencil className="h-3.5 w-3.5" />
                          编辑
                        </button>
                        <button
                          onClick={() => { void remove(recipe) }}
                          className="inline-flex items-center gap-1 px-2 py-1.5 rounded text-xs text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/30 transition-colors"
                          title="删除脚本模板"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          删除
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

      {/* 新建 / 编辑模板弹窗 */}
      {formOpen && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-lg border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-2xl max-h-[85vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">{editing ? '编辑脚本模板' : '新建脚本模板'}</h3>
              <button onClick={closeForm} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-5 space-y-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">名称 <span className="text-red-500">*</span></label>
                  <input
                    type="text"
                    value={form.name}
                    onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                    placeholder="安装 Docker"
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  />
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">可见范围</label>
                  <select
                    value={form.scope}
                    onChange={(e) => setForm((f) => ({ ...f, scope: e.target.value }))}
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  >
                    <option value="private">私有（仅管理员自己可见）</option>
                    <option value="shared">共享（对子用户可见）</option>
                  </select>
                </div>
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">说明（可选）</label>
                <input
                  type="text"
                  value={form.description}
                  onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                  placeholder="在容器内安装并启动 Docker"
                  className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">脚本正文 <span className="text-red-500">*</span></label>
                <textarea
                  value={form.script}
                  onChange={(e) => setForm((f) => ({ ...f, script: e.target.value }))}
                  placeholder="#!/bin/sh&#10;echo hello"
                  rows={10}
                  spellCheck={false}
                  className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100 px-3 py-2 font-mono text-xs leading-relaxed outline-none focus:border-gray-400"
                />
                <p className="mt-1 text-xs text-gray-400">危险脚本（如 rm -rf / 等）将被后端拒绝</p>
              </div>
            </div>
            <div className="flex items-center justify-end gap-2 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
              <button onClick={closeForm} disabled={saving} className="px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 rounded-md">取消</button>
              <button
                onClick={() => { void submitForm() }}
                disabled={saving}
                className="inline-flex items-center gap-1.5 px-3 py-2 text-sm bg-black text-white rounded-md hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black dark:hover:bg-gray-200"
              >
                <Save className="h-4 w-4" />
                {saving ? '保存中...' : (editing ? '保存' : '创建')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 执行弹窗 */}
      {execRecipe && (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/70 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-gray-900 rounded-lg border border-gray-200 dark:border-gray-700 shadow-xl w-full max-w-2xl max-h-[85vh] overflow-hidden flex flex-col">
            <div className="flex items-center justify-between px-5 py-3 border-b border-gray-200 dark:border-gray-700">
              <div>
                <h3 className="text-sm font-semibold text-black dark:text-white">执行脚本</h3>
                <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{execRecipe.name} · 仅支持运行中（running）的容器</p>
              </div>
              <button onClick={closeExec} disabled={executing} className="p-1 text-gray-400 hover:text-black dark:hover:text-white rounded disabled:opacity-50">
                <X className="w-4 h-4" />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-5 space-y-4">
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="sm:col-span-2">
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">目标容器 <span className="text-red-500">*</span></label>
                  {execContainersLoading ? (
                    <div className="flex items-center gap-2 rounded-md border border-gray-200 dark:border-gray-700 px-2.5 py-2 text-sm text-gray-400">
                      <RefreshCw className="h-3.5 w-3.5 animate-spin" />加载容器中…
                    </div>
                  ) : (
                    <select
                      value={execContainerId}
                      onChange={(e) => setExecContainerId(e.target.value === '' ? '' : Number(e.target.value))}
                      className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                    >
                      <option value="">请选择容器</option>
                      {execContainers.map((c) => (
                        <option key={c.uuid || c.id} value={c.id}>
                          {c.name}（#{c.id}{c.virtualization ? ` · ${c.virtualization}` : ''}）
                        </option>
                      ))}
                    </select>
                  )}
                  {!execContainersLoading && execContainers.length === 0 && (
                    <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">当前没有运行中的容器，请先启动容器再执行</p>
                  )}
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">超时（秒，可选）</label>
                  <input
                    type="number"
                    min={1}
                    value={execTimeout}
                    onChange={(e) => setExecTimeout(Number(e.target.value))}
                    className="w-full rounded-md border border-gray-200 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100 px-2.5 py-1.5 text-sm outline-none focus:border-gray-400"
                  />
                </div>
              </div>

              <div>
                <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">
                  <Terminal className="h-3.5 w-3.5" />执行输出
                </div>
                {executing ? (
                  <div className="flex flex-col items-center justify-center rounded-md border border-gray-200 bg-gray-50 py-10 text-center dark:border-gray-700 dark:bg-gray-950">
                    <div className="h-7 w-7 animate-spin rounded-full border-b-2 border-black dark:border-white" />
                    <div className="mt-3 text-sm text-gray-600 dark:text-gray-300">脚本执行中，耗时较长请耐心等待…</div>
                  </div>
                ) : execResult ? (
                  <div className="space-y-2">
                    <div className="flex flex-wrap items-center gap-2 text-xs">
                      <span className="text-gray-500 dark:text-gray-400">容器：{execResult.container || '—'}</span>
                      <span className={`inline-flex items-center rounded px-2 py-0.5 font-medium ${execResult.exit_code === 0 ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400'}`}>
                        退出码 {execResult.exit_code}
                      </span>
                      {execResult.virtualization && (
                        <span className="inline-flex items-center rounded bg-gray-100 px-2 py-0.5 font-medium text-gray-500 dark:bg-gray-800 dark:text-gray-400">{execResult.virtualization}</span>
                      )}
                    </div>
                    <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md border border-gray-200 bg-gray-50 p-3 font-mono text-xs leading-relaxed text-gray-800 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-200">
                      {execResult.output || '（无输出）'}
                    </pre>
                  </div>
                ) : (
                  <div className="rounded-md border border-dashed border-gray-300 py-8 text-center text-sm text-gray-400 dark:border-gray-700">
                    选择容器后点击「开始执行」查看输出
                  </div>
                )}
              </div>
            </div>
            <div className="flex items-center justify-between gap-3 border-t border-gray-200 dark:border-gray-700 px-5 py-3">
              <span className="text-xs text-gray-400">执行可能耗时较长，请勿重复提交</span>
              <div className="flex items-center gap-2">
                <button onClick={closeExec} disabled={executing} className="px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800 rounded-md disabled:opacity-50">关闭</button>
                <button
                  onClick={() => { void submitExec() }}
                  disabled={executing || execContainersLoading || execContainerId === ''}
                  className="inline-flex items-center gap-1.5 px-3 py-2 text-sm bg-black text-white rounded-md hover:bg-gray-800 disabled:opacity-50 dark:bg-white dark:text-black dark:hover:bg-gray-200"
                >
                  <Play className="h-4 w-4" />
                  {executing ? '执行中...' : '开始执行'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
