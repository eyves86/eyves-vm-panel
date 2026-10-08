package config

// cells_test.go —— P3 分片路由（node_id → cell_id）的契约守卫。
//
// 路由必须是**全函数**：任何节点（乃至任何容器）都恰属一个 cell。这不是洁癖——
// P3-b 按 cell 分桶落库、P3-c 按 cell 分库，都建立在「归属是确定且唯一的」之上；
// 一旦出现"无归属"的中间态，那些行就会被漏写或漏路由。

import "testing"

// TestNodeCellIDAffectsFingerprint 是 CellID 的「漏写」守卫：指纹是「这一行要不要写」
// 的唯一依据（diffNodes 取 fingerprint(Node)）。若 CellID 进不了编码计划，节点换 cell
// 后就不会被落库、重启即丢。Node **不在** TestFingerprintCoversEveryField 的类型清单里
// （那份清单只覆盖容器/子用户/密钥/快照/任务），所以这里单独钉一下。
func TestNodeCellIDAffectsFingerprint(t *testing.T) {
	base := Node{ID: "n-1", Name: "a", Status: "online"}
	moved := base
	moved.CellID = "cell-1"

	if fingerprint(moved) == fingerprint(base) {
		t.Fatal("CellID 变化未改变节点指纹：换 cell 不会被落库（重启丢失）")
	}
	back := moved
	back.CellID = ""
	if fingerprint(back) != fingerprint(base) {
		t.Fatal("CellID 复原后指纹未还原：每次保存都会误判为「变了」")
	}
}

func TestCellRouting(t *testing.T) {
	withSecGroupPersistTest(t)

	AppConfigMu.Lock()
	AppConfig.Cells = []Cell{{ID: "cell-1", Name: "shard-a"}, {ID: "cell-2", Name: "shard-b"}}
	AppConfig.Nodes = []Node{
		{ID: "n-1", CellID: "cell-1"},    // 显式归属
		{ID: "n-2", CellID: ""},          // 未归属
		{ID: "n-3", CellID: "cell-gone"}, // 归属的 cell 已被删除
	}
	AppConfigMu.Unlock()

	for _, tc := range []struct{ name, nodeID, want string }{
		{"显式归属", "n-1", "cell-1"},
		{"节点不存在回落默认", "n-ghost", DefaultCellID},
		{"未归属回落默认", "n-2", DefaultCellID},
		{"归属已失效回落默认", "n-3", DefaultCellID},
		{"本机容器（无所属节点）回落默认", "", DefaultCellID},
	} {
		if got := CellForNodeID(tc.nodeID); got != tc.want {
			t.Errorf("%s：CellForNodeID(%q) = %q, want %q", tc.name, tc.nodeID, got, tc.want)
		}
	}

	// 容器经其所属节点归属 cell。
	if got := CellForContainer(Container{NodeID: "n-1"}); got != "cell-1" {
		t.Errorf("远程容器应随所属节点：got %q, want cell-1", got)
	}
	if got := CellForContainer(Container{NodeID: ""}); got != DefaultCellID {
		t.Errorf("本机容器应归默认 cell：got %q", got)
	}
	if got := CellForContainer(Container{NodeID: "n-3"}); got != DefaultCellID {
		t.Errorf("节点归属失效时容器应回落默认 cell：got %q", got)
	}

	// 完备性：每个节点解析出的 cell 都必须存在（默认 cell 恒存在，无需在目录里）。
	AppConfigMu.RLock()
	known := map[string]bool{DefaultCellID: true}
	for _, c := range AppConfig.Cells {
		known[c.ID] = true
	}
	for _, n := range AppConfig.Nodes {
		if got := CellForNodeLocked(n); !known[got] {
			t.Errorf("节点 %s 解析出未知 cell %q —— 路由必须落到一个已知分片", n.ID, got)
		}
	}
	AppConfigMu.RUnlock()

	if _, ok := FindCell("cell-2"); !ok {
		t.Error("FindCell 应能查到已存在的 cell-2")
	}
	if _, ok := FindCell("cell-nope"); ok {
		t.Error("FindCell 不应查到不存在的 cell")
	}
}
