package session

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCreateSessionReturnsValidIDs(t *testing.T) {
	s := NewStore()
	sess, rt, err := s.CreateSession("u1", "127.0.0.1", "ua/1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || rt.ID == "" {
		t.Fatal("ids must be set")
	}
	if sess.UserID != "u1" {
		t.Fatal("user_id mismatch")
	}
	if rt.FamilyID != sess.FamilyID {
		t.Fatal("refresh family must match session family")
	}
}

func TestRotateRefreshSingleUse(t *testing.T) {
	s := NewStore()
	_, rt, _ := s.CreateSession("u1", "127.0.0.1", "ua", time.Hour)
	// 第一次旋转成功
	rt2, err := s.RotateRefresh(rt.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if rt2.ID == rt.ID {
		t.Fatal("rotation must produce new token id")
	}
	if !s.refreshes[rt.ID].Used {
		t.Fatal("old refresh must be marked used")
	}
}

func TestRotateRefreshReplayInvalidatesFamily(t *testing.T) {
	s := NewStore()
	_, rt, _ := s.CreateSession("u1", "127.0.0.1", "ua", time.Hour)
	_, _ = s.RotateRefresh(rt.ID, time.Hour)
	// 第二次使用相同 refresh → 重放
	_, err := s.RotateRefresh(rt.ID, time.Hour)
	if !errors.Is(err, ErrReplay) {
		t.Fatalf("expected ErrReplay, got %v", err)
	}
	if !s.FamilyRevoked(s.refreshes[rt.ID].FamilyID) {
		t.Fatal("family must be revoked after replay")
	}
}

func TestReplayRevokesAllFamilySessions(t *testing.T) {
	s := NewStore()
	// 创建两个会话但把第二个塞到同一个 family（模拟同一登录族）
	_, rt, _ := s.CreateSession("u1", "127.0.0.1", "ua", time.Hour)
	sess2 := &Session{
		ID:         "sess-2",
		UserID:     "u1",
		FamilyID:   s.refreshes[rt.ID].FamilyID,
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
	}
	s.mu.Lock()
	s.sessions[sess2.ID] = sess2
	s.mu.Unlock()
	// 第一次合法旋转
	_, _ = s.RotateRefresh(rt.ID, time.Hour)
	// 第二次重放
	_, err := s.RotateRefresh(rt.ID, time.Hour)
	if !errors.Is(err, ErrReplay) {
		t.Fatal("second rotate must return ErrReplay")
	}
	if !sess2.Revoked {
		t.Fatal("sess2 in same family must be revoked")
	}
}

func TestRotateRefreshExpired(t *testing.T) {
	s := NewStore()
	_, rt, _ := s.CreateSession("u1", "127.0.0.1", "ua", time.Hour)
	// 直接把 refresh 标记为过期
	s.mu.Lock()
	s.refreshes[rt.ID].ExpiresAt = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if _, err := s.RotateRefresh(rt.ID, time.Hour); err == nil {
		t.Fatal("expired refresh must error")
	}
}

func TestRotateRefreshUnknownID(t *testing.T) {
	s := NewStore()
	if _, err := s.RotateRefresh("ghost", time.Hour); err == nil {
		t.Fatal("unknown refresh must error")
	}
}

func TestRevokeSessionIsolated(t *testing.T) {
	s := NewStore()
	sess1, _, _ := s.CreateSession("u1", "127.0.0.1", "ua1", time.Hour)
	sess2, _, _ := s.CreateSession("u1", "127.0.0.2", "ua2", time.Hour)
	if err := s.RevokeSession(sess1.ID); err != nil {
		t.Fatal(err)
	}
	all := s.ListSessions("u1")
	if len(all) != 1 {
		t.Fatalf("revoked session must be filtered, got %d", len(all))
	}
	if all[0].ID != sess2.ID {
		t.Fatalf("remaining session id mismatch: %s vs %s", all[0].ID, sess2.ID)
	}
}

func TestListSessionsSortedByLastActive(t *testing.T) {
	s := NewStore()
	_, _, _ = s.CreateSession("u1", "127.0.0.1", "ua1", time.Hour)
	sess2, _, _ := s.CreateSession("u1", "127.0.0.2", "ua2", time.Hour)
	s.Touch(sess2.ID)
	all := s.ListSessions("u1")
	if len(all) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(all))
	}
	if all[0].ID != sess2.ID {
		t.Fatal("most recently active must be first")
	}
}

func TestChallengeLifecycle(t *testing.T) {
	c := NewChallengeStore()
	ch, err := c.NewChallenge("u1", ChallengeRegistration, "localhost", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID == "" || len(ch.Challenge) != 32 {
		t.Fatal("challenge shape wrong")
	}
	got, err := c.ConsumeChallenge(ch.ID, ChallengeRegistration)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Challenge) != string(ch.Challenge) {
		t.Fatal("challenge bytes mismatch")
	}
	// 二次消费必须失败
	if _, err := c.ConsumeChallenge(ch.ID, ChallengeRegistration); err == nil {
		t.Fatal("second consume must fail")
	}
}

func TestChallengeExpired(t *testing.T) {
	c := NewChallengeStore()
	ch, _ := c.NewChallenge("u1", ChallengeAssertion, "localhost", time.Hour)
	// 直接把 challenge 标记为过期
	c.mu.Lock()
	c.challenges[ch.ID].ExpiresAt = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if _, err := c.ConsumeChallenge(ch.ID, ChallengeAssertion); err == nil {
		t.Fatal("expired challenge must error")
	}
}

func TestChallengeTypeMismatch(t *testing.T) {
	c := NewChallengeStore()
	ch, _ := c.NewChallenge("u1", ChallengeRegistration, "localhost", time.Hour)
	if _, err := c.ConsumeChallenge(ch.ID, ChallengeAssertion); err == nil {
		t.Fatal("type mismatch must error")
	}
}

func TestFingerprintIsStable(t *testing.T) {
	if Fingerprint("ua", "1.2.3.4") != Fingerprint("ua", "1.2.3.4") {
		t.Fatal("fingerprint must be stable for identical inputs")
	}
	if Fingerprint("ua", "1.2.3.4") == Fingerprint("ua", "1.2.3.5") {
		t.Fatal("different IPs must produce different fingerprints")
	}
}

func TestConcurrentRotateRefresh(t *testing.T) {
	s := NewStore()
	_, rt, _ := s.CreateSession("u1", "127.0.0.1", "ua", time.Hour)
	var wg sync.WaitGroup
	const goroutines = 10
	wg.Add(goroutines)
	replays := 0
	var mu sync.Mutex
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := s.RotateRefresh(rt.ID, time.Hour)
			if errors.Is(err, ErrReplay) {
				mu.Lock()
				replays++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	// 只有一个 goroutine 能成功旋转，其余 9 个必须检测为重放
	if replays != goroutines-1 {
		t.Fatalf("expected %d replays, got %d", goroutines-1, replays)
	}
}