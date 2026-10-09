package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"eyvescloud/internal/config"
)

// BenchmarkNodeHeartbeatAtScale 量化心跳处理在「主控容器总量 N、本节点容器数 m」下的
// 单请求成本，用于验证 #102 的目标：**O(m)，与 N 无关**。
//
// P2 之前，心跳处理里有两条 O(N) 扫描（按 UUID 找容器、找本节点缺失容器），
// 30w 容器 × 3000 次/秒是纯 CPU 墙。改用下标缓存 + 「上次上报集合差集」后应降为 O(m)。
//
// 判定方式：同一 m 下扫过不同的 N（见 benchN 列表），ns/op 不随 N 增长即达标。
// 运行：
//
//	go test -bench=BenchmarkNodeHeartbeatAtScale -benchtime=2000x -run=^$ ./internal/api/
func BenchmarkNodeHeartbeatAtScale(b *testing.B) {
	ns := []int{2000, 20000, 200000}
	if testing.Short() {
		ns = []int{2000, 20000}
	}

	for _, n := range ns {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			setupHeartbeatBench(b, n)
			body := heartbeatBody(10) // 本节点上报 10 个容器
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-bench/heartbeat", strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer bench-token")
				rec := httptest.NewRecorder()
				handleNodeHeartbeat(rec, req, "node-bench")
				if rec.Code != http.StatusOK {
					b.Fatalf("心跳返回 %d，want 200", rec.Code)
				}
			}
		})
	}
}

// setupHeartbeatBench 建立 N 个容器的库（其中 10 个归属被测节点），并注册被测节点。
func setupHeartbeatBench(b *testing.B, n int) {
	b.Helper()
	requirePGTest(b)
	dir := b.TempDir()
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	b.Setenv("EYVESCLOUD_DATA_DIR", dir)
	b.Cleanup(func() {
		config.CloseConfigDB()
		config.SetConfigPath("")
	})
	if _, err := config.InitConfig(); err != nil {
		b.Fatalf("init config: %v", err)
	}

	conts := make([]config.Container, 0, n)
	for i := 0; i < n; i++ {
		c := config.Container{
			ID: i + 1, UUID: fmt.Sprintf("uuid-%d", i+1), Name: fmt.Sprintf("ct-%d", i+1),
			Virtualization: "lxc", Status: "running", VCPU: 1, RAMMB: 128, DiskGB: 1,
		}
		// 前 10 个归属被测节点，其余为本机容器（放大 N 以验证扫描成本与 N 无关）。
		if i < 10 {
			c.NodeID = "node-bench"
			c.NodeLocalID = i + 1
		}
		conts = append(conts, c)
	}
	config.AppConfigMu.Lock()
	config.AppConfig = &config.EyvescloudConfig{
		AdminUser:       "root-admin",
		AdminPassHash:   "x",
		JWTSecret:       "s",
		NextContainerID: n + 1,
		Nodes:           []config.Node{{ID: "node-bench", Name: "bench", Token: "bench-token"}},
		Containers:      conts,
	}
	config.AppConfigMu.Unlock()
	if err := config.SaveConfig(); err != nil {
		b.Fatalf("save: %v", err)
	}
	// 让节点首次心跳按「已落库状态」播种上次上报集合，避免把首次当结构性变化。
	resetNodeReportedForTest()
}

// heartbeatBody 构造 m 个容器的上报 JSON（与生产 agent 的字段一致）。
func heartbeatBody(m int) string {
	var sb strings.Builder
	sb.WriteString(`{"version":"2.2.50","containers":[`)
	for i := 0; i < m; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"id":%d,"uuid":"uuid-%d","name":"ct-%d","status":"running",`+
			`"virtualization":"lxc","vcpu":1,"ram_mb":128,"disk_gb":1,`+
			`"traffic_used_rx":%d,"traffic_used_tx":%d}`,
			i+1, i+1, i+1, 1000+i, 2000+i)
	}
	sb.WriteString(`]}`)
	return sb.String()
}
