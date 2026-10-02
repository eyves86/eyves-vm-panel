package config

// save_log_test.go —— 落库失败必须留痕（P0-3 静默失败收口）。
//
// 背景：全库原有 33 处 `_ = SaveConfig()` / `_ = saveConfigToDB()`，落库失败被
// 静默吞掉——用户改完设置看到 200，配置却没进库，重启后凭空消失且无迹可查。
// 这些测试锁定"失败必留痕"。

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withSaveLogTestConfig(t *testing.T) {
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

// captureLog 把标准 logger 的输出重定向到缓冲区，返回读取函数。
func captureLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	previous := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previous)
		log.SetFlags(previousFlags)
	})
	return func() string { return buf.String() }
}

// TestSaveConfigToDBLoggedReportsFailure 数据库不可用时，落库失败必须出现在日志里
// 并标注调用位置（文件名），否则故障会完全静默。
func TestSaveConfigToDBLoggedReportsFailure(t *testing.T) {
	withSaveLogTestConfig(t)
	readLog := captureLog(t)

	// 关掉数据库连接，制造落库失败。
	CloseConfigDB()

	SaveConfigToDBLogged()

	out := readLog()
	if !strings.Contains(out, "配置落库失败") {
		t.Fatalf("落库失败必须留痕，实际日志为空或不匹配: %q", out)
	}
	if !strings.Contains(out, "save_log_test.go") {
		t.Fatalf("日志应标注调用位置（文件名），实际: %q", out)
	}
}

// TestSaveConfigLoggedReportsFailure SaveConfig 路径同样必须留痕。
func TestSaveConfigLoggedReportsFailure(t *testing.T) {
	withSaveLogTestConfig(t)
	readLog := captureLog(t)

	CloseConfigDB()

	SaveConfigLogged()

	if out := readLog(); !strings.Contains(out, "配置落库失败") {
		t.Fatalf("SaveConfig 落库失败必须留痕，实际: %q", out)
	}
}

// TestSaveConfigLoggedSilentOnSuccess 成功路径不应产生噪音日志（否则日志被淹没）。
func TestSaveConfigLoggedSilentOnSuccess(t *testing.T) {
	withSaveLogTestConfig(t)
	readLog := captureLog(t)

	SaveConfigLogged()

	if out := readLog(); strings.Contains(out, "配置落库失败") {
		t.Fatalf("成功落库不应记录失败日志，实际: %q", out)
	}
}
