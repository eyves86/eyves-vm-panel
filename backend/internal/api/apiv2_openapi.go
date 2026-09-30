package api

// apiv2_openapi.go —— 从路由表**自动生成** OpenAPI 3.0 规范。
//
// 为什么不用手写清单：v1 的 spec 是手写的，结果长期落后于实现（这也是
// openapi_paths.go 那个补丁文件存在的原因）。v2 的规范直接遍历 v2Routes 生成，
// 端点增删会立即反映到文档里，**不可能漂移**；每个端点的摘要/查询参数/请求体
// 说明由元数据表补充，未登记的操作会退化为通用描述（但路径与方法一定正确）。
//
// 端点：GET /api/v2/openapi.json（公开，与 v1 的 /api/openapi.json 一致，
// 便于集成方导入 Postman / Swagger UI / 代码生成器）。

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"eyvescloud/internal/version"
)

func init() {
	registerV2("GET /api/v2/openapi.json", v2OpenAPIHandler)
}

// v2OpenAPIMeta 是端点元数据：摘要 + 查询参数 + 是否含请求体。
// 键为 "METHOD /api/v2/path"，与路由表一一对应；未登记的操作使用默认描述。
type v2OpenAPIMeta struct {
	Summary string
	Query   []string
	Body    string
}

func v2OpenAPIMetadata() map[string]v2OpenAPIMeta {
	return map[string]v2OpenAPIMeta{
		"POST /api/v2/auth/login":  {Summary: "登录并获取 access_token", Body: "username / password / code（两步验证，可选）/ type（admin|client）"},
		"POST /api/v2/auth/logout": {Summary: "退出登录（无状态令牌，客户端丢弃即可）"},
		"GET /api/v2/auth/me":      {Summary: "当前身份、角色、scope 与功能点（前端按钮与集成方能力探测）"},

		"GET /api/v2/instances":         {Summary: "实例列表", Query: []string{"page", "page_size", "all", "status", "runtime", "node_id", "owner", "tenant", "template_id", "locked", "q", "sort", "order"}},
		"POST /api/v2/instances":        {Summary: "创建实例（支持 node_id=auto 调度与批量 count）", Body: "name / runtime / template_id / node_id / count / vcpu / memory_mb / disk_gb / down_mbps / up_mbps / traffic_quota_gb / ssh_port / nat_ports / public_ipv4_count / ipv6_count / storage_pool_id / auth{mode,password,ssh_key_ids} / cloud_init / snapshot_limit / owner / tenant / remark / expires_at / firewall_enabled"},
		"GET /api/v2/instances/{id}":    {Summary: "实例详情（含快照、备份、安全组）"},
		"PATCH /api/v2/instances/{id}":  {Summary: "部分更新实例", Body: "vcpu / cpu_percent(1-100) / memory_mb / disk_gb / data_disk_gb / down_mbps / up_mbps / traffic_quota_gb / snapshot_limit / remark / expires_at / tenant / firewall_enabled"},
		"DELETE /api/v2/instances/{id}": {Summary: "删除实例（节点上的实例由被控执行）"},

		"POST /api/v2/instances/{id}/power":                   {Summary: "电源操作", Body: "action: start|stop|shutdown|restart|hard-stop|hard-restart|suspend|unsuspend（hard-stop=强制断电不删除）"},
		"POST /api/v2/instances/{id}/reinstall":               {Summary: "重装系统", Body: "template_id / password"},
		"POST /api/v2/instances/{id}/reset-password":          {Summary: "重置 root 密码", Body: "password（留空自动生成，仅返回一次）"},
		"POST /api/v2/instances/{id}/console":                 {Summary: "获取控制台票据", Body: "type: ssh|vnc（LXC 仅 ssh；KVM 两者皆可，需运行中）"},
		"GET /api/v2/instances/{id}/metrics":                  {Summary: "实时指标（CPU/内存/网络/磁盘）"},
		"GET /api/v2/instances/{id}/usage":                    {Summary: "流量用量与配额"},
		"GET /api/v2/instances/{id}/events":                   {Summary: "实例审计事件", Query: []string{"page", "page_size", "all"}},
		"GET /api/v2/instances/{id}/xml":                      {Summary: "KVM libvirt XML（LXC 返回 400）"},
		"GET /api/v2/instances/{id}/snapshots":                {Summary: "快照列表"},
		"POST /api/v2/instances/{id}/snapshots":               {Summary: "创建快照", Body: "name（可选）"},
		"POST /api/v2/instances/{id}/snapshots/{sid}/restore": {Summary: "从快照恢复"},
		"DELETE /api/v2/instances/{id}/snapshots/{sid}":       {Summary: "删除快照"},
		"GET /api/v2/instances/{id}/backups":                  {Summary: "备份列表"},
		"POST /api/v2/instances/{id}/backups":                 {Summary: "创建完整备份"},
		"DELETE /api/v2/instances/{id}/backups/{bid}":         {Summary: "删除备份"},
		"POST /api/v2/instances/{id}/clone":                   {Summary: "克隆实例", Body: "name"},
		"POST /api/v2/instances/{id}/rescue":                  {Summary: "进入救援模式（KVM）", Body: "iso_id（可选）"},
		"DELETE /api/v2/instances/{id}/rescue":                {Summary: "退出救援模式"},
		"POST /api/v2/instances/{id}/lock":                    {Summary: "锁定实例（禁止删除/重装等破坏性操作）"},
		"DELETE /api/v2/instances/{id}/lock":                  {Summary: "解锁实例"},
		"PUT /api/v2/instances/{id}/network":                  {Summary: "网络配置调整", Body: "nat_ports[] / public_ipv4_count / ipv6_count"},
		"GET /api/v2/instances/{id}/security-groups":          {Summary: "查询安全组绑定"},
		"PUT /api/v2/instances/{id}/security-groups":          {Summary: "设置安全组绑定（整体替换）", Body: "group_ids[]"},
		"GET /api/v2/instances/{id}/migrate":                  {Summary: "迁移计划（阻塞项、可选节点、存储与数据流向）"},
		"POST /api/v2/instances/{id}/migrate":                 {Summary: "迁移实例到目标节点（配置重建）", Body: "target_node_id / mode: move|copy / start_after"},
		"POST /api/v2/instances/batch":                        {Summary: "批量操作（本机入队、节点代理）", Body: "action: power|delete|reinstall / ids[] / params{} / template_id"},

		"GET /api/v2/nodes":                   {Summary: "节点列表", Query: []string{"page", "page_size", "all", "status", "maintenance", "region_id", "node_group_id", "q"}},
		"POST /api/v2/nodes":                  {Summary: "添加节点（quick 一键接入 / manual 手工接入）", Body: "name / address / mode / tls_skip_verify / allow_private / bind_ip"},
		"GET /api/v2/nodes/{id}":              {Summary: "节点详情（凭据脱敏）"},
		"PATCH /api/v2/nodes/{id}":            {Summary: "修改节点", Body: "name / address / tls_skip_verify / region_id / node_group_id"},
		"DELETE /api/v2/nodes/{id}":           {Summary: "删除节点", Query: []string{"force"}},
		"POST /api/v2/nodes/{id}/maintenance": {Summary: "维护模式开关", Body: "enabled"},
		"POST /api/v2/nodes/{id}/install-key": {Summary: "换发一次性安装密钥（返回接入命令）"},
		"GET /api/v2/nodes/{id}/metrics":      {Summary: "节点资源指标"},
		"GET /api/v2/nodes/{id}/instances":    {Summary: "该节点上的实例"},
		"GET /api/v2/nodes/schedule":          {Summary: "放置调度决策（过滤+评分+候选理由）", Query: []string{"ram_mb", "disk_gb", "virt", "storage", "count", "node_priority"}},

		"GET /api/v2/node-groups":            {Summary: "节点分组列表"},
		"POST /api/v2/node-groups":           {Summary: "创建节点分组", Body: "name / description / region_id"},
		"PATCH /api/v2/node-groups/{id}":     {Summary: "修改节点分组"},
		"DELETE /api/v2/node-groups/{id}":    {Summary: "删除节点分组（自动解除成员归属）"},
		"PUT /api/v2/node-groups/{id}/nodes": {Summary: "设置分组成员（整体替换）", Body: "node_ids[]"},

		"GET /api/v2/regions":         {Summary: "区域列表（含节点数与在线数）"},
		"POST /api/v2/regions":        {Summary: "创建区域", Body: "name / location"},
		"PATCH /api/v2/regions/{id}":  {Summary: "修改区域"},
		"DELETE /api/v2/regions/{id}": {Summary: "删除区域（区域下有节点时拒绝）"},

		"GET /api/v2/images":                              {Summary: "镜像目录（模板/自定义源，含下载与启用状态）", Query: []string{"page", "page_size", "all", "runtime", "type", "q"}},
		"POST /api/v2/images":                             {Summary: "新增自定义镜像源", Body: "name / runtime / url / sha256 / distro / release / arch"},
		"GET /api/v2/images/{id}":                         {Summary: "镜像详情"},
		"PATCH /api/v2/images/{id}":                       {Summary: "启用/停用镜像", Body: "enabled"},
		"POST /api/v2/images/{id}/download":               {Summary: "触发镜像下载（异步）"},
		"DELETE /api/v2/images/{id}":                      {Summary: "删除自定义镜像源（内置模板拒绝）"},
		"GET /api/v2/iso-images":                          {Summary: "ISO 目录（含文件可用性）"},
		"DELETE /api/v2/iso-images/{id}":                  {Summary: "删除 ISO（同时清理磁盘文件）"},
		"GET /api/v2/storage-pools":                       {Summary: "存储池列表（含路径可用性与共享标记）"},
		"GET /api/v2/storage-pools/{id}":                  {Summary: "存储池详情"},
		"GET /api/v2/ssh-keys":                            {Summary: "SSH 公钥列表"},
		"POST /api/v2/ssh-keys":                           {Summary: "录入 SSH 公钥（服务端计算指纹）", Body: "name / public_key"},
		"DELETE /api/v2/ssh-keys/{id}":                    {Summary: "删除 SSH 公钥"},
		"GET /api/v2/security-groups":                     {Summary: "安全组列表"},
		"POST /api/v2/security-groups":                    {Summary: "创建安全组", Body: "name / default_action: accept|drop"},
		"GET /api/v2/security-groups/{id}":                {Summary: "安全组详情（含规则）"},
		"PATCH /api/v2/security-groups/{id}":              {Summary: "修改安全组"},
		"DELETE /api/v2/security-groups/{id}":             {Summary: "删除安全组（级联清理规则）"},
		"GET /api/v2/security-groups/{id}/rules":          {Summary: "规则列表"},
		"POST /api/v2/security-groups/{id}/rules":         {Summary: "新增规则", Body: "direction: ingress|egress / protocol: any|tcp|udp|icmp / src_cidr / dst_cidr / src_port / dst_port / action: accept|drop / priority"},
		"DELETE /api/v2/security-groups/{id}/rules/{rid}": {Summary: "删除规则"},
		"GET /api/v2/ip-pools":                            {Summary: "公网 IP 池（IPv4 池 + IPv6 前缀，含分配归属）"},
		"POST /api/v2/ip-pools":                           {Summary: "添加池条目", Body: "type: ipv4|ipv6 / address（IPv4）/ prefix（IPv6）/ interface"},
		"DELETE /api/v2/ip-pools":                         {Summary: "移除池条目", Query: []string{"type", "address", "prefix"}},

		"GET /api/v2/tasks":                       {Summary: "任务列表", Query: []string{"page", "page_size", "all", "status", "type", "instance_id"}},
		"GET /api/v2/tasks/{id}":                  {Summary: "任务详情（含执行日志）"},
		"POST /api/v2/tasks/{id}/cancel":          {Summary: "取消排队中的任务（执行中返回 409）"},
		"GET /api/v2/backups":                     {Summary: "备份总览", Query: []string{"page", "page_size", "all", "instance_id"}},
		"POST /api/v2/backups/{id}/restore":       {Summary: "从备份恢复"},
		"DELETE /api/v2/backups/{id}":             {Summary: "删除备份"},
		"GET /api/v2/backup-plans":                {Summary: "备份计划列表", Query: []string{"page", "page_size", "all", "instance_id"}},
		"POST /api/v2/backup-plans":               {Summary: "创建备份计划", Body: "name / instance_id / cron / enabled / keep"},
		"GET /api/v2/backup-plans/{id}":           {Summary: "备份计划详情（含最近运行记录）"},
		"PATCH /api/v2/backup-plans/{id}":         {Summary: "修改备份计划（部分更新）"},
		"DELETE /api/v2/backup-plans/{id}":        {Summary: "删除备份计划"},
		"POST /api/v2/backup-plans/{id}/run":      {Summary: "立即执行一次备份计划"},
		"GET /api/v2/webhooks":                    {Summary: "事件订阅列表（密钥不回显）"},
		"POST /api/v2/webhooks":                   {Summary: "创建订阅（回调地址做 SSRF 校验）", Body: "name / url / event_types[] / enabled"},
		"PATCH /api/v2/webhooks/{id}":             {Summary: "修改订阅"},
		"DELETE /api/v2/webhooks/{id}":            {Summary: "删除订阅"},
		"GET /api/v2/users":                       {Summary: "子用户列表"},
		"POST /api/v2/users":                      {Summary: "创建子用户（bcrypt 口令 + 一次性访问码）", Body: "username / password / email / role: operator|viewer / tenant / container_uuids[]"},
		"GET /api/v2/users/{id}":                  {Summary: "子用户详情（含绑定实例）"},
		"PATCH /api/v2/users/{id}":                {Summary: "修改子用户（变更即吊销旧令牌）", Body: "email / role / tenant / container_uuids[]"},
		"DELETE /api/v2/users/{id}":               {Summary: "删除子用户（解除容器归属）"},
		"POST /api/v2/users/{id}/rotate-password": {Summary: "轮换子用户口令（新口令仅返回一次）"},
		"GET /api/v2/admins":                      {Summary: "管理员列表（脱敏）"},
		"POST /api/v2/admins":                     {Summary: "创建管理员", Body: "username / password / role"},
		"PATCH /api/v2/admins/{id}":               {Summary: "修改管理员（角色/启停/重置口令）"},
		"DELETE /api/v2/admins/{id}":              {Summary: "删除管理员（仅主管理员）"},
		"GET /api/v2/api-keys":                    {Summary: "API Key 列表（仅指纹与前缀）"},
		"POST /api/v2/api-keys":                   {Summary: "创建 API Key（明文仅返回一次）", Body: "name / scopes[] / container_uuids[] / ip_whitelist / expires_at"},
		"DELETE /api/v2/api-keys/{id}":            {Summary: "删除 API Key"},
		"GET /api/v2/audit-logs":                  {Summary: "审计日志", Query: []string{"page", "page_size", "all", "actor", "action", "target", "failed_only", "q"}},
		"GET /api/v2/metrics/host":                {Summary: "宿主机实时指标"},
		"GET /api/v2/metrics/instances":           {Summary: "实例实时指标（批量）"},
		"GET /api/v2/metrics/summary":             {Summary: "总览汇总（实例/节点/镜像/存储/快照/备份）"},
		"GET /api/v2/system/info":                 {Summary: "系统信息（版本、端口、能力）"},
		"GET /api/v2/system/health":               {Summary: "健康检查"},
		"GET /api/v2/system/update-check":         {Summary: "版本检测（当前/最新/是否有更新）"},

		"GET /api/v2/openapi.json": {Summary: "本规范（OpenAPI 3.0，由路由表自动生成）"},
	}
}

// v2OpenAPITagFor 按路径首段给出 tag（Swagger UI 按模块分组展示）。
func v2OpenAPITagFor(path string) string {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) == 0 {
		return "misc"
	}
	first := segments[0]
	switch first {
	case "auth":
		return "认证"
	case "instances":
		return "实例"
	case "nodes", "node-groups", "regions":
		return "节点与区域"
	case "images", "iso-images", "storage-pools", "ssh-keys", "security-groups", "ip-pools":
		return "资源目录"
	case "tasks", "backups", "backup-plans", "webhooks":
		return "运维"
	case "users", "admins", "api-keys", "audit-logs":
		return "用户与审计"
	case "metrics", "system", "openapi.json":
		return "系统"
	}
	return first
}

// v2OpenAPISpec 遍历路由表生成 OpenAPI 3.0 文档。
func v2OpenAPISpec() map[string]interface{} {
	meta := v2OpenAPIMetadata()

	type opEntry struct {
		method string
		path   string
		meta   v2OpenAPIMeta
		found  bool
	}
	entries := make([]opEntry, 0, len(v2Routes))
	for pattern := range v2Routes {
		parts := strings.SplitN(pattern, " ", 2)
		if len(parts) != 2 {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(parts[0]))
		fullPath := strings.TrimSpace(parts[1])
		path := strings.TrimPrefix(fullPath, "/api/v2")
		if path == "" {
			path = "/"
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		item, ok := meta[pattern]
		entries = append(entries, opEntry{method: method, path: path, meta: item, found: ok})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path == entries[j].path {
			return entries[i].method < entries[j].method
		}
		return entries[i].path < entries[j].path
	})

	paths := map[string]interface{}{}
	undocumented := []string{}
	for _, entry := range entries {
		summary := entry.meta.Summary
		if summary == "" {
			summary = "（未登记摘要：" + entry.method + " " + entry.path + "）"
			undocumented = append(undocumented, entry.method+" "+entry.path)
		}
		operation := map[string]interface{}{
			"summary": summary,
			"tags":    []string{v2OpenAPITagFor(entry.path)},
			"security": []map[string][]string{
				{"bearerAuth": {}},
				{"apiKeyAuth": {}},
			},
			"responses": map[string]interface{}{
				"200": map[string]interface{}{
					"description": "成功",
					"content": map[string]interface{}{
						"application/json": map[string]interface{}{"schema": map[string]interface{}{"$ref": "#/components/schemas/Envelope"}},
					},
				},
				"400": map[string]string{"description": "INVALID_ARGUMENT：参数缺失或非法（details 给出字段级原因）"},
				"401": map[string]string{"description": "UNAUTHENTICATED：未认证或凭据过期"},
				"403": map[string]string{"description": "PERMISSION_DENIED：缺少 scope 或容器绑定不满足"},
				"404": map[string]string{"description": "NOT_FOUND：资源不存在"},
				"409": map[string]string{"description": "CONFLICT：状态冲突（重名、节点不在线、执行中无法取消等）"},
				"412": map[string]string{"description": "PRECONDITION_FAILED：前置条件不满足（锁定、挂起、到期、超流量）"},
				"502": map[string]string{"description": "UPSTREAM_ERROR：被控节点通信或执行失败"},
			},
		}

		// 路径参数：从 {name} 提取（统一为字符串型）。
		params := []map[string]interface{}{}
		for _, segment := range strings.Split(entry.path, "/") {
			if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
				name := strings.Trim(segment, "{}")
				params = append(params, map[string]interface{}{
					"name": name, "in": "path", "required": true,
					"schema": map[string]string{"type": "string"},
					"description": map[string]string{
						"id": "实例 ID / UUID / 名称", "sid": "快照 ID", "bid": "备份 ID", "rid": "规则 ID",
					}[name],
				})
			}
		}
		for _, query := range entry.meta.Query {
			params = append(params, map[string]interface{}{
				"name": query, "in": "query", "required": false,
				"schema": map[string]string{"type": "string"},
			})
		}
		if len(params) > 0 {
			operation["parameters"] = params
		}
		if entry.meta.Body != "" {
			operation["requestBody"] = map[string]interface{}{
				"required":    true,
				"description": entry.meta.Body,
				"content": map[string]interface{}{
					"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object"}},
				},
			}
		}
		methods, _ := paths[entry.path].(map[string]interface{})
		if methods == nil {
			methods = map[string]interface{}{}
		}
		methods[strings.ToLower(entry.method)] = operation
		paths[entry.path] = methods
	}

	spec := map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title":   "EyvesCloud API v2",
			"version": version.Current(),
			"description": "EyvesCloud 集成专用 API（契约稳定）。统一信封 {success, code, message, data, request_id}；" +
				"列表返回 data.items + data.pagination；认证使用 Bearer access_token 或 X-API-Key。" +
				"本规范由服务端路由表自动生成，端点清单不可能与实现漂移。",
		},
		"servers": []map[string]string{{"url": "/api/v2", "description": "当前面板"}},
		"components": map[string]interface{}{
			"securitySchemes": map[string]interface{}{
				"bearerAuth": map[string]interface{}{"type": "http", "scheme": "bearer", "bearerFormat": "JWT"},
				"apiKeyAuth": map[string]interface{}{"type": "apiKey", "in": "header", "name": "X-API-Key"},
			},
			"schemas": map[string]interface{}{
				"Envelope": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"success":    map[string]string{"type": "boolean"},
						"code":       map[string]string{"type": "string", "description": "OK / INVALID_ARGUMENT / UNAUTHENTICATED / PERMISSION_DENIED / NOT_FOUND / CONFLICT / PRECONDITION_FAILED / RATE_LIMITED / INTERNAL / UPSTREAM_ERROR"},
						"message":    map[string]string{"type": "string"},
						"data":       map[string]string{"type": "object"},
						"details":    map[string]string{"type": "object", "description": "字段级错误原因"},
						"request_id": map[string]string{"type": "string"},
					},
					"required": []string{"success", "code"},
				},
				"Pagination": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"page":      map[string]string{"type": "integer"},
						"page_size": map[string]string{"type": "integer"},
						"total":     map[string]string{"type": "integer"},
						"pages":     map[string]string{"type": "integer"},
					},
				},
				"Instance": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":         map[string]string{"type": "integer"},
						"uuid":       map[string]string{"type": "string"},
						"name":       map[string]string{"type": "string"},
						"status":     map[string]string{"type": "string", "description": "running / stopped / creating / suspended / error / unknown"},
						"runtime":    map[string]string{"type": "string", "description": "lxc / kvm"},
						"node_id":    map[string]string{"type": "string"},
						"node_name":  map[string]string{"type": "string"},
						"vcpu":       map[string]string{"type": "number"},
						"memory_mb":  map[string]string{"type": "integer"},
						"disk_gb":    map[string]string{"type": "number"},
						"primary_ip": map[string]string{"type": "string"},
						"locked":     map[string]string{"type": "boolean"},
						"remark":     map[string]string{"type": "string"},
					},
				},
			},
		},
		"paths": paths,
		"x-generated": map[string]interface{}{
			"endpoints":    len(entries),
			"documented":   len(entries) - len(undocumented),
			"undocumented": undocumented,
		},
	}
	return spec
}

// v2OpenAPIHandler 输出 OpenAPI 规范（公开，便于集成方导入工具链）。
func v2OpenAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		v2Error(w, r, http.StatusMethodNotAllowed, v2CodeMethodNotAllowed, "Method not allowed", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	encoded, err := jsonMarshalIndent(v2OpenAPISpec())
	if err != nil {
		v2Internal(w, r, "生成规范失败："+err.Error())
		return
	}
	_, _ = w.Write(encoded)
}

// jsonMarshalIndent 缩进序列化（规范文件便于人工阅读与 diff）。
func jsonMarshalIndent(value interface{}) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}
