package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"eyvescloud/internal/cli"
	"eyvescloud/internal/config"
	"eyvescloud/internal/version"
)

// 版本检查结果带短缓存：避免每个面板请求都打 GitHub API（会触发限流）。
// 面板「系统设置 → 版本」每次进入页面触发一次检查，缓存 10 分钟。

type cachedUpdateCheck struct {
	mu        sync.Mutex
	checkedAt time.Time
	result    cli.CheckUpdateResult
}

var updateCheckCache cachedUpdateCheck

// checkUpdateCached 返回缓存中的版本检测结果；超过 ttl 重新检测。
func checkUpdateCached(ttl time.Duration) cli.CheckUpdateResult {
	updateCheckCache.mu.Lock()
	defer updateCheckCache.mu.Unlock()

	if !updateCheckCache.checkedAt.IsZero() && time.Since(updateCheckCache.checkedAt) < ttl {
		return updateCheckCache.result
	}

	result := cli.CheckForUpdate()
	// 缓存任何结果（包括错误），避免连续失败打满 GitHub API。
	updateCheckCache.result = result
	updateCheckCache.checkedAt = time.Now()
	return result
}

// HandleCheckUpdate 返回当前版本与最新版本对比，供面板提示“有可用更新”。
// 仅做检测，不下载也不重启服务（升级由 install.sh / CLI 完成）。
func HandleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	result := checkUpdateCached(10 * time.Minute)
	data := map[string]interface{}{
		"current":    version.Current(),
		"latest":     result.Latest,
		"has_update": result.HasUpdate,
		"err":        result.Err,
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: data})
}

// HandleUpdateReleases 返回指定仓库的可选版本列表（供面板选择升级目标）。
// repo 参数为 "owner/name"，默认官方仓库；严格校验防 SSRF。带 2 分钟短缓存。
func HandleUpdateReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repo == "" {
		repo = version.Repo
	}
	if !cli.ValidateRepoSlug(repo) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "无效的仓库标识，格式应为 owner/name"})
		return
	}

	releases, err := releasesListCached(repo, 2*time.Minute)
	if err != nil {
		jsonResponse(w, http.StatusBadGateway, APIResponse{Success: false, Message: "获取版本列表失败: " + err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{
		"repo":     repo,
		"current":  version.Current(),
		"releases": releases,
	}})
}

type releasesCacheEntry struct {
	fetchedAt time.Time
	items     []cli.GithubReleaseListItem
}

var (
	releasesCacheMu sync.Mutex
	releasesCache   = map[string]releasesCacheEntry{}
)

func releasesListCached(repo string, ttl time.Duration) ([]cli.GithubReleaseListItem, error) {
	releasesCacheMu.Lock()
	defer releasesCacheMu.Unlock()
	if e, ok := releasesCache[repo]; ok && time.Since(e.fetchedAt) < ttl {
		return e.items, nil
	}
	items, err := cli.FetchReleasesList(repo, 20)
	if err != nil {
		return nil, err
	}
	// 缓存容量控制：最多保留 8 个仓库的列表缓存。
	if len(releasesCache) >= 8 {
		releasesCache = map[string]releasesCacheEntry{}
	}
	releasesCache[repo] = releasesCacheEntry{fetchedAt: time.Now(), items: items}
	return items, nil
}

// 面板内升级的状态锁：一次只允许一个升级任务，避免并发触发互相覆盖二进制。
var (
	panelUpgradeMu      sync.Mutex
	panelUpgradeRunning bool
	panelUpgradeStarted time.Time
)

// HandlePanelUpdate 提供「面板内直接升级」：立即在后台启动升级任务（下载→解压→
// 备份→就地替换→重启），本请求同步返回"已开始"。升级完成后面板服务重启。
// 由于升级会重启当前服务（自身进程），不能在请求内等待全部完成。
// 请求体可选 {repo, tag}：repo 为 "owner/name"（默认官方仓库，支持第三方仓库），
// tag 为目标版本（默认最新版）；两者均经严格校验。
func HandlePanelUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	var req struct {
		Repo string `json:"repo"`
		Tag  string `json:"tag"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
			return
		}
	}
	repo := strings.TrimSpace(req.Repo)
	tag := strings.TrimSpace(req.Tag)
	if repo != "" && !cli.ValidateRepoSlug(repo) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "无效的仓库标识，格式应为 owner/name"})
		return
	}
	if tag != "" && !cli.ValidateReleaseTag(tag) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "无效的版本标签"})
		return
	}

	panelUpgradeMu.Lock()
	if panelUpgradeRunning {
		panelUpgradeMu.Unlock()
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "升级任务已在进行中，请稍候"})
		return
	}
	panelUpgradeRunning = true
	panelUpgradeStarted = time.Now()
	panelUpgradeMu.Unlock()

	go func() {
		newVersion, upgraded, err := cli.PanelSelfUpdateTo(repo, tag)
		panelUpgradeMu.Lock()
		panelUpgradeRunning = false
		panelUpgradeMu.Unlock()
		target := repo
		if target == "" {
			target = version.Repo
		}
		if err != nil {
			// 升级失败：记录日志，前端可通过再次 check-update 感知版本未变化。
			fmt.Printf("面板升级失败: %v\n", err)
			config.AddAuditLog("panel.update", version.Current(), fmt.Sprintf("repo=%s tag=%s failed: %s", target, tag, err.Error()), requestUser(r))
			return
		}
		if upgraded {
			config.AddAuditLog("panel.update", newVersion, fmt.Sprintf("repo=%s tag=%s upgraded from %s", target, tag, version.Current()), requestUser(r))
		}
	}()
	jsonResponse(w, http.StatusAccepted, APIResponse{Success: true, Message: "升级已开始，面板将在完成后自动重启"})
}