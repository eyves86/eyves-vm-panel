package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 节点 Token 静态加密（F7 / P2-11）。
//
// 背景：Node.Token 是主控调用被控 agent API 的 Bearer 凭据。API Key 已用
// argon2id 单向哈希存储，但节点 Token 主控侧必须可逆（转发请求要用明文），
// 因此采用 AES-256-GCM 静态加密落库：
//
//   - 密文格式：enc:v1:<base64(nonce || ciphertext+tag)>
//   - 密钥来源：环境变量 EYVESCLOUD_NODE_TOKEN_KEY（64 位 hex，32 字节）优先；
//     未设置时首次自动生成并写入 <db路径>.tokenkey（权限 0600）。
//   - 兼容存量：无 enc:v1: 前缀的值视为遗留明文，读取原样返回；下次落库时
//     透明重加密，无需停机迁移。
//   - 加解密只发生在存储边界（store_db.go 序列化/反序列化），内存中
//     Node.Token 始终是明文，业务代码（心跳校验、agent 转发）零改动。
//
// 密钥丢失的后果：所有节点 Token 无法解密，节点需要重新注册（重新生成
// install_key）。这与密钥文件 0600 + 数据目录 0700 的保护等级一致。

const (
	nodeTokenEncPrefix = "enc:v1:"
	nodeTokenKeyEnv    = "EYVESCLOUD_NODE_TOKEN_KEY"
)

var (
	nodeTokenKeyOnce sync.Once
	nodeTokenKeyVal  []byte
	nodeTokenKeyErr  error
)

// nodeTokenKey 返回 32 字节 AES 密钥（进程内懒加载并缓存）。
func nodeTokenKey() ([]byte, error) {
	nodeTokenKeyOnce.Do(func() {
		if env := strings.TrimSpace(os.Getenv(nodeTokenKeyEnv)); env != "" {
			key, err := hex.DecodeString(env)
			if err != nil || len(key) != 32 {
				nodeTokenKeyErr = fmt.Errorf("%s 必须是 64 位 hex 字符（32 字节）", nodeTokenKeyEnv)
				return
			}
			nodeTokenKeyVal = key
			return
		}
		key, err := loadOrCreateNodeTokenKeyFile()
		if err != nil {
			nodeTokenKeyErr = err
			return
		}
		nodeTokenKeyVal = key
	})
	return nodeTokenKeyVal, nodeTokenKeyErr
}

// loadOrCreateNodeTokenKeyFile 首次启动自动生成密钥文件（0600）。
func loadOrCreateNodeTokenKeyFile() ([]byte, error) {
	path := getDBPath() + ".tokenkey"
	if raw, err := os.ReadFile(path); err == nil {
		hexKey := strings.TrimSpace(string(raw))
		key, err := hex.DecodeString(hexKey)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("节点 Token 密钥文件 %s 内容无效（需要 64 位 hex）", path)
		}
		return key, nil
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("生成节点 Token 密钥失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("创建密钥目录失败: %w", err)
	}
	// 0600 + 原子写：先写临时文件再 rename，避免部分写入留下坏密钥。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(hex.EncodeToString(key)), 0600); err != nil {
		return nil, fmt.Errorf("写入节点 Token 密钥失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("落盘节点 Token 密钥失败: %w", err)
	}
	_ = os.Chmod(path, 0600)
	return key, nil
}

// EncryptNodeToken 用 AES-256-GCM 加密节点 Token。
// 空串原样返回（无 Token 的节点不需要占位密文）。
func EncryptNodeToken(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	// 已经是密文格式（重复调用幂等）。
	if strings.HasPrefix(plain, nodeTokenEncPrefix) {
		return plain, nil
	}
	key, err := nodeTokenKey()
	if err != nil {
		return "", err
	}
	aesGCM, err := newAESGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}
	sealed := aesGCM.Seal(nonce, nonce, []byte(plain), nil)
	return nodeTokenEncPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptNodeToken 解密节点 Token。无 enc:v1: 前缀的存量明文原样返回
// （透明兼容升级前的数据）；解密失败返回错误（密钥变更/文件损坏需要人工介入）。
func DecryptNodeToken(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, nodeTokenEncPrefix) {
		return stored, nil
	}
	key, err := nodeTokenKey()
	if err != nil {
		return "", err
	}
	aesGCM, err := newAESGCM(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, nodeTokenEncPrefix))
	if err != nil {
		return "", fmt.Errorf("节点 Token 密文 base64 解码失败: %w", err)
	}
	if len(raw) < aesGCM.NonceSize() {
		return "", errors.New("节点 Token 密文长度无效")
	}
	nonce, ciphertext := raw[:aesGCM.NonceSize()], raw[aesGCM.NonceSize():]
	plain, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("节点 Token 解密失败（密钥是否变更？）: %w", err)
	}
	return string(plain), nil
}

func newAESGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
