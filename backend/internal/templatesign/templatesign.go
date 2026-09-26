// Package templatesign 模板签名与开通校验（P5-3）：
//
//   - 签名算法：ed25519（Go 标准库 crypto/ed25519，无新依赖）；
//   - TemplateVerifyResult：校验通过/失败 + 失败原因（"signature_mismatch"/
//     "no_signature" 等，不暴露笼统"失败"）；
//   - 缓存：template_id → VerifyResult，避免每次开通重算大文件 hash；
//   - 全局开关 Strict：true（默认拒绝未签名）/ false（仅警告）；
//   - 签名密钥对：管理员导入公钥，私钥离线管理（生产）；
//     本包 Sign/Verify 由调用方注入 Signer / Verifier 接口。
package templatesign

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Reason 失败原因分类（机器可读 + 人类可读）。
type Reason string

const (
	ReasonOK             Reason = "ok"
	ReasonSignatureMiss  Reason = "signature_missing"
	ReasonSignatureWrong Reason = "signature_mismatch"
	ReasonKeyUnknown     Reason = "key_unknown"
	ReasonHashWrong      Reason = "hash_mismatch"
	ReasonStrictNoSig    Reason = "strict_rejects_unsigned"
)

// VerifyResult 校验结果（不返回原始文件内容，只返回结论）。
type VerifyResult struct {
	TemplateID string `json:"template_id"`
	OK         bool   `json:"ok"`
	Reason     Reason `json:"reason"`
	Detail     string `json:"detail,omitempty"`
	// HashHex 关联的镜像文件 sha256（hex）。
	HashHex string `json:"hash_hex,omitempty"`
}

// KeyID 是签名密钥 ID（管理员导入时分配，便于轮换）。
type KeyID string

// PublicKey 是公钥（base64 编码的 32 字节 ed25519 公钥）。
type PublicKey []byte

// Fingerprint 返回公钥指纹（hex 短串，便于审计对照）。
func (p PublicKey) Fingerprint() string {
	h := sha256.Sum256(p)
	return hex.EncodeToString(h[:])[:16]
}

// KeyStore 公开管理：管理员注入公钥（来自离线私钥对）。
type KeyStore struct {
	mu   sync.RWMutex
	keys map[KeyID]PublicKey
}

// NewKeyStore 创建空 keystore。
func NewKeyStore() *KeyStore {
	return &KeyStore{keys: map[KeyID]PublicKey{}}
}

// Add 注册公钥。
func (k *KeyStore) Add(id KeyID, pub PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("templatesign: public key must be %d bytes, got %d", ed25519.PublicKeySize, len(pub))
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys[id] = pub
	return nil
}

// Get 查询公钥。
func (k *KeyStore) Get(id KeyID) (PublicKey, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	p, ok := k.keys[id]
	return p, ok
}

// ---- 签名 / 校验 ----

// Signer 签名（私钥不在本包内）。
type Signer interface {
	Sign(content []byte) (signature []byte, err error)
}

// Verifier 校验（通常就是 KeyStore.Verify）。
type Verifier interface {
	Verify(content []byte, signature []byte, keyID KeyID) bool
}

// ed25519Signer 默认签名实现。
type ed25519Signer struct {
	priv ed25519.PrivateKey
}

// NewSigner 从 base64 私钥构造 Signer。
func NewSigner(privKeyB64 string) (Signer, error) {
	raw, err := base64.StdEncoding.DecodeString(privKeyB64)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("templatesign: private key must be %d bytes", ed25519.PrivateKeySize)
	}
	return &ed25519Signer{priv: ed25519.PrivateKey(raw)}, nil
}

// Sign 实现签名。
func (s *ed25519Signer) Sign(content []byte) ([]byte, error) {
	return ed25519.Sign(s.priv, content), nil
}

// Verify 实现校验：返回 true 通过；false 不通过。
func (k *KeyStore) Verify(content []byte, signature []byte, keyID KeyID) bool {
	pub, ok := k.Get(keyID)
	if !ok {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), content, signature)
}

// ---- 校验编排 ----

// TemplateManifest 是模板元数据（image 路径 + 签名 + 密钥 ID）。
type TemplateManifest struct {
	TemplateID    string `json:"template_id"`
	ImagePath     string `json:"image_path"`
	ImageSHA256   string `json:"image_sha256"`        // hex
	Signature     string `json:"signature"`            // base64
	SigningKeyID  KeyID  `json:"signing_key_id"`
}

// VerifyTemplate 校验模板签名（缓存友好：同 template_id + image_sha256
// 重复校验直接命中缓存）。
//
// strict=true：未签名模板一律拒绝（ErrStruckHardOfUnsigned）。
// strict=false：未签名模板仅标记 ReasonSignatureMiss，OK=true 让上层决定。
func (k *KeyStore) VerifyTemplate(m TemplateManifest, strict bool, imageContent []byte, cache *VerifyCache) VerifyResult {
	cacheKey := m.TemplateID + "@" + m.ImageSHA256
	if cache != nil {
		if cached, ok := cache.Get(cacheKey); ok {
			return cached
		}
	}

	res := VerifyResult{TemplateID: m.TemplateID, HashHex: m.ImageSHA256}
	// 1. hash 自检
	if m.ImageSHA256 != "" {
		actual := sha256.Sum256(imageContent)
		if hex.EncodeToString(actual[:]) != m.ImageSHA256 {
			res.Reason = ReasonHashWrong
			res.Detail = "image sha256 mismatch"
			if cache != nil {
				cache.Set(cacheKey, res)
			}
			return res
		}
	}
	// 2. 未签名
	if m.Signature == "" || m.SigningKeyID == "" {
		if strict {
			res.Reason = ReasonStrictNoSig
			res.Detail = "unsigned template rejected by strict policy"
			if cache != nil {
				cache.Set(cacheKey, res)
			}
			return res
		}
		res.OK = true // 非严格策略放行
		res.Reason = ReasonSignatureMiss
		res.Detail = "no signature; non-strict policy accepts"
		if cache != nil {
			cache.Set(cacheKey, res)
		}
		return res
	}
	// 3. 公钥未知
	if _, ok := k.Get(m.SigningKeyID); !ok {
		res.Reason = ReasonKeyUnknown
		res.Detail = fmt.Sprintf("signing key %q not in keystore", m.SigningKeyID)
		if cache != nil {
			cache.Set(cacheKey, res)
		}
		return res
	}
	// 4. 签名解码 + 验签
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		res.Reason = ReasonSignatureWrong
		res.Detail = "signature base64 decode: " + err.Error()
		if cache != nil {
			cache.Set(cacheKey, res)
		}
		return res
	}
	if !k.Verify(imageContent, sig, m.SigningKeyID) {
		res.Reason = ReasonSignatureWrong
		res.Detail = "ed25519 signature mismatch (image tampered or wrong key)"
		if cache != nil {
			cache.Set(cacheKey, res)
		}
		return res
	}
	res.OK = true
	res.Reason = ReasonOK
	if cache != nil {
		cache.Set(cacheKey, res)
	}
	return res
}

// SignTemplateManifest 便捷函数：计算 hash + 签名（生产用；测试桩 NoopSigner）。
func SignTemplateManifest(s Signer, templateID, imagePath string, imageContent []byte) (TemplateManifest, error) {
	if s == nil {
		return TemplateManifest{}, errors.New("templatesign: nil signer")
	}
	sig, err := s.Sign(imageContent)
	if err != nil {
		return TemplateManifest{}, err
	}
	h := sha256.Sum256(imageContent)
	return TemplateManifest{
		TemplateID:   templateID,
		ImagePath:    imagePath,
		ImageSHA256:  hex.EncodeToString(h[:]),
		Signature:    base64.StdEncoding.EncodeToString(sig),
		// SigningKeyID 由调用方注入（本函数不知具体 key）。
	}, nil
}

// ---- 缓存 ----

// VerifyCache 是 (template_id@sha256) → VerifyResult 的内存缓存。
type VerifyCache struct {
	mu sync.RWMutex
	m  map[string]VerifyResult
}

// NewVerifyCache 创建空缓存。
func NewVerifyCache() *VerifyCache { return &VerifyCache{m: map[string]VerifyResult{}} }

// Get 取缓存。
func (c *VerifyCache) Get(key string) (VerifyResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.m[key]
	return r, ok
}

// Set 写缓存。
func (c *VerifyCache) Set(key string, r VerifyResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = r
}

// SanityCheck：util 包名覆盖测试。
var _ = strings.TrimSpace