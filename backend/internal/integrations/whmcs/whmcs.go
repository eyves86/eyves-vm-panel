// Package whmcs 内置 WHMCS 9.0 服务器开通模块（eyvescloud）的源码，
// 供管理员从面板直接下载 zip 后解压到 WHMCS 根目录完成安装。
//
// 模块源码以单一来源保存在本包的 module/ 目录，通过 go:embed 编入二进制，
// 因此发布包无需额外附带 PHP 文件即可提供下载。
package whmcs

import (
	"archive/zip"
	"bytes"
	"embed"
	"io/fs"
	"sort"
	"strings"
	"time"
)

//go:embed module
var moduleFS embed.FS

const (
	// ModuleName 是 WHMCS 模块名（也是模块目录名）。
	ModuleName = "eyvescloud"
	// DisplayName 是展示给管理员的模块名称。
	DisplayName = "EYVESCLOUD WHMCS Server Module"
	// Version 是模块版本（与主程序大版本对齐）。
	Version = "2.0.0"
	// InstallPath 是解压后模块在 WHMCS 中的相对路径。
	InstallPath = "modules/servers/eyvescloud"
)

// File 描述模块内的一个文件。
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Files 返回模块内文件清单（按路径升序）。
func Files() []File {
	out := make([]File, 0, 16)
	_ = fs.WalkDir(moduleFS, "module", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		out = append(out, File{
			Path: strings.TrimPrefix(p, "module/"),
			Size: info.Size(),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Read 读取模块内的单个文件（rel 为相对 module/ 的路径）。
func Read(rel string) ([]byte, error) {
	return moduleFS.ReadFile("module/" + strings.TrimPrefix(rel, "/"))
}

// Readme 返回模块 README 文本；不存在时返回空字符串。
func Readme() string {
	data, err := Read("README.md")
	if err != nil {
		return ""
	}
	return string(data)
}

// Archive 生成可直接解压到 WHMCS 根目录的 zip 归档。
// 归档内的顶层目录固定为 modules/servers/eyvescloud/，与 WHMCS 约定一致。
func Archive() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	walkErr := fs.WalkDir(moduleFS, "module", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, "module/")
		data, rerr := moduleFS.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		header := &zip.FileHeader{
			Name:     InstallPath + "/" + rel,
			Method:   zip.Deflate,
			Modified: time.Now(),
		}
		w, cerr := zw.CreateHeader(header)
		if cerr != nil {
			return cerr
		}
		_, werr := w.Write(data)
		return werr
	})
	if walkErr != nil {
		_ = zw.Close()
		return nil, walkErr
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}