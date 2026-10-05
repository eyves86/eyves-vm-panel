package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
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
	Email                string   `json:"email,omitempty"`
	Password             string   `json:"password,omitempty"`
	Role                 string   `json:"role"`
	Tenant               string   `json:"tenant"`
	ContainerNames       []string `json:"container_names"`
	ContainerUUIDs       []string `json:"container_uuids,omitempty"`
	AllowedImageIDs      []string `json:"allowed_image_ids,omitempty"`
	ImageLimitConfigured bool     `json:"image_limit_configured,omitempty"`
	CurrentImageIDs      []string `json:"current_image_ids,omitempty"`
	CreatedAt            string   `json:"created_at"`
}

func newSubUserResponse(su config.SubUser, password string) subUserResponse {
	return subUserResponse{
		ID:                   su.ID,
		Username:             su.Username,
		Email:                su.Email,
		Password:             password,
		Role:                 subUserRole(su.Role),
		Tenant:               strings.TrimSpace(su.Tenant),
		ContainerNames:       su.ContainerNames,
		ContainerUUIDs:       su.ContainerUUIDs,
		AllowedImageIDs:      effectiveSubUserAllowedImageIDs(&su),
		ImageLimitConfigured: su.ImageLimitConfigured,
		CurrentImageIDs:      subUserCurrentImageIDs(&su),
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

// HandleSubUserCreate 创建子用户。
// 请求体兼容两种模式：
//  1. 老模式：container_name 单数，自动生成用户名和密码
//  2. 新模式（推荐）：container_names 复数 + username + email + tenant + role + password（均可选）
//     - 不传任何容器 → 创建空账号，后续可通过 sub-users/{id}/bind-containers 追加
//     - 指定 username / email → 唯一性校验
//     - 指定 password → 用之；不指定 → 生成随机 16 位
func HandleSubUserCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "subuser:create") {
		return
	}

	var req struct {
		ContainerName  string   `json:"container_name,omitempty"`  // 旧字段，兼容
		ContainerNames []string `json:"container_names,omitempty"` // 新字段，可多个
		Username       string   `json:"username,omitempty"`
		Email          string   `json:"email,omitempty"`
		Tenant         string   `json:"tenant,omitempty"`
		Role           string   `json:"role,omitempty"`
		Password       string   `json:"password,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	// 归一化：把旧 container_name 并入 container_names
	containers := make([]string, 0, len(req.ContainerNames)+1)
	seen := map[string]struct{}{}
	if req.ContainerName != "" {
		containers = append(containers, req.ContainerName)
		seen[req.ContainerName] = struct{}{}
	}
	for _, name := range req.ContainerNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		containers = append(containers, name)
	}

	// 容器解析：在一次读锁内快照所需字段（UUID/Name/OwnerSubUserID/镜像限制），
	// 不持有活指针——FindContainerByIdentifier 返回的指针在锁释放后继续读
	// 会与并发 MutateGlobal 写 OwnerSubUserID 构成数据竞争。
	// 标识符支持：数字 ID / UUID / 名称。
	type containerRef struct {
		UUID              string
		Name              string
		OwnerSubUserID    string
		EffectiveImageIDs []string
	}
	var validContainers []containerRef
	{
		var missing string
		config.AppConfigMu.RLock()
		for _, ident := range containers {
			var match *config.Container
			if id, err := strconv.Atoi(ident); err == nil {
				for i := range config.AppConfig.Containers {
					if config.AppConfig.Containers[i].ID == id {
						match = &config.AppConfig.Containers[i]
						break
					}
				}
			}
			if match == nil {
				for i := range config.AppConfig.Containers {
					if config.AppConfig.Containers[i].UUID == ident {
						match = &config.AppConfig.Containers[i]
						break
					}
				}
			}
			if match == nil {
				for i := range config.AppConfig.Containers {
					if config.AppConfig.Containers[i].Name == ident {
						match = &config.AppConfig.Containers[i]
						break
					}
				}
			}
			if match == nil {
				missing = ident
				break
			}
			validContainers = append(validContainers, containerRef{
				UUID:              match.UUID,
				Name:              match.Name,
				OwnerSubUserID:    match.OwnerSubUserID,
				EffectiveImageIDs: effectiveContainerAllowedImageIDs(match),
			})
		}
		config.AppConfigMu.RUnlock()
		if missing != "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found: " + missing})
			return
		}
	}

	// 唯一性校验：username / email
	// 注意：此处先做初筛（排除空值）；合并场景下目标自身的用户名/邮箱
	// 会在幂等检查确定 mergeOwnerID 后再按"排除自身"复检，保证 upsert 语义。
	req.Username = strings.TrimSpace(req.Username)
	emailNorm, err := config.NormalizeEmail(req.Email)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid email: " + err.Error()})
		return
	}

	// 密码处理：指定了就校验长度，否则生成随机 16 位
	password := req.Password
	if password == "" {
		password = generateRandomStr(16)
	}
	if len(password) < 8 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Password must be at least 8 characters long"})
		return
	}

	hash, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if hashErr != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to hash password"})
		return
	}

	// ========== 幂等检查：validContainers 是否已被某个 SubUser 绑定 ==========
	// 绑定来源同时看两个维度：
	//   1) Container.OwnerSubUserID（新数据权威字段）
	//   2) SubUser.ContainerUUIDs（老数据兼容：迁移时多用户共享的容器不会回填 owner）
	// 规则：
	//   - 全部未绑定 → 走"创建新 SubUser"路径（路径 B）
	//   - 部分/全部已绑到**同一个** SubUser → 走"合并到已有 SubUser"路径（路径 A，兼容旧行为）
	//   - 绑到**不同** SubUser → 拒绝，提示冲突（让超管先厘清关系）
	type existingResult struct {
		su       config.SubUser
		password string
		message  string
	}
	var mergedResult *existingResult
	var mergeOwnerID string

	if len(validContainers) > 0 {
		// 先扫一遍，看 validContainers 分别被哪些 SubUser 绑（owner 字段 + 老列表双来源）
		boundOwners := map[string]struct{}{} // ownerSubUserID 集合
		for _, c := range validContainers {
			if c.OwnerSubUserID != "" {
				boundOwners[c.OwnerSubUserID] = struct{}{}
			}
		}
		config.AppConfigMu.RLock()
		for i := range config.AppConfig.SubUsers {
			su := &config.AppConfig.SubUsers[i]
			for _, c := range validContainers {
				if stringContains(su.ContainerUUIDs, c.UUID) {
					boundOwners[su.ID] = struct{}{}
					break
				}
			}
		}
		config.AppConfigMu.RUnlock()

		switch len(boundOwners) {
		case 0:
			// 全部未绑定 → 走创建路径（下面的 B 分支）

		case 1:
			// 全绑到同一个 SubUser（幂等合并）
			for id := range boundOwners {
				mergeOwnerID = id
			}
			// 合并前一致性预检（在读锁内取目标的 username/email）：
			//  1) 目标在检查与落库之间被删除 → 拒绝，不落入创建路径"偷"容器
			//  2) 请求 username/email 与目标**已有值**冲突 → 409。
			//     合并只允许"追加容器 + 补缺失字段"，改名/改邮箱必须走 edit 端点，
			//     避免超管以为在建新账号、实际静默改掉了既有账号。
			{
				config.AppConfigMu.RLock()
				targetFound := false
				targetUsername, targetEmail := "", ""
				for i := range config.AppConfig.SubUsers {
					if config.AppConfig.SubUsers[i].ID == mergeOwnerID {
						targetFound = true
						targetUsername = config.AppConfig.SubUsers[i].Username
						targetEmail = strings.TrimSpace(config.AppConfig.SubUsers[i].Email)
						break
					}
				}
				config.AppConfigMu.RUnlock()
				if !targetFound {
					jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "The sub-user bound to these containers was removed concurrently; retry the request"})
					return
				}
				if req.Username != "" && req.Username != targetUsername {
					jsonResponse(w, http.StatusConflict, APIResponse{
						Success: false,
						Message: fmt.Sprintf("Containers already belong to sub-user %q; the requested username %q mismatches. Use the edit endpoint to rename, or unbind first.", targetUsername, req.Username),
					})
					return
				}
				if emailNorm != "" && targetEmail != "" && emailNorm != targetEmail {
					jsonResponse(w, http.StatusConflict, APIResponse{
						Success: false,
						Message: fmt.Sprintf("Containers already belong to sub-user %q; the requested email mismatches the one on record. Use the edit endpoint to change it.", targetUsername),
					})
					return
				}
				// 唯一性校验（排除目标自身，保证传目标自己的用户名/邮箱不误报冲突）
				if req.Username != "" || emailNorm != "" {
					if config.SubUserUsernameOrEmailExists(req.Username, emailNorm, mergeOwnerID) {
						jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Username or email already registered"})
						return
					}
				}
			}
			var merged *existingResult
			config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
				for i := range cfg.SubUsers {
					su := &cfg.SubUsers[i]
					if su.ID != mergeOwnerID {
						continue
					}
					// 追加容器
					for _, c := range validContainers {
						if !stringContains(su.ContainerUUIDs, c.UUID) {
							su.ContainerUUIDs = append(su.ContainerUUIDs, c.UUID)
							su.ContainerNames = appendUniqueString(su.ContainerNames, c.Name)
						}
					}
					// ImageLimitConfigured 未覆盖时从容器继承
					if !su.ImageLimitConfigured && len(su.AllowedImageIDs) == 0 && len(validContainers) > 0 {
						su.AllowedImageIDs = validContainers[0].EffectiveImageIDs
						su.ImageLimitConfigured = true
					}
					// 邮箱缺失时补填（已有值时上面的预检已保证一致或为空）
					if su.Email == "" && emailNorm != "" {
						su.Email = emailNorm
					}
					// 密码策略：
					//  - 超管显式传了 password → 覆盖（等价重置，失效旧 token）
					//  - 没传 && PassHash 为空（无可用凭据的异常数据）→ 生成随机密码
					//  - 没传 && PassHash 有效 → **绝不动密码**（用户可能已自助改密，
					//    此处重置会把用户改过的密码悄悄覆盖并踢下线）
					password := su.Password
					message := "Sub-user link returned (merged)"
					if req.Password != "" {
						hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
						if err == nil {
							su.PassHash = string(hash)
							su.Password = req.Password
							su.TokenVersion++
							password = req.Password
							message = "Sub-user password updated and containers merged"
						}
					} else if su.PassHash == "" {
						password = generateRandomStr(16)
						hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
						if err == nil {
							su.PassHash = string(hash)
							su.Password = password
							su.TokenVersion++
							message = "Sub-user password generated and containers merged"
						}
					} else {
						// 保留既有密码；落库明文缺失时响应里不回显
						if password == "" {
							message = "Sub-user link returned (merged; password unchanged)"
						}
					}
					// 同步 OwnerSubUserID 回填到所有 validContainers（仅当为空时）
					for _, c := range validContainers {
						if target := config.FindContainerInConfigUnlocked(cfg, c.UUID); target != nil {
							if target.OwnerSubUserID == "" {
								target.OwnerSubUserID = su.ID
							}
						}
					}
					cp := *su
					merged = &existingResult{su: cp, password: password, message: message}
					return
				}
			})
			if merged != nil {
				mergedResult = merged
			} else {
				// 目标在预检后、落库前被并发删除：拒绝而不是继续创建新账号
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "The sub-user bound to these containers was removed concurrently; retry the request"})
				return
			}

		default:
			// 绑到了不同 SubUser → 拒绝
			ownerIDs := make([]string, 0, len(boundOwners))
			for id := range boundOwners {
				ownerIDs = append(ownerIDs, id)
			}
			config.AppConfigMu.RLock()
			var conflictNames []string
			for i := range config.AppConfig.SubUsers {
				su := &config.AppConfig.SubUsers[i]
				if !stringContains(ownerIDs, su.ID) {
					continue
				}
				for _, c := range validContainers {
					if c.OwnerSubUserID == su.ID || stringContains(su.ContainerUUIDs, c.UUID) {
						conflictNames = append(conflictNames, c.Name+" → "+su.Username)
						break
					}
				}
			}
			config.AppConfigMu.RUnlock()
			jsonResponse(w, http.StatusConflict, APIResponse{
				Success: false,
				Message: fmt.Sprintf("Containers are already owned by different sub-users: %s. Remove existing bindings first.",
					strings.Join(conflictNames, "; ")),
			})
			return
		}
	}

	// ========== 路径 A：幂等合并成功 ==========
	if mergedResult != nil {
		resp := newSubUserResponse(mergedResult.su, mergedResult.password)
		auditDetail := fmt.Sprintf("已合并子用户 %s, 追加容器", mergedResult.su.Username)
		for _, c := range validContainers {
			auditDetail += ", " + c.Name
		}
		config.AddAuditLog("创建子用户（幂等合并）", mergedResult.su.Username, auditDetail, "admin")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: mergedResult.message, Data: resp})
		return
	}

	// ========== 路径 B：创建全新 SubUser ==========
	// 创建场景的唯一性校验：不排除任何已有账号
	if req.Username != "" || emailNorm != "" {
		if config.SubUserUsernameOrEmailExists(req.Username, emailNorm, "") {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Username or email already registered"})
			return
		}
	}

	// 生成或使用指定 username
	username := req.Username
	if username == "" {
		username = "user-" + generateRandomStr(8)
	}

	// 构建新 SubUser
	subUser := config.SubUser{
		ID:           "sub-" + generateRandomStr(8),
		Username:     username,
		Email:        emailNorm,
		Password:     password,
		PassHash:     string(hash),
		Role:         subUserRole(req.Role),
		Tenant:       strings.TrimSpace(req.Tenant),
		CreatedAt:    time.Now().Format("2006-01-02 15:04:05"),
		TokenVersion: 0,
	}
	for _, c := range validContainers {
		subUser.ContainerNames = appendUniqueString(subUser.ContainerNames, c.Name)
		subUser.ContainerUUIDs = appendUniqueString(subUser.ContainerUUIDs, c.UUID)
	}
	// 绑定容器的 allowed_image_ids 作为初始值（仅当超管没显式覆盖时）
	if len(validContainers) > 0 {
		subUser.AllowedImageIDs = validContainers[0].EffectiveImageIDs
		subUser.ImageLimitConfigured = true
	}

	// 落库：加 SubUser + 把每个 Container 的 OwnerSubUserID 补上（仅当还没绑 owner 时）
	var auditTargets []string
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		cfg.SubUsers = append(cfg.SubUsers, subUser)
		for _, c := range validContainers {
			if target := config.FindContainerInConfigUnlocked(cfg, c.UUID); target != nil {
				if target.OwnerSubUserID == "" {
					target.OwnerSubUserID = subUser.ID
				}
			}
			auditTargets = append(auditTargets, c.Name)
		}
	})

	auditDetail := fmt.Sprintf("用户: %s, 邮箱: %s", username, emailNorm)
	if len(auditTargets) > 0 {
		auditDetail += ", 绑定容器: " + strings.Join(auditTargets, ",")
	} else {
		auditDetail += ", 空账号（未绑定容器）"
	}
	config.AddAuditLog("创建子用户", username, auditDetail, "admin")

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
		// TurnstileToken 是 Cloudflare Turnstile 人机验证一次性 token；
		// 用户登录启用 Turnstile 时必填（校验发生在密码比对之前）。
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	ip := clientIP(r)
	clientUA := r.Header.Get("User-Agent")
	// 限流桶键只用 IP：用户名是攻击者可控输入（且下方匹配用 EqualFold + 邮箱
	// 归一化，同一个账号有多种输入形式），把它含进键名等于给攻击者一把
	// "换个写法就重置配额"的钥匙。对齐 v2 登录的做法（apiv2_auth.go）。
	rateKey := ip + "|subuser-login"
	if loginRateLimited(w, rateKey) {
		return
	}

	// Turnstile 人机验证（若启用）：先于密码比对，失败同样计入限流。
	if !requireTurnstile(w, r, turnstileEnabledForUser(), rateKey, req.TurnstileToken) {
		return
	}

	// Find sub-user (snapshot under the read lock so a concurrent sub-user
	// edit cannot tear the slice while it is being scanned)
	config.AppConfigMu.RLock()
	subUsers := append([]config.SubUser(nil), config.AppConfig.SubUsers...)
	config.AppConfigMu.RUnlock()
	reqNorm := strings.ToLower(strings.TrimSpace(req.Username))
	for _, su := range subUsers {
		matched := strings.EqualFold(su.Username, req.Username)
		if !matched && su.Email != "" {
			matched = strings.ToLower(strings.TrimSpace(su.Email)) == reqNorm
		}
		if matched {
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

				setSessionCookie(w, r, tokenStr)
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
		// TurnstileToken：用户登录启用 Turnstile 时访问码登录同样必须验证。
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	// Find sub-user by access code
	ip := clientIP(r)
	clientUA := r.Header.Get("User-Agent")
	// 访问码是"被猜测的秘密"，绝不可作为限流桶键——否则攻击者每换一个猜测值
	// 就得到一个全新配额，等于无限爆破（渗透实测：47 个随机码 0 次 429）。
	// 桶键只保留 IP，与子用户密码登录同口径。
	rateKey := ip + "|subuser-accesscode"
	if loginRateLimited(w, rateKey) {
		return
	}

	// Turnstile 人机验证（若启用）：先于密码比对，失败同样计入限流。
	if !requireTurnstile(w, r, turnstileEnabledForUser(), rateKey, req.TurnstileToken) {
		return
	}

	config.AppConfigMu.RLock()
	containers := append([]config.Container(nil), config.AppConfig.Containers...)
	config.AppConfigMu.RUnlock()
	// 常量时间比较访问码（审计 H-8）：遍历全量候选而不提前 return，
	// 避免通过响应耗时差异逐个字符猜测访问码。访问码是「机器级」的：
	// 命中即唯一确定一台容器。
	var matched *config.Container
	for i := range containers {
		if containers[i].AccessCode != "" &&
			subtle.ConstantTimeCompare([]byte(containers[i].AccessCode), []byte(req.Code)) == 1 {
			matched = &containers[i]
		}
	}
	if matched == nil {
		// Unknown access code: throttle further attempts from this identity.
		loginLimiter.recordFail(rateKey)
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid access code"})
		return
	}

	c := *matched
	// 访问码口令与账号密码是两套独立凭据：此处只校验这台机器自己的访问码口令，
	// 绝不回退到账号密码。空口令（未生成或解密失败）视为不可用直接拒绝。
	// 常量时间比较，避免口令前缀的时序侧信道。
	if c.AccessCodePassword == "" ||
		subtle.ConstantTimeCompare([]byte(c.AccessCodePassword), []byte(req.Password)) != 1 {
		loginLimiter.recordFail(rateKey)
		config.AddLoginLog(c.Name, ip, clientUA, false)
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Invalid password"})
		return
	}

	// 访问码会话以容器属主子用户为令牌主体（沿用子用户 JWT 语义），但授权范围
	// 仅含这一台容器——「一个访问码 = 一台机器」单机登录。
	owner, ok := findSubUserOwningContainer(c)
	if !ok || owner == nil {
		loginLimiter.recordFail(rateKey)
		config.AddLoginLog(c.Name, ip, clientUA, false)
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "该机器尚未绑定账号，请先在管理端「管理链接」中启用"})
		return
	}

	loginLimiter.reset(rateKey)
	// 标记会话来源：访问码会话禁止修改账号密码（见 HandleSubUserChangePassword）。
	tokenStr := newSubUserTokenWithOrigin(owner.Username, []string{c.UUID}, owner.Role, time.Now().Add(24*time.Hour), owner.TokenVersion, subUserOriginAccessCode)
	config.AddLoginLog(owner.Username, ip, clientUA, true)

	setSessionCookie(w, r, tokenStr)
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"token":           tokenStr,
			"username":        owner.Username,
			"role":            subUserRole(owner.Role),
			"container_uuids": []string{c.UUID},
			"container_name":  c.Name,
		},
	})
}

// findSubUserOwningContainer 解析某台容器的属主子用户：优先用容器上的
// OwnerSubUserID，缺失时回退到按绑定（UUID 或名称）扫描子用户列表。
func findSubUserOwningContainer(c config.Container) (*config.SubUser, bool) {
	if id := strings.TrimSpace(c.OwnerSubUserID); id != "" {
		if su, ok := config.FindSubUserByID(id); ok && su != nil {
			return su, true
		}
	}
	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	for i := range config.AppConfig.SubUsers {
		su := &config.AppConfig.SubUsers[i]
		if c.UUID != "" && stringContains(su.ContainerUUIDs, c.UUID) {
			cp := *su
			return &cp, true
		}
		if c.Name != "" && stringContains(su.ContainerNames, c.Name) {
			cp := *su
			return &cp, true
		}
	}
	return nil, false
}

// 子用户令牌来源：区分「账号登录（用户名/邮箱 + 账号密码）」与「访问码登录
// （访问码 + 访问码口令）」。访问码会话可管理已绑定容器，但禁止修改账号密码
// 与轮换账号密码（访问码是分享凭据，持有者不应能锁定/接管账号本身）。
const (
	subUserOriginAccount    = "account"
	subUserOriginAccessCode = "access_code"
)

func newSubUserToken(username string, containerUUIDs []string, expiresAt time.Time, tokenVersion int) string {
	return newSubUserTokenWithRole(username, containerUUIDs, "", expiresAt, tokenVersion)
}

func newSubUserTokenWithRole(username string, containerUUIDs []string, role string, expiresAt time.Time, tokenVersion int) string {
	return newSubUserTokenWithOrigin(username, containerUUIDs, role, expiresAt, tokenVersion, subUserOriginAccount)
}

func newSubUserTokenWithOrigin(username string, containerUUIDs []string, role string, expiresAt time.Time, tokenVersion int, origin string) string {
	claims := jwt.MapClaims{
		"sub_user":        username,
		"container_uuids": containerUUIDs,
		"role":            subUserRole(role),
		"token_version":   tokenVersion,
		"iss":             jwtIssuer,
		"aud":             jwtAudience,
		"exp":             expiresAt.Unix(),
		"iat":             time.Now().Unix(),
	}
	if origin != "" {
		claims["via"] = origin
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
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
		// H3 设计：未绑定容器（container_uuids 为空）的 API Key 不做容器级限制，
		// 访问控制交由 scope 完成。返回 restricted=false 即放行全部容器。
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

// subUserSessionVia 返回子用户会话的来源（account / access_code；未知时为空）。
func subUserSessionVia(r *http.Request) string {
	if ctx, ok := authContextFromRequest(r); ok && ctx.Type == authTypeSubUser {
		return ctx.Via
	}
	if claims, ok := claimsFromRequest(r); ok {
		if via, _ := claims["via"].(string); via != "" {
			return via
		}
	}
	return ""
}

// isAccessCodeSession 判断当前是否为「访问码登录」会话。此类会话可管理已绑定
// 容器，但禁止修改账号密码与轮换账号密码。
func isAccessCodeSession(r *http.Request) bool {
	return subUserSessionVia(r) == subUserOriginAccessCode
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
		c.VNCPassword = ""
		c.SSHHostKey = ""
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

	p := parsePagination(r)
	if p.Invalid {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST",
			"page must be >= 1 and page_size within [1, 200]")
		return
	}
	if p.Requested {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true,
			Data: pagedEnvelope(paginate(reversed, p), len(reversed), p.Page, p.PageSize)})
		return
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
		// 任务历史与单任务详情允许子用户 GET 访问：
		// 列表在 HandleTaskHistory 内按 actor 过滤，详情在 HandleTaskDetail 内按容器可见性二次校验，
		// 因此这里只放行形状匹配的读请求，避免历史/详情对子用户完全不可见。
		if r.Method == http.MethodGet && strings.HasPrefix(path, tasksPath+"/") {
			rest := strings.Trim(path[len(tasksPath)+1:], "/")
			if rest == "history" || (rest != "" && !strings.Contains(rest, "/")) {
				next(w, r)
				return
			}
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

// stringContains returns true if value is present in values.
func stringContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// removeString returns a new slice with the first occurrence of value removed.
func removeString(values []string, value string) []string {
	out := make([]string, 0, len(values))
	removed := false
	for _, v := range values {
		if !removed && v == value {
			removed = true
			continue
		}
		out = append(out, v)
	}
	return out
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
	Email                string   `json:"email,omitempty"`
	Role                 string   `json:"role"`
	Tenant               string   `json:"tenant"`
	ContainerNames       []string `json:"container_names"`
	ContainerUUIDs       []string `json:"container_uuids"`
	AllowedImageIDs      []string `json:"allowed_image_ids"`
	ImageLimitConfigured bool     `json:"image_limit_configured"`
	CurrentImageIDs      []string `json:"current_image_ids,omitempty"`
	ContainerName        string   `json:"container_name"`
	ContainerUUID        string   `json:"container_uuid"`
	Password             string   `json:"password,omitempty"`
	CreatedAt            string   `json:"created_at"`
	LastLogin          string `json:"last_login"`
	LastLoginIP        string `json:"last_login_ip"`
	LastLoginUA        string `json:"last_login_ua"`
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
			Email:                su.Email,
			Role:                 subUserRole(su.Role),
			Tenant:               strings.TrimSpace(su.Tenant),
			ContainerNames:       su.ContainerNames,
			ContainerUUIDs:       su.ContainerUUIDs,
			AllowedImageIDs:      effectiveSubUserAllowedImageIDs(&su),
			ImageLimitConfigured: su.ImageLimitConfigured,
			CurrentImageIDs:      subUserCurrentImageIDs(&su),
			// 账号登录口令为一次性凭据，仅创建/轮换时返回，列表不回显已落库明文。
			// 访问码/访问码口令已下沉到「机器级」（见 Container.AccessCode），
			// 不再挂在子用户上。
			Password:  "",
			CreatedAt: su.CreatedAt,
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

		// Skip orphaned sub-users whose containers are all gone (legacy data);
		// but keep new-style empty accounts (created without containers) visible
		// so admins can bind containers to them later.
		hasRecordedBinding := len(su.ContainerNames) > 0 || len(su.ContainerUUIDs) > 0
		if item.ContainerName == "" && item.ContainerUUID == "" && hasRecordedBinding {
			continue
		}

		result = append(result, item)
	}

	// 服务端搜索（企业级万级子用户：下拉选择器按关键字过滤，避免下发全量）。
	// 命中范围：用户名 / 邮箱 / ID / 租户 / 角色（不区分大小写子串）。
	if q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search"))); q != "" {
		filtered := result[:0]
		for _, it := range result {
			if strings.Contains(strings.ToLower(it.Username), q) ||
				strings.Contains(strings.ToLower(it.Email), q) ||
				strings.Contains(strings.ToLower(it.ID), q) ||
				strings.Contains(strings.ToLower(it.Tenant), q) ||
				strings.Contains(strings.ToLower(it.Role), q) {
				filtered = append(filtered, it)
			}
		}
		result = filtered
	}

	p := parsePagination(r)
	if p.Invalid {
		errResponse(w, http.StatusBadRequest, "INVALID_REQUEST",
			"page must be >= 1 and page_size within [1, 200]")
		return
	}
	if p.Requested {
		jsonResponse(w, http.StatusOK, APIResponse{Success: true,
			Data: pagedEnvelope(paginate(result, p), len(result), p.Page, p.PageSize)})
		return
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
			"password": password,
			"username": updated.Username,
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

	case action == "edit" && r.Method == http.MethodPut:
		// 超管编辑 SubUser 基本信息：username / email / role / password（均可选填）。
		if !requireScope(w, r, "subuser:update") {
			return
		}
		var req struct {
			Username string `json:"username"`
			Email    string `json:"email"`
			Role     string `json:"role"`
			Password string `json:"password"` // 超管可选指定新密码
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}

		newUsername := strings.TrimSpace(req.Username)
		newEmail, err := config.NormalizeEmail(req.Email)
		if err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid email: " + err.Error()})
			return
		}

		// 唯一性校验：准备修改的新 username / email 不能与其他 SubUser 冲突
		if newUsername == "" && newEmail == "" && req.Role == "" && req.Password == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "No fields to update"})
			return
		}
		if newUsername != "" {
			if config.SubUserUsernameOrEmailExists(newUsername, "", subUserID) {
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Username already taken"})
				return
			}
		}
		if newEmail != "" {
			if config.SubUserUsernameOrEmailExists("", newEmail, subUserID) {
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Email already registered"})
				return
			}
		}

		// 新密码 hash（只有提供了才算）
		var newPassHash string
		if req.Password != "" {
			if len(req.Password) < 8 {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Password must be at least 8 characters long"})
				return
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
			if err != nil {
				jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to hash password"})
				return
			}
			newPassHash = string(hash)
		}

		// 落库
		var updated config.SubUser
		var usernameChanged bool
		var emailChanged bool
		var passwordChanged bool
		config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.SubUsers {
				if cfg.SubUsers[i].ID != subUserID {
					continue
				}
				if newUsername != "" && cfg.SubUsers[i].Username != newUsername {
					usernameChanged = true
					cfg.SubUsers[i].Username = newUsername
				}
				if newEmail != "" && strings.TrimSpace(cfg.SubUsers[i].Email) != newEmail {
					emailChanged = true
					cfg.SubUsers[i].Email = newEmail
				}
				if req.Role != "" {
					cfg.SubUsers[i].Role = subUserRole(req.Role)
				}
				if newPassHash != "" {
					passwordChanged = true
					cfg.SubUsers[i].PassHash = newPassHash
					cfg.SubUsers[i].Password = ""
				}
				// 改 username 或改密码 → token 里存的是 username 和 PassHash，必须失效
				if usernameChanged || passwordChanged {
					cfg.SubUsers[i].TokenVersion++
				}
				updated = cfg.SubUsers[i]
				return
			}
		})
		if updated.ID == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Sub-user not found"})
			return
		}

		// 审计日志
		var parts []string
		if usernameChanged {
			parts = append(parts, "username="+updated.Username)
		}
		if emailChanged {
			parts = append(parts, "email="+updated.Email)
		}
		if passwordChanged {
			parts = append(parts, "password=changed")
		}
		auditRequest(r, "subuser.edit", updated.Username, strings.Join(parts, ","), true, "")

		resp := newSubUserResponse(updated, "")
		resp.Email = updated.Email
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: resp})

	case action == "delete" && r.Method == http.MethodDelete:
		// 删除子用户：清空其名下容器的 OwnerSubUserID（容器保留，仅解绑归属）。
		if !requireScope(w, r, "subuser:update") {
			return
		}
		var removed config.SubUser
		var freedContainers []string
		saveErr := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.SubUsers {
				if cfg.SubUsers[i].ID != subUserID {
					continue
				}
				removed = cfg.SubUsers[i]
				cfg.SubUsers = append(cfg.SubUsers[:i], cfg.SubUsers[i+1:]...)
				break
			}
			if removed.ID == "" {
				return
			}
			for i := range cfg.Containers {
				if cfg.Containers[i].OwnerSubUserID == subUserID {
					cfg.Containers[i].OwnerSubUserID = ""
					freedContainers = append(freedContainers, cfg.Containers[i].Name)
				}
			}
		})
		if removed.ID == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Sub-user not found"})
			return
		}
		if saveErr != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save config"})
			return
		}
		auditRequest(r, "subuser.delete", removed.Username, "freed="+strings.Join(freedContainers, ","), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
			"id":               removed.ID,
			"username":         removed.Username,
			"freed_containers": freedContainers,
		}})

	case action == "bind-containers" && r.Method == http.MethodPut:
		// 整体替换该子用户的容器绑定集：{containers: [id/uuid/name 列表]}。
		// 已绑定给其他子用户的容器返回 409（属主转移请走 PUT /containers/{id}/owner）。
		if !requireScope(w, r, "subuser:update") {
			return
		}
		var req struct {
			Containers []string `json:"containers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}

		// 解析并校验容器标识（支持数字 ID / UUID / 名称）
		type bindRef struct {
			UUID string
			Name string
		}
		seen := map[string]struct{}{}
		binds := make([]bindRef, 0, len(req.Containers))
		conflict := ""
		missing := ""
		{
			config.AppConfigMu.RLock()
			for _, ident := range req.Containers {
				ident = strings.TrimSpace(ident)
				if ident == "" {
					continue
				}
				var match *config.Container
				if id, err := strconv.Atoi(ident); err == nil {
					for i := range config.AppConfig.Containers {
						if config.AppConfig.Containers[i].ID == id {
							match = &config.AppConfig.Containers[i]
							break
						}
					}
				}
				if match == nil {
					for i := range config.AppConfig.Containers {
						if config.AppConfig.Containers[i].UUID == ident {
							match = &config.AppConfig.Containers[i]
							break
						}
					}
				}
				if match == nil {
					for i := range config.AppConfig.Containers {
						if config.AppConfig.Containers[i].Name == ident {
							match = &config.AppConfig.Containers[i]
							break
						}
					}
				}
				if match == nil {
					missing = ident
					break
				}
				if _, dup := seen[match.UUID]; dup {
					continue
				}
				seen[match.UUID] = struct{}{}
				if match.OwnerSubUserID != "" && match.OwnerSubUserID != subUserID {
					conflict = match.Name
					break
				}
				binds = append(binds, bindRef{UUID: match.UUID, Name: match.Name})
			}
			config.AppConfigMu.RUnlock()
			if missing != "" {
				jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found: " + missing})
				return
			}
			if conflict != "" {
				jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Container already owned by another sub-user: " + conflict})
				return
			}
		}

		uuids := make([]string, 0, len(binds))
		names := make([]string, 0, len(binds))
		for _, b := range binds {
			uuids = append(uuids, b.UUID)
			names = append(names, b.Name)
		}
		newUUIDSet := make(map[string]struct{}, len(uuids))
		for _, u := range uuids {
			newUUIDSet[u] = struct{}{}
		}

		var updated config.SubUser
		var unboundNames []string
		saveErr := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
			for i := range cfg.SubUsers {
				if cfg.SubUsers[i].ID != subUserID {
					continue
				}
				updated = cfg.SubUsers[i]
				// 先解绑：旧集合中不再出现在新集合的容器清空归属
				for _, u := range cfg.SubUsers[i].ContainerUUIDs {
					if _, keep := newUUIDSet[u]; keep {
						continue
					}
					for j := range cfg.Containers {
						if cfg.Containers[j].UUID == u && cfg.Containers[j].OwnerSubUserID == subUserID {
							cfg.Containers[j].OwnerSubUserID = ""
							unboundNames = append(unboundNames, cfg.Containers[j].Name)
						}
					}
				}
				cfg.SubUsers[i].ContainerUUIDs = uuids
				cfg.SubUsers[i].ContainerNames = names
				// 再绑定：新集合中尚未归属的容器写入 OwnerSubUserID
				for _, b := range binds {
					for j := range cfg.Containers {
						if cfg.Containers[j].UUID == b.UUID && cfg.Containers[j].OwnerSubUserID == "" {
							cfg.Containers[j].OwnerSubUserID = subUserID
						}
					}
				}
				cfg.SubUsers[i].TokenVersion++ // 强制刷新可见容器列表
				updated = cfg.SubUsers[i]
				return
			}
		})
		if updated.ID == "" {
			jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Sub-user not found"})
			return
		}
		if saveErr != nil {
			jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save config"})
			return
		}
		auditRequest(r, "subuser.bind-containers", updated.Username, "bound="+strings.Join(names, ",")+" unbound="+strings.Join(unboundNames, ","), true, "")
		jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: newSubUserResponse(updated, "")})

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

// handleSubUserChangePassword 子用户自助改密码。
// 只对当前登录的 sub-user 生效；需要提供旧密码做二次确认。
// 改完强制 TokenVersion++，旧 token 全部失效，前端应自动登出。
func HandleSubUserChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	su := subUserFromRequest(r)
	if su == nil {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Sub-user session required"})
		return
	}
	// 访问码会话（分享凭据持有者）不得改账号密码，否则等于取得账号接管权。
	if isAccessCodeSession(r) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access-code sessions cannot change the account password"})
		return
	}
	// 旧密码在线校验同样计入登录限流：防止持有泄露 token 的攻击者
	// 借此端点对旧密码做不限速的在线爆破。
	rateKey := clientIP(r) + "|user:" + su.Username
	if loginRateLimited(w, rateKey) {
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	req.OldPassword = strings.TrimSpace(req.OldPassword)
	req.NewPassword = strings.TrimSpace(req.NewPassword)
	if req.OldPassword == "" || req.NewPassword == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Both old and new passwords are required"})
		return
	}
	if len(req.NewPassword) < 8 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "New password must be at least 8 characters long"})
		return
	}
	if req.NewPassword == req.OldPassword {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "New password must differ from the old one"})
		return
	}

	// 在线校验旧密码
	if err := bcrypt.CompareHashAndPassword([]byte(su.PassHash), []byte(req.OldPassword)); err != nil {
		loginLimiter.recordFail(rateKey)
		ip := clientIP(r)
		config.AddLoginLog(su.Username, ip, r.Header.Get("User-Agent"), false)
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Current password is incorrect"})
		return
	}
	loginLimiter.reset(rateKey)

	// 生成新 hash 并落库
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to hash new password"})
		return
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.SubUsers {
			if cfg.SubUsers[i].ID != su.ID {
				continue
			}
			cfg.SubUsers[i].PassHash = string(newHash)
			cfg.SubUsers[i].Password = ""  // 明文只在 rotate-password 响应时短暂存在
			cfg.SubUsers[i].TokenVersion++ // 失效所有已签发 token
			break
		}
	})

	auditRequest(r, "subuser.self.change_password", su.Username, "self password change", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Password changed. Please sign in again with the new password."})
}

// HandleSubUserProfile 返回子用户会话的自助信息（用户门户「安全设置」页）：
// 用户名、角色、访问码（用于拼接分享链接）与当前生效的绑定容器数。
// 仅子用户会话可访问；管理员 token 在 subUserFromRequest 处被拒绝。
func HandleSubUserProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	su := subUserFromRequest(r)
	if su == nil {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Sub-user session required"})
		return
	}

	uuids := sessionContainerUUIDs(r, su)
	links := make([]map[string]interface{}, 0, len(uuids))
	for _, uuid := range uuids {
		c := config.FindContainerByUUID(uuid)
		if c == nil {
			continue
		}
		// 机器级访问码凭据按需生成并落库（缺失时才写库）。
		ensureContainerAccessCredentials(c)
		links = append(links, map[string]interface{}{
			"container_uuid":       c.UUID,
			"container_name":       c.Name,
			"access_code":          c.AccessCode,
			"access_code_password": c.AccessCodePassword,
			"login_url":            "/user/login?code=" + url.QueryEscape(c.AccessCode),
		})
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"username":        su.Username,
		"email":           su.Email,
		"role":            subUserRole(su.Role),
		"container_count": len(uuids),
		// access_links 是当前会话可管理的每台机器的访问码凭据（访问码会话只有一台）。
		"access_links": links,
		// via 让前端知道当前会话是账号登录还是访问码登录，从而决定
		// 「账号密码管理」是否可用（访问码会话不可改账号密码）。
		"via": subUserSessionVia(r),
	}})
}

// sessionContainerUUIDs 返回当前会话被授权的容器 UUID：优先取令牌声明
// （访问码会话只含其唯一的一台机器），回退到子用户绑定的全部容器。
func sessionContainerUUIDs(r *http.Request, su *config.SubUser) []string {
	if allowed, ok := subUserAllowedContainers(r); ok {
		out := make([]string, 0, len(allowed.uuids))
		for uuid := range allowed.uuids {
			out = append(out, uuid)
		}
		sort.Strings(out)
		return out
	}
	return activeSubUserContainerUUIDs(su)
}

// ensureContainerAccessCredentials 为容器按需生成「访问码 + 访问码口令」并落库。
// 仅当确有缺失时才触发一次写库，避免每次读取详情都全量落库。
func ensureContainerAccessCredentials(c *config.Container) {
	if c == nil || (c.AccessCode != "" && c.AccessCodePassword != "") {
		return
	}
	if _, updated := config.MutateContainerByID(c.ID, func(t *config.Container) {
		config.EnsureContainerAccessCredentials(t)
	}); updated != nil {
		c.AccessCode = updated.AccessCode
		c.AccessCodePassword = updated.AccessCodePassword
	}
}

// HandleSubUserSelfRotatePassword 子用户自助轮换密码（用户门户「安全设置」）。
// 用户侧只允许随机安全密码：校验旧密码后由服务端 crypto/rand 生成 16 位新密码，
// 明文仅在本次响应中一次性返回，不落库（Password 置空）；TokenVersion++ 使
// 所有已签发 token（含当前会话）失效，前端轮换成功后引导重新登录。
func HandleSubUserSelfRotatePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	su := subUserFromRequest(r)
	if su == nil {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Sub-user session required"})
		return
	}
	// 账号密码轮换同样禁止在访问码会话中执行（防止分享对象接管账号）。
	if isAccessCodeSession(r) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access-code sessions cannot rotate the account password"})
		return
	}
	// 与登录端点一致的限流：旧密码校验失败计入同一登录限流器。
	rateKey := clientIP(r) + "|user:" + su.Username
	if loginRateLimited(w, rateKey) {
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	req.OldPassword = strings.TrimSpace(req.OldPassword)
	if req.OldPassword == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Current password is required"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(su.PassHash), []byte(req.OldPassword)); err != nil {
		loginLimiter.recordFail(rateKey)
		ip := clientIP(r)
		config.AddLoginLog(su.Username, ip, r.Header.Get("User-Agent"), false)
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Current password is incorrect"})
		return
	}
	loginLimiter.reset(rateKey)

	password := generateRandomStr(16)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to generate password"})
		return
	}
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.SubUsers {
			if cfg.SubUsers[i].ID != su.ID {
				continue
			}
			cfg.SubUsers[i].PassHash = string(hash)
			cfg.SubUsers[i].Password = "" // 明文不落库，仅本次响应一次性返回
			cfg.SubUsers[i].Token = ""
			cfg.SubUsers[i].TokenVersion++
			return
		}
	})

	auditRequest(r, "subuser.self.rotate_password", su.Username, "self random password rotation", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{
		"password": password,
	}})
}

// HandleSubUserSelfRotateAccessCodePassword 重置「机器级访问码口令」
// （用户门户「安全设置」）。访问码口令与账号密码相互独立：账号会话与访问码
// 会话都可重置它（前者代表所有者本人；后者代表分享对象，重置只影响该机器的
// 分享凭据，不触及账号本身，故不属于被禁的「改账号密码」）。请求体可选
// container_uuid；不传时要求会话恰好只授权一台机器（即访问码会话）。服务端
// 随机生成 16 位新口令，明文仅本次返回；属主的 TokenVersion++ 使所有已签发
// token（含当前会话）失效，前端展示新口令并引导重新登录。
func HandleSubUserSelfRotateAccessCodePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	su := subUserFromRequest(r)
	if su == nil {
		jsonResponse(w, http.StatusUnauthorized, APIResponse{Success: false, Message: "Sub-user session required"})
		return
	}
	// 与其他自助口令端点同口径限流，防止被用于高频写配置。
	rateKey := clientIP(r) + "|user:" + su.Username
	if loginRateLimited(w, rateKey) {
		return
	}

	var req struct {
		ContainerUUID string `json:"container_uuid"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	target := strings.TrimSpace(req.ContainerUUID)
	if target == "" {
		uuids := sessionContainerUUIDs(r, su)
		if len(uuids) != 1 {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "请指定要重置访问码口令的机器（container_uuid）"})
			return
		}
		target = uuids[0]
	}
	if !isContainerAllowedForRequest(r, target) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Access denied to this container"})
		return
	}
	c := config.FindContainerByUUID(target)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	loginLimiter.reset(rateKey)
	rotateContainerAccessCodePassword(w, r, c)
}

// rotateContainerAccessCodePassword 生成新的「机器级访问码口令」并落库，同时使该
// 机器属主已签发的令牌立即失效（含访问码会话）。调用方需先完成鉴权与容器可见性校验。
func rotateContainerAccessCodePassword(w http.ResponseWriter, r *http.Request, c *config.Container) {
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	password := generateRandomStr(16)
	ownerID := strings.TrimSpace(c.OwnerSubUserID)
	config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Containers {
			if (c.UUID != "" && cfg.Containers[i].UUID == c.UUID) || (c.UUID == "" && cfg.Containers[i].ID == c.ID) {
				cfg.Containers[i].AccessCodePassword = password
				break
			}
		}
		// 口令轮换后使属主已签发令牌立即失效（含当前访问码会话）。
		for i := range cfg.SubUsers {
			if ownerID != "" && cfg.SubUsers[i].ID == ownerID {
				cfg.SubUsers[i].TokenVersion++
				break
			}
		}
	})
	c.AccessCodePassword = password

	auditRequest(r, "container.rotate_access_code_password", c.Name, "access-code password rotated", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{
		"container_uuid":       c.UUID,
		"container_name":       c.Name,
		"access_code":          c.AccessCode,
		"access_code_password": password,
	}})
}
