package api

// apiv2_catalog.go —— API v2：镜像 / ISO / 存储池 / SSH 密钥 / 安全组与规则 / IP 池。
//
// 端点：
//
//	GET    /api/v2/images                  镜像目录（LXC 模板 + KVM 镜像 + 自定义源）
//	GET    /api/v2/images/{id}             镜像详情（下载状态/启用状态/大小）
//	POST   /api/v2/images/{id}/download    触发下载（异步，返回任务/队列信息）
//	PATCH  /api/v2/images/{id}             启用/停用镜像 {enabled}
//	POST   /api/v2/images                  新增自定义镜像源（URL + 可选 SHA256）
//	DELETE /api/v2/images/{id}             删除自定义镜像源（内置模板不可删）
//	GET    /api/v2/iso-images               ISO 目录
//	DELETE /api/v2/iso-images/{id}          删除 ISO（同时清理文件）
//	GET    /api/v2/storage-pools            存储池列表（含内容类型与启用状态）
//	GET    /api/v2/storage-pools/{id}       存储池详情（含路径可用性）
//	GET    /api/v2/ssh-keys                 SSH 公钥列表（按归属过滤）
//	POST   /api/v2/ssh-keys                 录入公钥（服务端计算指纹）
//	DELETE /api/v2/ssh-keys/{id}            删除公钥
//	GET    /api/v2/security-groups          安全组列表
//	POST   /api/v2/security-groups          创建安全组
//	GET    /api/v2/security-groups/{id}     安全组详情（含规则）
//	PATCH  /api/v2/security-groups/{id}     修改安全组（名称/默认动作）
//	DELETE /api/v2/security-groups/{id}     删除安全组（连同规则）
//	GET    /api/v2/security-groups/{id}/rules      规则列表
//	POST   /api/v2/security-groups/{id}/rules      新增规则
//	DELETE /api/v2/security-groups/{id}/rules/{rid} 删除规则
//	GET    /api/v2/ip-pools                 公网 IPv4 池 + IPv6 前缀
//	POST   /api/v2/ip-pools                 向池中添加地址/前缀
//	DELETE /api/v2/ip-pools                 从池中移除地址/前缀


import (
	"runtime"
	"strconv"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/kvm"
	"eyvescloud/internal/lxc"
	"eyvescloud/internal/secgroup"

	"golang.org/x/crypto/ssh"
)

func init() {
	registerV2("GET /api/v2/images", v2Auth(v2ImagesList))
	registerV2("POST /api/v2/images", v2Auth(v2ImagesCreate))
	registerV2("GET /api/v2/images/{id}", v2Auth(v2ImageGet))
	registerV2("PATCH /api/v2/images/{id}", v2Auth(v2ImageUpdate))
	registerV2("DELETE /api/v2/images/{id}", v2Auth(v2ImageDelete))
	registerV2("POST /api/v2/images/{id}/download", v2Auth(v2ImageDownload))

	registerV2("GET /api/v2/iso-images", v2Auth(v2ISOList))
	registerV2("DELETE /api/v2/iso-images/{id}", v2Auth(v2ISODelete))

	registerV2("GET /api/v2/storage-pools", v2Auth(v2StoragePoolsList))
	registerV2("GET /api/v2/storage-pools/{id}", v2Auth(v2StoragePoolGet))

	registerV2("GET /api/v2/ssh-keys", v2Auth(v2SSHKeysList))
	registerV2("POST /api/v2/ssh-keys", v2Auth(v2SSHKeysCreate))
	registerV2("DELETE /api/v2/ssh-keys/{id}", v2Auth(v2SSHKeysDelete))

	registerV2("GET /api/v2/security-groups", v2Auth(v2SecGroupsList))
	registerV2("POST /api/v2/security-groups", v2Auth(v2SecGroupsCreate))
	registerV2("GET /api/v2/security-groups/{id}", v2Auth(v2SecGroupGet))
	registerV2("PATCH /api/v2/security-groups/{id}", v2Auth(v2SecGroupUpdate))
	registerV2("DELETE /api/v2/security-groups/{id}", v2Auth(v2SecGroupDelete))
	registerV2("GET /api/v2/security-groups/{id}/rules", v2Auth(v2SecGroupRulesList))
	registerV2("POST /api/v2/security-groups/{id}/rules", v2Auth(v2SecGroupRuleCreate))
	registerV2("DELETE /api/v2/security-groups/{id}/rules/{rid}", v2Auth(v2SecGroupRuleDelete))

	registerV2("GET /api/v2/ip-pools", v2Auth(v2IPPoolsList))
	registerV2("POST /api/v2/ip-pools", v2Auth(v2IPPoolsAdd))
	registerV2("DELETE /api/v2/ip-pools", v2Auth(v2IPPoolsRemove))
}

// ---------------------------------------------------------------------------
// 镜像
// ---------------------------------------------------------------------------

// v2ImageView 统一的镜像视图（内置模板 / KVM 镜像 / 自定义源共用）。
func v2ImageView(id, name, runtime, distro, release, arch, description string,
	downloaded bool, enabled bool, sizeBytes int64, custom bool, sha256 string, url string) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "name": name, "runtime": runtime, "distro": distro,
		"release": release, "arch": arch, "description": description,
		"downloaded": downloaded, "enabled": enabled, "size_bytes": sizeBytes,
		"custom": custom, "sha256": sha256, "url": url,
	}
}

func v2ImagesList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "image:read") {
		return
	}
	query := v2ParsePage(r)
	params := r.URL.Query()
	runtimeFilter := strings.ToLower(strings.TrimSpace(params.Get("runtime")))
	enabledSet := getEnabledImageSet()

	items := []map[string]interface{}{}
	for _, t := range lxc.GetTemplates() {
		if runtimeFilter != "" && runtimeFilter != "lxc" {
			continue
		}
		downloaded, size := lxcTemplateDownloadedInfo(t)
		items = append(items, v2ImageView(t.ID, t.Name, "lxc", t.Distro, t.Release, t.Arch,
			t.Description, downloaded, enabledSet[t.ID], size, t.Custom, t.SHA256, t.URL))
	}
	for _, img := range kvm.GetImages() {
		if runtimeFilter != "" && runtimeFilter != "kvm" {
			continue
		}
		downloaded, size := kvm.ImageDownloadedInfo(img.ID)
		items = append(items, v2ImageView(img.ID, img.Name, "kvm", img.Distro, img.Release, img.Arch,
			img.Description, downloaded, enabledSet[img.ID], size, img.Custom, img.SHA256, img.URL))
	}
	if keyword := strings.ToLower(query.Search); keyword != "" {
		filtered := items[:0]
		for _, item := range items {
			if strings.Contains(strings.ToLower(item["id"].(string)), keyword) ||
				strings.Contains(strings.ToLower(item["name"].(string)), keyword) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2ImageGet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "image:read") {
		return
	}
	imageID := strings.TrimSpace(r.PathValue("id"))
	enabledSet := getEnabledImageSet()
	if t := lxc.FindTemplate(imageID); t != nil {
		downloaded, size := lxcTemplateDownloadedInfo(*t)
		v2OK(w, r, v2ImageView(t.ID, t.Name, "lxc", t.Distro, t.Release, t.Arch, t.Description,
			downloaded, enabledSet[t.ID], size, t.Custom, t.SHA256, t.URL))
		return
	}
	if img := kvm.FindImage(imageID); img != nil {
		downloaded, size := kvm.ImageDownloadedInfo(img.ID)
		v2OK(w, r, v2ImageView(img.ID, img.Name, "kvm", img.Distro, img.Release, img.Arch, img.Description,
			downloaded, enabledSet[img.ID], size, img.Custom, img.SHA256, img.URL))
		return
	}
	v2NotFound(w, r, "镜像不存在："+imageID)
}

// v2ImageUpdate PATCH /images/{id} {enabled}
func v2ImageUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	imageID := strings.TrimSpace(r.PathValue("id"))
	if lxc.FindTemplate(imageID) == nil && kvm.FindImage(imageID) == nil {
		v2NotFound(w, r, "镜像不存在："+imageID)
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if req.Enabled == nil {
		v2BadRequest(w, r, "缺少必填字段", map[string]string{"enabled": "true/false"})
		return
	}
	if *req.Enabled {
		ensureImageEnabled(imageID)
	} else {
		removeImageEnabled(imageID)
	}
	auditRequest(r, "api.v2.image.update", imageID, "enabled 变更", true, "")
	v2OK(w, r, map[string]interface{}{"id": imageID, "enabled": *req.Enabled})
}

// v2ImageDownload POST /images/{id}/download：触发镜像下载（异步队列）。
func v2ImageDownload(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "image:download") {
		return
	}
	imageID := strings.TrimSpace(r.PathValue("id"))
	if t := lxc.FindTemplate(imageID); t != nil {
		queued, started := enqueueLXCImageDownload(*t)
		auditRequest(r, "api.v2.image.download", imageID, "触发下载", true, "")
		v2Accepted(w, r, map[string]interface{}{"id": imageID, "runtime": "lxc", "queued": queued, "started": started})
		return
	}
	if img := kvm.FindImage(imageID); img != nil {
		if err := startKVMImageDownloadV2(*img); err != nil {
			v2Internal(w, r, "启动下载失败："+err.Error())
			return
		}
		auditRequest(r, "api.v2.image.download", imageID, "触发下载", true, "")
		v2Accepted(w, r, map[string]interface{}{"id": imageID, "runtime": "kvm", "started": true})
		return
	}
	v2NotFound(w, r, "镜像不存在："+imageID)
}

// v2ImagesCreate POST /images：新增自定义镜像源（LXC rootfs 或 KVM 磁盘镜像）。
func v2ImagesCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Runtime  string `json:"runtime"`
		URL      string `json:"url"`
		SHA256   string `json:"sha256"`
		Distro   string `json:"distro"`
		Release  string `json:"release"`
		Arch     string `json:"arch"`
		Filename string `json:"filename"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name, "url": req.URL}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	runtime := strings.ToLower(strings.TrimSpace(req.Runtime))
	if runtime == "" {
		runtime = "lxc"
	}
	if runtime != "lxc" && runtime != "kvm" {
		v2BadRequest(w, r, "runtime 只能是 lxc 或 kvm", map[string]string{"runtime": runtime})
		return
	}
	arch := strings.TrimSpace(req.Arch)
	if arch == "" {
		arch = runtimeGOARCHValue()
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	if runtime == "lxc" {
		image := config.CustomLXCImage{
			ID:          "custom-" + randomHex(6),
			Name:        strings.TrimSpace(req.Name),
			URL:         strings.TrimSpace(req.URL),
			SHA256:      strings.ToLower(strings.TrimSpace(req.SHA256)),
			Distro:      strings.TrimSpace(req.Distro),
			Release:     strings.TrimSpace(req.Release),
			Arch:        arch,
			Description: "自定义 LXC rootfs",
			CreatedAt:   now,
		}
		if err := config.AddCustomLXCImage(image); err != nil {
			v2BadRequest(w, r, "新增失败："+err.Error(), nil)
			return
		}
		auditRequest(r, "api.v2.image.create", image.ID, image.URL, true, "")
		v2Created(w, r, map[string]interface{}{"id": image.ID, "name": image.Name, "runtime": "lxc"})
		return
	}
	image := config.CustomKVMImage{
		ID:          "custom-" + randomHex(6),
		Name:        strings.TrimSpace(req.Name),
		URL:         strings.TrimSpace(req.URL),
		SHA256:      strings.ToLower(strings.TrimSpace(req.SHA256)),
		Distro:      strings.TrimSpace(req.Distro),
		Release:     strings.TrimSpace(req.Release),
		Arch:        arch,
		Provisioner: strings.TrimSpace(req.Filename),
		Description: "自定义 KVM 镜像",
		CreatedAt:   now,
	}
	if err := config.AddCustomKVMImage(image); err != nil {
		v2BadRequest(w, r, "新增失败："+err.Error(), nil)
		return
	}
	auditRequest(r, "api.v2.image.create", image.ID, image.URL, true, "")
	v2Created(w, r, map[string]interface{}{"id": image.ID, "name": image.Name, "runtime": "kvm"})
}

// v2ImageDelete DELETE /images/{id}：仅允许删除自定义镜像源。
func v2ImageDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	imageID := strings.TrimSpace(r.PathValue("id"))
	if removed, err := config.RemoveCustomLXCImage(imageID); err == nil && removed {
		auditRequest(r, "api.v2.image.delete", imageID, "删除自定义 LXC 镜像", true, "")
		v2NoContent(w, r)
		return
	}
	if removed, err := config.RemoveCustomKVMImage(imageID); err == nil && removed {
		auditRequest(r, "api.v2.image.delete", imageID, "删除自定义 KVM 镜像", true, "")
		v2NoContent(w, r)
		return
	}
	if lxc.FindTemplate(imageID) != nil || kvm.FindImage(imageID) != nil {
		v2Precondition(w, r, "内置镜像不可删除（可仅停用：PATCH /images/{id} {enabled:false}）")
		return
	}
	v2NotFound(w, r, "镜像不存在："+imageID)
}

// ---------------------------------------------------------------------------
// ISO
// ---------------------------------------------------------------------------

func v2ISOList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "image:read") {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	files := append([]config.ISOFile(nil), config.AppConfig.ISOFiles...)
	config.AppConfigMu.RUnlock()
	items := make([]map[string]interface{}, 0, len(files))
	for _, iso := range files {
		items = append(items, map[string]interface{}{
			"id": iso.ID, "name": iso.Name, "path": iso.Path, "os": iso.OS,
			"size_bytes": iso.SizeBytes, "created_at": v2Time(iso.CreatedAt),
			"available": iso.Path == "" || fileExistsV2(iso.Path),
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func fileExistsV2(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func v2ISODelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	isoID := strings.TrimSpace(r.PathValue("id"))
	var removed *config.ISOFile
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := cfg.ISOFiles[:0]
		for i := range cfg.ISOFiles {
			if cfg.ISOFiles[i].ID == isoID {
				copyISO := cfg.ISOFiles[i]
				removed = &copyISO
				continue
			}
			out = append(out, cfg.ISOFiles[i])
		}
		cfg.ISOFiles = out
	})
	if removed == nil {
		v2NotFound(w, r, "ISO 不存在："+isoID)
		return
	}
	if removed.Path != "" {
		_ = os.Remove(removed.Path)
	}
	auditRequest(r, "api.v2.iso.delete", removed.Name, "删除 ISO", true, "")
	v2NoContent(w, r)
}

// ---------------------------------------------------------------------------
// 存储池
// ---------------------------------------------------------------------------

func v2StoragePoolsList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:read") {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	pools := append([]config.StoragePool(nil), config.AppConfig.StoragePools...)
	config.AppConfigMu.RUnlock()
	items := make([]map[string]interface{}, 0, len(pools))
	for _, pool := range pools {
		items = append(items, map[string]interface{}{
			"id": pool.ID, "name": pool.Name, "path": pool.Path, "backend": pool.Backend,
			"enabled": pool.Enabled, "shared": pool.Shared,
			"content_types": pool.ContentTypes,
			"available":     dirExistsV2(pool.Path),
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func dirExistsV2(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func v2StoragePoolGet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "node:read") {
		return
	}
	poolID := strings.TrimSpace(r.PathValue("id"))
	pool := config.StoragePoolByID(poolID)
	if pool == nil {
		v2NotFound(w, r, "存储池不存在："+poolID)
		return
	}
	view := map[string]interface{}{
		"id": pool.ID, "name": pool.Name, "path": pool.Path, "mount_point": pool.MountPoint,
		"backend": pool.Backend, "enabled": pool.Enabled, "shared": pool.Shared,
		"content_types": pool.ContentTypes, "default_contents": pool.DefaultContents,
		"available": dirExistsV2(pool.Path),
	}
	if stat, err := os.Stat(pool.Path); err == nil {
		view["path_is_dir"] = stat.IsDir()
	}
	v2OK(w, r, view)
}

// ---------------------------------------------------------------------------
// SSH 密钥
// ---------------------------------------------------------------------------

func v2SSHKeysList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:ssh-key") {
		return
	}
	ctx := v2AuthContext(r)
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	keys := append([]config.SSHKey(nil), config.AppConfig.SSHKeys...)
	config.AppConfigMu.RUnlock()
	items := make([]map[string]interface{}, 0, len(keys))
	for _, key := range keys {
		// 子用户只能看到自己的密钥；管理员看到全部。
		if ctx.Type != authTypeAdmin && key.Type != ctx.Username && key.OwnerID != "" && key.OwnerID != ctx.Username {
			continue
		}
		items = append(items, map[string]interface{}{
			"id": key.ID, "name": key.Name, "fingerprint": key.Fingerprint,
			"type": key.Type, "owner_id": key.OwnerID,
			"created_at": v2Time(key.CreatedAt), "last_used_at": v2Time(key.LastUsedAt),
			"public_key": key.PublicKey,
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2SSHKeysCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:ssh-key") {
		return
	}
	var req struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name, "public_key": req.PublicKey}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(req.PublicKey)))
	if err != nil {
		v2BadRequest(w, r, "公钥格式不合法："+err.Error(), map[string]string{"public_key": "需为单行 OpenSSH 公钥"})
		return
	}
	fingerprint := ssh.FingerprintSHA256(publicKey)
	ctx := v2AuthContext(r)
	key := config.SSHKey{
		ID:          "sk-" + randomHex(6),
		Name:        strings.TrimSpace(req.Name),
		PublicKey:   strings.TrimSpace(req.PublicKey),
		Fingerprint: fingerprint,
		Type:        ctx.Username,
		OwnerID:     ctx.Username,
		CreatedAt:   time.Now().Format("2006-01-02 15:04:05"),
	}
	if ctx.Type == authTypeAdmin {
		key.Type = "admin"
		key.OwnerID = ""
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.SSHKeys = append(cfg.SSHKeys, key)
	})
	auditRequest(r, "api.v2.ssh_key.create", key.Name, key.Fingerprint, true, "")
	v2Created(w, r, map[string]interface{}{
		"id": key.ID, "name": key.Name, "fingerprint": key.Fingerprint, "type": key.Type,
	})
}

func v2SSHKeysDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:ssh-key") {
		return
	}
	keyID := strings.TrimSpace(r.PathValue("id"))
	ctx := v2AuthContext(r)
	removed := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := cfg.SSHKeys[:0]
		for i := range cfg.SSHKeys {
			if cfg.SSHKeys[i].ID != keyID {
				out = append(out, cfg.SSHKeys[i])
				continue
			}
			if ctx.Type == authTypeAdmin || cfg.SSHKeys[i].OwnerID == ctx.Username || cfg.SSHKeys[i].Type == ctx.Username {
				removed = true
				continue
			}
			out = append(out, cfg.SSHKeys[i])
		}
		cfg.SSHKeys = out
	})
	if !removed {
		v2NotFound(w, r, "SSH 密钥不存在或无权删除："+keyID)
		return
	}
	auditRequest(r, "api.v2.ssh_key.delete", keyID, "", true, "")
	v2NoContent(w, r)
}

// ---------------------------------------------------------------------------
// 安全组与规则
// ---------------------------------------------------------------------------

func v2SecGroupsList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "security:read") {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	groups := append([]secgroup.Group(nil), config.AppConfig.SecGroups...)
	config.AppConfigMu.RUnlock()
	items := make([]map[string]interface{}, 0, len(groups))
	for _, group := range groups {
		rules := findSecGroupRules(group.ID)
		items = append(items, map[string]interface{}{
			"id": group.ID, "name": group.Name, "tenant_id": group.TenantID,
			"default_action": string(group.DefaultAction), "rule_count": len(rules),
		})
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2SecGroupsCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "security:write") {
		return
	}
	var req struct {
		Name          string `json:"name"`
		DefaultAction string `json:"default_action"`
		TenantID      string `json:"tenant_id"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	action := secgroup.Action(strings.ToLower(strings.TrimSpace(req.DefaultAction)))
	if action == "" {
		action = secgroup.ActionDrop
	}
	if action != secgroup.ActionAccept && action != secgroup.ActionDrop {
		v2BadRequest(w, r, "default_action 只能是 accept 或 drop", map[string]string{"default_action": req.DefaultAction})
		return
	}
	group := secgroup.Group{
		ID:            "sg-" + randomHex(6),
		Name:          strings.TrimSpace(req.Name),
		TenantID:      strings.TrimSpace(req.TenantID),
		DefaultAction: action,
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.SecGroups = append(cfg.SecGroups, group)
	})
	auditRequest(r, "api.v2.security_group.create", group.Name, "", true, "")
	v2Created(w, r, map[string]interface{}{"id": group.ID, "name": group.Name, "default_action": string(group.DefaultAction)})
}

func v2SecGroupGet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "security:read") {
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	group, ok := findSecGroup(groupID)
	if !ok {
		v2NotFound(w, r, "安全组不存在："+groupID)
		return
	}
	rules := findSecGroupRules(groupID)
	ruleViews := make([]map[string]interface{}, 0, len(rules))
	for _, rule := range rules {
		ruleViews = append(ruleViews, v2SecRuleView(rule))
	}
	v2OK(w, r, map[string]interface{}{
		"id": group.ID, "name": group.Name, "tenant_id": group.TenantID,
		"default_action": string(group.DefaultAction), "rules": ruleViews,
	})
}

func v2SecRuleView(rule secgroup.Rule) map[string]interface{} {
	return map[string]interface{}{
		"id": rule.ID, "group_id": rule.GroupID, "direction": string(rule.Direction),
		"protocol": string(rule.Protocol), "src_mask": rule.SrcMask, "dst_mask": rule.DstMask,
		"src_port": rule.SrcPort, "dst_port": rule.DstPort,
		"action": string(rule.Action), "priority": rule.Priority,
	}
}

func v2SecGroupUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "security:write") {
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Name          *string `json:"name"`
		DefaultAction *string `json:"default_action"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	found := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.SecGroups {
			if cfg.SecGroups[i].ID != groupID {
				continue
			}
			found = true
			if req.Name != nil {
				cfg.SecGroups[i].Name = strings.TrimSpace(*req.Name)
			}
			if req.DefaultAction != nil {
				cfg.SecGroups[i].DefaultAction = secgroup.Action(strings.ToLower(strings.TrimSpace(*req.DefaultAction)))
			}
			return
		}
	})
	if !found {
		v2NotFound(w, r, "安全组不存在："+groupID)
		return
	}
	auditRequest(r, "api.v2.security_group.update", groupID, "", true, "")
	updated, _ := findSecGroup(groupID)
	v2OK(w, r, map[string]interface{}{"id": updated.ID, "name": updated.Name, "default_action": string(updated.DefaultAction)})
}

func v2SecGroupDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "security:write") {
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	removed := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := cfg.SecGroups[:0]
		for i := range cfg.SecGroups {
			if cfg.SecGroups[i].ID == groupID {
				removed = true
				continue
			}
			out = append(out, cfg.SecGroups[i])
		}
		cfg.SecGroups = out
		// 规则随之清理，避免孤儿规则。
		kept := cfg.SecGroupRules[:0]
		for i := range cfg.SecGroupRules {
			if cfg.SecGroupRules[i].GroupID == groupID {
				continue
			}
			kept = append(kept, cfg.SecGroupRules[i])
		}
		cfg.SecGroupRules = kept
	})
	if !removed {
		v2NotFound(w, r, "安全组不存在："+groupID)
		return
	}
	auditRequest(r, "api.v2.security_group.delete", groupID, "", true, "")
	v2NoContent(w, r)
}

func v2SecGroupRulesList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "security:read") {
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	if _, ok := findSecGroup(groupID); !ok {
		v2NotFound(w, r, "安全组不存在："+groupID)
		return
	}
	query := v2ParsePage(r)
	rules := findSecGroupRules(groupID)
	items := make([]map[string]interface{}, 0, len(rules))
	for _, rule := range rules {
		items = append(items, v2SecRuleView(rule))
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

func v2SecGroupRuleCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "security:write") {
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	if _, ok := findSecGroup(groupID); !ok {
		v2NotFound(w, r, "安全组不存在："+groupID)
		return
	}
	var req struct {
		Direction string `json:"direction"` // ingress | egress
		Protocol  string `json:"protocol"`  // tcp | udp | icmp | all
		SrcMask   string `json:"src_mask"`
		DstMask   string `json:"dst_mask"`
		SrcPort   int    `json:"src_port"`
		DstPort   int    `json:"dst_port"`
		Action    string `json:"action"` // accept | drop
		Priority  int    `json:"priority"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	direction := secgroup.Direction(strings.ToLower(strings.TrimSpace(req.Direction)))
	if direction != secgroup.DirIngress && direction != secgroup.DirEgress {
		v2BadRequest(w, r, "direction 只能是 ingress 或 egress", map[string]string{"direction": req.Direction})
		return
	}
	protocol := secgroup.Protocol(strings.ToLower(strings.TrimSpace(req.Protocol)))
	switch protocol {
	case secgroup.ProtoTCP, secgroup.ProtoUDP, secgroup.ProtoICMP, secgroup.ProtoAny:
	default:
		v2BadRequest(w, r, "protocol 只能是 tcp/udp/icmp/all", map[string]string{"protocol": req.Protocol})
		return
	}
	action := secgroup.Action(strings.ToLower(strings.TrimSpace(req.Action)))
	if action == "" {
		action = secgroup.ActionAccept
	}
	if action != secgroup.ActionAccept && action != secgroup.ActionDrop {
		v2BadRequest(w, r, "action 只能是 accept 或 drop", map[string]string{"action": req.Action})
		return
	}
	for _, port := range []int{req.SrcPort, req.DstPort} {
		if port < 0 || port > 65535 {
			v2BadRequest(w, r, "端口范围非法（0-65535）", map[string]string{"port": "0-65535"})
			return
		}
	}
	for _, mask := range []string{req.SrcMask, req.DstMask} {
		if strings.TrimSpace(mask) != "" && !validCIDROrAddrV2(mask) {
			v2BadRequest(w, r, "CIDR 格式非法："+mask, map[string]string{"mask": mask})
			return
		}
	}
	rule := secgroup.Rule{
		ID:        "sgr-" + randomHex(6),
		GroupID:   groupID,
		Direction: direction,
		Protocol:  protocol,
		SrcMask:   strings.TrimSpace(req.SrcMask),
		DstMask:   strings.TrimSpace(req.DstMask),
		SrcPort:   req.SrcPort,
		DstPort:   req.DstPort,
		Action:    action,
		Priority:  req.Priority,
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.SecGroupRules = append(cfg.SecGroupRules, rule)
	})
	auditRequest(r, "api.v2.security_group.rule.create", groupID, rule.ID, true, "")
	v2Created(w, r, v2SecRuleView(rule))
}

func v2SecGroupRuleDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "security:write") {
		return
	}
	groupID := strings.TrimSpace(r.PathValue("id"))
	ruleID := strings.TrimSpace(r.PathValue("rid"))
	removed := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		out := cfg.SecGroupRules[:0]
		for i := range cfg.SecGroupRules {
			if cfg.SecGroupRules[i].ID == ruleID && cfg.SecGroupRules[i].GroupID == groupID {
				removed = true
				continue
			}
			out = append(out, cfg.SecGroupRules[i])
		}
		cfg.SecGroupRules = out
	})
	if !removed {
		v2NotFound(w, r, "规则不存在："+ruleID)
		return
	}
	auditRequest(r, "api.v2.security_group.rule.delete", groupID, ruleID, true, "")
	v2NoContent(w, r)
}

// ---------------------------------------------------------------------------
// IP 池（公网 IPv4 池 + IPv6 前缀）
// ---------------------------------------------------------------------------

func v2IPPoolsList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "routing:read") {
		return
	}
	query := v2ParsePage(r)
	config.AppConfigMu.RLock()
	pool := append([]config.PublicIPv4Assignment(nil), config.AppConfig.PublicIPv4Pool...)
	prefixes := append([]config.PublicIPv6Prefix(nil), config.AppConfig.PublicIPv6Prefixes...)
	config.AppConfigMu.RUnlock()

	used := map[string]string{}
	config.AppConfigMu.RLock()
	for _, c := range config.AppConfig.Containers {
		for _, item := range c.PublicIPv4s {
			if item.Address != "" {
				used[item.Address] = c.Name
			}
		}
	}
	config.AppConfigMu.RUnlock()

	ipv4 := make([]map[string]interface{}, 0, len(pool))
	for _, item := range pool {
		entry := map[string]interface{}{"address": item.Address, "interface": item.Interface, "assigned": false}
		if name, ok := used[item.Address]; ok {
			entry["assigned"] = true
			entry["instance"] = name
		}
		ipv4 = append(ipv4, entry)
	}
	ipv6 := make([]map[string]interface{}, 0, len(prefixes))
	for _, prefix := range prefixes {
		ipv6 = append(ipv6, map[string]interface{}{"prefix": prefix.Prefix, "interface": prefix.Interface})
	}
	total := len(ipv4) + len(ipv6)
	start, end := query.Slice(total)
	combined := make([]map[string]interface{}, 0, total)
	for _, item := range ipv4 {
		item["type"] = "ipv4"
		combined = append(combined, item)
	}
	for _, item := range ipv6 {
		item["type"] = "ipv6_prefix"
		combined = append(combined, item)
	}
	if start > len(combined) {
		start = len(combined)
	}
	if end > len(combined) {
		end = len(combined)
	}
	v2List(w, r, combined[start:end], query, total)
}

func v2IPPoolsAdd(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "routing:write") {
		return
	}
	var req struct {
		Addresses []string `json:"addresses"`  // 公网 IPv4 地址
		Prefixes  []string `json:"prefixes"`   // IPv6 前缀（CIDR）
		Interface string   `json:"interface"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if len(req.Addresses) == 0 && len(req.Prefixes) == 0 {
		v2BadRequest(w, r, "请至少提供 addresses 或 prefixes", nil)
		return
	}
	addedIPv4, addedIPv6 := 0, 0
	if len(req.Addresses) > 0 {
		config.AppConfigMu.Lock()
		existing := map[string]bool{}
		for _, item := range config.AppConfig.PublicIPv4Pool {
			existing[item.Address] = true
		}
		for _, address := range req.Addresses {
			address = strings.TrimSpace(address)
			if address == "" || existing[address] {
				continue
			}
			config.AppConfig.PublicIPv4Pool = append(config.AppConfig.PublicIPv4Pool, config.PublicIPv4Assignment{
				Address: address, Interface: strings.TrimSpace(req.Interface),
			})
			addedIPv4++
		}
		config.AppConfigMu.Unlock()
	}
	if len(req.Prefixes) > 0 {
		config.AppConfigMu.Lock()
		existing := map[string]bool{}
		for _, item := range config.AppConfig.PublicIPv6Prefixes {
			existing[item.Prefix] = true
		}
		for _, prefix := range req.Prefixes {
			prefix = strings.TrimSpace(prefix)
			if prefix == "" || existing[prefix] || !validCIDROrAddrV2(prefix) {
				continue
			}
			config.AppConfig.PublicIPv6Prefixes = append(config.AppConfig.PublicIPv6Prefixes, config.PublicIPv6Prefix{
				Prefix: prefix, Interface: strings.TrimSpace(req.Interface),
			})
			addedIPv6++
		}
		config.AppConfigMu.Unlock()
	}
	if err := config.SaveConfig(); err != nil {
		v2Internal(w, r, "保存失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.ip_pool.add", "", "新增 IPv4 "+itoaV2(addedIPv4)+" / IPv6 "+itoaV2(addedIPv6), true, "")
	v2OK(w, r, map[string]interface{}{"ipv4_added": addedIPv4, "ipv6_added": addedIPv6})
}

func v2IPPoolsRemove(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "routing:write") {
		return
	}
	var req struct {
		Addresses []string `json:"addresses"`
		Prefixes  []string `json:"prefixes"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	// 已分配给实例的地址不允许移除（避免出现地址悬空）。
	inUse := map[string]string{}
	config.AppConfigMu.RLock()
	for _, c := range config.AppConfig.Containers {
		for _, item := range c.PublicIPv4s {
			if item.Address != "" {
				inUse[item.Address] = c.Name
			}
		}
	}
	config.AppConfigMu.RUnlock()
	for _, address := range req.Addresses {
		if name, ok := inUse[strings.TrimSpace(address)]; ok {
			v2Precondition(w, r, "地址 "+address+" 已分配给实例 "+name+"，请先回收")
			return
		}
	}
	removedIPv4, removedIPv6 := 0, 0
	removeSet := map[string]bool{}
	for _, address := range req.Addresses {
		removeSet[strings.TrimSpace(address)] = true
	}
	prefixSet := map[string]bool{}
	for _, prefix := range req.Prefixes {
		prefixSet[strings.TrimSpace(prefix)] = true
	}
	config.AppConfigMu.Lock()
	keptIPv4 := config.AppConfig.PublicIPv4Pool[:0]
	for _, item := range config.AppConfig.PublicIPv4Pool {
		if removeSet[item.Address] {
			removedIPv4++
			continue
		}
		keptIPv4 = append(keptIPv4, item)
	}
	config.AppConfig.PublicIPv4Pool = keptIPv4
	keptIPv6 := config.AppConfig.PublicIPv6Prefixes[:0]
	for _, item := range config.AppConfig.PublicIPv6Prefixes {
		if prefixSet[item.Prefix] {
			removedIPv6++
			continue
		}
		keptIPv6 = append(keptIPv6, item)
	}
	config.AppConfig.PublicIPv6Prefixes = keptIPv6
	config.AppConfigMu.Unlock()
	config.SaveConfigLogged()
	auditRequest(r, "api.v2.ip_pool.remove", "", "移除 IPv4 "+itoaV2(removedIPv4)+" / IPv6 "+itoaV2(removedIPv6), true, "")
	v2OK(w, r, map[string]interface{}{"ipv4_removed": removedIPv4, "ipv6_removed": removedIPv6})
}

func itoaV2(value int) string { return strconv.Itoa(value) }


func validCIDROrAddrV2(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.Contains(value, "/") {
		_, err := netip.ParsePrefix(value)
		return err == nil
	}
	_, err := netip.ParseAddr(value)
	return err == nil
}

func runtimeGOARCHValue() string { return runtime.GOARCH }
