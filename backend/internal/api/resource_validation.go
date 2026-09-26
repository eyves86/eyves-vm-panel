package api

import (
	"fmt"
	"math"

	"eyvescloud/internal/config"
)

const minVCPU = 0.25

// validateCumulativeDiskQuota 校验「已有容器的磁盘配额总和 + 本次新增请求」不超出宿主
// 物理磁盘总容量，防止连续/批量开通时累计配额超售导致磁盘爆满、运行中容器受损。
// 容器使用稀疏镜像/配额，单台实际占用小于请求值，但配额总和仍不应超过宿主物理容量。
// 企业超售场景可通过 DiskOvercommitRatio（磁盘超售比）放宽该上限（默认 1.0 不超售）。
func validateCumulativeDiskQuota(reqDiskGB, reqDataDiskGB float64) error {
	host := getHostInfo()
	if host.Disk.TotalGB <= 0 {
		return nil
	}
	allowable := host.Disk.TotalGB * config.GetDiskOvercommitRatio()
	sum := reqDiskGB + reqDataDiskGB
	for i := range config.AppConfig.Containers {
		c := &config.AppConfig.Containers[i]
		sum += c.DiskGB + c.DataDiskGB
	}
	if sum > allowable {
		return fmt.Errorf(
			"磁盘累计配额超限: 现有容器 + 本次请求共需 %.0f GB，宿主磁盘总量 %.0f GB × 超售比 %.2f = %.0f GB，请扩容存储、增大磁盘超售比或降低磁盘大小",
			sum, host.Disk.TotalGB, config.GetDiskOvercommitRatio(), allowable,
		)
	}
	return nil
}

func validateContainerResourceRequest(vcpu float64, ramMB int, diskGB float64) error {
	host := getHostInfo()

	if vcpu <= 0 {
		return fmt.Errorf("vCPU must be greater than 0")
	}
	if vcpu < minVCPU {
		return fmt.Errorf("vCPU must be at least %.2f", minVCPU)
	}
	if math.Abs(vcpu*4-math.Round(vcpu*4)) > 0.000001 {
		return fmt.Errorf("vCPU must use 0.25 increments")
	}
	if host.CPU.Cores > 0 && vcpu > float64(host.CPU.Cores) {
		return fmt.Errorf("vCPU cannot exceed host CPU cores (%d)", host.CPU.Cores)
	}
	if host.RAM.TotalMB > 0 {
		memCeiling := int(host.RAM.TotalMB)
		if enabled, ratio := config.GetMemoryOvercommit(); enabled && ratio > 0 {
			// 内存超售：可分配上限 = 物理内存 × 超售比。KSM 会压缩实际占用，
			// 但仍有系统性风险，因此仅当管理员显式开启时才放宽硬限。
			memCeiling = int(float64(host.RAM.TotalMB) * ratio)
		}
		if ramMB > memCeiling {
			return fmt.Errorf("memory cannot exceed host memory ceiling (%d MB)", memCeiling)
		}
	}
	if diskGB <= 0 {
		return fmt.Errorf("disk must be greater than 0")
	}
	if host.Disk.TotalGB > 0 {
		maxDiskGB := host.Disk.TotalGB
		if maxDiskGB < 1 {
			maxDiskGB = 1
		}
		if diskGB > maxDiskGB {
			return fmt.Errorf("disk cannot exceed host disk (%.0f GB)", maxDiskGB)
		}
	}
	return nil
}
