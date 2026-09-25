package api

import (
	"fmt"
	"net/http"

	"eyvescloud/internal/integrations/whmcs"
	"eyvescloud/internal/version"
)

// HandleWHMCSModule 返回内置 WHMCS 服务器模块的元信息：版本、安装路径、文件清单与 README。
// 仅管理员可访问，供后台「API 集成」页展示与下载。
func HandleWHMCSModule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"name":          whmcs.ModuleName,
		"display_name":  whmcs.DisplayName,
		"version":       whmcs.Version,
		"install_path":  whmcs.InstallPath,
		"panel_version": version.Current(),
		"files":         whmcs.Files(),
		"readme":        whmcs.Readme(),
	}})
}

// HandleWHMCSDownload 把内置 WHMCS 模块打包为 zip 供下载。
// zip 内顶层目录为 modules/servers/eyvescloud/，解压到 WHMCS 根目录即可安装。
func HandleWHMCSDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	data, err := whmcs.Archive()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "打包 WHMCS 模块失败: " + err.Error()})
		return
	}

	filename := fmt.Sprintf("eyvescloud-whmcs-module-%s.zip", whmcs.Version)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}