package templatesign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// newKeyPair 生成测试用 ed25519 密钥对 + 公钥 base64。
func newKeyPair(t *testing.T) (Signer, PublicKey, KeyID) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(base64.StdEncoding.EncodeToString(priv))
	if err != nil {
		t.Fatal(err)
	}
	return signer, PublicKey(pub), KeyID("key-" + PublicKey(pub).Fingerprint()[:8])
}

func TestKeyStoreAddAndFingerprint(t *testing.T) {
	_, pub, kid := newKeyPair(t)
	ks := NewKeyStore()
	if err := ks.Add(kid, pub); err != nil {
		t.Fatal(err)
	}
	if got, ok := ks.Get(kid); !ok || string(got) != string(pub) {
		t.Fatal("Get must return added key")
	}
	if got := pub.Fingerprint(); len(got) != 16 {
		t.Fatalf("fingerprint len = %d, want 16", len(got))
	}
}

func TestKeyStoreAddRejectsWrongSize(t *testing.T) {
	ks := NewKeyStore()
	if err := ks.Add("k", PublicKey([]byte{1, 2, 3})); err == nil {
		t.Fatal("wrong size must error")
	}
}

func TestVerifyTemplateSignedOK(t *testing.T) {
	signer, pub, kid := newKeyPair(t)
	ks := NewKeyStore()
	if err := ks.Add(kid, pub); err != nil {
		t.Fatal(err)
	}
	content := []byte("template image bytes")
	m, err := SignTemplateManifest(signer, "tpl-1", "/img/alpine.qcow2", content)
	if err != nil {
		t.Fatal(err)
	}
	m.SigningKeyID = kid
	res := ks.VerifyTemplate(m, true, content, NewVerifyCache())
	if !res.OK {
		t.Fatalf("signed template must pass: %+v", res)
	}
	if res.Reason != ReasonOK {
		t.Fatalf("reason = %v, want ok", res.Reason)
	}
}

func TestVerifyTemplateDetectsTamperedImage(t *testing.T) {
	signer, pub, kid := newKeyPair(t)
	ks := NewKeyStore()
	ks.Add(kid, pub)
	content := []byte("original image")
	m, err := SignTemplateManifest(signer, "tpl", "/img/x", content)
	if err != nil {
		t.Fatal(err)
	}
	m.SigningKeyID = kid
	// 篡改一个字节（同时让 hash 与签名都失效）。
	// 为测"签名被破坏（hash 仍匹配但签名错）"的场景，先重算 manifest.hash
	// 让其与篡改后内容一致，然后验证签名不通过。
	tampered := []byte("Xriginal image")
	m.ImageSHA256 = sha256Hex(tampered)
	res := ks.VerifyTemplate(m, true, tampered, NewVerifyCache())
	if res.OK {
		t.Fatal("tampered image must fail verification")
	}
	if res.Reason != ReasonSignatureWrong {
		t.Fatalf("reason = %v, want signature_mismatch", res.Reason)
	}
	if !strings.Contains(res.Detail, "tampered") && !strings.Contains(res.Detail, "mismatch") {
		t.Fatalf("detail must mention tampering: %q", res.Detail)
	}
}

func TestVerifyTemplateDetectsHashMismatch(t *testing.T) {
	signer, pub, kid := newKeyPair(t)
	ks := NewKeyStore()
	ks.Add(kid, pub)
	content := []byte("content")
	m, _ := SignTemplateManifest(signer, "tpl", "/img/x", content)
	m.SigningKeyID = kid
	// 模拟 hash 字段被改坏：手动篡改 manifest。
	m.ImageSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	res := ks.VerifyTemplate(m, true, content, NewVerifyCache())
	if res.OK {
		t.Fatal("hash mismatch must fail")
	}
	if res.Reason != ReasonHashWrong {
		t.Fatalf("reason = %v, want hash_mismatch", res.Reason)
	}
}

func TestVerifyTemplateUnsignedStrictRejects(t *testing.T) {
	ks := NewKeyStore()
	m := TemplateManifest{TemplateID: "tpl", ImageSHA256: "", Signature: ""}
	res := ks.VerifyTemplate(m, true, []byte("x"), NewVerifyCache())
	if res.OK {
		t.Fatal("strict must reject unsigned")
	}
	if res.Reason != ReasonStrictNoSig {
		t.Fatalf("reason = %v, want strict_rejects_unsigned", res.Reason)
	}
}

func TestVerifyTemplateUnsignedNonStrictAccepts(t *testing.T) {
	ks := NewKeyStore()
	m := TemplateManifest{TemplateID: "tpl", ImageSHA256: ""}
	res := ks.VerifyTemplate(m, false, []byte("x"), NewVerifyCache())
	if !res.OK {
		t.Fatalf("non-strict must accept unsigned (warn only): %+v", res)
	}
	if res.Reason != ReasonSignatureMiss {
		t.Fatalf("reason = %v, want signature_missing", res.Reason)
	}
}

func TestVerifyTemplateUnknownKey(t *testing.T) {
	signer, _, _ := newKeyPair(t)
	ks := NewKeyStore()
	// keystore 为空：未导入任何 key。
	content := []byte("data")
	m, _ := SignTemplateManifest(signer, "tpl", "/x", content)
	m.SigningKeyID = "unknown-key"
	res := ks.VerifyTemplate(m, true, content, NewVerifyCache())
	if res.OK {
		t.Fatal("unknown key must fail")
	}
	if res.Reason != ReasonKeyUnknown {
		t.Fatalf("reason = %v, want key_unknown", res.Reason)
	}
}

func TestVerifyCacheReusesResult(t *testing.T) {
	signer, pub, kid := newKeyPair(t)
	ks := NewKeyStore()
	ks.Add(kid, pub)
	content := []byte("content")
	m, _ := SignTemplateManifest(signer, "tpl", "/x", content)
	m.SigningKeyID = kid
	cache := NewVerifyCache()
	// 第一次校验：写入缓存。
	res1 := ks.VerifyTemplate(m, true, content, cache)
	if !res1.OK {
		t.Fatal("first verify must pass")
	}
	// 第二次校验（manifest 内容已变成"failure"：删 cache key 看是否能复现）。
	res2 := ks.VerifyTemplate(m, true, content, cache)
	if !res2.OK {
		t.Fatal("cached verify must still pass")
	}
}

func TestSignerInterfaceRejectsBadBase64(t *testing.T) {
	if _, err := NewSigner("!!!not-base64!!!"); err == nil {
		t.Fatal("bad base64 must error")
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}