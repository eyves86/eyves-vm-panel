package restoredrill

import (
	"errors"
	"testing"
	"time"
)

func TestDrillSandboxFullPath(t *testing.T) {
	e := NewEngine(NoopDriver{}, AllowAllConfirmer{})
	r, err := e.DrillSandbox(BackupRef{Backend: "zfs", Instance: "ct-1", SnapshotID: "snap-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.BootSuccess {
		t.Fatal("boot must succeed")
	}
	if !r.ChecksumPass {
		t.Fatal("checksum must pass on matching files")
	}
	if r.SandboxID == "" {
		t.Fatal("sandbox id must be set")
	}
	if r.Mode != ModeSandbox {
		t.Fatalf("mode = %v, want sandbox", r.Mode)
	}
}

func TestDrillSandboxNilDriverErrors(t *testing.T) {
	e := NewEngine(nil, AllowAllConfirmer{})
	if _, err := e.DrillSandbox(BackupRef{}); err == nil {
		t.Fatal("nil driver must error")
	}
}

func TestRestoreProductionRequiresConfirmation(t *testing.T) {
	e := NewEngine(NoopDriver{}, AllowAllConfirmer{})
	r, err := e.RestoreProduction(BackupRef{}, 7, "admin@x")
	if err != nil || r == nil {
		t.Fatalf("allowed confirm must succeed: err=%v r=%v", err, r)
	}
}

func TestRestoreProductionDeniedByConfirmer(t *testing.T) {
	e := NewEngine(NoopDriver{}, DenyAllConfirmer{})
	_, err := e.RestoreProduction(BackupRef{}, 7, "admin@x")
	if err == nil {
		t.Fatal("denied must error")
	}
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("error = %v, want ErrNotConfirmed", err)
	}
}

func TestRestoreProductionNoConfirmerRejected(t *testing.T) {
	e := NewEngine(NoopDriver{}, nil)
	_, err := e.RestoreProduction(BackupRef{}, 7, "admin@x")
	if !errors.Is(err, ErrProductionRequiresConfirmation) {
		t.Fatalf("nil confirmer must require confirmation, got %v", err)
	}
}

type failingDriver struct{ NoopDriver }

func (failingDriver) RestoreSandbox(BackupRef) (string, error) {
	return "", errors.New("boom")
}

func TestDrillSandboxFailureRecorded(t *testing.T) {
	e := NewEngine(failingDriver{}, AllowAllConfirmer{})
	r, err := e.DrillSandbox(BackupRef{})
	if err == nil {
		t.Fatal("driver failure must surface")
	}
	if r == nil || r.Error == "" {
		t.Fatal("failure must be recorded in DrillResult")
	}
	if r.BootSuccess {
		t.Fatal("boot must be false on failure")
	}
	results := e.Results()
	if len(results) != 1 {
		t.Fatalf("results count = %d, want 1", len(results))
	}
}

func TestDrillSandboxReportsError(t *testing.T) {
	r := &DrillResult{
		ID:        "x",
		StartedAt: time.Now(),
		Mode:      ModeSandbox,
	}
	if r.Error != "" {
		t.Fatal("empty error expected by default")
	}
	r.Error = "boom"
	if r.Error != "boom" {
		t.Fatal("error field must be settable")
	}
}