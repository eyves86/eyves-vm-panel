package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
)

type TaskType string

const (
	TaskCreate    TaskType = "create"
	TaskStart     TaskType = "start"
	TaskStop      TaskType = "stop"
	TaskRestart   TaskType = "restart"
	TaskDelete    TaskType = "delete"
	TaskReinstall TaskType = "reinstall"
)

type Task struct {
	ID            string              `json:"id"`
	Type          TaskType            `json:"type"`
	ContainerID   int                 `json:"container_id"`
	ContainerName string              `json:"container_name"`
	Status        string              `json:"status"`
	Error         string              `json:"error,omitempty"`
	Stage         string              `json:"stage,omitempty"`
	StageDetail   string              `json:"stage_detail,omitempty"`
	Percent       int                 `json:"percent"`
	CreatedAt     string              `json:"created_at"`
	StartedAt     string              `json:"started_at,omitempty"` // 任务开始执行的时间（RFC3339）
	TemplateID    string              `json:"template_id,omitempty"`
	Config        lxc.ContainerConfig `json:"config,omitempty"`
	Name          string              `json:"name,omitempty"`
	User          string              `json:"user,omitempty"` // who created this task
	IP            string              `json:"ip,omitempty"`
	UserAgent     string              `json:"user_agent,omitempty"`
	activeKey     string
}

type TaskQueue struct {
	mu             sync.Mutex
	createQueue    []*Task
	opQueue        []*Task
	tasks          map[string]*Task
	nextID         int
	createCond     *sync.Cond
	opCond         *sync.Cond
	maxConcurrency int
	maxPending     int
	rejectedTotal  uint64
	activeTasks    int
	activeTargets  map[string]bool
	stop           chan struct{}
}

type TaskQueueSettings struct {
	Concurrency int    `json:"concurrency"`
	Active      int    `json:"active"`
	Pending     int    `json:"pending"`
	MaxPending  int    `json:"max_pending"`
	Rejected    uint64 `json:"rejected_total"`
}

// ErrQueueFull 队列已达背压阈值：新任务被快速拒绝（可读错误，不做假 UI）。
var ErrQueueFull = errors.New("容器操作队列已满，请稍后重试")

// EnqueueOutcome 描述一次入队的结果：
//   - TaskID：新建或被命中的既有任务 ID；
//   - Deduped：命中幂等去重（同一目标 + 同一类型已有 pending/running 任务），未新建任务。
type EnqueueOutcome struct {
	TaskID  string `json:"task_id"`
	Deduped bool   `json:"deduped"`
}

var globalQueue *TaskQueue

func init() {
	globalQueue = newTaskQueue(config.DefaultTaskConcurrency)
	go globalQueue.createDispatcher()
	go globalQueue.opDispatcher()
}

func newTaskQueue(concurrency int) *TaskQueue {
	q := &TaskQueue{
		tasks:          make(map[string]*Task),
		maxConcurrency: config.NormalizeTaskConcurrency(concurrency),
		maxPending:     config.DefaultTaskQueueMaxPending,
		activeTargets:  make(map[string]bool),
		stop:           make(chan struct{}),
	}
	q.createCond = sync.NewCond(&q.mu)
	q.opCond = sync.NewCond(&q.mu)
	return q
}

// ConfigureTaskQueue 在启动时应用持久化的队列参数（并发上限 + 背压阈值）。
func ConfigureTaskQueue(concurrency int, maxPending int) {
	globalQueue.SetConcurrency(concurrency)
	globalQueue.SetMaxPending(maxPending)
}

func (q *TaskQueue) SetConcurrency(concurrency int) {
	q.mu.Lock()
	q.maxConcurrency = config.NormalizeTaskConcurrency(concurrency)
	q.createCond.Broadcast()
	q.opCond.Broadcast()
	q.mu.Unlock()
}

// SetMaxPending 调整背压阈值（<=0 取默认）。供设置页与测试使用。
func (q *TaskQueue) SetMaxPending(maxPending int) {
	q.mu.Lock()
	q.maxPending = config.NormalizeTaskQueueMaxPending(maxPending)
	q.mu.Unlock()
}

func (q *TaskQueue) Settings() TaskQueueSettings {
	q.mu.Lock()
	defer q.mu.Unlock()
	return TaskQueueSettings{
		Concurrency: q.maxConcurrency,
		Active:      q.activeTasks,
		Pending:     len(q.createQueue) + len(q.opQueue),
		MaxPending:  q.maxPending,
		Rejected:    q.rejectedTotal,
	}
}

func (q *TaskQueue) signalDispatchers() {
	q.createCond.Broadcast()
	q.opCond.Broadcast()
}

// findActiveDuplicateLocked 在锁内查找「同一目标 + 同一类型」且仍在排队/执行的任务。
// 幂等键 = 容器并发键（名字优先，其次 ID）+ 任务类型：重复提交（前端双击、网络重试、
// 脚本重放）不该产生第二个 job —— 否则同一容器会被连续重启/删除两次。
// 仅对操作类任务生效（创建任务走名字唯一性守卫，不在此列）。
func (q *TaskQueue) findActiveDuplicateLocked(task *Task) *Task {
	if task.Type == TaskCreate {
		return nil
	}
	key := taskConcurrencyKey(task)
	for _, existing := range q.tasks {
		if existing == task || existing.Type != task.Type {
			continue
		}
		if existing.Status != "pending" && existing.Status != "running" {
			continue
		}
		if taskConcurrencyKey(existing) == key {
			return existing
		}
	}
	return nil
}

// selectDupOrSpace 幂等去重 + 可选背压检查（须持有 q.mu）。
//
//   - 幂等：命中「同一目标 + 同一类型」的 pending/running 任务则返回既有任务，不新建；
//   - 背压：enforceCap 时，待执行总数达阈值则返回 ErrQueueFull（快速失败、可读错误）。
//
// 背压只在调用方能向上报错的路径开启（enforceCap=true）；无法上报的遗留内部调用
// 仍走 dedup-only —— 否则保守的队列上限会变成「静默丢弃」，违背「失败要有反馈」。
func (q *TaskQueue) selectDupOrSpace(task *Task, enforceCap bool) (*Task, bool, error) {
	if dup := q.findActiveDuplicateLocked(task); dup != nil {
		return dup, true, nil
	}
	if enforceCap && len(q.createQueue)+len(q.opQueue) >= q.maxPending {
		q.rejectedTotal++
		return nil, false, ErrQueueFull
	}
	q.tasks[task.ID] = task
	if task.Type == TaskCreate {
		q.createQueue = append(q.createQueue, task)
		q.createCond.Signal()
	} else {
		q.opQueue = append(q.opQueue, task)
		q.opCond.Signal()
	}
	return task, false, nil
}

// enqueueTask 去重入队（不设上限；无法上报错误的遗留路径使用）。须持有 q.mu。
func (q *TaskQueue) enqueueTask(task *Task) (*Task, bool) {
	t, deduped, _ := q.selectDupOrSpace(task, false)
	return t, deduped
}

// restoreEnqueue 把从磁盘恢复的任务直接放回队列。恢复路径是权威状态：不做幂等去重
// （两条同目标同类型的持久化任务都必须各自执行，否则会被吞成永不结束的 pending），
// 也不受背压上限约束（这些任务本就已存在，不是新提交）。须持有 q.mu。
func (q *TaskQueue) restoreEnqueue(task *Task) {
	q.tasks[task.ID] = task
	if task.Type == TaskCreate {
		q.createQueue = append(q.createQueue, task)
		q.createCond.Signal()
	} else {
		q.opQueue = append(q.opQueue, task)
		q.opCond.Signal()
	}
}

// outcomeIDs 把入队结果投影为任务 ID 列表（供遗留 []string 签名使用）。
func outcomeIDs(outcomes []EnqueueOutcome) []string {
	ids := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		ids = append(ids, o.TaskID)
	}
	return ids
}

// newOpTaskLocked 分配 ID 并构造操作任务（须持有 q.mu）。
func (q *TaskQueue) newOpTaskLocked(containerID int, containerName string, taskType TaskType, templateID string, cfg *lxc.ContainerConfig, user string, ip string, userAgent string) *Task {
	id := q.nextID
	q.nextID++
	task := &Task{
		ID:            fmt.Sprintf("task-%d", id),
		Type:          taskType,
		ContainerID:   containerID,
		ContainerName: containerName,
		Status:        "pending",
		Stage:         "queued",
		StageDetail:   "排队等待",
		CreatedAt:     time.Now().Format("2006-01-02 15:04:05"),
		TemplateID:    templateID,
		User:          user,
		IP:            ip,
		UserAgent:     userAgent,
	}
	if cfg != nil {
		task.Config = *cfg
		task.Config.NormalizeResourceAliases()
	}
	return task
}

func (q *TaskQueue) Enqueue(containerID int, containerName string, taskType TaskType, templateID string, cfg *lxc.ContainerConfig) []string {
	return q.EnqueueWithAudit(containerID, containerName, taskType, templateID, cfg, "admin", "", "")
}

// EnqueueWithAudit 入队单个操作任务（遗留 []string 签名；幂等去重生效、不启用背压）。
func (q *TaskQueue) EnqueueWithAudit(containerID int, containerName string, taskType TaskType, templateID string, cfg *lxc.ContainerConfig, user string, ip string, userAgent string) []string {
	outcomes, _ := q.enqueueOp(containerID, containerName, taskType, templateID, cfg, user, ip, userAgent, false)
	return outcomeIDs(outcomes)
}

// EnqueueWithAuditChecked 入队单个操作任务：幂等去重 + 背压。
// 队列达阈值时返回 ErrQueueFull（调用方须回可读错误，不得静默丢弃）。
func (q *TaskQueue) EnqueueWithAuditChecked(containerID int, containerName string, taskType TaskType, templateID string, cfg *lxc.ContainerConfig, user string, ip string, userAgent string) ([]EnqueueOutcome, error) {
	return q.enqueueOp(containerID, containerName, taskType, templateID, cfg, user, ip, userAgent, true)
}

func (q *TaskQueue) enqueueOp(containerID int, containerName string, taskType TaskType, templateID string, cfg *lxc.ContainerConfig, user string, ip string, userAgent string, enforceCap bool) ([]EnqueueOutcome, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	task := q.newOpTaskLocked(containerID, containerName, taskType, templateID, cfg, user, ip, userAgent)
	enq, deduped, err := q.selectDupOrSpace(task, enforceCap)
	if err != nil {
		return nil, err
	}
	q.persistTasks()
	return []EnqueueOutcome{{TaskID: enq.ID, Deduped: deduped}}, nil
}

func (q *TaskQueue) EnqueueBatch(taskType TaskType, ids []int, templateID string) []string {
	return q.EnqueueBatchWithUser(taskType, ids, templateID, "admin")
}

func (q *TaskQueue) EnqueueBatchWithUser(taskType TaskType, ids []int, templateID string, user string) []string {
	return q.EnqueueBatchWithAudit(taskType, ids, templateID, user, "", "")
}

// EnqueueBatchWithAudit 批量入队操作任务（遗留签名；去重生效、不启用背压）。
func (q *TaskQueue) EnqueueBatchWithAudit(taskType TaskType, ids []int, templateID string, user string, ip string, userAgent string) []string {
	outcomes, _ := q.enqueueBatch(taskType, ids, templateID, user, ip, userAgent, false)
	return outcomeIDs(outcomes)
}

// EnqueueBatchWithAuditChecked 批量入队：去重 + 背压；关键词触发上限时返回已入队结果 + ErrQueueFull。
func (q *TaskQueue) EnqueueBatchWithAuditChecked(taskType TaskType, ids []int, templateID string, user string, ip string, userAgent string) ([]EnqueueOutcome, error) {
	return q.enqueueBatch(taskType, ids, templateID, user, ip, userAgent, true)
}

func (q *TaskQueue) enqueueBatch(taskType TaskType, ids []int, templateID string, user string, ip string, userAgent string, enforceCap bool) ([]EnqueueOutcome, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	outcomes := make([]EnqueueOutcome, 0, len(ids))
	for _, id := range ids {
		c := config.FindContainer(id)
		name := ""
		if c != nil {
			name = c.Name
		}
		task := q.newOpTaskLocked(id, name, taskType, templateID, nil, user, ip, userAgent)
		enq, deduped, err := q.selectDupOrSpace(task, enforceCap)
		if err != nil {
			// 背压：停止后续入队，但已入队的任务保持有效（调用方回可读错误）。
			return outcomes, err
		}
		outcomes = append(outcomes, EnqueueOutcome{TaskID: enq.ID, Deduped: deduped})
	}
	q.persistTasks()
	return outcomes, nil
}

func (q *TaskQueue) EnqueueBatchCreate(configs []lxc.ContainerConfig) []string {
	return q.EnqueueBatchCreateWithAudit(configs, "admin", "", "")
}

// EnqueueBatchCreateWithAudit 批量入队创建任务（遗留签名；创建不参与去重、不启用背压）。
func (q *TaskQueue) EnqueueBatchCreateWithAudit(configs []lxc.ContainerConfig, user string, ip string, userAgent string) []string {
	outcomes, _ := q.enqueueCreates(configs, user, ip, userAgent, false)
	return outcomeIDs(outcomes)
}

// EnqueueBatchCreateWithAuditChecked 批量入队创建任务：背压（创建不参与幂等去重）。
func (q *TaskQueue) EnqueueBatchCreateWithAuditChecked(configs []lxc.ContainerConfig, user string, ip string, userAgent string) ([]EnqueueOutcome, error) {
	return q.enqueueCreates(configs, user, ip, userAgent, true)
}

func (q *TaskQueue) ActiveCreateNames() map[string]bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	names := make(map[string]bool)
	for _, task := range q.tasks {
		if task.Type != TaskCreate || (task.Status != "pending" && task.Status != "running") {
			continue
		}
		name := task.Config.Name
		if name == "" {
			name = task.ContainerName
		}
		if name != "" {
			names[name] = true
		}
	}
	return names
}

func (q *TaskQueue) enqueueCreates(configs []lxc.ContainerConfig, user string, ip string, userAgent string, enforceCap bool) ([]EnqueueOutcome, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	outcomes := make([]EnqueueOutcome, 0, len(configs))
	for _, cfg := range configs {
		cfgCopy := cfg
		cfgCopy.NormalizeResourceAliases()
		id := q.nextID
		q.nextID++
		task := &Task{
			ID:            fmt.Sprintf("task-%d", id),
			Type:          TaskCreate,
			ContainerID:   0,
			ContainerName: cfgCopy.Name,
			Status:        "pending",
			Stage:         "queued",
			StageDetail:   "排队等待",
			CreatedAt:     time.Now().Format("2006-01-02 15:04:05"),
			Config:        cfgCopy,
			User:          user,
			IP:            ip,
			UserAgent:     userAgent,
		}
		enq, deduped, err := q.selectDupOrSpace(task, enforceCap)
		if err != nil {
			return outcomes, err
		}
		outcomes = append(outcomes, EnqueueOutcome{TaskID: enq.ID, Deduped: deduped})
	}
	q.persistTasks()
	return outcomes, nil
}

func (q *TaskQueue) enqueueSingle(containerID int, containerName string, taskType TaskType, templateID string) string {
	return q.enqueueSingleWithUser(containerID, containerName, taskType, templateID, "admin")
}

func (q *TaskQueue) enqueueSingleWithUser(containerID int, containerName string, taskType TaskType, templateID string, user string) string {
	return q.enqueueSingleWithAudit(containerID, containerName, taskType, templateID, user, "", "")
}

// enqueueSingleWithAudit 去重入队（须持有 q.mu；不启用背压）。返回命中/新建任务的 ID。
func (q *TaskQueue) enqueueSingleWithAudit(containerID int, containerName string, taskType TaskType, templateID string, user string, ip string, userAgent string) string {
	task := q.newOpTaskLocked(containerID, containerName, taskType, templateID, nil, user, ip, userAgent)
	enq, _ := q.enqueueTask(task)
	return enq.ID
}

func (q *TaskQueue) EnqueueSecurityStop(containerID int, containerName string) (string, bool) {
	if !config.AppConfig.SecurityAutoShutdown {
		return "", false
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	for _, task := range q.tasks {
		if task.Type != TaskStop || task.ContainerID != containerID {
			continue
		}
		if task.Status == "pending" || task.Status == "running" {
			return task.ID, false
		}
	}

	taskID := q.enqueueSingleWithAudit(containerID, containerName, TaskStop, "", "system:security", "", "")
	q.persistTasks()
	return taskID, true
}

func (q *TaskQueue) CancelPendingSecurityStops() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	cancelled := 0
	newOpQueue := make([]*Task, 0, len(q.opQueue))
	for _, task := range q.opQueue {
		if isSecurityStopTask(task) && task.Status == "pending" {
			delete(q.tasks, task.ID)
			cancelled++
			continue
		}
		newOpQueue = append(newOpQueue, task)
	}
	q.opQueue = newOpQueue

	for id, task := range q.tasks {
		if isSecurityStopTask(task) && task.Status == "pending" {
			delete(q.tasks, id)
			cancelled++
		}
	}
	if cancelled > 0 {
		q.persistTasks()
	}
	return cancelled
}

// The two dispatchers keep long-running creates from blocking power operations,
// while sharing one global concurrency budget.
func (q *TaskQueue) createDispatcher() {
	for {
		task := q.takeNextTask(true)
		go q.runCreateTask(task)
	}
}

func (q *TaskQueue) opDispatcher() {
	for {
		task := q.takeNextTask(false)
		go q.runOperationTask(task)
	}
}

func (q *TaskQueue) takeNextTask(create bool) *Task {
	q.mu.Lock()
	cond := q.opCond
	if create {
		cond = q.createCond
	}
	for {
		queue := q.opQueue
		if create {
			queue = q.createQueue
		}
		if q.activeTasks < q.maxConcurrency {
			if index := runnableTaskIndex(queue, q.activeTargets); index >= 0 {
				task := queue[index]
				queue = append(queue[:index], queue[index+1:]...)
				if create {
					q.createQueue = queue
				} else {
					q.opQueue = queue
				}
				// 已取消的任务不再执行（取消发生在入队之后、派发之前）。
				if task.Status == "canceled" {
					continue
				}
				task.Status = "running"
				task.Error = ""
				task.Stage = "preparing"
				task.StageDetail = "准备初始化环境"
				// 记录任务实际开始执行的时间，供历史留档计算耗时。
				task.StartedAt = time.Now().UTC().Format(time.RFC3339)
				if percent, ok := taskStagePercent(task.Config.Virtualization, "preparing"); ok && percent > task.Percent {
					task.Percent = percent
				}
				task.activeKey = taskConcurrencyKey(task)
				q.activeTargets[task.activeKey] = true
				q.activeTasks++
				q.persistTasks()
				taskID := task.ID
				q.mu.Unlock()
				// 锁外落库：追加"任务开始执行"日志，避免持锁做 IO 造成阻塞。
				_ = config.AppendTaskLog(taskID, "INFO", "任务开始执行")
				return task
			}
		}
		cond.Wait()
	}
}

func runnableTaskIndex(queue []*Task, activeTargets map[string]bool) int {
	for index, task := range queue {
		if !activeTargets[taskConcurrencyKey(task)] {
			return index
		}
	}
	return -1
}

func taskConcurrencyKey(task *Task) string {
	if task == nil {
		return "task:nil"
	}
	name := strings.TrimSpace(task.ContainerName)
	if name == "" {
		name = strings.TrimSpace(task.Config.Name)
	}
	if name != "" {
		return "name:" + strings.ToLower(name)
	}
	if task.ContainerID > 0 {
		return fmt.Sprintf("id:%d", task.ContainerID)
	}
	return "task:" + task.ID
}

func (q *TaskQueue) finishTask(task *Task, status string, taskErr error) {
	q.mu.Lock()
	// 运行期间被标记取消的任务必须保留 cancelled 终态：任务没有强制中断能力，
	// 若让执行结果把状态改回 done/failed，取消操作会被静默吞掉，历史留档里
	// 已经写入的取消记录也会被覆盖。这里以用户意图为准，实际执行结果仍进审计日志。
	wasCancelled := task.Status == "cancelled"
	if wasCancelled {
		status = "cancelled"
	}
	task.Status = status
	switch {
	case taskErr != nil:
		task.Error = taskErr.Error()
		if task.Type == TaskCreate && !wasCancelled {
			task.Stage = "failed"
			task.StageDetail = "初始化失败"
		}
	case wasCancelled:
		// 取消优先：保留取消时的阶段与进度，不标记完成。
	default:
		task.Error = ""
		if task.Type == TaskCreate {
			task.Stage = "completed"
			task.StageDetail = "初始化完成"
		}
		// 任务成功结束才标记 100%；失败时保留中断处的进度，便于定位卡点。
		task.Percent = 100
	}
	if task.activeKey != "" {
		delete(q.activeTargets, task.activeKey)
		task.activeKey = ""
	}
	if q.activeTasks > 0 {
		q.activeTasks--
	}
	q.persistTasks()
	q.signalDispatchers()
	// 在锁内拷贝落库所需数据，锁外再写历史/日志，避免持锁做 IO 造成阻塞。
	entry := config.TaskHistoryEntry{
		ID:            task.ID,
		Type:          string(task.Type),
		ContainerID:   task.ContainerID,
		ContainerName: task.ContainerName,
		Status:        taskHistoryStatus(status),
		Error:         task.Error,
		Stage:         task.Stage,
		StageDetail:   task.StageDetail,
		Percent:       task.Percent,
		User:          task.User,
		IP:            task.IP,
		UserAgent:     task.UserAgent,
		CreatedAt:     task.CreatedAt,
		StartedAt:     task.StartedAt,
	}
	q.mu.Unlock()
	// 锁外落库：写入终态留档与结束日志。
	persistFinishedTask(entry, taskErr)
}

// taskHistoryStatus 把内存中的任务状态映射为历史表使用的终态字符串。
// 内存里成功状态是 "done"，历史表统一记作 "completed"。
func taskHistoryStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "completed", "done":
		return "completed"
	case "":
		return "completed"
	default:
		return status
	}
}

// persistFinishedTask 在释放队列锁之后落库：补全结束时间与耗时，写入历史并追加日志。
func persistFinishedTask(entry config.TaskHistoryEntry, taskErr error) {
	endedAt := time.Now().UTC()
	entry.EndedAt = endedAt.Format(time.RFC3339)
	if start, err := time.Parse(time.RFC3339, entry.StartedAt); err == nil {
		entry.DurationMs = endedAt.Sub(start).Milliseconds()
	}
	_ = config.UpsertTaskHistory(entry)
	level := "INFO"
	message := "任务完成"
	if taskErr != nil {
		level = "ERROR"
		message = taskErr.Error()
	}
	_ = config.AppendTaskLog(entry.ID, level, message)
}

// createStageOrderLXC / createStageOrderKVM 列出各自运行时创建流程的阶段顺序。
// 任务百分比按阶段在序列中的位置推导，因此进度条随流程单调推进；
// 两个运行时的阶段集合与先后顺序不同，必须分开定义，不能用单一映射表。
var (
	createStageOrderLXC = []string{
		"preparing", "rootfs", "storage", "disk", "resources", "data_disk",
		"addresses", "metadata", "network", "ssh", "permissions", "credentials", "cloud_init",
	}
	createStageOrderKVM = []string{
		"preparing", "storage", "addresses", "disk", "cloud_init", "define", "nat", "metadata",
	}
)

// taskStagePercent 把阶段换算成 0-100 的完成度。100 保留给任务真正完成，
// 因此中间阶段最高到 99，避免任务未结束就显示 100%。
func taskStagePercent(virtualization, stage string) (int, bool) {
	stage = strings.TrimSpace(stage)
	switch stage {
	case "queued":
		return 0, true
	case "starting":
		return 99, true
	case "completed":
		return 100, true
	}
	order := createStageOrderLXC
	if strings.EqualFold(strings.TrimSpace(virtualization), config.VirtualizationKVM) {
		order = createStageOrderKVM
	}
	for i, s := range order {
		if s == stage {
			return (i + 1) * 99 / len(order), true
		}
	}
	return 0, false
}

func (q *TaskQueue) updateTaskStage(task *Task, stage, detail string) {
	q.mu.Lock()
	task.Stage = stage
	task.StageDetail = detail
	if percent, ok := taskStagePercent(task.Config.Virtualization, stage); ok && percent > task.Percent {
		task.Percent = percent
	}
	taskID := task.ID
	q.mu.Unlock()
	// 锁外落库：追加阶段日志，避免持锁做 IO 造成阻塞。
	message := "阶段: " + stage
	if strings.TrimSpace(detail) != "" {
		message += " " + detail
	}
	_ = config.AppendTaskLog(taskID, "INFO", message)
}

// runCreateTask handles lxc-create, resource setup, start, and SSH init. A
// restored task resumes initialization when the same-name container exists.
func (q *TaskQueue) runCreateTask(task *Task) {
	q.mu.Lock()
	createdByTask := false
	if task.Config.Name == "" {
		task.Config.Name = task.ContainerName
	}
	task.Config.NormalizeResourceAliases()
	cfg := task.Config
	q.mu.Unlock()
	cfg.Progress = func(stage, detail string) {
		q.updateTaskStage(task, stage, detail)
	}
	if cfg.Name == "" {
		err := fmt.Errorf("container name is required")
		config.AddAuditLog(string(task.Type), task.ContainerName, "failed: "+err.Error(), "admin")
		q.finishTask(task, "failed", err)
		return
	}
	c := config.FindContainerByName(cfg.Name)
	if c == nil {
		if err := createByRuntime(cfg); err != nil {
			config.AddAuditLog(string(task.Type), cfg.Name, "失败: "+err.Error(), "admin")
			q.finishTask(task, "failed", err)
			return
		}
		createdByTask = true
		c = config.FindContainerByName(cfg.Name)
		if c == nil {
			err := fmt.Errorf("created but not found in config")
			config.AddAuditLog(string(task.Type), task.Config.Name, "失败: "+err.Error(), "admin")
			q.finishTask(task, "failed", err)
			return
		}
	} else {
		lxc.ReleaseQueuedCreateNATPorts(cfg.Name)
	}

	q.mu.Lock()
	task.ContainerID = c.ID
	task.ContainerName = c.Name
	q.mu.Unlock()
	startDetail := "启动容器并等待网络就绪"
	if strings.EqualFold(cfg.Virtualization, config.VirtualizationKVM) {
		startDetail = "启动虚拟机并等待网络就绪"
	}
	q.updateTaskStage(task, "starting", startDetail)
	if err := startByRuntime(c.ID); err != nil {
		if createdByTask {
			_ = destroyByRuntime(c.ID)
		}
		config.AddAuditLog(string(task.Type), task.ContainerName, "初始化失败: "+err.Error(), "admin")
		q.finishTask(task, "failed", err)
		return
	}
	config.AddAuditLog(string(task.Type), task.ContainerName, "成功", "admin")
	q.finishTask(task, "done", nil)
}

func (q *TaskQueue) runOperationTask(task *Task) {
	q.mu.Lock()
	err := resolveTaskContainer(task)
	q.mu.Unlock()
	skipped := false
	if err == nil && (task.Type == TaskStart || task.Type == TaskRestart || task.Type == TaskReinstall) {
		c := config.FindContainer(task.ContainerID)
		if c != nil {
			if c.Suspended {
				err = fmt.Errorf("容器已挂起（欠费停机），不允许此操作")
			} else if lxc.IsExpired(*c) {
				err = fmt.Errorf("容器已到期，不允许此操作")
			} else if lxc.IsTrafficExceeded(*c) {
				err = fmt.Errorf("容器流量已超限，不允许此操作")
			}
		}
	}
	if err == nil && isSecurityStopTask(task) && !config.AppConfig.SecurityAutoShutdown {
		skipped = true
	}
	if err == nil && !skipped {
		switch task.Type {
		case TaskStart:
			err = startByRuntime(task.ContainerID)
		case TaskStop:
			err = stopByRuntime(task.ContainerID)
		case TaskRestart:
			err = restartByRuntime(task.ContainerID)
		case TaskDelete:
			err = destroyByRuntime(task.ContainerID)
			if err == nil {
				time.Sleep(time.Second)
				if config.FindContainer(task.ContainerID) != nil {
					err = fmt.Errorf("container still exists after delete: %d", task.ContainerID)
				}
			}
		case TaskReinstall:
			if lxc.HasSSHAuthOptions(task.Config) {
				err = reinstallByRuntime(task.ContainerID, task.TemplateID, task.Config)
			} else {
				err = reinstallByRuntime(task.ContainerID, task.TemplateID)
			}
		}
	}

	auditUser := task.User
	if auditUser == "" {
		auditUser = "admin"
	}
	if err != nil {
		config.AddAuditLogFull(string(task.Type), task.ContainerName, "失败: "+err.Error(), auditUser, task.IP, task.UserAgent, false, err.Error())
		q.finishTask(task, "failed", err)
		return
	}
	if skipped {
		config.AddAuditLogFull(string(task.Type), task.ContainerName, "跳过: 安全告警自动关机已关闭", auditUser, task.IP, task.UserAgent, true, "")
		q.finishTask(task, "done", nil)
		return
	}

	config.AddAuditLogFull(string(task.Type), task.ContainerName, "成功", auditUser, task.IP, task.UserAgent, true, "")
	switch task.Type {
	case TaskStart:
		config.UpdateContainerStatus(task.ContainerID, "running")
		clearPolicyBlockAfterAdminRecovery(task)
	case TaskStop:
		config.UpdateContainerStatus(task.ContainerID, "stopped")
	case TaskRestart:
		config.UpdateContainerStatus(task.ContainerID, "running")
		clearPolicyBlockAfterAdminRecovery(task)
	case TaskReinstall:
		clearPolicyBlockAfterAdminRecovery(task)
	}
	q.finishTask(task, "done", nil)
}

func isSecurityStopTask(task *Task) bool {
	return task != nil && task.Type == TaskStop && task.User == "system:security"
}

func clearPolicyBlockAfterAdminRecovery(task *Task) {
	if task == nil || strings.HasPrefix(task.User, "user:") || task.User == "system:security" {
		return
	}
	c := config.FindContainer(task.ContainerID)
	if c != nil && c.PolicyBlocked {
		config.SetContainerPolicyBlock(c.ID, false, "")
		config.AddAuditLog("security_policy_unblock", c.Name, "管理员操作后解除策略临时封禁", task.User)
	}
}

func resolveTaskContainer(task *Task) error {
	if task.Type == TaskCreate {
		return nil
	}
	if task.ContainerID > 0 {
		if c := config.FindContainer(task.ContainerID); c != nil {
			if task.ContainerName == "" {
				task.ContainerName = c.Name
			}
			return nil
		}
	}
	if task.ContainerName != "" {
		if c := config.FindContainerByName(task.ContainerName); c != nil {
			task.ContainerID = c.ID
			task.ContainerName = c.Name
			return nil
		}
		return fmt.Errorf("container not found: %s", task.ContainerName)
	}
	return fmt.Errorf("container not found: %d", task.ContainerID)
}

func (q *TaskQueue) persistTasks() {
	saved := make([]config.SavedTask, 0)
	for _, t := range q.tasks {
		// Only persist pending and running tasks to avoid
		// re-queuing already completed/failed tasks after restart.
		if t.Status != "pending" && t.Status != "running" {
			continue
		}
		cfgJSON, _ := json.Marshal(t.Config)
		saved = append(saved, config.SavedTask{
			ID:            t.ID,
			Type:          string(t.Type),
			ContainerID:   t.ContainerID,
			ContainerName: t.ContainerName,
			Status:        t.Status,
			Error:         t.Error,
			CreatedAt:     t.CreatedAt,
			TemplateID:    t.TemplateID,
			Config:        string(cfgJSON),
			User:          t.User,
			IP:            t.IP,
			UserAgent:     t.UserAgent,
		})
	}
	config.SaveTasks(saved)
}

func (q *TaskQueue) GetTasks() []*Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]*Task, 0, len(q.tasks))
	// Collect all task IDs, sort by creation time (extracted from ID number)
	for _, t := range q.tasks {
		copyTask := *t
		result = append(result, &copyTask)
	}
	// Stable sort by ID number (task-N where N is sequential)
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if parseIDNum(result[i].ID) > parseIDNum(result[j].ID) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

// HandleSingleTaskAction creates a task for a single container action
func HandleSingleTaskAction(w http.ResponseWriter, r *http.Request, id int, action string) {
	c := config.FindContainer(id)
	name := ""
	if c != nil {
		name = c.Name
	}

	// Determine user from authenticated request context.
	user := requestActor(r)
	ip := clientIP(r)
	userAgent := r.Header.Get("User-Agent")

	var taskType TaskType
	var templateID string
	var taskConfig *lxc.ContainerConfig
	switch action {
	case "start":
		taskType = TaskStart
	case "stop":
		taskType = TaskStop
	case "restart":
		taskType = TaskRestart
	case "delete":
		taskType = TaskDelete
	case "reinstall":
		var req struct {
			TemplateID    string `json:"template_id"`
			SSHAuthMode   string `json:"ssh_auth_mode,omitempty"`
			SSHPassword   string `json:"ssh_password,omitempty"`
			SSHPublicKey  string `json:"ssh_public_key,omitempty"`
			ReinstallMode string `json:"reinstall_mode,omitempty"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		reinstallMode := lxc.NormalizeReinstallMode(req.ReinstallMode)
		if strings.TrimSpace(req.ReinstallMode) != "" && reinstallMode == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid reinstall_mode, expected \"system\" or \"full\""})
			return
		}
		templateID = req.TemplateID
		if templateID == "" {
			c := config.FindContainer(id)
			if c != nil {
				templateID = c.Template
			}
		}
		runtime := runtimeFromTemplateID(templateID)
		if c := config.FindContainer(id); c != nil {
			runtime = c.Runtime()
		}
		if !isTemplateAllowedForRequest(r, c, templateID) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Template is not allowed for this user"})
			return
		}
		if !isTemplateAvailableForRequest(r, c, templateID, runtime) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Template is not enabled or downloaded"})
			return
		}
		authCfg := lxc.ContainerConfig{
			TemplateID:    templateID,
			SSHAuthMode:   req.SSHAuthMode,
			SSHPassword:   req.SSHPassword,
			SSHPublicKey:  req.SSHPublicKey,
			ReinstallMode: reinstallMode,
		}
		if lxc.HasSSHAuthOptions(authCfg) {
			if err := validateReinstallSSHAuth(c, templateID, authCfg); err != nil {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
				return
			}
		}
		// 只要有登录方式或重装范围任一项，就把配置传给执行层。
		if lxc.HasSSHAuthOptions(authCfg) || reinstallMode != "" {
			taskConfig = &authCfg
		}
		taskType = TaskReinstall
	default:
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Unknown action"})
		return
	}

	ids := globalQueue.EnqueueWithAudit(id, name, taskType, templateID, taskConfig, user, ip, userAgent)
	jsonResponse(w, http.StatusAccepted, APIResponse{
		Success: true,
		Message: "Task queued",
		Data:    map[string]interface{}{"task_id": ids[0], "container_name": name, "status": "pending"},
	})
}

// HandleBatchCreate handles batch container creation
func HandleBatchCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "container:create") {
		return
	}
	if isAccessRestrictedRequest(r) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Container-bound API keys cannot create containers"})
		return
	}
	var req struct {
		Containers []lxc.ContainerConfig `json:"containers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	if len(req.Containers) == 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "No containers requested"})
		return
	}

	activeCreateNames := globalQueue.ActiveCreateNames()
	requestNames := make(map[string]bool)
	requestNATPorts := make(map[string]string)
	var batchDiskSum float64
	var batchDataDiskSum float64
	var batchRAMSum int
	for i := range req.Containers {
		name := strings.TrimSpace(req.Containers[i].Name)
		req.Containers[i].Name = name
		if !config.IsValidContainerNameSyntax(name) {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid container name: " + name})
			return
		}
		if requestNames[name] {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Duplicate container name in request: " + name})
			return
		}
		if config.FindContainerByName(name) != nil {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Container name already exists: " + name})
			return
		}
		if activeCreateNames[name] {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Container creation already queued: " + name})
			return
		}
		if req.Containers[i].VCPU <= 0 {
			req.Containers[i].VCPU = 1
		}
		if err := rejectNegativeCreateLimits(req.Containers[i]); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": " + err.Error()})
			return
		}
		req.Containers[i].NormalizeResourceAliases()
		req.Containers[i].Virtualization = runtimeFromRequest(req.Containers[i].Virtualization)
		if req.Containers[i].WantsLANIPv4() && req.Containers[i].Virtualization != config.VirtualizationLXC {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": LAN IPv4 is only supported for LXC containers"})
			return
		}
		if req.Containers[i].RAMMB < 128 {
			req.Containers[i].RAMMB = 512
		}
		if req.Containers[i].DiskGB <= 0 {
			req.Containers[i].DiskGB = 5
		}
		if err := validateCreateStoragePool(&req.Containers[i]); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": " + err.Error()})
			return
		}
		if !isImageEnabledAndDownloaded(req.Containers[i].TemplateID, req.Containers[i].Virtualization) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: name + ": template is not enabled or downloaded"})
			return
		}
		if ids, err := normalizeAllowedImageIDs(req.Containers[i].AllowedImageIDs); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": " + err.Error()})
			return
		} else {
			req.Containers[i].AllowedImageIDs = ids
		}
		if req.Containers[i].PortMappingCount < 0 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": port mapping count cannot be negative"})
			return
		}
		if err := req.Containers[i].NormalizeCreateNATMappings(); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": " + err.Error()})
			return
		}
		if err := lxc.ValidateCreateNATPortAvailability(req.Containers[i]); err != nil {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: name + ": " + err.Error()})
			return
		}
		if req.Containers[i].ManagementPort > 0 {
			key := fmt.Sprintf("%d/tcp", req.Containers[i].ManagementPort)
			if owner := requestNATPorts[key]; owner != "" {
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: fmt.Sprintf("%s: NAT management port %s is also requested by %s", name, key, owner)})
				return
			}
			requestNATPorts[key] = name
		}
		for _, mapping := range req.Containers[i].NATPortMappings {
			key := fmt.Sprintf("%d/%s", mapping.HostPort, mapping.Protocol)
			if owner := requestNATPorts[key]; owner != "" {
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: fmt.Sprintf("%s: NAT host port %s is also requested by %s", name, key, owner)})
				return
			}
			requestNATPorts[key] = name
		}
		if req.Containers[i].PortMappingCount > 64 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": port mapping count cannot exceed 64"})
			return
		}
		if req.Containers[i].IPv4Count < 0 || req.Containers[i].IPv6Count < 0 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": IP address count cannot be negative"})
			return
		}
		if req.Containers[i].IPv4Count > 64 || req.Containers[i].IPv6Count > 64 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": IP address count cannot exceed 64"})
			return
		}
		if !req.Containers[i].AssignIPv4 && len(req.Containers[i].PublicIPv4s) == 0 {
			req.Containers[i].IPv4Count = 0
		}
		if !req.Containers[i].AssignIPv6 && len(req.Containers[i].IPv6Addresses) == 0 {
			req.Containers[i].IPv6Count = 0
		}
		if !hasRequestedNetwork(req.Containers[i]) {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": " + noNetworkSelectedMessage})
			return
		}
		if req.Containers[i].SnapshotLimit <= 0 {
			req.Containers[i].SnapshotLimit = config.DefaultSnapshotLimit
		}
		if err := validateRuntimeResourceRequest(req.Containers[i].Virtualization, req.Containers[i].TemplateID, req.Containers[i].VCPU, req.Containers[i].RAMMB, req.Containers[i].DiskGB); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": " + err.Error()})
			return
		}
		if err := validateCreateSSHAuth(req.Containers[i]); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: name + ": " + err.Error()})
			return
		}
		requestNames[name] = true
		batchDiskSum += req.Containers[i].DiskGB
		batchDataDiskSum += req.Containers[i].DataDiskGB
		batchRAMSum += req.Containers[i].RAMMB
	}
	if err := validateCumulativeDiskQuota(batchDiskSum, batchDataDiskSum); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "batch: " + err.Error()})
		return
	}
	// F8：内存累计配额检查（批量创建按整批内存总和）
	if err := validateCumulativeRAMQuota(batchRAMSum); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "batch: " + err.Error()})
		return
	}
	planned, err := lxc.ReserveBatchCreateNATPorts(req.Containers)
	if err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: err.Error()})
		return
	}
	ids := globalQueue.EnqueueBatchCreateWithAudit(planned, requestActor(r), clientIP(r), r.UserAgent())
	jsonResponse(w, http.StatusAccepted, APIResponse{Success: true, Data: ids})
}

// HandleBatchAction handles batch container actions
func HandleBatchAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !hasAnyScope(r, "container:power", "container:delete", "container:reinstall") {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Insufficient API key scope"})
		return
	}
	if !requireSubUserWrite(w, r) {
		return
	}
	var req struct {
		Action       string `json:"action"`
		Containers   []int  `json:"containers"`
		TemplateID   string `json:"template_id,omitempty"`
		SSHAuthMode  string `json:"ssh_auth_mode,omitempty"`
		SSHPassword  string `json:"ssh_password,omitempty"`
		SSHPublicKey string `json:"ssh_public_key,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	var taskType TaskType
	var requiredScope string
	var taskConfig *lxc.ContainerConfig
	switch req.Action {
	case "start":
		taskType = TaskStart
		requiredScope = "container:power"
	case "stop":
		taskType = TaskStop
		requiredScope = "container:power"
	case "restart":
		taskType = TaskRestart
		requiredScope = "container:power"
	case "delete":
		taskType = TaskDelete
		requiredScope = "container:delete"
	case "reinstall":
		if req.TemplateID == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "template_id required"})
			return
		}
		if !isTemplateEnabledAndDownloaded(req.TemplateID) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Template is not enabled or downloaded"})
			return
		}
		authCfg := lxc.ContainerConfig{
			TemplateID:   req.TemplateID,
			SSHAuthMode:  req.SSHAuthMode,
			SSHPassword:  req.SSHPassword,
			SSHPublicKey: req.SSHPublicKey,
		}
		if lxc.HasSSHAuthOptions(authCfg) {
			taskConfig = &authCfg
		}
		taskType = TaskReinstall
		requiredScope = "container:reinstall"
	default:
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Unknown action"})
		return
	}
	if !requireScope(w, r, requiredScope) {
		return
	}
	for _, id := range req.Containers {
		c := config.FindContainer(id)
		if c == nil || !isContainerAllowedForRequest(r, c.UUID) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to one or more containers"})
			return
		}
		if taskType == TaskReinstall && !isTemplateAllowedForRequest(r, c, req.TemplateID) {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: c.Name + ": template is not allowed for this user"})
			return
		}
		if taskConfig != nil {
			if err := validateReinstallSSHAuth(c, req.TemplateID, *taskConfig); err != nil {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: c.Name + ": " + err.Error()})
				return
			}
		}
	}

	var outcomes []EnqueueOutcome
	if taskConfig != nil {
		for _, id := range req.Containers {
			c := config.FindContainer(id)
			name := ""
			if c != nil {
				name = c.Name
			}
			res, err := globalQueue.EnqueueWithAuditChecked(id, name, taskType, req.TemplateID, taskConfig, requestActor(r), clientIP(r), r.UserAgent())
			if err != nil {
				writeQueueFull(w, err, outcomes)
				return
			}
			outcomes = append(outcomes, res...)
		}
	} else {
		res, err := globalQueue.EnqueueBatchWithAuditChecked(taskType, req.Containers, req.TemplateID, requestActor(r), clientIP(r), r.UserAgent())
		outcomes = append(outcomes, res...)
		if err != nil {
			writeQueueFull(w, err, outcomes)
			return
		}
	}
	ids := outcomeIDs(outcomes)
	// 幂等去重要有可见反馈：重复提交被合并成一个任务时必须告知，否则用户会以为点了两次。
	message := ""
	if deduped := countDeduped(outcomes); deduped > 0 {
		message = fmt.Sprintf("已受理 %d 个任务（%d 个重复提交已合并为同一任务）", len(ids), deduped)
	}
	jsonResponse(w, http.StatusAccepted, APIResponse{Success: true, Message: message, Data: ids})
}

// writeQueueFull 回背压错误（429）：队列过载时快速失败并给出可读信息，同时带上
// 本次已受理的任务 ID，避免调用方在部分入队时丢失已排队的任务。
func writeQueueFull(w http.ResponseWriter, err error, accepted []EnqueueOutcome) {
	message := err.Error()
	if n := len(accepted); n > 0 {
		message = fmt.Sprintf("%s（已受理 %d 个，请稍后重试其余）", message, n)
	}
	jsonResponse(w, http.StatusTooManyRequests, APIResponse{
		Success: false,
		Code:    "QUEUE_FULL",
		Message: message,
		Data:    outcomeIDs(accepted),
	})
}

func countDeduped(outcomes []EnqueueOutcome) int {
	n := 0
	for _, o := range outcomes {
		if o.Deduped {
			n++
		}
	}
	return n
}

// HandleTaskDelete deletes a specific task by ID
func HandleTaskDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "task:delete") {
		return
	}
	// URL: /api/tasks/{id} or /api/v1/tasks/{id}
	taskID := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	taskID = strings.TrimPrefix(taskID, "/api/tasks/")
	if taskID == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Task ID required"})
		return
	}
	globalQueue.mu.Lock()
	task := globalQueue.tasks[taskID]
	if task != nil && !isTaskAllowedForRequest(r, task) {
		globalQueue.mu.Unlock()
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this task"})
		return
	}
	delete(globalQueue.tasks, taskID)
	// Also remove from both queues if pending
	newCreate := make([]*Task, 0, len(globalQueue.createQueue))
	for _, t := range globalQueue.createQueue {
		if t.ID != taskID {
			newCreate = append(newCreate, t)
		}
	}
	globalQueue.createQueue = newCreate
	newOp := make([]*Task, 0, len(globalQueue.opQueue))
	for _, t := range globalQueue.opQueue {
		if t.ID != taskID {
			newOp = append(newOp, t)
		}
	}
	globalQueue.opQueue = newOp
	globalQueue.persistTasks()
	globalQueue.mu.Unlock()
	if task != nil && task.Type == TaskCreate {
		lxc.ReleaseQueuedCreateNATPorts(task.Config.Name)
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Task deleted"})
}

// HandleTasks returns the current task queue
func HandleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "task:read") {
		return
	}
	tasks := globalQueue.GetTasks()
	tasks = filterTasksForRequest(r, tasks)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: tasks})
}

// RestoreTasks restores task queue from config
func RestoreTasks() {
	globalQueue.mu.Lock()
	for _, st := range config.AppConfig.Tasks {
		if st.Type == string(TaskStop) && st.User == "system:security" && !config.AppConfig.SecurityAutoShutdown {
			continue
		}
		var cfg lxc.ContainerConfig
		if st.Config != "" {
			json.Unmarshal([]byte(st.Config), &cfg)
		}
		containerName := st.ContainerName
		if containerName == "" {
			containerName = cfg.Name
		}
		if cfg.Name == "" {
			cfg.Name = containerName
		}
		cfg.NormalizeResourceAliases()
		containerID := st.ContainerID
		if containerID <= 0 && containerName != "" {
			if c := config.FindContainerByName(containerName); c != nil {
				containerID = c.ID
			}
		}
		globalQueue.tasks[st.ID] = &Task{
			ID:            st.ID,
			Type:          TaskType(st.Type),
			ContainerID:   containerID,
			ContainerName: containerName,
			Status:        st.Status,
			Error:         st.Error,
			Stage:         "queued",
			StageDetail:   "排队等待",
			CreatedAt:     st.CreatedAt,
			TemplateID:    st.TemplateID,
			Config:        cfg,
			User:          st.User,
			IP:            st.IP,
			UserAgent:     st.UserAgent,
		}
		if st.Status == "pending" || st.Status == "running" {
			// Reset running tasks back to pending so they get retried
			globalQueue.tasks[st.ID].Status = "pending"
			globalQueue.restoreEnqueue(globalQueue.tasks[st.ID])
		}
		if num := parseIDNum(st.ID); num >= globalQueue.nextID {
			globalQueue.nextID = num + 1
		}
	}
	// Clear persisted tasks from disk (they're now in memory)
	config.SaveTasks([]config.SavedTask{})
	globalQueue.mu.Unlock()
	// 启动时清理历史留档，只保留最近 1000 条；失败不影响启动流程。
	_, _ = config.PruneTaskHistory(1000)
}

func parseIDNum(id string) int {
	var num int
	for _, c := range id {
		if c >= '0' && c <= '9' {
			num = num*10 + int(c-'0')
		}
	}
	return num
}

func validateCreateStoragePool(cfg *lxc.ContainerConfig) error {
	required := config.StorageContentLXC
	if cfg.Virtualization == config.VirtualizationKVM {
		required = config.StorageContentKVM
	}
	requiredBytes := int64(math.Round(cfg.DiskGB*1024)) * 1024 * 1024
	pool, err := config.SelectStoragePoolForContent(required, cfg.StoragePoolID, requiredBytes)
	if err != nil {
		return err
	}
	cfg.StoragePoolID = pool.ID
	return nil
}

// Cancel 取消一个尚未开始执行（排队中）的任务。
//
// 返回 (canceled, running)：
//   - canceled=true 表示已成功取消（从队列摘除并标记 canceled）；
//   - running=true 表示任务已在执行，无法取消（调用方应返回 409）；
//   - 两者皆 false 表示任务不存在或已结束。
//
// 实现方式：标记 Status=canceled 并唤醒派发协程；takeNextTask 在出队时会跳过
// 已取消任务，因此不存在"取消后仍然执行"的竞态窗口。
func (q *TaskQueue) Cancel(taskID string) (canceled bool, running bool) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return false, false
	}
	q.mu.Lock()
	task, ok := q.tasks[taskID]
	if !ok {
		q.mu.Unlock()
		return false, false
	}
	switch task.Status {
	case "running", "processing":
		q.mu.Unlock()
		return false, true
	case "success", "failed", "canceled", "done", "completed":
		q.mu.Unlock()
		return false, false
	}
	task.Status = "canceled"
	task.Error = "已取消"
	q.persistTasks()
	// 唤醒派发协程，让被取消的任务尽快从队列中排空。
	if q.createCond != nil {
		q.createCond.Broadcast()
	}
	if q.opCond != nil {
		q.opCond.Broadcast()
	}
	q.mu.Unlock()

	_ = config.AppendTaskLog(taskID, "WARN", "任务已由管理员取消")
	config.MutateGlobalLogged(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Tasks {
			if cfg.Tasks[i].ID != taskID {
				continue
			}
			switch cfg.Tasks[i].Status {
			case "running", "success", "failed", "canceled", "done", "completed":
				// 已结束/执行中的历史记录不改写。
			default:
				cfg.Tasks[i].Status = "canceled"
				cfg.Tasks[i].Error = "已取消"
			}
			return
		}
	})
	return true, false
}
