package node

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"eyvescloud/internal/config"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(EnsureSchemaSQL); err != nil {
		t.Fatal(err)
	}
	return db
}

func setupConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	previous := config.AppConfig
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	os.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		if previous == nil {
			config.AppConfig = nil
		} else {
			config.AppConfig = previous
		}
		os.Unsetenv("EYVESCLOUD_DATA_DIR")
		config.SetConfigPath("")
	})
	if _, err := config.InitConfig(); err != nil {
		t.Fatal(err)
	}
}

func addTestNode(t *testing.T, id string) {
	t.Helper()
	err := config.AddNode(config.Node{ID: id, Name: id, Status: "offline"})
	if err != nil {
		t.Fatal(err)
	}
}

// ---- Status 枚举与状态机 ----

func TestNormalizeStatus(t *testing.T) {
	cases := map[string]Status{
		"online":      StatusOnline,
		"offline":     StatusOffline,
		"maintenance": StatusMaintenance,
		"draining":    StatusDraining,
		"pending":     StatusOffline,
		"":            StatusOffline,
		"bogus":       StatusOffline,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Fatalf("Normalize(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsValidStatus(t *testing.T) {
	for _, s := range []Status{StatusOnline, StatusOffline, StatusMaintenance, StatusDraining} {
		if !s.IsValid() {
			t.Fatalf("%v not valid", s)
		}
	}
	for _, s := range []Status{"", "bogus", "pending"} {
		if s.IsValid() {
			t.Fatalf("%v should be invalid", s)
		}
	}
}

func TestTransitionLegality(t *testing.T) {
	legal := []struct{ from, to Status }{
		{StatusOnline, StatusOffline},
		{StatusOnline, StatusMaintenance},
		{StatusOnline, StatusDraining},
		{StatusOffline, StatusOnline},
		{StatusOffline, StatusMaintenance},
		{StatusMaintenance, StatusOnline},
		{StatusMaintenance, StatusDraining},
		{StatusDraining, StatusMaintenance},
		{StatusDraining, StatusOffline},
	}
	for _, c := range legal {
		if err := Transition(c.from, c.to); err != nil {
			t.Fatalf("expected legal: %s -> %s: %v", c.from, c.to, err)
		}
	}
	illegal := []struct{ from, to Status }{
		{StatusOffline, StatusDraining},   // 不允许离线直接排水
		{StatusDraining, StatusOnline},    // 不允许从排水直跳在线
		{StatusMaintenance, StatusOffline}, // 维护态必须显式维护退出
		{"bogus", StatusOnline},            // 非法源
		{StatusOnline, "bogus"},            // 非法目标
	}
	for _, c := range illegal {
		if err := Transition(c.from, c.to); !errors.Is(err, ErrIllegalTransition) {
			t.Fatalf("expected illegal: %s -> %s, got %v", c.from, c.to, err)
		}
	}
}

func TestAllowsContainerOps(t *testing.T) {
	if !AllowsContainerOps(StatusOnline) {
		t.Fatal("online must allow container ops")
	}
	for _, s := range []Status{StatusOffline, StatusMaintenance, StatusDraining, ""} {
		if AllowsContainerOps(s) {
			t.Fatalf("%v must NOT allow container ops", s)
		}
	}
}

func TestAllowsNewInstance(t *testing.T) {
	if !AllowsNewInstance(StatusOnline) {
		t.Fatal("online must allow new instance")
	}
	for _, s := range []Status{StatusOffline, StatusMaintenance, StatusDraining} {
		if AllowsNewInstance(s) {
			t.Fatalf("%v must NOT allow new instance", s)
		}
	}
}

// ---- Heartbeat 与租约 ----

func TestHeartbeatRenewal(t *testing.T) {
	setupConfig(t)
	db := newTestDB(t)
	addTestNode(t, "node-hb")

	if err := Heartbeat("node-hb", "tok-1", 60*time.Second, db); err != nil {
		t.Fatal(err)
	}
	got, ok := config.FindNode("node-hb")
	if !ok || got.Status != string(StatusOnline) {
		t.Fatalf("expected node online after heartbeat, got %+v ok=%v", got, ok)
	}
	if !ValidateToken("node-hb", "tok-1", db) {
		t.Fatal("token must validate")
	}
	if ValidateToken("node-hb", "tok-2", db) {
		t.Fatal("wrong token must fail")
	}
}

func TestHeartbeatResetsToken(t *testing.T) {
	setupConfig(t)
	db := newTestDB(t)
	addTestNode(t, "node-hb2")
	_ = Heartbeat("node-hb2", "old", 60*time.Second, db)
	_ = Heartbeat("node-hb2", "new", 60*time.Second, db)
	if !ValidateToken("node-hb2", "new", db) {
		t.Fatal("new token must validate")
	}
	if ValidateToken("node-hb2", "old", db) {
		t.Fatal("old token must be invalidated")
	}
}

func TestHeartbeatRequiresFields(t *testing.T) {
	setupConfig(t)
	db := newTestDB(t)
	if err := Heartbeat("", "t", time.Second, db); err == nil {
		t.Fatal("empty node id must error")
	}
	if err := Heartbeat("n", " ", time.Second, db); err == nil {
		t.Fatal("empty token must error")
	}
}

// ---- Scanner 与租约超时 → offline ----

func TestScannerMarksOfflineOnExpiredLease(t *testing.T) {
	setupConfig(t)
	db := newTestDB(t)
	addTestNode(t, "node-exp")

	// 先 Heartbeat 让节点 online，再注入一条已过期的租约（模拟心跳中断）。
	if err := Heartbeat("node-exp", "tok-1", 60*time.Second, db); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE node_leases SET expires_at = ? WHERE node_id = ?`, past, "node-exp"); err != nil {
		t.Fatal(err)
	}

	var captured []EventStateChanged
	var evMu sync.Mutex
	sink := func(e EventStateChanged) error {
		evMu.Lock()
		captured = append(captured, e)
		evMu.Unlock()
		return nil
	}
	rl := &ReconcileLock{DB: db}
	sc, err := NewScanner(db, rl, sinkFunc(sink), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	marked, err := sc.RunOnce()
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 1 || marked[0] != "node-exp" {
		t.Fatalf("marked = %v, want [node-exp]", marked)
	}
	got, _ := config.FindNode("node-exp")
	if got.Status != string(StatusOffline) {
		t.Fatalf("status = %q, want offline", got.Status)
	}
	evMu.Lock()
	defer evMu.Unlock()
	if len(captured) != 1 || captured[0].To != StatusOffline || captured[0].From != StatusOnline {
		t.Fatalf("event = %+v, want one state_changed -> offline from online", captured)
	}
}

func TestScannerSkipsInProgressLeases(t *testing.T) {
	setupConfig(t)
	db := newTestDB(t)
	addTestNode(t, "node-inprog")
	// in_progress=1：另一实例已抢占；本次扫描不应拾取。
	past := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO node_leases (node_id, token, expires_at, in_progress) VALUES (?, ?, ?, 1)`,
		"node-inprog", "tok", past); err != nil {
		t.Fatal(err)
	}
	rl := &ReconcileLock{DB: db}
	sc, _ := NewScanner(db, rl, nil, time.Second)
	marked, err := sc.RunOnce()
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 0 {
		t.Fatalf("in-progress lease must be skipped by other scanners, got %v", marked)
	}
}

func TestScannerConcurrentOnlyOneWins(t *testing.T) {
	setupConfig(t)
	db := newTestDB(t)
	addTestNode(t, "node-race")
	past := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO node_leases (node_id, token, expires_at, in_progress) VALUES (?, ?, ?, 0)`,
		"node-race", "tok", past); err != nil {
		t.Fatal(err)
	}
	rl := &ReconcileLock{DB: db}
	sc, _ := NewScanner(db, rl, nil, time.Second)

	// 顺序执行两次：第一次拾取 → 第二次看不到。
	first, _ := sc.RunOnce()
	second, _ := sc.RunOnce()
	if len(first) != 1 || len(second) != 0 {
		t.Fatalf("expected first to win, second to see empty; got %v / %v", first, second)
	}
}

func TestScannerUnknownNodeClearsInProgress(t *testing.T) {
	setupConfig(t)
	db := newTestDB(t)
	past := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO node_leases (node_id, token, expires_at, in_progress) VALUES (?, ?, ?, 0)`,
		"orphan", "tok", past); err != nil {
		t.Fatal(err)
	}
	rl := &ReconcileLock{DB: db}
	sc, _ := NewScanner(db, rl, nil, time.Second)
	marked, err := sc.RunOnce()
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 0 {
		t.Fatalf("orphan lease must not appear as marked, got %v", marked)
	}
	// MarkProcessed 删除已处理行；下一次心跳 INSERT（ON CONFLICT 覆盖）会重新建。
	var count int
	if err := db.QueryRow(`SELECT COUNT(1) FROM node_leases WHERE node_id = ?`, "orphan").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("orphan lease must be deleted after processing, got %d rows", count)
	}
}

// ---- Event sink 注入 ----

type sinkFunc func(EventStateChanged) error

func (f sinkFunc) Emit(e EventStateChanged) error { return f(e) }

// ---- EnsureSchema 幂等 ----

func TestEnsureSchemaIdempotent(t *testing.T) {
	db := newTestDB(t)
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(EnsureSchemaSQL); err != nil {
			t.Fatalf("EnsureSchema round %d: %v", i, err)
		}
	}
}