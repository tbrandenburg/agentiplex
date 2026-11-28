package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialOriginNormalization(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://Agent.Example", "https://agent.example:443"},
		{"https://agent.example:443/", "https://agent.example:443"},
		{"https://agent.example:0443", "https://agent.example:443"},
		{"https://agent.example:8443", "https://agent.example:8443"},
		{"https://[0:0:0:0:0:0:0:1]:443/", "https://[::1]:443"},
		{"https://agent.example.", "https://agent.example.:443"},
	} {
		got, err := HTTPSCredentialOrigin(tc.raw)
		if err != nil || got != tc.want {
			t.Errorf("origin %q = %q, %v; want %q", tc.raw, got, err, tc.want)
		}
	}
	for _, raw := range []string{
		"http://agent.example", "https://user:secret@agent.example", "https://agent.example/card",
		"https://agent.example/%2f", "https://agent.example/?", "https://agent.example/?q=x",
		"https://agent.example/#", "https://agent.example/#fragment", "https:opaque",
		"https://agent.example:0", "https://agent.example:65536", "https://agent.example:",
		"https://[agent.example]", "https://[127.0.0.1]", "https://[fe80::1%25eth0]",
	} {
		if _, err := HTTPSCredentialOrigin(raw); err == nil {
			t.Errorf("accepted invalid credential origin %q", raw)
		}
	}
}

func TestNormalizeOutboundTokenFiles(t *testing.T) {
	dir := t.TempDir()
	token := strings.Repeat("a", 48)
	write := func(name, value string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, same, b := write("a", token), write("same", token+"\n"), write("b", strings.Repeat("b", 48))
	missing := filepath.Join(dir, "missing")
	for _, tc := range []struct {
		name string
		files map[string]string
	}{
		{"conflict", map[string]string{"https://agent.example": a, "https://AGENT.example:443/": b}},
		{"numeric port conflict", map[string]string{"https://agent.example:0443": a, "https://agent.example": b}},
		{"IPv6 conflict", map[string]string{"https://[::1]": a, "https://[0:0:0:0:0:0:0:1]:443": b}},
		{"unreadable alias", map[string]string{"https://agent.example": a, "https://AGENT.example": missing}},
		{"invalid path alias", map[string]string{"https://agent.example": a, "https://agent.example/card": same}},
		{"invalid token", map[string]string{"https://agent.example": write("invalid", "short")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 10; i++ {
				out, err := NormalizeOutboundTokenFiles(tc.files)
				if err == nil || out != nil {
					t.Fatal("invalid credentials did not fail closed")
				}
				if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), dir) {
					t.Fatal("credential data disclosed in error")
				}
			}
		})
	}
	out, err := NormalizeOutboundTokenFiles(map[string]string{
		"https://AGENT.example:443/": same, "https://agent.example": a,
		"https://agent.example:8443": b, "https://other.example": b,
	})
	if err != nil || len(out) != 3 || out["https://agent.example:443"] != same || out["https://agent.example:8443"] != b || out["https://other.example:443"] != b {
		t.Fatal("equal aliases did not merge deterministically, or distinct origins collapsed")
	}
}
