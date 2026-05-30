package secret

import "testing"

func TestMatchFlagsKnownTokens(t *testing.T) {
	// NOTE: all tokens below are deliberately fake (EXAMPLE/dummy/AWS's own
	// documented example key) — they match our detector's shape but are not real
	// credentials, so this repo never trips secret scanners.
	secrets := []string{
		"echo gsk_EXAMPLEdummytoken00000000",
		`git commit -m "key is sk-ant-EXAMPLEdummytoken00000000"`,
		"export OPENAI_API_KEY=sk-EXAMPLEdummytoken",
		"curl -H 'Authorization: Bearer EXAMPLEdummytoken00'",
		"deploy --token ghp_EXAMPLEdummytokenpadding000000",
		"aws s3 ls AKIAIOSFODNN7EXAMPLE",
		"git clone https://user:EXAMPLEpw@github.com/x/y",
		"FOO_SECRET=EXAMPLEvalue make deploy",
	}
	for _, s := range secrets {
		if _, ok := Match(s); !ok {
			t.Errorf("expected secret detected in %q", s)
		}
	}
}

func TestMatchIgnoresInnocentCommands(t *testing.T) {
	clean := []string{
		"git status",
		"git checkout 9f2c1ab3de4567890abcdef1234567890abcdef12", // 40-hex SHA, not a secret
		"npm run build",
		"echo $GROQ_API_KEY",                   // env reference, no literal on the line
		"export PATH=/usr/bin",                 // not a secret-named var
		"export EDITOR=vim",                    // ditto
		"ssh-keygen -t ed25519",                // mentions 'key' but no token
		"git commit -m 'fix api key handling'", // word 'key', no KEY=value, no token
		"docker run -p 8080:80 nginx",
	}
	for _, s := range clean {
		if reason, ok := Match(s); ok {
			t.Errorf("false positive on %q: %s", s, reason)
		}
	}
}

func TestMatchReasonNeverLeaksValue(t *testing.T) {
	reason, ok := Match("echo gsk_EXAMPLEdummytoken00000000")
	if !ok {
		t.Fatal("expected detection")
	}
	if reason == "" {
		t.Fatal("expected a reason")
	}
	// The reason is a static label; it must not contain the token text.
	if want := "looks like a Groq API key"; reason != want {
		t.Errorf("reason = %q, want %q", reason, want)
	}
}
