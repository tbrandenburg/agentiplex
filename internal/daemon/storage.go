package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

// XDG base directories must be absolute. Ignore a relative XDG_STATE_HOME
// rather than accidentally selecting a directory relative to the process.
func defaultStoragePath(stateHome, home string) string {
	if filepath.IsAbs(stateHome) {
		return filepath.Join(stateHome, "agentiplex")
	}
	if filepath.IsAbs(home) {
		return filepath.Join(home, ".local", "state", "agentiplex")
	}
	return ""
}

// New default directories are private. Do not chmod, migrate or delete
// existing directories; explicit storage paths keep their existing semantics.
func prepareDefaultStorage(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create default embedded storage: %w", err)
	}
	return nil
}
