package api

import (
	"fmt"
	"log"
	"sync"
	"time"

	"eyvescloud/internal/config"
)

// scheduledActionsWorker 容器级定时启停任务调度器（对齐主流面板语义）。
//
// 设计目标：
//   - 每分钟扫描一次 ScheduleActions，命中 ExecuteAt 且 Enabled=true 的任务。
//   - 计算下次执行时间（按 Repeat 推算）后写入配置，再执行幂等标记避免重复触发。
//   - 节点转发：调用 startByRuntime / stopByRuntime / restartByRuntime，
//     这些函数内部已经会按 NodeID 转发到被控 agent。
//
// 幂等：
//   - scheduledActionRunning 内存表防止本进程内并发重复触发；
//   - 写回 LastRunAt 后再次启动时按"已执行过的时间窗"判重（避免重启后的双触发）。
const scheduledActionsTickInterval = time.Minute

var (
	scheduledActionsWorkerOnce sync.Once
	scheduledActionRunningMu  sync.Mutex
	scheduledActionRunning    = map[string]bool{}
)

// StartScheduledActionsWorker 启动定时任务调度器（每分钟检查一次）。
// 在 server.Run 中与其他后台 worker 一起启动。
func StartScheduledActionsWorker() {
	scheduledActionsWorkerOnce.Do(func() {
		go func() {
			// 启动后立即跑一次（不等待 ticker），便于上次进程崩溃遗留的过期任务立即触发。
			runScheduledActionsTick()
			ticker := time.NewTicker(scheduledActionsTickInterval)
			defer ticker.Stop()
			for range ticker.C {
				runScheduledActionsTick()
			}
		}()
	})
}

func runScheduledActionsTick() {
	if !maintenanceLeaseActive() {
		return
	}
	now := time.Now().UTC()
	actions := config.ListAllScheduledActions()
	for _, a := range actions {
		if !a.Enabled {
			continue
		}
		execAt, err := time.Parse(time.RFC3339, a.ExecuteAt)
		if err != nil {
			// 配置异常：记日志后跳过；不要让坏数据导致整个 tick 退出。
			log.Printf("scheduled-action %s: execute_at 解析失败: %v", a.ID, err)
			continue
		}
		if now.Before(execAt) {
			continue
		}
		if !claimScheduledActionRun(a.ID) {
			continue
		}
		// 先推进下次时间 / 标记执行，避免下一 tick 重复触发。
		a.LastRunAt = now.Format(time.RFC3339)
		if next := scheduledActionNextFire(a, now); next != "" {
			a.ExecuteAt = next
		} else {
			// 非重复任务：执行后禁用，防止每次启动都重复触发。
			a.Enabled = false
		}
		updated := a
		if err := config.SaveScheduledAction(updated); err != nil {
			log.Printf("scheduled-action %s: 持久化失败: %v", a.ID, err)
			releaseScheduledActionRun(a.ID)
			continue
		}
		// tick 里起 goroutine 异步执行，但 tick 结束前 WaitGroup 等待全部完成，
		// 避免下次 tick 叠加上一轮还没执行完的任务；也让测试可以在 runScheduledActionsTick
		// 返回后立即 assert，不用 sleep 等待异步。
		wg.Add(1)
		go func(a config.ScheduledAction) {
			defer wg.Done()
			defer releaseScheduledActionRun(a.ID)
			executeScheduledAction(a)
		}(a)
	}
	wg.Wait()
}

// runScheduledActionsTick 的 goroutine 都登记到这个 WaitGroup，便于单元测试和 race 检测。
var wg sync.WaitGroup

// scheduledActionNextFire 根据 Repeat 计算下次执行时间（RFC3339）。
//   - none / 空：返回 ""（调用方会禁用任务）。
//   - daily：+24h。
//   - weekly：+168h。
//   - monthly：按"同一天次月"对齐，二月按 28/29 天自动收口（最简实现）。
func scheduledActionNextFire(a config.ScheduledAction, from time.Time) string {
	switch a.Repeat {
	case "", "none":
		return ""
	case "daily":
		return from.Add(24 * time.Hour).Format(time.RFC3339)
	case "weekly":
		return from.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	case "monthly":
		next := from.AddDate(0, 1, 0)
		return next.Format(time.RFC3339)
	default:
		return ""
	}
}

func claimScheduledActionRun(id string) bool {
	scheduledActionRunningMu.Lock()
	defer scheduledActionRunningMu.Unlock()
	if scheduledActionRunning[id] {
		return false
	}
	scheduledActionRunning[id] = true
	return true
}

func releaseScheduledActionRun(id string) {
	scheduledActionRunningMu.Lock()
	delete(scheduledActionRunning, id)
	scheduledActionRunningMu.Unlock()
}

// executeScheduledAction 执行一次任务（start/stop/restart/poweroff）。
// 错误仅写日志，不返回 HTTP（这是后台 worker，不是 handler）。
func executeScheduledAction(a config.ScheduledAction) {
	c := config.FindContainer(a.ContainerID)
	if c == nil {
		log.Printf("scheduled-action %s: 容器 %d 不存在，跳过", a.ID, a.ContainerID)
		return
	}
	if c.Suspended {
		log.Printf("scheduled-action %s: 容器 %d 已挂起，跳过", a.ID, c.ID)
		return
	}
	var err error
	switch a.Type {
	case "start":
		err = startByRuntime(c.ID)
	case "stop":
		err = stopByRuntime(c.ID)
	case "restart":
		err = restartByRuntime(c.ID)
	case "poweroff":
		err = poweroffByRuntime(c.ID)
	default:
		log.Printf("scheduled-action %s: 未知 type=%q", a.ID, a.Type)
		return
	}
	if err != nil {
		log.Printf("scheduled-action %s: 容器 %d %s 失败: %v", a.ID, c.ID, a.Type, err)
		return
	}
	log.Printf("scheduled-action %s: 容器 %d %s 成功", a.ID, c.ID, a.Type)
}

// poweroffByRuntime 容器硬关机（断电等价），对齐主流面板语义。
// 不复用 stopByRuntime：stop 是优雅关机（acpid），poweroff 是立即切断电源（仅 KVM）。
// LXC 容器没有真正的"硬断电"语义，复用 stop。
func poweroffByRuntime(id int) error {
	c := config.FindContainer(id)
	if c == nil {
		return fmt.Errorf("容器 %d 不存在", id)
	}
	if c.IsKVM() {
		// KVM：qemu 进程直接 destroy（等价 virsh destroy），避免依赖 guest acpid。
		if err := kvmManager.PoweroffContainer(id); err != nil {
			return err
		}
		return nil
	}
	return lxcManager.StopContainer(id)
}