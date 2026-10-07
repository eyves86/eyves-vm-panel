package api

// apiv2_manage.go —— API v2：管理类写操作（Webhook / 子用户 / 管理员 / 实例安全组绑定）。
//
// 端点：
//
//	POST   /api/v2/webhooks                 创建事件订阅（URL 做 SSRF 校验，密钥仅返回一次）
//	PATCH  /api/v2/webhooks/{id}            修改订阅（名称/URL/事件类型/启停）
//	DELETE /api/v2/webhooks/{id}            删除订阅
//	POST   /api/v2/users                    创建子用户（可选绑定实例、返回一次性口令）
//	PATCH  /api/v2/users/{id}               修改子用户（角色/邮箱/租户/绑定实例/启停配额）
//	DELETE /api/v2/users/{id}               删除子用户（同时解除其容器归属）
//	POST   /api/v2/users/{id}/rotate-password  轮换口令（返回一次性新口令）
//	POST   /api/v2/admins                   创建管理员账号
//	PATCH  /api/v2/admins/{id}              修改管理员（角色/启停/重置口令）
//	DELETE /api/v2/admins/{id}              删除管理员
//	PUT    /api/v2/instances/{id}/security-groups  设置实例绑定的安全组（整体替换）
//	GET    /api/v2/instances/{id}/security-groups  查询绑定关系

import (
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/safehttp"

	"golang.org/x/crypto/bcrypt"
)

func init() {
	registerV2("POST /api/v2/webhooks", v2Auth(v2WebhookCreate))
	registerV2("PATCH /api/v2/webhooks/{id}", v2Auth(v2WebhookUpdate))
	registerV2("DELETE /api/v2/webhooks/{id}", v2Auth(v2WebhookDelete))

	registerV2("POST /api/v2/users", v2Auth(v2UserCreate))
	registerV2("PATCH /api/v2/users/{id}", v2Auth(v2UserUpdate))
	registerV2("DELETE /api/v2/users/{id}", v2Auth(v2UserDelete))
	registerV2("POST /api/v2/users/{id}/rotate-password", v2Auth(v2UserRotatePassword))

	registerV2("POST /api/v2/admins", v2Auth(v2AdminCreate))
	registerV2("PATCH /api/v2/admins/{id}", v2Auth(v2AdminUpdate))
	registerV2("DELETE /api/v2/admins/{id}", v2Auth(v2AdminDelete))

	registerV2("GET /api/v2/instances/{id}/security-groups", v2Auth(v2InstanceSecGroupsGet))
	registerV2("PUT /api/v2/instances/{id}/security-groups", v2Auth(v2InstanceSecGroupsSet))
}

// ---------------------------------------------------------------------------
// Webhook
// ---------------------------------------------------------------------------

func v2WebhookCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	var req struct {
		Name       string   `json:"name"`
		URL        string   `json:"url"`
		EventTypes []string `json:"event_types"`
		Enabled    *bool    `json:"enabled"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"name": req.Name, "url": req.URL}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	// SSRF 防护：拒绝回环/链路本地/云元数据/保留地址（与镜像下载同口径）。
	if _, err := safehttp.ValidateURL(strings.TrimSpace(req.URL)); err != nil {
		v2BadRequest(w, r, "回调地址不可用："+err.Error(), map[string]string{"url": req.URL})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	secret := randomHex(32)
	subscription := config.WebhookSubscription{
		ID:         "wh-" + randomHex(6),
		Name:       strings.TrimSpace(req.Name),
		URL:        strings.TrimSpace(req.URL),
		Secret:     secret,
		EventTypes: req.EventTypes,
		Enabled:    enabled,
		CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
	}
	config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		cfg.Webhooks = append(cfg.Webhooks, subscription)
	})
	auditRequest(r, "api.v2.webhook.create", subscription.Name, subscription.URL, true, "")
	v2Created(w, r, map[string]interface{}{
		"id": subscription.ID, "name": subscription.Name, "url": subscription.URL,
		"event_types": subscription.EventTypes, "enabled": subscription.Enabled,
		"secret":     secret, // 仅此一次返回，用于校验回调签名（HMAC-SHA256）
		"created_at": v2Time(subscription.CreatedAt),
	})
}

func v2WebhookUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	webhookID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Name       *string   `json:"name"`
		URL        *string   `json:"url"`
		EventTypes *[]string `json:"event_types"`
		Enabled    *bool     `json:"enabled"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if req.URL != nil {
		if _, err := safehttp.ValidateURL(strings.TrimSpace(*req.URL)); err != nil {
			v2BadRequest(w, r, "回调地址不可用："+err.Error(), map[string]string{"url": *req.URL})
			return
		}
	}
	found := false
	config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Webhooks {
			if cfg.Webhooks[i].ID != webhookID {
				continue
			}
			found = true
			if req.Name != nil {
				cfg.Webhooks[i].Name = strings.TrimSpace(*req.Name)
			}
			if req.URL != nil {
				cfg.Webhooks[i].URL = strings.TrimSpace(*req.URL)
			}
			if req.EventTypes != nil {
				cfg.Webhooks[i].EventTypes = *req.EventTypes
			}
			if req.Enabled != nil {
				cfg.Webhooks[i].Enabled = *req.Enabled
				if *req.Enabled {
					cfg.Webhooks[i].ConsecutiveFailures = 0
					cfg.Webhooks[i].AutoDisabledReason = ""
				}
			}
			return
		}
	})
	if !found {
		v2NotFound(w, r, "订阅不存在："+webhookID)
		return
	}
	auditRequest(r, "api.v2.webhook.update", webhookID, "", true, "")
	v2OK(w, r, map[string]interface{}{"id": webhookID, "updated": true})
}

func v2WebhookDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	webhookID := strings.TrimSpace(r.PathValue("id"))
	removed := false
	config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		out := cfg.Webhooks[:0]
		for i := range cfg.Webhooks {
			if cfg.Webhooks[i].ID == webhookID {
				removed = true
				continue
			}
			out = append(out, cfg.Webhooks[i])
		}
		cfg.Webhooks = out
	})
	if !removed {
		v2NotFound(w, r, "订阅不存在："+webhookID)
		return
	}
	auditRequest(r, "api.v2.webhook.delete", webhookID, "", true, "")
	v2NoContent(w, r)
}

// ---------------------------------------------------------------------------
// 子用户
// ---------------------------------------------------------------------------

// v2UserCreate POST /users {username, password?, email?, role?, tenant?, container_uuids?}
func v2UserCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	var req struct {
		Username       string   `json:"username"`
		Password       string   `json:"password"`
		Email          string   `json:"email"`
		Role           string   `json:"role"`
		Tenant         string   `json:"tenant"`
		ContainerUUIDs []string `json:"container_uuids"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"username": req.Username}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	username := strings.TrimSpace(req.Username)
	config.AppConfigMu.RLock()
	for _, su := range config.AppConfig.SubUsers {
		if strings.EqualFold(su.Username, username) {
			config.AppConfigMu.RUnlock()
			v2Conflict(w, r, "用户名已存在："+username)
			return
		}
	}
	config.AppConfigMu.RUnlock()

	password := strings.TrimSpace(req.Password)
	if password == "" {
		password = generateRandomStr(16)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		v2Internal(w, r, "生成口令失败："+err.Error())
		return
	}
	// 校验容器绑定：不存在的 UUID 直接拒绝，避免创建出无权实例的账号。
	names := []string{}
	for _, uuid := range req.ContainerUUIDs {
		c := config.FindContainerByUUID(uuid)
		if c == nil {
			v2NotFound(w, r, "容器不存在："+uuid)
			return
		}
		names = append(names, c.Name)
	}
	subUser := config.SubUser{
		ID:             "su-" + randomHex(6),
		Username:       username,
		Email:          strings.TrimSpace(req.Email),
		Password:       password,
		PassHash:       string(hash),
		Role:           normalizeSubUserRoleV2(req.Role),
		Tenant:         strings.TrimSpace(req.Tenant),
		ContainerNames: names,
		ContainerUUIDs: req.ContainerUUIDs,
		CreatedAt:      time.Now().Format("2006-01-02 15:04:05"),
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.SubUsers = append(cfg.SubUsers, subUser)
		// 同步容器归属，使子用户按容器授权生效。
		for i := range cfg.Containers {
			for _, uuid := range req.ContainerUUIDs {
				if cfg.Containers[i].UUID == uuid {
					cfg.Containers[i].OwnerSubUserID = subUser.ID
				}
			}
		}
	})
	auditRequest(r, "api.v2.user.create", username,
		"绑定 "+itoaV2(len(req.ContainerUUIDs))+" 个实例", true, "")
	v2Created(w, r, map[string]interface{}{
		"id": subUser.ID, "username": subUser.Username, "role": subUserRole(subUser.Role),
		"password":        password, // 一次性账号凭据
		"container_uuids": subUser.ContainerUUIDs,
	})
}

// v2UserUpdate PATCH /users/{id}
func v2UserUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	userID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Email          *string   `json:"email"`
		Role           *string   `json:"role"`
		Tenant         *string   `json:"tenant"`
		ContainerUUIDs *[]string `json:"container_uuids"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if req.ContainerUUIDs != nil {
		for _, uuid := range *req.ContainerUUIDs {
			if config.FindContainerByUUID(uuid) == nil {
				v2NotFound(w, r, "容器不存在："+uuid)
				return
			}
		}
	}
	found := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.SubUsers {
			if cfg.SubUsers[i].ID != userID && !strings.EqualFold(cfg.SubUsers[i].Username, userID) {
				continue
			}
			found = true
			if req.Email != nil {
				cfg.SubUsers[i].Email = strings.TrimSpace(*req.Email)
			}
			if req.Role != nil {
				cfg.SubUsers[i].Role = normalizeSubUserRoleV2(*req.Role)
			}
			if req.Tenant != nil {
				cfg.SubUsers[i].Tenant = strings.TrimSpace(*req.Tenant)
			}
			if req.ContainerUUIDs != nil {
				cfg.SubUsers[i].ContainerUUIDs = *req.ContainerUUIDs
				names := []string{}
				for _, uuid := range *req.ContainerUUIDs {
					if c := config.FindContainerByUUID(uuid); c != nil {
						names = append(names, c.Name)
					}
				}
				cfg.SubUsers[i].ContainerNames = names
				// 同步容器归属（加入的设为本用户，移出的清空）
				for j := range cfg.Containers {
					owned := false
					for _, uuid := range *req.ContainerUUIDs {
						if cfg.Containers[j].UUID == uuid {
							owned = true
							break
						}
					}
					if owned {
						cfg.Containers[j].OwnerSubUserID = cfg.SubUsers[i].ID
					} else if cfg.Containers[j].OwnerSubUserID == cfg.SubUsers[i].ID {
						cfg.Containers[j].OwnerSubUserID = ""
					}
				}
			}
			// 变更后强制旧令牌失效（角色/绑定可能变化）
			cfg.SubUsers[i].TokenVersion++
			return
		}
	})
	if !found {
		v2NotFound(w, r, "用户不存在："+userID)
		return
	}
	auditRequest(r, "api.v2.user.update", userID, "", true, "")
	v2OK(w, r, map[string]interface{}{"id": userID, "updated": true})
}

func v2UserDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	userID := strings.TrimSpace(r.PathValue("id"))
	removed := false
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		var subUserID string
		out := cfg.SubUsers[:0]
		for i := range cfg.SubUsers {
			if cfg.SubUsers[i].ID == userID || strings.EqualFold(cfg.SubUsers[i].Username, userID) {
				subUserID = cfg.SubUsers[i].ID
				removed = true
				continue
			}
			out = append(out, cfg.SubUsers[i])
		}
		cfg.SubUsers = out
		if subUserID != "" {
			for i := range cfg.Containers {
				if cfg.Containers[i].OwnerSubUserID == subUserID {
					cfg.Containers[i].OwnerSubUserID = ""
				}
			}
		}
	})
	if !removed {
		v2NotFound(w, r, "用户不存在："+userID)
		return
	}
	auditRequest(r, "api.v2.user.delete", userID, "并解除容器归属", true, "")
	v2NoContent(w, r)
}

func v2UserRotatePassword(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	userID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Password string `json:"password"`
	}
	_ = v2Decode(r, &req)
	password := strings.TrimSpace(req.Password)
	if password == "" {
		password = generateRandomStr(16)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		v2Internal(w, r, "生成口令失败："+err.Error())
		return
	}
	found := false
	config.MutateGlobalSaveCatalogOnly(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.SubUsers {
			if cfg.SubUsers[i].ID != userID && !strings.EqualFold(cfg.SubUsers[i].Username, userID) {
				continue
			}
			found = true
			cfg.SubUsers[i].PassHash = string(hash)
			cfg.SubUsers[i].Password = password
			cfg.SubUsers[i].TokenVersion++ // 旧令牌立刻失效
			return
		}
	})
	if !found {
		v2NotFound(w, r, "用户不存在："+userID)
		return
	}
	auditRequest(r, "api.v2.user.rotate_password", userID, "", true, "")
	v2OK(w, r, map[string]interface{}{"id": userID, "password": password})
}

// ---------------------------------------------------------------------------
// 管理员账号
// ---------------------------------------------------------------------------

func v2AdminCreate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	// 仅主管理员可创建管理员账号。
	if !strings.EqualFold(v2AuthContext(r).Username, config.AppConfig.AdminUser) {
		v2Forbidden(w, r, "仅主管理员可创建管理员账号")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	if details := v2RequiredStrings(map[string]string{"username": req.Username, "password": req.Password}); details != nil {
		v2BadRequest(w, r, "缺少必填字段", details)
		return
	}
	if err := validateStrongPassword(req.Password); err != nil {
		v2BadRequest(w, r, err.Error(), map[string]string{"password": "至少 10 位且含字母与数字"})
		return
	}
	username := strings.TrimSpace(req.Username)
	if strings.EqualFold(username, config.AppConfig.AdminUser) {
		v2Conflict(w, r, "用户名与主管理员冲突")
		return
	}
	if _, ok := config.FindAdminAccount(username); ok {
		v2Conflict(w, r, "管理员已存在："+username)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		v2Internal(w, r, "生成口令失败："+err.Error())
		return
	}
	account := config.AdminAccount{
		ID:        "adm-" + randomHex(6),
		Username:  username,
		PassHash:  string(hash),
		Role:      config.NormalizeAdminRole(req.Role),
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		cfg.Admins = append(cfg.Admins, account)
	})
	auditRequest(r, "api.v2.admin.create", username, "role="+account.Role, true, "")
	v2Created(w, r, map[string]interface{}{"id": account.ID, "username": account.Username, "role": account.Role})
}

func v2AdminUpdate(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	if !strings.EqualFold(v2AuthContext(r).Username, config.AppConfig.AdminUser) {
		v2Forbidden(w, r, "仅主管理员可修改管理员账号")
		return
	}
	adminID := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
		Password *string `json:"password"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	var newHash string
	if req.Password != nil && strings.TrimSpace(*req.Password) != "" {
		if err := validateStrongPassword(*req.Password); err != nil {
			v2BadRequest(w, r, err.Error(), map[string]string{"password": "至少 10 位且含字母与数字"})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			v2Internal(w, r, "生成口令失败："+err.Error())
			return
		}
		newHash = string(hash)
	}
	found := false
	config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Admins {
			if cfg.Admins[i].ID != adminID && !strings.EqualFold(cfg.Admins[i].Username, adminID) {
				continue
			}
			found = true
			if req.Role != nil {
				cfg.Admins[i].Role = config.NormalizeAdminRole(*req.Role)
			}
			if req.Disabled != nil {
				cfg.Admins[i].Disabled = *req.Disabled
			}
			if newHash != "" {
				cfg.Admins[i].PassHash = newHash
			}
			cfg.Admins[i].TokenVersion++ // 角色/状态/口令变化即吊销旧令牌
			return
		}
	})
	if !found {
		v2NotFound(w, r, "管理员不存在："+adminID)
		return
	}
	auditRequest(r, "api.v2.admin.update", adminID, "", true, "")
	v2OK(w, r, map[string]interface{}{"id": adminID, "updated": true})
}

func v2AdminDelete(w http.ResponseWriter, r *http.Request) {
	if !v2RequireAdmin(w, r) {
		return
	}
	if !strings.EqualFold(v2AuthContext(r).Username, config.AppConfig.AdminUser) {
		v2Forbidden(w, r, "仅主管理员可删除管理员账号")
		return
	}
	adminID := strings.TrimSpace(r.PathValue("id"))
	if adminID == "founder" {
		v2Precondition(w, r, "主管理员账号不可删除")
		return
	}
	removed := false
	config.MutateGlobalMetaOnly(func(cfg *config.EyvescloudConfig) {
		out := cfg.Admins[:0]
		for i := range cfg.Admins {
			if cfg.Admins[i].ID == adminID || strings.EqualFold(cfg.Admins[i].Username, adminID) {
				removed = true
				continue
			}
			out = append(out, cfg.Admins[i])
		}
		cfg.Admins = out
	})
	if !removed {
		v2NotFound(w, r, "管理员不存在："+adminID)
		return
	}
	auditRequest(r, "api.v2.admin.delete", adminID, "", true, "")
	v2NoContent(w, r)
}

// ---------------------------------------------------------------------------
// 实例 ↔ 安全组
// ---------------------------------------------------------------------------

func v2InstanceSecGroupsGet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	v2OK(w, r, map[string]interface{}{
		"instance_id": c.ID, "instance_name": c.Name, "security_group_ids": c.SecGroupIDs,
	})
}

// v2InstanceSecGroupsSet PUT /instances/{id}/security-groups {security_group_ids: [...]}（整体替换）
func v2InstanceSecGroupsSet(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "security:write") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	var req struct {
		SecurityGroupIDs []string `json:"security_group_ids"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	for _, groupID := range req.SecurityGroupIDs {
		if _, ok := findSecGroup(groupID); !ok {
			v2NotFound(w, r, "安全组不存在："+groupID)
			return
		}
	}
	config.MutateContainerByID(c.ID, func(target *config.Container) {
		target.SecGroupIDs = append([]string(nil), req.SecurityGroupIDs...)
	})
	auditRequest(r, "api.v2.instance.security_groups", c.Name,
		"绑定 "+itoaV2(len(req.SecurityGroupIDs))+" 个安全组", true, "")
	updated := config.FindContainer(c.ID)
	if updated == nil {
		v2NotFound(w, r, "实例不存在")
		return
	}
	v2OK(w, r, map[string]interface{}{
		"instance_id": updated.ID, "instance_name": updated.Name,
		"security_group_ids": updated.SecGroupIDs,
	})
}

// normalizeSubUserRoleV2 归一化子用户角色（operator 可写 / viewer 只读）。
// 落库前的规范化由配置层负责，这里只做输入口径统一。
func normalizeSubUserRoleV2(role string) string {
	if strings.EqualFold(strings.TrimSpace(role), "viewer") {
		return "viewer"
	}
	return "operator"
}
