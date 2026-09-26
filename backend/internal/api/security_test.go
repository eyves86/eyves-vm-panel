package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"eyvescloud/internal/config"
)

func TestDetectReflectionAbuseIgnoresSingleDNSResolver(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 180; i++ {
		stats.add(connEntry{
			dstIP:   "1.1.1.1",
			dstPort: 53,
			proto:   "udp",
			state:   "UNREPLIED",
		})
	}

	ss := newSecurityScanner()
	ss.detectReflectionAbuse("ct-dns", "10.0.0.2", stats)

	if len(ss.alerts) != 0 {
		t.Fatalf("normal DNS queries to one resolver should not trigger reflection alert: %+v", ss.alerts)
	}
}

func TestDetectReflectionAbuseFlagsWideDNSFanout(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 120; i++ {
		stats.add(connEntry{
			dstIP:   fmt.Sprintf("203.0.113.%d", i),
			dstPort: 53,
			proto:   "udp",
			state:   "UNREPLIED",
		})
	}

	ss := newSecurityScanner()
	ss.detectReflectionAbuse("ct-dns", "10.0.0.2", stats)

	if len(ss.alerts) != 1 {
		t.Fatalf("expected one reflection alert, got %+v", ss.alerts)
	}
	if got := ss.alerts[0].Type; got != "reflection" {
		t.Fatalf("expected reflection alert, got %q", got)
	}
}

func TestDetectPortScansUsesHalfOpenConnections(t *testing.T) {
	resetSecurityTestConfig()

	established := newTrafficStats()
	for port := 8000; port < 8020; port++ {
		established.add(connEntry{
			dstIP:   "198.51.100.10",
			dstPort: port,
			proto:   "tcp",
			state:   "ESTABLISHED",
		})
	}

	ss := newSecurityScanner()
	ss.detectPortScans("ct-web", "10.0.0.3", established)
	if len(ss.alerts) != 0 {
		t.Fatalf("established multi-port connections should not trigger port scan alert: %+v", ss.alerts)
	}

	halfOpen := newTrafficStats()
	for port := 8000; port < 8012; port++ {
		halfOpen.add(connEntry{
			dstIP:   "198.51.100.10",
			dstPort: port,
			proto:   "tcp",
			state:   "SYN_SENT",
		})
	}

	ss.detectPortScans("ct-web", "10.0.0.3", halfOpen)
	if len(ss.alerts) != 1 {
		t.Fatalf("expected one port scan alert, got %+v", ss.alerts)
	}
	if got := ss.alerts[0].Type; got != "port_scan" {
		t.Fatalf("expected port_scan alert, got %q", got)
	}
}

func TestCancelPendingSecurityStops(t *testing.T) {
	resetSecurityTestConfig()

	q := &TaskQueue{
		tasks: map[string]*Task{},
	}
	securityTask := &Task{
		ID:          "task-1",
		Type:        TaskStop,
		ContainerID: 1,
		Status:      "pending",
		User:        "system:security",
	}
	userTask := &Task{
		ID:          "task-2",
		Type:        TaskStop,
		ContainerID: 2,
		Status:      "pending",
		User:        "admin",
	}
	runningSecurityTask := &Task{
		ID:          "task-3",
		Type:        TaskStop,
		ContainerID: 3,
		Status:      "running",
		User:        "system:security",
	}
	q.tasks[securityTask.ID] = securityTask
	q.tasks[userTask.ID] = userTask
	q.tasks[runningSecurityTask.ID] = runningSecurityTask
	q.opQueue = []*Task{securityTask, userTask, runningSecurityTask}

	if got := q.CancelPendingSecurityStops(); got != 1 {
		t.Fatalf("expected one pending security stop to be cancelled, got %d", got)
	}
	if _, ok := q.tasks[securityTask.ID]; ok {
		t.Fatal("pending security stop task was not removed")
	}
	if _, ok := q.tasks[userTask.ID]; !ok {
		t.Fatal("user stop task should not be removed")
	}
	if _, ok := q.tasks[runningSecurityTask.ID]; !ok {
		t.Fatal("running security stop task should be left for worker-side skip")
	}
	if len(q.opQueue) != 2 {
		t.Fatalf("expected op queue to keep two tasks, got %d", len(q.opQueue))
	}
}

func resetSecurityTestConfig() {
	config.AppConfig = &config.EyvescloudConfig{
		Containers: []config.Container{},
		AuditLogs:  []config.AuditLog{},
		Tasks:      []config.SavedTask{},
	}
}

func TestDetectCCFlagsConcentratedWebFlood(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 200; i++ {
		stats.add(connEntry{dstIP: "198.51.100.10", dstPort: 443, proto: "tcp", state: "ESTABLISHED"})
	}

	ss := newSecurityScanner()
	ss.detectCC("ct-cc", "10.0.0.3", stats)

	if len(ss.alerts) == 0 {
		t.Fatalf("concentrated web flood should trigger a cc alert")
	}
	if ss.alerts[0].Type != "cc" {
		t.Fatalf("expected cc alert, got %q", ss.alerts[0].Type)
	}
}

func TestDetectCCIgnoresLightWebTraffic(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 5; i++ {
		stats.add(connEntry{dstIP: "198.51.100.20", dstPort: 80, proto: "tcp", state: "ESTABLISHED"})
	}

	ss := newSecurityScanner()
	ss.detectCC("ct-light", "10.0.0.4", stats)

	if len(ss.alerts) != 0 {
		t.Fatalf("light web traffic should not trigger a cc alert: %+v", ss.alerts)
	}
}

func TestAntiSpoofIPRulesBindMac(t *testing.T) {
	rules := antiSpoofIPRules("203.0.113.5", "eth0", "AA:BB:CC:DD:EE:FF")
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	accept := strings.Join(rules[0], " ")
	if !strings.Contains(accept, "--mac-source") || !strings.Contains(accept, "aa:bb:cc:dd:ee:ff") {
		t.Fatalf("accept rule should bind the container MAC: %v", rules[0])
	}
	if !strings.Contains(accept, "203.0.113.5/32") {
		t.Fatalf("accept rule should pin the assigned IP: %v", rules[0])
	}

	drop := strings.Join(rules[1], " ")
	if !strings.Contains(drop, "-j DROP") {
		t.Fatalf("second rule should drop spoofed source: %v", rules[1])
	}

	// 规则必须落在本项目自有链内，便于整体回收、不影响其它链。
	if rules[0][0] != antiSpoofChain || rules[1][0] != antiSpoofChain {
		t.Fatalf("rules must live in the dedicated chain, got %q / %q", rules[0][0], rules[1][0])
	}
}

func TestResolveAbuseOwnershipMapsTenantAndSubUser(t *testing.T) {
	resetSecurityTestConfig()
	config.AppConfig.Containers = []config.Container{
		{ID: 1, Name: "ct-a", UUID: "uuid-a", Tenant: "team-x", Virtualization: config.VirtualizationKVM},
	}
	config.AppConfig.SubUsers = []config.SubUser{
		{ID: "su-1", Username: "alice", ContainerNames: []string{"ct-a"}},
	}

	tenant, owner, kind := resolveAbuseOwnership("ct-a")
	if owner != "alice" {
		t.Fatalf("owner = %q, want alice", owner)
	}
	if tenant != "team-x" {
		t.Fatalf("tenant = %q, want team-x", tenant)
	}
	if kind != config.VirtualizationKVM {
		t.Fatalf("kind = %q, want kvm", kind)
	}
}

func TestResolveAbuseOwnershipUnknownContainer(t *testing.T) {
	resetSecurityTestConfig()
	tenant, owner, kind := resolveAbuseOwnership("does-not-exist")
	if tenant != "" || owner != "" || kind != "" {
		t.Fatalf("unknown container should resolve to empty ownership, got %q/%q/%q", tenant, owner, kind)
	}
}

// TestAbuseDetailFormatsBehaviorMessage 校验统一告警文案格式。
func TestAbuseDetailFormatsBehaviorMessage(t *testing.T) {
	got := abuseDetail("ct-1", config.VirtualizationLXC, "mining", "连接矿池端口 Stratum/3333 共 6 条")
	want := "ct-1（LXC）可能存在挖矿行为：连接矿池端口 Stratum/3333 共 6 条"
	if got != want {
		t.Fatalf("unexpected detail:\n got: %s\nwant: %s", got, want)
	}

	got = abuseDetail("ct-2", config.VirtualizationKVM, compromiseAlertType, "存在后门/远控监听端口（31337）")
	want = "ct-2（KVM）疑似被入侵：存在后门/远控监听端口（31337）"
	if got != want {
		t.Fatalf("unexpected compromise detail:\n got: %s\nwant: %s", got, want)
	}
}

// TestClassifyConntrackTuplesHandlesNatInbound 校验入站（NAT 应答方向）识别。
func TestClassifyConntrackTuplesHandlesNatInbound(t *testing.T) {
	// 出站：原始方向 src 即容器地址
	outbound := "tcp 6 431999 ESTABLISHED src=10.0.0.5 dst=198.51.100.9 sport=40000 dport=3333 " +
		"packets=5 bytes=400 src=198.51.100.9 dst=10.0.0.5 sport=3333 dport=40000 [ASSURED] mark=0 use=1"
	dir, peer, ok := classifyConntrackTuples(parseConntrackTuples(outbound), "10.0.0.5")
	if !ok || dir != connOutbound || peer.ip != "198.51.100.9" || peer.port != 3333 {
		t.Fatalf("outbound classification failed: dir=%v peer=%+v ok=%v", dir, peer, ok)
	}

	// 入站（DNAT 端口映射）：容器地址只出现在应答方向的 src
	inbound := "tcp 6 431999 SYN_RECV src=203.0.113.9 dst=1.2.3.4 sport=51000 dport=22 " +
		"packets=2 bytes=120 src=10.0.0.5 dst=203.0.113.9 sport=22 dport=51000 mark=0 use=1"
	dir, peer, ok = classifyConntrackTuples(parseConntrackTuples(inbound), "10.0.0.5")
	if !ok || dir != connInbound || peer.ip != "203.0.113.9" || peer.localPort != 22 {
		t.Fatalf("inbound(DNAT) classification failed: dir=%v peer=%+v ok=%v", dir, peer, ok)
	}

	// 入站（无 NAT，macvlan）：原始方向 dst 即容器地址
	macvlan := "tcp 6 431999 SYN_RECV src=203.0.113.9 dst=10.0.0.5 sport=51000 dport=22 " +
		"packets=2 bytes=120 src=10.0.0.5 dst=203.0.113.9 sport=22 dport=51000 mark=0 use=1"
	dir, peer, ok = classifyConntrackTuples(parseConntrackTuples(macvlan), "10.0.0.5")
	if !ok || dir != connInbound || peer.ip != "203.0.113.9" || peer.localPort != 22 {
		t.Fatalf("inbound(macvlan) classification failed: dir=%v peer=%+v ok=%v", dir, peer, ok)
	}
}

func TestDetectInboundBruteForceFlagsManySources(t *testing.T) {
	resetSecurityTestConfig()

	in := newInboundStats()
	for i := 0; i < 20; i++ {
		in.add(fmt.Sprintf("198.51.100.%d", i+1), 22, "tcp", "SYN_RECV")
	}

	ss := newSecurityScanner()
	ss.detectInboundBruteForce("ct-ssh", "10.0.0.5", in)

	if len(ss.alerts) == 0 || ss.alerts[0].Type != "inbound_brute_force" {
		t.Fatalf("expected inbound_brute_force alert, got %+v", ss.alerts)
	}
}

func TestDetectInboundBruteForceIgnoresFewSources(t *testing.T) {
	resetSecurityTestConfig()

	in := newInboundStats()
	for i := 0; i < 3; i++ {
		in.add(fmt.Sprintf("198.51.100.%d", i+1), 22, "tcp", "ESTABLISHED")
	}

	ss := newSecurityScanner()
	ss.detectInboundBruteForce("ct-ssh", "10.0.0.5", in)

	if len(ss.alerts) != 0 {
		t.Fatalf("a few SSH clients should not trigger brute force alert: %+v", ss.alerts)
	}
}

func TestDetectBackdoorListenerFlagsRATPort(t *testing.T) {
	resetSecurityTestConfig()

	in := newInboundStats()
	in.add("198.51.100.77", 31337, "tcp", "ESTABLISHED")

	ss := newSecurityScanner()
	ss.detectBackdoorListener("ct-owned", "10.0.0.5", in)

	if len(ss.alerts) == 0 || ss.alerts[0].Type != "backdoor" {
		t.Fatalf("expected backdoor alert, got %+v", ss.alerts)
	}
}

func TestDetectInboundDDoSUsesHalfOpenConnections(t *testing.T) {
	resetSecurityTestConfig()

	// 大量已建立连接（正常高并发）不应触发 DDoS。
	established := newInboundStats()
	for i := 0; i < 300; i++ {
		established.add(fmt.Sprintf("198.51.100.%d", i%250+1), 443, "tcp", "ESTABLISHED")
	}
	ss := newSecurityScanner()
	ss.detectInboundDDoS("ct-web", "10.0.0.5", established)
	if len(ss.alerts) != 0 {
		t.Fatalf("established high-concurrency traffic should not trigger inbound ddos: %+v", ss.alerts)
	}

	// 大量半开连接（SYN_RECV）应触发。
	halfOpen := newInboundStats()
	for i := 0; i < 120; i++ {
		halfOpen.add(fmt.Sprintf("203.0.113.%d", i%250+1), 443, "tcp", "SYN_RECV")
	}
	ss.detectInboundDDoS("ct-web", "10.0.0.5", halfOpen)
	if len(ss.alerts) == 0 || ss.alerts[0].Type != "inbound_ddos" {
		t.Fatalf("expected inbound_ddos alert, got %+v", ss.alerts)
	}
}

func TestDetectLateralMovementFlagsPrivateTargets(t *testing.T) {
	resetSecurityTestConfig()

	out := newTrafficStats()
	for _, ip := range []string{"10.0.8.11", "10.0.8.12", "192.168.50.9"} {
		out.add(connEntry{dstIP: ip, dstPort: 445, proto: "tcp", state: "SYN_SENT"})
	}

	ss := newSecurityScanner()
	ss.detectLateralMovement("ct-pivot", "10.0.0.5", out)

	if len(ss.alerts) == 0 || ss.alerts[0].Type != "lateral_movement" {
		t.Fatalf("expected lateral_movement alert, got %+v", ss.alerts)
	}
}

func TestDetectLateralMovementIgnoresPublicTargets(t *testing.T) {
	resetSecurityTestConfig()

	out := newTrafficStats()
	for _, ip := range []string{"198.51.100.11", "198.51.100.12", "198.51.100.13"} {
		out.add(connEntry{dstIP: ip, dstPort: 445, proto: "tcp", state: "SYN_SENT"})
	}

	ss := newSecurityScanner()
	ss.detectLateralMovement("ct-ok", "10.0.0.5", out)

	if len(ss.alerts) != 0 {
		t.Fatalf("public targets should not be treated as lateral movement: %+v", ss.alerts)
	}
}

func TestDetectCompromiseEscalatesOnMultipleIndicators(t *testing.T) {
	resetSecurityTestConfig()

	out := newTrafficStats()
	out.add(connEntry{dstIP: "198.51.100.9", dstPort: 4444, proto: "tcp", state: "ESTABLISHED"})
	in := newInboundStats()
	in.add("203.0.113.5", 31337, "tcp", "ESTABLISHED")

	ss := newSecurityScanner()
	ss.detectCompromise("ct-owned", "10.0.0.5", out, in)

	found := false
	for _, alert := range ss.alerts {
		if alert.Type == compromiseAlertType {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected compromise alert when multiple indicators present, got %+v", ss.alerts)
	}
}

func TestDetectCompromiseIgnoresSingleIndicator(t *testing.T) {
	resetSecurityTestConfig()

	out := newTrafficStats()
	out.add(connEntry{dstIP: "198.51.100.9", dstPort: 4444, proto: "tcp", state: "ESTABLISHED"})
	in := newInboundStats()

	ss := newSecurityScanner()
	ss.detectCompromise("ct-c2", "10.0.0.5", out, in)

	if len(ss.alerts) != 0 {
		t.Fatalf("a single indicator should not raise the compromise summary: %+v", ss.alerts)
	}
}

func TestDetectSustainedCPUMiningFlagsSustainedLoad(t *testing.T) {
	resetSecurityTestConfig()
	c := config.Container{ID: 7, Name: "ct-miner", UUID: "uuid-miner", Status: "running"}

	containerMetricMu.Lock()
	containerMetricHistory[containerMetricKey(c)] = []ContainerMetricPoint{
		{CPU: 99}, {CPU: 98}, {CPU: 100}, {CPU: 97}, {CPU: 99}, {CPU: 96},
	}
	containerMetricMu.Unlock()
	t.Cleanup(func() {
		containerMetricMu.Lock()
		delete(containerMetricHistory, containerMetricKey(c))
		containerMetricMu.Unlock()
	})

	ss := newSecurityScanner()
	ss.detectSustainedCPUMining(c)

	if len(ss.alerts) == 0 || ss.alerts[0].Type != "mining" {
		t.Fatalf("expected mining alert for sustained CPU load, got %+v", ss.alerts)
	}
}

func TestDetectSustainedCPUMiningIgnoresBurstyLoad(t *testing.T) {
	resetSecurityTestConfig()
	c := config.Container{ID: 8, Name: "ct-bursty", UUID: "uuid-bursty", Status: "running"}

	containerMetricMu.Lock()
	containerMetricHistory[containerMetricKey(c)] = []ContainerMetricPoint{
		{CPU: 99}, {CPU: 20}, {CPU: 100}, {CPU: 10}, {CPU: 99}, {CPU: 30},
	}
	containerMetricMu.Unlock()
	t.Cleanup(func() {
		containerMetricMu.Lock()
		delete(containerMetricHistory, containerMetricKey(c))
		containerMetricMu.Unlock()
	})

	ss := newSecurityScanner()
	ss.detectSustainedCPUMining(c)

	if len(ss.alerts) != 0 {
		t.Fatalf("bursty CPU load should not be treated as mining: %+v", ss.alerts)
	}
}

func TestDetectP2PFlagsBittorrentPorts(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 8; i++ {
		stats.add(connEntry{dstIP: fmt.Sprintf("198.51.100.%d", i+1), dstPort: 6881, proto: "tcp", state: "ESTABLISHED"})
	}

	ss := newSecurityScanner()
	ss.detectP2P("ct-bt", "10.0.0.5", stats)

	if len(ss.alerts) == 0 || ss.alerts[0].Type != "p2p" {
		t.Fatalf("expected p2p alert for BitTorrent ports, got %+v", ss.alerts)
	}
}

func TestDetectP2PIgnoresNormalTraffic(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 5; i++ {
		stats.add(connEntry{dstIP: "198.51.100.30", dstPort: 443, proto: "tcp", state: "ESTABLISHED"})
	}

	ss := newSecurityScanner()
	ss.detectP2P("ct-ok", "10.0.0.6", stats)

	if len(ss.alerts) != 0 {
		t.Fatalf("normal HTTPS traffic should not trigger p2p alert: %+v", ss.alerts)
	}
}

func TestDetectSpamFlagsPort25(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 12; i++ {
		stats.add(connEntry{dstIP: fmt.Sprintf("203.0.113.%d", i+1), dstPort: 25, proto: "tcp", state: "ESTABLISHED"})
	}

	ss := newSecurityScanner()
	ss.detectSpam("ct-spam", "10.0.0.7", stats)

	found := false
	for _, alert := range ss.alerts {
		if alert.Type == "spam" && alert.TargetPort == 25 && alert.Severity == "critical" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected critical spam alert on port 25, got %+v", ss.alerts)
	}
}

func TestContainerMonitoredAddressesIncludesPublicIPs(t *testing.T) {
	c := config.Container{
		IP:          "10.0.3.5",
		IPv6:        "2001:db8::5",
		PublicIPv4s: []config.PublicIPv4Assignment{{Address: "203.0.113.20"}},
	}
	addresses := containerMonitoredAddresses(c)
	want := map[string]bool{"10.0.3.5": true, "203.0.113.20": true, "2001:db8::5": true}
	if len(addresses) != len(want) {
		t.Fatalf("expected %d addresses, got %v", len(want), addresses)
	}
	for _, address := range addresses {
		if !want[address] {
			t.Fatalf("unexpected address %q in %v", address, addresses)
		}
	}
}

func TestContainerMonitoredAddressesDeduplicates(t *testing.T) {
	c := config.Container{
		IP:          "10.0.3.5",
		PublicIPv4s: []config.PublicIPv4Assignment{{Address: "10.0.3.5"}},
	}
	addresses := containerMonitoredAddresses(c)
	if len(addresses) != 1 {
		t.Fatalf("duplicate addresses should collapse, got %v", addresses)
	}
}

func TestDesiredAntiSpoofRulesSkipsInvalidAddress(t *testing.T) {
	resetSecurityTestConfig()
	config.AppConfig.Containers = []config.Container{
		{
			ID: 1, Name: "ct-valid", Status: "running", MACAddress: "aa:bb:cc:dd:ee:01",
			PublicIPv4s: []config.PublicIPv4Assignment{{Address: "203.0.113.9", Interface: "eth0"}},
		},
		{
			ID: 2, Name: "ct-bad", Status: "running", MACAddress: "aa:bb:cc:dd:ee:02",
			PublicIPv4s: []config.PublicIPv4Assignment{{Address: "0.0.0.0/0", Interface: "eth0"}},
		},
	}

	targets, _ := desiredAntiSpoofRules()
	if len(targets) != 1 || targets[0].address != "203.0.113.9" {
		t.Fatalf("invalid address should be skipped, got %+v", targets)
	}
}

// TestDetectMiningFlagsStratumPort 校验挖矿检测：命中 Stratum 矿池端口即告警。
func TestDetectMiningFlagsStratumPort(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 6; i++ {
		stats.add(connEntry{dstIP: "198.51.100.77", dstPort: 3333, proto: "tcp", state: "ESTABLISHED"})
	}

	ss := newSecurityScanner()
	ss.detectMining("ct-miner", "10.0.0.8", stats)

	if len(ss.alerts) == 0 || ss.alerts[0].Type != "mining" {
		t.Fatalf("expected mining alert for Stratum port 3333, got %+v", ss.alerts)
	}
	if ss.alerts[0].Severity != "critical" {
		t.Fatalf("expected critical severity for repeated stratum connections, got %q", ss.alerts[0].Severity)
	}
}

// TestDetectMiningIgnoresNormalTraffic 确保普通流量不会误报挖矿。
func TestDetectMiningIgnoresNormalTraffic(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 20; i++ {
		stats.add(connEntry{dstIP: "198.51.100.90", dstPort: 443, proto: "tcp", state: "ESTABLISHED"})
	}

	ss := newSecurityScanner()
	ss.detectMining("ct-web", "10.0.0.9", stats)

	if len(ss.alerts) != 0 {
		t.Fatalf("normal HTTPS traffic should not trigger mining alert: %+v", ss.alerts)
	}
}

// TestDetectProxyAndTorFlagsVPNPort 校验 VPN/代理检测：OpenVPN 端口持续多目标连接即告警。
func TestDetectProxyAndTorFlagsVPNPort(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	for i := 0; i < 12; i++ {
		stats.add(connEntry{dstIP: fmt.Sprintf("203.0.113.%d", i+1), dstPort: 1194, proto: "udp", state: "ESTABLISHED"})
	}

	ss := newSecurityScanner()
	ss.detectProxyAndTor("ct-vpn", "10.0.0.10", stats)

	found := false
	for _, alert := range ss.alerts {
		if alert.Type == "proxy" && alert.TargetPort == 1194 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected proxy/VPN alert for OpenVPN port 1194, got %+v", ss.alerts)
	}
}

// TestDetectProxyAndTorIgnoresSingleWireGuardPeer 确保单条 WireGuard 连接不会误报。
func TestDetectProxyAndTorIgnoresSingleWireGuardPeer(t *testing.T) {
	resetSecurityTestConfig()

	stats := newTrafficStats()
	stats.add(connEntry{dstIP: "198.51.100.55", dstPort: 51820, proto: "udp", state: "ESTABLISHED"})

	ss := newSecurityScanner()
	ss.detectProxyAndTor("ct-wg", "10.0.0.11", stats)

	if len(ss.alerts) != 0 {
		t.Fatalf("a single WireGuard peer should not trigger a proxy alert: %+v", ss.alerts)
	}
}

// TestReconcileConfigDoesNotDeadlockWithSubUserMigrations 回归测试：
// ReconcileConfig 持有 AppConfigMu 写锁并执行迁移，若迁移中再取读锁会死锁。
func TestReconcileConfigDoesNotDeadlockWithSubUserMigrations(t *testing.T) {
	resetSecurityTestConfig()
	config.AppConfig.Containers = []config.Container{
		{ID: 1, Name: "ct-a", UUID: "uuid-a"},
	}
	config.AppConfig.SubUsers = []config.SubUser{
		{ID: "su-1", Username: "alice", ContainerNames: []string{"ct-a"}},
	}

	done := make(chan struct{})
	go func() {
		// db 未初始化时 saveConfigToDB 会返回错误，不影响本次锁路径验证。
		_ = config.ReconcileConfig()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ReconcileConfig deadlocked: recursive AppConfigMu lock in migrations")
	}

	config.AppConfigMu.RLock()
	uuids := append([]string(nil), config.AppConfig.SubUsers[0].ContainerUUIDs...)
	config.AppConfigMu.RUnlock()
	if len(uuids) != 1 || uuids[0] != "uuid-a" {
		t.Fatalf("migration should backfill container UUIDs, got %v", uuids)
	}
}
