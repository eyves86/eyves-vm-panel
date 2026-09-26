package hapolicy

import (
	"testing"
	"time"
)

func TestPolicyValidate(t *testing.T) {
	good := PolicyBundle{InstanceID: 1, Priority: PriorityCritical, Strategy: HAActiveStandby, FailoverTimeout: time.Minute}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.Priority = "weird"
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid priority must error")
	}
	bad = good
	bad.Strategy = "weird"
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid strategy must error")
	}
	bad = good
	bad.FailoverTimeout = -time.Second
	if err := bad.Validate(); err == nil {
		t.Fatal("negative timeout must error")
	}
	bad = good
	bad.InstanceID = 0
	if err := bad.Validate(); err == nil {
		t.Fatal("zero instance_id must error")
	}
}

func TestRegistryUpsertGetList(t *testing.T) {
	r := NewRegistry()
	if err := r.Upsert(PolicyBundle{InstanceID: 1, Priority: PriorityHigh, Strategy: HAActiveStandby}); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get(1)
	if !ok || got.Priority != PriorityHigh {
		t.Fatal("Get must return inserted policy")
	}
	if len(r.List()) != 1 {
		t.Fatalf("list = %d, want 1", len(r.List()))
	}
	r.Delete(1)
	if _, ok := r.Get(1); ok {
		t.Fatal("Delete must remove policy")
	}
}

func TestEvaluateCriticalKeepsOnMaintenance(t *testing.T) {
	p := PolicyBundle{InstanceID: 1, Priority: PriorityCritical, Strategy: HAActiveStandby}
	dec := p.EvaluateForNode(NodeState{NodeID: "a", MaintenanceMode: true})
	if !dec.KeepOnNode {
		t.Fatal("critical must keep on maintenance")
	}
	if dec.Action != "keep" {
		t.Fatalf("action = %q, want keep", dec.Action)
	}
}

func TestEvaluateActiveActiveKeepsOnDrain(t *testing.T) {
	p := PolicyBundle{InstanceID: 1, Priority: PriorityNormal, Strategy: HAActiveActive}
	dec := p.EvaluateForNode(NodeState{NodeID: "a", Draining: true})
	if !dec.KeepOnNode {
		t.Fatal("active_active must keep on drain")
	}
	if dec.Reason == "" {
		t.Fatal("reason must be set")
	}
}

func TestEvaluateLowStopsOnMaintenance(t *testing.T) {
	p := PolicyBundle{InstanceID: 1, Priority: PriorityLow, Strategy: HAActiveStandby}
	dec := p.EvaluateForNode(NodeState{NodeID: "a", MaintenanceMode: true})
	if dec.KeepOnNode {
		t.Fatal("low priority must not keep on maintenance")
	}
	if dec.Action != "stop" {
		t.Fatalf("action = %q, want stop", dec.Action)
	}
}

func TestEvaluateNormalMigratesOnMaintenance(t *testing.T) {
	p := PolicyBundle{InstanceID: 1, Priority: PriorityNormal, Strategy: HAActiveStandby}
	dec := p.EvaluateForNode(NodeState{NodeID: "a", MaintenanceMode: true})
	if dec.KeepOnNode {
		t.Fatal("normal + active_standby must migrate on maintenance")
	}
	if dec.Action != "live_migrate" {
		t.Fatalf("action = %q, want live_migrate", dec.Action)
	}
}

func TestEvaluateKeepOnNormalNode(t *testing.T) {
	p := PolicyBundle{InstanceID: 1, Priority: PriorityNormal, Strategy: HANone}
	dec := p.EvaluateForNode(NodeState{NodeID: "a"})
	if !dec.KeepOnNode || dec.Action != "keep" {
		t.Fatalf("normal node must keep: %+v", dec)
	}
}

func TestEvaluateForAllReturnsDecisions(t *testing.T) {
	r := NewRegistry()
	r.Upsert(PolicyBundle{InstanceID: 1, Priority: PriorityHigh, Strategy: HAActiveStandby})
	r.Upsert(PolicyBundle{InstanceID: 2, Priority: PriorityCritical, Strategy: HAActiveStandby})
	decs := r.EvaluateForAll(map[string]NodeState{})
	if len(decs) != 2 {
		t.Fatalf("decisions = %d, want 2", len(decs))
	}
}

func TestIsValidPriorityAndStrategy(t *testing.T) {
	if !IsValidPriority(PriorityCritical) {
		t.Fatal("critical must be valid")
	}
	if IsValidPriority("weird") {
		t.Fatal("weird must be invalid")
	}
	if !IsValidHAStrategy(HAActiveActive) {
		t.Fatal("active_active must be valid")
	}
	if IsValidHAStrategy("weird") {
		t.Fatal("weird must be invalid")
	}
}