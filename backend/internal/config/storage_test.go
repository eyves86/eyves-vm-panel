package config

import (
	"path/filepath"
	"testing"
)

func TestNormalizeStoragePoolsReplacesPersistedCustomPath(t *testing.T) {
	previousConfig := AppConfig
	t.Cleanup(func() { AppConfig = previousConfig })
	mountPoint := filepath.Join(t.TempDir(), "data")

	AppConfig = &EyvescloudConfig{StoragePools: []StoragePool{{
		ID:         "data",
		Name:       "data",
		Path:       filepath.Join(t.TempDir(), "uncontrolled"),
		MountPoint: mountPoint,
		Enabled:    true,
	}}}
	if !normalizeStoragePools() {
		t.Fatal("expected custom path normalization to report a change")
	}
	want := managedStoragePoolPath(mountPoint)
	if got := AppConfig.StoragePools[0].Path; got != want {
		t.Fatalf("normalized path = %q, want %q", got, want)
	}
}

func TestSelectStoragePoolForContent(t *testing.T) {
	previousConfig := AppConfig
	previousProbe := probeStoragePoolFreeBytes
	t.Cleanup(func() {
		AppConfig = previousConfig
		probeStoragePoolFreeBytes = previousProbe
	})

	AppConfig = &EyvescloudConfig{StoragePools: []StoragePool{
		{
			ID:              "primary",
			Path:            "/primary",
			ContentTypes:    []string{StorageContentLXC},
			DefaultContents: []string{StorageContentLXC},
			Enabled:         true,
		},
		{
			ID:           "large",
			Path:         "/large",
			ContentTypes: []string{StorageContentLXC},
			Enabled:      true,
		},
		{
			ID:           "small",
			Path:         "/small",
			ContentTypes: []string{StorageContentLXC},
			Enabled:      true,
		},
	}}

	free := map[string]int64{
		"primary": 20 * 1024 * 1024 * 1024,
		"large":   50 * 1024 * 1024 * 1024,
		"small":   10 * 1024 * 1024 * 1024,
	}
	probeStoragePoolFreeBytes = func(pool StoragePool) (int64, bool) {
		value, ok := free[pool.ID]
		return value, ok
	}

	pool, err := SelectStoragePoolForContent(StorageContentLXC, "", 5*1024*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if pool.ID != "primary" {
		t.Fatalf("selected %q, want configured default primary", pool.ID)
	}

	free["primary"] = 128 * 1024 * 1024
	pool, err = SelectStoragePoolForContent(StorageContentLXC, "", 5*1024*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if pool.ID != "large" {
		t.Fatalf("selected %q, want largest fallback pool", pool.ID)
	}

	pool, err = SelectStoragePoolForContent(StorageContentLXC, "small", 5*1024*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if pool.ID != "small" {
		t.Fatalf("selected %q, want requested pool", pool.ID)
	}
}

func TestSelectStoragePoolRequiresEnabledContent(t *testing.T) {
	previousConfig := AppConfig
	previousProbe := probeStoragePoolFreeBytes
	t.Cleanup(func() {
		AppConfig = previousConfig
		probeStoragePoolFreeBytes = previousProbe
	})

	AppConfig = &EyvescloudConfig{StoragePools: []StoragePool{{
		ID:           "primary",
		Path:         "/primary",
		ContentTypes: []string{StorageContentLXC},
		Enabled:      true,
	}}}
	probeStoragePoolFreeBytes = func(StoragePool) (int64, bool) { return 100 * 1024 * 1024 * 1024, true }

	if _, err := SelectStoragePoolForContent(StorageContentSnapshots, "", 0); err == nil {
		t.Fatal("expected snapshots selection to fail when no pool enables snapshots")
	}
}
