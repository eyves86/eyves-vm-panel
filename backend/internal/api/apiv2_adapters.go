package api

// apiv2_adapters.go —— API v2 与内部实现的适配层。
//
// v2 的 handler 只表达契约（参数/字段/错误码），具体动作通过本文件收敛到
// 既有内部能力（lxc.Manager / kvm.Manager / 任务队列 / 配置层），避免在
// handler 里散落实现细节，也便于后续替换实现而不动契约。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/kvm"
	"eyvescloud/internal/lxc"
)

// sortSliceStable 泛型稳定排序（保持同分值元素的原始顺序，列表分页才稳定）。
func sortSliceStable[T any](items []T, less func(a, b T) bool) {
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
}

// ---------------------------------------------------------------------------
// 快照
// ---------------------------------------------------------------------------

func v2SnapshotView(snap config.Snapshot) map[string]interface{} {
	return map[string]interface{}{
		"id":           snap.ID,
		"instance_id":  snap.ContainerID,
		"name":         snap.ID,
		"size_bytes":   snap.SizeBytes,
		"created_at":   v2Time(snap.CreatedAt),
		"created_by":   snap.CreatedBy,
		"scheduled":    snap.Scheduled,
		"storage_path": snap.Path,
	}
}

func v2SnapshotViews(c config.Container) []map[string]interface{} {
	items := []map[string]interface{}{}
	for _, snap := range config.ContainerSnapshots(c.ID) {
		items = append(items, v2SnapshotView(snap))
	}
	sortSliceStable(items, func(a, b map[string]interface{}) bool {
		return fmt.Sprint(a["created_at"]) > fmt.Sprint(b["created_at"])
	})
	return items
}

func createSnapshotForContainer(containerID int, name string, createdBy string) (config.Snapshot, error) {
	c := config.FindContainer(containerID)
	if c == nil {
		return config.Snapshot{}, fmt.Errorf("实例不存在")
	}
	manager := lxc.NewManager()
	snap, err := manager.CreateSnapshot(containerID, createdBy, false, c.SnapshotLimit)
	if err != nil {
		return config.Snapshot{}, err
	}
	_ = name // 快照名由内部生成（snap-xxx），保留参数以便后续支持自定义名
	return snap, nil
}

func deleteSnapshotForContainer(containerID int, snapshotID string) error {
	snap := config.FindSnapshot(snapshotID)
	if snap == nil || snap.ContainerID != containerID {
		return fmt.Errorf("快照不存在")
	}
	return lxc.NewManager().DeleteSnapshot(snapshotID)
}

func restoreSnapshotForContainer(containerID int, snapshotID string) error {
	snap := config.FindSnapshot(snapshotID)
	if snap == nil || snap.ContainerID != containerID {
		return fmt.Errorf("快照不存在")
	}
	return lxc.NewManager().RestoreSnapshot(snapshotID)
}

// ---------------------------------------------------------------------------
// 备份
// ---------------------------------------------------------------------------

func v2BackupView(backup config.InstanceBackup) map[string]interface{} {
	return map[string]interface{}{
		"id":          backup.ID,
		"instance_id": backup.ContainerID,
		"name":        backup.ID,
		"size_bytes":  backup.SizeBytes,
		"created_at":  v2Time(backup.CreatedAt),
		"created_by":  backup.CreatedBy,
		"scheduled":   backup.Scheduled,
		"kind":        backup.Kind,
		"path":        backup.Path,
	}
}

func v2BackupViews(c config.Container) []map[string]interface{} {
	items := []map[string]interface{}{}
	for _, backup := range config.ContainerInstanceBackups(c.ID) {
		items = append(items, v2BackupView(backup))
	}
	sortSliceStable(items, func(a, b map[string]interface{}) bool {
		return fmt.Sprint(a["created_at"]) > fmt.Sprint(b["created_at"])
	})
	return items
}

func createBackupForContainer(containerID int, createdBy string) (config.InstanceBackup, error) {
	backup, err := createInstanceBackup(containerID, createdBy, 0, false)
	if err != nil {
		return config.InstanceBackup{}, err
	}
	if backup == nil {
		return config.InstanceBackup{}, fmt.Errorf("备份创建失败")
	}
	return *backup, nil
}

func deleteBackupForContainer(containerID int, backupID string) error {
	for _, backup := range config.ContainerInstanceBackups(containerID) {
		if backup.ID == backupID {
			return deleteInstanceBackup(backupID)
		}
	}
	return fmt.Errorf("备份不存在")
}

// ---------------------------------------------------------------------------
// 克隆 / 救援 / 改密 / 端口映射
// ---------------------------------------------------------------------------

// cloneContainerForAPI 克隆实例（沿用面板的克隆实现，自动分配 ID/UUID/端口/MAC）。
func cloneContainerForAPI(sourceID int, newName string, operator string) error {
	src := config.FindContainer(sourceID)
	if src == nil {
		return fmt.Errorf("源实例不存在")
	}
	if config.FindContainerByName(newName) != nil {
		return fmt.Errorf("实例名称已存在")
	}
	newID := nextContainerIDForV2()
	newUUID := config.NewContainerUUID()
	newLxcName := fmt.Sprintf("ct-%d", newID)
	newVMName := fmt.Sprintf("vm-%d", newID)
	vncPort, sshPort, mac := allocateCloneIdentifiers()
	mode := "clone"
	if src.IsKVM() {
		mode = "clone"
	}
	if err := cloneByRuntime(src, newName, newLxcName, newVMName, newID, newUUID,
		fmt.Sprintf("%d", vncPort), fmt.Sprintf("%d", sshPort), mac, mode, false); err != nil {
		return err
	}
	auditRequest(nil, "api.v2.instance.clone", src.Name, "克隆为 "+newName+"（操作人 "+operator+"）", true, "")
	return nil
}

func nextContainerIDForV2() int {
	config.AppConfigMu.Lock()
	defer config.AppConfigMu.Unlock()
	id := config.AppConfig.NextContainerID
	config.AppConfig.NextContainerID++
	return id
}

func allocateCloneIdentifiers() (int, int, string) {
	config.AppConfigMu.Lock()
	defer config.AppConfigMu.Unlock()
	vncPort := config.AppConfig.NextVNCPort
	config.AppConfig.NextVNCPort++
	sshPort := config.AppConfig.NextSSHPort
	config.AppConfig.NextSSHPort++
	return vncPort, sshPort, randomVirtualMAC()
}

// applyRescueForContainer 进入/退出救援模式（KVM：挂载救援 ISO 并重启）。
func applyRescueForContainer(containerID int, enter bool) error {
	c := config.FindContainer(containerID)
	if c == nil {
		return fmt.Errorf("实例不存在")
	}
	if !c.IsKVM() {
		return fmt.Errorf("救援模式仅 KVM 实例支持")
	}
	if enter {
		isoPath := ""
		if c.RescueISOID != "" {
			config.AppConfigMu.RLock()
			for _, iso := range config.AppConfig.ISOFiles {
				if iso.ID == c.RescueISOID {
					isoPath = iso.Path
					break
				}
			}
			config.AppConfigMu.RUnlock()
		}
		if isoPath == "" {
			return fmt.Errorf("救援 ISO 不存在或未配置路径：%s", c.RescueISOID)
		}
		return enterRescueByRuntime(containerID, c.RescueISOID, isoPath)
	}
	return exitRescueByRuntime(containerID)
}

// quickSetContainerPassword 直接设置实例 root 密码（不排队，便于 API 同步返回）。
func quickSetContainerPassword(containerID int, password string) error {
	c := config.FindContainer(containerID)
	if c == nil {
		return fmt.Errorf("实例不存在")
	}
	if c.IsKVM() {
		_, err := kvm.NewManager().ResetSSHPassword(containerID, password)
		return err
	}
	return lxc.NewManager().SetRootPasswordByID(containerID, password)
}

func applyPortMappingsForContainer(containerID int) error {
	return lxc.NewManager().ApplyPortMappings(containerID)
}

// ---------------------------------------------------------------------------
// 网络资源调整
// ---------------------------------------------------------------------------

// setIPv6CountForContainer 调整实例 IPv6 数量（增加时从已配置前缀分配）。
func setIPv6CountForContainer(containerID, count int) error {
	if count < 0 {
		return fmt.Errorf("ipv6_count 不能为负")
	}
	c := config.FindContainer(containerID)
	if c == nil {
		return fmt.Errorf("实例不存在")
	}
	current := len(c.IPv6Addresses)
	if count == current {
		return nil
	}
	manager := lxc.NewManager()
	if count > current {
		for i := current; i < count; i++ {
			if _, err := manager.AssignIPv6(containerID); err != nil {
				return err
			}
		}
		return nil
	}
	// 减少：从尾部释放（保留前面的地址，避免打断已有连接）
	for i := count; i < current; i++ {
		updated := config.FindContainer(containerID)
		if updated == nil || len(updated.IPv6Addresses) == 0 {
			break
		}
		config.MutateContainerByID(containerID, func(target *config.Container) {
			if len(target.IPv6Addresses) > 0 {
				target.IPv6Addresses = target.IPv6Addresses[:len(target.IPv6Addresses)-1]
			}
		})
	}
	return nil
}

// setPublicIPv4CountForContainer 调整实例公网 IPv4 数量（从地址池分配 / 归还）。
func setPublicIPv4CountForContainer(containerID, count int) error {
	if count < 0 {
		return fmt.Errorf("public_ipv4_count 不能为负")
	}
	c := config.FindContainer(containerID)
	if c == nil {
		return fmt.Errorf("实例不存在")
	}
	current := len(c.PublicIPv4s)
	if count == current {
		return nil
	}
	if count < current {
		config.MutateContainerByID(containerID, func(target *config.Container) {
			target.PublicIPv4s = target.PublicIPv4s[:count]
		})
		return nil
	}
	// 增加：从池中挑选未被占用的地址
	config.AppConfigMu.RLock()
	pool := append([]config.PublicIPv4Assignment(nil), config.AppConfig.PublicIPv4Pool...)
	config.AppConfigMu.RUnlock()
	used := map[string]bool{}
	for _, item := range c.PublicIPv4s {
		used[item.Address] = true
	}
	assigned := []config.PublicIPv4Assignment{}
	need := count - current
	for _, item := range pool {
		if need == 0 {
			break
		}
		if item.Address == "" || used[item.Address] {
			continue
		}
		assigned = append(assigned, config.PublicIPv4Assignment{Address: item.Address, Interface: item.Interface})
		used[item.Address] = true
		need--
	}
	if need > 0 {
		return fmt.Errorf("地址池可用公网 IPv4 不足（缺少 %d 个）", need)
	}
	config.MutateContainerByID(containerID, func(target *config.Container) {
		target.PublicIPv4s = append(target.PublicIPv4s, assigned...)
	})
	return nil
}

// ---------------------------------------------------------------------------
// KVM / 控制台辅助
// ---------------------------------------------------------------------------

func kvmImageExistsV2(imageID string) bool {
	return kvm.FindImage(imageID) != nil
}

func kvmDomainXMLV2(domain string) (string, error) {
	out, err := exec.Command("virsh", "dumpxml", domain).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func urlQueryEscapeV2(value string) string { return url.QueryEscape(value) }

// newVNCTicketV2 为 KVM 实例签发一次性 VNC 票据（复用面板的 VNC 票据机制）。
func newVNCTicketV2(c *config.Container, r *http.Request) string {
	ticket := randomHex(32)
	webVNCTickets.Lock()
	cleanupExpiredWebVNCTicketsLocked(time.Now())
	webVNCTickets.items[ticket] = webVNCTicket{
		ContainerName: c.Name,
		ClientIP:      clientIP(r),
		UserAgent:     r.UserAgent(),
		ExpiresAt:     time.Now().Add(60 * time.Second),
	}
	webVNCTickets.Unlock()
	return ticket
}

// v2JSON 便捷：把结构体转成 map（用于把内部结构直接嵌入响应）。
func v2JSON(value interface{}) map[string]interface{} {
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]interface{}{}
	}
	out := map[string]interface{}{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// v2NormalizeSortKey 兼容前端传 "created_at:desc" 的写法。
func v2NormalizeSortKey(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) == 2 {
		return parts[0], strings.EqualFold(parts[1], "desc")
	}
	return raw, false
}

// startKVMImageDownloadV2 触发 KVM 镜像下载（与面板 v1 流程一致：下载进度写入
// 内存状态表，供 GET /images/{id} 轮询展示；完成后自动启用该镜像）。
func startKVMImageDownloadV2(img kvm.Image) error {
	if ok, _ := kvm.ImageDownloadedInfo(img.ID); ok {
		return nil
	}
	ctx, started := startImageDownload(img.ID, "downloading")
	if !started {
		return fmt.Errorf("该镜像正在下载中")
	}
	go func() {
		err := kvm.DownloadImageWithProgress(ctx, img, func(p kvm.DownloadProgress) {
			updateImageDownload(img.ID, func(st *imageDownloadStatus) {
				st.Stage = p.Stage
				st.DownloadedBytes = p.DownloadedBytes
				st.TotalBytes = p.TotalBytes
				st.Progress = p.Percent
			})
		})
		finishImageDownload(img.ID, err)
		if err == nil {
			ensureImageEnabled(img.ID)
		}
	}()
	return nil
}

// hostSummaryForIDC 宿主机实时指标（供 /metrics/host 与首页复用）。
// 字段口径与实例指标一致（百分比保留两位小数），便于前端统一展示。
func hostSummaryForIDC() map[string]interface{} {
	info := getHostInfo()
	memUsage := 0.0
	if info.RAM.TotalMB > 0 {
		memUsage = float64(info.RAM.UsedMB) / float64(info.RAM.TotalMB) * 100
	}
	diskUsage := 0.0
	if info.Disk.TotalGB > 0 {
		diskUsage = info.Disk.UsedGB / info.Disk.TotalGB * 100
	}
	return map[string]interface{}{
		"cpu_count":       info.CPU.Cores,
		"cpu_percent":     round2(info.CPU.Usage),
		"memory_total_mb": info.RAM.TotalMB,
		"memory_used_mb":  info.RAM.UsedMB,
		"memory_percent":  round2(memUsage),
		"disk_total_gb":   round2(info.Disk.TotalGB),
		"disk_used_gb":    round2(info.Disk.UsedGB),
		"disk_percent":    round2(diskUsage),
		"load1":           info.Load.Load1,
		"load5":           info.Load.Load5,
		"load15":          info.Load.Load15,
		"network_rx_bps":  info.Network.RXBps,
		"network_tx_bps":  info.Network.TXBps,
	}
}
