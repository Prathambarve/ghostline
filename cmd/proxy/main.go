// ghostline-proxy — managed inference proxy.
// Deploy this on a server with your API key set as an env var.
// Users' ghostline binaries talk to this; your key never leaves the server.
//
// Environment variables:
//   GROQ_API_KEY       — Groq key (default target)
//   ANTHROPIC_API_KEY  — Anthropic key (if TARGET_BACKEND=anthropic)
//   TARGET_BACKEND     — "groq" (default) or "anthropic"
//   PORT               — listen port (default 8080)
//   RATE_LIMIT         — max requests per IP per hour (default 60)
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── Rate limiter (token bucket per IP) ───────────────────────────────────────

type bucket struct {
	tokens   float64
	lastFill time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	maxRate  float64 // requests per hour
	capacity float64
}

func newRateLimiter(perHour int) *rateLimiter {
	rl := &rateLimiter{
		buckets:  make(map[string]*bucket),
		maxRate:  float64(perHour) / 3600, // tokens per second
		capacity: float64(perHour),
	}
	// Evict stale buckets every 10 minutes.
	go func() {
		for range time.Tick(10 * time.Minute) {
			rl.mu.Lock()
			cutoff := time.Now().Add(-2 * time.Hour)
			for ip, b := range rl.buckets {
				if b.lastFill.Before(cutoff) {
					delete(rl.buckets, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[ip]
	if !ok {
		b = &bucket{tokens: rl.capacity, lastFill: time.Now()}
		rl.buckets[ip] = b
	}

	now := time.Now()
	elapsed := now.Sub(b.lastFill).Seconds()
	b.tokens = min(rl.capacity, b.tokens+elapsed*rl.maxRate)
	b.lastFill = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// ── Target backend config ─────────────────────────────────────────────────────

type backend struct {
	url    string
	apiKey string
	name   string
}

func loadBackend() backend {
	target := strings.ToLower(os.Getenv("TARGET_BACKEND"))
	if target == "" {
		target = "groq"
	}
	switch target {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			log.Fatal("ANTHROPIC_API_KEY not set")
		}
		// Anthropic uses a different protocol — wrap it as OpenAI-compatible via a
		// local adapter or use the native endpoint. For simplicity proxy to Groq by
		// default; switch to anthropic only if explicitly requested.
		log.Printf("Target: anthropic (key: %s...)", key[:8])
		return backend{
			url:    "https://api.anthropic.com/v1/messages",
			apiKey: key,
			name:   "anthropic",
		}
	default: // groq
		key := os.Getenv("GROQ_API_KEY")
		if key == "" {
			log.Fatal("GROQ_API_KEY not set")
		}
		log.Printf("Target: groq (key: %s...)", key[:8])
		return backend{
			url:    "https://api.groq.com/openai/v1/chat/completions",
			apiKey: key,
			name:   "groq",
		}
	}
}

// ── Proxy handler ─────────────────────────────────────────────────────────────

type proxy struct {
	backend backend
	rl      *rateLimiter
	stats   struct {
		mu       sync.Mutex
		requests int64
		denied   int64
	}
}

func clientIP(r *http.Request) string {
	// Respect X-Forwarded-For from load balancers (Fly.io, Render, etc.)
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	// Strip port from RemoteAddr.
	ip := r.RemoteAddr
	if i := strings.LastIndex(ip, ":"); i >= 0 {
		ip = ip[:i]
	}
	return ip
}

func (p *proxy) handleCompletion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(r)

	if !p.rl.allow(ip) {
		p.stats.mu.Lock()
		p.stats.denied++
		p.stats.mu.Unlock()
		http.Error(w, `{"error":{"message":"rate limit exceeded — try again later"}}`,
			http.StatusTooManyRequests)
		return
	}

	p.stats.mu.Lock()
	p.stats.requests++
	p.stats.mu.Unlock()

	// Read and forward body as-is (OpenAI-compatible JSON).
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		p.backend.url, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Always use our key — never trust the incoming Authorization header.
	req.Header.Set("Authorization", "Bearer "+p.backend.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("upstream error: %v", err)
		http.Error(w, `{"error":{"message":"upstream unavailable"}}`,
			http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Stream the upstream response back to the client.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

	log.Printf("[%s] ip=%s status=%d", time.Now().Format("15:04:05"), ip, resp.StatusCode)
}

func (p *proxy) handleHealth(w http.ResponseWriter, _ *http.Request) {
	p.stats.mu.Lock()
	reqs := p.stats.requests
	denied := p.stats.denied
	p.stats.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"backend":  p.backend.name,
		"requests": reqs,
		"denied":   denied,
	})
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	rateLimit := 60
	if v := os.Getenv("RATE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rateLimit = n
		}
	}

	p := &proxy{
		backend: loadBackend(),
		rl:      newRateLimiter(rateLimit),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", p.handleCompletion)
	mux.HandleFunc("/health", p.handleHealth)

	addr := fmt.Sprintf(":%s", port)
	log.Printf("ghostline proxy listening on %s (rate limit: %d req/hour/ip)", addr, rateLimit)
	log.Fatal(http.ListenAndServe(addr, mux))
}
