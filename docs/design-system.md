# 设计系统（Design System）

> 来源：项目根 [`DESIGN.md`](../DESIGN.md)，取自 [VoltAgent/awesome-design-md](https://github.com/VoltAgent/awesome-design-md)
> 中 **cal** 的设计分析（`design-md/cal/DESIGN.md`，https://getdesign.md/cal/design-md ）。
> 全量 74 套备选位于 `/var/minis/shared/awesome-design-md/design-md/`，如需切换仅需替换 `DESIGN.md`
> 与下表的令牌映射。

## 1. 风格定位（Cal.com）

- **白画布 + 黑色主按钮**：主操作一律黑底白字（`#111111` / hover `#242424`），不用彩色 CTA
- **中性灰层级**：`canvas #ffffff` → `surface-soft #f8f9fa` → `surface-card #f5f5f5` → `surface-strong #e5e7eb`
- **发丝描边优先**：卡片/表格用 `hairline #e5e7eb` 分隔，不用重阴影
- **蓝色仅作强调**：`#3b82f6` 用于聚焦环、链接、暗色模式主按钮；不铺面积
- **软圆角 + 宽松留白**：卡片 ~12px 圆角，区块间距 24px
- **字体**：Inter（body/title），标题 600 字重 + 轻微负字距（Cal Sans 用系统栈替代）

## 2. 令牌映射（Cal → 本项目）

| Cal 令牌 | 值 | 本项目 | 用途 |
| --- | --- | --- | --- |
| `primary` / `primary-active` | `#111111` / `#242424` | `bg-brand-600` / `bg-brand-700` | 主按钮（浅色） |
| `brand-accent` | `#3b82f6` | `bg-brand-500`（暗色主按钮）/ `cal-accent` | 强调、聚焦环 |
| `ink` / `body` / `muted` | `#111111` / `#374151` / `#6b7280` | `text-ink` / `text-body` / `text-muted` | 文字层级 |
| `canvas` / `surface-soft` / `surface-card` / `surface-strong` | `#ffffff` / `#f8f9fa` / `#f5f5f5` / `#e5e7eb` | `bg-canvas` 等 | 背景层级 |
| `hairline` / `hairline-soft` | `#e5e7eb` / `#f3f4f6` | `border-hairline` | 描边/分隔 |
| `surface-dark` / `surface-dark-elevated` | `#101010` / `#1a1a1a` | 暗色 `--surface` / `--soft` | 暗色背景 |
| `on-dark` / `on-dark-soft` | `#ffffff` / `#a1a1aa` | 暗色文字 | 暗色文字层级 |
| `success` / `warning` / `error` | `#10b981` / `#f59e0b` / `#ef4444` | `cal-success` 等 | 状态色 |

暗色模式遵循 Cal 的中性近黑（`#101010` 画布、`#1a1a1a` 卡片、`#262626` 描边），主按钮用 Cal 蓝。

## 3. 必须遵守的守则（摘自 DESIGN.md）

- 主 CTA 用黑色（暗色下用 Cal 蓝），**不要新增彩色按钮**
- 卡片 = 白底 + 1px 发丝描边 + 极轻阴影；禁止彩色荧光阴影
- 层级靠底色深浅与描边，不靠投影大小
- 数字/指标右对齐，等宽字体；表格行高紧凑（32–40px）
- 间距节奏：区块 24px（`space-y-6`）、卡片内 16–20px
- 徽章用语义色（绿=在线/成功、红=离线/错误、灰=中性标签）
- 响应式：≥1024 三栏密度、768–1024 两栏、<768 单栏

## 4. 已落地的改动（v2.2.26）

- `DESIGN.md` 换为 Cal 版本；`frontend/tailwind.config.js` 令牌换为 Cal 语义集合
- `index.css`：全局变量（accent/text/muted/surface/line）、主按钮（去荧光、黑色 + 克制阴影）、
  次级按钮 hover 描边、输入框聚焦环（Cal 蓝）、暗色覆盖层（紫调 → 中性近黑）
- 效果：主按钮黑底白字、页面底 `#f8f9fa`、卡片白底发丝描边、暗色中性化

## 5. 后续按此执行的清单

- [x] DESIGN.md / Tailwind 令牌 / 全局样式对齐 Cal
- [ ] 存量页面把硬编码色值逐步替换为语义令牌（不破坏观感）
- [ ] 卡片圆角 8px → 12px 的渐进替换（Cal ~12px）
- [ ] 新增页面直接使用 `ink/body/muted/hairline/surface-*` 令牌
