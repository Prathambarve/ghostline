// Package openai is an HTTP client for the OpenAI Chat Completions API and any
// OpenAI-compatible endpoint (e.g. Groq). Use New for OpenAI; NewCompatible for
// other providers that speak the same /chat/completions protocol.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

const (
	apiURL       = "https://api.openai.com/v1/chat/completions"
	defaultModel = "gpt-4o-mini"
)

type Client struct {
	apiKey  string
	model   string
	baseURL string
	name    string // provider label, for error messages
	keyEnv  string // env var the key falls back to, for the Ping hint
	httpCli *http.Client
}

// New builds a client for the OpenAI API, falling back to the OPENAI_API_KEY
// env var when apiKey is empty.
func New(apiKey, model string) *Client {
	if model == "" {
		model = defaultModel
	}
	return NewCompatible("openai", apiKey, model, apiURL, "OPENAI_API_KEY")
}

// NewCompatible builds a client for any OpenAI-compatible endpoint. name is the
// provider label used in errors; keyEnv is the env var the key falls back to
// when apiKey is empty.
func NewCompatible(name, apiKey, model, baseURL, keyEnv string) *Client {
	if apiKey == "" {
		apiKey = os.Getenv(keyEnv)
	}
	return &Client{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		name:    name,
		keyEnv:  keyEnv,
		httpCli: &http.Client{},
	}
}

func (c *Client) Generate(ctx context.Context, prompt string, maxTokens int) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":       c.model,
		"max_tokens":  maxTokens,
		"temperature": 0.1,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s unreachable: %w", c.name, err)
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("%s: %s", c.name, result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return result.Choices[0].Message.Content, nil
}

func (c *Client) Ping(ctx context.Context) error {
	if c.apiKey == "" {
		return fmt.Errorf("%s not set — export it in ~/.zshrc", c.keyEnv)
	}
	return nil
}
