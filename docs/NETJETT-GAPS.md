# 与 NetJett（魔方系）的能力差距与路线图

> 更新：2026-09-30（第二批收口完成）· 基线：NetJett API 文档 613 接口/65 模块 vs EyvesCloud v2.2.33
> 原则：财务/商城不做（外部 WHMCS 承接）；LXC/KVM 定位不适用的不追；其余按生产价值排序补齐。

## 已收口（2026-09-30，v2.2.33 · 两批合计）

| 能力 | NetJett 对应 | EyvesCloud 实现 |
|---|---|---|
| 欠费挂起 suspend/unsuspend | `clouds/:id/suspend` / `unsuspend` | v1 + v2 power action + UI 详情页按钮 |
| 回收站（误删恢复） | `recycle_bin` | v2 DELETE=软删除、`/recycle-bin`、`restore`、`purge`、保留期自动清理（`EYVESCLOUD_RECYCLE_DAYS`，默认 7 天）、UI 回收站视图 |
| 批量改密/备注/到期 | `clouds/password` / `PUT clouds` | v2 batch `reset-password`/`remark`/`expiry`（按动作粒度 scope，逐实例结果） |
| 实例 CSV 导出 | `clouds/export_csv` | `GET /api/v2/instances/export.csv` |
| 弹性 IP 独立绑定 | `elastic_ip attach/detach` | `POST /api/v2/ip-pools/attach` / `detach`（地址池资源级 attach/detach） |

| 能力 | 说明 | 工作量 |
|---|---|---|
| 实例模板（从实例做镜像） | NetJett `clouds/:id/templates`：LXC 打包 rootfs 为自定义镜像、KVM 快照 qcow2 为模板。EyvesCloud 已有 custom_lxc_images/custom_kvm_images 存储，补"从实例导入"链路 | 中（数据面，需实机验证） |
| 流量包（加油包） | 计费联动：实例月流量超限后可用流量包抵扣。需与 WHMCS 模块联动出配置项 | 中 |
| IP 池分组 | NetJett `ip_groups`：按分组划分地址段，配合开通页筛选 | 小 |

## 远期路线（P3，需求驱动）

| 能力 | 说明 | 不做/缓做的理由 |
|---|---|---|
| 负载均衡（28 接口） | 独立 LB 资源 + 监听器/转发规则 | 需要专用数据面（haproxy/lvs 管理通道）；目标客户现阶段用外部 LB 更稳 |
| GPU/硬件直通/vGPU（16 接口） | PCI 直通、显卡计数、vGPU 模型 | 仅 KVM 且依赖具体硬件；等有 GPU 节点再做 |
| VPC/私有网络 | 多实例二层互联 | LXC LAN 模式已覆盖同主机组网；跨节点 overlay（vxlan）需要 agent 侧大改 |
| 智能带宽/智能CPU规则、告警中心 | 监控联动限速 | 现有 监控+Webhook 可拼装；等真实告警需求反馈 |
| 疏散/掉线迁移 | 节点故障自动迁移实例 | 依赖块级复制/共享存储（design-migration-data-transfer.md 前置） |
| 数据库类/ADSL/NAT网关 | 云数据库、拨号云 | 不属于 LXC/KVM 面板定位 |

## 设计取舍备忘

- **回收站默认开**：删除进回收站（数据面不动），WHMCS Terminate 走 `?purge=true` 真释放——计费语义（取消=释放）与人工误删保护并存。
- **批量改密**：省略密码=逐实例随机生成并**仅此一次**返回；与 WHMCS/自动化的对接需保存响应。
- **弹性 IP**：池地址 attach/detach 是配置层绑定；网络面生效依赖实例所在节点的公网路由配置（与创建时分配 IPv4 的既有语义一致）。

## 本轮附带修复的预存严重 bug

**全局列表切片共享**：`lxcManager.ListContainers()` 直接返回 `config.AppConfig.Containers`（共享底层数组），而各列表端点的过滤（tag/status/runtime 等）普遍用 `filtered := containers[:0]` 原地复用数组——**任何带过滤的实例列表请求都会原地覆写内存态全局配置**，并在下一次落库时持久化损坏（回收站视图过滤实测触发：被回收实例的记录被后续元素覆写消失）。修复：`listByRuntime()` 出口统一防御性拷贝；`agent_api.go` 同口径。此 bug 影响所有 v2.2.x 历史版本，建议尽快随 v2.2.33 部署。
