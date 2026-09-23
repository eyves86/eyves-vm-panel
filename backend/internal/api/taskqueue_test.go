package api

import (
	"testing"

	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
)

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
