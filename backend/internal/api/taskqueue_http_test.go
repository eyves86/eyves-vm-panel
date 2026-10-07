package api

// taskqueue_http_test.go —— 容器操作队列的 HTTP 契约测试（幂等去重反馈 + 背压 429）。
//
// 前面的 taskqueue_test.go 覆盖的是队列对象本身的语义；这里补上「经过 handler →
// jsonResponse」的端到端契约：调用方（前端 / 脚本）最终收到的状态码、错误码与 message
// 必须与实际行为一致，否则就会出现「点了没反应」「报错但没说清」的假 UI。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// setupBatchActionTestStore 初始化临时 SQLite 配置库 + 给定容器集合，使
// HandleBatchAction 的容器校验与入队落库都能真实执行（不依赖全局真实数据目录）。
func setupBatchActionTestStore(t *testing.T, containers ...config.Container) {
	t.Helper()
	dir := t.TempDir()
	previous := config.AppConfig
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		config.CloseConfigDB()
		config.AppConfigMu.Lock()
		config.AppConfig = previous
		config.AppConfigMu.Unlock()
		config.SetConfigPath("")
	})
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.Containers = containers
	}); err != nil {
		t.Fatalf("写入测试容器失败: %v", err)
	}
}

// isolateTaskQueue 把包级队列替换成「无分发器」的独立队列：任务入队后保持 pending，
// 让去重与背压断言可确定复现；同时避免污染真实队列状态。
func isolateTaskQueue(t *testing.T) {
	t.Helper()
	previous := globalQueue
	globalQueue = newTaskQueue(config.DefaultTaskConcurrency)
	t.Cleanup(func() { globalQueue = previous })
}

func newAdminBatchActionRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/batch-action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return withAuthContext(req, AuthContext{Type: authTypeAdmin, Actor: "admin"})
}

// TestHandleBatchActionBackpressureReturns429 锁定背压的 HTTP 契约：队列达积压上限时
// 批量操作返回 429 + code=QUEUE_FULL，并回带本次**已受理**的任务 ID（部分入队不能丢），
// 而不是静默丢弃或笼统报错。
func TestHandleBatchActionBackpressureReturns429(t *testing.T) {
	isolateTaskQueue(t)
	setupBatchActionTestStore(t,
		config.Container{ID: 1, UUID: "u1", Name: "c1"},
		config.Container{ID: 2, UUID: "u2", Name: "c2"},
		config.Container{ID: 3, UUID: "u3", Name: "c3"},
	)
	globalQueue.SetMaxPending(2)

	rr := httptest.NewRecorder()
	HandleBatchAction(rr, newAdminBatchActionRequest(t, `{"action":"restart","containers":[1,2,3]}`))

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("状态码 = %d，应为 429（body=%s）", rr.Code, rr.Body.String())
	}
	var resp struct {
		Success bool     `json:"success"`
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Data    []string `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（body=%s）", err, rr.Body.String())
	}
	if resp.Success {
		t.Fatalf("背压响应 success 应为 false，实际 %+v", resp)
	}
	if resp.Code != "QUEUE_FULL" {
		t.Fatalf("错误码 = %q，应为 QUEUE_FULL", resp.Code)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("已受理 ID = %v，应为 2 个（部分入队不能丢）", resp.Data)
	}
	if !strings.Contains(resp.Message, "已受理 2 个") {
		t.Fatalf("message 未告知已受理数量：%q", resp.Message)
	}
	if got := globalQueue.Settings().Pending; got != 2 {
		t.Fatalf("pending = %d，应为 2", got)
	}
	if got := globalQueue.Settings().Rejected; got != 1 {
		t.Fatalf("rejected_total = %d，应为 1（超限提交要计数）", got)
	}
}

// TestHandleBatchActionDedupIsVisibleToCaller 重复提交（同一容器 + 同一动作）被幂等
// 合并为一个任务时，必须在 message 里明确告知并返回同一任务 ID，否则用户会以为
// 「点了两次都没反应」（假 UI）。
func TestHandleBatchActionDedupIsVisibleToCaller(t *testing.T) {
	isolateTaskQueue(t)
	setupBatchActionTestStore(t, config.Container{ID: 1, UUID: "u1", Name: "c1"})

	body := `{"action":"restart","containers":[1]}`
	first := httptest.NewRecorder()
	HandleBatchAction(first, newAdminBatchActionRequest(t, body))
	if first.Code != http.StatusAccepted {
		t.Fatalf("首次提交状态码 = %d，应为 202（body=%s）", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	HandleBatchAction(second, newAdminBatchActionRequest(t, body))
	if second.Code != http.StatusAccepted {
		t.Fatalf("重复提交状态码 = %d，应为 202（body=%s）", second.Code, second.Body.String())
	}

	type batchResp struct {
		Message string   `json:"message"`
		Data    []string `json:"data"`
	}
	var r1, r2 batchResp
	_ = json.Unmarshal(first.Body.Bytes(), &r1)
	_ = json.Unmarshal(second.Body.Bytes(), &r2)

	if len(r1.Data) != 1 || len(r2.Data) != 1 || r2.Data[0] != r1.Data[0] {
		t.Fatalf("重复提交应命中同一任务：first=%v second=%v", r1.Data, r2.Data)
	}
	if !strings.Contains(r2.Message, "已合并为同一任务") {
		t.Fatalf("重复提交的 message 缺少合并提示：%q", r2.Message)
	}
	if got := globalQueue.Settings().Pending; got != 1 {
		t.Fatalf("pending = %d，重复提交不应产生第二个任务", got)
	}
}

// TestHandleTaskQueueSettingsPersistsBackpressureLimit 校验设置接口能持久化背压上限并
// 立即生效；越界值必须显式 400（不能静默夹取成上限，否则界面显示的与实际不符）。
func TestHandleTaskQueueSettingsPersistsBackpressureLimit(t *testing.T) {
	isolateTaskQueue(t)
	setupBatchActionTestStore(t)

	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/task-queue/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		HandleTaskQueueSettings(rr, req)
		return rr
	}

	rr := put(`{"concurrency":3,"max_pending":7}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，应为 200（body=%s）", rr.Code, rr.Body.String())
	}
	if config.AppConfig.TaskQueueMaxPending != 7 {
		t.Fatalf("落库的 max_pending = %d，应为 7", config.AppConfig.TaskQueueMaxPending)
	}
	if got := globalQueue.Settings().MaxPending; got != 7 {
		t.Fatalf("运行时 max_pending = %d，应为 7（设置需立即生效）", got)
	}

	bad := put(fmt.Sprintf(`{"concurrency":3,"max_pending":%d}`, config.MaxTaskQueueMaxPending+1))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("越界 max_pending 状态码 = %d，应为 400（body=%s）", bad.Code, bad.Body.String())
	}
	if got := globalQueue.Settings().MaxPending; got != 7 {
		t.Fatalf("越界请求不应改变运行值，实际 %d", got)
	}
	if config.AppConfig.TaskQueueMaxPending != 7 {
		t.Fatalf("越界请求不应改变落库值，实际 %d", config.AppConfig.TaskQueueMaxPending)
	}
}
