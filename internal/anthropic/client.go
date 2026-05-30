package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

const (
	apiURL       = "https://api.anthropic.com/v1/messages"
	defaultModel = "claude-haiku-4-5-20251001"
	apiVersion   = "2023-06-01"
)

type Client struct {
	apiKey  string
	model   string
	httpCli *http.Client
}

func New(apiKey, model string) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if model == "" {
		model = defaultModel
	}
	return &Client{
		apiKey:  apiKey,
		model:   model,
		httpCli: &http.Client{},
	}
}

func (c *Client) Generate(ctx context.Context, prompt string, maxTokens int) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      c.model,
		"max_tokens": maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic unreachable: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode error: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("anthropic: %s", result.Error.Message)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return result.Content[0].Text, nil
}

func (c *Client) Ping(ctx context.Context) error {
	if c.apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY not set — export it in ~/.zshrc")
	}
	return nil
}
