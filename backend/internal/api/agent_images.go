package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/safehttp"
)

// 被控（Agent）镜像同步 API。
//
// 主控把「自定义镜像清单」下发给被控，被控按需拉取并做 SHA256 校验，
// 从而让主控成为统一镜像源，被控无需各自手工录入镜像元数据。

// agentImageCatalog 是被控端返回/接收的镜像清单结构。
type agentImageCatalog struct {
	LXC []config.CustomLXCImage `json:"lxc"`
	KVM []config.CustomKVMImage `json:"kvm"`
}

func currentAgentImageCatalog() agentImageCatalog {
	return agentImageCatalog{
		LXC: config.ListCustomLXCImages(),
		KVM: config.ListCustomKVMImages(),
	}
}

// HandleAgentImages 返回被控当前的镜像清单（供主控查看差异）。
func HandleAgentImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: currentAgentImageCatalog()})
}

// HandleAgentImageSync 接收主控下发的镜像清单，对比 SHA256 后：
//   - 缺失或哈希不一致的自定义镜像元数据被写入被控配置；
//   - 若镜像 URL 指向本被控尚缺的远端自定义镜像文件，则尝试拉取到本地缓存。
func HandleAgentImageSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var catalog agentImageCatalog
	if err := json.NewDecoder(r.Body).Decode(&catalog); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid image catalog: " + err.Error()})
		return
	}

	added := 0
	updated := 0
	for _, img := range catalog.LXC {
		existing := config.FindCustomLXCImage(img.ID)
		if existing == nil {
			_ = config.AddCustomLXCImage(img)
			added++
			continue
		}
		if existing.SHA256 != "" && img.SHA256 != "" && existing.SHA256 != img.SHA256 {
			_, _ = config.RemoveCustomLXCImage(img.ID)
			_ = config.AddCustomLXCImage(img)
			updated++
		}
	}
	for _, img := range catalog.KVM {
		existing := config.FindCustomKVMImage(img.ID)
		if existing == nil {
			_ = config.AddCustomKVMImage(img)
			added++
			continue
		}
		if existing.SHA256 != "" && img.SHA256 != "" && existing.SHA256 != img.SHA256 {
			_, _ = config.RemoveCustomKVMImage(img.ID)
			_ = config.AddCustomKVMImage(img)
			updated++
		}
	}

	// 拉取纳入统一镜像管理的远端镜像文件（尽力而为，失败不阻断同步）。
	var pulled, failed []string
	for _, img := range catalog.LXC {
		if img.URL == "" || img.SHA256 == "" {
			continue
		}
		dest := customLXCCachePath(img)
		if cacheFileValid(dest, img.SHA256) {
			continue
		}
		if err := downloadImageFile(img.URL, dest); err != nil {
			failed = append(failed, img.ID+": "+err.Error())
			continue
		}
		if fileSHA256(dest) != img.SHA256 {
			failed = append(failed, img.ID+": sha256 mismatch")
			_ = os.Remove(dest)
			continue
		}
		pulled = append(pulled, img.ID)
	}
	for _, img := range catalog.KVM {
		if img.URL == "" || img.SHA256 == "" {
			continue
		}
		dest := customKVMCachePath(img)
		if cacheFileValid(dest, img.SHA256) {
			continue
		}
		if err := downloadImageFile(img.URL, dest); err != nil {
			failed = append(failed, img.ID+": "+err.Error())
			continue
		}
		if fileSHA256(dest) != img.SHA256 {
			failed = append(failed, img.ID+": sha256 mismatch")
			_ = os.Remove(dest)
			continue
		}
		pulled = append(pulled, img.ID)
	}

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"added":   added,
		"updated": updated,
		"pulled":  pulled,
		"failed":  failed,
	}})
}

// reconcileImageCatalog 纯函数：计算将 incoming 清单应用到本地后「新增/更新」的镜像数量。
// 便于单元测试；实际写库逻辑在 HandleAgentImageSync 内闭环。
func reconcileImageCatalog(incoming agentImageCatalog) (added, updated int) {
	for _, img := range incoming.LXC {
		existing := config.FindCustomLXCImage(img.ID)
		if existing == nil {
			added++
			continue
		}
		if existing.SHA256 != "" && img.SHA256 != "" && existing.SHA256 != img.SHA256 {
			updated++
		}
	}
	for _, img := range incoming.KVM {
		existing := config.FindCustomKVMImage(img.ID)
		if existing == nil {
			added++
			continue
		}
		if existing.SHA256 != "" && img.SHA256 != "" && existing.SHA256 != img.SHA256 {
			updated++
		}
	}
	return added, updated
}

func customLXCCachePath(img config.CustomLXCImage) string {
	dir := "/var/cache/eyvescloud/images/lxc"
	return filepath.Join(dir, img.ID+".tar")
}

func customKVMCachePath(img config.CustomKVMImage) string {
	dir := "/var/cache/eyvescloud/images/kvm"
	return filepath.Join(dir, img.ID+".qcow2")
}

// cacheFileValid 校验本地缓存文件存在且（若给定 SHA256）内容匹配。
func cacheFileValid(path, sha string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false
	}
	if sha == "" {
		return true
	}
	return fileSHA256(path) == sha
}

func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func downloadImageFile(url, dest string) error {
	if _, err := safehttp.ValidateURL(url); err != nil {
		return err
	}
	if dir := filepath.Dir(dest); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	resp, err := safehttp.Get(context.Background(), url, "EyvesCloud/1.0 agent image sync", 60*time.Minute)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp := dest + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dest)
}