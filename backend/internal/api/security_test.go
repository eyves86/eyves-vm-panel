package api

import (
	"fmt"
	"strings"
	"testing"

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
		{ID: 1, Name: "ct-a", UUID: "uuid-a", Tenant: "team-x"},
	}
	config.AppConfig.SubUsers = []config.SubUser{
		{ID: "su-1", Username: "alice", ContainerNames: []string{"ct-a"}},
	}

	tenant, owner := resolveAbuseOwnership("ct-a")
	if owner != "alice" {
		t.Fatalf("owner = %q, want alice", owner)
	}
	if tenant != "team-x" {
		t.Fatalf("tenant = %q, want team-x", tenant)
	}
}

func TestResolveAbuseOwnershipUnknownContainer(t *testing.T) {
	resetSecurityTestConfig()
	tenant, owner := resolveAbuseOwnership("does-not-exist")
	if tenant != "" || owner != "" {
		t.Fatalf("unknown container should resolve to empty ownership, got %q/%q", tenant, owner)
	}
}
