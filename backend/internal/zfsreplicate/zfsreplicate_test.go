package zfsreplicate

import (
	"strings"
	"testing"
)

func TestSnapshotRefFullNameAndFingerprint(t *testing.T) {
	s := SnapshotRef{Pool: "tank", Dataset: "eyves/p1", Name: "snap-1"}
	want := "tank/eyves/p1@snap-1"
	if got := s.FullName(); got != want {
		t.Fatalf("FullName = %q, want %q", got, want)
	}
	if len(s.Fingerprint()) != 16 {
		t.Fatalf("fingerprint length = %d, want 16", len(s.Fingerprint()))
	}
}

func TestBuildIncrementalPlanFirstSyncIsFull(t *testing.T) {
	repo := Repo{
		Type: RepoZFSSSH, Address: "user@host",
		LocalPool: "tank", LocalDataset: "eyves/p1",
		RemotePool: "backup", RemoteDataset: "tank/eyves-rep",
	}
	plan, err := BuildIncrementalPlan(repo, SnapshotRef{}, SnapshotRef{
		Pool: "tank", Dataset: "eyves/p1", Name: "snap-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != ModeFull {
		t.Fatalf("first sync mode = %v, want full", plan.Mode)
	}
	if plan.RemoteTempDataset == plan.RemoteFinalDataset {
		t.Fatal("temp dataset must differ from final for atomic rename")
	}
}

func TestBuildIncrementalPlanSubsequentSyncIsIncremental(t *testing.T) {
	repo := Repo{LocalPool: "tank", LocalDataset: "eyves/p1", RemoteDataset: "tank/eyves-rep"}
	plan, err := BuildIncrementalPlan(repo,
		SnapshotRef{Pool: "tank", Dataset: "eyves/p1", Name: "snap-1"},
		SnapshotRef{Pool: "tank", Dataset: "eyves/p1", Name: "snap-2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != ModeIncremental {
		t.Fatalf("mode = %v, want incremental", plan.Mode)
	}
	if plan.SourceSnap.Name != "snap-1" || plan.TargetSnap.Name != "snap-2" {
		t.Fatalf("source/target = %+v / %+v", plan.SourceSnap, plan.TargetSnap)
	}
}

func TestBuildIncrementalPlanRejectsCrossPool(t *testing.T) {
	repo := Repo{LocalPool: "tank", LocalDataset: "eyves/p1", RemoteDataset: "tank/eyves-rep"}
	_, err := BuildIncrementalPlan(repo,
		SnapshotRef{Pool: "tank", Dataset: "eyves/p1", Name: "snap-1"},
		SnapshotRef{Pool: "otherpool", Dataset: "eyves/p1", Name: "snap-2"},
	)
	if err == nil {
		t.Fatal("cross-pool must error")
	}
}

func TestBuildIncrementalPlanRejectsSameSnap(t *testing.T) {
	repo := Repo{LocalPool: "tank", LocalDataset: "eyves/p1", RemoteDataset: "tank/eyves-rep"}
	snap := SnapshotRef{Pool: "tank", Dataset: "eyves/p1", Name: "snap-1"}
	_, err := BuildIncrementalPlan(repo, snap, snap)
	if err == nil {
		t.Fatal("last == new must error")
	}
}

func TestBuildIncrementalPlanRejectsWrongDataset(t *testing.T) {
	repo := Repo{LocalPool: "tank", LocalDataset: "eyves/p1", RemoteDataset: "tank/eyves-rep"}
	_, err := BuildIncrementalPlan(repo,
		SnapshotRef{Pool: "tank", Dataset: "eyves/p1", Name: "snap-1"},
		SnapshotRef{Pool: "tank", Dataset: "eyves/OTHER", Name: "snap-2"},
	)
	if err == nil {
		t.Fatal("wrong dataset must error")
	}
}

func TestBuildLocalSendCommandsFull(t *testing.T) {
	plan := TransferPlan{Mode: ModeFull, LocalSnap: SnapshotRef{Pool: "tank", Dataset: "p", Name: "s1"}}
	cmds, err := BuildLocalSendCommands(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0][0] != "send" || cmds[0][2] != "tank/p@s1" {
		t.Fatalf("send full = %v", cmds)
	}
}

func TestBuildLocalSendCommandsIncremental(t *testing.T) {
	plan := TransferPlan{
		Mode:      ModeIncremental,
		SourceSnap: SnapshotRef{Pool: "tank", Dataset: "p", Name: "s1"},
		TargetSnap: SnapshotRef{Pool: "tank", Dataset: "p", Name: "s2"},
	}
	cmds, err := BuildLocalSendCommands(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cmds[0], " "), "-i tank/p@s1 tank/p@s2") {
		t.Fatalf("send incr = %v", cmds)
	}
}

func TestBuildRemoteCommandsValidateInputs(t *testing.T) {
	plan := TransferPlan{RemoteTempDataset: "tank/eyves/temp", RemoteFinalDataset: "tank/eyves/final"}
	if _, err := BuildRemoteRecvCommands(plan, ""); err == nil {
		t.Fatal("empty ssh must error")
	}
	if _, err := BuildRemoteRenameCommands(TransferPlan{}, "user@host"); err == nil {
		t.Fatal("rename requires final + temp")
	}
	if _, err := BuildRemoteCleanupCommands(TransferPlan{}, "user@host"); err == nil {
		t.Fatal("cleanup requires temp")
	}
}

func TestSafeSnapName(t *testing.T) {
	cases := map[string]string{
		"normal-snap_1":     "normal-snap_1",
		"../escape":         "___escape",
		"with space":        "with_space",
		"":                  "snap",
	}
	for in, want := range cases {
		if got := safeSnapName(in); got != want {
			t.Fatalf("safeSnapName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCryptoBoxRoundtrip(t *testing.T) {
	box := NoopBox{}
	sealed, err := box.Seal("password-1")
	if err != nil {
		t.Fatal(err)
	}
	if sealed != "password-1" {
		t.Fatalf("sealed = %q", sealed)
	}
	opened, err := box.Open(sealed)
	if err != nil || opened != "password-1" {
		t.Fatalf("opened = %q, err=%v", opened, err)
	}
}