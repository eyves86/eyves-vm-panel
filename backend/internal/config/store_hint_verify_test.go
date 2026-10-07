package config

// store_hint_verify_test.go —— #96 护栏工具的自测。
//
// verifyHintCompleteness 用一次全量扫描复核「已落库指纹」，用于在批量把 MutateGlobal
// 改为脏集声明时定位漏声明。它假定调用方遵守「声明 ⊇ 已改动」契约，本文件证明：
//   - 声明完整时复核通过（不误报）；
//   - 漏声明时精确指出集合与键（能抓到）。

import (
	"path/filepath"
	"strings"
	"testing"
)

func initHintVerifyTest(t *testing.T) {
	t.Helper()
	resetConfigStoreForTest(t)
	t.Cleanup(func() { resetConfigStoreForTest(t) })
	SetConfigPath(filepath.Join(t.TempDir(), "config.json"))
	if _, err := InitConfig(); err != nil {
		t.Fatal(err)
	}
	AppConfig.Containers = []Container{
		{ID: 1, UUID: "uuid-1", Name: "ct-1", Status: "running"},
		{ID: 2, UUID: "uuid-2", Name: "ct-2", Status: "running"},
	}
	AppConfig.SubUsers = []SubUser{
		{ID: "su-1", Username: "u1", PassHash: "x", Role: "operator"},
	}
	if err := SaveConfig(); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyHintCompletenessPassesForCompleteDeclaration 完整声明不得误报。
func TestVerifyHintCompletenessPassesForCompleteDeclaration(t *testing.T) {
	initHintVerifyTest(t)
	AppConfig.Containers[1].Status = "stopped"
	if err := saveConfigToDBHinted(newExactDirtySet(DirtySet{Containers: []Container{AppConfig.Containers[1]}})); err != nil {
		t.Fatal(err)
	}
	if err := verifyHintCompleteness(); err != nil {
		t.Fatalf("完整声明被误判为不完整：%v", err)
	}
}

// TestVerifyHintCompletenessCatchesUndeclaredContainer 漏声明容器必须被定位。
func TestVerifyHintCompletenessCatchesUndeclaredContainer(t *testing.T) {
	initHintVerifyTest(t)
	// 改了容器 0，却只声明「无容器改动」——典型的漏声明。
	AppConfig.Containers[0].Name = "ct-1-renamed"
	if err := saveConfigToDBHinted(newExactDirtySet(DirtySet{})); err != nil {
		t.Fatal(err)
	}
	err := verifyHintCompleteness()
	if err == nil {
		t.Fatal("漏声明容器未被检出")
	}
	if !strings.Contains(err.Error(), "containers") {
		t.Fatalf("错误信息未定位到 containers：%v", err)
	}
}

// TestVerifyHintCompletenessCatchesUndeclaredSubUser 漏声明目录集合必须被定位
// （用来验证「目录未改动」的短路声明在真的改了子用户时会被抓到）。
func TestVerifyHintCompletenessCatchesUndeclaredSubUser(t *testing.T) {
	initHintVerifyTest(t)
	AppConfig.SubUsers[0].Role = "viewer"
	if err := saveConfigToDBHinted(newExactDirtySet(DirtySet{})); err != nil {
		t.Fatal(err)
	}
	err := verifyHintCompleteness()
	if err == nil {
		t.Fatal("漏声明子用户未被检出")
	}
	if !strings.Contains(err.Error(), "sub_users") {
		t.Fatalf("错误信息未定位到 sub_users：%v", err)
	}
}
