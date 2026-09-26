// Package session 实现 P8-2 会话管控核心：
//
//   - RefreshToken 族（family）：每次刷新签发新 refresh，旧 refresh 被用即
//     判定为重放 → 整个 family 撤销 → 高危安全事件；
//   - Session：会话（设备/IP/UA），可单独撤销不影响其他会话；
//   - PasskeyChallenge：WebAuthn challenge 注册/认证握手桩（生产替换为
//     go-webauthn/webauthn 等成熟库；本包提供 challenge 生命周期管理）。
//
// 本包只产出状态机原语；HTTP 集成与 2FA 强制逻辑由调用方注入。
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Session 一个登录会话。
type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	FamilyID   string    `json:"family_id"` // refresh token family
	Device     string    `json:"device"`    // UA 解析
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastActive time.Time `json:"last_active"`
	Revoked   bool      `json:"revoked"`
}

// RefreshToken refresh token + 关联元数据。
type RefreshToken struct {
	ID        string    `json:"id"`
	FamilyID  string    `json:"family_id"`
	UserID    string    `json:"user_id"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
}

// Store 会话+refresh token 内存仓库（生产换 SQLite）。
type Store struct {
	mu        sync.Mutex
	sessions  map[string]*Session
	refreshes map[string]*RefreshToken
	families  map[string]bool // 已撤销 family
}

// NewStore 构造空仓库。
func NewStore() *Store {
	return &Store{
		sessions:  map[string]*Session{},
		refreshes: map[string]*RefreshToken{},
		families:  map[string]bool{},
	}
}

// CreateSession 新建会话（含初始 refresh token）。
func (s *Store) CreateSession(userID, ip, ua string, ttl time.Duration) (Session, RefreshToken, error) {
	if userID == "" {
		return Session{}, RefreshToken{}, errors.New("session: user_id required")
	}
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	familyID := randomID(16)
	now := time.Now()
	sess := &Session{
		ID:         randomID(16),
		UserID:     userID,
		FamilyID:   familyID,
		Device:     parseDevice(ua),
		IP:         ip,
		UserAgent:   ua,
		CreatedAt:  now,
		LastActive: now,
	}
	rt := &RefreshToken{
		ID:        randomID(24),
		FamilyID:  familyID,
		UserID:    userID,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.refreshes[rt.ID] = rt
	s.mu.Unlock()
	return *sess, *rt, nil
}

// RotateRefresh 旋转 refresh token：旧 token 标记 used 并签发新 token。
// 若旧 token 已 used，判定为重放 → 整个 family 撤销 + 返回 ErrReplay。
func (s *Store) RotateRefresh(refreshID string, ttl time.Duration) (RefreshToken, error) {
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.refreshes[refreshID]
	if !ok {
		return RefreshToken{}, fmt.Errorf("session: refresh token %q not found", refreshID)
	}
	if old.Used {
		// 重放：撤销整族
		s.families[old.FamilyID] = true
		for _, rt := range s.refreshes {
			if rt.FamilyID == old.FamilyID {
				rt.Used = true
			}
		}
		for _, sess := range s.sessions {
			if sess.FamilyID == old.FamilyID {
				sess.Revoked = true
			}
		}
		return RefreshToken{}, ErrReplay
	}
	if time.Now().After(old.ExpiresAt) {
		return RefreshToken{}, fmt.Errorf("session: refresh token %q expired", refreshID)
	}
	// 正常旋转：旧 token 标记 used，同 family 签发新 token。
	now := time.Now()
	newRT := &RefreshToken{
		ID:        randomID(24),
		FamilyID:  old.FamilyID,
		UserID:    old.UserID,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
	}
	old.Used = true
	s.refreshes[newRT.ID] = newRT
	return *newRT, nil
}

// ErrReplay 表示 refresh token 被重放使用。
var ErrReplay = errors.New("session: refresh token replay detected")

// FamilyRevoked 查询 family 是否已撤销（重放后置 true）。
func (s *Store) FamilyRevoked(familyID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.families[familyID]
}

// RevokeSession 撤销单个会话（不影响其它会话）。
func (s *Store) RevokeSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session: session %q not found", sessionID)
	}
	sess.Revoked = true
	return nil
}

// ListSessions 列出某用户所有未撤销会话（按最近活跃倒序）。
func (s *Store) ListSessions(userID string) []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0)
	for _, sess := range s.sessions {
		if sess.UserID != userID || sess.Revoked {
			continue
		}
		out = append(out, *sess)
	}
	// 简单排序：最近活跃靠前
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].LastActive.After(out[i].LastActive) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Touch 更新会话最近活跃时间。
func (s *Store) Touch(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[sessionID]; ok && !sess.Revoked {
		sess.LastActive = time.Now()
	}
}

// ChallengeType 区分注册 vs 认证。
type ChallengeType string

const (
	ChallengeRegistration ChallengeType = "registration"
	ChallengeAssertion    ChallengeType = "assertion"
)

// PasskeyChallenge WebAuthn challenge 记录。
type PasskeyChallenge struct {
	ID        string
	UserID    string
	Type      ChallengeType
	Challenge []byte
	CreatedAt time.Time
	ExpiresAt time.Time
	// 用于注册：会话内期望的 credential parameters (RP ID 等由调用方传入)
	RPID string
}

// ChallengeStore 临时 challenge 仓库。
type ChallengeStore struct {
	mu         sync.Mutex
	challenges map[string]*PasskeyChallenge
}

// NewChallengeStore 构造 challenge 仓库。
func NewChallengeStore() *ChallengeStore {
	return &ChallengeStore{challenges: map[string]*PasskeyChallenge{}}
}

// maxChallenges 限制未消费 challenge 的上限，防止重复调用注册/认证入口
// 造成内存无限增长（DoS）。
const maxChallenges = 4096

// NewChallenge 生成 32 字节随机 challenge，记录到仓库，ttl 后过期。
//
// 写入前会清理过期条目；若清理后仍超过 maxChallenges 上限，则拒绝签发，
// 避免被刷爆内存。
func (c *ChallengeStore) NewChallenge(userID string, kind ChallengeType, rpID string, ttl time.Duration) (PasskeyChallenge, error) {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	now := time.Now()
	c.mu.Lock()
	c.purgeExpiredLocked(now)
	if len(c.challenges) >= maxChallenges {
		c.mu.Unlock()
		return PasskeyChallenge{}, errors.New("session: too many pending challenges")
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		c.mu.Unlock()
		return PasskeyChallenge{}, err
	}
	ch := &PasskeyChallenge{
		ID:        randomID(16),
		UserID:    userID,
		Type:      kind,
		Challenge: buf,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		RPID:      rpID,
	}
	c.challenges[ch.ID] = ch
	c.mu.Unlock()
	return *ch, nil
}

// purgeExpiredLocked 删除已过期的 challenge（调用方须持锁）。
func (c *ChallengeStore) purgeExpiredLocked(now time.Time) {
	for id, ch := range c.challenges {
		if now.After(ch.ExpiresAt) {
			delete(c.challenges, id)
		}
	}
}

// ConsumeChallenge 按 ID 取并删除 challenge；过期/不存在/类型不匹配都返回错误。
func (c *ChallengeStore) ConsumeChallenge(id string, kind ChallengeType) (PasskeyChallenge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.challenges[id]
	if !ok {
		return PasskeyChallenge{}, errors.New("session: challenge not found")
	}
	delete(c.challenges, id)
	if time.Now().After(ch.ExpiresAt) {
		return PasskeyChallenge{}, errors.New("session: challenge expired")
	}
	if ch.Type != kind {
		return PasskeyChallenge{}, fmt.Errorf("session: challenge type mismatch: %s != %s", ch.Type, kind)
	}
	return *ch, nil
}

// randomID 生成 crypto/rand 随机 ID（hex 编码）。
func randomID(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// Fingerprint 计算 UA/IP/用户名的稳定指纹，用于异地登录检测。
func Fingerprint(ua, ip string) string {
	h := sha256.New()
	h.Write([]byte(ua))
	h.Write([]byte{0})
	h.Write([]byte(ip))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// parseDevice 简单解析 UA 前 50 字符作为设备标签。
func parseDevice(ua string) string {
	if len(ua) > 50 {
		return ua[:50]
	}
	return ua
}