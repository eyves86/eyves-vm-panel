package config

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// TestCellShardedNodes 验证 P3-c 第一片（nodes 按 cell 分库）的行为：
//  1. 未配置 cell 库时，带 cell_id 的节点仍落控制库（与历史一致）；
//  2. 配置 cell 库并重启后，控制库里属于该 cell 的节点被搬进 cell 库、控制库不再留行，
//     且读回时两个库的节点被合并（不重不漏）；
//  3. 节点更新写回 cell 库、删除也从 cell 库生效；
//  4. 节点换库（控制↔cell）时新库写入、旧库删除，不留双份；重启后仍不重不漏。
func TestCellShardedNodes(t *testing.T) {
	// 阶段 1：未配置 cell 库。
	prev, had := os.LookupEnv(cellDSNsEnv)
	os.Unsetenv(cellDSNsEnv)
	t.Cleanup(func() {
		if had {
			os.Setenv(cellDSNsEnv, prev)
		} else {
			os.Unsetenv(cellDSNsEnv)
		}
	})

	cfgPath := setupMetaTest(t)
	// 登记 cell-1（与 EYVESCLOUD_CELL_DSNS 中的键对应；未登记时引用完整性检查会把
	// 节点的 CellID 清空）。
	AppConfig.Cells = []Cell{{ID: "cell-1", Name: "分片一"}}
	AppConfig.Nodes = []Node{
		{ID: "n-cell", Name: "cell 节点", CellID: "cell-1", CreatedAt: "2026-10-08 01:00:00"},
		{ID: "n-ctrl", Name: "控制库节点", CreatedAt: "2026-10-08 02:00:00"},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if got := countNodesIn(t, db, "n-cell"); got != 1 {
		t.Fatalf("阶段1：未配置 cell 库时节点应落控制库，count=%d", got)
	}

	// 阶段 2：配置 cell 库后重启。
	cellDSN, err := testCellDSN("cell-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv(cellDSNsEnv, "cell-1="+cellDSN); err != nil {
		t.Fatal(err)
	}
	resetConfigStoreForTest(t)
	SetConfigPath(cfgPath)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	st, ok := cellStoresSnapshot()["cell-1"]
	if !ok {
		t.Fatal("cell-1 库未打开")
	}
	if got := countNodesIn(t, st.db, "n-cell"); got != 1 {
		t.Fatalf("搬迁后 cell 库应含 n-cell，count=%d", got)
	}
	if got := countNodesIn(t, db, "n-cell"); got != 0 {
		t.Fatalf("搬迁后控制库不应再有 n-cell，count=%d", got)
	}
	if len(AppConfig.Nodes) != 2 {
		t.Fatalf("读回节点数 = %d, want 2", len(AppConfig.Nodes))
	}
	byID := map[string]Node{}
	for _, n := range AppConfig.Nodes {
		byID[n.ID] = n
	}
	if byID["n-cell"].CellID != "cell-1" || byID["n-ctrl"].ID != "n-ctrl" {
		t.Fatalf("读回节点内容不对: %+v", AppConfig.Nodes)
	}

	// 阶段 3a：更新 cell 节点 → 写回 cell 库，不得在控制库新建行。
	for i := range AppConfig.Nodes {
		if AppConfig.Nodes[i].ID == "n-cell" {
			AppConfig.Nodes[i].Name = "改名后"
		}
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if got := countNodesIn(t, st.db, "n-cell"); got != 1 {
		t.Fatalf("更新后 cell 库应仍有 n-cell，count=%d", got)
	}
	if got := countNodesIn(t, db, "n-cell"); got != 0 {
		t.Fatalf("更新不应在控制库新建 n-cell，count=%d", got)
	}

	// 阶段 3b：删除 cell 节点 → 从 cell 库删除。
	kept := AppConfig.Nodes[:0]
	for _, n := range AppConfig.Nodes {
		if n.ID != "n-cell" {
			kept = append(kept, n)
		}
	}
	AppConfig.Nodes = kept
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if got := countNodesIn(t, st.db, "n-cell"); got != 0 {
		t.Fatalf("删除后 cell 库不应再有 n-cell，count=%d", got)
	}

	// 阶段 4：控制库 → cell（换库）。n-ctrl 原在控制库，改归属到 cell-1 后应写进 cell 库，
	// 并从控制库删掉——否则同一节点会在两库各留一份，重启后重复出现。
	for i := range AppConfig.Nodes {
		if AppConfig.Nodes[i].ID == "n-ctrl" {
			AppConfig.Nodes[i].CellID = "cell-1"
		}
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if got := countNodesIn(t, st.db, "n-ctrl"); got != 1 {
		t.Fatalf("阶段4：换库后 cell 库应含 n-ctrl，count=%d", got)
	}
	if got := countNodesIn(t, db, "n-ctrl"); got != 0 {
		t.Fatalf("阶段4：换库后控制库不应再留 n-ctrl，count=%d", got)
	}

	// 阶段 5：cell → 控制库（反向换库）。这一步走「精确脏集声明」入口（管理接口/心跳
	// 落库的实际路径），与阶段 4 的全量扫描分支互为对照。n-ctrl 归属清空后应写回控制库，
	// 并从 cell 库删掉。
	var moved Node
	for i := range AppConfig.Nodes {
		if AppConfig.Nodes[i].ID == "n-ctrl" {
			AppConfig.Nodes[i].CellID = ""
			moved = AppConfig.Nodes[i]
		}
	}
	AppConfigMu.Lock()
	err = saveConfigToDBHinted(newExactDirtySet(DirtySet{Nodes: []Node{moved}}))
	AppConfigMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := countNodesIn(t, db, "n-ctrl"); got != 1 {
		t.Fatalf("阶段5：换回后控制库应含 n-ctrl，count=%d", got)
	}
	if got := countNodesIn(t, st.db, "n-ctrl"); got != 0 {
		t.Fatalf("阶段5：换回后 cell 库不应再留 n-ctrl，count=%d", got)
	}

	// 阶段 6：重启后不重不漏（读回归并去重）。
	resetConfigStoreForTest(t)
	SetConfigPath(cfgPath)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	if len(AppConfig.Nodes) != 1 || AppConfig.Nodes[0].ID != "n-ctrl" {
		t.Fatalf("重启后节点集合 = %+v, want 仅 n-ctrl", AppConfig.Nodes)
	}
}

// TestMergeCellNodesDedupesAcrossStores 验证「换库崩溃窗口」留下的两库同 ID 副本在加载
// 合并时被去重（取 LastSeen 较新者），且控制库的同 ID 副本让位给 cell 库副本。
func TestMergeCellNodesDedupesAcrossStores(t *testing.T) {
	cfg := &EyvescloudConfig{
		// 控制库留了一份同 ID 旧副本，应被 cell 库副本覆盖。
		Nodes: []Node{{ID: "n-dup", Name: "控制库旧", CellID: "", LastSeen: "2026-10-08 00:00:00"}},
	}
	mergeCellNodes(cfg, []Node{
		{ID: "n-dup", Name: "cell-1 旧", CellID: "cell-1", LastSeen: "2026-10-08 01:00:00"},
		{ID: "n-dup", Name: "cell-2 新", CellID: "cell-2", LastSeen: "2026-10-08 03:00:00"},
		{ID: "n-other", Name: "另一个", CellID: "cell-2"},
	})
	if len(cfg.Nodes) != 2 {
		t.Fatalf("合并后节点数 = %d, want 2（n-dup 只留一份 + n-other）: %+v", len(cfg.Nodes), cfg.Nodes)
	}
	got := map[string]Node{}
	for _, n := range cfg.Nodes {
		got[n.ID] = n
	}
	if got["n-dup"].Name != "cell-2 新" || got["n-dup"].CellID != "cell-2" {
		t.Fatalf("重复 ID 应取 LastSeen 较新者: %+v", got["n-dup"])
	}
	if got["n-other"].ID != "n-other" {
		t.Fatalf("不重复的节点不应丢失: %+v", cfg.Nodes)
	}
}

func countNodesIn(t *testing.T, conn *sql.DB, id string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM nodes WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCellShardedContainers 验证 P3-c 第二片（containers 按「其节点的 cell」分库）：
//  1. 未配置 cell 库时容器落控制库（与历史一致）；
//  2. 配置 cell 库并重启后，节点与其容器一并搬进 cell 库（含端口映射子表），访问码
//     凭据（container_access_links）按设计留在控制库、合并后仍能挂回；
//  3. 未分配节点的容器始终留控制库；
//  4. 精确脏集声明（心跳路径）更新 cell 库的容器行；
//  5. 节点换回控制库时其容器一并迁回（子表同行），旧库不留双份；
//  6. 重启后不重不漏、子表与改动完整。
func TestCellShardedContainers(t *testing.T) {
	prev, had := os.LookupEnv(cellDSNsEnv)
	os.Unsetenv(cellDSNsEnv)
	t.Cleanup(func() {
		if had {
			os.Setenv(cellDSNsEnv, prev)
		} else {
			os.Unsetenv(cellDSNsEnv)
		}
	})

	cfgPath := setupMetaTest(t)
	AppConfig.Cells = []Cell{{ID: "cell-1", Name: "分片一"}}
	AppConfig.Nodes = []Node{{ID: "n-cell", Name: "cell 节点", CellID: "cell-1", CreatedAt: "2026-10-08 01:00:00"}}
	AppConfig.Containers = []Container{{
		ID: 1, UUID: "u-1", Name: "c1", NodeID: "n-cell", Status: "running",
		CreatedAt:    "2026-10-08 01:00:00",
		PortMappings: []PortMapping{{ContainerPort: 22, HostPort: 2201, Protocol: "tcp"}},
		AccessCode:   "AC-TEST",
	}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if got := countContainersIn(t, db); got != 1 {
		t.Fatalf("阶段1：未配置 cell 库时容器应落控制库，count=%d", got)
	}

	// 阶段 2：配置 cell 库后重启 → 节点与其容器一并搬迁、合并读回。
	cellDSN, err := testCellDSN("cell-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv(cellDSNsEnv, "cell-1="+cellDSN); err != nil {
		t.Fatal(err)
	}
	resetConfigStoreForTest(t)
	SetConfigPath(cfgPath)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	st, ok := cellStoresSnapshot()["cell-1"]
	if !ok {
		t.Fatal("cell-1 库未打开")
	}
	if got := countContainersIn(t, st.db); got != 1 {
		t.Fatalf("阶段2：cell 库应含容器，count=%d", got)
	}
	if got := countContainersIn(t, db); got != 0 {
		t.Fatalf("阶段2：控制库不应再留容器，count=%d", got)
	}
	if got := countPortMappingsIn(t, st.db, 1); got != 1 {
		t.Fatalf("阶段2：端口映射子行应随主行进 cell 库，count=%d", got)
	}
	if len(AppConfig.Containers) != 1 || AppConfig.Containers[0].ID != 1 ||
		len(AppConfig.Containers[0].PortMappings) != 1 || AppConfig.Containers[0].AccessCode != "AC-TEST" {
		t.Fatalf("阶段2：合并读回的容器不完整: %+v", AppConfig.Containers)
	}

	// 阶段 3：未分配节点的容器留控制库。
	AppConfig.Containers = append(AppConfig.Containers, Container{
		ID: 2, UUID: "u-2", Name: "c2", Status: "running", CreatedAt: "2026-10-08 02:00:00",
	})
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if got := countContainersIn(t, db); got != 1 {
		t.Fatalf("阶段3：未分配节点的容器应落控制库，count=%d", got)
	}
	if got := countContainersIn(t, st.db); got != 1 {
		t.Fatalf("阶段3：cell 库容器数不应变，count=%d", got)
	}

	// 阶段 4：精确脏集声明（心跳路径的落库形状）更新 cell 库的容器行。
	for i := range AppConfig.Containers {
		if AppConfig.Containers[i].ID == 1 {
			AppConfig.Containers[i].Remark = "心跳改的"
		}
	}
	n := AppConfig.Nodes[0]
	var c1 Container
	for _, c := range AppConfig.Containers {
		if c.ID == 1 {
			c1 = c
		}
	}
	AppConfigMu.Lock()
	err = saveConfigToDBHinted(newExactDirtySet(DirtySet{Nodes: []Node{n}, Containers: []Container{c1}}))
	AppConfigMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := containerRemarkIn(t, st.db, 1); got != "心跳改的" {
		t.Fatalf("阶段4：cell 库容器行未更新，remark=%q", got)
	}

	// 阶段 5：节点换回控制库（全量保存）→ 其容器一并迁回，旧库不留行。
	AppConfig.Nodes[0].CellID = ""
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	if got := countContainersIn(t, db); got != 2 {
		t.Fatalf("阶段5：迁回后控制库应含 2 个容器，count=%d", got)
	}
	if got := countPortMappingsIn(t, db, 1); got != 1 {
		t.Fatalf("阶段5：端口映射子行应随容器迁回控制库，count=%d", got)
	}
	if got := countContainersIn(t, st.db); got != 0 {
		t.Fatalf("阶段5：cell 库不应再留容器，count=%d", got)
	}

	// 阶段 6：重启后不重不漏、子表与改动完整。
	resetConfigStoreForTest(t)
	SetConfigPath(cfgPath)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	if len(AppConfig.Containers) != 2 {
		t.Fatalf("重启后容器数 = %d, want 2", len(AppConfig.Containers))
	}
	for _, c := range AppConfig.Containers {
		if c.ID == 1 && (len(c.PortMappings) != 1 || c.Remark != "心跳改的" || c.AccessCode != "AC-TEST") {
			t.Fatalf("重启后 c1 不完整: %+v", c)
		}
	}
}

// TestMergeCellContainersDedupesAcrossStores 验证「迁移/换库崩溃窗口」留下的两库同 ID
// 副本在加载合并时被去重（cell 库副本让位规则：控制库副本让位），并恢复全局 ID 序。
func TestMergeCellContainersDedupesAcrossStores(t *testing.T) {
	cfg := &EyvescloudConfig{
		Containers: []Container{
			{ID: 7, UUID: "u-7", Name: "控制库残留"},
			{ID: 3, UUID: "u-3", Name: "控制库独有"},
		},
	}
	mergeCellContainers(cfg, []Container{
		{ID: 7, UUID: "u-7", Name: "cell 库副本"},
		{ID: 5, UUID: "u-5", Name: "cell 库独有"},
	})
	if len(cfg.Containers) != 3 {
		t.Fatalf("合并后容器数 = %d, want 3（ID=7 只留一份）: %+v", len(cfg.Containers), cfg.Containers)
	}
	if cfg.Containers[0].ID != 3 || cfg.Containers[1].ID != 5 || cfg.Containers[2].ID != 7 {
		t.Fatalf("合并后应按 ID 全局升序: %+v", cfg.Containers)
	}
	if cfg.Containers[2].Name != "cell 库副本" {
		t.Fatalf("重复 ID 应取 cell 库副本: %+v", cfg.Containers[2])
	}
}

func countContainersIn(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM containers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countPortMappingsIn(t *testing.T, conn *sql.DB, containerID int) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM port_mappings WHERE container_id = ?`, containerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func containerRemarkIn(t *testing.T, conn *sql.DB, id int) string {
	t.Helper()
	var s sql.NullString
	if err := conn.QueryRow(`SELECT remark FROM containers WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s.String
}

// TestLoadDanglingNodeContainerBoots 容器 NodeID 指向不存在的节点（节点已删而
// 容器未清的库）时，装载不得 panic：装载期 AppConfig 尚未装配（为 nil），
// containerStoreIDIn 的全局回扫曾在此空指针崩溃——此类库一重启即全线挂。
// 文档语义：节点不存在 → 控制库。
func TestLoadDanglingNodeContainerBoots(t *testing.T) {
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	dir := t.TempDir()
	SetConfigPath(filepath.Join(dir, "config.json"))

	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfigMu.Lock()
	AppConfig.Containers = []Container{{
		ID: 1, UUID: "uuid-ghost", Name: "ct-ghost",
		NodeID: "node-gone", Status: "running", Template: "ubuntu-22.04",
	}}
	AppConfigMu.Unlock()
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	loaded, err := InitConfig()
	if err != nil {
		t.Fatalf("带悬空 NodeID 的库必须能正常装载: %v", err)
	}
	if len(loaded.Containers) != 1 || loaded.Containers[0].NodeID != "node-gone" {
		t.Fatalf("容器应原样读回: %+v", loaded.Containers)
	}
}
