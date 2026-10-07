package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"eyvescloud/internal/storage"
)

func TestSQLiteConfigMigratesLegacyJSONAndPersists(t *testing.T) {
	resetConfigStoreForTest(t)

	dir := t.TempDir()
	t.Cleanup(func() {
		resetConfigStoreForTest(t)
	})
	legacyPath := filepath.Join(dir, "config.json")
	SetConfigPath(legacyPath)

	legacy := EyvescloudConfig{
		AdminUser:       "admin",
		AdminPassHash:   "hash",
		JWTSecret:       "secret",
		Port:            8999,
		DataDir:         dir,
		NextContainerID: 2,
		NextVNCPort:     5900,
		NextSSHPort:     22000,
		Containers: []Container{{
			ID:               1,
			UUID:             "uuid-1",
			Name:             "ct1",
			Virtualization:   "lxc",
			Template:         "debian-12",
			Status:           "running",
			PortMappingLimit: 2,
			SnapshotLimit:    3,
			PortMappings: []PortMapping{{
				ContainerPort: 22,
				HostPort:      22001,
				Protocol:      "tcp",
				Description:   "SSH",
			}},
		}},
		AuditLogs: []AuditLog{{
			Time:   "2026-06-07 17:29:00",
			Action: "security_horizontal_scan",
			Target: "ct1",
			Detail: "[medium] 可疑横向探测",
			User:   "system",
		}},
		LoginLogs: []SavedLoginLog{{
			Time:      "2026-06-07 17:29:01 CST",
			Username:  "admin",
			IP:        "127.0.0.1",
			UserAgent: "test",
			Success:   true,
		}},
		Tasks: []SavedTask{{
			ID:            "task-1",
			Type:          "create",
			ContainerName: "ct2",
			Status:        "pending",
			CreatedAt:     "2026-06-07 17:29:02",
			Config:        `{"name":"ct2","template_id":"debian-12","vcpu":1,"ram_mb":512,"disk_gb":5,"extra_ports":[80,443],"nat_port_mappings":[{"host_port":30080,"container_port":80,"protocol":"tcp","description":"HTTP"}],"management_port":30022,"assign_ipv6":true}`,
		}},
		EnabledImages: []string{"debian-12"},
		CustomKVMImages: []CustomKVMImage{{
			ID:          "custom-kvm-test",
			Name:        "Test Cloud Image",
			Description: "third-party image",
			Distro:      "ubuntu",
			Release:     "noble",
			Arch:        "amd64",
			URL:         "https://images.example.test/ubuntu.qcow2",
			Provisioner: KVMProvisionerLinuxCloudInit,
			SHA256:      strings.Repeat("a", 64),
			CreatedAt:   "2026-07-26 10:00:00",
		}},
		CustomLXCImages: []CustomLXCImage{{
			ID:          "custom-lxc-test",
			Name:        "Test Rootfs",
			Description: "third-party LXC image",
			Distro:      "alpine",
			Release:     "3.21",
			Arch:        "amd64",
			URL:         "https://images.example.test/alpine-rootfs.tar.xz",
			SHA256:      strings.Repeat("b", 64),
			CreatedAt:   "2026-07-26 10:00:00",
		}},
		PanelAccessPolicy: PanelAccessPolicy{
			Enabled:        true,
			AllowedSources: []string{"192.0.2.0/24"},
			TrustedProxies: []string{"127.0.0.1"},
		},
		Snapshots: []Snapshot{{
			ID:            "snap-1",
			ContainerID:   1,
			ContainerName: "ct1",
			LXCName:       "ct-1",
			CreatedAt:     "2026-06-07 17:30:00",
			Path:          filepath.Join(dir, "snap-1"),
		}},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Containers) != 1 || len(cfg.Containers[0].PortMappings) != 1 {
		t.Fatalf("legacy config was not migrated: %+v", cfg.Containers)
	}
	if len(cfg.Tasks) != 1 || !strings.Contains(cfg.Tasks[0].Config, `"extra_ports":[80,443]`) {
		t.Fatalf("task config was not restored from sqlite columns: %+v", cfg.Tasks)
	}
	if !strings.Contains(cfg.Tasks[0].Config, `"nat_port_mappings":[{"host_port":30080,"container_port":80`) {
		t.Fatalf("task NAT mappings were not restored from sqlite: %+v", cfg.Tasks)
	}
	if !strings.Contains(cfg.Tasks[0].Config, `"management_port":30022`) {
		t.Fatalf("task management port was not restored from sqlite: %+v", cfg.Tasks)
	}
	if cfg.TaskConcurrency != DefaultTaskConcurrency {
		t.Fatalf("legacy task concurrency = %d, want default %d", cfg.TaskConcurrency, DefaultTaskConcurrency)
	}
	if !cfg.PanelAccessPolicy.Enabled || len(cfg.PanelAccessPolicy.AllowedSources) != 1 {
		t.Fatalf("legacy panel access policy was not migrated: %+v", cfg.PanelAccessPolicy)
	}
	if len(cfg.CustomKVMImages) != 1 || cfg.CustomKVMImages[0].ID != "custom-kvm-test" {
		t.Fatalf("legacy custom KVM images were not migrated: %+v", cfg.CustomKVMImages)
	}
	if len(cfg.CustomLXCImages) != 1 || cfg.CustomLXCImages[0].ID != "custom-lxc-test" {
		t.Fatalf("legacy custom LXC images were not migrated: %+v", cfg.CustomLXCImages)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.db")); err != nil {
		t.Fatalf("sqlite database was not created: %v", err)
	}

	cfg.Containers[0].Status = "stopped"
	cfg.TaskConcurrency = 6
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	resetConfigStoreForTest(t)
	SetConfigPath(legacyPath)
	cfg, err = InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Containers[0].Status; got != "stopped" {
		t.Fatalf("expected sqlite value to win after migration, got %q", got)
	}
	if got := cfg.TaskConcurrency; got != 6 {
		t.Fatalf("persisted task concurrency = %d, want 6", got)
	}
	if !cfg.PanelAccessPolicy.Enabled || cfg.PanelAccessPolicy.AllowedSources[0] != "192.0.2.0/24" {
		t.Fatalf("persisted panel access policy = %+v", cfg.PanelAccessPolicy)
	}
	if len(cfg.CustomKVMImages) != 1 || cfg.CustomKVMImages[0].SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("persisted custom KVM images = %+v", cfg.CustomKVMImages)
	}
	if len(cfg.CustomLXCImages) != 1 || cfg.CustomLXCImages[0].SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("persisted custom LXC images = %+v", cfg.CustomLXCImages)
	}
}

func resetConfigStoreForTest(t *testing.T) {
	t.Helper()
	if db != nil {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		db = nil
	}
	// 遥测库（telemetry.db）与配置库生命周期一致，重置时同样必须关闭，
	// 否则句柄会泄漏到下一个用例的临时目录。
	CloseTelemetryDB()
	// 行指纹是进程级状态（persistedRows），必须随 db 一起复位，
	// 否则上一个用例的指纹会泄漏到下一个用例，导致增量落库误判为「无变化」。
	resetPersistedRows()
	AppConfig = nil
	configPath = ""
}

// TestP0xStorageMigrationIdempotent 验证 P0-1 表结构迁移幂等：
// 同一数据库连续两次执行完整迁移（InitConfig 内含 createSchema +
// ensureSchemaMigrations），第二次不报错、不产生重复数据。
func TestP0xStorageMigrationIdempotent(t *testing.T) {
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	dir := t.TempDir()
	SetConfigPath(filepath.Join(dir, "config.json"))

	for round := 1; round <= 2; round++ {
		cfg, err := InitConfig()
		if err != nil {
			t.Fatalf("migration round %d failed: %v", round, err)
		}
		if len(cfg.Containers) != 0 {
			t.Fatalf("round %d: unexpected containers %+v", round, cfg.Containers)
		}
		// 每轮结束手工注册一个卷与一个池字段，下一轮加载必须原样读回
		//（第二轮同时证明第一轮数据未因重复迁移丢失）。
		vol := storage.Volume{
			ID:                    fmt.Sprintf("vol-idem-%d", round),
			PoolID:                "disk-root",
			Kind:                  storage.VolumeKindDir,
			SizeMB:                1024,
			AttachedToContainerID: round,
			Status:                storage.VolumeStatusAttached,
			CreatedAt:             "2026-09-24 12:00:00",
		}
		if err := CreateVolumeRecord(vol); err != nil {
			t.Fatalf("round %d: CreateVolumeRecord failed: %v", round, err)
		}
	}

	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	volumes := ListVolumes()
	if len(volumes) != 2 {
		t.Fatalf("expected 2 persisted volumes after two migration rounds, got %d: %+v", len(volumes), volumes)
	}
	for _, vol := range volumes {
		if got, ok := GetVolume(vol.ID); !ok || got.AttachedToContainerID != vol.AttachedToContainerID {
			t.Fatalf("volume %s did not round-trip: got=%+v ok=%v", vol.ID, got, ok)
		}
	}
}

// TestP0xVolumeRecordCRUD 覆盖卷记录完整生命周期（creating→available→
// attached→deleting）与删除语义（按容器批量清理、幂等删除）。
func TestP0xVolumeRecordCRUD(t *testing.T) {
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	dir := t.TempDir()
	SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}

	vol := storage.Volume{
		ID:        "vol-crud",
		PoolID:    "disk-root",
		Kind:      storage.VolumeKindDir,
		SizeMB:    2048,
		Status:    storage.VolumeStatusCreating,
		CreatedAt: "2026-09-24 12:00:00",
	}
	if err := CreateVolumeRecord(vol); err != nil {
		t.Fatal(err)
	}
	if err := CreateVolumeRecord(vol); !errors.Is(err, storage.ErrVolumeExists) {
		t.Fatalf("duplicate record error = %v, want ErrVolumeExists", err)
	}

	// creating → available → attached：全量更新生效。
	vol.Status = storage.VolumeStatusAvailable
	if err := UpdateVolumeRecord(vol); err != nil {
		t.Fatal(err)
	}
	vol.Status = storage.VolumeStatusAttached
	vol.AttachedToContainerID = 42
	if err := UpdateVolumeRecord(vol); err != nil {
		t.Fatal(err)
	}
	got, ok := GetVolume("vol-crud")
	if !ok || got.Status != storage.VolumeStatusAttached || got.AttachedToContainerID != 42 || got.SizeMB != 2048 {
		t.Fatalf("attached volume did not round-trip: %+v ok=%v", got, ok)
	}

	// attached → deleting：状态落库后按容器清理。
	vol.Status = storage.VolumeStatusDeleting
	if err := UpdateVolumeRecord(vol); err != nil {
		t.Fatal(err)
	}
	removed := DeleteContainerVolumes(42)
	if len(removed) != 1 || removed[0] != "vol-crud" {
		t.Fatalf("DeleteContainerVolumes(42) = %v, want [vol-crud]", removed)
	}
	if _, ok := GetVolume("vol-crud"); ok {
		t.Fatal("volume record still exists after DeleteContainerVolumes")
	}
	if removed := DeleteContainerVolumes(42); len(removed) != 0 {
		t.Fatalf("DeleteContainerVolumes must be idempotent, got %v", removed)
	}

	missing := storage.Volume{ID: "vol-missing", Kind: storage.VolumeKindDir, Status: storage.VolumeStatusAvailable}
	if err := UpdateVolumeRecord(missing); !errors.Is(err, storage.ErrVolumeNotFound) {
		t.Fatalf("update missing record error = %v, want ErrVolumeNotFound", err)
	}
	if err := DeleteVolumeRecord("vol-crud"); err != nil {
		t.Fatalf("DeleteVolumeRecord of missing id must be nil, got %v", err)
	}
}

// TestP0xStoragePoolAndContainerVolumeFieldsPersist 覆盖 P0-1 新字段双向持久化：
// 池的 Backend/Shared/Watermark、容器的 RootVolumeID/DataVolumeIDs 均需
// 保存后原样读回；旧数据（空 Backend）加载时自动补 dir。
func TestP0xStoragePoolAndContainerVolumeFieldsPersist(t *testing.T) {
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	dir := t.TempDir()
	SetConfigPath(filepath.Join(dir, "config.json"))
	cfg, err := InitConfig()
	if err != nil {
		t.Fatal(err)
	}

	cfg.StoragePools = []StoragePool{{
		ID:                "disk-data",
		Name:              "data",
		Path:              "/mnt/data/eyvescloud",
		MountPoint:        "/mnt/data",
		ContentTypes:      []string{StorageContentLXC},
		DefaultContents:   []string{StorageContentLXC},
		Enabled:           true,
		Shared:            true,
		WatermarkWarn:     75,
		WatermarkCritical: 85,
	}}
	cfg.Containers = []Container{{
		ID:             1,
		UUID:           "uuid-vol",
		Name:           "ct-vol",
		Virtualization: VirtualizationLXC,
		LXCName:        "ct-1",
		Status:         "stopped",
		RootVolumeID:   "vol-root-1",
		DataVolumeIDs:  []string{"vol-data-1", "vol-data-2"},
	}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}

	resetConfigStoreForTest(t)
	SetConfigPath(filepath.Join(dir, "config.json"))
	cfg, err = InitConfig()
	if err != nil {
		t.Fatal(err)
	}
	pool := cfg.StoragePools[0]
	if pool.Backend != storage.BackendDir {
		t.Fatalf("pool backend = %q, want %q (empty must normalize to dir)", pool.Backend, storage.BackendDir)
	}
	if !pool.Shared || pool.WatermarkWarn != 75 || pool.WatermarkCritical != 85 {
		t.Fatalf("P0-1 pool fields did not round-trip: %+v", pool)
	}
	c := cfg.Containers[0]
	if c.RootVolumeID != "vol-root-1" || len(c.DataVolumeIDs) != 2 || c.DataVolumeIDs[1] != "vol-data-2" {
		t.Fatalf("container volume fields did not round-trip: %+v", c)
	}
}
