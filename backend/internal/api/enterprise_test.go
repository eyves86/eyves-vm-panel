package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// TestTenantUsageCountsPerTenant 守卫租户用量聚合语义（容器数/vCPU/内存/磁盘，按 tenant 精确过滤）。
func TestTenantUsageCountsPerTenant(t *testing.T) {
	containers := []config.Container{
		{ID: 1, Tenant: "t1", VCPU: 2, RAMMB: 1024, DiskGB: 20},
		{ID: 2, Tenant: "t1", VCPU: 1.5, RAMMB: 512, DiskGB: 10.5},
		{ID: 3, Tenant: "t2", VCPU: 4, RAMMB: 2048, DiskGB: 40},
		{ID: 4, Tenant: "", VCPU: 8, RAMMB: 4096, DiskGB: 80},
	}
	u := tenantUsage("t1", containers)
	if u.Containers != 2 || u.VCPU != 4 || u.RAMMB != 1536 || u.DiskGB != 31 {
		t.Fatalf("t1 用量 = %+v，应为 {2 4 1536 31}", u)
	}
	if u := tenantUsage("absent", containers); u.Containers != 0 {
		t.Fatalf("未知租户用量应为 0，got %+v", u)
	}
}

// TestHandleTenantsUsageViaROView 守卫租户列表端点在只读共享视界下仍返回正确用量。
func TestHandleTenantsUsageViaROView(t *testing.T) {
	prev := config.GetTestConfig()
	t.Cleanup(func() { config.RestoreTestConfig(prev) })
	config.SetTestConfig(&config.EyvescloudConfig{
		Tenants: []config.Tenant{{ID: "t1", Name: "T1", ContainerQuota: 5, Enabled: true}},
		Containers: []config.Container{
			{ID: 1, Tenant: "t1", VCPU: 2, RAMMB: 1024, DiskGB: 20},
			{ID: 2, Tenant: "t1", VCPU: 2, RAMMB: 1024, DiskGB: 20},
			{ID: 3, Tenant: "t2", VCPU: 2, RAMMB: 1024, DiskGB: 20},
		},
	})

	w := httptest.NewRecorder()
	HandleTenants(w, httptest.NewRequest(http.MethodGet, "/api/tenants", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    []struct {
			ID              string `json:"id"`
			UsageContainers int    `json:"usage_containers"`
			UsageRAMMB      int64  `json:"usage_ram_mb"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "t1" {
		t.Fatalf("租户列表 = %+v", resp.Data)
	}
	if resp.Data[0].UsageContainers != 2 || resp.Data[0].UsageRAMMB != 2048 {
		t.Fatalf("t1 用量 = %+v，应为 2/2048", resp.Data[0])
	}
}

// TestTenantPathsAvoidFullContainersCopy 结构性守卫：租户列表/删除/配额校验只读访问容器，
// 不得退回 config.GetContainers()（整份值拷贝；#142 实测 20w 容器 ~270MB/次）。
// 一律使用 config.ContainersROView()（零拷贝只读视界，用完显式释放）。
func TestTenantPathsAvoidFullContainersCopy(t *testing.T) {
	src, err := os.ReadFile("enterprise.go")
	if err != nil {
		t.Fatalf("read enterprise.go: %v", err)
	}
	if strings.Contains(string(src), "config.GetContainers()") {
		t.Fatal("enterprise.go 不得调用 config.GetContainers()（全量拷贝）；应改用 config.ContainersROView()")
	}
}
