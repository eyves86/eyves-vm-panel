package api

// secgroup_enforce.go —— 安全组强制执行：把编译结果真正下发到 iptables。
//
// 背景：secgroup 包原本只有 Compile()，其编译产物（CompileResult.NftText）
// 在全库没有任何消费方——界面上配好的安全组规则从未下发，容器实际裸奔。
// 本文件补齐「下发」这一环。
//
// 两个刻意的设计取舍：
//
//  1. **默认关闭**（config.SecurityGroupEnforced）：默认策略是 drop，若某个容器
//     的放行规则配得不全，启用后该容器会直接断网。这个风险必须由管理员显式承担，
//     不能由升级动作替他决定。
//
//  2. **周期同步而非逐点挂接**：安全组的写入点有 10+ 处（v1/v2 × 建组/改规则/
//     绑定容器），逐点挂接极易漏掉一处而留下"配了不生效"的暗坑。改为定期按租户
//     重算并下发，配合内容哈希做增量（规则没变就不碰 iptables）。
//
// 为什么是 iptables 而不是原设计的 nftables：目标宿主机普遍预装 iptables，
// 而生产实测主控机上**没有 nft 命令**，nft 方案会静默失效。

import (
	"log"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/secgroup"
	"eyvescloud/internal/storage"
)

const (
	// secgroupSyncInterval 是强制执行时的重算周期。
	secgroupSyncInterval = 60 * time.Second
)

// secgroupSyncState 记录每个租户上次下发的内容哈希，用于增量跳过。
var secgroupSyncState = struct {
	mu   sync.Mutex
	hash map[string]string
}{hash: map[string]string{}}

// StartSecurityGroupEnforcer 启动安全组强制执行循环。
//
// 未启用（SecurityGroupEnforced=false）时只做一次状态提示并退出，
// 不做任何 iptables 操作。
func StartSecurityGroupEnforcer() {
	go func() {
		if !config.GetSecurityGroupEnforced() {
			log.Printf("安全组：强制执行未启用——安全组规则会被保存，但不会下发到防火墙。")
			log.Printf("安全组：如需真正生效，请在设置中开启（开启前请确认各容器的放行规则完整，")
			log.Printf("安全组：默认策略为 drop，规则不全的容器会立即断网）。")
			return
		}

		log.Printf("安全组：强制执行已启用，首次全量同步将在 30 秒后进行。")
		time.Sleep(30 * time.Second)

		runner := storage.OSCommandRunner{}
		syncAllSecurityGroups(runner)

		ticker := time.NewTicker(secgroupSyncInterval)
		defer ticker.Stop()
		for range ticker.C {
			if !config.GetSecurityGroupEnforced() {
				// 运行期被关闭：清理已下发的链，避免留下无人管理的防火墙规则。
				if err := teardownAllSecurityGroups(runner); err != nil {
					log.Printf("Warning: 关闭安全组强制时清理失败: %v", err)
				}
				return
			}
			syncAllSecurityGroups(runner)
		}
	}()
}

// syncAllSecurityGroups 重算所有租户的安全组并下发（内容未变则跳过）。
func syncAllSecurityGroups(runner secgroup.Runner) {
	tenants := securityGroupTenants()
	for _, tenantID := range tenants {
		if err := syncTenantSecurityGroups(runner, tenantID); err != nil {
			log.Printf("Warning: 同步租户 %q 的安全组失败: %v", tenantID, err)
		}
	}
}

// syncTenantSecurityGroups 编译单个租户的规则并下发到独立链。
func syncTenantSecurityGroups(runner secgroup.Runner, tenantID string) error {
	groups, rules := securityGroupSnapshot(tenantID)
	if len(groups) == 0 {
		// 该租户的安全组已被删干净：摘除遗留链。
		return teardownTenantSecurityGroup(runner, tenantID)
	}

	res, err := secgroup.Compile(tenantID, groups, rules)
	if err != nil {
		return err
	}

	ips := containerIPsForTenant(tenantID)

	// 增量：内容与绑定容器都没变时跳过（避免每分钟重写防火墙规则）。
	syncKey := res.Hash + "|" + strings.Join(ips, ",")
	secgroupSyncState.mu.Lock()
	if secgroupSyncState.hash[tenantID] == syncKey {
		secgroupSyncState.mu.Unlock()
		return nil
	}
	secgroupSyncState.mu.Unlock()

	cmds, err := secgroup.BuildIptablesCommands(res, ips)
	if err != nil {
		return err
	}
	if err := secgroup.Apply(runner, cmds); err != nil {
		return err
	}

	secgroupSyncState.mu.Lock()
	secgroupSyncState.hash[tenantID] = syncKey
	secgroupSyncState.mu.Unlock()

	if len(ips) == 0 {
		log.Printf("安全组：租户 %q 已编译规则，但没有容器绑定该安全组，暂未放行任何地址。", tenantID)
		return nil
	}
	log.Printf("安全组：租户 %q 已下发 %d 条规则到 %d 个容器地址。", tenantID, len(res.Rules), len(ips))
	return nil
}

// teardownAllSecurityGroups 摘除所有租户的独立链（关闭强制执行时使用）。
func teardownAllSecurityGroups(runner secgroup.Runner) error {
	var firstErr error
	for _, tenantID := range securityGroupTenants() {
		if err := teardownTenantSecurityGroup(runner, tenantID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// teardownTenantSecurityGroup 摘除单个租户的独立链。
func teardownTenantSecurityGroup(runner secgroup.Runner, tenantID string) error {
	if err := secgroup.Apply(runner, secgroup.RemoveIptablesCommands(tenantID)); err != nil {
		return err
	}
	secgroupSyncState.mu.Lock()
	delete(secgroupSyncState.hash, tenantID)
	secgroupSyncState.mu.Unlock()
	return nil
}

// securityGroupTenants 返回当前所有定义过安全组的租户 ID。
func securityGroupTenants() []string {
	seen := map[string]bool{}
	var out []string
	config.AppConfigMu.RLock()
	if config.AppConfig != nil {
		for _, g := range config.AppConfig.SecGroups {
			id := strings.TrimSpace(g.TenantID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	config.AppConfigMu.RUnlock()
	return out
}

// securityGroupSnapshot 取某租户的组与规则快照（持锁拷贝，避免并发读切片）。
func securityGroupSnapshot(tenantID string) ([]secgroup.Group, []secgroup.Rule) {
	groups := []secgroup.Group{}
	groupIDs := map[string]bool{}

	config.AppConfigMu.RLock()
	if config.AppConfig != nil {
		for _, g := range config.AppConfig.SecGroups {
			if strings.TrimSpace(g.TenantID) != tenantID {
				continue
			}
			groups = append(groups, g)
			groupIDs[g.ID] = true
		}
	}
	rules := []secgroup.Rule{}
	if config.AppConfig != nil {
		for _, r := range config.AppConfig.SecGroupRules {
			if groupIDs[r.GroupID] {
				rules = append(rules, r)
			}
		}
	}
	config.AppConfigMu.RUnlock()

	return groups, rules
}

// containerIPsForTenant 返回绑定了该租户任一安全组的、且有可用 IP 的容器地址。
//
// 这一步是原实现缺失的关键环节：安全组是"租户级规则集"，必须落到具体容器
// 地址上才有作用对象。
func containerIPsForTenant(tenantID string) []string {
	_, rules := securityGroupSnapshot(tenantID)
	_ = rules // 规则不参与地址收集，仅为对称调用

	config.AppConfigMu.RLock()
	defer config.AppConfigMu.RUnlock()
	if config.AppConfig == nil {
		return nil
	}

	tenantGroups := map[string]bool{}
	for _, g := range config.AppConfig.SecGroups {
		if strings.TrimSpace(g.TenantID) == tenantID {
			tenantGroups[g.ID] = true
		}
	}
	if len(tenantGroups) == 0 {
		return nil
	}

	var ips []string
	seen := map[string]bool{}
	for i := range config.AppConfig.Containers {
		c := &config.AppConfig.Containers[i]
		bound := false
		for _, gid := range c.SecGroupIDs {
			if tenantGroups[gid] {
				bound = true
				break
			}
		}
		if !bound {
			continue
		}
		ip := strings.TrimSpace(c.IP)
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		ips = append(ips, ip)
	}
	return ips
}
