package api

import (
	"errors"
	"testing"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
)

// setupTaskQueueConfig 为涉及任务持久化的用例提供最小配置，避免 SaveTasks 空指针。
func setupTaskQueueConfig(t *testing.T) {
	t.Helper()
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.EyvescloudConfig{}
}

// TestFinishTaskKeepsCancelledStatus 验证运行中被取消的任务在结束时不会被改回 done，
// 否则取消操作会被静默吞掉（历史留档里的取消记录也会被覆盖）。
func TestFinishTaskKeepsCancelledStatus(t *testing.T) {
	setupTaskQueueConfig(t)
	q := newTaskQueue(config.DefaultTaskConcurrency)
	task := &Task{
		ID:          "task-9",
		Type:        TaskStart,
		ContainerID: 1,
		Status:      "cancelled",
		StageDetail: "运行中任务已标记取消，将在当前步骤结束后停止",
		CreatedAt:   "2026-09-25 10:00:00",
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	q.mu.Lock()
	q.tasks[task.ID] = task
	q.activeTasks = 1
	task.activeKey = taskConcurrencyKey(task)
	q.activeTargets[task.activeKey] = true
	q.mu.Unlock()

	q.finishTask(task, "done", nil)

	if task.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", task.Status)
	}
	if task.Percent == 100 {
		t.Fatal("被取消的任务不应被标记 100% 完成")
	}
	if active := q.Settings().Active; active != 0 {
		t.Fatalf("active = %d, want 0（取消后应释放并发额度）", active)
	}
}

// TestFinishTaskMarksDoneOnSuccess 回归：正常任务结束时仍写入 done 且进度 100%。
func TestFinishTaskMarksDoneOnSuccess(t *testing.T) {
	setupTaskQueueConfig(t)
	q := newTaskQueue(config.DefaultTaskConcurrency)
	task := &Task{
		ID:          "task-10",
		Type:        TaskStart,
		ContainerID: 2,
		Status:      "running",
		CreatedAt:   "2026-09-25 10:00:00",
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	q.mu.Lock()
	q.tasks[task.ID] = task
	q.activeTasks = 1
	q.mu.Unlock()

	q.finishTask(task, "done", nil)

	if task.Status != "done" {
		t.Fatalf("status = %q, want done", task.Status)
	}
	if task.Percent != 100 {
		t.Fatalf("percent = %d, want 100", task.Percent)
	}
}

func TestRunnableTaskIndexSkipsActiveContainer(t *testing.T) {
	queue := []*Task{
		{ID: "task-1", Type: TaskStop, ContainerID: 1, ContainerName: "alpha"},
		{ID: "task-2", Type: TaskStart, ContainerID: 1, ContainerName: "alpha"},
		{ID: "task-3", Type: TaskStart, ContainerID: 2, ContainerName: "beta"},
	}
	active := map[string]bool{taskConcurrencyKey(queue[0]): true}

	if got := runnableTaskIndex(queue[1:], active); got != 1 {
		t.Fatalf("runnableTaskIndex() = %d, want 1 for the other container", got)
	}
}

func TestTaskConcurrencyKeyUsesContainerName(t *testing.T) {
	create := &Task{ID: "task-1", Type: TaskCreate, Config: lxcConfigWithName("Example")}
	operation := &Task{ID: "task-2", Type: TaskDelete, ContainerID: 9, ContainerName: "example"}
	if taskConcurrencyKey(create) != taskConcurrencyKey(operation) {
		t.Fatalf("same container received different concurrency keys: %q and %q", taskConcurrencyKey(create), taskConcurrencyKey(operation))
	}
}

func TestTaskQueueSetConcurrencyNormalizesAndReports(t *testing.T) {
	q := newTaskQueue(config.DefaultTaskConcurrency)
	q.SetConcurrency(config.MaxTaskConcurrency + 10)
	if got := q.Settings().Concurrency; got != config.MaxTaskConcurrency {
		t.Fatalf("concurrency = %d, want %d", got, config.MaxTaskConcurrency)
	}
	q.SetConcurrency(0)
	if got := q.Settings().Concurrency; got != config.DefaultTaskConcurrency {
		t.Fatalf("concurrency = %d, want default %d", got, config.DefaultTaskConcurrency)
	}
}

func TestTaskQueueUpdateTaskStage(t *testing.T) {
	q := newTaskQueue(config.DefaultTaskConcurrency)
	task := &Task{ID: "task-1", Type: TaskCreate, Status: "running"}

	q.updateTaskStage(task, "rootfs", "下载模板并创建基础文件系统")

	if task.Stage != "rootfs" || task.StageDetail != "下载模板并创建基础文件系统" {
		t.Fatalf("unexpected task stage: %q %q", task.Stage, task.StageDetail)
	}
}

func lxcConfigWithName(name string) lxc.ContainerConfig {
	return lxc.ContainerConfig{Name: name}
}

func TestTaskStagePercentIsMonotonicPerRuntime(t *testing.T) {
	cases := []struct {
		name   string
		virt   string
		stages []string
	}{
		{"lxc", "", createStageOrderLXC},
		{"kvm", config.VirtualizationKVM, createStageOrderKVM},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := newTaskQueue(config.DefaultTaskConcurrency)
			task := &Task{
				ID: "task-1", Type: TaskCreate, Status: "running",
				Config: lxc.ContainerConfig{Virtualization: tc.virt},
			}
			last := -1
			for _, stage := range tc.stages {
				q.updateTaskStage(task, stage, stage)
				if task.Percent < last {
					t.Fatalf("percent went backwards at stage %q: %d -> %d", stage, last, task.Percent)
				}
				last = task.Percent
			}
			if task.Percent <= 0 || task.Percent >= 100 {
				t.Fatalf("intermediate stages must stay in (0,100), got %d", task.Percent)
			}
		})
	}
}

func TestTaskStagePercentMappings(t *testing.T) {
	cases := []struct {
		virt  string
		stage string
		want  int
		ok    bool
	}{
		{"", "queued", 0, true},
		{"", "starting", 99, true},
		{"", "completed", 100, true},
		{config.VirtualizationKVM, "define", 0, true},
		{"", "no-such-stage", 0, false},
	}
	for _, tc := range cases {
		got, ok := taskStagePercent(tc.virt, tc.stage)
		if ok != tc.ok {
			t.Fatalf("taskStagePercent(%q,%q) ok = %v, want %v", tc.virt, tc.stage, ok, tc.ok)
		}
		if ok && tc.stage != "define" && got != tc.want {
			t.Fatalf("taskStagePercent(%q,%q) = %d, want %d", tc.virt, tc.stage, got, tc.want)
		}
		if ok && tc.stage == "define" && got <= 0 {
			t.Fatalf("kvm define stage should have positive percent, got %d", got)
		}
	}
}

func TestTaskStagePercentQueueIsZeroThenAdvances(t *testing.T) {
	q := newTaskQueue(config.DefaultTaskConcurrency)
	task := &Task{ID: "task-1", Type: TaskCreate, Status: "running"}

	q.updateTaskStage(task, "queued", "排队等待")
	if task.Percent != 0 {
		t.Fatalf("queued percent = %d, want 0", task.Percent)
	}

	q.updateTaskStage(task, "rootfs", "下载模板并创建基础文件系统")
	if task.Percent <= 0 {
		t.Fatalf("expected positive percent after rootfs, got %d", task.Percent)
	}
}

func TestTaskStagePercentNeverRegresses(t *testing.T) {
	q := newTaskQueue(config.DefaultTaskConcurrency)
	task := &Task{ID: "task-1", Type: TaskCreate, Status: "running"}

	q.updateTaskStage(task, "cloud_init", "写入 cloud-init 初始化配置")
	high := task.Percent
	if high <= 0 {
		t.Fatalf("expected positive percent after cloud_init, got %d", high)
	}

	// 更早的阶段（如运行时阶段顺序不同）不得让进度回退。
	q.updateTaskStage(task, "preparing", "检查模板与创建参数")
	if task.Percent != high {
		t.Fatalf("percent regressed from %d to %d", high, task.Percent)
	}
}

// TestQueueIdempotentDedupSameTargetAndType 幂等键回归：同一容器 + 同一类型的重复提交
// （前端双击、网络重试、脚本重放）必须合流为同一任务，不得产生第二个 job。
func TestQueueIdempotentDedupSameTargetAndType(t *testing.T) {
	setupTaskQueueConfig(t)
	q := newTaskQueue(config.DefaultTaskConcurrency)

	first, err := q.EnqueueWithAuditChecked(7, "alpha", TaskRestart, "", nil, "admin", "", "")
	if err != nil {
		t.Fatalf("首次入队失败: %v", err)
	}
	if len(first) != 1 || first[0].Deduped {
		t.Fatalf("首次入队结果 = %+v，应为单个未去重结果", first)
	}

	second, err := q.EnqueueWithAuditChecked(7, "alpha", TaskRestart, "", nil, "admin", "", "")
	if err != nil {
		t.Fatalf("重复入队失败: %v", err)
	}
	if len(second) != 1 || !second[0].Deduped {
		t.Fatalf("重复入队结果 = %+v，应命中幂等去重", second)
	}
	if second[0].TaskID != first[0].TaskID {
		t.Fatalf("去重命中 ID = %q，应为 %q", second[0].TaskID, first[0].TaskID)
	}
	if got := q.Settings().Pending; got != 1 {
		t.Fatalf("pending = %d，应为 1（重复提交不该新增任务）", got)
	}
}

// TestQueueDedupDistinguishesTaskType 不同类型不合并：同一容器的 start 与 stop 是两个
// 独立意图，幂等键必须包含任务类型。
func TestQueueDedupDistinguishesTaskType(t *testing.T) {
	setupTaskQueueConfig(t)
	q := newTaskQueue(config.DefaultTaskConcurrency)

	start, err := q.EnqueueWithAuditChecked(3, "beta", TaskStart, "", nil, "admin", "", "")
	if err != nil {
		t.Fatalf("start 入队失败: %v", err)
	}
	stop, err := q.EnqueueWithAuditChecked(3, "beta", TaskStop, "", nil, "admin", "", "")
	if err != nil {
		t.Fatalf("stop 入队失败: %v", err)
	}
	if stop[0].Deduped {
		t.Fatal("不同任务类型不应被去重合并")
	}
	if stop[0].TaskID == start[0].TaskID {
		t.Fatal("不同任务类型应分配到新的任务 ID")
	}
	if got := q.Settings().Pending; got != 2 {
		t.Fatalf("pending = %d，应为 2", got)
	}
}

// TestQueueDedupReleasedAfterTerminal 去重键在任务结束后释放：历史任务不应拦截新提交。
func TestQueueDedupReleasedAfterTerminal(t *testing.T) {
	setupTaskQueueConfig(t)
	q := newTaskQueue(config.DefaultTaskConcurrency)

	first, err := q.EnqueueWithAuditChecked(5, "gamma", TaskStart, "", nil, "admin", "", "")
	if err != nil {
		t.Fatalf("首次入队失败: %v", err)
	}
	q.mu.Lock()
	q.tasks[first[0].TaskID].Status = "done"
	q.mu.Unlock()

	second, err := q.EnqueueWithAuditChecked(5, "gamma", TaskStart, "", nil, "admin", "", "")
	if err != nil {
		t.Fatalf("再次入队失败: %v", err)
	}
	if second[0].Deduped {
		t.Fatal("已结束的任务不应拦截新提交")
	}
	if second[0].TaskID == first[0].TaskID {
		t.Fatal("新提交应分配到新的任务 ID")
	}
}

// TestQueueBackpressureRejectsWhenFull 背压回归：达到上限后新任务必须快速失败，
// 返回可读的 ErrQueueFull，并计入 rejected 统计（不得静默丢弃）。
func TestQueueBackpressureRejectsWhenFull(t *testing.T) {
	setupTaskQueueConfig(t)
	q := newTaskQueue(config.DefaultTaskConcurrency)
	q.SetMaxPending(2)

	if _, err := q.EnqueueWithAuditChecked(1, "n1", TaskStart, "", nil, "admin", "", ""); err != nil {
		t.Fatalf("第 1 个入队失败: %v", err)
	}
	if _, err := q.EnqueueWithAuditChecked(2, "n2", TaskStart, "", nil, "admin", "", ""); err != nil {
		t.Fatalf("第 2 个入队失败: %v", err)
	}
	if _, err := q.EnqueueWithAuditChecked(3, "n3", TaskStart, "", nil, "admin", "", ""); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("第 3 个入队 err = %v，应为 ErrQueueFull", err)
	}

	s := q.Settings()
	if s.Pending != 2 {
		t.Fatalf("pending = %d，应被上限限制为 2", s.Pending)
	}
	if s.MaxPending != 2 {
		t.Fatalf("max_pending = %d，应为 2", s.MaxPending)
	}
	if s.Rejected != 1 {
		t.Fatalf("rejected_total = %d，应为 1", s.Rejected)
	}
}

// TestQueueLegacyEnqueueIgnoresCap 遗留路径不启用背压：这些调用方无法上报错误，
// 若被上限拒绝就会变成静默丢弃 —— 必须继续入队。
func TestQueueLegacyEnqueueIgnoresCap(t *testing.T) {
	setupTaskQueueConfig(t)
	q := newTaskQueue(config.DefaultTaskConcurrency)
	q.SetMaxPending(1)

	if ids := q.EnqueueWithAudit(1, "n1", TaskStart, "", nil, "admin", "", ""); len(ids) != 1 {
		t.Fatalf("遗留入队 1 返回 %v，应为 1 个 ID", ids)
	}
	if ids := q.EnqueueWithAudit(2, "n2", TaskStart, "", nil, "admin", "", ""); len(ids) != 1 || ids[0] == "" {
		t.Fatalf("遗留入队 2 返回 %v，应仍入队 1 个 ID", ids)
	}

	s := q.Settings()
	if s.Pending != 2 {
		t.Fatalf("pending = %d，遗留路径应忽略上限仍入队 2 个", s.Pending)
	}
	if s.Rejected != 0 {
		t.Fatalf("rejected_total = %d，遗留路径不应计入拒绝", s.Rejected)
	}
}

// TestQueueBatchBackpressureReturnsPartial 批量入队遇上限时返回已受理结果 + 错误，
// 让调用方能同时告知「哪些已受理、哪些需重试」。
func TestQueueBatchBackpressureReturnsPartial(t *testing.T) {
	setupTaskQueueConfig(t)
	q := newTaskQueue(config.DefaultTaskConcurrency)
	q.SetMaxPending(2)

	outcomes, err := q.EnqueueBatchWithAuditChecked(TaskStart, []int{10, 11, 12}, "", "admin", "", "")
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("批量入队 err = %v，应为 ErrQueueFull", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("已受理结果 = %d 个，应为 2", len(outcomes))
	}
	if got := q.Settings().Pending; got != 2 {
		t.Fatalf("pending = %d，应为 2", got)
	}
}

// TestQueueRestoreBypassesDedup 恢复路径是权威状态：两条同目标同类型的持久化任务
// 必须各自入队，否则会被去重吞成永不结束的 pending。
func TestQueueRestoreBypassesDedup(t *testing.T) {
	setupTaskQueueConfig(t)
	q := newTaskQueue(config.DefaultTaskConcurrency)

	a := &Task{ID: "task-restore-a", Type: TaskStart, ContainerID: 4, ContainerName: "delta", Status: "pending"}
	b := &Task{ID: "task-restore-b", Type: TaskStart, ContainerID: 4, ContainerName: "delta", Status: "pending"}
	q.mu.Lock()
	q.restoreEnqueue(a)
	q.restoreEnqueue(b)
	q.mu.Unlock()

	if got := q.Settings().Pending; got != 2 {
		t.Fatalf("pending = %d，恢复路径应保留 2 个任务", got)
	}
}
