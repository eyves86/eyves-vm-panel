# 实例迁移：数据传输实现方案（设计文档）

> 状态：**待实现**。当前线上实现是「配置重建」（`migration_kind: config-only`，`data_migrated: false`），
> 本文给出补齐磁盘数据传输的分阶段方案，供后续实现与验收使用。

## 1. 现状与问题

| 能力 | 现状 |
| --- | --- |
| 迁移语义 | 配置重建：规格/网络参数在目标节点重建，磁盘数据不传输 |
| 共享存储感知 | 已实现（`GET /instances/{id}/migrate` 返回 `storage.source_pool_shared`） |
| 数据传输 | ❌ 未实现 |

集成方最关心的问题是"数据会不会过去"，因此接口已明确回报，但真正的迁移仍缺数据通道。

## 2. 阶段一：共享存储免传输迁移（推荐先做）

**适用前提**：源容器所在存储池 `Shared = true`（NFS / CephFS / RBD / 共享 dir），且目标节点挂载了同一存储。

**核心思路**：数据本来就在双方都可见的挂载点上，迁移 = 在目标节点**接管既有 rootfs**（不复制一个字节）。

### 2.1 需要新增的被控端点

```http
POST /api/agent/containers/adopt
Authorization: Bearer <node-token>
{
  "name": "ct-5",                 // 目标节点上的容器名
  "lxc_name": "ct-5",
  "rootfs_path": "/mnt/ceph/lxc/ct-5/rootfs",
  "template_id": "debian-bookworm",
  "vcpu": 2, "ram_mb": 2048, "disk_gb": 20,
  "network": { "nat": true, "port_count": 2, "lan_*": ... },
  "limits": { "down_mbps": 100, "up_mbps": 100, "io_read_mbps": ... }
}
```

### 2.2 被控侧实现要点（`internal/api/agent_api.go` + `internal/lxc`）

1. **路径白名单校验**：`rootfs_path` 必须位于「已启用且 Shared 的存储池路径」之下（`filepath.Rel` + 前缀校验），并且该路径存在且含 `etc/` 或 `sbin/init`/`bin/sh` —— 防止被控伪造成任意目录接管。
2. **占用检查**：目标节点不得已有容器引用同一 rootfs（比对 `Container.StoragePath`）。
3. **写 LXC 配置**：复用自定义 rootfs 的写法（`internal/lxc/lxc.go` 中 custom rootfs base config）：
   ```
   lxc.include = /usr/share/lxc/config/common.conf
   lxc.arch = linux64
   lxc.rootfs.path = dir:<rootfs_path>
   lxc.uts.name = <lxc_name>
   ```
   之后由既有的网络/限额应用逻辑补齐（`applyLANIPv4Config` / `SetupDefaultPortMappings` / `applyBandwidthLimit` / 防火墙）。
4. **注册容器**：写入本机 `AppConfig.Containers`（含 `StoragePoolID` / `StoragePath` / `RootVolumeID`），状态置 `stopped`。
5. **审计**：记录 adopt 动作（谁在何时接管了哪个 rootfs）。

### 2.3 主控侧改动（`internal/api/apiv2_migrate.go`）

- 前置：源池 `Shared=true` **且** 目标节点配置了相同路径的共享池 → 走 adopt 通道；否则保持现状（config-only）并明确回报。
- 迁移流程：源容器停止 → 校验目标可写共享挂载 → `POST adopt` → 源记录删除（`mode=move`）→ 返回 `data_transferred: true` / `migration_kind: "shared-storage"`。
- 并发保护：迁移期间对该容器加锁（复用 `Locked` 或队列互斥），避免与电源/重装并发。

## 3. 阶段二：本地存储流式迁移

**适用**：源池为本地存储（dir / ZFS / LVM），必须复制数据。

**通道**：新增被控端点，分块 + 断点续传 + 限速：

```http
POST /api/agent/containers/import-rootfs      # 初始化：声明容器名/目标路径/总大小/校验方式
PUT  /api/agent/containers/import-rootfs      # 分块上传：Content-Range: bytes a-b/total
POST /api/agent/containers/import-rootfs/done # 收尾：解包 + 校验 + 应用网络/限额
```

**源端**：`tar --xattrs --acls -czf - -C <parent> rootfs`（流式，不落盘）→ 切块上传；KVM 侧用 `qemu-img convert -O qcow2` 输出到 stdout 流式上传。

**要点**：

- 传输限速（避免打满被控带宽）与超时控制；
- 断点续传（记录已接收偏移，重启后可续）；
- **失败回滚**：目标端删除半成品目录；源容器保持停止、数据完好；
- 完成后走与 `create` 相同的收尾（网络/端口/限额/cloud-init 已处理过的部分跳过）。

## 4. 阶段三：在线迁移（可选，长期）

接入 `internal/livemigrate`（当前是需注入驱动的骨架）：

- **共享存储**：暂停源容器 → 目标机 `virsh start` / `lxc-start` → 切换（秒级切换）；
- **本地存储**：块级复制 + 最终短暂停机切换（qemu `drive-mirror`；LXC 视内核 CRIU 支持度）。

需要注入：`storage.Backend` + libvirt Driver（`livemigrate.Mover` 接口）。

## 5. 验收标准

| 场景 | 验收方式 |
| --- | --- |
| 共享存储 adopt | 迁移后目标容器 `rootfs.path` 与源为同一路径；容器内落盘文件（如 `touch /tmp/marker`）在迁移后可见 |
| 本地流式迁移 | 迁移前后对 `rootfs` 做 `find | sort | xargs sha256sum` 比对一致；中断续传后仍一致 |
| 失败回滚 | 人为中断传输：目标端无半成品容器，源容器与数据完好 |
| 安全 | 被控拒绝白名单外路径、拒绝接管已被引用的 rootfs；审计含操作人与时间 |

## 6. 涉及文件清单

| 层 | 文件 |
| --- | --- |
| 被控端点 | `backend/internal/api/agent_api.go`（新增 adopt / import-rootfs） |
| LXC 接管 | `backend/internal/lxc/lxc.go`（写配置 + 注册容器 + 应用网络/限额的复用） |
| 存储校验 | `backend/internal/config/config.go`（`StoragePoolByID` / `Shared`）、`internal/storage/*` |
| 主控迁移 | `backend/internal/api/apiv2_migrate.go` |
| 存储池管理 | 建议为「共享池需在目标节点配置同路径」补一条一致性检查（命中时为迁移提速的前提） |
