import { useCallback, useEffect, useState } from 'react'
import {
  Boxes,
  Copy,
  Cpu,
  Eye,
  HardDrive,
  KeyRound,
  Loader2,
  MemoryStick,
  Monitor,
  Network,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  RotateCw,
  Server,
  Square,
  Trash2,
  X,
} from 'lucide-react'
import {
  createCluster,
  createNode,
  createNodeContainer,
  createNodeGroup,
  deleteCluster,
  deleteNode,
  deleteNodeGroup,
  getEnabledImages,
  getNodeContainers,
  getNodeImages,
  getNodeInstallCommand,
  getNodes,
  getRegions,
  listClusters,
  listNodeGroups,
  nodeColdBackup,
  nodeContainerAction,
  syncNodeImages,
  updateCluster,
  updateNodeGroup,
  type Cluster,
  type NodeInstallCommand,
  type Container,
  type CreateContainerRequest,
  type ManagedNode,
  type NodeCatalogImage,
  type NodeGroup,
  type Region,
  type Template,
} from '../services/api'
import { useDialog } from '../components/Dialog'
import { useLanguage } from '../contexts/LanguageContext'

function formatMB(mb?: number) {
  if (!mb || mb <= 0) return '-'
  if (mb >= 1024 * 1024) return `${(mb / 1024 / 1024).toFixed(1)} TB`
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`
  return `${Math.round(mb)} MB`
}

function formatGB(gb?: number) {
  if (gb === undefined || gb === null || gb <= 0) return '-'
  if (gb >= 1024) return `${(gb / 1024).toFixed(1)} TB`
  return `${gb.toFixed(1)} GB`
}

function formatGBBytes(bytes?: number) {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = bytes
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

// 后端 Node 结构带有 node_group_id / cluster_id / region_id（omitempty），
// ManagedNode 类型未声明这些字段，这里用扩展类型读取分组归属。
type GroupedNode = ManagedNode & {
  node_group_id?: string
  cluster_id?: string
  region_id?: string
}

export default function NodeManagement() {
  const { t } = useLanguage()
  const { confirm, alert } = useDialog()
  const [nodes, setNodes] = useState<ManagedNode[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  // 添加模式：quick=一键添加（生成一行安装命令）；manual=手动添加（录入地址信息）
  const [createMode, setCreateMode] = useState<'quick' | 'manual'>('quick')
  const [newName, setNewName] = useState('')
  const [newAddress, setNewAddress] = useState('')
  const [newBindIP, setNewBindIP] = useState('')
  const [creating, setCreating] = useState(false)
  // 一行安装命令（curl | sudo bash）：只展示命令，不展示脚本正文
  const [cmdNode, setCmdNode] = useState<ManagedNode | null>(null)
  const [cmdInfo, setCmdInfo] = useState<NodeInstallCommand | null>(null)
  const [cmdLoading, setCmdLoading] = useState(false)
  const [detailNode, setDetailNode] = useState<ManagedNode | null>(null)
  const [nodeContainers, setNodeContainers] = useState<Container[]>([])
  const [detailLoading, setDetailLoading] = useState(false)
  const [busyId, setBusyId] = useState<string>('')

  // 被控节点镜像同步
  const [nodeImages, setNodeImages] = useState<{ lxc?: NodeCatalogImage[]; kvm?: NodeCatalogImage[] }>({})
  const [imagesLoading, setImagesLoading] = useState(false)
  const [syncingImages, setSyncingImages] = useState(false)

  // 在主控上对指定被控节点开通（发机）容器
  const [createTarget, setCreateTarget] = useState<ManagedNode | null>(null)
  const [templates, setTemplates] = useState<Template[]>([])
  const [createForm, setCreateForm] = useState<CreateContainerRequest | null>(null)
  const [creatingContainer, setCreatingContainer] = useState(false)

  // 节点分组与集群（迁移池 / 策略池）
  const [nodeGroups, setNodeGroups] = useState<NodeGroup[]>([])
  const [clusters, setClusters] = useState<Cluster[]>([])
  const [regions, setRegions] = useState<Region[]>([])

  // 节点分组弹窗（groupEditTarget 为 null 表示新建，否则编辑该分组）
  const [groupFormOpen, setGroupFormOpen] = useState(false)
  const [groupEditTarget, setGroupEditTarget] = useState<NodeGroup | null>(null)
  const [groupName, setGroupName] = useState('')
  const [groupDesc, setGroupDesc] = useState('')
  const [groupRegionId, setGroupRegionId] = useState('')
  // 勾选的成员节点（整体替换语义：保存时提交 node_ids）
  const [groupNodeIds, setGroupNodeIds] = useState<string[]>([])
  const [groupSaving, setGroupSaving] = useState(false)

  // 集群弹窗（clusterEditTarget 为 null 表示新建，否则编辑该集群）
  const [clusterFormOpen, setClusterFormOpen] = useState(false)
  const [clusterEditTarget, setClusterEditTarget] = useState<Cluster | null>(null)
  const [clusterName, setClusterName] = useState('')
  const [clusterDesc, setClusterDesc] = useState('')
  const [clusterRegionIds, setClusterRegionIds] = useState<string[]>([])
  const [clusterNodeIds, setClusterNodeIds] = useState<string[]>([])
  const [clusterSaving, setClusterSaving] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const res = await getNodes()
      setNodes(res.data.data || [])
    } catch {
      // 保留上次数据
    } finally {
      setLoading(false)
    }
  }, [])

  // 拉取节点分组 / 集群 / 区域（供分组与集群管理使用）
  const refreshGroups = useCallback(async () => {
    try {
      const [groupsRes, clustersRes, regionsRes] = await Promise.all([listNodeGroups(), listClusters(), getRegions()])
      setNodeGroups(groupsRes.data.data || [])
      setClusters(clustersRes.data.data || [])
      setRegions(regionsRes.data.data || [])
    } catch {
      // 保留上次数据
    }
  }, [])

  useEffect(() => {
    refresh()
    refreshGroups()
    const timer = window.setInterval(refresh, 8000)
    return () => window.clearInterval(timer)
  }, [refresh, refreshGroups])

  const handleCreate = async () => {
    const name = newName.trim()
    const address = newAddress.trim()
    const bindIP = newBindIP.trim()
    if (!name && !address) {
      await alert(t('提示'), t('请填写节点名称或地址'))
      return
    }
    setCreating(true)
    try {
      const res = await createNode(name || undefined, address || undefined, bindIP || undefined)
      const created = res.data.data
      setShowCreate(false)
      setNewName('')
      setNewAddress('')
      setNewBindIP('')
      refresh()
      // 创建成功后直接展示一行安装命令（不展示脚本正文）
      if (created?.node) {
        await loadInstallCommand(created.node)
      }
    } catch (e: any) {
      await alert(t('创建失败'), e?.response?.data?.message || String(e))
    } finally {
      setCreating(false)
    }
  }

  // loadInstallCommand 拉取一行安装命令（curl | sudo bash）。
  // 面板不展示/复制完整 bash 脚本正文，只下发命令与 key 元信息。
  const loadInstallCommand = async (node: ManagedNode) => {
    setCmdNode(node)
    setCmdInfo(null)
    setCmdLoading(true)
    try {
      const res = await getNodeInstallCommand(node.id)
      setCmdInfo(res.data.data ?? null)
    } catch (e: any) {
      await alert(t('获取安装命令失败'), e?.response?.data?.message || String(e))
      setCmdNode(null)
    } finally {
      setCmdLoading(false)
    }
  }

  const copyCommand = async () => {
    if (!cmdInfo) return
    const text = cmdInfo.command
    try {
      await navigator.clipboard.writeText(text)
      await alert(t('已复制'), t('安装命令已复制，请在被控服务器上执行'))
    } catch {
      const ta = document.createElement('textarea')
      ta.value = text
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
      await alert(t('已复制'), t('安装命令已复制，请在被控服务器上执行'))
    }
  }

  const handleDelete = async (node: ManagedNode) => {
    const ok = await confirm(t('删除节点'), `${t('确定删除节点')} ${node.name}？`)
    if (!ok) return
    try {
      await deleteNode(node.id)
      await alert(t('完成'), t('节点已删除'))
      refresh()
    } catch (e: any) {
      await alert(t('删除失败'), e?.response?.data?.message || String(e))
    }
  }

  const loadDetail = async (node: ManagedNode) => {
    setDetailNode(node)
    setNodeContainers([])
    setNodeImages({})
    setDetailLoading(true)
    try {
      const res = await getNodeContainers(node.id)
      setNodeContainers(res.data.data || [])
    } catch (e: any) {
      await alert(t('加载失败'), e?.response?.data?.message || String(e))
    } finally {
      setDetailLoading(false)
    }
    loadImages(node)
  }

  const runAction = async (container: Container, action: 'start' | 'stop' | 'restart') => {
    if (!detailNode) return
    setBusyId(`${container.id}:${action}`)
    try {
      await nodeContainerAction(detailNode.id, container.id, action)
      await loadDetail(detailNode)
    } catch (e: any) {
      await alert(t('操作失败'), e?.response?.data?.message || String(e))
    } finally {
      setBusyId('')
    }
  }

  // 重置子容器 SSH 密码：母面板下发指令，子端自动生成新密码并回传。
  const resetNodePassword = async (container: Container) => {
    if (!detailNode) return
    setBusyId(`${container.id}:reset-password`)
    try {
      const res = await nodeContainerAction(detailNode.id, container.id, 'reset-password')
      const password = (res.data?.data as { password?: string } | undefined)?.password || ''
      await alert(
        t('SSH 密码已重置'),
        password
          ? `${t('容器')} ${container.name} 的新 SSH 密码为：\n\n${password}\n\n${t('请立即复制保存，关闭后不再显示。')}`
          : `${container.name} 的 SSH 密码已重置`
      )
      await loadDetail(detailNode)
    } catch (e: any) {
      await alert(t('操作失败'), e?.response?.data?.message || String(e))
    } finally {
      setBusyId('')
    }
  }

  // 查询被控节点当前镜像清单
  const loadImages = async (node: ManagedNode) => {
    setImagesLoading(true)
    try {
      const res = await getNodeImages(node.id)
      setNodeImages(res.data.data || {})
    } catch (e: any) {
      await alert(t('加载镜像失败'), e?.response?.data?.message || String(e))
    } finally {
      setImagesLoading(false)
    }
  }

  // 把主控的镜像清单下发给被控并触发同步
  const syncImages = async (node: ManagedNode) => {
    if (syncingImages) return
    setSyncingImages(true)
    try {
      const res = await syncNodeImages(node.id)
      const d = res.data?.data
      await alert(
        t('镜像同步完成'),
        `${t('新增')} ${d?.added ?? 0} · ${t('更新')} ${d?.updated ?? 0} · ${t('拉取')} ${d?.pulled?.length ?? 0}${(d?.failed?.length ?? 0) > 0 ? ` · ${t('失败')} ${d?.failed?.length}` : ''}`
      )
      await loadImages(node)
    } catch (e: any) {
      await alert(t('镜像同步失败'), e?.response?.data?.message || String(e))
    } finally {
      setSyncingImages(false)
    }
  }

  // 节点级冷备份：触发被控对其全部容器做完整备份（重装/重建前保全）。
  const coldBackupNode = async (node: ManagedNode) => {
    const ok = await confirm(t('节点冷备份'), `${t('将对该节点全部容器创建完整备份（会逐个临时停机）。是否继续？')}`)
    if (!ok) return
    setBusyId('node-cold-backup')
    try {
      const res = await nodeColdBackup(node.id)
      const d = res.data?.data
      await alert(
        t('冷备份完成'),
        `${t('已备份')} ${d?.backed_up ?? 0}/${d?.container_cnt ?? 0} 个容器${(d?.total_bytes ?? 0) > 0 ? `，${formatGBBytes(d?.total_bytes ?? 0)}` : ''}${(d?.failed?.length ?? 0) > 0 ? `，${t('失败')} ${d?.failed?.length}` : ''}`
      )
    } catch (e: any) {
      await alert(t('冷备份失败'), e?.response?.data?.message || String(e))
    } finally {
      setBusyId('')
    }
  }

  // 打开对该被控节点的发机表单
  const openCreateContainer = async (node: ManagedNode) => {
    setCreateTarget(node)
    setCreateForm({
      name: '',
      virtualization: 'lxc',
      template_id: '',
      storage_pool_id: '',
      vcpu: 1,
      cpu_percent: 100,
      ram_mb: 512,
      disk_gb: 10,
      data_disk_gb: 0,
      data_disk_mount_path: '',
      network_bw_mbps: 0,
      network_down_mbps: 0,
      network_up_mbps: 0,
      monthly_traffic_gb: 0,
      traffic_mode: 'total',
      traffic_in_gb: 0,
      traffic_out_gb: 0,
      io_speed_mbps: 0,
      io_read_mbps: 0,
      io_write_mbps: 0,
      extra_ports: [],
      nat_port_mappings: [],
      management_port: 0,
      port_mapping_count: 2,
      assign_nat: true,
      lan_ipv4_mode: '',
      lan_interface: '',
      lan_ipv4_address: '',
      lan_ipv4_prefix_len: 24,
      lan_ipv4_gateway: '',
      snapshot_limit: 1,
      assign_ipv4: false,
      ipv4_count: 1,
      public_ipv4s: [],
      assign_ipv6: false,
      ipv6_count: 1,
      ipv6_addresses: [],
      ssh_auth_mode: 'auto_password',
      ssh_password: '',
      ssh_public_key: '',
      cloud_init_user_data: '',
      allowed_image_ids: [],
      image_limit_configured: false,
      expires_at: '',
    } as CreateContainerRequest)
    try {
      const res = await getEnabledImages('lxc')
      setTemplates(res.data.data || [])
    } catch {
      setTemplates([])
    }
  }

  // 在主控上对被控节点发起创建（发机）
  const handleCreateContainer = async () => {
    if (!createTarget || !createForm) return
    const name = createForm.name.trim()
    if (!name) {
      await alert(t('提示'), t('请填写容器名称'))
      return
    }
    if (!createForm.template_id) {
      await alert(t('提示'), t('请选择镜像模板'))
      return
    }
    setCreatingContainer(true)
    try {
      await createNodeContainer(createTarget.id, createForm)
      await alert(t('完成'), t('已在被控节点开通新容器'))
      setCreateTarget(null)
      if (detailNode?.id === createTarget.id) await loadDetail(detailNode)
    } catch (e: any) {
      await alert(t('发机失败'), e?.response?.data?.message || String(e))
    } finally {
      setCreatingContainer(false)
    }
  }

  // ---- 节点分组 ----

  // 分组成员 = 分组归属（node_group_id）指向该分组的节点，随节点列表自动刷新。
  const groupMembers = (groupId: string) => nodes.filter((n) => (n as GroupedNode).node_group_id === groupId)

  const openGroupCreate = () => {
    setGroupEditTarget(null)
    setGroupName('')
    setGroupDesc('')
    setGroupRegionId('')
    setGroupNodeIds([])
    setGroupFormOpen(true)
  }

  const openGroupEdit = (group: NodeGroup) => {
    setGroupEditTarget(group)
    setGroupName(group.name)
    setGroupDesc(group.description || '')
    setGroupRegionId(group.region_id || '')
    setGroupNodeIds(groupMembers(group.id).map((n) => n.id))
    setGroupFormOpen(true)
  }

  const toggleGroupNode = (nodeId: string) => {
    setGroupNodeIds((prev) =>
      prev.includes(nodeId) ? prev.filter((id) => id !== nodeId) : [...prev, nodeId]
    )
  }

  const saveNodeGroup = async () => {
    const name = groupName.trim()
    if (!name) {
      await alert(t('提示'), t('请填写分组名称'))
      return
    }
    setGroupSaving(true)
    try {
      // node_ids 整体替换成员集合（后端 PUT：非 nil 即生效）
      const payload = { name, description: groupDesc.trim(), region_id: groupRegionId, node_ids: groupNodeIds }
      if (groupEditTarget) {
        await updateNodeGroup(groupEditTarget.id, payload)
        await alert(t('完成'), t('节点分组已更新'))
      } else {
        await createNodeGroup(payload)
        await alert(t('完成'), t('节点分组已创建'))
      }
      setGroupFormOpen(false)
      refreshGroups()
      refresh()
    } catch (e: any) {
      await alert(t('保存失败'), e?.response?.data?.message || String(e))
    } finally {
      setGroupSaving(false)
    }
  }

  const removeNodeGroup = async (group: NodeGroup) => {
    const ok = await confirm(t('删除节点分组'), `${t('确定删除节点分组')} ${group.name}？`)
    if (!ok) return
    try {
      await deleteNodeGroup(group.id)
      await alert(t('完成'), t('节点分组已删除'))
      refreshGroups()
    } catch (e: any) {
      await alert(t('删除失败'), e?.response?.data?.message || String(e))
    }
  }

  // ---- 集群 ----

  // 集群成员 = 归属（cluster_id）指向该集群的节点，随节点列表自动刷新。
  const clusterMembers = (clusterId: string) => nodes.filter((n) => (n as GroupedNode).cluster_id === clusterId)

  const openClusterCreate = () => {
    setClusterEditTarget(null)
    setClusterName('')
    setClusterDesc('')
    setClusterRegionIds([])
    setClusterNodeIds([])
    setClusterFormOpen(true)
  }

  const openClusterEdit = (cluster: Cluster) => {
    setClusterEditTarget(cluster)
    setClusterName(cluster.name)
    setClusterDesc(cluster.description || '')
    setClusterRegionIds(cluster.region_ids || [])
    setClusterNodeIds(clusterMembers(cluster.id).map((n) => n.id))
    setClusterFormOpen(true)
  }

  const toggleClusterRegion = (regionId: string) => {
    setClusterRegionIds((prev) =>
      prev.includes(regionId) ? prev.filter((id) => id !== regionId) : [...prev, regionId]
    )
  }

  const toggleClusterNode = (nodeId: string) => {
    setClusterNodeIds((prev) =>
      prev.includes(nodeId) ? prev.filter((id) => id !== nodeId) : [...prev, nodeId]
    )
  }

  const saveCluster = async () => {
    const name = clusterName.trim()
    if (!name) {
      await alert(t('提示'), t('请填写集群名称'))
      return
    }
    setClusterSaving(true)
    try {
      const payload = { name, description: clusterDesc.trim(), region_ids: clusterRegionIds, node_ids: clusterNodeIds }
      if (clusterEditTarget) {
        await updateCluster(clusterEditTarget.id, payload)
        await alert(t('完成'), t('集群已更新'))
      } else {
        await createCluster(payload)
        await alert(t('完成'), t('集群已创建'))
      }
      setClusterFormOpen(false)
      refreshGroups()
      refresh()
    } catch (e: any) {
      await alert(t('保存失败'), e?.response?.data?.message || String(e))
    } finally {
      setClusterSaving(false)
    }
  }

  const removeCluster = async (cluster: Cluster) => {
    const ok = await confirm(t('删除集群'), `${t('确定删除集群')} ${cluster.name}？`)
    if (!ok) return
    try {
      await deleteCluster(cluster.id)
      await alert(t('完成'), t('集群已删除'))
      refreshGroups()
    } catch (e: any) {
      await alert(t('删除失败'), e?.response?.data?.message || String(e))
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold text-black dark:text-white">{t('节点管理')}</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {t('主控节点：统一管理多台被控服务器（一键安装 Agent 后自动接入）')}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button
            onClick={() => { refresh(); refreshGroups() }}
            className="flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            {t('刷新')}
          </button>
          <button
            onClick={() => setShowCreate(true)}
            className="flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-brand-700 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
          >
            <Plus className="h-3.5 w-3.5" />
            {t('添加节点')}
          </button>
        </div>
      </div>

      {loading ? (
        <div className="flex items-center justify-center py-20">
          <div className="h-8 w-8 animate-spin rounded-full border-b-2 border-brand-600 dark:border-white"></div>
        </div>
      ) : nodes.length === 0 ? (
        <div className="rounded-lg border border-gray-200 bg-white py-16 text-center dark:border-gray-700 dark:bg-gray-900">
          <Server className="mx-auto h-10 w-10 text-gray-300 dark:text-gray-600" />
          <p className="mt-3 text-sm text-gray-500 dark:text-gray-400">{t('暂无被控节点')}</p>
          <button
            onClick={() => setShowCreate(true)}
            className="mt-4 inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-4 py-2 text-sm text-white dark:bg-brand-500 dark:text-white"
          >
            <Plus className="h-4 w-4" />
            {t('添加第一个节点')}
          </button>
        </div>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
          <table className="w-full min-w-[820px] text-left text-sm">
            <thead>
              <tr className="border-b border-gray-200 text-xs text-gray-500 dark:border-gray-700 dark:text-gray-400">
                <th className="px-4 py-3 font-medium">{t('节点')}</th>
                <th className="px-4 py-3 font-medium">{t('地址')}</th>
                <th className="px-4 py-3 font-medium">{t('状态')}</th>
                <th className="px-4 py-3 font-medium">{t('资源')}</th>
                <th className="px-4 py-3 font-medium">{t('容器')}</th>
                <th className="px-4 py-3 font-medium">{t('最后心跳')}</th>
                <th className="px-4 py-3 text-right font-medium">{t('操作')}</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((node) => {
                const online = node.status === 'online'
                return (
                  <tr key={node.id} className="border-b border-gray-100 last:border-0 dark:border-gray-800">
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2">
                        <Monitor className="h-4 w-4 shrink-0 text-gray-400" />
                        <div className="min-w-0">
                          <div className="truncate font-medium text-black dark:text-white">{node.name}</div>
                          <div className="text-xs text-gray-400">{node.version || node.id}</div>
                        </div>
                      </div>
                    </td>
                    <td className="max-w-[200px] truncate px-4 py-3 text-gray-600 dark:text-gray-300" title={node.address}>
                      {node.address || '-'}
                    </td>
                    <td className="px-4 py-3">
                      <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ${online ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400'}`}>
                        <span className={`h-1.5 w-1.5 rounded-full ${online ? 'bg-emerald-500' : 'bg-gray-400'}`} />
                        {online ? t('在线') : node.status === 'pending' ? t('待接入') : t('离线')}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                      {node.cpu_count ? (
                        <div className="space-y-0.5">
                          <div className="flex items-center gap-1"><Cpu className="h-3 w-3 text-gray-400" /> {node.cpu_count} {t('核')}</div>
                          <div className="flex items-center gap-1"><MemoryStick className="h-3 w-3 text-gray-400" /> {formatMB(node.ram_used_mb)} / {formatMB(node.ram_total_mb)}</div>
                          <div className="flex items-center gap-1"><HardDrive className="h-3 w-3 text-gray-400" /> {formatGB(node.disk_used_gb)} / {formatGB(node.disk_total_gb)}</div>
                        </div>
                      ) : (
                        '-'
                      )}
                    </td>
                    <td className="px-4 py-3 text-gray-600 dark:text-gray-300">{node.container_count ?? '-'}</td>
                    <td className="px-4 py-3 text-xs text-gray-400">{node.last_seen || '-'}</td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          onClick={() => loadInstallCommand(node)}
                          className="rounded-md border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                          title={t('一行安装命令')}
                        >
                          {t('安装命令')}
                        </button>
                        <button
                          onClick={() => loadDetail(node)}
                          disabled={!online}
                          className="rounded-md border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                          title={t('查看被控节点')}
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </button>
                        <button
                          onClick={() => handleDelete(node)}
                          className="rounded-md border border-gray-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 disabled:opacity-40 dark:border-gray-700 dark:hover:bg-red-950"
                          title={t('删除节点')}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* 节点分组 */}
      <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-4 py-3 dark:border-gray-700">
          <div className="flex items-center gap-2">
            <Boxes className="h-4 w-4 shrink-0 text-gray-400" />
            <div>
              <h2 className="text-sm font-semibold text-black dark:text-white">{t('节点分组')}</h2>
              <p className="text-xs text-gray-400">{t('迁移池 / 策略池：同组节点共享迁移范围与资源策略')}</p>
            </div>
          </div>
          <button
            onClick={openGroupCreate}
            className="flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-brand-700 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
          >
            <Plus className="h-3.5 w-3.5" />
            {t('新建分组')}
          </button>
        </div>
        {nodeGroups.length === 0 ? (
          <p className="px-4 py-10 text-center text-sm text-gray-400">{t('暂无节点分组，点击「新建分组」创建')}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[560px] text-left text-sm">
              <thead>
                <tr className="border-b border-gray-200 text-xs text-gray-500 dark:border-gray-700 dark:text-gray-400">
                  <th className="px-4 py-3 font-medium">{t('名称')}</th>
                  <th className="px-4 py-3 font-medium">{t('说明')}</th>
                  <th className="px-4 py-3 font-medium">{t('成员节点数')}</th>
                  <th className="px-4 py-3 text-right font-medium">{t('操作')}</th>
                </tr>
              </thead>
              <tbody>
                {nodeGroups.map((group) => {
                  const members = groupMembers(group.id)
                  return (
                    <tr key={group.id} className="border-b border-gray-100 last:border-0 dark:border-gray-800">
                      <td className="px-4 py-3 font-medium text-black dark:text-white">{group.name}</td>
                      <td className="px-4 py-3 text-gray-600 dark:text-gray-300">{group.description || '-'}</td>
                      <td className="px-4 py-3 text-gray-600 dark:text-gray-300" title={members.map((n) => n.name).join('、')}>
                        {members.length}
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center justify-end gap-1">
                          <button
                            onClick={() => openGroupEdit(group)}
                            className="inline-flex items-center gap-1 rounded-md border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                          >
                            <Pencil className="h-3 w-3" />
                            {t('编辑')}
                          </button>
                          <button
                            onClick={() => removeNodeGroup(group)}
                            className="inline-flex items-center gap-1 rounded-md border border-gray-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-gray-700 dark:hover:bg-red-950"
                          >
                            <Trash2 className="h-3 w-3" />
                            {t('删除')}
                          </button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* 集群 */}
      <div className="rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-4 py-3 dark:border-gray-700">
          <div className="flex items-center gap-2">
            <Network className="h-4 w-4 shrink-0 text-gray-400" />
            <div>
              <h2 className="text-sm font-semibold text-black dark:text-white">{t('集群')}</h2>
              <p className="text-xs text-gray-400">{t('跨分组的高可用 / 迁移域：容器默认只允许在同集群内迁移')}</p>
            </div>
          </div>
          <button
            onClick={openClusterCreate}
            className="flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-brand-700 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
          >
            <Plus className="h-3.5 w-3.5" />
            {t('新建集群')}
          </button>
        </div>
        {clusters.length === 0 ? (
          <p className="px-4 py-10 text-center text-sm text-gray-400">{t('暂无集群，点击「新建集群」创建')}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[560px] text-left text-sm">
              <thead>
                <tr className="border-b border-gray-200 text-xs text-gray-500 dark:border-gray-700 dark:text-gray-400">
                  <th className="px-4 py-3 font-medium">{t('名称')}</th>
                  <th className="px-4 py-3 font-medium">{t('说明')}</th>
                  <th className="px-4 py-3 font-medium">{t('区域')}</th>
                  <th className="px-4 py-3 font-medium">{t('成员节点')}</th>
                  <th className="px-4 py-3 text-right font-medium">{t('操作')}</th>
                </tr>
              </thead>
              <tbody>
                {clusters.map((cluster) => (
                  <tr key={cluster.id} className="border-b border-gray-100 last:border-0 dark:border-gray-800">
                    <td className="px-4 py-3 font-medium text-black dark:text-white">{cluster.name}</td>
                    <td className="px-4 py-3 text-gray-600 dark:text-gray-300">{cluster.description || '-'}</td>
                    <td className="px-4 py-3 text-gray-600 dark:text-gray-300">
                      {(cluster.region_ids || []).map((id) => regions.find((r) => r.id === id)?.name || id).join('、') || '-'}
                    </td>
                    <td className="px-4 py-3 text-gray-600 dark:text-gray-300" title={clusterMembers(cluster.id).map((n) => n.name).join('、')}>
                      {clusterMembers(cluster.id).length}
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          onClick={() => openClusterEdit(cluster)}
                          className="inline-flex items-center gap-1 rounded-md border border-gray-200 px-2 py-1 text-xs text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                        >
                          <Pencil className="h-3 w-3" />
                          {t('编辑')}
                        </button>
                        <button
                          onClick={() => removeCluster(cluster)}
                          className="inline-flex items-center gap-1 rounded-md border border-gray-200 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-gray-700 dark:hover:bg-red-950"
                        >
                          <Trash2 className="h-3 w-3" />
                          {t('删除')}
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

      {/* 添加节点：一键添加 / 手动添加 */}
      {showCreate && (
        <div className="fixed inset-0 z-[110] flex items-center justify-center bg-black/50 p-4 dark:bg-black/70">
          <div className="w-full max-w-md overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">{t('添加被控节点')}</h3>
              <button onClick={() => setShowCreate(false)} className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>

            {/* 模式切换：下划线 tab */}
            <div className="flex gap-6 border-b border-gray-200 px-5 dark:border-gray-700">
              {([['quick', t('一键添加')], ['manual', t('手动添加')]] as const).map(([mode, label]) => (
                <button
                  key={mode}
                  type="button"
                  onClick={() => setCreateMode(mode)}
                  className={`-mb-px border-b-2 pb-2.5 pt-1 text-sm transition-colors ${
                    createMode === mode
                      ? 'border-brand-600 font-medium text-gray-900 dark:border-brand-400 dark:text-white'
                      : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'
                  }`}
                >
                  {label}
                </button>
              ))}
            </div>

            <div className="space-y-4 px-5 py-4">
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('节点名称')}</label>
                <input
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="node-1"
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
              </div>

              {createMode === 'manual' && (
                <div>
                  <label className="mb-1.5 block text-xs text-gray-500">{t('被控面板地址（可选）')}</label>
                  <input
                    value={newAddress}
                    onChange={(e) => setNewAddress(e.target.value)}
                    placeholder="http://1.2.3.4:8999"
                    className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                  />
                </div>
              )}

              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('绑定被控出口 IP（可选）')}</label>
                <input
                  value={newBindIP}
                  onChange={(e) => setNewBindIP(e.target.value)}
                  placeholder="203.0.113.10"
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
                <p className="mt-1.5 text-xs text-gray-400">
                  {t('填写后安装密钥仅允许该 IP 的机器注册（同 /24 网段亦可）；留空则不限制来源 IP，仅保留一次性与 24 小时时效')}
                </p>
              </div>

              {createMode === 'quick' ? (
                <p className="rounded-md bg-brand-50 px-3 py-2 text-xs text-brand-700 dark:bg-brand-950/50 dark:text-brand-300">
                  {t('创建后将生成一行安装命令，在被控服务器上以 root 执行即可自动安装并接入主控。')}
                </p>
              ) : (
                <p className="text-xs text-gray-400">
                  {t('手动模式适合先录入节点信息再安装 Agent；创建后同样会提供安装命令。')}
                </p>
              )}
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button
                onClick={() => setShowCreate(false)}
                className="rounded-md px-4 py-2 text-sm text-gray-700 hover:bg-gray-200 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {t('取消')}
              </button>
              <button
                onClick={handleCreate}
                disabled={creating}
                className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
              >
                {creating && <Loader2 className="h-4 w-4 animate-spin" />}
                {createMode === 'quick' ? t('生成安装命令') : t('创建')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 一行安装命令（curl | sudo bash）：只展示命令，不展示脚本正文 */}
      {cmdNode && (
        <div className="fixed inset-0 z-[110] flex items-center justify-center bg-black/50 p-4 dark:bg-black/70">
          <div className="flex max-h-[85vh] w-full max-w-2xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">
                {t('一键安装命令')} · {cmdNode.name}
              </h3>
              <button onClick={() => setCmdNode(null)} className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="min-h-0 flex-1 overflow-auto px-5 py-4">
              <p className="mb-3 text-xs text-gray-500 dark:text-gray-400">
                {t('在被控服务器（root 权限）上执行以下命令即可安装并接入主控。密钥经 HTTPS 请求头传输，不会出现在 URL 中。')}
              </p>
              {cmdLoading ? (
                <div className="flex items-center justify-center py-10">
                  <Loader2 className="h-6 w-6 animate-spin text-gray-400" />
                </div>
              ) : cmdInfo ? (
                <>
                  <pre className="overflow-x-auto rounded-md bg-gray-950 p-4 text-xs leading-relaxed text-gray-100">
                    <code className="break-all">{cmdInfo.command}</code>
                  </pre>
                  <div className="mt-3 grid grid-cols-1 gap-2 text-xs sm:grid-cols-3">
                    <div className="rounded-md border border-gray-200 px-3 py-2 dark:border-gray-700">
                      <div className="text-gray-400">{t('密钥时效')}</div>
                      <div className="mt-0.5 font-medium text-black dark:text-white">{t('24 小时 / 一次性')}</div>
                    </div>
                    <div className="rounded-md border border-gray-200 px-3 py-2 dark:border-gray-700">
                      <div className="text-gray-400">{t('绑定 IP')}</div>
                      <div className="mt-0.5 font-medium text-black dark:text-white">{cmdInfo.bound_ip || t('未绑定')}</div>
                    </div>
                    <div className="rounded-md border border-gray-200 px-3 py-2 dark:border-gray-700">
                      <div className="text-gray-400">{t('脚本 SHA256')}</div>
                      <div className="mt-0.5 truncate font-mono text-[11px] text-gray-600 dark:text-gray-300" title={cmdInfo.sha256}>
                        {cmdInfo.sha256.slice(0, 16)}…
                      </div>
                    </div>
                  </div>
                  <p className="mt-3 text-xs text-amber-600 dark:text-amber-400">
                    {t('注意：密钥注册成功后立即失效；节点重装或换机时可重新生成新命令。')}
                  </p>
                </>
              ) : null}
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button
                onClick={() => { void loadInstallCommand(cmdNode) }}
                disabled={cmdLoading}
                className="inline-flex items-center gap-2 rounded-md border border-gray-300 px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                <RefreshCw className="h-4 w-4" />
                {t('重新生成')}
              </button>
              <button
                onClick={copyCommand}
                disabled={!cmdInfo}
                className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
              >
                <Copy className="h-4 w-4" />
                {t('复制命令')}
              </button>
              <button
                onClick={() => setCmdNode(null)}
                className="rounded-md px-4 py-2 text-sm text-gray-700 hover:bg-gray-200 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {t('关闭')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 节点容器详情 */}
      {detailNode && (
        <div className="fixed inset-0 z-[110] flex items-center justify-center bg-black/50 p-4 dark:bg-black/70">
          <div className="flex max-h-[85vh] w-full max-w-3xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">
                {t('被控节点')} · {detailNode.name}
                <span className="ml-2 text-xs font-normal text-gray-400">{detailNode.address}</span>
              </h3>
              <div className="flex items-center gap-1">
                <button
                  onClick={() => loadDetail(detailNode)}
                  className="rounded p-1.5 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white"
                  title={t('刷新')}
                >
                  <RefreshCw className="h-4 w-4" />
                </button>
                <button onClick={() => setDetailNode(null)} className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white">
                  <X className="h-4 w-4" />
                </button>
              </div>
            </div>
            {/* 被控操作：发机 + 镜像同步 */}
            <div className="flex flex-wrap items-center gap-2 border-b border-gray-100 px-5 py-2.5 dark:border-gray-700">
              <button
                onClick={() => openCreateContainer(detailNode)}
                className="inline-flex items-center gap-1.5 rounded-md bg-brand-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-brand-700 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
              >
                <Plus className="h-3.5 w-3.5" />
                {t('开通容器')}
              </button>
              <button
                onClick={() => syncImages(detailNode)}
                disabled={syncingImages}
                className="inline-flex items-center gap-1.5 rounded-md border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
              >
                {syncingImages ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
                {t('同步镜像')}
              </button>
              <button
                onClick={() => coldBackupNode(detailNode)}
                disabled={busyId === 'node-cold-backup'}
                className="inline-flex items-center gap-1.5 rounded-md border border-amber-300 px-3 py-1.5 text-xs font-medium text-amber-700 hover:bg-amber-50 disabled:opacity-40 dark:border-amber-800 dark:text-amber-300 dark:hover:bg-amber-950"
              >
                {busyId === 'node-cold-backup' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <HardDrive className="h-3.5 w-3.5" />}
                {t('冷备份')}
              </button>
            </div>
            <div className="min-h-0 flex-1 overflow-auto px-5 py-4">
              {detailLoading ? (
                <div className="flex items-center justify-center py-10">
                  <Loader2 className="h-6 w-6 animate-spin text-gray-400" />
                </div>
              ) : nodeContainers.length === 0 ? (
                <p className="py-10 text-center text-sm text-gray-400">{t('被控节点暂无容器')}</p>
              ) : (
                <div className="space-y-2">
                  {nodeContainers.map((container) => (
                    <div key={container.id} className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-gray-200 px-3 py-2.5 dark:border-gray-700">
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="truncate text-sm font-medium text-black dark:text-white">{container.name}</span>
                          <span className={`rounded-full px-2 py-0.5 text-xs ${container.status === 'running' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400'}`}>
                            {container.status === 'running' ? t('运行中') : t('已停止')}
                          </span>
                        </div>
                        <div className="mt-0.5 text-xs text-gray-400">
                          {container.template} · {container.vcpu} vCPU · {container.ram_mb}MB · {container.disk_gb}GB
                          {container.ipv6 ? ` · ${container.ipv6}` : ''}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-1">
                        <button
                          onClick={() => runAction(container, 'start')}
                          disabled={container.status === 'running' || busyId === `${container.id}:start`}
                          className="rounded-md border border-gray-200 p-1.5 text-gray-600 hover:bg-emerald-50 hover:text-emerald-700 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300"
                          title={t('开机')}
                        >
                          <Power className="h-3.5 w-3.5" />
                        </button>
                        <button
                          onClick={() => runAction(container, 'stop')}
                          disabled={container.status !== 'running' || busyId === `${container.id}:stop`}
                          className="rounded-md border border-gray-200 p-1.5 text-gray-600 hover:bg-red-50 hover:text-red-700 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300"
                          title={t('关机')}
                        >
                          <Square className="h-3.5 w-3.5" />
                        </button>
                        <button
                          onClick={() => runAction(container, 'restart')}
                          disabled={busyId === `${container.id}:restart`}
                          className="rounded-md border border-gray-200 p-1.5 text-gray-600 hover:bg-amber-50 hover:text-amber-700 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300"
                          title={t('重启')}
                        >
                          <RotateCw className="h-3.5 w-3.5" />
                        </button>
                        <button
                          onClick={() => resetNodePassword(container)}
                          disabled={busyId === `${container.id}:reset-password`}
                          className="rounded-md border border-gray-200 p-1.5 text-gray-600 hover:bg-indigo-50 hover:text-indigo-700 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300"
                          title={t('重置 SSH 密码')}
                        >
                          <KeyRound className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              )}

              {/* 被控镜像清单 */}
              <div className="mt-6 border-t border-gray-100 pt-4 dark:border-gray-700">
                <div className="mb-2 flex items-center justify-between">
                  <h4 className="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                    {t('被控镜像')}
                    <button
                      onClick={() => loadImages(detailNode)}
                      disabled={imagesLoading}
                      className="ml-2 inline-flex items-center gap-1 rounded border border-gray-200 px-1.5 py-0.5 text-[11px] font-normal normal-case text-gray-500 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:hover:bg-gray-800"
                      title={t('刷新镜像清单')}
                    >
                      <RefreshCw className={`h-3 w-3 ${imagesLoading ? 'animate-spin' : ''}`} />
                      {t('刷新')}
                    </button>
                  </h4>
                </div>
                {imagesLoading ? (
                  <div className="flex items-center justify-center py-6">
                    <Loader2 className="h-5 w-5 animate-spin text-gray-400" />
                  </div>
                ) : (nodeImages.lxc || []).length === 0 && (nodeImages.kvm || []).length === 0 ? (
                  <p className="py-4 text-center text-xs text-gray-400">{t('被控节点暂无镜像')}</p>
                ) : (
                  <div className="space-y-1">
                    {[...(nodeImages.lxc || []).map((img) => ({ ...img, kind: 'LXC' })), ...(nodeImages.kvm || []).map((img) => ({ ...img, kind: 'KVM' }))].map((img) => (
                      <div key={`${img.kind}-${img.id}`} className="flex items-center justify-between gap-2 rounded-md border border-gray-200 px-3 py-2 dark:border-gray-700">
                        <div className="min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-semibold text-gray-500 dark:bg-gray-800 dark:text-gray-400">{img.kind}</span>
                            <span className="truncate text-sm text-black dark:text-white">{img.name || img.id}</span>
                          </div>
                          {img.sha256 && <div className="mt-0.5 truncate font-mono text-[10px] text-gray-400" title={img.sha256}>{img.sha256}</div>}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
            <div className="flex justify-end border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button
                onClick={() => setDetailNode(null)}
                className="rounded-md px-4 py-2 text-sm text-gray-700 hover:bg-gray-200 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {t('关闭')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 在被控节点开通（发机）容器 */}
      {createTarget && createForm && (
        <div className="fixed inset-0 z-[110] flex items-center justify-center bg-black/50 p-4 dark:bg-black/70">
          <div className="w-full max-w-md overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">
                {t('开通容器')} · {createTarget.name}
              </h3>
              <button onClick={() => setCreateTarget(null)} className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="space-y-4 px-5 py-4">
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('容器名称')}</label>
                <input
                  value={createForm.name}
                  onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })}
                  placeholder="ct-1"
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('镜像模板')}</label>
                <select
                  value={createForm.template_id}
                  onChange={(e) => setCreateForm({ ...createForm, template_id: e.target.value })}
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                >
                  <option value="">{t('请选择镜像')}</option>
                  {templates.map((tmpl) => (
                    <option key={tmpl.id} value={tmpl.id}>{tmpl.name || tmpl.id}</option>
                  ))}
                </select>
              </div>
              <div className="grid grid-cols-3 gap-3">
                <div>
                  <label className="mb-1.5 block text-xs text-gray-500">vCPU</label>
                  <input
                    type="number"
                    min={1}
                    value={createForm.vcpu}
                    onChange={(e) => setCreateForm({ ...createForm, vcpu: Number(e.target.value) || 0 })}
                    className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                  />
                </div>
                <div>
                  <label className="mb-1.5 block text-xs text-gray-500">{t('内存 (MB)')}</label>
                  <input
                    type="number"
                    min={128}
                    value={createForm.ram_mb}
                    onChange={(e) => setCreateForm({ ...createForm, ram_mb: Number(e.target.value) || 0 })}
                    className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                  />
                </div>
                <div>
                  <label className="mb-1.5 block text-xs text-gray-500">{t('磁盘 (GB)')}</label>
                  <input
                    type="number"
                    min={1}
                    value={createForm.disk_gb}
                    onChange={(e) => setCreateForm({ ...createForm, disk_gb: Number(e.target.value) || 0 })}
                    className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                  />
                </div>
                <div>
                  <label className="mb-1.5 block text-xs text-gray-500">{t('数据盘 (GB)')}</label>
                  <input
                    type="number"
                    min={0}
                    value={createForm.data_disk_gb ?? 0}
                    onChange={(e) => setCreateForm({ ...createForm, data_disk_gb: Number(e.target.value) || 0 })}
                    className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                  />
                </div>
                <div>
                  <label className="mb-1.5 block text-xs text-gray-500">{t('数据盘挂载路径')}</label>
                  <input
                    type="text"
                    placeholder="/data"
                    value={createForm.data_disk_mount_path ?? ''}
                    onChange={(e) => setCreateForm({ ...createForm, data_disk_mount_path: e.target.value })}
                    className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                  />
                </div>
              </div>
              <p className="text-xs text-gray-400">{t('镜像需已通过「同步镜像」下发到该被控节点，否则发机可能失败。')}</p>
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button
                onClick={() => setCreateTarget(null)}
                className="rounded-md px-4 py-2 text-sm text-gray-700 hover:bg-gray-200 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {t('取消')}
              </button>
              <button
                onClick={handleCreateContainer}
                disabled={creatingContainer}
                className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
              >
                {creatingContainer && <Loader2 className="h-4 w-4 animate-spin" />}
                {t('开通')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 节点分组弹窗 */}
      {groupFormOpen && (
        <div className="fixed inset-0 z-[110] flex items-center justify-center bg-black/50 p-4 dark:bg-black/70">
          <div className="w-full max-w-md overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">
                {groupEditTarget ? t('编辑节点分组') : t('新建节点分组')}
              </h3>
              <button onClick={() => setGroupFormOpen(false)} className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="space-y-4 px-5 py-4">
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('名称')}</label>
                <input
                  value={groupName}
                  onChange={(e) => setGroupName(e.target.value)}
                  placeholder="group-1"
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('说明')}</label>
                <input
                  value={groupDesc}
                  onChange={(e) => setGroupDesc(e.target.value)}
                  placeholder={t('可选')}
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('区域（可选）')}</label>
                <select
                  value={groupRegionId}
                  onChange={(e) => setGroupRegionId(e.target.value)}
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                >
                  <option value="">{t('不关联区域')}</option>
                  {regions.map((region) => (
                    <option key={region.id} value={region.id}>
                      {region.name}{region.location ? ` · ${region.location}` : ''}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('成员节点')}</label>
                {nodes.length === 0 ? (
                  <p className="text-xs text-gray-400">{t('暂无节点，可先在上方创建')}</p>
                ) : (
                  <div className="max-h-40 space-y-1.5 overflow-y-auto rounded-md border border-gray-200 p-2.5 dark:border-gray-700">
                    {nodes.map((node) => {
                      const gn = node as GroupedNode
                      const otherGroup = groupEditTarget && gn.node_group_id && gn.node_group_id !== groupEditTarget.id
                      const otherName = otherGroup ? nodeGroups.find((g) => g.id === gn.node_group_id)?.name : ''
                      return (
                        <label key={node.id} className="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
                          <input
                            type="checkbox"
                            checked={groupNodeIds.includes(node.id)}
                            onChange={() => toggleGroupNode(node.id)}
                            className="h-4 w-4"
                          />
                          <span>{node.name}</span>
                          {otherGroup && (
                            <span className="text-[11px] text-amber-600 dark:text-amber-400" title={t('勾选保存后将从原分组移出')}>
                              {t('当前属')} {otherName || gn.node_group_id}
                            </span>
                          )}
                        </label>
                      )
                    })}
                  </div>
                )}
                <p className="mt-1.5 text-[11px] text-gray-400">{t('保存时整体替换成员集合；一个节点只能属于一个分组，勾选已属其他分组的节点会在保存时移出原分组')}</p>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button
                onClick={() => setGroupFormOpen(false)}
                className="rounded-md px-4 py-2 text-sm text-gray-700 hover:bg-gray-200 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {t('取消')}
              </button>
              <button
                onClick={saveNodeGroup}
                disabled={groupSaving}
                className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
              >
                {groupSaving && <Loader2 className="h-4 w-4 animate-spin" />}
                {t('保存')}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 集群弹窗 */}
      {clusterFormOpen && (
        <div className="fixed inset-0 z-[110] flex items-center justify-center bg-black/50 p-4 dark:bg-black/70">
          <div className="w-full max-w-md overflow-hidden rounded-lg border border-gray-200 bg-white shadow-xl dark:border-gray-700 dark:bg-gray-900">
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-gray-700">
              <h3 className="text-sm font-semibold text-black dark:text-white">
                {clusterEditTarget ? t('编辑集群') : t('新建集群')}
              </h3>
              <button onClick={() => setClusterFormOpen(false)} className="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-black dark:hover:bg-gray-800 dark:hover:text-white">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="space-y-4 px-5 py-4">
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('名称')}</label>
                <input
                  value={clusterName}
                  onChange={(e) => setClusterName(e.target.value)}
                  placeholder="cluster-1"
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('说明')}</label>
                <input
                  value={clusterDesc}
                  onChange={(e) => setClusterDesc(e.target.value)}
                  placeholder={t('可选')}
                  className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-black outline-none focus:border-brand-500 dark:border-gray-700 dark:bg-gray-950 dark:text-white"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('覆盖区域（可选）')}</label>
                {regions.length === 0 ? (
                  <p className="text-xs text-gray-400">{t('暂无区域，可先在「区域管理」中创建')}</p>
                ) : (
                  <div className="max-h-40 space-y-1.5 overflow-y-auto rounded-md border border-gray-200 p-2.5 dark:border-gray-700">
                    {regions.map((region) => (
                      <label key={region.id} className="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
                        <input
                          type="checkbox"
                          checked={clusterRegionIds.includes(region.id)}
                          onChange={() => toggleClusterRegion(region.id)}
                          className="h-4 w-4"
                        />
                        <span>{region.name}{region.location ? ` · ${region.location}` : ''}</span>
                      </label>
                    ))}
                  </div>
                )}
                <p className="mt-1.5 text-[11px] text-gray-400">{t('集群内容器默认只允许在本集群范围内迁移')}</p>
              </div>
              <div>
                <label className="mb-1.5 block text-xs text-gray-500">{t('成员节点')}</label>
                {nodes.length === 0 ? (
                  <p className="text-xs text-gray-400">{t('暂无节点，可先在上方创建')}</p>
                ) : (
                  <div className="max-h-40 space-y-1.5 overflow-y-auto rounded-md border border-gray-200 p-2.5 dark:border-gray-700">
                    {nodes.map((node) => {
                      const cn = node as GroupedNode
                      const otherCluster = clusterEditTarget && cn.cluster_id && cn.cluster_id !== clusterEditTarget.id
                      const otherName = otherCluster ? clusters.find((c) => c.id === cn.cluster_id)?.name : ''
                      return (
                        <label key={node.id} className="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
                          <input
                            type="checkbox"
                            checked={clusterNodeIds.includes(node.id)}
                            onChange={() => toggleClusterNode(node.id)}
                            className="h-4 w-4"
                          />
                          <span>{node.name}</span>
                          {otherCluster && (
                            <span className="text-[11px] text-amber-600 dark:text-amber-400" title={t('勾选保存后将从原集群移出')}>
                              {t('当前属')} {otherName || cn.cluster_id}
                            </span>
                          )}
                        </label>
                      )
                    })}
                  </div>
                )}
                <p className="mt-1.5 text-[11px] text-gray-400">{t('保存时整体替换成员集合；一个节点只能属于一个集群，勾选已属其他集群的节点会在保存时移出原集群')}</p>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-gray-100 bg-gray-50 px-5 py-3 dark:border-gray-700 dark:bg-gray-800">
              <button
                onClick={() => setClusterFormOpen(false)}
                className="rounded-md px-4 py-2 text-sm text-gray-700 hover:bg-gray-200 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {t('取消')}
              </button>
              <button
                onClick={saveCluster}
                disabled={clusterSaving}
                className="inline-flex items-center gap-2 rounded-md bg-brand-600 px-4 py-2 text-sm text-white hover:bg-brand-700 disabled:opacity-50 dark:bg-brand-500 dark:text-white dark:hover:bg-brand-400"
              >
                {clusterSaving && <Loader2 className="h-4 w-4 animate-spin" />}
                {t('保存')}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
