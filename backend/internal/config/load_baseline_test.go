package config

// load_baseline_test.go —— #91：可复现的规模压测基线。
//
// 目的：把「这台机器、这个 N」下的关键写路径成本一次性量出来并打印成固定格式的报告，
// 供 scripts/load-baseline.sh 归档、跨版本对比。所有断言都用「写量」这种与机器无关
// 的量表达，耗时的「解释」（单核可支撑多少次/秒）随报告一并给出。
//
// 这是**基线**，不是回归测试：默认跳过（避免拖慢常规 suite），仅在设置
// EYVES_LOAD_BASELINE=1 时运行。规模由环境变量控制：
//   EYVES_LB_CONTAINERS （默认 20000）容器数
//   EYVES_LB_NODES      （默认 30000）节点数
//   EYVES_LB_SUBUSERS   （默认 20000）子用户数
//   EYVES_LB_ROUNDS     （默认 7）    每项取最小值的轮数
//
// 运行：
//   EYVES_LOAD_BASELINE=1 go test -run TestLoadBaseline -v -timeout 30m ./internal/config/

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const lbPrefix = "LOADBASELINE|"

func lbEnvInt(t *testing.T, key string, def int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		t.Fatalf("invalid %s=%q", key, raw)
	}
	return v
}

// lbMeasure 跑 rounds 次取最小非零耗时（Windows 时钟分辨率低也会自动跳过 0 值）。
func lbMeasure(rounds int, fn func()) time.Duration {
	var min time.Duration
	for i := 0; i < rounds; i++ {
		t0 := time.Now()
		fn()
		if d := time.Since(t0); d > 0 && (min == 0 || d < min) {
			min = d
		}
	}
	return min
}

// lbThroughput 由单次耗时换算单核吞吐（次/秒），用于把延迟翻译成容量口径。
func lbThroughput(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(time.Second) / float64(d)
}

func TestLoadBaseline(t *testing.T) {
	if os.Getenv("EYVES_LOAD_BASELINE") == "" {
		t.Skip("set EYVES_LOAD_BASELINE=1 to run the load baseline")
	}
	nCont := lbEnvInt(t, "EYVES_LB_CONTAINERS", 20000)
	nNodes := lbEnvInt(t, "EYVES_LB_NODES", 30000)
	nSub := lbEnvInt(t, "EYVES_LB_SUBUSERS", 20000)
	rounds := lbEnvInt(t, "EYVES_LB_ROUNDS", 7)

	resetConfigStoreForTest(t)
	dir := t.TempDir()
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(dir, "config.json"))
	t.Setenv("EYVESCLOUD_DATA_DIR", dir)
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}

	// ---- 构造规模 ----
	const m = 10 // 被测节点上报的容器数（心跳的 O(m) 口径）
	conts := make([]Container, 0, nCont)
	for i := 0; i < nCont; i++ {
		c := Container{
			ID: i + 1, UUID: fmt.Sprintf("uuid-%d", i+1), Name: fmt.Sprintf("ct-%d", i+1),
			Virtualization: VirtualizationLXC, Status: "running", VCPU: 1, RAMMB: 128, DiskGB: 1,
		}
		if i < m {
			c.NodeID = "node-0"
			c.NodeLocalID = i + 1
		}
		conts = append(conts, c)
	}
	nodes := make([]Node, 0, nNodes)
	for i := 0; i < nNodes; i++ {
		nodes = append(nodes, Node{
			ID: fmt.Sprintf("node-%d", i), Name: fmt.Sprintf("node-%d", i),
			Address: fmt.Sprintf("https://10.0.%d.%d:8999", (i/255)%255, i%255+1),
			Token:   fmt.Sprintf("node-token-%048d", i),
			Status:  "online", LastSeen: "2026-01-01 00:00:00",
			Version: "v2.2.50", CPUCount: 8, RAMTotalMB: 16384,
		})
	}
	subs := make([]SubUser, 0, nSub)
	for i := 0; i < nSub; i++ {
		subs = append(subs, SubUser{
			ID: fmt.Sprintf("su-%d", i), Username: fmt.Sprintf("user-%d", i),
			PassHash: "x", Role: "operator",
		})
	}

	AppConfigMu.Lock()
	AppConfig.Containers = conts
	AppConfig.Nodes = nodes
	AppConfig.SubUsers = subs
	AppConfigMu.Unlock()

	t0 := time.Now()
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
	initialWrite := time.Since(t0)

	report := func(format string, args ...any) {
		fmt.Printf(lbPrefix+format+"\n", args...)
	}

	report("规模 容器=%d 节点=%d 子用户=%d 每节点上报容器=%d 轮数=%d", nCont, nNodes, nSub, m, rounds)
	report("首次全量落库（构建库）=%s", initialWrite.Round(time.Millisecond))

	// 配置库占用（Postgres：当前测试 schema 下全部表的总大小）。
	var dbBytes int64
	if err := db.QueryRow(`SELECT COALESCE(SUM(pg_total_relation_size(c.oid)), 0)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relkind = 'r'`).Scan(&dbBytes); err == nil {
		report("配置库占用=%.1f MB", float64(dbBytes)/(1024*1024))
	}

	installWriteProbe(t, "containers", "nodes", "sub_users", "app_meta", "audit_logs", "login_logs")

	// 1) 全量扫描保存（未改造的 MutateGlobal 走的就是这条路，nil hint = 全量兜底）。
	base := probeRead(t)
	full := lbMeasure(rounds, func() {
		if err := saveConfigToDBHinted(nil); err != nil {
			t.Fatal(err)
		}
	})
	fullW := lbWriteDelta(base, probeRead(t))

	// 2) 精确保存：只声明 1 个容器（管理接口改单容器的理想口径）。
	base = probeRead(t)
	var exactOne time.Duration
	for i := 0; i < rounds; i++ {
		AppConfig.Containers[3].TrafficUsedRX++
		c := AppConfig.Containers[3]
		t1 := time.Now()
		if err := saveConfigToDBHinted(newExactDirtySet(DirtySet{Containers: []Container{c}})); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(t1); d > 0 && (exactOne == 0 || d < exactOne) {
			exactOne = d
		}
	}
	exactOneW := lbWriteDelta(base, probeRead(t))

	// 3) 心跳式保存：声明 1 个节点 + m 个容器（与 api/nodes.go 心跳处理器的声明一致）。
	base = probeRead(t)
	var heartbeat time.Duration
	for i := 0; i < rounds; i++ {
		AppConfig.Nodes[0].LastSeen = fmt.Sprintf("2026-01-01 00:01:%02d", i+1)
		nd := AppConfig.Nodes[0]
		cs := make([]Container, 0, m)
		for j := 0; j < m; j++ {
			AppConfig.Containers[j].TrafficUsedRX++
			cs = append(cs, AppConfig.Containers[j])
		}
		t1 := time.Now()
		if err := saveConfigToDBHinted(newExactDirtySet(DirtySet{Nodes: []Node{nd}, Containers: cs})); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(t1); d > 0 && (heartbeat == 0 || d < heartbeat) {
			heartbeat = d
		}
	}
	heartbeatW := lbWriteDelta(base, probeRead(t))

	// 4) 空声明下限：没有任何改动时的固定成本（事务 + app_meta + 无日志重写）。
	base = probeRead(t)
	floor := lbMeasure(rounds, func() {
		if err := saveConfigToDBHinted(newExactDirtySet(DirtySet{})); err != nil {
			t.Fatal(err)
		}
	})
	floorW := lbWriteDelta(base, probeRead(t))

	report("全量扫描保存=%s 单核≈%.0f 次/秒 写出=%d 行", full, lbThroughput(full), fullW)
	report("精确保存(1容器)=%s 单核≈%.0f 次/秒 写出=%d 行", exactOne, lbThroughput(exactOne), exactOneW)
	report("心跳式保存(1节点+%d容器)=%s 单核≈%.0f 次/秒 写出=%d 行", m, heartbeat, lbThroughput(heartbeat), heartbeatW)
	report("空声明固定下限=%s 单核≈%.0f 次/秒 写出=%d 行", floor, lbThroughput(floor), floorW)

	// 5) 批处理合并：M 次心跳 → 1 个事务（#101）。
	batchRounds := 500
	base = probeRead(t)
	for i := 0; i < batchRounds; i++ {
		AppConfig.Containers[0].TrafficUsedRX += int64(i + 1)
		enqueueDeferred(DirtyIDs{Containers: []int{AppConfig.Containers[0].ID}})
	}
	t1 := time.Now()
	if err := FlushDeferredSaves(); err != nil {
		t.Fatal(err)
	}
	batchFlush := time.Since(t1)
	batchW := lbWriteDelta(base, probeRead(t))
	report("批处理合并 %d 次心跳 → 刷新 1 次=%s 写出=%d 行（逐跳落库需 %d 事务）",
		batchRounds, batchFlush.Round(time.Microsecond), batchW, batchRounds)

	// 6) 写入放大：全量扫描保存不得随 N 线性增长（这是 #96 要消灭的剩余项）。
	if fullW > 0 {
		report("注意：全量扫描保存写出 %d 行（未改造的 MutateGlobal 路径；#96 目标为 O(改动)）", fullW)
	}

	// 7) 目录式保存：只改 1 个子用户（管理接口 + 每次 API 请求都写的 api_key.last_used
	//    的落库口径）。对比第 1 项的全量兜底，量化「只改目录也要重算全部容器指纹」的浪费。
	if len(AppConfig.SubUsers) > 0 {
		base = probeRead(t)
		var catalogOnly time.Duration
		for i := 0; i < rounds; i++ {
			AppConfig.SubUsers[0].TokenVersion++
			t1 := time.Now()
			if err := saveConfigToDBHinted(newExactCatalogOnlyHint()); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(t1); d > 0 && (catalogOnly == 0 || d < catalogOnly) {
				catalogOnly = d
			}
		}
		catalogW := lbWriteDelta(base, probeRead(t))
		report("目录式保存(1子用户)=%s 单核≈%.0f 次/秒 写出=%d 行", catalogOnly, lbThroughput(catalogOnly), catalogW)

		// 8) 目录精确声明：只声明改动的那一个子用户（#111）。与第 7 项的区别是目录本身
		//    也不再全量扫描，故成本应为 O(声明行) 而非 O(全部目录行)——与目录规模无关。
		base = probeRead(t)
		var catalogExact time.Duration
		for i := 0; i < rounds; i++ {
			AppConfig.SubUsers[0].TokenVersion++
			set := DirtySet{SubUsers: []SubUser{AppConfig.SubUsers[0]}}
			t1 := time.Now()
			if err := saveConfigToDBHinted(newExactCatalogSet(set)); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(t1); d > 0 && (catalogExact == 0 || d < catalogExact) {
				catalogExact = d
			}
		}
		catalogExactW := lbWriteDelta(base, probeRead(t))
		report("目录精确声明(1子用户)=%s 单核≈%.0f 次/秒 写出=%d 行（#111，应与目录规模无关）",
			catalogExact, lbThroughput(catalogExact), catalogExactW)
	}
}

// lbWriteDelta 汇总某次测量相对基线的所有行写（含 UPDATE）。
func lbWriteDelta(base, after map[string]int) int {
	total := 0
	for k, v := range after {
		if d := v - base[k]; d > 0 {
			total += d
		}
	}
	return total
}
