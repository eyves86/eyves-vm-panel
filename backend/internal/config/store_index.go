package config

import "sync"

// ---- P2 接入路径的 O(1) 行查找 ----
//
// 背景：心跳是最高频的读取+写入路径。此前它每请求要做两次 O(节点数) 线性查找
// （校验 token 的 FindNode、改状态时在写锁内再扫一遍 cfg.Nodes），容器同步更是
// O(全部容器) 两次（见 syncAgentContainersUnlocked）。30k 节点 / 30w 容器、
// 3000 次心跳/秒时，这些扫描是纯粹的 CPU 墙，与「一次心跳改了多少行」无关。
//
// 这里维护三张「下标缓存」：节点 ID、容器 UUID、容器 ID 到 cfg 切片下标的映射。
//
// 为什么不做显式失效：cfg.Nodes / cfg.Containers 的结构性改动点多而分散（配置包、
// api 各文件、lxc 包都直接 append/过滤），漏掉一处就会静默给出错误下标。改为
// **自校验 + 自愈**：
//   - 命中时校验下标处的行主键是否一致，不一致即视为失效；
//   - 失效（指向别处，说明发生过删除/重排）⇒ 整表重建（O(n)，每次结构性改动后至多一次）；
//   - 缺失（新追加的行）⇒ 线性找一次并只补这一个条目，不重建。
//
// 因此未命中的查询永远只是「慢」，不会「错」；结构性改动后最多付一次 O(n)。
//
// 锁：调用方必须持有 AppConfigMu（读锁即可），用于读取 AppConfig 切片本身；
// 三张表的读写另由 rowIdxMu 串行化——重建会写 map，而读侧可能只持 AppConfigMu
// 的读锁，两者并发即为 map 竞态。锁序固定 AppConfigMu → rowIdxMu。

var (
	rowIdxMu sync.Mutex

	nodeIdxByID   = map[string]int{}
	contIdxByUUID = map[string]int{}
	contIdxByID   = map[int]int{}
)

// FindNodeIndexUnlocked 返回节点在 AppConfig.Nodes 中的下标。
// 调用方必须持有 AppConfigMu（读锁或写锁）。
func FindNodeIndexUnlocked(id string) (int, bool) {
	if AppConfig == nil || id == "" {
		return -1, false
	}
	nodes := AppConfig.Nodes
	rowIdxMu.Lock()
	defer rowIdxMu.Unlock()
	if i, ok := nodeIdxByID[id]; ok {
		if i < len(nodes) && nodes[i].ID == id {
			return i, true
		}
		rebuildNodeIndexLocked(nodes)
		if i, ok := nodeIdxByID[id]; ok && i < len(nodes) && nodes[i].ID == id {
			return i, true
		}
		return -1, false
	}
	for i := range nodes {
		if nodes[i].ID == id {
			nodeIdxByID[id] = i
			return i, true
		}
	}
	return -1, false
}

func rebuildNodeIndexLocked(nodes []Node) {
	next := make(map[string]int, len(nodes))
	for i := range nodes {
		next[nodes[i].ID] = i
	}
	nodeIdxByID = next
}

// FindContainerIndexByUUIDUnlocked 返回 UUID 对应的容器下标。
// 调用方必须持有 AppConfigMu。
func FindContainerIndexByUUIDUnlocked(uuid string) (int, bool) {
	if AppConfig == nil || uuid == "" {
		return -1, false
	}
	conts := AppConfig.Containers
	rowIdxMu.Lock()
	defer rowIdxMu.Unlock()
	if i, ok := contIdxByUUID[uuid]; ok {
		if i < len(conts) && conts[i].UUID == uuid {
			return i, true
		}
		rebuildContainerIndexLocked(conts)
		if i, ok := contIdxByUUID[uuid]; ok && i < len(conts) && conts[i].UUID == uuid {
			return i, true
		}
		return -1, false
	}
	for i := range conts {
		if conts[i].UUID == uuid {
			contIdxByUUID[uuid] = i
			return i, true
		}
	}
	return -1, false
}

// findContainerIndexByIDUnlocked 返回主控侧容器 ID 对应的容器下标。
// 调用方必须持有 AppConfigMu。
func findContainerIndexByIDUnlocked(id int) (int, bool) {
	if AppConfig == nil {
		return -1, false
	}
	conts := AppConfig.Containers
	rowIdxMu.Lock()
	defer rowIdxMu.Unlock()
	if i, ok := contIdxByID[id]; ok {
		if i < len(conts) && conts[i].ID == id {
			return i, true
		}
		rebuildContainerIndexLocked(conts)
		if i, ok := contIdxByID[id]; ok && i < len(conts) && conts[i].ID == id {
			return i, true
		}
		return -1, false
	}
	for i := range conts {
		if conts[i].ID == id {
			contIdxByID[id] = i
			return i, true
		}
	}
	return -1, false
}

func rebuildContainerIndexLocked(conts []Container) {
	byUUID := make(map[string]int, len(conts))
	byID := make(map[int]int, len(conts))
	for i := range conts {
		if conts[i].UUID != "" {
			byUUID[conts[i].UUID] = i
		}
		byID[conts[i].ID] = i
	}
	contIdxByUUID = byUUID
	contIdxByID = byID
}

// resetRowIndexes 清空下标缓存。切换配置快照（重载/测试 teardown）后调用：
// 旧下标对新切片没有意义，留着只会让第一次查询多付一次重建。
func resetRowIndexes() {
	rowIdxMu.Lock()
	defer rowIdxMu.Unlock()
	nodeIdxByID = map[string]int{}
	contIdxByUUID = map[string]int{}
	contIdxByID = map[int]int{}
}

// AppendContainerUnlocked 把一个新容器追加到 AppConfig.Containers 并登记其下标。
// 调用方必须持有 AppConfigMu 写锁。
//
// 用法要求：它替代裸 append。裸 append 会让新容器在缓存里缺席，之后的按 UUID 查找
// 要退化成一次 O(全部容器) 的线性扫描——心跳首次接入一台有几十个容器的节点时，
// 这个代价会乘以容器数。
func AppendContainerUnlocked(c Container) {
	AppConfig.Containers = append(AppConfig.Containers, c)
	i := len(AppConfig.Containers) - 1
	rowIdxMu.Lock()
	defer rowIdxMu.Unlock()
	if c.UUID != "" {
		contIdxByUUID[c.UUID] = i
	}
	contIdxByID[c.ID] = i
}
