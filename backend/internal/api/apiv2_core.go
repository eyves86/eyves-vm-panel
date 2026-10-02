package api

// apiv2_core.go —— EyvesCloud API v2 核心层。
//
// 设计目标（面向集成方：计费系统、WHMCS 模块、运维自动化、Terraform 风格工具）：
//
//	1. 契约稳定：v2 一旦发布，路径/字段/错误码不再变化；破坏性调整只进 v3。
//	2. 一套凭据：复用面板既有认证（登录 JWT / 长期 API Key / 同源会话 Cookie），
//	   不再新增第二套 token 体系，避免权限语义分叉。
//	3. 响应可判定：统一信封 {success, code, message, data, request_id}；
//	   失败时 code 为稳定错误码（集成方按码分支），message 供人读，details 定位字段。
//	4. 分页一致：所有列表统一 page/page_size/all + data.pagination。
//	5. 幂等可重试：创建类接口支持 Idempotency-Key 头；节点/被控通信失败返回 502 UPSTREAM_ERROR。
//	6. 与 LXC/KVM 语义对齐：状态枚举、资源字段直接映射面板内部模型，不做二次翻译。
//
// 命名规范：路径小写复数 + 连字符（/storage-pools、/security-groups）；
// 字段 snake_case；时间 RFC3339；布尔用 true/false（不再用 0/1）。
//
// 认证：
//
//	Authorization: Bearer <access_token>   —— POST /api/v2/auth/login 获取，24h 有效
//	X-API-Key: <key>                       —— 长期密钥（带 scope，可绑定容器）
//
// 错误码表：
//
//	INVALID_ARGUMENT   400 参数缺失/非法（details 给出字段级原因）
//	UNAUTHENTICATED    401 未认证或凭据过期
//	PERMISSION_DENIED  403 已认证但无权限（scope 或容器绑定不满足）
//	NOT_FOUND          404 资源不存在
//	METHOD_NOT_ALLOWED 405 方法不支持
//	CONFLICT           409 状态冲突（重名、重复操作、节点不在线等）
//	PRECONDITION_FAILED 412 前置条件不满足（如实例已锁定、配额不足）
//	RATE_LIMITED       429 触发限流
//	INTERNAL           500 服务内部错误
//	UPSTREAM_ERROR     502 被控节点通信失败

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/version"
)

const (
	v2CodeOK                 = "OK"
	v2CodeInvalidArgument    = "INVALID_ARGUMENT"
	v2CodeUnauthenticated    = "UNAUTHENTICATED"
	v2CodePermissionDenied   = "PERMISSION_DENIED"
	v2CodeNotFound           = "NOT_FOUND"
	v2CodeMethodNotAllowed   = "METHOD_NOT_ALLOWED"
	v2CodeConflict           = "CONFLICT"
	v2CodePreconditionFailed = "PRECONDITION_FAILED"
	v2CodeRateLimited        = "RATE_LIMITED"
	v2CodeInternal           = "INTERNAL"
	v2CodeUpstreamError      = "UPSTREAM_ERROR"
)

// v2Envelope 统一响应信封。
type v2Envelope struct {
	Success   bool        `json:"success"`
	Code      string      `json:"code"`
	Message   string      `json:"message,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Details   interface{} `json:"details,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
}

// v2RequestID 从请求头取（便于链路追踪）或生成一个。
func v2RequestID(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get("X-Request-Id")); id != "" {
		return id
	}
	return "req_" + randomHex(8)
}

func v2Write(w http.ResponseWriter, r *http.Request, status int, envelope v2Envelope) {
	envelope.RequestID = v2RequestID(r)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Request-Id", envelope.RequestID)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope)
}

func v2OK(w http.ResponseWriter, r *http.Request, data interface{}) {
	v2Write(w, r, http.StatusOK, v2Envelope{Success: true, Code: v2CodeOK, Data: data})
}

func v2Created(w http.ResponseWriter, r *http.Request, data interface{}) {
	v2Write(w, r, http.StatusCreated, v2Envelope{Success: true, Code: v2CodeOK, Data: data})
}

func v2Accepted(w http.ResponseWriter, r *http.Request, data interface{}) {
	v2Write(w, r, http.StatusAccepted, v2Envelope{Success: true, Code: v2CodeOK, Data: data})
}

func v2NoContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Request-Id", v2RequestID(r))
	w.WriteHeader(http.StatusNoContent)
}

// v2Error 统一错误响应。
func v2Error(w http.ResponseWriter, r *http.Request, status int, code, message string, details interface{}) {
	v2Write(w, r, status, v2Envelope{Success: false, Code: code, Message: message, Details: details})
}

// v2BadRequest / v2NotFound / … 语义化快捷方法（避免各处手写状态码）。
func v2BadRequest(w http.ResponseWriter, r *http.Request, message string, details interface{}) {
	v2Error(w, r, http.StatusBadRequest, v2CodeInvalidArgument, message, details)
}
func v2Unauthorized(w http.ResponseWriter, r *http.Request, message string) {
	if message == "" {
		message = "未认证或凭据已过期"
	}
	v2Error(w, r, http.StatusUnauthorized, v2CodeUnauthenticated, message, nil)
}
func v2Forbidden(w http.ResponseWriter, r *http.Request, message string) {
	v2Error(w, r, http.StatusForbidden, v2CodePermissionDenied, message, nil)
}
func v2NotFound(w http.ResponseWriter, r *http.Request, message string) {
	v2Error(w, r, http.StatusNotFound, v2CodeNotFound, message, nil)
}
func v2Conflict(w http.ResponseWriter, r *http.Request, message string) {
	v2Error(w, r, http.StatusConflict, v2CodeConflict, message, nil)
}
func v2Precondition(w http.ResponseWriter, r *http.Request, message string) {
	v2Error(w, r, http.StatusPreconditionFailed, v2CodePreconditionFailed, message, nil)
}
func v2Internal(w http.ResponseWriter, r *http.Request, message string) {
	if message == "" {
		message = "服务内部错误"
	}
	v2Error(w, r, http.StatusInternalServerError, v2CodeInternal, message, nil)
}
func v2Upstream(w http.ResponseWriter, r *http.Request, message string) {
	v2Error(w, r, http.StatusBadGateway, v2CodeUpstreamError, message, nil)
}

// ---------------------------------------------------------------------------
// 分页 / 排序 / 过滤
// ---------------------------------------------------------------------------

// v2PageQuery 统一列表查询参数：page / page_size / all / sort / order / q。
type v2PageQuery struct {
	Page     int
	PageSize int
	All      bool
	Sort     string
	Desc     bool
	Search   string
}

func v2ParsePage(r *http.Request) v2PageQuery {
	query := r.URL.Query()
	page, _ := strconv.Atoi(strings.TrimSpace(query.Get("page")))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(strings.TrimSpace(query.Get("page_size")))
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	all := strings.EqualFold(strings.TrimSpace(query.Get("all")), "true") || query.Get("all") == "1"
	order := strings.ToLower(strings.TrimSpace(query.Get("order")))
	return v2PageQuery{
		Page:     page,
		PageSize: size,
		All:      all,
		Sort:     strings.TrimSpace(query.Get("sort")),
		Desc:     order != "asc",
		Search:   strings.TrimSpace(query.Get("q")),
	}
}

// Slice 返回分页区间（all=true 时返回全量）。
func (q v2PageQuery) Slice(total int) (int, int) {
	if q.All {
		return 0, total
	}
	start := (q.Page - 1) * q.PageSize
	if start > total {
		start = total
	}
	end := start + q.PageSize
	if end > total {
		end = total
	}
	return start, end
}

// v2Pagination 列表分页元信息。
type v2Pagination struct {
	Page     int  `json:"page"`
	PageSize int  `json:"page_size"`
	Total    int  `json:"total"`
	Pages    int  `json:"pages"`
	All      bool `json:"all,omitempty"`
}

// v2ListData 列表响应体：data.items + data.pagination。
type v2ListData struct {
	Items      interface{}   `json:"items"`
	Pagination v2Pagination  `json:"pagination"`
	Summary    interface{}   `json:"summary,omitempty"`
}

func v2List(w http.ResponseWriter, r *http.Request, items interface{}, query v2PageQuery, total int) {
	pages := 0
	if query.PageSize > 0 {
		pages = (total + query.PageSize - 1) / query.PageSize
	}
	v2OK(w, r, v2ListData{
		Items: items,
		Pagination: v2Pagination{
			Page: query.Page, PageSize: query.PageSize, Total: total, Pages: pages, All: query.All,
		},
	})
}

// v2ListWithSummary 列表 + 汇总（如实例列表附带 on/off 计数，便于前端一次渲染）。
func v2ListWithSummary(w http.ResponseWriter, r *http.Request, items interface{}, query v2PageQuery, total int, summary interface{}) {
	pages := 0
	if query.PageSize > 0 {
		pages = (total + query.PageSize - 1) / query.PageSize
	}
	v2OK(w, r, v2ListData{
		Items: items,
		Pagination: v2Pagination{
			Page: query.Page, PageSize: query.PageSize, Total: total, Pages: pages, All: query.All,
		},
		Summary: summary,
	})
}

// ---------------------------------------------------------------------------
// 请求体解码
// ---------------------------------------------------------------------------

// v2Decode 解析 JSON 请求体（上限 8MiB）。空体返回 nil 而不报错，便于
// "POST 动作型接口"（无参数）直接调用。
func v2Decode(r *http.Request, dst interface{}) error {
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	if err := decoder.Decode(dst); err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	return nil
}

// v2RequiredStrings 校验必填字符串字段，返回字段级错误详情（details）。
func v2RequiredStrings(values map[string]string) map[string]string {
	missing := map[string]string{}
	for field, value := range values {
		if strings.TrimSpace(value) == "" {
			missing[field] = "必填"
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return missing
}

// ---------------------------------------------------------------------------
// 认证与授权
// ---------------------------------------------------------------------------

// v2Auth 解析凭据并注入认证上下文；未认证返回 401。
// 支持 Bearer JWT（登录获取）与 X-API-Key（长期密钥）。
func v2Auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if claims, ok := claimsFromToken(tokenFromRequest(r)); ok {
			// CSRF 纵深防御（审计 N-1）：v2 此前只有 SameSite=Lax 兜底，
			// cookieCSRFGuard 全库仅 v1 的 AuthMiddleware 在调用。浏览器用 Cookie
			// 认证时对写方法再校验一次 Origin/Referer；Bearer 与 X-API-Key 调用方
			// 不带 Cookie，cookieCSRFGuard 直接放行，CLI / 集成方不受影响。
			if !cookieCSRFGuard(r) {
				v2Forbidden(w, r, "请求来源校验失败")
				return
			}
			next(w, withAuthContext(r, authContextFromClaims(claims)))
			return
		}
		if key, ok := validateApiKeyRequest(r); ok {
			if !enforceAPIKeyRateLimit(w, key) {
				return
			}
			next(w, withAuthContext(r, authContextFromAPIKey(key)))
			return
		}
		v2Unauthorized(w, r, "")
	}
}

// v2RequireScope 校验 scope；失败返回 403（与面板 v1 的权限语义一致）。
func v2RequireScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	if hasScope(r, scope) {
		return true
	}
	v2Forbidden(w, r, "缺少权限："+scope)
	return false
}

// v2RequireAdmin 要求管理员身份（子用户与受限 API Key 拒绝）。
func v2RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if isAdminRequest(r) {
		return true
	}
	v2Forbidden(w, r, "该操作需要管理员权限")
	return false
}

// v2AuthContext 取当前认证上下文。
func v2AuthContext(r *http.Request) AuthContext {
	ctx, _ := authContextFromRequest(r)
	return ctx
}

// ---------------------------------------------------------------------------
// 资源视图（内部模型 → v2 契约字段）
// ---------------------------------------------------------------------------

// v2StatusVocabulary 是 v2 的状态枚举（稳定契约，不随内部状态改名而变）。
//
//	running / stopped / creating / suspended / error / unknown
func v2InstanceStatus(c config.Container) string {
	if c.Status == "running" {
		return "running"
	}
	if c.Suspended {
		return "suspended"
	}
	switch c.Status {
	case "creating", "queued", "pending":
		return "creating"
	case "stopped":
		return "stopped"
	case "orphaned":
		return "error"
	}
	return "unknown"
}

// v2Time 统一 RFC3339 输出；空值返回空串（不用 "0001-01-01"）。
func v2Time(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	layouts := []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05"}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return parsed.Format(time.RFC3339)
		}
	}
	return raw
}

// v2SystemInfo 系统信息（/system/info 与 /system/health 共用）。
// adminPath 泄漏防护：隐藏管理路径是本产品的安全设计（防护尘/防扫描），
// 仅管理员身份可见；子用户与集成 API Key（即使只有 dashboard:read）一律不返回
// admin_path / data_dir（data_dir 属服务器文件系统布局，集成方无需要知）。
func v2SystemInfo(adminOK bool) map[string]interface{} {
	config.AppConfigMu.RLock()
	port := config.AppConfig.Port
	dataDir := config.AppConfig.DataDir
	language := config.NormalizeLanguage(config.AppConfig.Language)
	config.AppConfigMu.RUnlock()
	info := map[string]interface{}{
		"product":     "EyvesCloud",
		"api_version": "v2",
		"version":     version.Current(),
		"port":        port,
		"data_dir":    "",
		"admin_path":  "",
		"language":    language,
		"lxc_enabled": commandExists("lxc-create"),
		"kvm_enabled": hostKVMAvailable(),
		"arch":        runtime.GOARCH,
	}
	if adminOK {
		info["data_dir"] = dataDir
		info["admin_path"] = config.CurrentAdminPath()
	}
	return info
}

// round2 保留两位小数（容量/百分比展示统一口径）。
func round2(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}

// v2SortKey 解析 "field:desc" 形式的排序参数（前端友好写法）。
func v2SortKey(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if parts := strings.SplitN(raw, ":", 2); len(parts) == 2 {
		return parts[0], strings.EqualFold(parts[1], "desc")
	}
	return raw, false
}

// v2TokenTTL 是 access_token 有效期（24h，与面板登录一致）。
const v2TokenTTL = 24 * time.Hour

// v2IssueToken 签发 access_token：管理员复用面板 admin token（含 token_version
// 吊销语义），子用户复用子用户 token（含容器绑定与角色）。
func v2IssueToken(username string, isAdmin bool, adminID, role string, subUserUUIDs []string, subUserRole string, tokenVersion int) (string, error) {
	if isAdmin {
		return signAdminToken(username, adminID, role, tokenVersion)
	}
	return newSubUserTokenWithRole(username, subUserUUIDs, subUserRole, time.Now().Add(v2TokenTTL), tokenVersion), nil
}

// v2ScopeAllowedForCtx 在认证上下文上判断 scope（与 v1 权限语义完全一致）。
func v2ScopeAllowedForCtx(ctx AuthContext, scope string) bool {
	switch ctx.Type {
	case authTypeAdmin:
		return true
	case authTypeSubUser:
		return subUserScopeAllowed(scope, ctx.Role)
	case authTypeAPIKey:
		return scopeAllowed(ctx.Scopes, scope)
	}
	return false
}
