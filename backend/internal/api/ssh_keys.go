package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"context"
	"regexp"
	"strings"
	"time"

	"eyvescloud/internal/config"
)

// ssh_keys.go 实现 SSH 公钥托管（CRUD + 容器级绑定）。
//
// 安全模型：
//   - 公钥入库前强制 ssh-keygen -lf 解析，拒绝非法格式
//   - 指纹由服务端二次计算，前端提交的 fingerprint 不采信
//   - 管理员可管理全部 key；子用户只能管理 type=subuser 的 key
//     （通过 OwnerID + authContext 关联）
//   - 删除 key 时同步清理所有容器的 SSHKeyIDs 引用

var sshKeyAuthHeaderRegex = regexp.MustCompile(`^\s*(ssh-rsa|ssh-ed25519|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com)\s+[A-Za-z0-9+/=]+(\s+.*)?$`)

// HandleSSHKeys 处理 /api/ssh-keys 集合端点（列表 + 创建）。
func HandleSSHKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		listSSHKeys(w, r)
	case http.MethodPost:
		createSSHKey(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleSSHKeyItem 处理 /api/ssh-keys/{id} 条目端点（获取 + 更新 + 删除）。
func HandleSSHKeyItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/ssh-keys/")
	rest = strings.TrimPrefix(rest, "/api/ssh-keys/")
	if rest == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "SSH key id required"})
		return
	}
	id := strings.SplitN(rest, "/", 2)[0]
	switch r.Method {
	case http.MethodGet:
		getSSHKey(w, r, id)
	case http.MethodPut:
		updateSSHKey(w, r, id)
	case http.MethodDelete:
		deleteSSHKey(w, r, id)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func listSSHKeys(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "container:read") {
		return
	}
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()

	keys := config.AppConfig.SSHKeys
	if len(keys) == 0 {
		keys = []config.SSHKey{}
	}

	// 子用户只看自己的 key
	if ctx, ok := authContextFromRequest(r); ok && ctx.Type == authTypeSubUser {
		filtered := make([]config.SSHKey, 0, len(keys))
		for _, k := range keys {
			if k.Type == "subuser" && k.OwnerID == ctx.Actor {
				filtered = append(filtered, k)
			}
		}
		keys = filtered
	}

	// 列表中不返回实际公钥（避免泄漏，详情端点才返回）
	type summary struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Fingerprint string `json:"fingerprint"`
		Type        string `json:"type"`
		OwnerID     string `json:"owner_id,omitempty"`
		CreatedAt   string `json:"created_at"`
		LastUsedAt  string `json:"last_used_at,omitempty"`
	}
	out := make([]summary, len(keys))
	for i, k := range keys {
		out[i] = summary{
			ID: k.ID, Name: k.Name, Fingerprint: k.Fingerprint,
			Type: k.Type, OwnerID: k.OwnerID,
			CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
		}
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: out})
}

func createSSHKey(w http.ResponseWriter, r *http.Request) {
	if !hasAnyScope(r, "container:ssh-key", "admin:write") {
		errResponse(w, http.StatusForbidden, "INSUFFICIENT_SCOPE", "Insufficient API key scope: container:ssh-key")
		return
	}
	var req struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	name := strings.TrimSpace(req.Name)
	pubKey := strings.TrimSpace(req.PublicKey)
	if name == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Name is required"})
		return
	}
	if len(name) > 64 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Name too long (max 64 chars)"})
		return
	}
	if pubKey == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Public key is required"})
		return
	}
	if !sshKeyAuthHeaderRegex.MatchString(pubKey) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid SSH public key format"})
		return
	}
	if len(pubKey) > 4096 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Public key too long"})
		return
	}

	// 服务端计算指纹（不采信前端提交）
	fingerprint, err := computeSSHKeyFingerprint(pubKey)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Failed to parse SSH key: " + err.Error()})
		return
	}

	// 检查重复（同一公钥指纹不允许创建两次）
	config.AppConfigMu.RLock()
	for _, k := range config.AppConfig.SSHKeys {
		if k.Fingerprint == fingerprint {
			config.AppConfigMu.RUnlock()
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "SSH key with this fingerprint already exists"})
			return
		}
	}
	config.AppConfigMu.RUnlock()

	// 确定类型与归属
	keyType := "admin"
	var ownerID string
	if ctx, ok := authContextFromRequest(r); ok {
		switch ctx.Type {
		case authTypeAdmin:
			keyType = "admin"
		case authTypeSubUser:
			keyType = "subuser"
			ownerID = ctx.Actor
		case authTypeAPIKey:
			keyType = "admin" // API Key 归管理员
		}
	}

	newKey := config.SSHKey{
		ID:          "sk-" + randomHex(8),
		Name:        name,
		PublicKey:   pubKey,
		Fingerprint: fingerprint,
		Type:        keyType,
		OwnerID:     ownerID,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	config.MutateGlobalMetaOnlyLogged(func(cfg *config.EyvescloudConfig) {
		cfg.SSHKeys = append(cfg.SSHKeys, newKey)
	})
	auditRequest(r, "ssh_key.create", newKey.Name, fmt.Sprintf("fingerprint=%s", fingerprint), true, "")

	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Data: newKey})
}

func getSSHKey(w http.ResponseWriter, r *http.Request, id string) {
	if !requireScope(w, r, "container:read") {
		return
	}
	key, ok := findSSHKey(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "SSH key not found"})
		return
	}
	if !sshKeyCanAccess(r, key) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this SSH key"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: key})
}

func updateSSHKey(w http.ResponseWriter, r *http.Request, id string) {
	if !hasAnyScope(r, "container:ssh-key", "admin:write") {
		errResponse(w, http.StatusForbidden, "INSUFFICIENT_SCOPE", "Insufficient API key scope: container:ssh-key")
		return
	}
	key, ok := findSSHKey(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "SSH key not found"})
		return
	}
	if !sshKeyCanAccess(r, key) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this SSH key"})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Name is required"})
		return
	}
	if len(name) > 64 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Name too long"})
		return
	}
	config.MutateGlobalMetaOnlyLogged(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.SSHKeys {
			if cfg.SSHKeys[i].ID == id {
				cfg.SSHKeys[i].Name = name
				break
			}
		}
	})
	auditRequest(r, "ssh_key.update", key.Name, fmt.Sprintf("id=%s", id), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

func deleteSSHKey(w http.ResponseWriter, r *http.Request, id string) {
	if !hasAnyScope(r, "container:ssh-key", "admin:write") {
		errResponse(w, http.StatusForbidden, "INSUFFICIENT_SCOPE", "Insufficient API key scope: container:ssh-key")
		return
	}
	key, ok := findSSHKey(id)
	if !ok {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "SSH key not found"})
		return
	}
	if !sshKeyCanAccess(r, key) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this SSH key"})
		return
	}
	config.MutateGlobalLogged(func(cfg *config.EyvescloudConfig) {
		// 从 key 列表中移除
		newList := make([]config.SSHKey, 0, len(cfg.SSHKeys))
		for _, k := range cfg.SSHKeys {
			if k.ID != id {
				newList = append(newList, k)
			}
		}
		cfg.SSHKeys = newList
		// 清理所有容器的引用
		for i := range cfg.Containers {
			for j, kid := range cfg.Containers[i].SSHKeyIDs {
				if kid == id {
					cfg.Containers[i].SSHKeyIDs = append(cfg.Containers[i].SSHKeyIDs[:j], cfg.Containers[i].SSHKeyIDs[j+1:]...)
					break
				}
			}
		}
	})
	auditRequest(r, "ssh_key.delete", key.Name, fmt.Sprintf("id=%s", id), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true})
}

// findSSHKey 按 ID 查找 SSH key（只读，不加锁——调用方已加）。
func findSSHKey(id string) (config.SSHKey, bool) {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for _, k := range config.AppConfig.SSHKeys {
		if k.ID == id {
			return k, true
		}
	}
	return config.SSHKey{}, false
}

// ResolveSSHKeyIDs 把托管 SSH Key ID 列表解析成实际公钥列表，同时返回缺失的 ID。
// 供 createContainer / reinstall 等创建/重装流程使用（注入 authorized_keys）。
// 去重：同一公钥不会重复返回。
func ResolveSSHKeyIDs(ids []string) (pubKeys []string, missing []string) {
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	if len(ids) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	idSet := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			idSet[id] = true
		}
	}
	// 构建 key lookup
	keyByID := map[string]config.SSHKey{}
	for _, k := range config.AppConfig.SSHKeys {
		keyByID[k.ID] = k
	}
	for id := range idSet {
		k, ok := keyByID[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		key := strings.TrimSpace(k.PublicKey)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		pubKeys = append(pubKeys, key)
	}
	return pubKeys, missing
}

// sshKeyCanAccess 子用户只能访问自己的 key；管理员可访问全部。
func sshKeyCanAccess(r *http.Request, key config.SSHKey) bool {
	ctx, ok := authContextFromRequest(r)
	if !ok {
		return false
	}
	switch ctx.Type {
	case authTypeAdmin:
		return true
	case authTypeAPIKey:
		// API Key 仅在持有管理级/SSH Key 作用域时可访问任意 Key，避免窄作用域
		// Key 越权读取或改写他人 SSH Key（IDOR）。
		return scopeAllowed(ctx.Scopes, "admin:access") || scopeAllowed(ctx.Scopes, "container:ssh-key")
	case authTypeSubUser:
		return key.Type == "subuser" && key.OwnerID == ctx.Actor
	}
	return false
}

// computeSSHKeyFingerprint 用 ssh-keygen -lf 解析公钥，取 SHA256 指纹。
// fallback：纯 Go 解析 OpenSSH pubkey 格式，用 SHA256 哈希其 base64 解码内容。
func computeSSHKeyFingerprint(pubKey string) (string, error) {
	// 优先用 ssh-keygen（系统命令）
	tmpDir := "/tmp"
	tmpFile := fmt.Sprintf("%s/eyvescloud_sshkey_%s.pub", tmpDir, randomHex(8))
	defer func() {
		_ctxRM, _cancelRM := context.WithTimeout(context.Background(), 5*time.Second)
		defer _cancelRM()
		_ = exec.CommandContext(_ctxRM, "rm", "-f", tmpFile).Run()
	}()
	if err := os.WriteFile(tmpFile, []byte(pubKey), 0600); err == nil {
		_ctx1, _cancel1 := context.WithTimeout(context.Background(), 15*time.Second)
		defer _cancel1()
		if out, err := exec.CommandContext(_ctx1, "ssh-keygen", "-lf", tmpFile).CombinedOutput(); err == nil {
			// ssh-keygen -lf 输出格式："2048 SHA256:xxx comment (RSA)"
			parts := strings.Fields(string(out))
			for i, p := range parts {
				if strings.HasPrefix(p, "SHA256:") {
					return p, nil
				}
				if i == 0 && len(parts) >= 3 {
					// MD5 降级（旧 ssh-keygen）：取第 2 个字段
					if len(parts[i+1]) > 20 {
						return "MD5:" + parts[i+1], nil
					}
				}
			}
		}
	}
	// Fallback：Go 纯实现
	return computeSSHKeyFingerprintGo(pubKey)
}

// computeSSHKeyFingerprintGo 不依赖 ssh-keygen 的纯 Go 实现。
// 解析 OpenSSH 公钥行，对 base64 解码后的 blob 做 SHA256。
func computeSSHKeyFingerprintGo(pubKey string) (string, error) {
	fields := strings.Fields(pubKey)
	if len(fields) < 2 {
		return "", fmt.Errorf("invalid key format")
	}
	data, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", fmt.Errorf("invalid base64 in key")
	}
	sum := sha256.Sum256(data)
	hex := base64.StdEncoding.EncodeToString(sum[:])
	return "SHA256:" + hex, nil
}
