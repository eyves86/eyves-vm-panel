package cli

// release_signature_test.go —— F-7 签名校验回归锁定。
//
// 覆盖：
//   - 内嵌公钥 + 有效签名 → 通过
//   - 清单被篡改 → 验签失败（中止）
//   - 签名文件缺失 → 默认中止，ALLOW_UNSIGNED=1 放行
//   - 未配置公钥（开发构建）→ 跳过验签不阻断

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyReleaseSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	sign := func(body []byte) []byte {
		sig := ed25519.Sign(priv, body)
		blob := append([]byte{0x00, 0x00}, sig...)
		return []byte("untrusted comment: minisign signature for SHA256SUMS\n" +
			base64.StdEncoding.EncodeToString(blob) + "\n" +
			"untrusted comment: " + pubB64 + "\n" +
			base64.StdEncoding.EncodeToString(sig) + "\n")
	}

	sums := []byte("aaaa  eyvescloud-linux-amd64.tar.gz\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, releaseSignatureSuffix) {
			_, _ = w.Write(sign(sums))
			return
		}
		_, _ = w.Write(sums)
	}))
	defer srv.Close()

	restore := releasePubKeyHex
	releasePubKeyHex = pubB64
	t.Cleanup(func() { releasePubKeyHex = restore })

	t.Run("有效签名通过", func(t *testing.T) {
		if err := verifyReleaseSignature(srv.URL+"/SHA256SUMS", sums); err != nil {
			t.Fatalf("有效签名被拒：%v", err)
		}
	})

	t.Run("清单被篡改则中止", func(t *testing.T) {
		tampered := append([]byte(nil), sums...)
		tampered = append(tampered, []byte("bbbb  evil.tar.gz\n")...)
		if err := verifyReleaseSignature(srv.URL+"/SHA256SUMS", tampered); err == nil {
			t.Fatal("篡改清单未被拦截（F-7 回归！）")
		}
	})

	t.Run("签名文件缺失默认中止", func(t *testing.T) {
		noSig := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		defer noSig.Close()
		t.Setenv("EYVESCLOUD_UPDATE_ALLOW_UNSIGNED", "")
		if err := verifyReleaseSignature(noSig.URL+"/SHA256SUMS", sums); err == nil {
			t.Fatal("签名缺失未中止（F-7 回归！）")
		}
	})

	t.Run("ALLOW_UNSIGNED 显式放行签名缺失", func(t *testing.T) {
		noSig := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		defer noSig.Close()
		t.Setenv("EYVESCLOUD_UPDATE_ALLOW_UNSIGNED", "1")
		if err := verifyReleaseSignature(noSig.URL+"/SHA256SUMS", sums); err != nil {
			t.Fatalf("显式放行仍被拒：%v", err)
		}
	})

	t.Run("未配置公钥的开发构建跳过验签", func(t *testing.T) {
		releasePubKeyHex = ""
		defer func() { releasePubKeyHex = pubB64 }()
		if err := verifyReleaseSignature(srv.URL+"/SHA256SUMS", sums); err != nil {
			t.Fatalf("开发构建不应因签名中断：%v", err)
		}
	})
}
