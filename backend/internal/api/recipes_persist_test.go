package api

// recipes_persist_test.go —— Recipe 创建回归：锁定「持锁 + SaveConfig 自锁死锁」。
//
// 背景（v2.2.56 线上 E2E 实测发现）：createRecipe 曾写成
//
//	config.AppConfigMu.Lock()
//	config.AppConfig.Recipes = append(...)
//	config.SaveConfig()          // ← 内部再次获取同一把 AppConfigMu
//	config.AppConfigMu.Unlock()
//
// Go 的 sync.Mutex 不可重入，该调用会在同一把锁上自锁：请求永久挂起，并且由于
// 锁始终不释放，**整个面板**的配置读写全部冻结（创建一条 Recipe 就能把面板打挂，
// 且接口不返回、前端只看到转圈，无任何错误提示）。
//
// 本用例保证：① 处理器正常返回 201（回归表现为挂起，由 go test -timeout 兜底定位）；
// ② Recipe 真的落库，且能在“重启”（关闭并重新初始化配置库）后读回。

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"eyvescloud/internal/config"
)

func TestCreateRecipeDoesNotDeadlockAndPersists(t *testing.T) {
	dir := t.TempDir()
	config.SetConfigPath(dir + "/config.json")
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		config.CloseConfigDB()
		config.SetConfigPath("")
	})
	if _, err := config.InitConfig(); err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}

	body := []byte(`{"name":"e2e-recipe","description":"regression","script":"echo hi","scope":"admin"}`)
	req := asAdminRequest(httptest.NewRequest(http.MethodPost, "/api/recipes", bytes.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	createRecipe(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("createRecipe = %d，期望 201（body=%s）", rec.Code, rec.Body.String())
	}

	config.CloseConfigDB()
	cfg, err := config.InitConfig()
	if err != nil {
		t.Fatalf("重新初始化配置库失败: %v", err)
	}
	if len(cfg.Recipes) != 1 || cfg.Recipes[0].Name != "e2e-recipe" {
		t.Fatalf("重启后应读回 1 条 Recipe，实际 %+v", cfg.Recipes)
	}
}
