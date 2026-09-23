package maintenance

import (
	"context"
	"errors"
	"testing"
)

func TestStartDrainEvacuateSuccess(t *testing.T) {
	lister := &stubLister{instances: []int{1, 2, 3}}
	mover := &stubMover{}
	picker := &stubPicker{node: "node-b"}
	e := NewEngine(lister, mover, picker)
	plan, err := e.StartDrain(context.Background(), "node-a", StrategyEvacuate)
	if err != nil {
		t.Fatal(err)
	}
	if plan.State != DrainCompleted {
		t.Fatalf("state = %v, want completed", plan.State)
	}
	if len(plan.MovedTo) != 3 {
		t.Fatalf("moved_to = %d, want 3", len(plan.MovedTo))
	}
	for inst, target := range plan.MovedTo {
		if target != "node-b" {
			t.Fatalf("instance %d target = %q", inst, target)
		}
	}
}

func TestStartDrainGracefulStopClearsMovedTo(t *testing.T) {
	lister := &stubLister{instances: []int{5}}
	mover := &stubMover{}
	e := NewEngine(lister, mover, &stubPicker{node: "node-b"})
	plan, err := e.StartDrain(context.Background(), "node-a", StrategyGracefulStop)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MovedTo[5] != "" {
		t.Fatalf("graceful_stop must not record target, got %q", plan.MovedTo[5])
	}
}

func TestStartDrainMigrationFailureMarksFailed(t *testing.T) {
	lister := &stubLister{instances: []int{1, 2}}
	mover := &stubMover{migrateErr: errors.New("target disk full")}
	e := NewEngine(lister, mover, &stubPicker{node: "node-b"})
	plan, err := e.StartDrain(context.Background(), "node-a", StrategyEvacuate)
	if err == nil {
		t.Fatal("migration failure must error")
	}
	if plan.State != DrainFailed {
		t.Fatalf("state = %v, want failed", plan.State)
	}
}

func TestStartDrainRequiresEngines(t *testing.T) {
	if _, err := NewEngine(nil, &stubMover{}, &stubPicker{}).StartDrain(context.Background(), "a", StrategyEvacuate); err == nil {
		t.Fatal("nil lister must error")
	}
	if _, err := NewEngine(&stubLister{}, nil, &stubPicker{}).StartDrain(context.Background(), "a", StrategyEvacuate); err == nil {
		t.Fatal("nil mover must error")
	}
	if _, err := NewEngine(&stubLister{}, &stubMover{}, nil).StartDrain(context.Background(), "a", StrategyEvacuate); err == nil {
		t.Fatal("nil picker must error")
	}
	if _, err := NewEngine(&stubLister{}, &stubMover{}, &stubPicker{}).StartDrain(context.Background(), "", StrategyEvacuate); err == nil {
		t.Fatal("empty node must error")
	}
}

func TestStartDrainNoInstancesIsSuccess(t *testing.T) {
	lister := &stubLister{instances: nil}
	e := NewEngine(lister, &stubMover{}, &stubPicker{node: "b"})
	plan, err := e.StartDrain(context.Background(), "node-a", StrategyEvacuate)
	if err != nil {
		t.Fatal(err)
	}
	if plan.State != DrainCompleted {
		t.Fatalf("state = %v, want completed (empty drain)", plan.State)
	}
	if len(plan.Instances) != 0 || len(plan.MovedTo) != 0 {
		t.Fatal("empty drain must have empty lists")
	}
}

func TestDrainGet(t *testing.T) {
	e := NewEngine(&stubLister{instances: []int{1}}, &stubMover{}, &stubPicker{node: "b"})
	plan, _ := e.StartDrain(context.Background(), "a", StrategyEvacuate)
	got, ok := e.Get(plan.ID)
	if !ok {
		t.Fatal("Get must succeed")
	}
	if got.NodeID != "a" {
		t.Fatalf("node = %q, want a", got.NodeID)
	}
	if _, ok := e.Get("missing"); ok {
		t.Fatal("missing must fail")
	}
}