package lxc

import (
	"fmt"
	"testing"

	"eyvescloud/internal/config"
)

// newCapacityConfig 构造一个可用的 AppConfig，可指定每个虚拟化类型的现有容器数量。
// LXC 与 KVM 的 NAT 子网均默认 /24（DHCPMax=253）。
func newCapacityConfig(lxcCount, kvmCount int) {
	config.AppConfig = &config.EyvescloudConfig{
		NATPortStart:    20000,
		NATPortEnd:      30000,
		NextContainerID: 100,
	}
	containers := make([]config.Container, 0, lxcCount+kvmCount)
	id := 1
	for i := 0; i < lxcCount && id <= 200; i++ {
		containers = append(containers, config.Container{
			ID: id, Name: fmt.Sprintf("lxc-%d", i+1),
			Virtualization: config.VirtualizationLXC,
		})
		id++
	}
	for i := 0; i < kvmCount && id <= 200; i++ {
		containers = append(containers, config.Container{
			ID: id, Name: fmt.Sprintf("kvm-%d", i+1),
			Virtualization: config.VirtualizationKVM,
		})
		id++
	}
	config.AppConfig.Containers = containers
}

func TestValidateNATSubnetCapacity_WithinLimit(t *testing.T) {
	// 负载很小，LXC/KVM 混合创建均通过。
	newCapacityConfig(2, 1)
	planned := []ContainerConfig{
		{Name: "a", Virtualization: config.VirtualizationLXC, AssignNAT: boolPtr(true)},
		{Name: "b", Virtualization: config.VirtualizationKVM, AssignNAT: boolPtr(true)},
	}
	if err := ValidateNATSubnetCapacity(planned); err != nil {
		t.Fatalf("expected no error within capacity, got: %v", err)
	}
}

func TestValidateNATSubnetCapacity_LXCExhaustion(t *testing.T) {
	// 把 LXC 子网缩到 /28 -> 可用 host=14。预置 13 台 LXC，再加 2 台 NAT LXC 应失败。
	prev := config.AppConfig
	newCapacityConfig(13, 0)
	config.AppConfig.LXCNATSubnet = "10.4.0.0/28"
	t.Cleanup(func() { config.AppConfig = prev })

	if got := config.LXCNATNetwork().DHCPMax; got != 13 {
		t.Fatalf("expected DHCPMax=13 for /28, got %d", got)
	}
	planned := []ContainerConfig{
		{Name: "a", Virtualization: config.VirtualizationLXC, AssignNAT: boolPtr(true)},
		{Name: "b", Virtualization: config.VirtualizationLXC, AssignNAT: boolPtr(true)},
	}
	if err := ValidateNATSubnetCapacity(planned); err == nil {
		t.Fatalf("expected LXC subnet exhaustion error: 13 in use + 2 requested > 13")
	}
}

func TestValidateNATSubnetCapacity_KVMExhaustion(t *testing.T) {
	prev := config.AppConfig
	defer func() { config.AppConfig = prev }()
	newCapacityConfig(2, 13) // KVM /28 已满
	config.AppConfig.KVMNATSubnet = "192.168.200.0/28"

	if got := config.KVMNATNetwork().DHCPMax; got != 13 {
		t.Fatalf("expected KVM DHCPMax=13 for /28, got %d", got)
	}
	planned := []ContainerConfig{
		{Name: "a", Virtualization: config.VirtualizationKVM, AssignNAT: boolPtr(true)},
		{Name: "b", Virtualization: config.VirtualizationKVM, AssignNAT: boolPtr(true)},
	}
	if err := ValidateNATSubnetCapacity(planned); err == nil {
		t.Fatalf("expected KVM subnet exhaustion error: 13 in use + 2 requested > 13")
	}
}

func TestValidateNATSubnetCapacity_LANNotCounted(t *testing.T) {
	prev := config.AppConfig
	defer func() { config.AppConfig = prev }()
	// LAN 模式的 LXC 不占用 NAT 池，不应计入 LXC 子网上限。
	newCapacityConfig(2, 0)
	config.AppConfig.LXCNATSubnet = "10.4.0.0/28"
	planned := []ContainerConfig{
		{Name: "a", Virtualization: config.VirtualizationLXC, AssignNAT: boolPtr(true)},
		{Name: "b", Virtualization: config.VirtualizationLXC, AssignNAT: boolPtr(false)},
		{Name: "c", Virtualization: config.VirtualizationLXC},
	}
	if err := ValidateNATSubnetCapacity(planned); err != nil {
		t.Fatalf("LAN containers should not count toward NAT pool, got: %v", err)
	}
}

func TestValidateNATSubnetCapacity_OversubscriptionLiftsGuard(t *testing.T) {
	prev := config.AppConfig
	defer func() { config.AppConfig = prev }()
	// /28 子网 DHCPMax=13，已满 13 台，且无超售开关时必被拦截。
	newCapacityConfig(13, 0)
	config.AppConfig.LXCNATSubnet = "10.4.0.0/28"
	config.AppConfig.NATSubnetOversubscription = true

	planned := []ContainerConfig{
		{Name: "a", Virtualization: config.VirtualizationLXC, AssignNAT: boolPtr(true)},
		{Name: "b", Virtualization: config.VirtualizationLXC, AssignNAT: boolPtr(true)},
	}
	if err := ValidateNATSubnetCapacity(planned); err != nil {
		t.Fatalf("NAT subnet oversubscription should lift the capacity guard, got: %v", err)
	}
}

func TestSubnetExhaustionErrorType(t *testing.T) {
	prev := config.AppConfig
	defer func() { config.AppConfig = prev }()
	newCapacityConfig(13, 0)
	config.AppConfig.LXCNATSubnet = "10.4.0.0/28"
	config.AppConfig.NATSubnetOversubscription = false

	planned := []ContainerConfig{
		{Name: "a", Virtualization: config.VirtualizationLXC, AssignNAT: boolPtr(true)},
	}
	err := ValidateNATSubnetCapacity(planned)
	if err == nil {
		t.Fatalf("expected exhaustion error")
	}
	if _, ok := err.(*SubnetExhaustionError); !ok {
		t.Fatalf("expected *SubnetExhaustionError, got %T", err)
	}
}

func boolPtr(b bool) *bool { return &b }