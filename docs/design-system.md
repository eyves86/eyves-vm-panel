# 设计系统（Design System）

> 来源：项目根目录 [`DESIGN.md`](../DESIGN.md)，取自 [VoltAgent/awesome-design-md](https://github.com/VoltAgent/awesome-design-md)
> 中 **linear.app** 的设计分析（74 套可选，见 `/var/minis/shared/awesome-design-md/design-md/`）。
> 选择理由：本产品既有主色为紫色系、管理面板以数据密度与技术感为主，Linear 的
> 「近黑画布 + 单一紫色强调 + 发丝描边 + 紧凑排版」与之最契合。

## 1. 怎么用（给实现者）

1. 新增页面 / 组件时**先读 `DESIGN.md`**（含 colors / typography / layout / elevation /
   shapes / components / do's and don'ts / responsive / iteration guide 九节）。
2. 颜色优先使用 Tailwind 语义令牌（见下），不要直接写十六进制。
3. 保留既有交互（品牌按钮 `bg-brand-600`、暗色紫调面板）——本设计系统以**新增与迭代**
   方式应用，不强制整站换肤；如确需整站切换，单独提 PR 并附前后截图。

## 2. 令牌映射（Linear → 本项目 Tailwind）

| Linear 令牌 | 值 | 本项目（Tailwind） | 用途 |
| --- | --- | --- | --- |
| `canvas` | `#010102` | `bg-canvas` | 最深底色（营销页/登录页背景） |
| `surface-1..4` | `#0f1011` … `#191a1b` | `bg-surface-1..4` | 面板/卡片层级（越上层越亮） |
| `hairline` | `#23252a` | `border-hairline` | 发丝描边（卡片/分割线） |
| `hairline-strong` | `#34343a` | `border-hairline-strong` | 强调描边（输入框聚焦前/选中态） |
| `ink` | `#f7f8f8` | `text-ink` | 主文字（暗色背景上） |
| `ink-muted` / `ink-subtle` / `ink-tertiary` | `#d0d6e0` / `#8a8f98` / `#62666d` | `text-ink-muted` 等 | 次级/辅助/弱化文字 |
| `primary` | `#5e6ad2` | `bg-accent` / `text-accent` | Linear 强调色（新增 UI 可选） |
| `primary-hover` | `#828fff` | `bg-accent-hover` | 强调色悬停 |
| 既有品牌紫 | `#7044ed` | `bg-brand-600`（不变） | 现有按钮/主操作（保持连续性） |

字体：显示字体使用系统无衬线栈（`font-sans`，Inter 优先，中文回退 PingFang/雅黑）；
字重 500–600、负字距仅用于大标题（与 DESIGN.md 的 display 规则一致）。

## 3. 必须遵守的守则（摘自 DESIGN.md 的 Do's and Don'ts）

- **强调色只用于**：品牌标记、聚焦环、少量主 CTA —— 不做装饰性用色。
- **层级靠背景层次与发丝描边**，不靠重阴影；卡片在暗色下是「炭灰面板 + 1px 描边」。
- **排版紧凑、技术感**：表头/标签用 `text-xs` + `text-ink-subtle`；数值右对齐、等宽数字。
- **间距节奏统一**：区块 24px（`space-y-6`）、卡片内 16–20px、控件高 32–40px。
- **状态色语义化**：成功 `#27a644`、危险用现有 red-500/600 系；不要新增第三种红。
- 响应式：≥1024px 三栏密度、768–1024 两栏、<768 单栏并收起次要列（与现有页面一致）。

## 4. 落地清单（后续 UI 迭代按此执行）

- [x] `DESIGN.md` 落项目根目录；令牌进入 `frontend/tailwind.config.js`
- [x] 设计守则并入本文档（作为代码评审依据）
- [ ] 新增页面（后续批次）直接使用语义令牌
- [ ] 存量页面按「不破坏现有观感」原则**渐进**替换硬编码色值
- [ ] 若整站切换为 Linear 暗色（中性近黑 + 薰衣草强调），单独 PR + 前后截图对比
