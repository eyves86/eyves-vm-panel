package scheduler

import (
	"strings"
	"testing"

	"eyvescloud/internal/config"
	"eyvescloud/internal/node"
)

func setupNodes(t *testing.T, nodes []config.Node) {
	t.Helper()
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{Nodes: append([]config.Node(nil), nodes...)}
}

func makeNode(id string, status string, ramTotal, ramUsed int64, diskTotal, diskUsed float64, containers int) config.Node {
	return config.Node{
		ID: id, Name: id, Status: status,
		RAMTotalMB: ramTotal, RAMUsedMB: ramUsed,
		DiskTotalGB: diskTotal, DiskUsedGB: diskUsed,
		ContainerCount: containers,
	}
}

// TestPlaceSelectsHighestScore 测试表驱动评分：固定一组节点 + 请求，断言选中。
func TestPlaceSelectsHighestScore(t *testing.T) {
	setupNodes(t, []config.Node{
		makeNode("n1", string(node.StatusOnline), 10000, 9000, 1000, 900, 50),
		makeNode("n2", string(node.StatusOnline), 10000, 5000, 1000, 400, 30),
		makeNode("n3", string(node.StatusOnline), 10000, 1000, 1000, 100, 10),
		makeNode("n4", string(node.StatusOffline), 10000, 0, 1000, 0, 0),
	})
	diag, err := Place(Request{RAMMB: 1000, DiskGB: 100, StorageBackend: "dir", RequestID: "req-1"}, DefaultPolicy(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if diag.Chosen != "n3" {
		t.Fatalf("chosen = %q, want n3 (highest free ratio + low containers)", diag.Chosen)
	}
	if !strings.Contains(diag.Reason, "highest score") {
		t.Fatalf("reason = %q", diag.Reason)
	}
}

// TestPlaceRejectsWhenAllOffline 验证全节点不可用时清晰报错。
func TestPlaceRejectsWhenAllOffline(t *testing.T) {
	setupNodes(t, []config.Node{
		makeNode("n1", string(node.StatusOffline), 10000, 0, 1000, 0, 0),
		makeNode("n2", string(node.StatusMaintenance), 10000, 0, 1000, 0, 0),
		makeNode("n3", string(node.StatusDraining), 10000, 0, 1000, 0, 0),
	})
	diag, err := Place(Request{RAMMB: 100, DiskGB: 10, StorageBackend: "dir"}, DefaultPolicy(), 3)
	if err == nil {
		t.Fatal("expected error when all nodes offline")
	}
	if diag.Chosen != "" {
		t.Fatalf("Chosen should be empty, got %q", diag.Chosen)
	}
	if !strings.Contains(diag.Reason, "rejected") {
		t.Fatalf("reason should summarize rejection, got %q", diag.Reason)
	}
	if len(diag.Candidates) == 0 {
		t.Fatal("Candidates should still expose top-N even on failure")
	}
}

// TestPlaceRejectsInsufficientCapacity 验证容量不足拒绝。
func TestPlaceRejectsInsufficientCapacity(t *testing.T) {
	setupNodes(t, []config.Node{
		makeNode("n1", string(node.StatusOnline), 10000, 9000, 1000, 950, 10),
	})
	_, err := Place(Request{RAMMB: 5000, DiskGB: 100, StorageBackend: "dir"}, DefaultPolicy(), 3)
	if err == nil {
		t.Fatal("expected error when RAM insufficient")
	}
}

// TestPlaceRejectsBackendMismatch 验证后端不匹配拒绝（保守策略）。
func TestPlaceRejectsBackendMismatch(t *testing.T) {
	setupNodes(t, []config.Node{
		makeNode("n1", string(node.StatusOnline), 10000, 0, 1000, 0, 0),
	})
	_, err := Place(Request{RAMMB: 100, DiskGB: 10, StorageBackend: "zfs"}, DefaultPolicy(), 3)
	if err == nil {
		t.Fatal("expected error when backend zfs not declared on node")
	}
}

// TestPlaceRecordsDecision 验证每次 Place 都留痕。
func TestPlaceRecordsDecision(t *testing.T) {
	setupNodes(t, []config.Node{
		makeNode("n1", string(node.StatusOnline), 10000, 0, 1000, 0, 0),
	})
	before := len(Decisions())
	_, _ = Place(Request{RAMMB: 100, DiskGB: 10, StorageBackend: "dir", RequestID: "rid"}, DefaultPolicy(), 3)
	after := len(Decisions())
	if after-before != 1 {
		t.Fatalf("expected 1 new decision, got %d", after-before)
	}
}

// TestPlaceNoNodesRegistered 验证空节点列表的报错。
func TestPlaceNoNodesRegistered(t *testing.T) {
	setupNodes(t, nil)
	_, err := Place(Request{RAMMB: 100}, DefaultPolicy(), 3)
	if err == nil {
		t.Fatal("expected error when no nodes registered")
	}
}

// TestPlaceSkipsZeroRAMNodes 验证未知 RAM 走回退评分，不被误拒。
func TestPlaceSkipsZeroRAMNodes(t *testing.T) {
	setupNodes(t, []config.Node{
		makeNode("n1", string(node.StatusOnline), 0, 0, 0, 0, 0),
	})
	diag, err := Place(Request{RAMMB: 100, DiskGB: 10, StorageBackend: "dir"}, DefaultPolicy(), 3)
	if err != nil {
		t.Fatalf("expected to place with unknown capacity, got %v", err)
	}
	if diag.Chosen != "n1" {
		t.Fatalf("chosen = %q, want n1", diag.Chosen)
	}
}