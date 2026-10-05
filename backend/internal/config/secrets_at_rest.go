package config

import (
	"encoding/json"
	"strings"
)

// secrets_at_rest.go —— 其它需要「可逆但不明文落库」的凭据（审计 H-5 修复）。
//
// 背景：容器 SSH 口令、子用户的登录口令与分享访问码都必须能被面板回显
// （创建/轮换时一次性展示、分享链接需要访问码），因此无法单向哈希。此前它们
// 以明文写入 SQLite，一旦数据库文件或配置备份泄露即为可直接使用的登录凭据。
//
// 处理方式：复用节点 Token 的 AES-256-GCM 静态加密实现（密钥来源见
// nodetoken_crypto.go：EYVESCLOUD_NODE_TOKEN_KEY 环境变量，或数据目录下
// 0600 的 <db>.tokenkey），在**存储边界**加解密——内存中始终是明文，业务代码
// 零改动；存量明文（无 enc:v1: 前缀）读取时原样通过，下次落库自动重加密。

// EncryptSecretAtRest 加密待落库的凭据；空串原样返回。
func EncryptSecretAtRest(plain string) string {
	if plain == "" {
		return ""
	}
	enc, err := EncryptNodeToken(plain)
	if err != nil {
		// 加密失败时拒绝静默写入明文：返回空串让调用方以空值落库，
		// 该凭据随后会走"未设置"路径（而不是把明文写进数据库）。
		return ""
	}
	return enc
}

// DecryptSecretAtRest 还原落库的凭据。存量明文（无前缀）原样返回；
// 解密失败返回空串（凭据视为不可用，需重新设置/轮换，绝不把密文当明文用）。
func DecryptSecretAtRest(stored string) string {
	if stored == "" {
		return ""
	}
	plain, err := DecryptNodeToken(stored)
	if err != nil {
		return ""
	}
	return plain
}

// EncryptSecretSlice / DecryptSecretSlice 便于容器、子用户等切片字段批量处理。
func EncryptSecretSlice(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, EncryptSecretAtRest(v))
	}
	return out
}

// DecryptSecretSlice 是 EncryptSecretSlice 的逆操作。
func DecryptSecretSlice(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, DecryptSecretAtRest(v))
	}
	return out
}

// EncryptSecretsForExport 就地加密"导出到磁盘/外部"的凭据副本。
//
// 适用场景：配置备份 JSON、迁移包等会把整份配置序列化到磁盘或通过网络传输。
// 这些文件此前包含节点 Token、容器 SSH 口令、Turnstile 密钥、对接密钥等**明文
// 凭据**，一旦备份文件泄露（备份目录、下载接口、异地副本）等同于交出整个集群。
// 导出前统一用本地 at-rest 密钥加密（enc:v1: 前缀），导入时用
// DecryptSecretsAfterImport 还原，保持同机备份/还原的完整可用性。
//
// 注意：传入的必须是**副本**（调用方自行深拷贝），本函数就地修改。
func EncryptSecretsForExport(cfg *EyvescloudConfig) {
	if cfg == nil {
		return
	}
	for i := range cfg.Containers {
		cfg.Containers[i].SSHPassword = EncryptSecretAtRest(cfg.Containers[i].SSHPassword)
		// 机器级访问码凭据（分享登录）同样必须加密，避免备份文件泄露即等于交出机器。
		cfg.Containers[i].AccessCode = EncryptSecretAtRest(cfg.Containers[i].AccessCode)
		cfg.Containers[i].AccessCodePassword = EncryptSecretAtRest(cfg.Containers[i].AccessCodePassword)
	}
	for i := range cfg.Nodes {
		cfg.Nodes[i].Token = EncryptSecretAtRest(cfg.Nodes[i].Token)
		cfg.Nodes[i].InstallKey = EncryptSecretAtRest(cfg.Nodes[i].InstallKey)
	}
	cfg.TurnstileSecretKey = EncryptSecretAtRest(cfg.TurnstileSecretKey)
	cfg.AgentPairingKey = EncryptSecretAtRest(cfg.AgentPairingKey)
	cfg.UpdateSource.Token = EncryptSecretAtRest(cfg.UpdateSource.Token)
	if cfg.SMTPSettings.Password != "" {
		cfg.SMTPSettings.Password = EncryptSecretAtRest(cfg.SMTPSettings.Password)
	}
	// JWT 签名密钥与 Webhook HMAC 密钥同样属于"泄露即可伪造凭据/回调"的秘密，
	// 备份文件必须一并加密（gosec G117 提示的正是这条路径）。
	cfg.JWTSecret = EncryptSecretAtRest(cfg.JWTSecret)
	for i := range cfg.Webhooks {
		cfg.Webhooks[i].Secret = EncryptSecretAtRest(cfg.Webhooks[i].Secret)
	}
	for i := range cfg.SubUsers {
		cfg.SubUsers[i].Password = EncryptSecretAtRest(cfg.SubUsers[i].Password)
		cfg.SubUsers[i].AccessCode = EncryptSecretAtRest(cfg.SubUsers[i].AccessCode)
	}
	for i := range cfg.Tasks {
		// SavedTask.Config 是 JSON 字符串（内含 ssh_password），单独处理。
		cfg.Tasks[i].Config = transformTaskConfigSecret(cfg.Tasks[i].Config, EncryptSecretAtRest)
	}
}

// transformTaskConfigSecret 在 SavedTask.Config（JSON 字符串）里就地替换
// ssh_password 字段；解析失败或字段缺失时原样返回（不改动任务负载）。
func transformTaskConfigSecret(configJSON string, fn func(string) string) string {
	if strings.TrimSpace(configJSON) == "" {
		return configJSON
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &obj); err != nil {
		return configJSON
	}
	raw, ok := obj["ssh_password"].(string)
	if !ok || raw == "" {
		return configJSON
	}
	obj["ssh_password"] = fn(raw)
	out, err := json.Marshal(obj)
	if err != nil {
		return configJSON
	}
	return string(out)
}

// DecryptSecretsAfterImport 是 EncryptSecretsForExport 的逆操作：
// 导入外部分份/迁移包后把 enc:v1: 密文还原为明文（内存态始终明文）。
//
//   - 存量备份为明文 → 原样通过（向后兼容）；
//   - 密文无法解密（换过 at-rest 密钥 / 跨面板迁移）→ 该字段置空并回传无法
//     解密的字段名，由调用方提示管理员"需重新设置"，绝不把密文当明文使用。
func DecryptSecretsAfterImport(cfg *EyvescloudConfig) []string {
	if cfg == nil {
		return nil
	}
	var failed []string
	decryptField := func(name, value string) string {
		if value == "" {
			return ""
		}
		if !strings.HasPrefix(value, nodeTokenEncPrefix) {
			return value // 存量明文
		}
		plain, err := DecryptNodeToken(value)
		if err != nil {
			failed = append(failed, name)
			return ""
		}
		return plain
	}
	for i := range cfg.Containers {
		cfg.Containers[i].SSHPassword = decryptField(
			"container:"+cfg.Containers[i].Name+".ssh_password", cfg.Containers[i].SSHPassword)
		cfg.Containers[i].AccessCode = decryptField(
			"container:"+cfg.Containers[i].Name+".access_code", cfg.Containers[i].AccessCode)
		cfg.Containers[i].AccessCodePassword = decryptField(
			"container:"+cfg.Containers[i].Name+".access_code_password", cfg.Containers[i].AccessCodePassword)
	}
	for i := range cfg.Nodes {
		cfg.Nodes[i].Token = decryptField("node:"+cfg.Nodes[i].Name+".token", cfg.Nodes[i].Token)
		cfg.Nodes[i].InstallKey = decryptField("node:"+cfg.Nodes[i].Name+".install_key", cfg.Nodes[i].InstallKey)
	}
	cfg.TurnstileSecretKey = decryptField("turnstile.secret_key", cfg.TurnstileSecretKey)
	cfg.AgentPairingKey = decryptField("agent.pairing_key", cfg.AgentPairingKey)
	cfg.UpdateSource.Token = decryptField("update_source.token", cfg.UpdateSource.Token)
	cfg.SMTPSettings.Password = decryptField("smtp.password", cfg.SMTPSettings.Password)
	cfg.JWTSecret = decryptField("jwt_secret", cfg.JWTSecret)
	for i := range cfg.Webhooks {
		cfg.Webhooks[i].Secret = decryptField("webhook:"+cfg.Webhooks[i].ID+".secret", cfg.Webhooks[i].Secret)
	}
	for i := range cfg.SubUsers {
		cfg.SubUsers[i].Password = decryptField("sub_user:"+cfg.SubUsers[i].Username+".password", cfg.SubUsers[i].Password)
		cfg.SubUsers[i].AccessCode = decryptField("sub_user:"+cfg.SubUsers[i].Username+".access_code", cfg.SubUsers[i].AccessCode)
	}
	for i := range cfg.Tasks {
		cfg.Tasks[i].Config = transformTaskConfigSecret(cfg.Tasks[i].Config, func(v string) string {
			return decryptField("task:"+cfg.Tasks[i].ID+".ssh_password", v)
		})
	}
	return failed
}
