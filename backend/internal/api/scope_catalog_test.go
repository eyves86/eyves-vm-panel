package api

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// scope_catalog_test.go —— 防止「前端 API Key 权限目录」与「后端实际强制的 scope」漂移。
//
// 背景：管理员界面的「API 集成」页里，创建/编辑 API Key 的可勾选权限目录（前端
// frontend/src/pages/ApiIntegration.tsx 的 scopeGroups）是硬编码的。一旦后端新增
// 或改名了某个 scope，而前端目录没同步，就会出现「权限申请不齐全、只显示一部分」
// 的现象——用户根本勾不到新权限。
//
// 本测试从后端源码里提取所有被强制校验的 scope，与前端目录逐一比对：
//   - 正向：后端强制的 scope 必须都能在前端目录里勾选到；
//   - 反向：前端目录里的 scope 必须都是后端真正认的，避免出现勾了却无效的僵尸项。
//
// 前端文件缺失时跳过（例如只拉取了 backend 子目录），不误伤纯后端构建。

// scopeCallRE 匹配 scope 校验函数的调用，并捕获其括号内参数（不含嵌套括号）。
var scopeCallRE = regexp.MustCompile(`\b(?:requireScope|v2RequireScope|hasAnyScope|scopeAllowed|ScopeMiddleware|AnyScopeMiddleware)\s*\(([^()]*)\)`)

// quotedRE 提取双引号字符串字面量。
var quotedRE = regexp.MustCompile(`"([^"]+)"`)

// scopeLikeRE 只保留形如 "word:word" 的 scope 字面量，过滤掉无关字符串。
var scopeLikeRE = regexp.MustCompile(`^[a-z][a-z0-9]*:[a-z][a-z0-9-]*$`)

// secgroupScopeRE 捕获由 secGroupScopeForMethod 间接返回的 secgroup:read / secgroup:write。
var secgroupScopeRE = regexp.MustCompile(`"(secgroup:[a-z]+)"`)

// frontendCatalogRE 提取前端目录块内单引号包裹的 scope 令牌（中文标签不会被匹配）。
var frontendCatalogRE = regexp.MustCompile(`'([a-z][a-z0-9]*:[a-z][a-z0-9-]*)'`)

// legacyAlternateScopes 是后端为兼容历史集成而接受、但刻意不在前端目录中提供的 scope。
// admin:write 仅出现在 ssh_keys.go 的 hasAnyScope 里作为旧别名，不需要让新密钥勾选。
var legacyAlternateScopes = map[string]bool{
	"admin:write": true,
}

// backendScopeScanDirs 是参与扫描的目录（相对仓库根），二者共用同一套 scope 词表。
var backendScopeScanDirs = []string{
	filepath.Join("backend", "internal", "api"),
	filepath.Join("backend", "internal", "server"),
}

func TestScopeCatalogCoversBackendEnforcedScopes(t *testing.T) {
	root := repoRootFromTest(t)

	catalog, ok := readFrontendScopeCatalog(root)
	if !ok {
		t.Skipf("未找到前端权限目录文件，跳过：%s", filepath.Join(root, "frontend", "src", "pages", "ApiIntegration.tsx"))
	}

	enforced := collectBackendEnforcedScopes(t, root)
	if len(enforced) == 0 {
		t.Fatal("未能从后端源码提取到任何 scope，扫描逻辑可能已失效，请检查 scopeCallRE / 扫描目录")
	}

	// 正向：后端强制的 scope 必须都能在前端目录中勾选。
	var missing []string
	for scope := range enforced {
		if legacyAlternateScopes[scope] {
			continue
		}
		if !catalog[scope] {
			missing = append(missing, scope)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("后端强制校验但前端权限目录缺失下列 scope（会导致 API 页权限不齐全）：\n  %s\n请在 frontend/src/pages/ApiIntegration.tsx 的 scopeGroups 中补齐。",
			strings.Join(missing, "\n  "))
	}

	// 反向：前端目录里的 scope 必须都是后端真正认的，避免僵尸项。
	var orphan []string
	for scope := range catalog {
		if legacyAlternateScopes[scope] {
			continue
		}
		if !enforced[scope] {
			orphan = append(orphan, scope)
		}
	}
	sort.Strings(orphan)
	if len(orphan) > 0 {
		t.Fatalf("前端权限目录列出了后端并未强制的 scope（勾了也不生效）：\n  %s\n请从 frontend/src/pages/ApiIntegration.tsx 的 scopeGroups 中移除，或确认后端已启用。",
			strings.Join(orphan, "\n  "))
	}
}

// repoRootFromTest 依据测试文件自身的路径反推仓库根目录（不依赖运行时工作目录）。
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法获取当前测试文件路径")
	}
	// file = <root>/backend/internal/api/scope_catalog_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// collectBackendEnforcedScopes 遍历后端源码，汇总所有被强制校验的 scope。
func collectBackendEnforcedScopes(t *testing.T, root string) map[string]bool {
	t.Helper()
	enforced := map[string]bool{}
	for _, rel := range backendScopeScanDirs {
		dir := filepath.Join(root, rel)
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			src := string(raw)
			for _, m := range scopeCallRE.FindAllStringSubmatch(src, -1) {
				for _, q := range quotedRE.FindAllStringSubmatch(m[1], -1) {
					if scopeLikeRE.MatchString(q[1]) {
						enforced[q[1]] = true
					}
				}
			}
			// secgroup:read / secgroup:write 由 secGroupScopeForMethod 间接产出，
			// 不一定出现在调用参数里，这里按字面量补扫。
			if strings.HasSuffix(path, "security_groups.go") {
				for _, m := range secgroupScopeRE.FindAllStringSubmatch(src, -1) {
					enforced[m[1]] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("扫描目录失败 %s：%v", dir, err)
		}
	}
	return enforced
}

// readFrontendScopeCatalog 读取并解析前端 scopeGroups 块中的 scope 令牌。
func readFrontendScopeCatalog(root string) (map[string]bool, bool) {
	path := filepath.Join(root, "frontend", "src", "pages", "ApiIntegration.tsx")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	src := string(raw)

	// 将扫描范围限定在 scopeGroups 数组块内，避免匹配到文件其他位置的引号字符串。
	start := strings.Index(src, "const scopeGroups = [")
	if start < 0 {
		return nil, false
	}
	end := strings.Index(src[start:], "const defaultReadScopes")
	if end < 0 {
		end = len(src) - start
	}
	block := src[start : start+end]

	catalog := map[string]bool{}
	for _, m := range frontendCatalogRE.FindAllStringSubmatch(block, -1) {
		catalog[m[1]] = true
	}
	return catalog, true
}
