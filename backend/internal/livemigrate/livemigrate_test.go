package livemigrate

import (
	"errors"
	"testing"
)

func TestStartMigrationSharedSkipsTransfer(t *testing.T) {
	e := NewEngine(&NoopTransferDriver{}, &NoopNodeController{})
	m, err := e.StartMigration(7, "node-a", "node-b", StorageShared)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != StatusDone {
		t.Fatalf("status = %v, want done", m.Status)
	}
	if m.BytesMoved != 0 {
		t.Fatalf("shared storage must not move bytes, got %d", m.BytesMoved)
	}
}

func TestStartMigrationRequiresSourceAndTarget(t *testing.T) {
	e := NewEngine(&NoopTransferDriver{}, &NoopNodeController{})
	if _, err := e.StartMigration(0, "a", "b", StorageShared); err == nil {
		t.Fatal("zero instance must error")
	}
	if _, err := e.StartMigration(1, "", "b", StorageShared); err == nil {
		t.Fatal("empty source must error")
	}
	if _, err := e.StartMigration(1, "a", "a", StorageShared); err == nil {
		t.Fatal("source==target must error")
	}
}

func TestStartMigrationNilEnginesError(t *testing.T) {
	if _, err := NewEngine(nil, &NoopNodeController{}).StartMigration(1, "a", "b", StorageShared); err == nil {
		t.Fatal("nil driver must error")
	}
	if _, err := NewEngine(&NoopTransferDriver{}, nil).StartMigration(1, "a", "b", StorageShared); err == nil {
		t.Fatal("nil controller must error")
	}
}

func TestStartMigrationFenceFailureLeavesSourceLive(t *testing.T) {
	c := &NoopNodeController{FenceErr: errors.New("fence timeout")}
	e := NewEngine(&NoopTransferDriver{}, c)
	m, err := e.StartMigration(1, "a", "b", StorageShared)
	if err == nil {
		t.Fatal("fence failure must error")
	}
	if m.Status != StatusFailed {
		t.Fatalf("status = %v, want failed", m.Status)
	}
}

func TestStartMigrationTransferFailureUnfences(t *testing.T) {
	c := &NoopNodeController{}
	d := &NoopTransferDriver{TransferErr: errors.New("disk full")}
	e := NewEngine(d, c)
	m, err := e.StartMigration(1, "a", "b", StorageLocal)
	if err == nil {
		t.Fatal("transfer failure must error")
	}
	// 失败回滚后状态应为 Failed（中间状态 RolledBack → Failed）。
	if m.Status != StatusFailed {
		t.Fatalf("status = %v, want failed", m.Status)
	}
	if m.BytesMoved != 0 {
		t.Fatalf("bytes moved = %d, want 0", m.BytesMoved)
	}
}

func TestStartMigrationTargetStartFailureRollsBack(t *testing.T) {
	c := &NoopNodeController{StartTargetErr: errors.New("libvirt timeout")}
	e := NewEngine(&NoopTransferDriver{}, c)
	m, err := e.StartMigration(1, "a", "b", StorageLocal)
	if err == nil {
		t.Fatal("start failure must error")
	}
	if m.Status != StatusFailed {
		t.Fatalf("status = %v, want failed", m.Status)
	}
	if m.Error == "" {
		t.Fatal("error must be recorded")
	}
}

func TestMigrationGet(t *testing.T) {
	e := NewEngine(&NoopTransferDriver{}, &NoopNodeController{})
	m, _ := e.StartMigration(1, "a", "b", StorageShared)
	got, ok := e.Get(m.ID)
	if !ok {
		t.Fatal("Get must succeed")
	}
	if got.InstanceID != 1 {
		t.Fatalf("instance = %d, want 1", got.InstanceID)
	}
	if _, ok := e.Get("missing"); ok {
		t.Fatal("missing must fail")
	}
}