// releasetool —— 发行版签名工具（CI 与维护者使用，不参与面板运行时）。
//
// 子命令：
//
//	releasetool keygen                     生成 ed25519 密钥对，输出 seed(hex)/pubkey(hex)
//	releasetool sign -key <seedhex> -in F  对文件签名，输出 minisign 信封到 stdout
//	releasetool verify -pub <pubhex> -in F -sig S   验签（CI 自检用）
//
// 发布约定：对 SHA256SUMS 的原始字节签名，产出 SHA256SUMS.minisig 上传为 Release
// 资产；升级器用内嵌公钥（EYVESCLOUD_RELEASE_PUBKEY）验签，见 internal/cli/release_signature.go。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
)

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "releasetool: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		die("用法：releasetool keygen | sign | verify（-h 查看参数）")
	}
	switch os.Args[1] {
	case "keygen":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			die("生成密钥失败: %v", err)
		}
		seed := priv.Seed()
		fmt.Printf("EYVESCLOUD_SIGNING_KEY (私密，放 CI Secret / 离线保管):\n%s\n\n", hex.EncodeToString(seed))
		fmt.Printf("EYVESCLOUD_RELEASE_PUBKEY (公开，内嵌进升级器):\n%s\n",
			base64.StdEncoding.EncodeToString(pub))
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ExitOnError)
		keyHex := fs.String("key", "", "ed25519 私钥 seed（hex，32 字节）")
		in := fs.String("in", "", "待签名文件（如 SHA256SUMS）")
		_ = fs.Parse(os.Args[2:])
		if *keyHex == "" || *in == "" {
			die("sign 需要 -key 与 -in")
		}
		seed, err := hex.DecodeString(strings.TrimSpace(*keyHex))
		if err != nil || len(seed) != ed25519.SeedSize {
			die("-key 必须是 64 位 hex（32 字节 ed25519 seed）")
		}
		priv := ed25519.NewKeyFromSeed(seed)
		data, err := os.ReadFile(*in)
		if err != nil {
			die("读取 %s 失败: %v", *in, err)
		}
		sig := ed25519.Sign(priv, data)
		// minisign 信封：2 字节算法号 0x0000 + 签名。
		blob := append([]byte{0x00, 0x00}, sig...)
		pub, _ := priv.Public().(ed25519.PublicKey)
		fmt.Printf("untrusted comment: minisign signature for %s\n%s\nuntrusted comment: %s\n%s\n",
			strings.TrimSpace(*in),
			base64.StdEncoding.EncodeToString(blob),
			base64.StdEncoding.EncodeToString(pub),
			base64.StdEncoding.EncodeToString(sig))
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		pubB64 := fs.String("pub", "", "ed25519 公钥（base64）")
		in := fs.String("in", "", "原文件")
		sigFile := fs.String("sig", "", "签名文件")
		_ = fs.Parse(os.Args[2:])
		if *pubB64 == "" || *in == "" || *sigFile == "" {
			die("verify 需要 -pub / -in / -sig")
		}
		pubRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*pubB64))
		if err != nil || len(pubRaw) != ed25519.PublicKeySize {
			die("-pub 无效")
		}
		data, err1 := os.ReadFile(*in)
		sigContent, err2 := os.ReadFile(*sigFile)
		if err1 != nil || err2 != nil {
			die("读取文件失败")
		}
		lines := strings.Split(strings.TrimSpace(string(sigContent)), "\n")
		if len(lines) < 2 {
			die("签名文件格式无效")
		}
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
		if err != nil {
			die("签名 base64 解码失败: %v", err)
		}
		if len(sig) == ed25519.SignatureSize+2 {
			sig = sig[2:]
		}
		if !ed25519.Verify(ed25519.PublicKey(pubRaw), data, sig) {
			die("验签失败")
		}
		fmt.Println("verify: OK")
	default:
		die("未知子命令：%s", os.Args[1])
	}
}
