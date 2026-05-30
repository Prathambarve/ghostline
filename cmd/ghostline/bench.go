package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/prathamesh/ghostline/internal/completion"
	"github.com/prathamesh/ghostline/internal/config"
	"github.com/prathamesh/ghostline/internal/llm"
	"github.com/prathamesh/ghostline/internal/recovery"
	"github.com/spf13/cobra"
)

type benchResult struct {
	Backend string  `json:"backend"`
	Model   string  `json:"model"`
	Count   int     `json:"count"`
	Errors  int     `json:"errors"`
	P50ms   float64 `json:"p50_ms"`
	P95ms   float64 `json:"p95_ms"`
	MinMs   float64 `json:"min_ms"`
	MaxMs   float64 `json:"max_ms"`
	Skipped string  `json:"skipped,omitempty"`
}

func benchCmd() *cobra.Command {
	var backend, mode string
	var n int
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Benchmark inference latency across backends",
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := config.Load()
			if err != nil {
				return err
			}

			backends := []string{backend}
			if backend == "all" || backend == "" {
				backends = []string{"anthropic", "openai", "groq"}
			}

			prompt, maxTokens := completion.SamplePrompt()
			if mode == "recover" {
				prompt, maxTokens = recovery.SamplePrompt()
			}

			var results []benchResult
			for _, b := range backends {
				results = append(results, runBench(base, b, prompt, maxTokens, n))
			}

			if asJSON {
				out, _ := json.MarshalIndent(results, "", "  ")
				fmt.Println(string(out))
				return nil
			}
			printBenchTable(results, mode, n)
			return nil
		},
	}
	cmd.Flags().StringVar(&backend, "backend", "all", "backend to test: anthropic|openai|groq|all")
	cmd.Flags().StringVar(&mode, "mode", "complete", "prompt shape to test: complete|recover")
	cmd.Flags().IntVar(&n, "n", 10, "number of iterations per backend")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	return cmd
}

// runBench clones the config with a single backend selected and times n
// Generate calls. A backend whose Ping fails (e.g. no API key) is skipped
// rather than aborting the whole run.
func runBench(base *config.Config, backend, prompt string, maxTokens, n int) benchResult {
	cfg := *base
	cfg.Backend = backend

	model := backendModel(&cfg)
	res := benchResult{Backend: backend, Model: model, Count: n}

	gen := llm.New(&cfg)
	if err := gen.Ping(context.Background()); err != nil {
		res.Skipped = err.Error()
		return res
	}

	var durs []float64
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		start := time.Now()
		_, err := gen.Generate(ctx, prompt, maxTokens)
		elapsed := time.Since(start)
		cancel()
		if err != nil {
			res.Errors++
			continue
		}
		durs = append(durs, float64(elapsed.Microseconds())/1000.0)
	}

	if len(durs) > 0 {
		sort.Float64s(durs)
		res.P50ms = percentile(durs, 50)
		res.P95ms = percentile(durs, 95)
		res.MinMs = durs[0]
		res.MaxMs = durs[len(durs)-1]
	}
	return res
}

func backendModel(cfg *config.Config) string {
	switch cfg.Backend {
	case "openai":
		return cfg.OpenAIModel
	case "groq":
		return cfg.GroqModel
	default:
		return cfg.AnthropicModel
	}
}

// percentile returns the p-th percentile of a pre-sorted slice (nearest-rank).
func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p * len(sorted)) / 100
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func printBenchTable(results []benchResult, mode string, n int) {
	fmt.Printf("ghostline bench — mode=%s, n=%d\n", mode, n)
	fmt.Println("─────────────────────────────────────────────────────────────────────")
	fmt.Printf("%-10s %-26s %6s %6s %6s %6s %6s\n", "BACKEND", "MODEL", "p50", "p95", "min", "max", "err")
	for _, r := range results {
		if r.Skipped != "" {
			fmt.Printf("%-10s %-26s  skipped: %s\n", r.Backend, truncate(r.Model, 26), r.Skipped)
			continue
		}
		fmt.Printf("%-10s %-26s %5.0fms %5.0fms %5.0fms %5.0fms %6d\n",
			r.Backend, truncate(r.Model, 26), r.P50ms, r.P95ms, r.MinMs, r.MaxMs, r.Errors)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
