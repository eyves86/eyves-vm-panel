package api

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"eyvescloud/internal/config"
)

func TestIsUsableStorageMount(t *testing.T) {
	tests := []struct {
		name       string
		deviceType string
		fsType     string
		devicePath string
		mountPoint string
		readOnly   bool
		wantUsable bool
	}{
		{name: "root partition", deviceType: "part", fsType: "ext4", devicePath: "/dev/sda2", mountPoint: "/", wantUsable: true},
		{name: "mounted data disk", deviceType: "disk", fsType: "xfs", devicePath: "/dev/sdb", mountPoint: "/data", wantUsable: true},
		{name: "snap loop", deviceType: "loop", fsType: "squashfs", devicePath: "/dev/loop0", mountPoint: "/snap/core20/2105", readOnly: true},
		{name: "loop without ro flag", deviceType: "loop", fsType: "ext4", devicePath: "/dev/loop7", mountPoint: "/mnt/loop"},
		{name: "read only disk", deviceType: "part", fsType: "ext4", devicePath: "/dev/sdc1", mountPoint: "/archive", readOnly: true},
		{name: "optical image", deviceType: "rom", fsType: "iso9660", devicePath: "/dev/sr0", mountPoint: "/media/cdrom"},
		{name: "efi partition", deviceType: "part", fsType: "vfat", devicePath: "/dev/sda1", mountPoint: "/boot/efi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUsableStorageMount(tt.deviceType, tt.fsType, tt.devicePath, tt.mountPoint, tt.readOnly)
			if got != tt.wantUsable {
				t.Fatalf("isUsableStorageMount() = %v, want %v", got, tt.wantUsable)
			}
		})
	}
}

func TestBestMountPointForPath(t *testing.T) {
	disks := []storageDiskInfo{
		{Path: "/dev/sda2", MountPoint: "/"},
		{Path: "/dev/sdb1", MountPoint: "/mnt/eyvescloud-data"},
	}
	tests := []struct {
		path string
		want string
	}{
		{path: "/var/lib/eyvescloud", want: "/"},
		{path: "/mnt/eyvescloud-data/eyvescloud", want: "/mnt/eyvescloud-data"},
		{path: "/mnt/eyvescloud-data", want: "/mnt/eyvescloud-data"},
	}
	for _, tt := range tests {
		if got := bestMountPointForPath(tt.path, disks); got != tt.want {
			t.Fatalf("bestMountPointForPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestNormalizeStoragePoolsUsesServerManagedPath(t *testing.T) {
	disks := []storageDiskInfo{
		{Path: "/dev/sda2", MountPoint: "/"},
		{Path: "/dev/sdb1", MountPoint: "/mnt/data"},
	}
	items := []config.StoragePool{{
		ID:              "disk-data",
		Name:            "data",
		Path:            "/mnt/data/eyvescloud",
		MountPoint:      "/mnt/data",
		ContentTypes:    []string{config.StorageContentLXC},
		DefaultContents: []string{config.StorageContentLXC},
		Enabled:         true,
	}}
	pools, err := normalizeStoragePoolsRequestWithDisks(items, disks)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(filepath.Clean("/mnt/data"), "eyvescloud")
	if len(pools) != 1 || pools[0].ID != "disk-data" || pools[0].Name != "data (/dev/sdb1)" || pools[0].Path != wantPath || pools[0].MountPoint != "/mnt/data" {
		t.Fatalf("unexpected normalized pools: %#v", pools)
	}
}

func TestNormalizeStoragePoolsRejectsUncontrolledPath(t *testing.T) {
	disks := []storageDiskInfo{{Path: "/dev/sdb1", MountPoint: "/mnt/data"}}
	for _, path := range []string{"/etc", "/mnt/data/eyvescloud/../../etc", "/mnt/data/other"} {
		_, err := normalizeStoragePoolsRequestWithDisks([]config.StoragePool{{
			ID:         "disk-data",
			Name:       "data",
			Path:       path,
			MountPoint: "/mnt/data",
			Enabled:    true,
		}}, disks)
		if err == nil {
			t.Fatalf("path %q was accepted", path)
		}
	}
}

func TestDirSizeBytesUsesAllocatedBlocks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("allocated-block behavior is provided by the Linux du command")
	}
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "sparse.img"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(1 << 30); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if got := dirSizeBytes(dir); got >= 128<<20 {
		t.Fatalf("dirSizeBytes() = %d, expected allocated size instead of 1 GiB apparent size", got)
	}
}
