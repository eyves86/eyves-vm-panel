package api

import (
	"testing"

	"eyvescloud/internal/config"
)

func TestNormalizePolicyRule(t *testing.T) {
	valid := config.PolicyRule{
		Name:        "cpu overload",
		Metric:      config.PolicyMetricCPU,
		Operator:    "gt",
		Threshold:   90,
		Action:      config.PolicyActionRaiseCPU,
		AdjustVCPU:  1,
		TargetScope: "all",
	}
	if err := normalizePolicyRule(&valid); err != nil {
		t.Fatalf("expected valid rule to pass, got: %v", err)
	}
	if valid.TargetScope != "all" {
		t.Fatalf("expected default scope 'all', got %q", valid.TargetScope)
	}

	// notify(仅告警) 动作应无需额外参数即可通过。
	if err := normalizePolicyRule(&config.PolicyRule{Name: "alert only", Metric: "cpu", Operator: "gt", Threshold: 95, Action: config.PolicyActionNotify}); err != nil {
		t.Fatalf("notify action should be valid, got: %v", err)
	}

	cases := []struct {
		name string
		rule config.PolicyRule
	}{
		{"missing name", config.PolicyRule{Metric: "cpu", Operator: "gt", Threshold: 1, Action: "shutdown"}},
		{"bad metric", config.PolicyRule{Name: "x", Metric: "bogus", Operator: "gt", Threshold: 1, Action: "shutdown"}},
		{"bad operator", config.PolicyRule{Name: "x", Metric: "cpu", Operator: "eq", Threshold: 1, Action: "shutdown"}},
		{"zero threshold", config.PolicyRule{Name: "x", Metric: "cpu", Operator: "gt", Threshold: 0, Action: "shutdown"}},
		{"raise_cpu without adjust", config.PolicyRule{Name: "x", Metric: "cpu", Operator: "gt", Threshold: 1, Action: "raise_cpu"}},
		{"raise_ram without adjust", config.PolicyRule{Name: "x", Metric: "cpu", Operator: "gt", Threshold: 1, Action: "raise_ram"}},
		{"adjust_bw without adjust", config.PolicyRule{Name: "x", Metric: "cpu", Operator: "gt", Threshold: 1, Action: "adjust_bw"}},
		{"bad action", config.PolicyRule{Name: "x", Metric: "cpu", Operator: "gt", Threshold: 1, Action: "explode"}},
		{"bad scope", config.PolicyRule{Name: "x", Metric: "cpu", Operator: "gt", Threshold: 1, Action: "shutdown", TargetScope: "group:foo"}},
	}
	for _, tc := range cases {
		if err := normalizePolicyRule(&tc.rule); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestPolicyConditionHolds(t *testing.T) {
	if !policyConditionHolds("gt", 95, 90) {
		t.Error("95 > 90 should hold")
	}
	if policyConditionHolds("gt", 80, 90) {
		t.Error("80 > 90 should not hold")
	}
	if !policyConditionHolds("lt", 100, 1000) {
		t.Error("100 < 1000 should hold")
	}
	if policyConditionHolds("lt", 2000, 1000) {
		t.Error("2000 < 1000 should not hold")
	}
}

func TestPolicyMatchesScope(t *testing.T) {
	all := config.PolicyRule{TargetScope: "all"}
	if !policyMatchesScope(all, config.Container{Name: "c1"}) {
		t.Error("scope all should match any container")
	}
	tenant := config.PolicyRule{TargetScope: "tenant:team-a"}
	if !policyMatchesScope(tenant, config.Container{Name: "c1", Tenant: "team-a"}) {
		t.Error("tenant scope should match matching tenant")
	}
	if policyMatchesScope(tenant, config.Container{Name: "c1", Tenant: "team-b"}) {
		t.Error("tenant scope should not match other tenant")
	}
	container := config.PolicyRule{TargetScope: "container:web-01"}
	if !policyMatchesScope(container, config.Container{Name: "web-01"}) {
		t.Error("container scope should match by name")
	}
	if policyMatchesScope(container, config.Container{Name: "db-01"}) {
		t.Error("container scope should not match other container")
	}
}
