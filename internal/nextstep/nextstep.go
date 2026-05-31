// Package nextstep predicts the single most likely NEXT command in the user's
// workflow given what they just ran — including a command they have not run here
// before (e.g. `terraform plan` → `terraform apply`, `git clone X` → `cd X &&
// npm install`). That forward-reasoning is the part only a model can do; frecency
// (history replay) can only suggest steps already taken.
//
// Predictions are gated to workflow-bearing moments, cached so a repeated
// prediction replays instantly and offline (see cache.go), and never auto-run —
// the shell renders the prediction as grey ghost text the user accepts with a
// keystroke.
package nextstep

import (
	"context"
	"strings"
	"time"

	"github.com/prathamesh/ghostline/internal/session"
)

type generator interface {
	Generate(ctx context.Context, prompt string, maxTokens int) (string, error)
}

type Predictor struct {
	gen       generator
	timeoutMS int
}

// Result is one predicted next step. Destructive marks commands that are
// irreversible or hard to undo, so the shell can render them with a warning and
// the user thinks twice before accepting.
type Result struct {
	Next        string
	Destructive bool
}

func New(gen generator, timeoutMS int) *Predictor {
	return &Predictor{gen: gen, timeoutMS: timeoutMS}
}

// workflowCommands are first tokens that usually have a meaningful, predictable
// next step. Prediction is gated to these (or to any failure) so trivial commands
// (ls, cd, cat) never trigger a prediction.
var workflowCommands = map[string]bool{
	"terraform": true, "tf": true, "tofu": true,
	"git": true, "gh": true,
	"docker": true, "docker-compose": true, "podman": true, "compose": true,
	"npm": true, "yarn": true, "pnpm": true, "bun": true, "npx": true,
	"make": true, "cargo": true, "go": true, "rake": true,
	"kubectl": true, "k": true, "helm": true, "kustomize": true,
	"vagrant": true, "ansible": true, "ansible-playbook": true,
	"pip": true, "pip3": true, "poetry": true, "pytest": true, "tox": true,
	"gradle": true, "mvn": true, "bundle": true, "rails": true,
	"aws": true, "gcloud": true, "az": true, "flyctl": true, "fly": true,
	"pulumi": true, "serverless": true, "sls": true,
}

// ShouldPredict gates prediction to meaningful moments: any failure (predict the
// rollback/fix), or a successful workflow command (predict the next step).
func ShouldPredict(lastCmd string, exitCode int) bool {
	lastCmd = strings.TrimSpace(lastCmd)
	if lastCmd == "" || strings.HasPrefix(lastCmd, "ghostline") {
		return false
	}
	if exitCode != 0 {
		return true
	}
	return workflowCommands[firstToken(lastCmd)]
}

// Predict asks the model for the next workflow step. Returns nil when there is no
// clear next step (model says NONE).
func (p *Predictor) Predict(lastCmd string, exitCode int, ctx *session.Context) (*Result, error) {
	prompt := buildPrompt(lastCmd, exitCode, ctx)

	tctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.timeoutMS)*time.Millisecond)
	defer cancel()

	resp, err := p.gen.Generate(tctx, prompt, 64)
	if err != nil {
		return nil, err
	}
	return parseResult(resp), nil
}

func parseResult(resp string) *Result {
	resp = strings.TrimSpace(resp)
	if resp == "" || strings.EqualFold(resp, "NONE") {
		return nil
	}

	r := &Result{}
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "NEXT:"); ok {
			r.Next = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "RISK:"); ok {
			r.Destructive = strings.Contains(strings.ToLower(after), "destruct")
		}
	}

	r.Next = strings.TrimSpace(r.Next)
	if r.Next == "" || strings.EqualFold(r.Next, "NONE") {
		return nil
	}
	// Defense in depth: flag destructive by pattern even if the model said safe.
	if isDestructive(r.Next) {
		r.Destructive = true
	}
	return r
}

// destructivePatterns are substrings (space-padded) marking a command as
// irreversible / hard to undo. Used to flag predictions — flagging is safe to
// over-trigger (it only adds a warning), so the list errs toward caution.
var destructivePatterns = []string{
	" rm ", " rm -", " rmdir ", " unlink ",
	" terraform apply", " terraform destroy", " tf apply", " tf destroy", " tofu apply", " tofu destroy",
	" pulumi up", " pulumi destroy",
	" push --force", " push -f", " push --force-with-lease",
	" reset --hard", " clean -f", " clean -d", " branch -d", " branch -dd",
	" kubectl delete", " helm delete", " helm uninstall",
	" drop ", " delete ", " truncate ", " deploy", " release",
	" dd ", " mkfs", " > /dev/", " docker rm", " docker rmi", " system prune", " volume rm",
}

func isDestructive(cmd string) bool {
	l := " " + strings.ToLower(strings.TrimSpace(cmd)) + " "
	for _, p := range destructivePatterns {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// firstToken returns the command's tool name, skipping leading VAR=value
// environment assignments (e.g. "FOO=bar git push" → "git").
func firstToken(cmd string) string {
	for _, f := range strings.Fields(cmd) {
		if !strings.HasPrefix(f, "-") && strings.Contains(f, "=") {
			continue
		}
		return f
	}
	return ""
}
