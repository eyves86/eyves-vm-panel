package api

// apiv2_recycle.go —— API v2：回收站（软删除）与批量操作扩展（同类商业面板 差距收口）。
//
// 回收站（对齐 同类商业面板/同类商业面板）：
//	DELETE /api/v2/instances/{id}          默认软删除（进回收站）；
//	                                       ?purge=true 才真销毁（计费终止/资源释放语义）
//	GET    /api/v2/recycle-bin             回收站列表
//	POST   /api/v2/instances/{id}/restore  恢复（同名活跃实例冲突时 409）
//	POST   /api/v2/instances/{id}/purge    彻底删除（真销毁数据面）
//
// 保留期：EYVESCLOUD_RECYCLE_DAYS（默认 7 天），到期由后台 worker 自动 purge。
//
// 批量操作扩展（v2 instances/batch 新增 action）：
//	reset-password  params.password 省略时逐实例随机生成，响应逐实例返回新口令（仅此一次）
//	remark          params.remark
//	expiry          params.expires_at（RFC3339 或 YYYY-MM-DD；空=长期有效）

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/lxc"
)

func init() {
	registerV2("GET /api/v2/recycle-bin", v2Auth(v2RecycleBinList))
	registerV2("POST /api/v2/instances/{id}/restore", v2Auth(v2InstanceRestore))
	registerV2("POST /api/v2/instances/{id}/purge", v2Auth(v2InstancePurge))
	// CSV 导出（同类商业面板 export_csv 对齐）：/api/v2/instances/export.csv
	registerV2("GET /api/v2/instances/export.csv", v2Auth(v2InstancesExportCSV))
	// 弹性 IP 独立绑定（同类商业面板 elastic_ip attach/detach 对齐）：
	// 在 IP 池资源上直接绑定/解绑实例，不必经过“实例 public_ipv4_count”间接语义。
	registerV2("POST /api/v2/ip-pools/attach", v2Auth(v2IPPoolAttach))
	registerV2("POST /api/v2/ip-pools/detach", v2Auth(v2IPPoolDetach))
}

// ---------------------------------------------------------------------------
// 回收站
// ---------------------------------------------------------------------------

// v2RecycleBinList GET /api/v2/recycle-bin
func v2RecycleBinList(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	query := v2ParsePage(r)
	items0 := config.RecycledContainers()
	items0 = filterContainersForRequest(r, items0)
	items := make([]map[string]interface{}, 0, len(items0))
	for _, c := range items0 {
		view := v2InstanceView(c)
		view["recycled_at"] = v2Time(c.RecycledAt)
		items = append(items, view)
	}
	total := len(items)
	start, end := query.Slice(total)
	v2List(w, r, items[start:end], query, total)
}

// v2InstanceRestore POST /api/v2/instances/{id}/restore
func v2InstanceRestore(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:create") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	if c.RecycledAt == "" {
		v2OK(w, r, map[string]interface{}{"id": c.ID, "name": c.Name, "restored": false, "reason": "不在回收站"})
		return
	}
	if err := config.RestoreContainer(c.ID); err != nil {
		v2Conflict(w, r, err.Error())
		return
	}
	auditRequest(r, "api.v2.instance.restore", c.Name, "从回收站恢复", true, "")
	v2OK(w, r, map[string]interface{}{"id": c.ID, "name": c.Name, "restored": true})
}

// v2InstancePurge POST /api/v2/instances/{id}/purge
func v2InstancePurge(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:create") {
		return
	}
	c, ok := v2FindInstance(w, r)
	if !ok {
		return
	}
	if c.Locked {
		v2Precondition(w, r, "实例已锁定，请先解锁")
		return
	}
	// 节点实例：真删除由被控 destroy 执行（数据面销毁）。
	if handled, nodeName, err := v2ProxyInstanceToNode(r, c, "destroy", nil); handled {
		if err != nil {
			v2Upstream(w, r, err.Error())
			return
		}
		removeNodeContainerRecord(c.UUID)
		auditRequest(r, "api.v2.instance.purge", c.Name, "彻底删除（节点 "+nodeName+"）", true, "")
		v2Accepted(w, r, map[string]interface{}{"id": c.ID, "name": c.Name, "node": nodeName, "purged": true})
		return
	}
	taskIDs := globalQueue.EnqueueBatchWithAudit(TaskDelete, []int{c.ID}, "", v2AuthContext(r).Username, clientIP(r), r.UserAgent())
	auditRequest(r, "api.v2.instance.purge", c.Name, "彻底删除", true, "")
	v2Accepted(w, r, map[string]interface{}{"id": c.ID, "name": c.Name, "task_ids": taskIDs, "purged": true})
}

// ---------------------------------------------------------------------------
// 批量操作扩展（reset-password / remark / expiry）
// ---------------------------------------------------------------------------

// v2InstancesBatchExtended 在 v2InstancesBatch 的主流程之外实现三个非 destroy 类
// 动作（同一入口分发）：这些动作不进任务队列，直接逐实例同步执行，结果逐实例返回。
func v2InstancesBatchExtended(w http.ResponseWriter, r *http.Request, action string, req struct {
	Action     string          `json:"action"`
	IDs        []int           `json:"ids"`
	Params     json.RawMessage `json:"params"`
	TemplateID string          `json:"template_id"`
}) {
	var params struct {
		Password  string `json:"password"`
		Remark    string `json:"remark"`
		ExpiresAt string `json:"expires_at"`
	}
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}

	type itemResult struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		OK      bool   `json:"ok"`
		Error   string `json:"error,omitempty"`
		Password string `json:"password,omitempty"`
	}
	results := make([]itemResult, 0, len(req.IDs))

	for _, id := range req.IDs {
		c := config.FindContainer(id)
		if c == nil {
			results = append(results, itemResult{ID: id, Error: "不存在"})
			continue
		}
		if !isContainerAllowedForRequest(r, c.UUID) {
			results = append(results, itemResult{ID: id, Name: c.Name, Error: "无权访问"})
			continue
		}
		if c.RecycledAt != "" {
			results = append(results, itemResult{ID: id, Name: c.Name, Error: "在回收站中"})
			continue
		}
		if c.Locked {
			results = append(results, itemResult{ID: id, Name: c.Name, Error: "已锁定"})
			continue
		}

		switch action {
		case "reset-password":
			password := strings.TrimSpace(params.Password)
			if password == "" {
				password = generateRandomStr(16)
			}
			if err := lxc.ValidateCustomSSHPassword(password); err != nil {
				results = append(results, itemResult{ID: id, Name: c.Name, Error: "密码不符合要求：" + err.Error()})
				continue
			}
			config.MutateContainerByID(id, func(target *config.Container) { target.SSHPassword = password })
			if err := quickSetContainerPassword(id, password); err != nil {
				results = append(results, itemResult{ID: id, Name: c.Name, Error: "写入失败：" + err.Error()})
				continue
			}
			results = append(results, itemResult{ID: id, Name: c.Name, OK: true, Password: password})
		case "remark":
			config.MutateContainerByID(id, func(target *config.Container) { target.Remark = params.Remark })
			results = append(results, itemResult{ID: id, Name: c.Name, OK: true})
		case "expiry":
			config.MutateContainerByID(id, func(target *config.Container) {
				target.ExpiresAt = normalizeV2Date(params.ExpiresAt)
			})
			results = append(results, itemResult{ID: id, Name: c.Name, OK: true})
		default:
			v2BadRequest(w, r, "action 取值非法", map[string]string{"action": action})
			return
		}
	}

	okCount := 0
	for _, item := range results {
		if item.OK {
			okCount++
		}
	}
	auditRequest(r, "api.v2.instances.batch."+action, fmt.Sprintf("%d 个实例", len(req.IDs)),
		fmt.Sprintf("成功 %d / 失败 %d", okCount, len(results)-okCount), true, "")
	v2OK(w, r, map[string]interface{}{
		"action": action, "results": results,
		"succeeded": okCount, "failed": len(results) - okCount,
	})
}

// ---------------------------------------------------------------------------
// CSV 导出（同类商业面板 clouds/export_csv 对齐）
// ---------------------------------------------------------------------------

// v2InstancesExportCSV GET /api/v2/instances/export.csv
// 尊重调用方权限：子用户/绑定密钥只导出自己可见的实例。
func v2InstancesExportCSV(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "container:read") {
		return
	}
	containers, _ := listByRuntime()
	containers = filterContainersForRequest(r, containers)
	containers = listContainersFilterRecycledV2(containers, r)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=instances.csv")
	w.WriteHeader(http.StatusOK)
	// UTF-8 BOM：让 Excel 正确识别中文。
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	rows := [][]string{
		{"id", "name", "status", "runtime", "node", "vcpu", "memory_mb", "disk_gb",
			"primary_ip", "ipv6", "owner", "tenant", "template_id", "created_at", "expires_at", "remark"},
	}
	for _, c := range containers {
		rows = append(rows, []string{
			fmt.Sprintf("%d", c.ID), c.Name, v2InstanceStatus(c), c.Runtime(),
			idcNodeNameOfV2(c.NodeID), fmt.Sprintf("%v", c.VCPU), fmt.Sprintf("%d", c.RAMMB), fmt.Sprintf("%v", c.DiskGB),
			c.IP, strings.Join(ipv6AddressStrings(c.IPv6Addresses), " "),
			idcOwnerNameV2(c), c.Tenant, c.Template,
			v2Time(c.CreatedAt), v2Time(c.ExpiresAt), c.Remark,
		})
	}
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			// CSV 转义：包含逗号/引号/换行的字段用引号包裹并转义内部引号。
			if strings.ContainsAny(cell, ",\"\n\r") {
				cell = `"` + strings.ReplaceAll(cell, `"`, `""`) + `"`
			}
			cells[i] = cell
		}
		_, _ = w.Write([]byte(strings.Join(cells, ",") + "\r\n"))
	}
	auditRequest(r, "api.v2.instances.export", fmt.Sprintf("%d 条", len(rows)-1), "CSV 导出", true, "")
}

// listContainersFilterRecycledV2 是 listContainersFilterRecycled 的 v2 侧别名
// （?recycled=true 只看回收站；默认排除回收站实例）。
func listContainersFilterRecycledV2(containers []config.Container, r *http.Request) []config.Container {
	wantRecycled := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("recycled")), "true")
	filtered := containers[:0]
	for _, c := range containers {
		if wantRecycled == (c.RecycledAt != "") {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// ---------------------------------------------------------------------------
// 弹性 IP 独立绑定（同类商业面板 elastic_ip attach/detach 对齐）
// ---------------------------------------------------------------------------

// v2IPPoolAttach POST /api/v2/ip-pools/attach {address, instance_id}
// 把池中空闲的公网 IPv4 显式绑定到实例（配置层；网络面由实例侧配置生效）。
func v2IPPoolAttach(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "routing:write") {
		return
	}
	var req struct {
		Address    string `json:"address"`
		InstanceID int    `json:"instance_id"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	address := strings.TrimSpace(req.Address)
	if address == "" {
		v2BadRequest(w, r, "缺少必填字段", map[string]string{"address": "必填"})
		return
	}
	c := config.FindContainer(req.InstanceID)
	if c == nil {
		v2NotFound(w, r, fmt.Sprintf("实例不存在：%d", req.InstanceID))
		return
	}
	if !isContainerAllowedForRequest(r, c.UUID) {
		v2Forbidden(w, r, "无权操作该实例")
		return
	}
	if c.RecycledAt != "" {
		v2Precondition(w, r, "实例在回收站中，不能绑定 IP")
		return
	}

	config.AppConfigMu.RLock()
	inPool := false
	for _, item := range config.AppConfig.PublicIPv4Pool {
		if item.Address == address {
			inPool = true
			break
		}
	}
	config.AppConfigMu.RUnlock()
	if !inPool {
		v2NotFound(w, r, "地址不在公网 IP 池中："+address)
		return
	}
	for _, item := range c.PublicIPv4s {
		if item.Address == address {
			v2Conflict(w, r, "地址已绑定到该实例")
			return
		}
	}
	for _, other := range config.AppConfig.Containers {
		if other.ID == c.ID {
			continue
		}
		for _, item := range other.PublicIPv4s {
			if item.Address == address {
				v2Conflict(w, r, fmt.Sprintf("地址已绑定到实例 %s（ID %d）", other.Name, other.ID))
				return
			}
		}
	}

	iface := ""
	config.AppConfigMu.RLock()
	for _, item := range config.AppConfig.PublicIPv4Pool {
		if item.Address == address {
			iface = item.Interface
			break
		}
	}
	config.AppConfigMu.RUnlock()
	ok, _ := config.MutateContainerByID(c.ID, func(target *config.Container) {
		target.PublicIPv4s = append(target.PublicIPv4s, config.PublicIPv4Assignment{
			Address: address, Interface: iface,
		})
	})
	if !ok {
		v2Internal(w, r, "绑定失败：实例已不存在")
		return
	}
	auditRequest(r, "api.v2.ip_pool.attach", address, "绑定到实例 "+c.Name, true, "")
	v2OK(w, r, map[string]interface{}{"address": address, "instance_id": c.ID, "instance_name": c.Name, "attached": true})
}

// v2IPPoolDetach POST /api/v2/ip-pools/detach {address}
// 从任意实例解绑地址（地址回到池中）。
func v2IPPoolDetach(w http.ResponseWriter, r *http.Request) {
	if !v2RequireScope(w, r, "routing:write") {
		return
	}
	var req struct {
		Address string `json:"address"`
	}
	if err := v2Decode(r, &req); err != nil {
		v2BadRequest(w, r, "请求体解析失败", map[string]string{"body": err.Error()})
		return
	}
	address := strings.TrimSpace(req.Address)
	if address == "" {
		v2BadRequest(w, r, "缺少必填字段", map[string]string{"address": "必填"})
		return
	}
	detached := ""
	config.AppConfigMu.Lock()
	for i := range config.AppConfig.Containers {
		c := &config.AppConfig.Containers[i]
		kept := c.PublicIPv4s[:0]
		for _, item := range c.PublicIPv4s {
			if item.Address == address {
				detached = c.Name
				continue
			}
			kept = append(kept, item)
		}
		c.PublicIPv4s = kept
	}
	config.AppConfigMu.Unlock()
	if detached == "" {
		v2NotFound(w, r, "地址未绑定到任何实例："+address)
		return
	}
	if err := config.SaveConfig(); err != nil {
		v2Internal(w, r, "保存失败："+err.Error())
		return
	}
	auditRequest(r, "api.v2.ip_pool.detach", address, "从实例 "+detached+" 解绑", true, "")
	v2OK(w, r, map[string]interface{}{"address": address, "instance_name": detached, "detached": true})
}

// timeNowV2 供测试注入（保持时间语义可测）。
var timeNowV2 = time.Now
