// Package llm abstracts inference behind a single interface so the rest of the
// codebase doesn't care whether requests go to Anthropic, OpenAI, Groq, or the
// ghostline managed proxy. Select the backend via config.Backend.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/prathamesh/ghostline/internal/anthropic"
	"github.com/prathamesh/ghostline/internal/config"
	"github.com/prathamesh/ghostline/internal/openai"
)

// Generator is the single inference primitive every backend implements.
type Generator interface {
	Generate(ctx context.Context, prompt string, maxTokens int) (string, error)
	Ping(ctx context.Context) error
}

const groqURL = "https://api.groq.com/openai/v1/chat/completions"

// New returns the inference backend for cfg.
//
// Priority:
//  1. Explicit backend + key configured by the user → use it directly.
//  2. No key configured → fall back to the managed proxy (cfg.ManagedURL).
//     Your API key lives on the proxy; users never see it.
func New(cfg *config.Config) Generator {
	// If the user has set a key for their chosen backend, use it directly.
	if hasDirectKey(cfg) {
		return newDirect(cfg)
	}

	// No key → route through the managed proxy (your key, hidden server-side).
	if cfg.ManagedURL != "" {
		proxyURL := cfg.ManagedURL + "/v1/chat/completions"
		return &managedClient{
			inner:      openai.NewCompatible("ghostline", "", cfg.GroqModel, proxyURL, ""),
			healthURL:  cfg.ManagedURL + "/health",
			managedURL: cfg.ManagedURL,
		}
	}

	// Last resort: try the configured backend anyway (will fail with a clear error).
	return newDirect(cfg)
}

// hasDirectKey reports whether the user has configured a key for the backend
// they selected (either in config or via environment variable).
func hasDirectKey(cfg *config.Config) bool {
	switch cfg.Backend {
	case "openai":
		return cfg.OpenAIAPIKey != "" || os.Getenv("OPENAI_API_KEY") != ""
	case "groq":
		return cfg.GroqAPIKey != "" || os.Getenv("GROQ_API_KEY") != ""
	case "managed":
		return false // always use proxy
	default: // anthropic
		return cfg.AnthropicAPIKey != "" || os.Getenv("ANTHROPIC_API_KEY") != ""
	}
}

// newDirect builds a backend client that calls the API directly with the user's key.
func newDirect(cfg *config.Config) Generator {
	switch cfg.Backend {
	case "openai":
		return openai.New(cfg.OpenAIAPIKey, cfg.OpenAIModel)
	case "groq":
		return openai.NewCompatible("groq", cfg.GroqAPIKey, cfg.GroqModel, groqURL, "GROQ_API_KEY")
	default: // anthropic
		return anthropic.New(cfg.AnthropicAPIKey, cfg.AnthropicModel)
	}
}

// managedClient wraps an openai-compatible client pointed at the proxy and
// overrides Ping to do a real health-check instead of checking for a local key.
type managedClient struct {
	inner      Generator
	healthURL  string
	managedURL string
}

func (m *managedClient) Generate(ctx context.Context, prompt string, maxTokens int) (string, error) {
	return m.inner.Generate(ctx, prompt, maxTokens)
}

func (m *managedClient) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.healthURL, nil)
	if err != nil {
		return fmt.Errorf("managed proxy unreachable: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("managed proxy unreachable (%s): %w", m.managedURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("managed proxy returned %d", resp.StatusCode)
	}
	return nil
}
