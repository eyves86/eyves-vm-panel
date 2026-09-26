package whmcs

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// TestFilesNotEmpty 校验模块源码已随二进制打包（go:embed 生效）。
func TestFilesNotEmpty(t *testing.T) {
	files := Files()
	if len(files) == 0 {
		t.Fatal("Files() returned no files; go:embed of module/ may have failed")
	}
	if _, err := Read("eyvescloud.php"); err != nil {
		t.Fatalf("Read(eyvescloud.php) = %v, want nil", err)
	}
}

// TestArchiveLayout 校验 zip 内的目录结构符合 WHMCS 安装约定。
func TestArchiveLayout(t *testing.T) {
	data, err := Archive()
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}

	seen := map[string]bool{}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, InstallPath+"/") {
			t.Errorf("zip entry %q does not start with %q", f.Name, InstallPath)
		}
		seen[f.Name] = true
	}
	if !seen[InstallPath+"/eyvescloud.php"] {
		t.Errorf("zip missing %s/eyvescloud.php", InstallPath)
	}
	if !seen[InstallPath+"/templates/clientarea.tpl"] {
		t.Errorf("zip missing %s/templates/clientarea.tpl", InstallPath)
	}
}