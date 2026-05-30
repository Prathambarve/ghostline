package history

// Hard-mode edge-case tests for redact() and denylisted().
// Tests marked "BUG" expose known failures in the current implementation.
// Run: go test ./internal/history/... -run TestRedactBugs -v

import "testing"

func TestDenylistedBugs(t *testing.T) {
	tests := []struct {
		name  string
		isBug bool
		desc  string
		cmd   string
		want  bool
	}{
		// ── BUG-5: Missing database URL patterns ──────────────────────────────
		// Database connection strings carry credentials (user:password@host) but
		// none of the denylist keywords match "DATABASE_URL", "DSN", "CONN_STR",
		// "SENTRY_DSN", etc. These are stored in history in plaintext.
		{
			name:  "BUG DATABASE_URL with embedded credentials not caught",
			isBug: true,
			desc:  "heroku config:set DATABASE_URL=postgres://user:pass@host/db should be denylisted",
			cmd:   "heroku config:set DATABASE_URL=postgres://user:password@db.host/mydb",
			want:  true,
		},
		{
			name:  "BUG SENTRY_DSN with secret key not caught",
			isBug: true,
			desc:  "SENTRY_DSN contains a secret key component and should be denylisted",
			cmd:   "SENTRY_DSN=https://abc123secret@o123456.ingest.sentry.io/789 ./app",
			want:  true,
		},
		{
			name:  "BUG DATABASE_URL as env var assignment not caught",
			isBug: true,
			desc:  "export DATABASE_URL=... should be denylisted",
			cmd:   "export DATABASE_URL=postgres://root:hunter2@localhost:5432/prod",
			want:  true,
		},
		{
			name:  "BUG MONGO_URL with credentials not caught",
			isBug: true,
			desc:  "MongoDB connection string with credentials should be denylisted",
			cmd:   "MONGO_URL=mongodb://user:pass@cluster.mongodb.net/db node server.js",
			want:  true,
		},

		// ── Confirmed catches ─────────────────────────────────────────────────
		{name: "export GITHUB_TOKEN caught", cmd: "export GITHUB_TOKEN=ghp_abc", want: true},
		{name: "export API_KEY caught", cmd: "export MY_API_KEY=secret", want: true},
		{name: "export SECRET caught", cmd: "export APP_SECRET=xyz", want: true},
		{name: "DB_PASSWORD assignment caught", cmd: "DB_PASSWORD=hunter2 ./run", want: true},
		{name: "authorization header caught", cmd: `curl -H "Authorization: Bearer tok" https://x`, want: true},
		{name: "aws_secret caught", cmd: "aws_secret_access_key=AKIAFOO", want: true},
		{name: "private key pasted caught", cmd: "echo -----BEGIN RSA PRIVATE KEY----- > k", want: true},

		// ── Confirmed non-catches (legit commands) ────────────────────────────
		{name: "export PATH is fine", cmd: "export PATH=$PATH:/usr/local/bin", want: false},
		{name: "git status is fine", cmd: "git status", want: false},
		{name: "make build is fine", cmd: "make build", want: false},
		{name: "docker run with port is fine", cmd: "docker run -p 8080:80 nginx", want: false},
		{name: "psql without password in cmd is fine", cmd: "psql -U myuser mydb", want: false},
		{name: "kubectl get pods is fine", cmd: "kubectl get pods", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := denylisted(tt.cmd)
			if got != tt.want {
				if tt.isBug {
					t.Errorf("BUG CONFIRMED — denylisted(%q) = %v, want %v\n\tDesc: %s",
						tt.cmd, got, tt.want, tt.desc)
				} else {
					t.Errorf("REGRESSION — denylisted(%q) = %v, want %v", tt.cmd, got, tt.want)
				}
			}
		})
	}
}

func TestRedactBugs(t *testing.T) {
	tests := []struct {
		name  string
		isBug bool
		desc  string
		cmd   string
		want  string
	}{
		// ── BUG-4: Docker port with /tcp or /udp suffix falsely redacted ──────
		// The -p pattern requires a letter in the value to avoid redacting numeric
		// port maps like "-p8080:80". But "3000:3000/tcp" contains letters (t,c,p),
		// triggering the pattern and producing "-p*** app".
		{
			name:  "BUG docker -p with /tcp suffix falsely redacted",
			isBug: true,
			desc:  "docker run -p3000:3000/tcp app should NOT be redacted — /tcp is not a password",
			cmd:   "docker run -p3000:3000/tcp app",
			want:  "docker run -p3000:3000/tcp app",
		},
		{
			name:  "BUG docker -p with /udp suffix falsely redacted",
			isBug: true,
			desc:  "docker run -p53:53/udp dns should NOT be redacted",
			cmd:   "docker run -p53:53/udp dns",
			want:  "docker run -p53:53/udp dns",
		},
		// Space form -p 3000:3000/tcp is NOT falsely redacted (the regex needs
		// \S* directly after -p, so a space breaks the match). The bug only
		// manifests in the no-space form "-p3000:3000/tcp".
		{
			name: "docker -p space-form with /tcp NOT affected (should pass)",
			cmd:  "docker run -p 3000:3000/tcp app",
			want: "docker run -p 3000:3000/tcp app",
		},

		// ── Confirmed redactions ──────────────────────────────────────────────
		{
			name: "mysql -p<password> redacted",
			cmd:  "mysql -psecretpw",
			want: "mysql -p***",
		},
		{
			name: "--password flag redacted",
			cmd:  "tool --password hunter2",
			want: "tool --password ***",
		},
		{
			name: "--token flag redacted",
			cmd:  "deploy --token abcdef",
			want: "deploy --token ***",
		},
		{
			// Note: \S+ consumes the trailing quote too, so it ends up in ***
			name: "bearer token redacted (trailing quote consumed)",
			cmd:  "curl -H 'Bearer mytoken123'",
			want: "curl -H 'Bearer ***",
		},

		// ── Confirmed non-redactions ──────────────────────────────────────────
		{
			name: "docker numeric port not redacted",
			cmd:  "docker run -p8080:80 nginx",
			want: "docker run -p8080:80 nginx",
		},
		{
			name: "git commit -m not redacted",
			cmd:  "git commit -m wip",
			want: "git commit -m wip",
		},
		{
			name: "npm port flag not redacted",
			cmd:  "npm start --port 3000",
			want: "npm start --port 3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redact(tt.cmd)
			if got != tt.want {
				if tt.isBug {
					t.Errorf("BUG CONFIRMED — redact(%q) = %q, want %q\n\tDesc: %s",
						tt.cmd, got, tt.want, tt.desc)
				} else {
					t.Errorf("REGRESSION — redact(%q) = %q, want %q", tt.cmd, got, tt.want)
				}
			}
		})
	}
}
