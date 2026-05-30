package history

import (
	"regexp"

	"github.com/prathamesh/ghostline/internal/secret"
)

// Secrets must never be written to disk. We take two passes:
//   1. denylisted() drops a whole command if it's the kind that almost always
//      carries a credential (exporting a secret, an Authorization header, etc.).
//   2. redact() masks inline secret values for commands we do keep.
// denylist runs first; if it fires the command is not stored at all.

var denyPatterns = []*regexp.Regexp{
	// export FOO_TOKEN=..., SECRET_KEY=..., DB_PASSWORD=...
	regexp.MustCompile(`(?i)\b\w*(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL)\w*\s*=`),
	// curl -H "Authorization: ..." / --header Authorization
	regexp.MustCompile(`(?i)authorization\s*:`),
	// AWS-style secret references
	regexp.MustCompile(`(?i)aws_secret`),
	// raw private key material pasted into a command
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	// URL with embedded credentials: postgres://user:pass@host, mongodb://user:pass@host,
	// https://key@sentry.io, etc. Matches any ://something@host pattern.
	regexp.MustCompile(`(?i)://[^@\s]+@[^\s]`),
}

func denylisted(cmd string) bool {
	// Shared value-based detector first — catches provider key prefixes
	// (gsk_…, sk-ant-…, ghp_…) that the name-based patterns below miss.
	if secret.Contains(cmd) {
		return true
	}
	for _, re := range denyPatterns {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

var redactPatterns = []*regexp.Regexp{
	// --password value / --password=value / --token=value
	regexp.MustCompile(`(?i)(--?(?:password|passwd|token|secret|api[-_]?key)[ =])\S+`),
	// bearer tokens
	regexp.MustCompile(`(?i)(bearer\s+)\S+`),
}

// portMapping matches Docker-style port specs (-p8080:80, -p3000:3000/tcp).
// These must NOT be redacted even though they contain letters (/tcp, /udp).
var portMapping = regexp.MustCompile(`^\d+(?::\d+)?(?:/(?:tcp|udp|sctp))?$`)

// mysqlPFlag matches -p<value> (no space) style password flags.
var mysqlPFlag = regexp.MustCompile(`(\s-p)(\S+)`)

func redact(cmd string) string {
	out := cmd
	for _, re := range redactPatterns {
		out = re.ReplaceAllString(out, "${1}***")
	}
	// mysql/psql style -p<password> (no space). Require a letter in the value so
	// numeric port mappings like `docker -p8080:80` are left intact; also exclude
	// Docker port specs with protocol suffixes like `3000:3000/tcp`.
	out = mysqlPFlag.ReplaceAllStringFunc(out, func(match string) string {
		m := mysqlPFlag.FindStringSubmatch(match)
		if len(m) < 3 {
			return match
		}
		val := m[2]
		hasLetter := false
		for _, r := range val {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				hasLetter = true
				break
			}
		}
		if !hasLetter || portMapping.MatchString(val) {
			return match
		}
		return m[1] + "***"
	})
	return out
}

// clean applies the full policy: returns the storable form of cmd, and false if
// the command should be dropped entirely.
func clean(cmd string) (string, bool) {
	if denylisted(cmd) {
		return "", false
	}
	return redact(cmd), true
}

// Clean exposes the redaction policy to other packages (e.g. fixcache, which
// must apply the same secret rules). It returns the storable form of cmd and
// false if the command should be dropped entirely.
func Clean(cmd string) (string, bool) {
	return clean(cmd)
}
