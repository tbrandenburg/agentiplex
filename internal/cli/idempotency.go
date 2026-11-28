package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Allocate once per intended CLI submission, not per payload or HTTP attempt.
// Print before admission so even a lost response leaves a retry key. Never
// change stdout: it is used for JSON receipts and human-readable wait results.
func submissionKey(c *cobra.Command, explicit string) (string, error) {
	if c.Flags().Changed("idempotency-key") {
		if strings.TrimSpace(explicit) == "" || len(explicit) > 256 {
			return "", fmt.Errorf("idempotency key must be nonempty and at most 256 bytes")
		}
		return explicit, nil
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	key := "cli-" + hex.EncodeToString(random[:])
	if _, err := fmt.Fprintf(c.ErrOrStderr(), "Idempotency key: %s\n", key); err != nil {
		return "", err
	}
	return key, nil
}

func submissionError(key string, err error) error {
	return fmt.Errorf("submission failed: %w; if retrying the same operation within the retention window, reuse --idempotency-key %q with identical arguments; inspect uncertain work before retrying", err, key)
}
