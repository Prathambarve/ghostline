package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Backend             string `yaml:"backend"` // "anthropic", "openai", or "groq"
	AnthropicAPIKey     string `yaml:"anthropic_api_key"`
	AnthropicModel      string `yaml:"anthropic_model"`
	OpenAIAPIKey        string `yaml:"openai_api_key"`
	OpenAIModel         string `yaml:"openai_model"`
	GroqAPIKey          string `yaml:"groq_api_key"`
	GroqModel           string `yaml:"groq_model"`
	CompletionTimeoutMS int    `yaml:"completion_timeout_ms"`
	RecoveryTimeoutMS   int    `yaml:"recovery_timeout_ms"`
	MaxContextCommands  int    `yaml:"max_context_commands"`
	// ManagedURL is the ghostline proxy endpoint. When set and no direct API key
	// is configured for the selected backend, requests are routed here instead.
	// Your key lives on the proxy server — users never see it.
	ManagedURL          string `yaml:"managed_url"`
	SetupComplete       bool   `yaml:"setup_complete"`
	HistoryEnabled      bool   `yaml:"history_enabled"`
	SendCWD             bool   `yaml:"send_cwd"`
	SendGitRemote       bool   `yaml:"send_git_remote"`
	SendGitStatus       bool   `yaml:"send_git_status"`
	SendDirFiles        bool   `yaml:"send_dir_files"`
	SendRecentCommands  bool   `yaml:"send_recent_commands"`
	SendStderr          bool   `yaml:"send_stderr"`
}

func Default() *Config {
	return &Config{
		Backend:             "anthropic", // default; override with "openai", "groq", or "managed"
		ManagedURL:          "https://api.ghostline.dev",
		AnthropicModel:      "claude-haiku-4-5-20251001",
		OpenAIModel:         "gpt-4o-mini",
		GroqModel:           "llama-3.3-70b-versatile",
		CompletionTimeoutMS: 5000,
		RecoveryTimeoutMS:   10000,
		MaxContextCommands:  15,
		HistoryEnabled:      true,
		SendCWD:             true,
		SendGitRemote:       true,
		SendGitStatus:       true,
		SendDirFiles:        true,
		SendRecentCommands:  true,
		SendStderr:          true,
	}
}

func Load() (*Config, error) {
	cfg := Default()

	path, err := filePath()
	if err != nil {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	return cfg, yaml.Unmarshal(data, cfg)
}

// Save writes cfg to ~/.ghostline/config.yaml, creating the directory if needed.
func Save(cfg *Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path, err := filePath()
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ghostline"), nil
}

func SocketPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ghostline.sock"), nil
}

func PIDPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ghostline.pid"), nil
}

// HistoryPath is the JSONL file of persisted, redacted command history used for
// cross-session suggestions.
func HistoryPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.jsonl"), nil
}

// FixCachePath is the JSONL file of user-accepted error recoveries, used to
// replay a known fix for an identical failure instantly and offline.
func FixCachePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "recoveries.jsonl"), nil
}

// WorkflowsPath is the YAML file of user-authored saved commands (workflows)
// surfaced in the command palette.
func WorkflowsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workflows.yaml"), nil
}

func filePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}
