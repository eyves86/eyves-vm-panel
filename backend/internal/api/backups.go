package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"
)

// 实例级备份（数据安全核心）：
//
// 备份 = 先走既有快照机（冷拷贝，保证一致性）得到一份 snapshot 目录，
//      再将其以 tar 归档到实例备份存储，登记 InstanceBackup，随后回收临时快照与磁盘。
// 还原 = 将归档解压到快照存储基址下的临时目录，复用既有 RestoreSnapshot 的
//      停机→交换→重启逻辑，极大降低回到真实机状态的风险。
//
// 设计要点：任何操作都先做安全路径校验；归档/解压使用 tar 命令（可靠、支持稀疏大文件）；
// backup 记录独立持久化，可跨实例删除/重装存活。

var instanceBackupMu sync.Mutex

func instanceBackupStoreDir() string {
	return filepath.Join(config.AppConfig.DataDir, "instance-backups")
}

func safeInstanceBackupStorePath(path string) error {
	store := instanceBackupStoreDir()
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absStore, err := filepath.Abs(store)
	if err != nil {
		return err
	}
	if strings.HasPrefix(absPath, absStore+string(os.PathSeparator)) {
		return nil
	}
	return fmt.Errorf("refusing unsafe backup path: %s", absPath)
}

// snapshotRestoreBase 返回一个受安全快照路径保护的解压基址（复位 Snapshot 可复用安全校验）。
func snapshotRestoreBase() (string, error) {
	pool, err := config.SelectStoragePoolForContent(config.StorageContentSnapshots, "", 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(pool.Path, "snapshots"), nil
}

// createInstanceBackup 为容器创建一份完整备份并做 keep-N 轮换。
// keep <= 0 表示不自动清理（手工备份全量保留），keep > 0 表示仅保留最新 keep 份。
func createInstanceBackup(containerID int, createdBy string, keep int) (*config.InstanceBackup, error) {
	instanceBackupMu.Lock()
	defer instanceBackupMu.Unlock()

	c := config.FindContainer(containerID)
	if c == nil {
		return nil, fmt.Errorf("container not found: %d", containerID)
	}

	// 1) 生成一致性快照（若快照池不可用则直接报错，避免半成品）。
	snapshot, err := createSnapshotByRuntime(containerID, createdBy, false, 0)
	if err != nil {
		return nil, fmt.Errorf("create consistency snapshot: %w", err)
	}
	// 无论归档是否成功，都回收临时快照。
	defer func() {
		if err := deleteSnapshotByRuntime(snapshot.ID); err != nil {
			fmt.Printf("Warning: failed to cleanup temp snapshot %s: %v\n", snapshot.ID, err)
		}
	}()

	backupID := fmt.Sprintf("bak-%d-%s", containerID, time.Now().Format("20060102150405-000000000"))
	storeDir := filepath.Join(instanceBackupStoreDir(), strconv.Itoa(containerID))
	if err := os.MkdirAll(storeDir, 0700); err != nil {
		return nil, err
	}
	archive := filepath.Join(storeDir, backupID+".tar")
	if err := safeInstanceBackupStorePath(archive); err != nil {
		return nil, err
	}
	if err := tarDirectory(snapshot.Path, archive); err != nil {
		_ = os.RemoveAll(archive)
		return nil, fmt.Errorf("archive backup: %w", err)
	}
	info, err := os.Stat(archive)
	if err != nil {
		_ = os.RemoveAll(archive)
		return nil, err
	}
	if info.Size() == 0 {
		_ = os.RemoveAll(archive)
		return nil, fmt.Errorf("backup archive is empty")
	}

	kind := "lxc"
	if c.IsKVM() {
		kind = "kvm"
	}
	backup := config.InstanceBackup{
		ID:            backupID,
		ContainerID:   c.ID,
		ContainerName: c.Name,
		Kind:          kind,
		CreatedAt:     time.Now().Format("2006-01-02 15:04:05"),
		CreatedBy:     createdBy,
		Path:          archive,
		SizeBytes:     info.Size(),
	}
	if err := config.AddInstanceBackup(backup); err != nil {
		_ = os.RemoveAll(archive)
		return nil, err
	}

	if keep > 0 {
		pruneInstanceBackups(containerID, keep)
	}
	return &backup, nil
}

func pruneInstanceBackups(containerID int, keep int) {
	backups := config.ContainerInstanceBackups(containerID) // 已按最新在前排序
	for _, b := range backupsToPrune(backups, keep) {
		if b.Path != "" && safeInstanceBackupStorePath(b.Path) == nil {
			_ = os.RemoveAll(b.Path)
		}
		config.RemoveInstanceBackup(b.ID)
	}
}

// backupsToPrune 给定按最新在前排序的 backups，返回应被清理的最老备份（保留最新 keep 份）。
// keep <= 0 表示不清理；keep >= len(backups) 表示全部保留。
func backupsToPrune(backups []config.InstanceBackup, keep int) []config.InstanceBackup {
	if keep <= 0 || keep >= len(backups) {
		return nil
	}
	result := make([]config.InstanceBackup, 0, len(backups)-keep)
	for i := len(backups) - 1; i >= keep; i-- {
		result = append(result, backups[i])
	}
	return result
}

func restoreInstanceBackup(backupID string) error {
	instanceBackupMu.Lock()
	defer instanceBackupMu.Unlock()

	backup := config.FindInstanceBackup(backupID)
	if backup == nil {
		return fmt.Errorf("backup not found: %s", backupID)
	}
	if backup.Path == "" {
		return fmt.Errorf("backup path is empty")
	}
	if err := safeInstanceBackupStorePath(backup.Path); err != nil {
		return err
	}
	if _, err := os.Stat(backup.Path); err != nil {
		return fmt.Errorf("backup archive not found: %v", err)
	}
	c := config.FindContainer(backup.ContainerID)
	if c == nil {
		return fmt.Errorf("container not found: %d", backup.ContainerID)
	}

	// 1) 解压到受保护的快照基址下，供既有 RestoreSnapshot 复用安全校验。
	base, err := snapshotRestoreBase()
	if err != nil {
		return fmt.Errorf("snapshot storage unavailable: %w", err)
	}
	restoreDir := filepath.Join(base, strconv.Itoa(c.ID), "restore-"+backup.ID)
	if err := safePathUnder(restoreDir, base); err != nil {
		return err
	}
	if err := os.RemoveAll(restoreDir); err != nil {
		return err
	}
	if err := os.MkdirAll(restoreDir, 0700); err != nil {
		return err
	}
	cleanupRestore := func() { _ = os.RemoveAll(restoreDir) }
	if err := untarDirectory(backup.Path, restoreDir); err != nil {
		cleanupRestore()
		return fmt.Errorf("extract backup: %w", err)
	}

	// 2) 登记临时快照并复用还原逻辑。
	tempSnapshotID := "restore-tmp-" + backup.ID
	config.AddSnapshot(config.Snapshot{
		ID:            tempSnapshotID,
		ContainerID:   c.ID,
		ContainerName: c.Name,
		CreatedAt:     time.Now().Format("2006-01-02 15:04:05"),
		Path:          restoreDir,
		Scheduled:     false,
	})
	defer cleanupRestore()
	defer config.RemoveSnapshot(tempSnapshotID)

	if err := restoreSnapshotByRuntime(tempSnapshotID); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	return nil
}

func deleteInstanceBackup(backupID string) error {
	instanceBackupMu.Lock()
	defer instanceBackupMu.Unlock()

	backup := config.FindInstanceBackup(backupID)
	if backup == nil {
		return fmt.Errorf("backup not found: %s", backupID)
	}
	if backup.Path != "" && safeInstanceBackupStorePath(backup.Path) == nil {
		if err := os.RemoveAll(backup.Path); err != nil {
			return fmt.Errorf("failed to delete backup archive: %v", err)
		}
	}
	if !config.RemoveInstanceBackup(backupID) {
		return fmt.Errorf("failed to remove backup record")
	}
	return nil
}

func tarDirectory(srcDir, archive string) error {
	// 使用 tar 归档，保留稀疏文件语义，支持大型 rootfs / qcow2。
	cmd := exec.Command("tar", "-C", srcDir, "--totals", "-cf", archive, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tar failed: %v: %s", err, string(out))
	}
	return nil
}

func untarDirectory(archive, destDir string) error {
	cmd := exec.Command("tar", "-C", destDir, "-xf", archive)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("untar failed: %v: %s", err, string(out))
	}
	return nil
}

func safePathUnder(path string, base string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	if absPath == absBase || strings.HasPrefix(absPath, absBase+string(os.PathSeparator)) {
		return nil
	}
	return fmt.Errorf("refusing unsafe path: %s", absPath)
}

// ---- HTTP 路由（/api/containers/{id}/backups[/...]） ----

func handleContainerBackups(w http.ResponseWriter, r *http.Request, containerID int, action string) {
	switch {
	case action == "backups" && r.Method == http.MethodGet:
		if !requireScope(w, r, "snapshot:read") {
			return
		}
		listContainerBackups(w, r, containerID)
	case action == "backups" && r.Method == http.MethodPost:
		if !requireScope(w, r, "snapshot:create") {
			return
		}
		createContainerBackup(w, r, containerID)
	case strings.HasPrefix(action, "backups/") && strings.HasSuffix(action, "/restore") && r.Method == http.MethodPost:
		if !requireScope(w, r, "snapshot:restore") {
			return
		}
		backupID := strings.TrimSuffix(strings.TrimPrefix(action, "backups/"), "/restore")
		restoreContainerBackup(w, r, containerID, backupID)
	case strings.HasPrefix(action, "backups/") && r.Method == http.MethodDelete:
		if !requireScope(w, r, "snapshot:delete") {
			return
		}
		backupID := strings.TrimPrefix(action, "backups/")
		deleteContainerBackup(w, r, containerID, backupID)
	default:
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Backup action not found"})
	}
}

func listContainerBackups(w http.ResponseWriter, r *http.Request, containerID int) {
	if config.FindContainer(containerID) == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	backups := config.ContainerInstanceBackups(containerID)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: backups})
}

func createContainerBackup(w http.ResponseWriter, r *http.Request, containerID int) {
	if config.FindContainer(containerID) == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if isSubUserRequest(r) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Sub-users cannot create instance backups"})
		return
	}
	var req struct {
		Keep int `json:"keep"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
	}
	user := requestUser(r)
	backup, err := createInstanceBackup(containerID, user, req.Keep)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	config.AddAuditLog("container.backup", backup.ContainerName, backup.ID, user)
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: backup})
}

func restoreContainerBackup(w http.ResponseWriter, r *http.Request, containerID int, backupID string) {
	backup := config.FindInstanceBackup(backupID)
	if backup == nil || backup.ContainerID != containerID {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Backup not found"})
		return
	}
	user := requestUser(r)
	if err := restoreInstanceBackup(backupID); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	config.AddAuditLog("container.backup_restore", backup.ContainerName, backup.ID, user)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Backup restored"})
}

func deleteContainerBackup(w http.ResponseWriter, r *http.Request, containerID int, backupID string) {
	backup := config.FindInstanceBackup(backupID)
	if backup == nil || backup.ContainerID != containerID {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Backup not found"})
		return
	}
	user := requestUser(r)
	if err := deleteInstanceBackup(backupID); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	config.AddAuditLog("container.backup_delete", backup.ContainerName, backupID, user)
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Backup deleted"})
}