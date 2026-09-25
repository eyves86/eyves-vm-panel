---
layout: home

hero:
  name: EyvesCloud
  text: 企业级多节点虚拟化云管理平台
  tagline: 以统一控制平面管理跨节点 LXC / KVM 工作负载 —— 调度引擎、多租户隔离、计量对接与安全审计，开箱即用。
  actions:
    - theme: brand
      text: 立即开始
      link: /guide/installation
    - theme: alt
      text: 查看 API
      link: /features/api
    - theme: alt
      text: 产品能力
      link: /features/containers

features:
  - title: 统一控制平面
    details: 单一主控纳管无限被控节点，Web 控制台 / CLI / 版本化 REST API 三端一致，跨节点操作无需逐台登录。
  - title: 智能调度引擎
    details: 过滤（在线/容量/存储后端/维护模式）→ 评分（RAM + Disk + 租户分散度）→ 决策留痕，全程可诊断。
  - title: 企业级多租户
    details: 子用户 operator / viewer 角色按容器授权、租户配额、独立面板入口、令牌版本控制与会话治理。
  - title: 计量与财务就绪
    details: 全量用量导出 API（资源配置 + 实时用量 + 到期/流量），配合幂等开通机制，无缝对接计费系统。
  - title: 安全纵深
    details: conntrack 威胁检测、JWT Issuer/Audience 绑定、API Key scope 细粒度授权、全量操作与登录审计。
  - title: 平滑运维
    details: 节点维护模式（drain/evacuate）、主动探活告警、面板内自升级（可选仓库与目标版本）、快照与到期自动回收。
---
