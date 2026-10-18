package cli

// config.go — ncoda 全局配置（XDG）。开放面：只依赖标准库。
//
// 文件:$XDG_CONFIG_HOME/ncoda/config.json（缺省 ~/.config/ncoda/config.json）
//
//	{
//	  "llm": {
//	    "base_url": "https://api.openai.com/v1",
//	    "key": "sk-...",
//	    "model": "gpt-5.6-sol"
//	  }
//	}
//
// 解析优先级（高 → 低）:
//  1. 命令行 flag（-openai-base-url / -openai-key / -openai-model）
//  2. 环境变量（OPENAI_BASE_URL / OPENAI_API_KEY / OPENAI_MODEL）
//  3. 配置文件
//  4. 内置默认（https://api.openai.com/v1）
//
// key 以 0600 权限落盘;文件不存在时按空配置处理（echo 工厂无需任何配置）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the on-disk ncoda configuration.
type Config struct {
	LLM LLMConfig `json:"llm"`
}

// LLMConfig holds the OpenAI-compatible model backend settings.
type LLMConfig struct {
	BaseURL string `json:"base_url,omitempty"`
	Key     string `json:"key,omitempty"`
	Model   string `json:"model,omitempty"`
}

// DefaultBaseURL is the built-in fallback (level 4).
const DefaultBaseURL = "https://api.openai.com/v1"

// ConfigPath returns the XDG config file path.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadConfig reads the config file; a missing file yields an empty Config
// (not an error). Malformed JSON is an error (fail loud, don't silently
// fall back to defaults — a typo in the config should not go unnoticed).
func LoadConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %v", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("config %s: %v", path, err)
	}
	return cfg, nil
}

// SaveConfig writes the config with 0600 permissions (key material).
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

// ResolveLLM merges flag → env → config → default (in that order) into the
// effective backend settings.
func ResolveLLM(flagBase, flagKey, flagModel string, cfg Config) (base, key, model string) {
	base = DefaultBaseURL
	if cfg.LLM.BaseURL != "" {
		base = cfg.LLM.BaseURL
	}
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
		base = v
	}
	if flagBase != "" {
		base = flagBase
	}

	key = cfg.LLM.Key
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		key = v
	}
	if flagKey != "" {
		key = flagKey
	}

	model = cfg.LLM.Model
	if v := os.Getenv("OPENAI_MODEL"); v != "" {
		model = v
	}
	if flagModel != "" {
		model = flagModel
	}
	return base, key, model
}

// ConfigDir returns the XDG config directory for ncoda (creates nothing).
func ConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "ncoda"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %v", err)
	}
	return filepath.Join(home, ".config", "ncoda"), nil
}
