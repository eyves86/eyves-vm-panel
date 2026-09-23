package api

import "testing"

func TestExtractCertbotVersion(t *testing.T) {
	tests := []struct {
		output string
		want   string
	}{
		{"certbot 5.4.0", "5.4.0"},
		{"certbot v5.10.1", "5.10.1"},
		{"certbot, version 4.9", "4.9"},
		{"installed", ""},
	}

	for _, tt := range tests {
		if got := extractCertbotVersion(tt.output); got != tt.want {
			t.Fatalf("extractCertbotVersion(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}

func TestCertbotVersionAtLeast54(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"5.4", true},
		{"5.4.0", true},
		{"5.10", true},
		{"6.0.0", true},
		{"5.3.9", false},
		{"4.99", false},
		{"5", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := certbotVersionAtLeast(tt.version, 5, 4); got != tt.want {
			t.Fatalf("certbotVersionAtLeast(%q, 5, 4) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func TestARMCPUModelName(t *testing.T) {
	if got := armCPUModelName("0x41", "0xd0c"); got != "ARM Neoverse N1" {
		t.Fatalf("armCPUModelName() = %q, want ARM Neoverse N1", got)
	}
	if got := armCPUModelName("41", "d0c"); got != "ARM Neoverse N1" {
		t.Fatalf("armCPUModelName() without hex prefix = %q, want ARM Neoverse N1", got)
	}
}

func TestMeaningfulCPUModel(t *testing.T) {
	if meaningfulCPUModel("0") {
		t.Fatal("numeric ARM processor index should not be treated as a CPU model")
	}
	if !meaningfulCPUModel("Neoverse-N1") {
		t.Fatal("expected Neoverse-N1 to be treated as a CPU model")
	}
}

func TestHostTrafficInterfaceFilter(t *testing.T) {
	accepted := []string{"eth0", "ens3", "enp0s6", "bond0", "wg0"}
	for _, name := range accepted {
		if !isHostTrafficInterface(name) {
			t.Fatalf("expected %s to be accepted as a host traffic interface", name)
		}
	}

	rejected := []string{"", "lo", "docker0", "br-3024b78640ee", "lxcbr0", "virbr0", "vethaaa9e44", "cni0"}
	for _, name := range rejected {
		if isHostTrafficInterface(name) {
			t.Fatalf("expected %s to be rejected as an internal/container interface", name)
		}
	}
}
