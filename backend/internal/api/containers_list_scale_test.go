package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"eyvescloud/internal/config"
)

func listScaleEnvInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// BenchmarkListContainersAtScale 量化管理端容器列表在目标规模下的单请求成本。
// 容器一律挂 NodeID（目标拓扑：容器在被控节点上，本地状态探测循环会跳过它们），
// 使基准只度量「快照拷贝 + 过滤/排序管线 + 物化」本身，不混入 lxc-info 子进程探测。
// 规模经 env 调节：EYVES_LIST_CONTAINERS_N / EYVES_LIST_SUBUSERS_N。
func BenchmarkListContainersAtScale(b *testing.B) {
	n := listScaleEnvInt("EYVES_LIST_CONTAINERS_N", 20000)
	m := listScaleEnvInt("EYVES_LIST_SUBUSERS_N", 10000)

	previous := config.AppConfig
	conts := make([]config.Container, n)
	for i := range conts {
		c := &conts[i]
		c.ID = i + 1
		c.UUID = fmt.Sprintf("uuid-%08d", i)
		c.Name = fmt.Sprintf("ct-%08d", i)
		c.Status = "running"
		c.Template = "ubuntu-22.04"
		c.NodeID = "node-bench"
		c.SSHPort = 20000 + i
		if i%3 == 0 {
			c.OwnerSubUserID = fmt.Sprintf("su-%08d", i%m)
		}
	}
	sus := make([]config.SubUser, m)
	for i := range sus {
		sus[i].ID = fmt.Sprintf("su-%08d", i)
		sus[i].Username = fmt.Sprintf("user%08d", i)
	}
	config.AppConfig = &config.EyvescloudConfig{Containers: conts, SubUsers: sus}
	b.Cleanup(func() { config.AppConfig = previous })

	req := httptest.NewRequest(http.MethodGet, "/api/containers?page=1&page_size=20", nil)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		listContainers(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("listContainers status = %d", w.Code)
		}
	}
}
