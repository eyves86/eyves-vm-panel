package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

func generateRandomStr(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

type subUserResponse struct {
	ID                   string   `json:"id"`
	Username             string   `json:"username"`
	Password             string   `json:"password,omitempty"`
	Role                 string   `json:"role"`
	Tenant               string   `json:"tenant"`
	ContainerNames       []string `json:"container_names"`
	ContainerUUIDs       []string `json:"container_uuids,omitempty"`
	AllowedImageIDs      []string `json:"allowed_image_ids,omitempty"`
	ImageLimitConfigured bool     `json:"image_limit_configured,omitempty"`
	CurrentImageIDs      []string `json:"current_image_ids,omitempty"`
	AccessCode           string   `json:"access_code"`
	CreatedAt            string   `json:"created_at"`
}

func newSubUserResponse(su config.SubUser, password string) subUserResponse {
	return subUserResponse{
		ID:                   su.ID,
		Username:             su.Username,
		Password:             password,
		Role:                 subUserRole(su.Role),
		Tenant:               strings.TrimSpace(su.Tenant),
		ContainerNames:       su.ContainerNames,
		ContainerUUIDs:       su.ContainerUUIDs,
		AllowedImageIDs:      effectiveSubUserAllowedImageIDs(&su),
		ImageLimitConfigured: su.ImageLimitConfigured,
		CurrentImageIDs:      subUserCurrentImageIDs(&su),
		AccessCode:           su.AccessCode,
		CreatedAt:            su.CreatedAt,
	}
}

// subUserRole normalizes an empty role to the default operator role.
func subUserRole(role string) string {
	if strings.EqualFold(role, "viewer") {
		return "viewer"
	}
	return "operator"
}

// HandleSubUserCreate creates a sub-user for a specific container
func HandleSubUserCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "subuser:create") {
		return
	}

	var req struct {
		ContainerName string `json:"container_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	c := containerByIdentifier(req.ContainerName)

	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	containerName := c.Name

	// Check if sub-user already exists and return the same management password.
	// The mutation and the in-memory response snapshot happen under the config
	// write lock so concurrent sub-user edits cannot tear the update.
	type existingResult struct {
		password string
		message  string
		su       config.SubUser
	}
	var found *existingResult
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.SubUsers {
			su := &cfg.SubUsers[i]
			matched := false
			for _, uuid := range su.ContainerUUIDs {
				if uuid == c.UUID {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			if su.AccessCode == "" {
				su.AccessCode = generateRandomStr(8)
			}
			password := su.Password
			message := "Sub-user link returned"
			if password == "" {
				password = generateRandomStr(16)
				hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
				if err != nil {
					continue
				}
				su.PassHash = string(hash)
				su.Password = password
				su.Token = ""
				su.TokenVersion++
				message = "Sub-user password generated"
			}
			su.ContainerNames = appendUniqueString(su.ContainerNames, containerName)
			su.ContainerUUIDs = appendUniqueString(su.ContainerUUIDs, c.UUID)
			if !su.ImageLimitConfigured && len(su.AllowedImageIDs) == 0 {
				su.AllowedImageIDs = effectiveContainerAllowedImageIDs(c)
				su.ImageLimitConfigured = true
			}
			found = &existingResult{password: password, message: message, su: *su}
			return
		}
	})
	if found != nil {
		resp := newSubUserResponse(found.su, found.password)
		jsonResponse(w, http.StatusOK, APIResponse{
			Success: true,
			Message: found.message,
			Data:    resp,
		})
		return
	}

	// Create new sub-user
	username := "user-" + generateRandomStr(8)
	password := generateRandomStr(16)
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)

	// Generate short access code (8 chars, for URL sharing)
	accessCode := generateRandomStr(8)

	subUser := config.SubUser{
		ID:                   "sub-" + generateRandomStr(8),
		Username:             username,
		Password:             password,
		PassHash:             string(hash),
		ContainerNames:       []string{containerName},
		ContainerUUIDs:       []string{c.UUID},
		AllowedImageIDs:      effectiveContainerAllowedImageIDs(c),
		ImageLimitConfigured: true,
		AccessCode:           accessCode,
		CreatedAt:            time.Now().Format("2006-01-02 15:04:05"),
	}

	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.SubUsers = append(cfg.SubUsers, subUser)
	})
	config.AddAuditLog("创建子用户", containerName, fmt.Sprintf("用户: %s", username), "admin")

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Sub-user created", Data: newSubUserResponse(subUser, password)})
}

// HandleSubUserLogin handles sub-user login
func HandleSubUserLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	ip := clientIP(r)
	clientUA := r.Header.Get("User-Agent")
	rateKey := ip + "|user:" + req.Username
	if loginRateLimited(w, rateKey) {
		return
	}

	// Find sub-user (snapshot under the read lock so a concurrent sub-user
	// edit cannot tear the slice while it is being scanned)
	config.AppConfigMu.RLock()
	subUsers := append([]config.SubUser(nil), config.AppConfig.SubUsers...)
	config.AppConfigMu.RUnlock()
	for _, su := range subUsers {
		if su.Username == req.Username {
			if err := bcrypt.CompareHashAndPassword([]byte(su.PassHash), []byte(req.Password)); err == nil {
				containerUUIDs := activeSubUserContainerUUIDs(&su)
				if len(containerUUIDs) == 0 {
					loginLimiter.recordFail(rateKey)
					config.AddLoginLog(su.Username, ip, clientUA, false)
					jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "No active container is assigned to this user"})
					return
				}
				loginLimiter.reset(rateKey)
				tokenStr := newSubUserTokenWithRole(su.Username, containerUUIDs, su.Role, time.Now().Add(24*time.Hour), su.TokenVersion)
				config.AddLoginLog(su.Username, ip, clientUA, true)

				jsonResponse(w, http.StatusOK, APIResponse{
					Success: true,
					Data: map[string]interface{}{
						"token":           tokenStr,
						"username":        su.Username,
						"role":            subUserRole(su.Role),
						"container_uuids": containerUUIDs,
					},
				})
				return
			} else {
				loginLimiter.recordFail(rateKey)
				config.AddLoginLog(su.Username, ip, clientUA, false)
			}
		}
	}

	// Unknown username: record the failure so the per-identity limiter still
	// throttles probing attempts for accounts that do not exist.
	loginLimiter.recordFail(rateKey)
	config.AddLoginLog(req.Username, ip, clientUA, false)
	jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid credentials"})
}

// HandleSubUserAccessCode handles access via short code + password (no token in URL)
func HandleSubUserAccessCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}

	var req struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	// Find sub-user by access code
	ip := clientIP(r)
	clientUA := r.Header.Get("User-Agent")
	rateKey := ip + "|code:" + req.Code
	if loginRateLimited(w, rateKey) {
		return
	}

	config.AppConfigMu.RLock()
	subUsers := append([]config.SubUser(nil), config.AppConfig.SubUsers...)
	config.AppConfigMu.RUnlock()
	for _, su := range subUsers {
		if su.AccessCode == req.Code {
			if err := bcrypt.CompareHashAndPassword([]byte(su.PassHash), []byte(req.Password)); err != nil {
				loginLimiter.recordFail(rateKey)
				config.AddLoginLog(su.Username, ip, clientUA, false)
				jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid password"})
				return
			}

			containerUUIDs := activeSubUserContainerUUIDs(&su)
			if len(containerUUIDs) == 0 {
				loginLimiter.recordFail(rateKey)
				config.AddLoginLog(su.Username, ip, clientUA, false)
				jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "No active container is assigned to this link"})
				return
			}
			loginLimiter.reset(rateKey)
			tokenStr := newSubUserTokenWithRole(su.Username, containerUUIDs, su.Role, time.Now().Add(24*time.Hour), su.TokenVersion)
			config.AddLoginLog(su.Username, ip, clientUA, true)

			jsonResponse(w, http.StatusOK, APIResponse{
				Success: true,
				Data: map[string]interface{}{
					"token":           tokenStr,
					"username":        su.Username,
					"role":            subUserRole(su.Role),
					"container_uuids": containerUUIDs,
				},
			})
			return
		}
	}

	// Unknown access code: throttle further attempts from this identity.
	loginLimiter.recordFail(rateKey)
	jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid access code"})
}

func newSubUserToken(username string, containerUUIDs []string, expiresAt time.Time, tokenVersion int) string {
	return newSubUserTokenWithRole(username, containerUUIDs, "", expiresAt, tokenVersion)
}

func newSubUserTokenWithRole(username string, containerUUIDs []string, role string, expiresAt time.Time, tokenVersion int) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub_user":        username,
		"container_uuids": containerUUIDs,
		"role":            subUserRole(role),
		"token_version":   tokenVersion,
		"iss":             jwtIssuer,
		"aud":             jwtAudience,
		"exp":             expiresAt.Unix(),
		"iat":             time.Now().Unix(),
	})
	tokenStr, _ := token.SignedString([]byte(config.AppConfig.JWTSecret))
	return tokenStr
}

type subUserAccess struct {
	names map[string]bool
	uuids map[string]bool
}

func subUserAllowedContainers(r *http.Request) (subUserAccess, bool) {
	claims, ok := claimsFromRequest(r)
	if !ok {
		return subUserAccess{}, false
	}
	if _, isSubUser := claims["sub_user"]; !isSubUser {
		return subUserAccess{}, false
	}

	allowed := subUserAccess{
		names: make(map[string]bool),
		uuids: make(map[string]bool),
	}
	if containerUUIDs, ok := claims["container_uuids"].([]interface{}); ok {
		for _, item := range containerUUIDs {
			if uuid, ok := item.(string); ok {
				allowed.uuids[uuid] = true
			}
		}
	}
	if containerUUIDs, ok := claims["container_uuids"].([]string); ok {
		for _, uuid := range containerUUIDs {
			allowed.uuids[uuid] = true
		}
	}
	return allowed, true
}

func requestAllowedContainers(r *http.Request) (subUserAccess, bool) {
	if ctx, ok := authContextFromRequest(r); ok {
		if ctx.Type == authTypeAPIKey && len(ctx.ContainerUUIDs) == 0 {
			return subUserAccess{}, false
		}
		if ctx.Type == authTypeSubUser || ctx.Type == authTypeAPIKey {
			allowed := subUserAccess{names: make(map[string]bool), uuids: make(map[string]bool)}
			for _, uuid := range ctx.ContainerUUIDs {
				allowed.uuids[uuid] = true
			}
			if ctx.Type == authTypeSubUser && len(ctx.ContainerUUIDs) == 0 {
				legacy, ok := subUserAllowedContainers(r)
				if ok {
					return legacy, true
				}
			}
			return allowed, true
		}
	}
	return subUserAllowedContainers(r)
}

func subUserFromRequest(r *http.Request) *config.SubUser {
	username := ""
	if ctx, ok := authContextFromRequest(r); ok && ctx.Type == authTypeSubUser {
		username = ctx.Username
	}
	if username == "" {
		if claims, ok := claimsFromRequest(r); ok {
			username, _ = claims["sub_user"].(string)
		}
	}
	if username == "" {
		return nil
	}
	// Return a snapshot copy so callers never hold a live pointer that a
	// concurrent sub-user edit (password rotation, role change) may mutate.
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for i := range config.AppConfig.SubUsers {
		if config.AppConfig.SubUsers[i].Username == username {
			copySU := config.AppConfig.SubUsers[i]
			return &copySU
		}
	}
	return nil
}

func normalizeAllowedImageIDs(ids []string) ([]string, error) {
	seen := map[string]bool{}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if !imageTemplateExists(id) {
			return nil, fmt.Errorf("unknown image template: %s", id)
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}

func isTemplateAllowedForRequest(r *http.Request, c *config.Container, templateID string) bool {
	if !isSubUserRequest(r) {
		return true
	}
	return isImageAllowedForSubUser(subUserFromRequest(r), c, templateID)
}

func isImageAllowedForSubUser(su *config.SubUser, c *config.Container, templateID string) bool {
	if su == nil || strings.TrimSpace(templateID) == "" {
		return false
	}
	for _, id := range effectiveSubUserAllowedImageIDs(su) {
		if id == templateID {
			return true
		}
	}
	return false
}

func effectiveContainerAllowedImageIDs(c *config.Container) []string {
	if c == nil {
		return nil
	}
	if c.ImageLimitConfigured || len(c.AllowedImageIDs) > 0 {
		return cleanImageIDList(c.AllowedImageIDs)
	}
	if c.Template != "" {
		return []string{c.Template}
	}
	return nil
}

func effectiveSubUserAllowedImageIDs(su *config.SubUser) []string {
	if su == nil {
		return nil
	}
	if su.ImageLimitConfigured || len(su.AllowedImageIDs) > 0 {
		return cleanImageIDList(su.AllowedImageIDs)
	}
	result := []string{}
	seen := map[string]bool{}
	for _, c := range subUserAssignedContainers(su) {
		for _, id := range effectiveContainerAllowedImageIDs(c) {
			if id != "" && !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
	}
	return result
}

func cleanImageIDList(ids []string) []string {
	result := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}

func subUserCurrentImageIDs(su *config.SubUser) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, c := range subUserAssignedContainers(su) {
		if c.Template != "" && !seen[c.Template] {
			seen[c.Template] = true
			result = append(result, c.Template)
		}
	}
	return result
}

func subUserAssignedContainers(su *config.SubUser) []*config.Container {
	if su == nil {
		return nil
	}
	result := []*config.Container{}
	seen := map[string]bool{}
	for _, uuid := range su.ContainerUUIDs {
		if c := config.FindContainerByUUID(uuid); c != nil {
			key := c.UUID
			if key == "" {
				key = c.Name
			}
			if !seen[key] {
				seen[key] = true
				result = append(result, c)
			}
		}
	}
	for _, name := range su.ContainerNames {
		if c := config.FindContainerByName(name); c != nil {
			key := c.UUID
			if key == "" {
				key = c.Name
			}
			if !seen[key] {
				seen[key] = true
				result = append(result, c)
			}
		}
	}
	return result
}

func isAccessRestrictedRequest(r *http.Request) bool {
	_, restricted := requestAllowedContainers(r)
	return restricted
}

// isAdminRequest reports whether the caller is the built-in administrator
// (or an API key with admin:access). Sub-users and narrow API keys return false.
func isAdminRequest(r *http.Request) bool {
	if ctx, ok := authContextFromRequest(r); ok {
		switch ctx.Type {
		case authTypeAdmin:
			return true
		case authTypeAPIKey:
			return scopeAllowed(ctx.Scopes, "admin:access")
		default:
			return false
		}
	}
	claims, ok := claimsFromRequest(r)
	if !ok {
		return false
	}
	_, isSubUser := claims["sub_user"]
	return !isSubUser
}

// sanitizeContainerResponse strips secrets from a container copy before it is
// returned to non-admin callers (sub-users and container-bound API keys).
func sanitizeContainerResponse(r *http.Request, c *config.Container) {
	if !isAdminRequest(r) {
		c.SSHPassword = ""
	}
}

// subUserIsReadOnly reports whether the current sub-user has the read-only role.
func subUserIsReadOnly(r *http.Request) bool {
	su := subUserFromRequest(r)
	if su == nil {
		return false
	}
	return subUserRole(su.Role) == "viewer"
}

// requireSubUserWrite denies mutating operations for read-only sub-users.
func requireSubUserWrite(w http.ResponseWriter, r *http.Request) bool {
	if subUserIsReadOnly(r) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Read-only sub-user cannot perform this operation"})
		return false
	}
	return true
}

func containerByIdentifier(identifier string) *config.Container {
	return config.FindContainerByIdentifier(identifier)
}

func isContainerAllowedForRequest(r *http.Request, identifier string) bool {
	allowed, restricted := requestAllowedContainers(r)
	if !restricted {
		return true
	}
	c := containerByIdentifier(identifier)
	if c == nil {
		return false
	}
	return isContainerAllowed(allowed, c)
}

// HandleAuditLogs returns audit logs
func HandleAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "audit:read") {
		return
	}

	config.AppConfigMu.RLock()
	logs := append([]config.AuditLog(nil), config.AppConfig.AuditLogs...)
	config.AppConfigMu.RUnlock()
	if logs == nil {
		logs = []config.AuditLog{}
	}
	// Return in reverse order (newest first)
	reversed := make([]config.AuditLog, len(logs))
	for i, l := range logs {
		reversed[len(logs)-1-i] = l
	}

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: reversed})
}

// SubUserMiddleware checks if a request is from a sub-user and restricts container access
func SubUserMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowed, isSubUser := subUserAllowedContainers(r)
		if !isSubUser {
			next(w, r)
			return
		}

		path := r.URL.Path
		containerPrefix := "/api/containers/"
		containerListPath := "/api/containers"
		tasksPath := "/api/tasks"
		if strings.HasPrefix(path, "/api/v1/") {
			containerPrefix = "/api/v1/containers/"
			containerListPath = "/api/v1/containers"
			tasksPath = "/api/v1/tasks"
		}
		if path == tasksPath && r.Method == http.MethodGet {
			next(w, r)
			return
		}

		imagesEnabledPath := "/api/images/enabled"
		if strings.HasPrefix(path, "/api/v1/") {
			imagesEnabledPath = "/api/v1/images/enabled"
		}
		if path == imagesEnabledPath && r.Method == http.MethodGet {
			next(w, r)
			return
		}

		if path == containerListPath {
			if r.Method != http.MethodGet {
				jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Sub-users cannot create containers"})
				return
			}
			next(w, r)
			return
		}

		if strings.HasPrefix(path, containerPrefix) {
			rest := path[len(containerPrefix):]
			parts := splitPath(rest)
			if len(parts) > 0 && parts[0] != "" {
				c := containerByIdentifier(parts[0])
				if c == nil || !isContainerAllowed(allowed, c) {
					jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
					return
				}
				action := ""
				if len(parts) > 1 {
					action = strings.Join(parts[1:], "/")
				}
				if c.PolicyBlocked && isSubUserBlockedAction(action, r.Method) {
					jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: policyBlockedMessage(c)})
					return
				}
				if !isSubUserContainerActionAllowed(action, r.Method) {
					jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Action is not allowed for this link"})
					return
				}
			}
			next(w, r)
			return
		}

		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied"})
		return
	}
}

func filterContainersForRequest(r *http.Request, containers []config.Container) []config.Container {
	allowed, restricted := requestAllowedContainers(r)
	if !restricted {
		return containers
	}
	filtered := make([]config.Container, 0, len(containers))
	for _, c := range containers {
		if isContainerAllowed(allowed, &c) {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func filterTasksForRequest(r *http.Request, tasks []*Task) []*Task {
	filtered := make([]*Task, 0, len(tasks))
	for _, task := range tasks {
		if isTaskAllowedForRequest(r, task) {
			filtered = append(filtered, task)
		}
	}
	return filtered
}

func isTaskAllowedForRequest(r *http.Request, task *Task) bool {
	allowed, restricted := requestAllowedContainers(r)
	if !restricted {
		return true
	}
	if task == nil {
		return false
	}
	if c := config.FindContainer(task.ContainerID); c != nil && isContainerAllowed(allowed, c) {
		return true
	}
	if task.ContainerName != "" {
		if c := config.FindContainerByName(task.ContainerName); c != nil && isContainerAllowed(allowed, c) {
			return true
		}
	}
	if task.Config.Name != "" {
		if c := config.FindContainerByName(task.Config.Name); c != nil && isContainerAllowed(allowed, c) {
			return true
		}
	}
	return false
}

func isContainerAllowed(allowed subUserAccess, c *config.Container) bool {
	if c == nil {
		return false
	}
	if c.UUID != "" && allowed.uuids[c.UUID] {
		return true
	}
	return c.Name != "" && allowed.names[c.Name]
}

func isSubUserBlockedAction(action string, method string) bool {
	if action == "" {
		return method != http.MethodGet
	}
	switch action {
	case "usage", "traffic", "history":
		return method != http.MethodGet
	default:
		return true
	}
}

func policyBlockedMessage(c *config.Container) string {
	if c != nil && c.PolicyBlockedReason != "" {
		return "虚拟机被策略临时封禁：" + c.PolicyBlockedReason
	}
	return "虚拟机被策略临时封禁"
}

func isSubUserContainerActionAllowed(action string, method string) bool {
	if action == "" {
		return method == http.MethodGet
	}
	switch {
	case action == "usage" || action == "traffic" || action == "history" || action == "random-port":
		return method == http.MethodGet
	case action == "snapshots":
		return method == http.MethodGet || method == http.MethodPost
	case action == "snapshots/schedule":
		return method == http.MethodPost
	case strings.HasPrefix(action, "snapshots/"):
		return method == http.MethodDelete || method == http.MethodPost
	case action == "start" || action == "stop" || action == "restart" || action == "reinstall" || action == "reset-password":
		return method == http.MethodPost
	case strings.HasPrefix(action, "port-mappings/"):
		return method == http.MethodPut
	default:
		return false
	}
}

func activeSubUserContainerUUIDs(su *config.SubUser) []string {
	uuids := make([]string, 0, len(su.ContainerUUIDs))
	// 租户绑定：可访问该租户下全部容器
	if strings.TrimSpace(su.Tenant) != "" {
		containers := config.GetContainers()
		for _, c := range containers {
			if strings.TrimSpace(c.Tenant) == strings.TrimSpace(su.Tenant) && c.UUID != "" {
				uuids = appendUniqueString(uuids, c.UUID)
			}
		}
	}
	for _, uuid := range su.ContainerUUIDs {
		if c := config.FindContainerByUUID(uuid); c != nil {
			uuids = appendUniqueString(uuids, c.UUID)
		}
	}
	if len(uuids) > 0 {
		return uuids
	}
	return subUserContainerUUIDs(su.ContainerNames)
}

func subUserContainerUUIDs(containerNames []string) []string {
	uuids := make([]string, 0, len(containerNames))
	for _, name := range containerNames {
		if c := config.FindContainerByName(name); c != nil && c.UUID != "" {
			uuids = appendUniqueString(uuids, c.UUID)
		}
	}
	return uuids
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func splitPath(path string) []string {
	parts := make([]string, 0)
	for _, p := range splitBy(path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitBy(s, sep string) []string {
	result := make([]string, 0)
	current := ""
	for _, c := range s {
		if string(c) == sep {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	result = append(result, current)
	return result
}

// SubUserListItem is the enriched sub-user info returned by the list API
type SubUserListItem struct {
	ID                   string   `json:"id"`
	Username             string   `json:"username"`
	Role                 string   `json:"role"`
	Tenant               string   `json:"tenant"`
	ContainerNames       []string `json:"container_names"`
	ContainerUUIDs       []string `json:"container_uuids"`
	AllowedImageIDs      []string `json:"allowed_image_ids"`
	ImageLimitConfigured bool     `json:"image_limit_configured"`
	CurrentImageIDs      []string `json:"current_image_ids"`
	ContainerName        string   `json:"container_name"`
	ContainerUUID        string   `json:"container_uuid"`
	AccessCode           string   `json:"access_code"`
	Password             string   `json:"password,omitempty"`
	CreatedAt            string   `json:"created_at"`
	LastLogin            string   `json:"last_login"`
	LastLoginIP          string   `json:"last_login_ip"`
	LastLoginUA          string   `json:"last_login_ua"`
}

// HandleSubUserList returns the list of all sub-users with container info
func HandleSubUserList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "subuser:read") {
		return
	}

	config.AppConfigMu.RLock()
	subUsers := append([]config.SubUser(nil), config.AppConfig.SubUsers...)
	loginLogs := append([]config.SavedLoginLog(nil), config.AppConfig.LoginLogs...)
	config.AppConfigMu.RUnlock()

	result := make([]SubUserListItem, 0, len(subUsers))
	for _, su := range subUsers {
		item := SubUserListItem{
			ID:                   su.ID,
			Username:             su.Username,
			Role:                 subUserRole(su.Role),
			Tenant:               strings.TrimSpace(su.Tenant),
			ContainerNames:       su.ContainerNames,
			ContainerUUIDs:       su.ContainerUUIDs,
			AllowedImageIDs:      effectiveSubUserAllowedImageIDs(&su),
			ImageLimitConfigured: su.ImageLimitConfigured,
			CurrentImageIDs:      subUserCurrentImageIDs(&su),
			// 访问码用于生成管理分享链接（产品设计，需在列表中提供）；
			// 登录口令为一次性凭据，仅创建/轮换时返回，列表不回显已落库明文。
			AccessCode:           su.AccessCode,
			Password:             "",
			CreatedAt:            su.CreatedAt,
		}

		// Resolve container name from first active UUID
		for _, uuid := range su.ContainerUUIDs {
			if c := config.FindContainerByUUID(uuid); c != nil {
				item.ContainerName = c.Name
				item.ContainerUUID = c.UUID
				break
			}
		}
		if item.ContainerName == "" && len(su.ContainerNames) > 0 {
			item.ContainerName = su.ContainerNames[0]
		}

		// Find last login time
		for i := len(loginLogs) - 1; i >= 0; i-- {
			log := loginLogs[i]
			if log.Username == su.Username {
				item.LastLogin = log.Time
				item.LastLoginIP = log.IP
				item.LastLoginUA = log.UserAgent
				break
			}
		}

		// Skip orphaned sub-users with no active containers
		if item.ContainerName == "" && item.ContainerUUID == "" {
			continue
		}

		result = append(result, item)
	}

	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: result})
}

// HandleSubUserAction handles actions on a specific sub-user
func HandleSubUserAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/sub-users/")
	path = strings.TrimPrefix(path, "/api/sub-users/")
	parts := strings.SplitN(path, "/", 2)
	subUserID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// Find sub-user (snapshot copy; mutations below go through MutateGlobal)
	var target *config.SubUser
	config.AppConfigMu.RLock()
	for i := range config.AppConfig.SubUsers {
		if config.AppConfig.SubUsers[i].ID == subUserID {
			copySU := config.AppConfig.SubUsers[i]
			target = &copySU
			break
		}
	}
	config.AppConfigMu.RUnlock()
	if target == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Sub-user not found"})
		return
	}

	switch {
	case action == "rotate-password" && r.Method == http.MethodPost:
		if !requireScope(w, r, "subuser:update") {
			return
		}
		password := generateRandomStr(16)
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to generate password"})
			return
		}
		var updated config.SubUser
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.SubUsers {
				if cfg.SubUsers[i].ID != subUserID {
					continue
				}
				cfg.SubUsers[i].PassHash = string(hash)
				cfg.SubUsers[i].Password = password
				cfg.SubUsers[i].Token = ""
				cfg.SubUsers[i].TokenVersion++ // invalidate all existing tokens
				updated = cfg.SubUsers[i]
				return
			}
		})
		if updated.ID == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Sub-user not found"})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{
			"password":    password,
			"access_code": updated.AccessCode,
			"username":    updated.Username,
		}})
		return

	case action == "audit-logs" && r.Method == http.MethodGet:
		if !requireScope(w, r, "audit:read") {
			return
		}
		// Filter audit logs for this sub-user
		logs := filterSubUserAuditLogs(target.Username)
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: logs})

	case action == "login-logs" && r.Method == http.MethodGet:
		if !requireScope(w, r, "loginlog:read") {
			return
		}
		// Filter login logs for this sub-user
		logs := filterSubUserLoginLogs(target.Username)
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: logs})

	case action == "images" && r.Method == http.MethodPut:
		if !requireScope(w, r, "subuser:update") {
			return
		}
		var req struct {
			AllowedImageIDs []string `json:"allowed_image_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		ids, err := normalizeAllowedImageIDs(req.AllowedImageIDs)
		if err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		var updated config.SubUser
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.SubUsers {
				if cfg.SubUsers[i].ID != subUserID {
					continue
				}
				cfg.SubUsers[i].AllowedImageIDs = ids
				cfg.SubUsers[i].ImageLimitConfigured = true
				cfg.SubUsers[i].TokenVersion++
				updated = cfg.SubUsers[i]
				return
			}
		})
		if updated.ID == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Sub-user not found"})
			return
		}
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: newSubUserResponse(updated, updated.Password)})

	case action == "role" && r.Method == http.MethodPut:
		if !requireScope(w, r, "subuser:update") {
			return
		}
		var req struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		role := subUserRole(req.Role)
		var updated config.SubUser
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.SubUsers {
				if cfg.SubUsers[i].ID != subUserID {
					continue
				}
				cfg.SubUsers[i].Role = role
				// 角色变更强制其既有 token 失效并重新登录，确保降级立即生效，
				// 避免旧 operator token 在 24h 内仍持有写权限。
				cfg.SubUsers[i].Token = ""
				cfg.SubUsers[i].TokenVersion++
				updated = cfg.SubUsers[i]
				return
			}
		})
		if updated.ID == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Sub-user not found"})
			return
		}
		auditRequest(r, "subuser.role", updated.Username, "role="+role, true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: newSubUserResponse(updated, updated.Password)})

	case action == "tenant" && r.Method == http.MethodPut:
		if !requireScope(w, r, "subuser:update") {
			return
		}
		var req struct {
			Tenant string `json:"tenant"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
		tenant := strings.TrimSpace(req.Tenant)
		var updated config.SubUser
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.SubUsers {
				if cfg.SubUsers[i].ID != subUserID {
					continue
				}
				cfg.SubUsers[i].Tenant = tenant
				cfg.SubUsers[i].TokenVersion++ // 强制重新登录，刷新租户容器范围
				updated = cfg.SubUsers[i]
				return
			}
		})
		if updated.ID == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Sub-user not found"})
			return
		}
		auditRequest(r, "subuser.tenant", updated.Username, "tenant="+tenant, true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: newSubUserResponse(updated, updated.Password)})

	default:
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Action not found"})
	}
}

func filterSubUserAuditLogs(username string) []config.AuditLog {
	config.AppConfigMu.RLock()
	logs := append([]config.AuditLog(nil), config.AppConfig.AuditLogs...)
	config.AppConfigMu.RUnlock()
	result := make([]config.AuditLog, 0)
	for i := len(logs) - 1; i >= 0; i-- {
		log := logs[i]
		if log.User == username || log.User == "user:"+username {
			result = append(result, log)
		}
	}
	if result == nil {
		result = []config.AuditLog{}
	}
	return result
}

func filterSubUserLoginLogs(username string) []config.SavedLoginLog {
	config.AppConfigMu.RLock()
	logs := append([]config.SavedLoginLog(nil), config.AppConfig.LoginLogs...)
	config.AppConfigMu.RUnlock()
	result := make([]config.SavedLoginLog, 0)
	for i := len(logs) - 1; i >= 0; i-- {
		log := logs[i]
		if log.Username == username {
			result = append(result, log)
		}
	}
	if result == nil {
		result = []config.SavedLoginLog{}
	}
	return result
}
