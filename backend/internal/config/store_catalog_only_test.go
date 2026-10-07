package config

// store_catalog_only_test.go —— 「只改目录」精确模式（MutateGlobalSaveCatalogOnly）的护栏。
//
// 契约：fn 只改目录类集合（子用户 / API Key / 快照 / 任务）；节点 / 容器 / 访问码一律
// 视为未改动（零指纹、零 SQL）。本文件证明两件事：
//   - 契约成立时结果正确：只写改动的那一行目录，容器/节点/访问码零 SQL，且完整性复核通过；
//   - 契约被违反时能被 verifyHintCompleteness 定位——不会「静默丢数据却没人知道」。

import (
	"path/filepath"
	"strings"
	"testing"
)

func initCatalogOnlyTest(t *testing.T) {
	t.Helper()
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("EYVESCLOUD_DATA_DIR", t.TempDir())
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Status: "running"},
	}
	AppConfig.Nodes = []Node{{ID: "node-1", Name: "n1", Token: "t1"}}
	AppConfig.SubUsers = []SubUser{{ID: "su-1", Username: "u1", PassHash: "x", Role: "operator"}}
	AppConfig.ApiKeys = []ApiKeyConfig{{ID: "key-1", Name: "k1", KeyHash: "h1"}}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
}

func probeWrites(t *testing.T, table string) int {
	t.Helper()
	n := 0
	for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
		n += probeCount(t, table, op)
	}
	return n
}

// TestCatalogOnlySaveWritesCatalogRowOnly 只写改动的那一行目录；容器/节点/访问码零 SQL。
func TestCatalogOnlySaveWritesCatalogRowOnly(t *testing.T) {
	initCatalogOnlyTest(t)
	installWriteProbe(t, "containers", "nodes", "sub_users", "container_access_links")

	if err := MutateGlobalSaveCatalogOnly(func(cfg *EyvescloudConfig) {
		cfg.SubUsers[0].Role = "viewer"
	}); err != nil {
		t.Fatal(err)
	}

	if probeWrites(t, "sub_users") == 0 {
		t.Fatal("子用户改动没有落库")
	}
	for _, table := range []string{"containers", "nodes", "container_access_links"} {
		if n := probeWrites(t, table); n != 0 {
			t.Fatalf("%s 不该被写，实际写了 %d 行", table, n)
		}
	}
	if err := verifyHintCompleteness(); err != nil {
		t.Fatalf("目录式保存后完整性复核失败：%v", err)
	}
}

// TestCatalogOnlySaveCatchesContainerMutation 违反契约（fn 偷改容器）必须被复核抓到。
func TestCatalogOnlySaveCatchesContainerMutation(t *testing.T) {
	initCatalogOnlyTest(t)
	if err := MutateGlobalSaveCatalogOnly(func(cfg *EyvescloudConfig) {
		cfg.Containers[0].Status = "stopped" // 契约违反：目录式入口不该碰容器
	}); err != nil {
		t.Fatal(err)
	}
	err := verifyHintCompleteness()
	if err == nil {
		t.Fatal("目录式入口漏写容器未被检出")
	}
	if !strings.Contains(err.Error(), "containers") {
		t.Fatalf("错误信息未定位到 containers：%v", err)
	}
}

// TestCatalogExactSaveWritesDeclaredRowOnly 目录精确声明：只写声明的那一行目录，
// 容器 / 节点 / 访问码 / 未声明的目录集合零 SQL（证明目录不再全量扫描）。
func TestCatalogExactSaveWritesDeclaredRowOnly(t *testing.T) {
	initCatalogOnlyTest(t)
	installWriteProbe(t, "containers", "nodes", "sub_users", "api_keys", "container_access_links")

	err := MutateGlobalSaveCatalogExact(func(cfg *EyvescloudConfig) DirtySet {
		for i := range cfg.ApiKeys {
			if cfg.ApiKeys[i].ID == "key-1" {
				cfg.ApiKeys[i].LastUsed = "2026-10-06 12:00:00"
				return DirtySet{APIKeys: []ApiKeyConfig{cfg.ApiKeys[i]}}
			}
		}
		return DirtySet{}
	})
	if err != nil {
		t.Fatal(err)
	}

	if probeWrites(t, "api_keys") == 0 {
		t.Fatal("声明的 API Key 改动没有落库")
	}
	for _, table := range []string{"containers", "nodes", "sub_users", "container_access_links"} {
		if n := probeWrites(t, table); n != 0 {
			t.Fatalf("%s 不该被写，实际写了 %d 行", table, n)
		}
	}
	if err := verifyHintCompleteness(); err != nil {
		t.Fatalf("目录精确保存后完整性复核失败：%v", err)
	}
}

// TestCatalogExactSaveCatchesUndeclaredSubUser 漏声明子用户必须被复核定位
// （证明「未列出即未变」这条契约有护栏，不会静默丢数据）。
func TestCatalogExactSaveCatchesUndeclaredSubUser(t *testing.T) {
	initCatalogOnlyTest(t)
	// 改了子用户，却声明「本次无目录改动」——典型漏声明。
	if err := MutateGlobalSaveCatalogExact(func(cfg *EyvescloudConfig) DirtySet {
		cfg.SubUsers[0].Role = "viewer"
		return DirtySet{}
	}); err != nil {
		t.Fatal(err)
	}
	err := verifyHintCompleteness()
	if err == nil {
		t.Fatal("目录精确入口漏声明子用户未被检出")
	}
	if !strings.Contains(err.Error(), "sub_users") {
		t.Fatalf("错误信息未定位到 sub_users：%v", err)
	}
}
