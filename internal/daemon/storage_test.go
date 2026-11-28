package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultStoragePath(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(t.TempDir(), "state")
	fallback := filepath.Join(home, ".local", "state", "agentiplex")
	for _, tc := range []struct {
		name, xdg, home, want string
	}{
		{"unset XDG", "", home, fallback},
		{"absolute XDG", xdg, home, filepath.Join(xdg, "agentiplex")},
		{"relative XDG ignored", "relative/state", home, fallback},
		{"XDG without HOME", xdg, "", filepath.Join(xdg, "agentiplex")},
		{"no usable base", "relative/state", "relative/home", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultStoragePath(tc.xdg, tc.home); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStorageFlagDefault(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	c := Command()
	f := c.Flags().Lookup("storage")
	if want := filepath.Join(xdg, "agentiplex"); f.DefValue != want {
		t.Fatalf("default %q, want %q", f.DefValue, want)
	}
	explicit := filepath.Join(t.TempDir(), "explicit")
	if err := c.Flags().Set("storage", explicit); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Flags().GetString("storage"); err != nil || got != explicit {
		t.Fatalf("explicit storage: %q, %v", got, err)
	}
}

func TestPrepareDefaultStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "agentiplex")
	if err := prepareDefaultStorage(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		t.Fatalf("storage directory not private: %v", info.Mode())
	}
	file := filepath.Join(path, "existing")
	if err := os.WriteFile(file, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareDefaultStorage(path); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "retain" {
		t.Fatalf("existing state changed: %q, %v", got, err)
	}
}
