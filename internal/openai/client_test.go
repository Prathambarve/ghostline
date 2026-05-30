package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withServer points the client at a stub server by overriding the package URL.
func TestGenerateSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("missing/incorrect auth header: %q", got)
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"eckout"}}]}`))
	}))
	defer srv.Close()

	c := New("test-key", "gpt-4o-mini")
	c.baseURL = srv.URL

	out, err := c.Generate(context.Background(), "git ch", 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "eckout" {
		t.Errorf("Generate() = %q, want %q", out, "eckout")
	}
}

func TestGenerateAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	c := New("bad", "gpt-4o-mini")
	c.baseURL = srv.URL

	_, err := c.Generate(context.Background(), "x", 80)
	if err == nil {
		t.Fatal("expected error from API error response")
	}
}

func TestPingRequiresKey(t *testing.T) {
	if err := New("", "").Ping(context.Background()); err == nil {
		t.Error("Ping should fail with no key")
	}
	if err := New("k", "").Ping(context.Background()); err != nil {
		t.Errorf("Ping should pass with a key, got %v", err)
	}
}

func TestNewCompatiblePingHintNamesItsEnvVar(t *testing.T) {
	// A Groq-style client should reference GROQ_API_KEY (not OPENAI_API_KEY) in
	// its readiness error, so the user knows which key to set.
	t.Setenv("GROQ_API_KEY", "")
	err := NewCompatible("groq", "", "llama-3.3-70b-versatile", "https://api.groq.com/openai/v1/chat/completions", "GROQ_API_KEY").Ping(context.Background())
	if err == nil {
		t.Fatal("expected error with no key")
	}
	if got := err.Error(); !strings.Contains(got, "GROQ_API_KEY") {
		t.Errorf("Ping hint = %q, want it to mention GROQ_API_KEY", got)
	}
}

func TestNewCompatibleFallsBackToEnv(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "from-env")
	c := NewCompatible("groq", "", "m", "https://x", "GROQ_API_KEY")
	if c.apiKey != "from-env" {
		t.Errorf("expected key resolved from env, got %q", c.apiKey)
	}
}
