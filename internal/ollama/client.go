package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Client struct {
	host    string
	model   string
	httpCli *http.Client
}

type generateRequest struct {
	Model   string          `json:"model"`
	Prompt  string          `json:"prompt"`
	Stream  bool            `json:"stream"`
	Options generateOptions `json:"options"`
}

type generateOptions struct {
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"num_predict"`
}

type generateResponse struct {
	Response string `json:"response"`
}

type tagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

func New(host, model string) *Client {
	return &Client{
		host:    host,
		model:   model,
		httpCli: &http.Client{},
	}
}

func (c *Client) Generate(ctx context.Context, prompt string, maxTokens int) (string, error) {
	body, _ := json.Marshal(generateRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
		Options: generateOptions{
			Temperature: 0.1,
			NumPredict:  maxTokens,
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", c.host+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama not reachable at %s — run: ollama serve", c.host)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode ollama response: %w", err)
	}

	return result.Response, nil
}

func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.host+"/api/tags", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not running at %s\n  Fix: brew install ollama && brew services start ollama", c.host)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) CheckModel(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.host+"/api/tags", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var tags tagsResponse
	json.NewDecoder(resp.Body).Decode(&tags)

	for _, m := range tags.Models {
		if strings.HasPrefix(m.Name, c.model) || m.Name == c.model {
			return nil
		}
	}
	return fmt.Errorf("model %s not found locally", c.model)
}
