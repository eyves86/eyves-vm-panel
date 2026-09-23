package api

import (
	"fmt"
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
