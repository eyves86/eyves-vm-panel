package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/safehttp"
)

// This file implements batch-3 network management: logical regions, IP groups
// with failover, and an ISO catalog that can be attached to KVM VMs.

// ---- helpers ----

func networkDataDir() string {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	dir := config.AppConfig.DataDir
	if dir == "" {
		dir = "/opt/eyvescloud"
	}
	return dir
}

func isoStorageDir() (string, error) {
	base := networkDataDir()
	dir := filepath.Join(base, "isos")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func newNetworkID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// ---- Regions ----

func HandleRegions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config.AppConfigMu.RLock()
		out := append([]config.Region(nil), config.AppConfig.Regions...)
		config.AppConfigMu.RUnlock()
		if out == nil {
			out = []config.Region{}
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: out})
	case http.MethodPost:
		var req struct {
			Name     string `json:"name"`
			Location string `json:"location,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "region name is required"})
			return
		}
		region := config.Region{
			ID:        newNetworkID("rg"),
			Name:      name,
			Location:  strings.TrimSpace(req.Location),
			CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
		}
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			cfg.Regions = append(cfg.Regions, region)
		})
		config.SaveConfig()
		auditRequest(r, "region.create", region.Name, fmt.Sprintf("创建区域 %s", region.Name), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: region})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func HandleRegionItem(w http.ResponseWriter, r *http.Request) {
	id := networkIDFromPath(r.URL.Path, "/api/regions/")
	if id == "" {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Not found"})
		return
	}
	if r.Method != http.MethodDelete {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	removed := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := cfg.Regions[:0]
		for _, rg := range cfg.Regions {
			if rg.ID == id {
				removed = true
				continue
			}
			out = append(out, rg)
		}
		cfg.Regions = out
	})
	if !removed {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Region not found"})
		return
	}
	config.SaveConfig()
	auditRequest(r, "region.delete", id, "删除区域", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

// ---- IP groups (分组/故障 IP) ----

// HandleIPGroupSubRoutes dispatches /api/ip-groups/{id} and
// /api/ip-groups/{id}/failover.
func HandleIPGroupSubRoutes(w http.ResponseWriter, r *http.Request) {
	callID, failover, ok := parseNetworkIDPath(r.URL.Path, "/api/ip-groups/")
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Not found"})
		return
	}
	if failover {
		HandleIPGroupFailover(w, r, callID)
		return
	}
	HandleIPGroupItem(w, r, callID)
}

func parseNetworkIDPath(path, prefix string) (id string, failover bool, ok bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", false, false
	}
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", false, false
	}
	if strings.HasSuffix(rest, "/failover") {
		return strings.TrimSuffix(rest, "/failover"), true, true
	}
	return rest, false, true
}

func networkIDFromPath(path, prefix string) string {
	id, _, ok := parseNetworkIDPath(path, prefix)
	if !ok {
		return ""
	}
	return id
}

func HandleIPGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config.AppConfigMu.RLock()
		out := append([]config.IPGroup(nil), config.AppConfig.IPGroups...)
		config.AppConfigMu.RUnlock()
		if out == nil {
			out = []config.IPGroup{}
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: out})
	case http.MethodPost:
		var req struct {
			Name    string   `json:"name"`
			Enable  []string `json:"enable"`
			Standby []string `json:"standby"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "group name is required"})
			return
		}
		for _, ip := range append(append([]string(nil), req.Enable...), req.Standby...) {
			if !validPublicIPLoose(ip) {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "invalid IP in group: " + ip})
				return
			}
		}
		group := config.IPGroup{
			ID:        newNetworkID("grp"),
			Name:      name,
			Enable:    normalizeIPList(req.Enable),
			Standby:   normalizeIPList(req.Standby),
			CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
		}
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) { cfg.IPGroups = append(cfg.IPGroups, group) })
		config.SaveConfig()
		auditRequest(r, "ipgroup.create", name, "创建 IP 组", true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: group})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func HandleIPGroupItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPut:
		var req struct {
			Name    string   `json:"name"`
			Enable  []string `json:"enable"`
			Standby []string `json:"standby"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
			return
		}
		for _, ip := range append(append([]string(nil), req.Enable...), req.Standby...) {
			if !validPublicIPLoose(ip) {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "invalid IP in group: " + ip})
				return
			}
		}
		found := false
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.IPGroups {
				if cfg.IPGroups[i].ID == id {
					if strings.TrimSpace(req.Name) != "" {
						cfg.IPGroups[i].Name = strings.TrimSpace(req.Name)
					}
					cfg.IPGroups[i].Enable = normalizeIPList(req.Enable)
					cfg.IPGroups[i].Standby = normalizeIPList(req.Standby)
					found = true
					break
				}
			}
		})
		if !found {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "IP group not found"})
			return
		}
		config.SaveConfig()
		auditRequest(r, "ipgroup.update", id, "更新 IP 组", true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true})
	case http.MethodDelete:
		found := false
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			out := cfg.IPGroups[:0]
			for _, g := range cfg.IPGroups {
				if g.ID == id {
					found = true
					continue
				}
				out = append(out, g)
			}
			cfg.IPGroups = out
		})
		if !found {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "IP group not found"})
			return
		}
		config.SaveConfig()
		auditRequest(r, "ipgroup.delete", id, "删除 IP 组", true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleIPGroupFailover performs a manual fast-failover for a grouped IP: the
// standby target is promoted to active, the current active IP is demoted to
// standby, and the change is applied to any container that referenced the
// demoted IP (best effort; a restart applies it at runtime).
func HandleIPGroupFailover(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		IP string `json:"ip"` // standby IP to promote
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
		return
	}
	target := strings.TrimSpace(req.IP)
	if target == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "ip is required"})
		return
	}
	var group *config.IPGroup
	var demoted string
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.IPGroups {
			if cfg.IPGroups[i].ID != id {
				continue
			}
			g := &cfg.IPGroups[i]
			inStandby := containsStr(g.Standby, target)
			if !inStandby {
				return
			}
			// demote the current first active if any
			if len(g.Enable) > 0 {
				demoted = g.Enable[0]
				g.Enable = g.Enable[1:]
			}
			// promote target
			g.Standby = removeStr(g.Standby, target)
			g.Enable = append(g.Enable, target)
			if demoted != "" && demoted != target {
				g.Standby = append(g.Standby, demoted)
			}
			g.FaultOpen = target
			c := *g
			group = &c
			return
		}
	})
	if group == nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "IP group not found or the IP is not in standby"})
		return
	}
	// Best-effort: containers referencing the demoted IP now carry the new active IP.
	if demoted != "" && demoted != target {
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for k := range cfg.Containers {
				c := &cfg.Containers[k]
				for j := range c.PublicIPv4s {
					if strings.EqualFold(c.PublicIPv4s[j].Address, demoted) {
						c.PublicIPv4s[j].Address = target
					}
				}
			}
		})
	}
	config.SaveConfig()
	auditRequest(r, "ipgroup.failover", group.Name, fmt.Sprintf("故障切换 IP %s", target), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: group})
}

func validPublicIPLoose(v string) bool {
	return strings.TrimSpace(v) != ""
}
func normalizeIPList(in []string) []string {
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}
func containsStr(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}
func removeStr(list []string, v string) []string {
	out := list[:0:0]
	out = append(out, list...)
	res := out[:0]
	for _, x := range out {
		if !strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(v)) {
			res = append(res, x)
		}
	}
	return res
}

// ---- ISO catalog ----

func HandleISOs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config.AppConfigMu.RLock()
		out := append([]config.ISOFile(nil), config.AppConfig.ISOFiles...)
		config.AppConfigMu.RUnlock()
		if out == nil {
			out = []config.ISOFile{}
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: out})
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			OS   string `json:"os,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "name is required"})
			return
		}
		if len(req.URL) > 4096 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "url must not exceed 4096 characters"})
			return
		}
		if _, err := safehttp.ValidateURL(req.URL); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		isoID := newNetworkID("iso")
		dir, err := isoStorageDir()
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		target := filepath.Join(dir, isoID+".iso")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		resp, err := safehttp.Get(ctx, req.URL, "EyvesCloud/1.0 ISO downloader", 30*time.Minute)
		if err != nil {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "download failed: " + err.Error()})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: fmt.Sprintf("download returned HTTP %d", resp.StatusCode)})
			return
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
			return
		}
		written, copyErr := io.Copy(f, io.LimitReader(resp.Body, 20*1024*1024*1024))
		f.Close()
		if copyErr != nil {
			_ = os.Remove(target)
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "write failed: " + copyErr.Error()})
			return
		}
		if written == 0 {
			_ = os.Remove(target)
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "downloaded ISO is empty"})
			return
		}
		iso := config.ISOFile{
			ID:        isoID,
			Name:      name,
			Path:      target,
			SizeBytes: written,
			OS:        strings.TrimSpace(req.OS),
			CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
		}
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) { cfg.ISOFiles = append(cfg.ISOFiles, iso) })
		config.SaveConfig()
		auditRequest(r, "iso.create", name, "添加 ISO 镜像", true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: iso})
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func HandleISOItem(w http.ResponseWriter, r *http.Request) {
	id := networkIDFromPath(r.URL.Path, "/api/isos/")
	if id == "" || id == "attach" || id == "upload" {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Not found"})
		return
	}
	if r.Method != http.MethodDelete {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var removedPath string
	found := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := cfg.ISOFiles[:0]
		for _, iso := range cfg.ISOFiles {
			if iso.ID == id {
				found = true
				removedPath = iso.Path
				continue
			}
			out = append(out, iso)
		}
		cfg.ISOFiles = out
	})
	if !found {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "ISO not found"})
		return
	}
	if removedPath != "" {
		base, _ := isoStorageDir()
		if abs, err := filepath.Abs(removedPath); err == nil && strings.HasPrefix(abs, base+string(os.PathSeparator)) {
			_ = os.Remove(removedPath)
		}
	}
	config.SaveConfig()
	auditRequest(r, "iso.delete", id, "删除 ISO 镜像", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

// HandleISOUpload 接收用户本地上传的 ISO 文件（multipart/form-data：
// 字段 name/os 可选，file 必填），流式写盘到 isos 目录并登记到 ISO 目录。
// 与 URL 在线下载（HandleISOs POST）互补，走独立端点以放宽请求体上限。
func HandleISOUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil { // 内存阈值，超大部分落临时文件
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid multipart form: " + err.Error()})
		return
	}
	defer r.MultipartForm.RemoveAll()

	displayName := strings.TrimSpace(r.FormValue("name"))
	osName := strings.TrimSpace(r.FormValue("os"))
	file, header, err := r.FormFile("file")
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "file field is required: " + err.Error()})
		return
	}
	defer file.Close()

	if displayName == "" {
		displayName = header.Filename
	}
	if displayName == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "name is required"})
		return
	}
	if len(displayName) > 512 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "name must not exceed 512 characters"})
		return
	}
	// 仅允许常见镜像扩展名，防止伪装文件。
	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".iso", ".img", ".qcow2", ".vhd", ".tar", ".gz", ".zip":
	default:
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "unsupported file type: " + ext})
		return
	}

	isoID := newNetworkID("iso")
	dir, err := isoStorageDir()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	target := filepath.Join(dir, isoID+ext)
	if err := safeInstanceBackupStorePath(target); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	written, copyErr := io.Copy(f, io.LimitReader(file, 20*1024*1024*1024))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(target)
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "write failed: " + copyErr.Error()})
		return
	}
	if closeErr != nil {
		_ = os.Remove(target)
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "close failed: " + closeErr.Error()})
		return
	}
	if written == 0 {
		_ = os.Remove(target)
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "uploaded ISO is empty"})
		return
	}
	iso := config.ISOFile{
		ID:        isoID,
		Name:      displayName,
		Path:      target,
		SizeBytes: written,
		OS:        osName,
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) { cfg.ISOFiles = append(cfg.ISOFiles, iso) })
	config.SaveConfig()
	auditRequest(r, "iso.upload", iso.Name, "本地上传 ISO 镜像", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: iso})
}

// HandleContainerISOAction attaches or detaches an ISO to/from a KVM VM (libvirt).
// Request: {"container_id": int, "iso_id": "iso-...", "attach": bool}.
func HandleContainerISOAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		ContainerID int    `json:"container_id"`
		ISOID       string `json:"iso_id"`
		Attach      bool   `json:"attach"` // false = detach
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request"})
		return
	}
	c := config.FindContainer(req.ContainerID)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	if !c.IsKVM() {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "ISO attach is only supported for KVM VMs"})
		return
	}
	vm := c.VirshName()
	if req.Attach {
		var iso *config.ISOFile
		config.AppConfigMu.RLock()
		for i := range config.AppConfig.ISOFiles {
			if config.AppConfig.ISOFiles[i].ID == req.ISOID {
				v := config.AppConfig.ISOFiles[i]
				iso = &v
				break
			}
		}
		config.AppConfigMu.RUnlock()
		if iso == nil || iso.Path == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "ISO not found"})
			return
		}
		if out, err := exec.Command("virsh", "attach-disk", vm, iso.Path, "sdb",
			"--type", "cdrom", "--mode", "readonly").CombinedOutput(); err != nil {
			jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "attach failed: " + err.Error() + ": " + string(out)})
			return
		}
		auditRequest(r, "container.iso_attach", c.Name, "挂载 ISO "+iso.Name, true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "ISO attached"})
		return
	}
	if out, err := exec.Command("virsh", "detach-disk", vm, "sdb").CombinedOutput(); err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "detach failed: " + err.Error() + ": " + string(out)})
		return
	}
	auditRequest(r, "container.iso_detach", c.Name, "卸载 ISO", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "ISO detached"})
}
