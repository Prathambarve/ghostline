// Package llm abstracts inference behind a single interface so the rest of the
// codebase doesn't care whether requests go to the Claude API or to a local
// Ollama instance. Select the implementation via config.Backend.
package llm

import (
	"context"

	"github.com/prathamesh/ghostline/internal/anthropic"
	"github.com/prathamesh/ghostline/internal/config"
	"github.com/prathamesh/ghostline/internal/ollama"
)

// Generator is the single inference primitive every backend implements.
type Generator interface {
	// Generate returns the model's completion for prompt, capped at maxTokens.
	Generate(ctx context.Context, prompt string, maxTokens int) (string, error)
	// Ping reports whether the backend is reachable/configured.
	Ping(ctx context.Context) error
}

// New returns the inference backend selected by cfg.Backend. It defaults to the
// Claude API ("anthropic"); set backend: ollama in config.yaml to run locally.
func New(cfg *config.Config) Generator {
	switch cfg.Backend {
	case "ollama":
		return ollama.New(cfg.OllamaHost, cfg.Model)
	default: // "anthropic" or unset
		return anthropic.New(cfg.AnthropicAPIKey, cfg.AnthropicModel)
	}
}
