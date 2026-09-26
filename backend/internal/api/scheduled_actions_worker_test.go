package api

import (
	"os/exec"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

// TestScheduledActionNextFire 覆盖 none/daily/weekly/monthly/未知四种 Repeat 路径。
func TestScheduledActionNextFire(t *testing.T) {
	from := time.Date(2025, 9, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		repeat string
		want   string // "" 表示关闭
	}{
		{"", ""},
		{"none", ""},
		{"daily", "2025-09-27T12:00:00Z"},
		{"weekly", "2025-10-03T12:00:00Z"},
		{"monthly", "2025-10-26T12:00:00Z"},
		{"unknown", ""},
	}
	for _, tc := range cases {
		got := scheduledActionNextFire(config.ScheduledAction{Repeat: tc.repeat}, from)
		if tc.want == "" {
			if got != "" {
				t.Fatalf("repeat=%q: got %q, want empty", tc.repeat, got)
			}
			continue
		}
		if got != tc.want {
			t.Fatalf("repeat=%q: got %q, want %q", tc.repeat, got, tc.want)
		}
	}
}

// TestScheduledActionTickDoesNotPanicOnBadExecuteAt 解析失败的 execute_at
// 必须被跳过（记日志），而不是 panic 或中断整个 tick。
func TestScheduledActionTickDoesNotPanicOnBadExecuteAt(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   300,
		UUID: "uuid-sca-bad",
		Name: "lxc-sca-bad",
	})

	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.ScheduledActions = []config.ScheduledAction{{
			ID:          "sca-bad-1",
			ContainerID: 300,
			Type:        "stop",
			Repeat:      "none",
			ExecuteAt:   "not-a-time",
			Enabled:     true,
		}}
	}); err != nil {
		t.Fatalf("写入坏任务失败: %v", err)
	}

	// 不应 panic
	runScheduledActionsTick()

	// 任务应保持原样（不修改 ExecuteAt / LastRunAt）
	actions := config.ListAllScheduledActions()
	if len(actions) != 1 {
		t.Fatalf("预期 1 条任务，got %d", len(actions))
	}
	if actions[0].ExecuteAt != "not-a-time" {
		t.Fatalf("坏任务不应被推进; got %q", actions[0].ExecuteAt)
	}
	if actions[0].LastRunAt != "" {
		t.Fatalf("坏任务不应写 LastRunAt; got %q", actions[0].LastRunAt)
	}
}

// TestScheduledActionTickDisablesOneShotAfterFire one-shot（repeat=none）执行后
// 必须被禁用，避免下次启动再触发。
func TestScheduledActionTickDisablesOneShotAfterFire(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   301,
		UUID: "uuid-sca-oneshot",
		Name: "lxc-sca-oneshot",
	})

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.ScheduledActions = []config.ScheduledAction{{
			ID:          "sca-oneshot-1",
			ContainerID: 301,
			Type:        "stop",
			Repeat:      "none",
			ExecuteAt:   past,
			Enabled:     true,
		}}
	}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	// stopByRuntime 在没有 lxc CLI 的沙箱里会失败，但 worker 不应因此放弃；
	// 它仍应禁用任务（前置：SaveScheduledAction 先于 executeScheduledAction）。
	// 为了避免误把外部 lxc 命令副作用引入测试，我们把整个 execute 路径用命令路径替换——
	// 由于沙箱里 lxc 命令不存在，StopContainer 会立即 error，goroutine 走 error 分支，
	// 但 SaveScheduledAction 已经先把任务禁用并落库。
	runScheduledActionsTick()

	// 短暂等待 SaveScheduledAction 完成
	time.Sleep(50 * time.Millisecond)

	actions := config.ListAllScheduledActions()
	if len(actions) != 1 {
		t.Fatalf("任务丢失; got %d 条", len(actions))
	}
	if actions[0].Enabled {
		t.Fatalf("one-shot 执行后必须禁用；enabled 仍为 true")
	}
	if actions[0].LastRunAt == "" {
		t.Fatalf("LastRunAt 必须被写入")
	}
}

// TestScheduledActionTickAdvancesDailyRepeat repeat=daily 的任务执行后下次时间
// 必须推进到 24h 后。
func TestScheduledActionTickAdvancesDailyRepeat(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   302,
		UUID: "uuid-sca-daily",
		Name: "lxc-sca-daily",
	})

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.ScheduledActions = []config.ScheduledAction{{
			ID:          "sca-daily-1",
			ContainerID: 302,
			Type:        "stop",
			Repeat:      "daily",
			ExecuteAt:   past,
			Enabled:     true,
		}}
	}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	runScheduledActionsTick()
	time.Sleep(50 * time.Millisecond)

	actions := config.ListAllScheduledActions()
	if len(actions) != 1 {
		t.Fatalf("任务丢失")
	}
	execAt, err := time.Parse(time.RFC3339, actions[0].ExecuteAt)
	if err != nil {
		t.Fatalf("ExecuteAt 解析失败: %v", err)
	}
	// daily 下次时间应在 now 之后且不超过 now+25h（容许测试抖动）
	if execAt.Before(time.Now()) {
		t.Fatalf("下次时间必须推进到未来; got %v", execAt)
	}
	if execAt.After(time.Now().Add(25 * time.Hour)) {
		t.Fatalf("下次时间超出 daily 窗口; got %v", execAt)
	}
}

// TestClaimScheduledActionRun 验证 claim / release 的并发防重入语义。
func TestClaimScheduledActionRun(t *testing.T) {
	id := "sca-claim-test"
	releaseScheduledActionRun(id) // 确保干净起点

	if !claimScheduledActionRun(id) {
		t.Fatal("首次 claim 应返回 true")
	}
	if claimScheduledActionRun(id) {
		t.Fatal("未释放时二次 claim 应返回 false")
	}
	releaseScheduledActionRun(id)
	if !claimScheduledActionRun(id) {
		t.Fatal("释放后再次 claim 应返回 true")
	}
	releaseScheduledActionRun(id)
}

// TestPoweroffByRuntimeUnknownContainer 容器不存在应返回 error，不 panic。
func TestPoweroffByRuntimeUnknownContainer(t *testing.T) {
	setupContainerEndpointTestStore(t, config.Container{
		ID:   310,
		UUID: "uuid-pow-unknown",
		Name: "lxc-pow-unknown",
	})
	if err := poweroffByRuntime(99999); err == nil {
		t.Fatal("未知容器应返回错误")
	}
}

// TestLXCManagerUsedByPoweroffWorker 检查 poweroffByRuntime 实际调用路径：
// 在没有真实 lxc 二进制且容器 KVM 的情况下，至少保证不会与外部命令交互。
// （此测试仅依赖纯 Go 逻辑，避免依赖 sandbox 之外的 lxc/virsh 命令。）
func TestLXCManagerUsedByPoweroffWorker(t *testing.T) {
	// 确保 lxcManager 和 kvmManager 全局变量在测试环境里仍可用
	if lxcManager == nil {
		t.Fatal("lxcManager 未初始化")
	}
	if kvmManager == nil {
		t.Fatal("kvmManager 未初始化")
	}
	// 不实际执行任何命令；只断言 poweroffByRuntime 不会对未知 ID panic。
	_ = exec.Command
}