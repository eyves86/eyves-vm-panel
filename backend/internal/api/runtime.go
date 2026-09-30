package api

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"time"
	"context"
	"strings"

	"eyvescloud/internal/config"
	"eyvescloud/internal/kvm"
	"eyvescloud/internal/lxc"
)

var kvmManager = kvm.NewManager()

const noNetworkSelectedMessage = "请勾选任意一个可用网络"

func runtimeFromRequest(value string) string {
	return config.NormalizeVirtualization(value)
}

func hasRequestedNetwork(cfg lxc.ContainerConfig) bool {
	return cfg.WantsNAT() || cfg.WantsLANIPv4() || cfg.AssignIPv4 || len(cfg.PublicIPv4s) > 0 || cfg.AssignIPv6 || len(cfg.IPv6Addresses) > 0
}

func runtimeFromTemplateID(templateID string) string {
	if kvm.FindImage(templateID) != nil {
		return config.VirtualizationKVM
	}
	return config.VirtualizationLXC
}

func createByRuntime(cfg lxc.ContainerConfig) error {
	cfg.Virtualization = runtimeFromRequest(cfg.Virtualization)
	cfg.NormalizeResourceAliases()
	if cfg.Virtualization == config.VirtualizationKVM {
		return kvmManager.CreateContainer(cfg)
	}
	return lxcManager.CreateContainer(cfg)
}

// cloneByRuntime 克隆容器（底层：LXC 用 lxc copy，KVM 用 qemu-img convert + virsh define）。
// srcContainer: 源容器配置（运行时层需从中取 LxcName/VirshName）
// newName / newLxcName: 新容器名 + 新 LXC/KVM 内部名
// newID / newUUID / newVNCPort / newSSHPort / newMAC: 已分配的新标识
// mode: "full" = 完整拷贝；"linked" = 秒级 COW 克隆（LXC ZFS/LVM / KVM qcow2 backing file 支持）
func cloneByRuntime(srcContainer *config.Container, newName, newLxcName, newVMName string,
	newID int, newUUID, newVNCPort, newSSHPort, newMAC string,
	mode string, startAfter bool) error {

	if srcContainer == nil {
		return fmt.Errorf("source container not found")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "linked" {
		return fmt.Errorf("invalid clone mode %q: expected 'full' or 'linked'", mode)
	}
	if newLxcName == "" {
		newLxcName = fmt.Sprintf("ct-%d", newID)
	}
	if newVMName == "" {
		newVMName = fmt.Sprintf("vm-%d", newID)
	}

	srcContainer.LXCName = srcContainer.LxcName()
	srcContainer.KVMName = srcContainer.VirshName()

	if srcContainer.IsKVM() {
		return kvmManager.CloneContainer(srcContainer, newName, newLxcName, newVMName,
			newID, newUUID, newVNCPort, newSSHPort, newMAC, mode, startAfter)
	}

	return lxcManager.CloneContainer(srcContainer, newName, newLxcName, newID, newUUID,
		newVNCPort, newSSHPort, newMAC, mode, startAfter)
}

func validateCreateSSHAuth(cfg lxc.ContainerConfig) error {
	if cfg.Virtualization == config.VirtualizationKVM && kvm.IsWindowsImage(cfg.TemplateID) {
		return nil
	}
	_, err := lxc.ResolveCreateSSHAccess(cfg)
	return err
}

func validateReinstallSSHAuth(c *config.Container, templateID string, cfg lxc.ContainerConfig) error {
	if c != nil && c.IsKVM() && kvm.IsWindowsImage(templateID) {
		return nil
	}
	currentPassword := ""
	if c != nil {
		currentPassword = c.SSHPassword
	}
	_, err := lxc.ResolveReinstallSSHAccess(currentPassword, cfg)
	return err
}

func startByRuntime(id int) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.StartContainer(id)
	}
	return lxcManager.StartContainer(id)
}

func stopByRuntime(id int) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.StopContainer(id)
	}
	return lxcManager.StopContainer(id)
}

func restartByRuntime(id int) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.RestartContainer(id)
	}
	return lxcManager.RestartContainer(id)
}

func destroyByRuntime(id int) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.DestroyContainer(id)
	}
	return lxcManager.DestroyContainer(id)
}

func enterRescueByRuntime(id int, isoID, isoPath string) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.EnterRescue(id, isoID, isoPath)
	}
	return fmt.Errorf("rescue mode is only supported for KVM VMs")
}

func exitRescueByRuntime(id int) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.ExitRescue(id)
	}
	return fmt.Errorf("rescue mode is only supported for KVM VMs")
}

func attachISOByRuntime(id int, isoPath string) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.AttachISO(id, isoPath)
	}
	return fmt.Errorf("ISO attach is only supported for KVM VMs")
}

func detachISOByRuntime(id int) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.DetachISO(id)
	}
	return fmt.Errorf("ISO detach is only supported for KVM VMs")
}

func reinstallByRuntime(id int, templateID string, authConfig ...lxc.ContainerConfig) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.ReinstallContainer(id, templateID, authConfig...)
	}
	return lxcManager.ReinstallContainer(id, templateID, authConfig...)
}

func resetPasswordByRuntime(id int, password string) (string, error) {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.ResetSSHPassword(id, password)
	}
	return lxcManager.ResetSSHPassword(id, password)
}

func createAccountByRuntime(id int, username, password string, sudo bool) error {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.CreateAccount(id, username, password, sudo)
	}
	return lxcManager.CreateAccount(id, username, password, sudo)
}

// setHostnameByRuntime 实时修改运行中容器的 hostname。
// LXC: 通过 lxc-attach 执行 hostname + 写 /etc/hostname + 写 /etc/hosts。
// KVM: 通过 virsh set-hostname（依赖 QEMU guest agent；不支持则回退 SSH 进入）。
func setHostnameByRuntime(id int, hostname string) error {
	c := config.FindContainer(id)
	if c == nil {
		return fmt.Errorf("container not found")
	}
	if c.Status != "running" {
		return fmt.Errorf("container must be running to change hostname")
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	if len(hostname) > 63 {
		return fmt.Errorf("hostname too long (max 63 chars)")
	}
	if c.IsKVM() {
		// virsh set-hostname 是较新 libvirt 才有的命令，先试试；不行再回退
		if err := kvmSetHostnameVirsh(c.VirshName(), hostname); err == nil {
			return nil
		}
		// 回退：SSH 进入 VM 改 hostname
		return kvmManager.SetHostnameBySSH(id, hostname)
	}
	return lxcSetHostname(c.LxcName(), hostname)
}

// setVNCPasswordByRuntime 修改 KVM VM 的 VNC 密码（类比 主流面板 Change VNC Password）。
// LXC 不支持 VNC（用 WebSSH 代替），返回错误。
// 运行中 VM 尝试热更新（virsh update-device），停机 VM 下次启动生效。
func setVNCPasswordByRuntime(id int, password string) error {
	c := config.FindContainer(id)
	if c == nil {
		return fmt.Errorf("container not found")
	}
	if !c.IsKVM() {
		return fmt.Errorf("VNC password is only applicable to KVM VMs; LXC containers use WebSSH")
	}
	if len(password) > 255 {
		return fmt.Errorf("vnc password too long (max 255 chars)")
	}
	return kvmManager.SetVNCPassword(id, password)
}

func lxcSetHostname(lxcName, hostname string) error {
	script := fmt.Sprintf(`
set -e
hostname %s
echo %s > /etc/hostname
sed -i 's/^127\.0\.1\.1.*/127.0.1.1\t%s/' /etc/hosts 2>/dev/null || true
`, hostname, hostname, hostname)
	cmd, cancel := execLXCAttachWithTimeout(lxcName, "sh", "-c", script)
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("lxc-attach hostname: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func kvmSetHostnameVirsh(domain, hostname string) error {
	cmd, cancel := execWithTimeout("virsh", "set-hostname", domain, hostname)
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("virsh set-hostname: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func assignIPv6ByRuntime(id int) (*config.Container, error) {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.AssignIPv6(id)
	}
	return lxcManager.AssignIPv6(id)
}

func updatePublicIPv4ByRuntime(id int, requested []string, count int, auto bool) (*config.Container, error) {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.UpdatePublicIPv4Assignments(id, requested, count, auto)
	}
	return lxcManager.UpdatePublicIPv4Assignments(id, requested, count, auto)
}

func updateIPv6ByRuntime(id int, requested []string, count int, auto bool) (*config.Container, error) {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.UpdateIPv6Assignments(id, requested, count, auto)
	}
	return lxcManager.UpdateIPv6Assignments(id, requested, count, auto)
}

func usageByRuntime(id int) (map[string]interface{}, error) {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.GetResourceUsage(id)
	}
	return lxcManager.GetResourceUsage(id)
}

func trafficByRuntime(id int) map[string]interface{} {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.GetTrafficInfo(id)
	}
	return lxcManager.GetTrafficInfo(id)
}

func createSnapshotByRuntime(id int, createdBy string, scheduled bool, rotateLimit int, storagePoolID ...string) (config.Snapshot, error) {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.CreateSnapshot(id, createdBy, scheduled, rotateLimit, storagePoolID...)
	}
	return lxcManager.CreateSnapshot(id, createdBy, scheduled, rotateLimit, storagePoolID...)
}

func deleteSnapshotByRuntime(snapshotID string) error {
	snapshot := config.FindSnapshot(snapshotID)
	if snapshot != nil {
		if c := config.FindContainer(snapshot.ContainerID); c != nil && c.IsKVM() {
			return kvmManager.DeleteSnapshot(snapshotID)
		}
		if strings.Contains(snapshot.Path, string(os.PathSeparator)+"kvm"+string(os.PathSeparator)) {
			return kvmManager.DeleteSnapshot(snapshotID)
		}
	}
	return lxcManager.DeleteSnapshot(snapshotID)
}

func restoreSnapshotByRuntime(snapshotID string) error {
	snapshot := config.FindSnapshot(snapshotID)
	if snapshot != nil {
		if c := config.FindContainer(snapshot.ContainerID); c != nil && c.IsKVM() {
			return kvmManager.RestoreSnapshot(snapshotID)
		}
		if strings.Contains(snapshot.Path, string(os.PathSeparator)+"kvm"+string(os.PathSeparator)) {
			return kvmManager.RestoreSnapshot(snapshotID)
		}
	}
	return lxcManager.RestoreSnapshot(snapshotID)
}

func setSnapshotScheduleByRuntime(id int, enabled bool, intervalHours int, scheduleTime string, createdBy string) (*config.Container, error) {
	c := config.FindContainer(id)
	if c != nil && c.IsKVM() {
		return kvmManager.SetSnapshotSchedule(id, enabled, intervalHours, scheduleTime, createdBy)
	}
	return lxcManager.SetSnapshotSchedule(id, enabled, intervalHours, scheduleTime, createdBy)
}

func applyLimitsByRuntime(c *config.Container) error {
	if c != nil && c.IsKVM() {
		return kvmManager.ApplyContainerLimits(c)
	}
	return lxcManager.ApplyContainerLimits(c)
}

func listByRuntime() ([]config.Container, error) {
	containers, err := lxcManager.ListContainers()
	if err != nil {
		containers = config.AppConfig.Containers
	}
	containers = kvmManager.ListContainers(containers)
	// 防御性拷贝（实测踩坑）：下游所有列表过滤普遍使用 `filtered := containers[:0]`
	// 原地复用底层数组；若这里与全局 config 共享切片，任何一次带过滤的列表请求
	// 都会原地写坏内存态全局配置（回收站视图过滤曾触发：被回收实例的记录被
	// 后续元素覆写消失）。列表语义必须只读，这里统一拷贝隔离。
	out := make([]config.Container, len(containers))
	copy(out, containers)
	return out, err
}

// resizeDiskByRuntime 扩容系统盘（仅允许扩大），按运行时分发：
//   - KVM：qemu-img resize 在线扩大 qcow2（绝对容量；qemu 对缩小天然报错）。
//   - LXC：扩 rootfs.img 文件 + 在线 resize2fs 扩大 ext4 文件系统。
//
// 对不存在根镜像的容器（仅配置级软配额，如 dir 后端抽取后的卷）跳过物理
// 扩容，仅返回 nil，由调用方持久化 config.DiskGB。真正的文件系统在线扩容
// 失败会返回错误，调用方不会提交配置漂移。
func resizeDiskByRuntime(c *config.Container, newDiskGB float64) error {
	if c == nil {
		return nil
	}
	if c.IsKVM() {
		return growKVMQcow2Disk(c, newDiskGB)
	}
	return lxcManager.GrowLXCRootfsDisk(c, newDiskGB)
}

func growKVMQcow2Disk(c *config.Container, newDiskGB float64) error {
	if c.DiskImage == "" {
		return nil
	}
	if _, err := os.Stat(c.DiskImage); err != nil {
		return nil
	}
	diskMB := int64(math.Round(newDiskGB * 1024))
	if diskMB < 128 {
		diskMB = 128
	}
 _ctx0, _cancel0 := context.WithTimeout(context.Background(), 30*time.Second)
 defer _cancel0()
	out, err := exec.CommandContext(_ctx0, "qemu-img", "resize", c.DiskImage, fmt.Sprintf("%dM", diskMB)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img resize failed: %v, output: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func validateRuntimeResourceRequest(runtime string, templateID string, vcpu float64, ramMB int, diskGB float64) error {
	if runtime == config.VirtualizationKVM {
		if vcpu < 1 || math.Abs(vcpu-math.Round(vcpu)) > 0.000001 {
			return fmt.Errorf("KVM vCPU must be a whole number and at least 1")
		}
		if templateID != "" {
			minDisk := kvm.GetKVMImageMinDiskGB(templateID)
			if diskGB < minDisk {
				return fmt.Errorf("template %s requires at least %.2f GB disk, but requested %.2f GB", templateID, minDisk, diskGB)
			}
		} else if diskGB < 5 {
			return fmt.Errorf("KVM disk must be at least 5 GB")
		}
	} else {
		if templateID != "" {
			minDisk := lxc.GetTemplateMinDiskGB(templateID)
			if diskGB < minDisk {
				return fmt.Errorf("template %s requires at least %.2f GB disk, but requested %.2f GB", templateID, minDisk, diskGB)
			}
		} else if diskGB < 0.25 {
			return fmt.Errorf("disk must be at least 0.25 GB")
		}
	}
	return validateContainerResourceRequest(vcpu, ramMB, diskGB)
}
