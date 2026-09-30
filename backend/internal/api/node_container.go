package api

// node_container.go —— 节点容器的统一辅助（代理调用 ID 映射 / 记录清理）。
//
// 背景（生产实测修复，v2.2.36）：
//   - 节点容器的主控侧 ID（c.ID）现在全局唯一；代理调用 /api/agent/containers/...
//     必须使用节点本地 ID（c.NodeLocalID）。
//   - 代理删除（destroy/purge）成功后，主控侧记录应立即清除
//     （此前残留：列表一直显示、心跳 orphan 标记又因 node_id 未持久化而失配）。

import "eyvescloud/internal/config"

// nodeLocalID 返回实例在所属被控节点上的本地 ID（代理调用路径用）。
// 兼容旧数据：NodeLocalID 未记录时回退 c.ID（历史版本二者相同）。
func nodeLocalID(c *config.Container) int {
	if c == nil {
		return 0
	}
	if c.NodeLocalID > 0 {
		return c.NodeLocalID
	}
	return c.ID
}

// removeNodeContainerRecord 从主控配置删除节点容器记录（按 UUID）。
// 在"被控已确认销毁"后调用；幂等（不存在时无副作用）。
func removeNodeContainerRecord(uuid string) {
	if uuid == "" {
		return
	}
	_ = config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := cfg.Containers[:0]
		for i := range cfg.Containers {
			if cfg.Containers[i].UUID == uuid {
				continue
			}
			out = append(out, cfg.Containers[i])
		}
		cfg.Containers = out
	})
}
