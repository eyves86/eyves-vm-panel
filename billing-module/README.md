# EyvesCloud WHMCS 模块 —— 对接与测试指南

把 CubeCloud（魔方云 ZJMF）的 WHMCS 服务器模块重写为对接 **EyvesCloud**。
LXC 与 KVM 双运行时并存，同一套 UI 靠「能力位」自动适配。

---

## 〇、与原 `billing-module` 的差异（替换前必读）

本模块替换了仓库里原有的 `billing-module/eyvescloud.php`（2459 行单文件 + 7 个 HTML 模板）。
**它不是一个超集**，替换前请知悉各自的能力：

| 能力 | 原模块 | 本模块 |
|---|:---:|:---:|
| **`UsageUpdate`（WHMCS 计量计费同步）** | ✅ | ❌ **缺失** |
| NAT 端口完整管理（增删改查） | ✅ | 基础增删 |
| 防火墙规则管理 | ✅ | 基础 |
| **WebSSH / VNC 反代 handler**（经计费服务器中转，不要求客户浏览器能直连面板） | ✅ | ❌ 直接跳面板控制台地址 |
| `Renew` / `AllowFunction` / 自建 hostid 绑定表 | ✅ | 无（用 WHMCS 自定义字段 `hostid`）|
| 客户区 UI | 7 个 HTML 模板 | **22 个 Smarty 模板 + 设计系统 + 中英双语** |
| 自动化测试 | 无 | **389 项 PHP 断言 + 135 次 UI 审计** |
| API 版本 | v1 | v1（同） |

**替换前请先确认 `UsageUpdate` 对你是必须的** —— 若用 WHMCS 做按量计费，
缺了它等于计量数据不同步，是「收不到钱」级别的问题。
原实现保留在本分支的 git 历史里，可随时取回。

### 第三方素材未随包提交

`templates/assets/flags/`（573 个国旗 SVG，3.3MB）未提交 —— 它只用于区域徽标，
缺失时模板会自动降级（不显示国旗，其余正常）。需要的话从 flag-icons 项目获取放进该目录即可。

---

## 一、包里有什么

```
eyves-whmcs/
├── modules/servers/eyvescloud/       ← 直接丢进 WHMCS 就能用
│   ├── eyvescloud.php                服务器模块（26 个钩子 + 客户区上下文构造）
│   ├── api_client.php                客户区 API 路由（前端唯一入口）
│   ├── lib/EyvesCloud.php            传输层：连接 / 认证 / API / 运行时语义
│   ├── lib/EyvesMapper.php           映射层：能力矩阵 / 字段归一
│   ├── lib/EyvesLang.php             语言加载（英文基底 + 当前语言覆盖）
│   ├── lang/english.php              154 条
│   ├── lang/chinese.php              154 条（与英文键集完全一致）
│   ├── templates/                    22 个模板
│   │   ├── clientarea.tpl            主壳
│   │   ├── header.tpl                实例标识 + 操作按钮
│   │   ├── header-content.tpl        规格网格 + 流量条
│   │   ├── nav.tpl                   页签导航（能力位驱动）
│   │   ├── traffic.tpl / error.tpl / system.tpl / javascript.tpl
│   │   ├── tabs/                     14 个页签
│   │   └── assets/                   样式 + 第三方前端库 + 国旗
│   └── ...
├── tests/                            271 项断言
├── eyvescloud-fix-v2-suspended-bypass.patch   面板漏洞修复补丁
└── README.md
```

### 14 个页签与运行时可用性

| 页签 | LXC | KVM | 说明 |
|---|:---:|:---:|---|
| 概览 / 资源监控 / 网络 | ✅ | ✅ | |
| 端口映射 | ✅ | ✅ | v1 `port-mappings` |
| 快照 / 备份 | ✅ | ✅ | |
| 安全组 | ✅ | ✅ | |
| 数据盘 | ✅ | ✅ | 单块，仅支持扩容 |
| SSH 密钥 | ✅ | ✅ | 面板侧账号级密钥库，只读 |
| 重置密码 | ✅ | ✅ | 挂起态仍可用（管理恢复路径）|
| 定时任务 | ✅ | ✅ | 映射为自动快照计划（只读）|
| 任务记录 / 设置与重装 | ✅ | ✅ | |
| **ISO 与救援** | ❌ | ✅ | `caps.iso_mount` 控制，LXC 不渲染该页签 |

> **运行时差异只体现在 `caps` 能力位上。** 模板与前端一律不判断 runtime 字符串；
> 服务端在 `ClientArea` 里也按能力位做了一次白名单校验，手改 URL 也进不去不该看的页签。

---

## 一之二、API 版本：使用面板的 **v1**

模块走 EyvesCloud 的 **v1 接口**（`/api/...`），不用 v2。选它的理由不是保守，是实测出来的：

| 维度 | v1 | v2 |
|---|---|---|
| 挂起（欠费停机）策略 | **完整**（`isSuspendedBlockedManageAction` 覆盖电源/重装/快照/备份/防火墙/ISO/救援）| 只在 3 处检查，**存在绕过**（已挂起的 KVM 仍能拿 VNC 票据、仍能抹盘重装 —— 已修）|
| VNC 票据的运行时校验 | **服务端校验**（对 LXC 申请直接拒绝）| `console` 的 vnc 分支**无任何校验**（已修）|
| KVM 镜像 | 门控在 `hostKVMAvailable()`（`/dev/kvm` + `virsh`）之后 | 无条件列出，会给不能跑的镜像 |
| 接口面 | 47 个容器动作，含 `vnc-ticket`/`resize`/`traffic`/`rdns`/`vnc-password`/`scheduled-actions` | 较窄 |

### 两版字段差异（迁移时踩过的）

| 语义 | v1 | v2 |
|---|---|---|
| 内存 | `ram_mb` | `memory_mb` |
| 主 IP | `ip` | `primary_ip` |
| 带宽 | `network_down_mbps`（扁平）| `bandwidth.down_mbps`（嵌套）|
| 已用流量 | **字节**（`traffic_used_rx`）| GB（已换算）|
| VNC 端口 | `vnc_port`（扁平）| `kvm.vnc_port`（嵌套）|
| 端口映射 | `port_mappings`（内联）| `nat_ports` |
| 运行时 | **`virtualization`** | `runtime` |
| 镜像运行时 | **`type`** | `runtime` |
| 列表响应 | 多数是**裸数组** | `{items, pagination}` |

> 运行时字段只认一种版本名是最危险的一处：会把所有实例判成 LXC，
> KVM 的 VNC / ISO / 救援入口**整片消失**。代码里 `runtimeOf()` 三个字段名都认，并加了回归测试。

### 请求方法（v1 与直觉不同的一处）

`resource-limit` / `traffic-limit` / `expiry` / `firewall` 都是 **PUT**；
`bandwidth` 是 **GET**（流量明细），**设置带宽走 `resource-limit`**；
救援是 `POST /api/containers/rescue` + `{container_id, enabled, iso_id}`（不是 REST 的 DELETE）。

### 没有通用 PATCH

v1 没有 `PATCH /instances/{id}`，`updateInstance()` 按字段分发到各自端点，
并返回 `{applied, unsupported}` —— **未生效的字段必须报出来，不能静默丢弃**（v1 不支持写 `remark`）。

---

## 二、安装（WHMCS 侧）

### 1. 放文件

```bash
cp -r modules/servers/eyvescloud /path/to/whmcs/modules/servers/
chown -R www-data:www-data /path/to/whmcs/modules/servers/eyvescloud
```

### 2. 添加服务器

WHMCS 后台 → **System Settings → Servers → Add New Server**

| 字段 | 填什么 |
|---|---|
| Hostname | 面板域名或 IP（**不要带 `http://` 和端口**）|
| IP Address | 同上 |
| Port | 面板端口，默认 `8999` |
| Secure (`https`) | 面板开了 SSL 才勾 |
| Username | `admin` |
| Password | 面板管理员密码 |
| **Module** | **EyvesCloud** ← 必须选这个 |

> 用 API Key 就跳过账号密码，把 **Access Hash** 填成 `{"api_key":"你的key"}`。
> 可选：`verify_ssl`(bool)、`timeout`(秒)、`debug`(bool)。
> 面板侧在「设置 → API 密钥」创建，权限至少 `container:*`、`terminal:*`、`image:read`、`snapshot:*`。

点 **Test Connection**，应返回成功 + 面板版本号。

### 3. 添加产品

Module 选 `EyvesCloud`，进 **Module Settings**：

| 配置项 | 说明 |
|---|---|
| **虚拟化类型** | `LXC` 或 `KVM` —— 产品分型总开关 |
| **系统镜像** | 下拉自动按上面选的类型过滤，只列该运行时的镜像 |
| CPU 核数 / 内存 (MB) / 系统盘 (GB) | 支持小数核 |
| 数据盘 (GB) | `0` = 不创建 |
| 月流量 (GB) | `0` = 不限 |
| 下行/上行带宽 (Mbps) | |
| NAT 端口数 / 独立 IPv4 数 / IPv6 数量 | |
| 防火墙 | `on` / `off` |
| 指定节点 | 留空 = 本机主控；`auto` = 调度器自选 |

> **LXC 和 KVM 要建两个产品**（分别选不同的虚拟化类型），因为是两套镜像族。

### 4. 加自定义字段（关键，别漏）

**Configuration → Custom Fields**，字段名精确填 `hostid`，类型 Text Box。

模块用它绑定面板实例。与 EyvesCloud 自带 billing-module 约定一致 —— **老客户迁移不用改数据**。

### 5. 开通测试

后台手动 **Create** 一个服务。成功后：客户区显示实例面板（规格/流量条/14 个页签），
后台服务页显示「面板实例 ID / 运行时 / 状态 / 规格 / 流量 / 到期」，
并有开机、关机、强制关机、重启、强制重启、重装系统、重置密码、重置流量按钮。

---

## 三、测试时重点验这几条

| # | 操作 | 预期 |
|---|---|---|
| 1 | LXC 产品开通 | 控制台按钮是 **「SSH 终端」**；页签**没有**「ISO 与救援」 |
| 2 | KVM 产品开通 | 控制台按钮是 **「VNC 控制台」**；页签**有**「ISO 与救援」 |
| 3 | 把 KVM 产品的镜像硬改成 LXC 镜像 ID | 开通**明确报错**（`RUNTIME_MISMATCH`），不静默装错 |
| 4 | LXC 实例请求 VNC | 前端**自动降到 SSH**，不弹后端 400 |
| 5 | 后台 Suspend 一个 KVM 服务 | 控制台（**VNC 也要试**）**全部打不开** |
| 6 | 挂起状态下重置密码 | **仍然可用**（有意保留的恢复路径）|
| 7 | 重装系统 | 要求**输入实例名**确认，且下拉只列同运行时镜像 |
| 8 | 切换 WHMCS 语言为中文 | 整个实例面板切成中文 |

第 5 条是关键 —— **这正是原面板的漏洞**（原版挂起的 KVM 照样能拿 VNC 票据，还能抹盘重装）。

---

## 四、面板侧补丁（建议一起上）

```bash
cd /path/to/eyvescloud-source
git apply eyvescloud-fix-v2-suspended-bypass.patch
go test ./internal/api/ -count=1     # 应全过
go build -o eyvescloud ./
```

不改面板也能跑模块，但第 5 条在面板侧会被绕过。**建议一起上。**

---

## 五、自测套件

```bash
cd eyves-whmcs
php tests/templates.php                    # 30 项：模板结构 / i18n / 资源（不需面板）
php tests/smoke.php      <面板IP> <端口>   # 33 项：连接 / 认证 / 目录 / 镜像分组
php tests/instances.php  <面板IP> <端口>   # 30 项：实例级 / 控制台分流
php tests/mapper.php     <面板IP> <端口>   # 37 项：能力矩阵 / 归一 / 健壮性
php tests/lifecycle.php  <面板IP> <端口>   # 52 项：26 个 WHMCS 钩子
php tests/clientarea.php <面板IP> <端口>   # 48 项：客户区上下文 / 语言 / 分型
php tests/router.php     <面板IP> <端口>   # 47 项：API 路由（自带 WHMCS 环境）
php tests/retry.php                        # 9 项：瞬时故障重试（自带桩服务器，不需面板）

# 真 Smarty 渲染（唯一能验证「模板真的能编译出页面」的套件）
curl -sL https://codeload.github.com/smarty-php/smarty/tar.gz/refs/tags/v4.5.4 | tar xz
SMARTY_PATH=$PWD/smarty-4.5.4/libs php tests/render.php <面板IP> <端口>   # 60 项
```

共 **359 项断言**。`templates.php` / `retry.php` 不需要面板；`render.php` 需要 Smarty（找不到会跳过）；其余需要面板。

前 5 个套件里账号密码硬编码在文件顶部（`Adapter#2026`），自己用时改一下。
`instances` / `lifecycle` / `clientarea` / `router` / `render` 需要面板里先有
`ct-lxc-01`、`ct-kvm-01` 两个实例（`tests/seed.sh` 是沙箱造数据用的，正式环境不需要）。

> `render.php` 用真 Smarty 把 14 个页签 × 2 种运行时全部编译渲染，并断言
> 语言切换生效、能力位差异正确、恶意 remark 被转义、页面无 PHP 错误泄漏。
> **这一层挡掉了静态校验发现不了的问题** —— 它真的抓到过 OS 图标类名少写、
> 日期未格式化、模板里硬编码中文、以及测试污染面板状态。


---

## 六、界面设计规范

数值**对齐 AWS Cloudscape 设计系统**（AWS 控制台在用的开源规范，cloudscape.design 可查）。
布局参照主流云控制台实例详情页：**身份区 → KPI 指标条 → 页签 → 内容**。

### 从 Cloudscape 抄来的硬数值

| 项 | 规范值 | 本实现 |
|---|---|---|
| 基准网格 | 4px | 4px |
| 容器内水平内边距 | 20px | 20px |
| 卡片内垂直间距 / 页头到内容 | 16px | 16px |
| 网格沟槽 / 表单字段间距 | 20px | 20px |
| 正文 | 14px / 行高 20px | 同 |
| 说明文字 | 12px / 行高 16px | 同 |
| **字号下限** | **不得小于 12px** | 最小 12px（早期有 11px 标签，已修正） |
| 容器标题 | 20px / 24px / Bold | 同 |
| 小节标题 | 16px / 20px / Bold | 同 |
| 等宽字体用途 | 代码、数值、时间、IP/MAC、ID | 同（早期把等宽滥用到了普通字段） |

组件形态同样对齐：

- **状态指示器**（StatusIndicator）= **图标 + 文字**，不是裸色点。
  running→绿勾、stopped→灰方块、creating→旋转箭头、error→红叹号、suspended→黄三角
- **键值对**（KeyValuePairs）= **多列网格，标签在上、值在下**（`columns=3`），
  不是「左标签右值单列」——密度差很多
- **复制**（CopyToClipboard `variant=inline`）= 值旁边一个小按钮，
  而不是把整段文字做成可点

### 其余设计决策

| 决策 | 理由 |
|---|---|
| 状态用**色点**而非仅文字标签 | 颜色是比文字更快的识别通道；色点带柔和光晕，过渡态加脉冲 |
| 指标条用 **flex + 发丝分隔线** | 早期用 grid 会在末尾留一块填不满的灰格子，很难看 |
| 指标格**等宽共享**而非固定宽度 | 固定 basis 会把最后一格挤到下一行单独占位 |
| 单位降一档字号、标签大写小字 | `2048 MB` 里 MB 和数字一样重是错的，数字才是信息 |
| 页签带图标 | 十几个页签纯文字排一列，扫读成本很高 |
| 内容区 `max-width: 1200px` | 宽屏不加约束时键值行横跨 1400px 而值只占左半截 |
| 页签溢出用**两侧渐隐** | 滚动条被隐藏后，没有渐隐用户根本不知道还有更多页签 |
| 复制到剪贴板时**元素自身闪一下** | 只有角落 toast 的话，眼睛还在看被复制的那个值 |

### 无障碍与响应式（实测数据）

在 320 / 375 / 768 / 1440 四个宽度上跑过浏览器体检脚本：

- **文本对比度全部达 WCAG AA**（4.5:1）。早期版本 `VCPU`/`MEMORY` 这类 11px 标签只有 **2.6:1**，全部压深修正
- **触控目标 ≥ 36px**：早期可复制的 IP/实例ID 只有 19px 高，手指点不准
- **无横向溢出**（四档宽度实测 `scrollWidth == innerWidth`）
- **窄屏表格改为堆叠行**：横向滚动会让列名和值脱节；堆叠时用 `data-label` 显示字段名
- 支持 `prefers-reduced-motion`，动效全部可降级

体检脚本随包提供，可直接在客户区页面控制台里跑：

```bash
cat tests/responsive-audit.js   # 复制全部内容，粘贴到浏览器控制台回车
```

它会输出横向溢出元素、对比度不达标的文本、过小的触控目标、页签渐隐是否生效。

### 开发期踩的坑（都已修）

| 坑 | 现象 | 修法 |
|---|---|---|
| `?v=` 写死常量 | 浏览器一直吃缓存，改十几次样式可能都是旧的 | 改为按 CSS 文件 mtime 生成 |
| **设计令牌定义在 `.ev-panel` 上** | **弹层/Toast 挂在 `<body>` 下拿不到变量 → 整块弹层没有背景、按钮没有边框** | 令牌移到 `:root`，另给浮层补基础样式 |
| JS 拼的 DOM 用旧类名 | 重写 CSS 时改了类名，`javascript.tpl` 里动态生成的弹层还是旧名 → 无样式 | 对齐（静态渲染测不出，因为 JS 没执行） |
| `bi-copy` 图标不存在 | 复制按钮是空白方块（第二次踩图标不存在的坑） | 换 `bi-clipboard`，并加「图标必须真实存在」的测试 |
| 状态标签拼字符串 | `'status_' . 'on'` 拼出不存在的键 → 运行中实例显示原始英文串 `running` | 改为显式映射表，并加全状态 × 全语言的回归测试 |

> 三条静态校验已固化进测试：**图标类名必须存在**、**CSS 类名必须存在**、
> **模板不得出现硬编码文案**。这三类 bug 都是「代码看着没错、渲染出来是坏的」。

---

## 七、已知限制

| 限制 | 说明 |
|---|---|
| **实例改名** | 面板 v2 的 `PATCH /instances/{id}` **不支持 `name` 字段**；改名只能走 v1 `POST /containers/{id}/hostname`，语义是**实时修改系统内 hostname**，要求实例运行中，字符集限 RFC1123 `[a-z0-9-]` |
| **多数据盘** | 魔方支持挂 2~4 块独立盘；EyvesCloud 是单块 `data_disk_gb`，当前降级为「容量调整」 |
| **NAT ACL 白名单** | EyvesCloud 无对应物（端口转发已映射）|
| **定时任务** | EyvesCloud 只有 `snapshot_schedule`，映射为只读的快照计划页 |
| **SSH 密钥** | 面板侧为账号级密钥库，客户区只读 |
| **授权** | CubeCloud 是商业模块。本实现是**按接口重新实现**传输层，未复制其代码；对外发布前建议法务确认 |

---

## 七、出问题怎么查

模块返回结构化错误，`code` 字段直接定位：

| code | 含义 |
|---|---|
| `PANEL_UNREACHABLE` | 连不上面板，查地址/端口/防火墙 |
| `UNAUTHENTICATED` | 账号密码或 API Key 不对 |
| `NOT_PROVISIONED` | 服务还没绑实例（`hostid` 字段空）|
| `INSTANCE_NOT_FOUND` | `hostid` 指向的实例在面板里没了 |
| `RUNTIME_MISMATCH` | 镜像和运行时对不上（防呆触发，说明产品配错了）|
| `CAPABILITY_UNSUPPORTED` | 该运行时没这个能力（如 LXC 访问 ISO 页签）|
| `SUSPENDED` / `LOCKED` | 实例挂起或锁定，操作被策略拒绝 |
| `PRECONDITION_FAILED` | 状态不满足（如未运行却要开控制台 / 改名）|
| `SHRINK_NOT_ALLOWED` | 数据盘缩容（面板不支持）|
| `BAD_PARAM` | 参数非法（主机名格式、备注过长等）|

需要详细日志：服务器 Access Hash 里加 `"debug":true`，请求会打到 PHP error log。
