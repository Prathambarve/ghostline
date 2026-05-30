package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Model               string `yaml:"model"`
	OllamaHost          string `yaml:"ollama_host"`
	Backend             string `yaml:"backend"` // "ollama" or "anthropic"
	AnthropicAPIKey     string `yaml:"anthropic_api_key"`
	AnthropicModel      string `yaml:"anthropic_model"`
	CompletionTimeoutMS int    `yaml:"completion_timeout_ms"`
	RecoveryTimeoutMS   int    `yaml:"recovery_timeout_ms"`
	MaxContextCommands  int    `yaml:"max_context_commands"`
}

func Default() *Config {
	return &Config{
		Model:               "qwen2.5-coder:3b",
		OllamaHost:          "http://localhost:11434",
		Backend:             "anthropic", // current default; set "ollama" to run fully local
		AnthropicModel:      "claude-haiku-4-5-20251001",
		CompletionTimeoutMS: 5000,
		RecoveryTimeoutMS:   10000,
		MaxContextCommands:  15,
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

func filePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}
