package config

// store_index_test.go —— 子用户下标缓存的自愈语义守卫。
//
// claimsFromToken 的 TokenVersion 吊销校验走 FindSubUserIndexByNameUnlocked；
// 下标一旦「错」就是把别人的 TokenVersion 当成自己的——要么放行已吊销令牌，
// 要么把有效令牌当已吊销。这里钉死：删行/换位后下标必须自愈，且查到的行
// TokenVersion 必须与该用户名一致（行一致性）。
import (
	"sync"
	"testing"
)

var subUserTestMu sync.Mutex

func seedSubUserIndex(t *testing.T, sus []SubUser) {
	t.Helper()
	subUserTestMu.Lock()
	t.Cleanup(subUserTestMu.Unlock)
	prev := AppConfig
	t.Cleanup(func() { AppConfig = prev })
	t.Cleanup(resetRowIndexes)
	AppConfigMu.Lock()
	AppConfig = &EyvescloudConfig{SubUsers: sus}
	resetRowIndexes()
	AppConfigMu.Unlock()

	// seed 后全部可查且行一致（回填路径）。
	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	for _, su := range sus {
		i, ok := FindSubUserIndexByNameUnlocked(su.Username)
		if !ok || AppConfig.SubUsers[i].Username != su.Username {
			t.Fatalf("seed 后查 %q：ok=%v", su.Username, ok)
		}
	}
}

func TestSubUserIndexSelfHealsAfterDeleteShift(t *testing.T) {
	seedSubUserIndex(t, []SubUser{
		{Username: "su-a", TokenVersion: 10},
		{Username: "su-b", TokenVersion: 20},
		{Username: "su-c", TokenVersion: 30},
	})

	// 删除 su-a：su-b/su-c 前移，su-b 的缓存下标 1 现在指向 su-c。
	// 命中校验必须发现失配并整表重建——这是「下标错=吊销错」的关键场景。
	AppConfigMu.Lock()
	AppConfig.SubUsers = AppConfig.SubUsers[1:]

	i, ok := FindSubUserIndexByNameUnlocked("su-b")
	if !ok || i != 0 || AppConfig.SubUsers[i].TokenVersion != 20 {
		t.Fatalf("删除前移后查 su-b：ok=%v idx=%d, want idx=0 version=20", ok, i)
	}
	i, ok = FindSubUserIndexByNameUnlocked("su-c")
	if !ok || i != 1 || AppConfig.SubUsers[i].TokenVersion != 30 {
		t.Fatalf("删除前移后查 su-c：ok=%v idx=%d, want idx=1 version=30", ok, i)
	}

	// 已删除的用户名必须查不到（旧缓存条目不得让删除复活）。
	if _, ok := FindSubUserIndexByNameUnlocked("su-a"); ok {
		t.Fatal("su-a 已删除仍能查到：删除子用户后旧令牌不会失效")
	}
	AppConfigMu.Unlock()
}

func TestSubUserIndexBackfillsNewRows(t *testing.T) {
	seedSubUserIndex(t, []SubUser{{Username: "su-a", TokenVersion: 1}})

	// 追加新行后直接查新用户名：缺失 ⇒ 线性补一条，不整表重建。
	AppConfigMu.Lock()
	AppConfig.SubUsers = append(AppConfig.SubUsers, SubUser{Username: "su-new", TokenVersion: 9})
	i, ok := FindSubUserIndexByNameUnlocked("su-new")
	if !ok || i != 1 || AppConfig.SubUsers[i].TokenVersion != 9 {
		t.Fatalf("追加后查 su-new：ok=%v idx=%d, want idx=1 version=9", ok, i)
	}
	if _, ok := FindSubUserIndexByNameUnlocked("su-a"); !ok {
		t.Fatal("追加后旧用户名 su-a 必须仍可查到")
	}
	AppConfigMu.Unlock()
}

func TestSubUserIndexRejectsEmptyAndUnknown(t *testing.T) {
	seedSubUserIndex(t, []SubUser{{Username: "su-a"}})

	AppConfigMu.RLock()
	defer AppConfigMu.RUnlock()
	if _, ok := FindSubUserIndexByNameUnlocked(""); ok {
		t.Fatal("空用户名不得命中")
	}
	if _, ok := FindSubUserIndexByNameUnlocked("su-none"); ok {
		t.Fatal("未知用户名不得命中")
	}
}
