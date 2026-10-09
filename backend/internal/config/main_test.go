package config

// main_test.go —— 配置包测试的全局安全网。
//
// 背景：InitConfig 的「首次安装」分支会往数据目录写 initial-admin-credentials.txt
// （含随机管理员密码与节点对接密钥）。该数据目录由 getDataDir() 解析，只有设置
// EYVESCLOUD_DATA_DIR 才会重定向到临时目录。此前 store_*_test.go 一族用例只调用了
// SetConfigPath 而没重定向数据目录，于是跑一次测试就会把真实的
// ~/.eyvescloud/initial-admin-credentials.txt 覆盖成一份**与数据库不符**的假凭据
// （实测踩到：线上文件写着一套新密码/新管理路径，但登录失败，库里仍是旧值——典型
// 「假 UI」：文件在说谎）。
//
// 这里在 m.Run 之前把数据目录统一重定向到一次性临时目录，任何用例都不再可能污染
// 真实数据目录；需要特定目录的用例仍可用 t.Setenv / os.Setenv 自行覆盖。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDataDirRedirectedInTests 锁定上面的安全网：本包用例期间数据目录必须落在
// 临时目录内，绝不能解析到真实的 ~/.eyvescloud。
func TestDataDirRedirectedInTests(t *testing.T) {
	dir := getDataDir()
	if dir == "" {
		t.Fatal("getDataDir 返回空")
	}
	tmp := filepath.Clean(os.TempDir())
	if !strings.HasPrefix(filepath.Clean(dir), tmp+string(filepath.Separator)) {
		t.Fatalf("测试期间数据目录应在临时目录下，实际 %s（真实数据目录会被测试污染）", dir)
	}
}

// TestCleanStaleContainersKeepsRecordsWhenLXCRootMissing 锁定「环境没就绪不得清库」：
// LXC 根目录缺失时必须整体跳过，而不是把全部本机容器记录当成过期记录删掉。
func TestCleanStaleContainersKeepsRecordsWhenLXCRootMissing(t *testing.T) {
	prevCfg, prevRoot := AppConfig, lxcRoot
	t.Cleanup(func() {
		AppConfig = prevCfg
		lxcRoot = prevRoot
	})

	lxcRoot = filepath.Join(t.TempDir(), "lxc-not-mounted-yet")
	AppConfig = &EyvescloudConfig{Containers: []Container{
		{ID: 1, Name: "c1", Virtualization: "lxc"},
		{ID: 2, Name: "c2", Virtualization: "lxc"},
	}}

	CleanStaleContainers()

	if got := len(AppConfig.Containers); got != 2 {
		t.Fatalf("根目录缺失时不得清库：容器记录 2 → %d", got)
	}
}

func TestMain(m *testing.M) {
	// v3 起配置库只有 Postgres 后端：绝大多数用例都要 InitConfig 连库，未提供测试 DSN
	// 时它们只会在 openConfigDB 处集体失败。这里整包跳过（与各用例内部
	// pgTestDSN 的 skip 口径一致），CI / 本地验证经 EYVESCLOUD_PG_TEST_DSN 注入。
	if strings.TrimSpace(os.Getenv(pgTestDSNEnv)) == "" {
		fmt.Fprintln(os.Stderr, "未设置 "+pgTestDSNEnv+"，跳过 config 包测试（配置库已改为 Postgres 必需，无 SQLite 回落）")
		os.Exit(0)
	}

	dir, err := os.MkdirTemp("", "eyvescloud-configtest-")
	if err != nil {
		panic("创建测试数据目录失败: " + err.Error())
	}
	if err := os.Setenv("EYVESCLOUD_DATA_DIR", dir); err != nil {
		panic("设置测试数据目录失败: " + err.Error())
	}

	code := m.Run()

	_ = os.RemoveAll(dir)
	os.Exit(code)
}
