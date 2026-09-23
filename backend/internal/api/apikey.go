package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"

	"golang.org/x/crypto/argon2"
)

type ApiKey struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Key            string   `json:"key,omitempty"`
	Prefix         string   `json:"prefix"`
	IPWhitelist    string   `json:"ip_whitelist"`
	CreatedAt      string   `json:"created_at"`
	LastUsed       string   `json:"last_used"`
	Scopes         []string `json:"scopes,omitempty"`
	ExpiresAt      string   `json:"expires_at,omitempty"`
	Disabled       bool     `json:"disabled,omitempty"`
	ContainerUUIDs []string `json:"container_uuids,omitempty"`
	LastUsedIP     string   `json:"last_used_ip,omitempty"`
}

type apiKeyRequest struct {
	Name           string   `json:"name"`
	IPWhitelist    string   `json:"ip_whitelist"`
	Scopes         []string `json:"scopes"`
	ExpiresAt      string   `json:"expires_at"`
	Disabled       bool     `json:"disabled"`
	ContainerUUIDs []string `json:"container_uuids"`
}

var defaultApiKeyScopes = []string{
	"dashboard:read",
	"container:read",
	"task:read",
	"image:read",
	"snapshot:read",
	"routing:read",
	"ipv6:read",
	"host:read",
}

// HandleApiKeys handles GET (list) and POST (create) for API keys
func HandleApiKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, r, "apikey:read") {
			return
		}
		listApiKeys(w, r)
	case http.MethodPost:
		if !requireScope(w, r, "apikey:create") {
			return
		}
		createApiKey(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

// HandleApiKeyDelete handles PATCH and DELETE for a specific API key
func HandleApiKeyDelete(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPatch:
		if !requireScope(w, r, "apikey:update") {
			return
		}
		updateApiKey(w, r)
	case http.MethodDelete:
		if !requireScope(w, r, "apikey:delete") {
			return
		}
		deleteApiKey(w, r)
	default:
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
	}
}

func apiKeyIDFromPath(path string) string {
	path = strings.TrimPrefix(path, "/api/api-keys/")
	path = strings.TrimPrefix(path, "/api/v1/api-keys/")
	return strings.Trim(path, "/")
}

func listApiKeys(w http.ResponseWriter, r *http.Request) {
	config.AppConfigMu.RLock()
	keys := make([]ApiKey, 0, len(config.AppConfig.ApiKeys))
	for _, k := range config.AppConfig.ApiKeys {
		keys = append(keys, apiKeyResponse(k))
	}
	config.AppConfigMu.RUnlock()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: keys})
}

func createApiKey(w http.ResponseWriter, r *http.Request) {
	var req apiKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Name is required"})
		return
	}
	if req.ExpiresAt != "" && !validApiKeyTime(req.ExpiresAt) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid expiration date"})
		return
	}

	// Generate key: eyvescloud_sk_ + 32 hex chars
	rawBytes := make([]byte, 16)
	if _, err := rand.Read(rawBytes); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to generate API key"})
		return
	}
	rawKey := "eyvescloud_sk_" + hex.EncodeToString(rawBytes)

	keyHash, err := hashAPIKey(rawKey)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to store API key"})
		return
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	scopes := normalizeRequestedScopes(req.Scopes, defaultApiKeyScopes)
	if err := validateKeyGrantScopes(r, scopes); err != nil {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: err.Error()})
		return
	}
	key := config.ApiKeyConfig{
		ID:             generateShortID(),
		Name:           strings.TrimSpace(req.Name),
		KeyHash:        keyHash,
		KeyFingerprint: apiKeyFingerprint(rawKey),
		Prefix:         rawKey[:13] + "...",
		IPWhitelist:    strings.TrimSpace(req.IPWhitelist),
		CreatedAt:      now,
		Scopes:         scopes,
		ExpiresAt:      strings.TrimSpace(req.ExpiresAt),
		Disabled:       req.Disabled,
		ContainerUUIDs: normalizeStringSlice(req.ContainerUUIDs),
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.ApiKeys = append(cfg.ApiKeys, key)
	})
	auditRequest(r, "apikey.create", key.Name, "scopes="+strings.Join(key.Scopes, ","), true, "")

	resp := apiKeyResponse(key)
	resp.Key = rawKey
	jsonResponse(w, http.StatusCreated, APIResponse{
		Success: true,
		Message: "API key created. Save this key now - it won't be shown again.",
		Data:    resp,
	})
}

func updateApiKey(w http.ResponseWriter, r *http.Request) {
	keyID := apiKeyIDFromPath(r.URL.Path)
	if keyID == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Key ID required"})
		return
	}
	var req apiKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	if req.ExpiresAt != "" && !validApiKeyTime(req.ExpiresAt) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid expiration date"})
		return
	}
	var updated *config.ApiKeyConfig
	// 若本次提交了新 scopes，先校验授予方是否有权授予（受限 Key 只能授予自身子集）。
	if len(req.Scopes) > 0 {
		newScopes := normalizeStringSlice(req.Scopes)
		if err := validateKeyGrantScopes(r, newScopes); err != nil {
			jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: err.Error()})
			return
		}
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.ApiKeys {
			if cfg.ApiKeys[i].ID != keyID {
				continue
			}
			if strings.TrimSpace(req.Name) != "" {
				cfg.ApiKeys[i].Name = strings.TrimSpace(req.Name)
			}
			cfg.ApiKeys[i].IPWhitelist = strings.TrimSpace(req.IPWhitelist)
			if len(req.Scopes) > 0 {
				cfg.ApiKeys[i].Scopes = normalizeStringSlice(req.Scopes)
			}
			cfg.ApiKeys[i].ExpiresAt = strings.TrimSpace(req.ExpiresAt)
			cfg.ApiKeys[i].Disabled = req.Disabled
			cfg.ApiKeys[i].ContainerUUIDs = normalizeStringSlice(req.ContainerUUIDs)
			copyKey := cfg.ApiKeys[i]
			updated = &copyKey
			return
		}
	})
	if updated == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "API key not found"})
		return
	}
	auditRequest(r, "apikey.update", updated.Name, "scopes="+strings.Join(updated.Scopes, ","), true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: apiKeyResponse(*updated)})
}

func deleteApiKey(w http.ResponseWriter, r *http.Request) {
	keyID := apiKeyIDFromPath(r.URL.Path)
	if keyID == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Key ID required"})
		return
	}
	name := keyID
	config.AppConfigMu.RLock()
	for _, k := range config.AppConfig.ApiKeys {
		if k.ID == keyID {
			name = k.Name
			break
		}
	}
	config.AppConfigMu.RUnlock()
	config.DeleteApiKey(keyID)
	auditRequest(r, "apikey.delete", name, "", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "API key deleted"})
}

func apiKeyResponse(k config.ApiKeyConfig) ApiKey {
	return ApiKey{
		ID:             k.ID,
		Name:           k.Name,
		Prefix:         k.Prefix,
		IPWhitelist:    k.IPWhitelist,
		CreatedAt:      k.CreatedAt,
		LastUsed:       k.LastUsed,
		Scopes:         normalizeApiKeyScopes(k.Scopes),
		ExpiresAt:      k.ExpiresAt,
		Disabled:       k.Disabled,
		ContainerUUIDs: k.ContainerUUIDs,
		LastUsedIP:     k.LastUsedIP,
	}
}

func generateShortID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

const (
	apiKeyHashPrefix     = "argon2id"
	apiKeyHashTime       = uint32(3)
	apiKeyHashMemory     = uint32(64 * 1024)
	apiKeyHashThreads    = uint8(1)
	apiKeyHashSaltLength = 16
	apiKeyHashKeyLength  = uint32(32)
)

// hashAPIKey stores API keys using a salted slow password-hash style function.
func hashAPIKey(key string) (string, error) {
	salt := make([]byte, apiKeyHashSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return hashAPIKeyWithSalt(key, salt), nil
}

// apiKeyFingerprint returns an unsalted SHA-256 hex fingerprint of the raw key.
// It is used only for O(1) existence pre-screening before paying for argon2.
func apiKeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func hashAPIKeyWithSalt(key string, salt []byte) string {
	digest := argon2.IDKey([]byte(key), salt, apiKeyHashTime, apiKeyHashMemory, apiKeyHashThreads, apiKeyHashKeyLength)
	return fmt.Sprintf("%s$v=19$m=%d,t=%d,p=%d$%s$%s",
		apiKeyHashPrefix,
		apiKeyHashMemory,
		apiKeyHashTime,
		apiKeyHashThreads,
		hex.EncodeToString(salt),
		hex.EncodeToString(digest),
	)
}

func verifyAPIKeyHash(rawKey, storedHash string) bool {
	parts := strings.Split(storedHash, "$")
	if len(parts) != 5 || parts[0] != apiKeyHashPrefix || parts[1] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	if memory != apiKeyHashMemory || iterations != apiKeyHashTime || threads != apiKeyHashThreads {
		return false
	}
	salt, err := hex.DecodeString(parts[3])
	if err != nil || len(salt) == 0 {
		return false
	}
	expected, err := hex.DecodeString(parts[4])
	if err != nil || len(expected) == 0 {
		return false
	}
	digest := argon2.IDKey([]byte(rawKey), salt, iterations, memory, threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(digest, expected) == 1
}

func legacyHashKey(key string) string {
	b := make([]byte, 32)
	for i := range key {
		b[i%32] ^= key[i]
	}
	return hex.EncodeToString(b)
}

func matchApiKey(rawKey string) (idx int, needsRehash bool) {
	legacyHashed := legacyHashKey(rawKey)
	finger := apiKeyFingerprint(rawKey)
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for i, k := range config.AppConfig.ApiKeys {
		// Fast O(1) pre-screen: if the key has a stored fingerprint and it does
		// not match, skip the expensive argon2 verification entirely.
		if k.KeyFingerprint != "" && k.KeyFingerprint != finger {
			continue
		}
		if verifyAPIKeyHash(rawKey, k.KeyHash) {
			return i, false
		}
		if subtle.ConstantTimeCompare([]byte(k.KeyHash), []byte(legacyHashed)) == 1 {
			return i, true
		}
	}
	return -1, false
}

// validateApiKey checks if the given key is valid and IP is allowed.
func validateApiKey(rawKey, clientIP string) bool {
	_, ok := validateApiKeyDetails(rawKey, clientIP)
	return ok
}

func validateApiKeyDetails(rawKey, clientIP string) (*config.ApiKeyConfig, bool) {
	idx, needsRehash := matchApiKey(rawKey)
	if idx < 0 {
		return nil, false
	}
	// Snapshot the key fields under the read lock and return a copy so callers
	// never hold a live pointer that another goroutine may mutate.
	config.AppConfigMu.RLock()
	live := config.AppConfig.ApiKeys[idx]
	if live.Disabled || apiKeyExpired(live.ExpiresAt) {
		config.AppConfigMu.RUnlock()
		return nil, false
	}
	if clientIP != "" && live.IPWhitelist != "" && !isIPAllowed(clientIP, live.IPWhitelist) {
		config.AppConfigMu.RUnlock()
		return nil, false
	}
	if len(live.Scopes) == 0 {
		live.Scopes = []string{"*"}
	}
	config.AppConfigMu.RUnlock()

	if needsRehash {
		if newHash, err := hashAPIKey(rawKey); err == nil {
			config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
				if idx < len(cfg.ApiKeys) && cfg.ApiKeys[idx].ID == live.ID {
					cfg.ApiKeys[idx].KeyHash = newHash
				}
			})
		}
	}
	copyKey := live
	return &copyKey, true
}

func validateApiKeyRequest(r *http.Request) (*config.ApiKeyConfig, bool) {
	apiKey := apiKeyFromRequest(r)
	if apiKey == "" {
		return nil, false
	}
	key, ok := validateApiKeyDetails(apiKey, clientIP(r))
	if !ok {
		return nil, false
	}
	updateApiKeyLastUsedForKey(key, clientIP(r))
	return key, true
}

func authContextFromAPIKey(key *config.ApiKeyConfig) AuthContext {
	actor := "api:" + key.ID
	if key.Name != "" {
		actor = "api:" + key.Name
	}
	return AuthContext{
		Type:           authTypeAPIKey,
		ApiKeyID:       key.ID,
		ApiKeyName:     key.Name,
		Actor:          actor,
		Scopes:         normalizeApiKeyScopes(key.Scopes),
		ContainerUUIDs: key.ContainerUUIDs,
	}
}

func apiKeyFromRequest(r *http.Request) string {
	if apiKey := strings.TrimSpace(r.Header.Get("X-API-Key")); apiKey != "" {
		return apiKey
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer eyvescloud_sk_") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func isValidApiKeyRequest(r *http.Request) bool {
	_, ok := validateApiKeyRequest(r)
	return ok
}

// isIPAllowed checks if clientIP matches any entry in the whitelist
func isIPAllowed(clientIP, whitelist string) bool {
	clientIP = normalizeIPString(clientIP)
	client := net.ParseIP(clientIP)
	if client == nil {
		return false
	}
	for _, entry := range strings.Split(whitelist, "\n") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err == nil && network.Contains(client) {
				return true
			}
			continue
		}
		if allowed := net.ParseIP(normalizeIPString(entry)); allowed != nil && allowed.Equal(client) {
			return true
		}
	}
	return false
}

func normalizeIPString(s string) string {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(s, "[]")
}

func ipInCIDR(ipStr, cidr string) bool {
	ip := net.ParseIP(normalizeIPString(ipStr))
	_, network, err := net.ParseCIDR(cidr)
	return err == nil && ip != nil && network.Contains(ip)
}

// updateApiKeyLastUsed marks the key as recently used.
func updateApiKeyLastUsed(rawKey string) {
	key, ok := validateApiKeyDetails(rawKey, "")
	if !ok {
		return
	}
	updateApiKeyLastUsedForKey(key, "")
}

func updateApiKeyLastUsedForKey(key *config.ApiKeyConfig, ip string) {
	if key == nil {
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.ApiKeys {
			if cfg.ApiKeys[i].ID == key.ID {
				cfg.ApiKeys[i].LastUsed = now
				if ip != "" {
					cfg.ApiKeys[i].LastUsedIP = ip
				}
				return
			}
		}
	})
}

// ApiKeyMiddleware authenticates requests via X-API-Key header or Authorization bearer.
func ApiKeyMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := validateApiKeyRequest(r)
		if !ok {
			jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid API key or IP not in whitelist"})
			return
		}
		next(w, withAuthContext(r, authContextFromAPIKey(key)))
	}
}

func normalizeApiKeyScopes(scopes []string) []string {
	return normalizeRequestedScopes(scopes, []string{"*"})
}

func normalizeRequestedScopes(scopes []string, fallback []string) []string {
	result := normalizeStringSlice(scopes)
	if len(result) == 0 {
		return append([]string(nil), fallback...)
	}
	return result
}

// isManagementScope 判断 scope 是否属于管理员/管理面权限（不应由受限 API Key 授予）。
func isManagementScope(scope string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "*" || scope == "admin:access" || scope == "admin:*" {
		return true
	}
	return strings.HasPrefix(scope, "apikey:") ||
		strings.HasPrefix(scope, "admin:")
}

// validateKeyGrantScopes 校验当前调用方是否有权创建/更新一个携带 requested scopes 的 API Key。
//   - 真管理员会话(authTypeAdmin)：可授予任意 scope。
//   - 受限 API Key / 子用户：只能授予其自身已拥有 scope 的子集，且禁止授予管理类 scope，
//     防止 apikey:create/update 权限被滥用为自我提权到 "*" / "admin:access"。
func validateKeyGrantScopes(r *http.Request, requested []string) error {
	ctx, ok := authContextFromRequest(r)
	if !ok {
		return fmt.Errorf("无法识别授予方身份")
	}
	if ctx.Type == authTypeAdmin {
		return nil
	}
	callerScopes := []string(append([]string{}, ctx.Scopes...))
	for _, scope := range requested {
		if isManagementScope(scope) {
			return fmt.Errorf("禁止授予管理类权限 %q：仅管理员可创建/更新此类作用域", scope)
		}
		if ctx.Type == authTypeAPIKey {
			allowed := scopeAllowed(callerScopes, scope)
			if !allowed {
				return fmt.Errorf("无权授予超出自身权限的作用域 %q：受限 API Key 只能授予自己已拥有作用域的子集", scope)
			}
		}
	}
	return nil
}

func normalizeStringSlice(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func validApiKeyTime(value string) bool {
	_, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.Local)
	return err == nil
}

func apiKeyExpired(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	expiresAt, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.Local)
	return err == nil && !time.Now().Before(expiresAt)
}
