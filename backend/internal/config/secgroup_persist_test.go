package config

// secgroup_persist_test.go —— 安全组配置持久化回归测试。
//
// 背景：SecGroups / SecGroupRules 此前**完全没有落库**（saveMeta 与
// loadConfigFromDB 都没有这两个键），面板重启后所有安全组与规则凭空消失。
// 生产 DB 的 78 个 app_meta 键里也确实没有任何 sec_group*。

import (
	"os"
	"path/filepath"
	"testing"

	"eyvescloud/internal/secgroup"
)

// reopenConfig 关闭并重新初始化配置库，模拟一次面板重启。
func reopenConfig(t *testing.T) *EyvescloudConfig {
	t.Helper()
	CloseConfigDB()
	cfg, err := InitConfig()
	if err != nil {
		t.Fatalf("重新初始化配置库失败: %v", err)
	}
	return cfg
}

func withSecGroupPersistTest(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	previousDataDir := os.Getenv("EYVESCLOUD_DATA_DIR")
	previousCfgPath := getConfigPath()
	previousApp := GetTestConfig()

	SetConfigPath(filepath.Join(dir, "config.json"))
	os.Setenv("EYVESCLOUD_DATA_DIR", dir)
	t.Cleanup(func() {
		CloseConfigDB()
		RestoreTestConfig(previousApp)
		SetConfigPath(previousCfgPath)
		if previousDataDir == "" {
			os.Unsetenv("EYVESCLOUD_DATA_DIR")
		} else {
			os.Setenv("EYVESCLOUD_DATA_DIR", previousDataDir)
		}
	})

	if _, err := InitConfig(); err != nil {
		t.Fatalf("初始化配置库失败: %v", err)
	}
}

// TestSecurityGroupsSurviveRestart 安全组与规则必须在重启后仍然存在。
func TestSecurityGroupsSurviveRestart(t *testing.T) {
	withSecGroupPersistTest(t)

	group := secgroup.Group{
		ID:            "sg-web",
		TenantID:      "acme",
		Name:          "web",
		DefaultAction: secgroup.ActionDrop,
	}
	rule := secgroup.Rule{
		ID: "rule-22", GroupID: "sg-web",
		Direction: secgroup.DirIngress, Protocol: secgroup.ProtoTCP,
		DstPort: 22, Action: secgroup.ActionAccept, Priority: 10,
	}

	if err := MutateGlobal(func(cfg *EyvescloudConfig) {
		cfg.SecGroups = []secgroup.Group{group}
		cfg.SecGroupRules = []secgroup.Rule{rule}
	}); err != nil {
		t.Fatalf("写入安全组失败: %v", err)
	}

	// 模拟重启。
	cfg := reopenConfig(t)

	if len(cfg.SecGroups) != 1 {
		t.Fatalf("重启后安全组应保留 1 个，实际 %d 个", len(cfg.SecGroups))
	}
	got := cfg.SecGroups[0]
	if got.ID != group.ID || got.Name != group.Name || got.DefaultAction != group.DefaultAction {
		t.Fatalf("安全组内容不一致：%+v", got)
	}
	if len(cfg.SecGroupRules) != 1 {
		t.Fatalf("重启后规则应保留 1 条，实际 %d 条", len(cfg.SecGroupRules))
	}
	gr := cfg.SecGroupRules[0]
	if gr.ID != rule.ID || gr.DstPort != rule.DstPort || gr.Action != rule.Action {
		t.Fatalf("规则内容不一致：%+v", gr)
	}
}

// TestSecurityGroupEnforcedFlagSurvivesRestart 强制执行开关同样必须持久化
// （否则管理员开启后重启又变回关闭，形成"开关失灵"）。
func TestSecurityGroupEnforcedFlagSurvivesRestart(t *testing.T) {
	withSecGroupPersistTest(t)

	if GetSecurityGroupEnforced() {
		t.Fatal("默认应为关闭")
	}

	if err := SetSecurityGroupEnforced(true); err != nil {
		t.Fatalf("开启开关失败: %v", err)
	}
	if !GetSecurityGroupEnforced() {
		t.Fatal("开启后内存态应为 true")
	}

	cfg := reopenConfig(t)
	if !cfg.SecurityGroupEnforced {
		t.Fatal("重启后开关应保持开启")
	}

	if err := SetSecurityGroupEnforced(false); err != nil {
		t.Fatalf("关闭开关失败: %v", err)
	}
	cfg = reopenConfig(t)
	if cfg.SecurityGroupEnforced {
		t.Fatal("重启后开关应保持关闭")
	}
}

// TestCorruptSecurityGroupsDoNotBreakStartup 一条损坏的安全组记录不能让面板起不来
// （解析失败按空处理，而不是让整个 config 加载失败）。
func TestCorruptSecurityGroupsDoNotBreakStartup(t *testing.T) {
	withSecGroupPersistTest(t)

	// 直接往 meta 里塞一段非法 JSON。
	dbMu.Lock()
	_, err := db.Exec(`INSERT INTO app_meta(key, value) VALUES('sec_groups', '{not-json')`+
		` ON CONFLICT(key) DO UPDATE SET value=excluded.value`)
	dbMu.Unlock()
	if err != nil {
		t.Fatalf("注入损坏数据失败: %v", err)
	}

	cfg := reopenConfig(t)
	if len(cfg.SecGroups) != 0 {
		t.Fatalf("损坏数据应被丢弃，实际解析出 %d 个安全组", len(cfg.SecGroups))
	}
}
