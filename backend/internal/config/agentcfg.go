package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// AgentConfig 是被控节点保存的注册信息（数据目录下的 agent.json）。
// agent 运行时读取它向主控上报心跳；被控面板也读取它展示接入状态。
type AgentConfig struct {
	Controller string `json:"controller"`
	NodeID     string `json:"node_id"`
	Token      string `json:"token"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	// AllowInsecureHTTP 允许与主控通过明文 http 通信（仅当主控不提供 TLS 时显式开启）。
	AllowInsecureHTTP bool `json:"allow_insecure_http,omitempty"`
}

var agentCfgMu sync.Mutex

// AgentConfigPath 返回 agent.json 的完整路径（数据目录下）。
func AgentConfigPath() string {
	dir := ""
	if AppConfig != nil {
		AppConfigMu.RLock()
		dir = AppConfig.DataDir
		AppConfigMu.RUnlock()
	}
	if dir == "" {
		dir = getDataDir()
	}
	return filepath.Join(dir, "agent.json")
}

// LoadAgentConfig 读取 agent.json；未注册或文件损坏时返回 nil。
func LoadAgentConfig() *AgentConfig {
	agentCfgMu.Lock()
	defer agentCfgMu.Unlock()
	data, err := os.ReadFile(AgentConfigPath())
	if err != nil {
		return nil
	}
	var ac AgentConfig
	if err := json.Unmarshal(data, &ac); err != nil || ac.NodeID == "" || ac.Token == "" {
		return nil
	}
	return &ac
}

// SaveAgentConfig 写入 agent.json（目录 0700、文件 0600，仅 root 可读——含节点 token）。
func SaveAgentConfig(ac *AgentConfig) error {
	agentCfgMu.Lock()
	defer agentCfgMu.Unlock()
	data, err := json.MarshalIndent(ac, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(AgentConfigPath()), 0700); err != nil {
		return err
	}
	return os.WriteFile(AgentConfigPath(), data, 0600)
}
