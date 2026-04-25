package core_test

import (
	"strings"
	"testing"

	"github.com/zakros-hq/zakros/mnemosyne/core"
)

func TestSanitizeRedactsKnownValues(t *testing.T) {
	in := []byte(`{"log": "token was ghs_abcdefghijklmnopqrstuvwxyz and password sensitive_password_value"}`)
	out := core.Sanitize(in, [][]byte{[]byte("sensitive_password_value")})
	if strings.Contains(string(out), "ghs_abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("github token not redacted: %s", out)
	}
	if strings.Contains(string(out), "sensitive_password_value") {
		t.Errorf("known value not redacted: %s", out)
	}
}

func TestSanitizeRedactsPatterns(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"github token", "token=ghp_abcdefghijklmnopqrstuvwxyz"},
		{"aws key", "key=AKIAIOSFODNN7EXAMPLE"},
		{"anthropic", "sk-ant-api03_abcdefghijklmnop"},
		{"bearer", "Authorization: Bearer abcdefghijklmnopqrstuvwxyz"},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\nabc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := core.Sanitize([]byte(tc.in), nil)
			if string(out) == tc.in {
				t.Errorf("nothing redacted in %q: %s", tc.name, out)
			}
			if !strings.Contains(string(out), "<redacted>") {
				t.Errorf("expected <redacted> marker in output: %s", out)
			}
		})
	}
}

func TestSanitizeLeavesSafeContentAlone(t *testing.T) {
	in := []byte(`{"summary": "opened PR at https://github.com/x/y/pull/1", "status": "completed"}`)
	out := core.Sanitize(in, nil)
	if !strings.Contains(string(out), "https://github.com/x/y/pull/1") {
		t.Errorf("PR URL lost: %s", out)
	}
	if !strings.Contains(string(out), "completed") {
		t.Errorf("status lost: %s", out)
	}
}
