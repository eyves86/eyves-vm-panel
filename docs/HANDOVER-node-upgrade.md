# 交接：被控一键升级（待 shell 恢复后验证）

> 起因：用户要求「主控节点管理界面一键全部升级（被控跟随主控版本）」。
> 状态：**代码已写完，未编译验证**（沙箱 shell 被 `python3` 读 stdin 卡死，需重启 App 恢复）。

## 已完成的代码改动

| # | 文件 | 内容 |
| --- | --- | --- |
| 1 | `backend/internal/cli/cli.go` | 新增 `SelfUpdateToVersion(tag)`：升级到指定版本（空=最新），沿用「就地替换 + detached 重启」策略 + SHA-256 校验 |
| 2 | `backend/internal/cli/cli.go` | 新增 `RunSelfUpdateCommand(args)`：`eyvescloud self-update [--check]` 非交互命令（与自动更新同一路径，便于脚本/手动触发与验证） |
| 3 | `backend/internal/api/agent_api.go` | 新增 `HandleAgentSelfUpdate`：被控端点（agent token 鉴权），body `{target_version, check_only}`，202 后延迟 2 秒执行升级 |
| 4 | `backend/internal/server/server.go` | 注册 `/api/agent/self-update`（AgentTokenMiddleware） |
| 5 | `backend/internal/api/apiv2_nodes.go` | 新增 `v2NodesUpgrade` + 路由 `POST /api/v2/nodes/upgrade`：默认「全部在线节点 → 主控当前版本」，返回 accepted/skipped/failed 明细 |
| 6 | `backend/internal/agent/agent.go` | **修复上轮遗漏**：`StartEmbeddedNodeSide()` 现在会在 `EYVESCLOUD_AUTO_UPDATE` 启用时启动自动更新循环（此前只有心跳） |
| 7 | `install.sh` | `EYVESCLOUD_AUTO_UPDATE=<分钟≥60>` 时写入**面板**单元的 `Environment=`（同机部署下被控职责由面板承担，变量必须落在这里才生效） |
| 8 | `frontend/src/services/apiV2.ts` | 新增 `v2UpgradeNodes()` 与结果类型 |
| 9 | `frontend/src/pages/NodeManagement.tsx` | 工具栏新增「一键升级被控」按钮（二次确认 + 结果明细弹窗 + 刷新），`upgrading` 状态 |

## 恢复后要执行的验证清单

1. `gofmt -w` 改动的 Go 文件；`go build ./...`、`go vet ./...`、`go test ./internal/api ./internal/cli ./internal/config`
2. 前端 `npx tsc --noEmit` + `npm run build`
3. `bash build.sh`（2.2.18，amd64）→ 部署到主控 → 重启
4. **实测**：主控节点管理页点「一键升级被控」→ 被控（131，当前 2.2.5）应在 ~30 秒内升到与主控相同版本；用 `GET /api/v2/nodes` 复查 `version` 字段
5. 顺带用 curl 验证：`POST /api/v2/nodes/upgrade {"check_only":true}` 只回报不升级
6. 提交 + 推送 + 发布 v2.2.18（tarball + SHA256SUMS）
7. 若一切正常，按用户意图在被控的**面板单元**写入 `Environment=EYVESCLOUD_AUTO_UPDATE=1440`（每天自动检查），使其长期跟随主控版本

## 注意事项

- 被控升级会**重启被控面板服务**（节点上的容器不受影响）；升级期间该节点短暂离线，主控会在心跳超时后标记 offline，恢复后自动回到 online。
- 升级走 SHA-256 校验：缺失清单仅告警、不匹配则中止（与面板升级策略一致）。
- 若被控版本**高于**目标版本（例如主控是本地构建 2.2.18 而最新发布是 2.2.17），升级会失败并回报原因——此时应先发布对应版本，或让主控与已发布版本对齐。
