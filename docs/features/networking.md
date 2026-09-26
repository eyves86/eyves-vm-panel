# 网络与路由

EyvesCloud 提供 NAT4 端口映射、随机可用端口、公网 IPv4 分配、IPv6 状态检查和 IPv6 分配能力。创建容器时可以只分配 NAT、只分配公网 IPv4、只分配 IPv6，或按需混合使用。

## NAT4

NAT4 用于把宿主机端口转发到容器内部端口。典型用途：

- 转发 SSH。
- 暴露 Web 服务。
- 给子用户分配固定外部端口。

端口映射包含：

| 字段 | 说明 |
| --- | --- |
| 名称 | 用于识别用途，例如 `ssh`、`web`。 |
| 协议 | `tcp` 或 `udp`。 |
| 外部端口 | 宿主机对外监听端口。 |
| 内部端口 | 容器内部服务端口。 |

## IPv6

IPv6 分配要求宿主机本身拥有可路由 IPv6 地址段，并且系统路由、邻居发现或代理策略配置正确。

```http
GET /api/v1/ipv6/status
POST /api/v1/containers/{id}/ipv6
```

如果宿主机没有公网 IPv6 或上游没有正确路由，面板中分配出的地址也无法从公网访问。

## 公网 IPv4

公网 IPv4 分配会从主机检测到的可用公网 IPv4 中选择地址，或使用 API 指定的 `public_ipv4s`。创建容器时可使用：

| 字段 | 说明 |
| --- | --- |
| `assign_nat` | 是否启用 NAT 端口映射。 |
| `assign_ipv4` | 是否分配公网 IPv4。 |
| `ipv4_count` | 自动分配公网 IPv4 数量。 |
| `public_ipv4s` | 指定公网 IPv4 地址列表。 |
| `assign_ipv6` | 是否分配 IPv6。 |
| `ipv6_count` | 自动分配 IPv6 数量。 |
| `ipv6_addresses` | 指定 IPv6 地址列表。 |

公网地址池相关接口：

```http
GET /api/v1/routing
PUT /api/v1/routing
POST /api/v1/routing/ipv4-scan
```

## 路由状态

```http
GET /api/v1/routing
```

该接口用于查看 NAT、IPv6、端口容量等运行时状态。

## 区域（地域化分组）

区域用于把节点、存储与容器按地域逻辑分组管理，便于多机房/多区域场景下的规划。节点可通过 `region_id` 归属到某个区域。

```http
GET    /api/regions
POST   /api/regions
DELETE /api/regions/{id}
```

创建区域请求体：

| 字段 | 说明 |
| --- | --- |
| `name` | 区域名称（必填） |
| `location` | 展示用地域/机房信息（可选） |

区域对象包含 `id`、`name`、`location`、`created_at`。以上接口仅管理员可调。

## 弹性 IP 组与故障切换

弹性 IP 组把一组公网 IP 分为「生效」（`enable`）与「备用」（`standby`）。当需要把备用 IP 提升为生效时，触发手动故障切换，面板会把切换应用到引用了被降级 IP 的容器。

```http
GET     /api/ip-groups
POST    /api/ip-groups
PUT     /api/ip-groups/{id}
DELETE  /api/ip-groups/{id}
POST    /api/ip-groups/{id}/failover
```

创建/更新请求体：

| 字段 | 说明 |
| --- | --- |
| `name` | 组名称（必填） |
| `enable` | 当前生效的公网 IP 列表 |
| `standby` | 备用的公网 IP 列表 |

`POST /api/ip-groups/{id}/failover` 携带 `{"ip": "<备用IP>"}`，将指定备用 IP 提升为生效，原生效的第一个 IP 自动降级为备用。IP 组对象包含 `id`、`name`、`enable`、`standby`、`fault_open`（当前故障保持的 IP）、`created_at`。以上接口仅管理员可调。
