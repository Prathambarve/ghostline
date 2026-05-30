package history

import "testing"

func TestDenylisted(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"export api token", "export GITHUB_TOKEN=ghp_abc123", true},
		{"export secret key lowercase", "export my_secret_key=hunter2", true},
		{"db password assignment", "DB_PASSWORD=swordfish ./run", true},
		{"authorization header", `curl -H "Authorization: Bearer xyz" https://api`, true},
		{"aws secret", "aws_secret_access_key=AKIA...", true},
		{"private key paste", "echo -----BEGIN RSA PRIVATE KEY----- > k", true},
		{"plain git command", "git status", false},
		{"docker run with port", "docker run -p 8080:80 nginx", false},
		{"make build", "make build", false},
		{"export PATH is fine", "export PATH=$PATH:/usr/local/bin", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := denylisted(tt.cmd); got != tt.want {
				t.Errorf("denylisted(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestRedact(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"password flag with space", "mysql --password secret123", "mysql --password ***"},
		{"password flag with equals", "tool --password=secret123", "tool --password=***"},
		{"token flag", "deploy --token abcdef", "deploy --token ***"},
		{"api-key flag", "x --api-key zzz", "x --api-key ***"},
		{"mysql -p no space with letters", "mysql -psecretpw", "mysql -p***"},
		{"bearer token", "curl -H Bearer abc.def.ghi", "curl -H Bearer ***"},
		{"docker numeric port untouched", "docker run -p8080:80 img", "docker run -p8080:80 img"},
		{"no secrets untouched", "git commit -m wip", "git commit -m wip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redact(tt.cmd); got != tt.want {
				t.Errorf("redact(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestClean(t *testing.T) {
	// denylisted commands are dropped entirely
	if _, ok := clean("export TOKEN=abc"); ok {
		t.Errorf("clean() should drop a denylisted command")
	}
	// kept commands are redacted
	got, ok := clean("mysql --password hunter2")
	if !ok {
		t.Fatalf("clean() should keep a non-denylisted command")
	}
	if got != "mysql --password ***" {
		t.Errorf("clean() = %q, want redacted form", got)
	}
}
