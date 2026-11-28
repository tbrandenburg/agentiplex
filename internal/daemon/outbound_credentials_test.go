package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutboundTokenFilesPreservesAliases(t *testing.T) {
	raw := []byte("delivery:\n  token_files:\n    'https://Agent.example': /a\n    'https://agent.example': /b\n")
	files, err := outboundTokenFiles(raw, "")
	if err != nil || len(files) != 2 || files["https://Agent.example"] != "/a" || files["https://agent.example"] != "/b" {
		t.Fatalf("case-distinct origin keys lost: %v", err)
	}
	files, err = outboundTokenFiles(raw, `{"https://Agent.example":"/c","https://agent.example":"/d"}`)
	if err != nil || len(files) != 2 || files["https://Agent.example"] != "/c" || files["https://agent.example"] != "/d" {
		t.Fatalf("environment map did not replace file map: %v", err)
	}
	for _, tc := range []struct{ raw, env string }{
		{"delivery:\n  token_files:\n    'https://agent.example': /a\n    'https://agent.example': /b\n", ""},
		{"delivery:\n  token_files: {}\n  TOKEN_FILES: {}\n", ""},
		{"delivery: {}\nDelivery: {}\n", ""},
		{"delivery.token_files: {}\n", ""},
		{"delivery: {token_files: []}\n", ""},
		{"", `{"https://agent.example":"/a","https://agent.example":"/b"}`},
		{"", "not-json"},
		{"", "null"},
		{"delivery: {token_files: {\"https://agent.example\": 123}}\n", ""},
		{"", `{"https://agent.example":123}`},
		{"delivery:\n  token_files:\n    <<: {\"https://agent.example\": /a}\n", ""},
	} {
		if _, err := outboundTokenFiles([]byte(tc.raw), tc.env); err == nil {
			t.Error("ambiguous or invalid credential mapping accepted")
		}
	}
}

func TestDaemonRejectsConflictingCredentialOrigins(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for path, token := range map[string]string{a: strings.Repeat("a", 48), b: strings.Repeat("b", 48)} {
		if err := os.WriteFile(path, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(dir, "config.yaml")
	raw := fmt.Sprintf("delivery:\n  token_files:\n    'https://Agent.example': %q\n    'https://agent.example': %q\n", a, b)
	if err := os.WriteFile(config, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, env, configText string }{
		{"yaml", "", raw},
		{"env", fmt.Sprintf(`{"https://Agent.example":%q,"https://agent.example":%q}`, a, b), "delivery: {token_files: {}}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENTIPLEX_DELIVERY_TOKEN_FILES", tc.env)
			if err := os.WriteFile(config, []byte(tc.configText), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := Command()
			// A bad listen address prevents network startup even if the
			// credential check regresses; the conflict must be reported first.
			cmd.SetArgs([]string{"--config", config, "--listen", "invalid-address"})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "conflicting outbound credential") {
				t.Fatalf("daemon did not reject credential conflict before startup: %v", err)
			}
			if strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), strings.Repeat("a", 48)) || strings.Contains(err.Error(), strings.Repeat("b", 48)) {
				t.Fatal("credential data disclosed in startup error")
			}
		})
	}
}
