package api

import (
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
