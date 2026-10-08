// hbgen 心跳入站压测器（stdlib only，仅测试机临时使用，不入仓）。
//
// 以「nodes / interval」的速率向面板打 POST /api/nodes/{id}/heartbeat，为每个节点带
// 其确定 token。请求体与 loadseed 的夹具逐字段对齐（-conts 0 = 纯遥测，不含容器数组），
// 稳态下不触发结构性变更。
//
// 幂等/限速：dispatcher 按 tick 投递节点序号（轮转），jobs 通道满时阻塞 —— 于是达成
// 速率 = min(目标速率, workers/时延)，面板变慢会直接反映在达成 rps 上。
package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func rssKB(pid int) int64 {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				v, _ := strconv.ParseInt(f[1], 10, 64)
				return v
			}
		}
	}
	return -1
}

// cpuTicks 读 /proc/<pid>/stat 的 utime+stime（时钟滴答，HZ=100）。
func cpuTicks(pid int) int64 {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	s := string(raw)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return -1
	}
	f := strings.Fields(s[i+2:])
	if len(f) < 15 {
		return -1
	}
	u, _ := strconv.ParseInt(f[11], 10, 64)
	sy, _ := strconv.ParseInt(f[12], 10, 64)
	return u + sy
}

func heartbeatBody(nodeIdx, m int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `{"version":"2.2.50","cpu_count":8,"ram_total_mb":16384,"ram_used_mb":%d,`+
		`"disk_total_gb":200,"disk_used_gb":%.1f,"container_count":%d`,
		(nodeIdx*7)%16384, float64((nodeIdx*3)%200)+0.5, m)
	if m > 0 {
		sb.WriteString(`,"containers":[`)
		for j := 0; j < m; j++ {
			if j > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `{"id":%d,"uuid":"u-%06d-%03d","name":"ct-%06d-%03d",`+
				`"status":"running","virtualization":"lxc","template":"ubuntu-22.04",`+
				`"vcpu":1,"ram_mb":128,"disk_gb":1}`, j+1, nodeIdx, j, nodeIdx, j)
		}
		sb.WriteString(`]`)
	}
	sb.WriteString(`}`)
	return sb.String()
}

type nodeReq struct{ url, token, body string }

func main() {
	base := flag.String("base", "http://127.0.0.1:18996", "")
	nodes := flag.Int("nodes", 30000, "节点总数（决定速率与 ID 范围）")
	conts := flag.Int("conts", 10, "每节点上报容器数（0=纯遥测）")
	interval := flag.Duration("interval", 10*time.Second, "节点心跳间隔")
	workers := flag.Int("workers", 128, "并发 worker 数")
	duration := flag.Duration("duration", 60*time.Second, "计时时长")
	warmup := flag.Bool("warmup", true, "先不计时给每个节点打一发（播种 nodeReported）")
	rssPid := flag.Int("rss-pid", 0, "结束后采样该 PID 的 VmRSS(kB)")
	flag.Parse()

	targetRate := float64(*nodes) / interval.Seconds()
	tick := time.Duration(float64(time.Second) * interval.Seconds() / float64(*nodes))

	reqs := make([]nodeReq, *nodes)
	for i := range reqs {
		id := fmt.Sprintf("node-%06d", i)
		reqs[i] = nodeReq{
			url:   *base + "/api/nodes/" + id + "/heartbeat",
			token: "tok-" + id,
			body:  heartbeatBody(i, *conts),
		}
	}

	tr := &http.Transport{
		MaxIdleConns:        *workers * 2,
		MaxIdleConnsPerHost: *workers * 2,
		DisableCompression:  true,
	}
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second}

	fire := func(nr nodeReq) (int, float64) {
		req, _ := http.NewRequest("POST", nr.url, strings.NewReader(nr.body))
		req.Header.Set("Authorization", "Bearer "+nr.token)
		req.Header.Set("Content-Type", "application/json")
		t0 := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return -1, 0
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode, time.Since(t0).Seconds() * 1000
	}

	if *warmup {
		var wg sync.WaitGroup
		idx := make(chan int, *workers)
		for w := 0; w < *workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range idx {
					fire(reqs[i])
				}
			}()
		}
		for i := range reqs {
			idx <- i
		}
		close(idx)
		wg.Wait()
		fmt.Printf("WARMUP nodes=%d conts=%d done\n", *nodes, *conts)
	}

	var ok, fail atomic.Int64
	var mu sync.Mutex
	codeCount := map[int]int64{}
	lat := make([]float64, 0, int(targetRate*duration.Seconds())+64)

	jobs := make(chan int, *workers*2)
	var cpu0 int64
	if *rssPid > 0 {
		cpu0 = cpuTicks(*rssPid)
	}
	start := time.Now()
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		deadline := start.Add(*duration)
		i := 0
		for {
			<-t.C
			if time.Now().After(deadline) {
				break
			}
			jobs <- i % *nodes
			i++
		}
		close(jobs)
	}()

	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				code, d := fire(reqs[i])
				mu.Lock()
				codeCount[code]++
				if code == 200 {
					ok.Add(1)
					lat = append(lat, d)
				} else {
					fail.Add(1)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()

	sort.Float64s(lat)
	sum := 0.0
	for _, v := range lat {
		sum += v
	}
	pct := func(p float64) float64 {
		if len(lat) == 0 {
			return 0
		}
		i := int(math.Ceil(p/100*float64(len(lat)))) - 1
		if i < 0 {
			i = 0
		}
		return lat[i]
	}
	avg := 0.0
	if len(lat) > 0 {
		avg = sum / float64(len(lat))
	}
	fmt.Printf("RESULT nodes=%d conts=%d workers=%d target=%.0f/s ok=%d fail=%d "+
		"p50=%.1fms p95=%.1fms p99=%.1fms avg=%.1fms rps=%.1f elapsed=%.1fs\n",
		*nodes, *conts, *workers, targetRate, ok.Load(), fail.Load(),
		pct(50), pct(95), pct(99), avg, float64(ok.Load())/elapsed, elapsed)
	codes := make([]int, 0, len(codeCount))
	for c := range codeCount {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	parts := make([]string, 0, len(codes))
	for _, c := range codes {
		parts = append(parts, fmt.Sprintf("%d:%d", c, codeCount[c]))
	}
	fmt.Printf("CODES %s\n", strings.Join(parts, " "))
	if *rssPid > 0 {
		fmt.Printf("RSS pid=%d VmRSS=%dkB\n", *rssPid, rssKB(*rssPid))
		if cpu0 > 0 {
			if cpu1 := cpuTicks(*rssPid); cpu1 > 0 {
				reqs := ok.Load() + fail.Load()
				coreUs := 0.0
				if reqs > 0 {
					coreUs = float64(cpu1-cpu0) * 10000.0 / float64(reqs)
				}
				fmt.Printf("PANEL-CPU cores=%.3f us_per_req=%.0f\n",
					float64(cpu1-cpu0)/100.0/elapsed, coreUs)
			}
		}
	}
}
