package cli

import (
	"testing"
)

// TestFetchReleasesListLive 验证版本列表在 api.github.com 限流（403/429）时
// 能通过 github.com releases/latest 跳转降级，至少返回最新版一条。
// 依赖真实网络，跳过条件：-short 模式。
func TestFetchReleasesListLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network test skipped in short mode")
	}
	items, err := fetchReleasesList("FenhaoLost/eyves-vm-panel", 20)
	if err != nil {
		t.Fatalf("fetchReleasesList failed: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one release item")
	}
	first := items[0]
	if first.TagName == "" {
		t.Fatal("first item has empty tag_name")
	}
	t.Logf("first release: tag=%s has_asset=%v (items=%d)", first.TagName, first.HasAsset, len(items))
	if !first.HasAsset {
		t.Fatal("first item should have asset for current arch")
	}
}
