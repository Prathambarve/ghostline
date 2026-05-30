// Package secret detects credential-shaped material in a command line. It backs
// two defenses:
//   - the prompt guard, which warns before the user runs a command that would
//     echo or commit a key;
//   - the history denylist, which drops such commands so they never hit disk.
//
// Detection is deliberately VALUE-based (known token prefixes and explicit
// secret-assignment shapes), not entropy-based: a 40-char git SHA or a UUID is
// high-entropy but not a secret, and false warnings train users to ignore the
// guard. We accept the occasional miss in exchange for near-zero false alarms.
package secret

import "regexp"

type pattern struct {
	re     *regexp.Regexp
	reason string
}

// patterns are tried in order; the first match wins, so more specific provider
// prefixes are listed before broader ones (sk-ant- before sk-).
var patterns = []pattern{
	{regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`), "looks like an Anthropic API key"},
	{regexp.MustCompile(`\bgsk_[A-Za-z0-9]{20,}`), "looks like a Groq API key"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`), "looks like an OpenAI API key"},
	{regexp.MustCompile(`\bgh[posru]_[A-Za-z0-9]{30,}`), "looks like a GitHub token"},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}`), "looks like a GitLab token"},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`), "looks like a Slack token"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "looks like an AWS access key ID"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`), "looks like a Google API key"},
	{regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`), "looks like an npm token"},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), "contains a private key"},
	{regexp.MustCompile(`(?i)\b\w*(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL)\w*\s*=\s*\S`), "assigns a secret-looking value"},
	{regexp.MustCompile(`(?i)\bauthorization\s*:\s*\S`), "includes an Authorization header"},
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-]{12,}`), "includes a bearer token"},
	{regexp.MustCompile(`(?i)\baws_secret`), "references an AWS secret"},
	// URL with embedded credentials: scheme://user:pass@host or scheme://token@host
	{regexp.MustCompile(`(?i)://[^@\s/]+@[^\s]`), "embeds a credential in a URL"},
}

// Match returns a short human-readable reason and true if s appears to carry a
// secret that should not be run, committed, or echoed. The reason is safe to
// display — it never includes the matched value itself.
func Match(s string) (string, bool) {
	for _, p := range patterns {
		if p.re.MatchString(s) {
			return p.reason, true
		}
	}
	return "", false
}

// Contains reports whether s carries a secret, discarding the reason.
func Contains(s string) bool {
	_, found := Match(s)
	return found
}
