// Contract test: Plan 3 §1 acceptance decision — master.key at mode 0644 must
// cause a fatal boot error with an informative message.
//
// BDD: Given a master.key file at mode 0644, When the credential store checks perms,
//
//	Then it detects the insecure permission and refuses to load the key.
//
// Acceptance decision: Plan 3 §1 "Credential file perms: 0600 enforced at boot; non-compliant → fatal exit"
// Traces to: temporal-puzzling-melody.md §4 Axis-1, pkg/credentials/perm_check_test.go

package credentials_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// TestMasterKeyMode0644RejectsBoot verifies that a master.key file with overly
// permissive mode (0644) is rejected by the production Unlock path.
//
// Unlock reads OMNIPUS_KEY_FILE (mode 2 of the boot contract). If the key file
// has group- or world-readable bits set, loadKeyFile returns an error with the
// phrase "unsafe permissions". This test exercises that production code path.
//
// Traces to: temporal-puzzling-melody.md §4 Axis-1 — TestMasterKeyMode0644RejectsBoot
func TestMasterKeyMode0644RejectsBoot(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "master.key")

	// BDD: Given a master.key file written at mode 0644.
	// The key must be valid hex (32 bytes = 64 hex chars) so the rejection
	// is purely permission-based, not content-based.
	hexKey := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	err := os.WriteFile(keyPath, []byte(hexKey), 0o644)
	require.NoError(t, err, "writing test key file must not fail")

	info, err := os.Stat(keyPath)
	require.NoError(t, err)

	// On some filesystems (e.g. NTFS via WSL) the OS masks the permission bits.
	if (info.Mode().Perm() & 0o044) == 0 {
		t.Skip("OS masked 0644 permissions — cannot verify security check on this platform")
	}

	// BDD: When Unlock is called with OMNIPUS_KEY_FILE pointing at the 0644 file.
	// Redirect OMNIPUS_KEY_FILE and unset OMNIPUS_MASTER_KEY so mode 2 fires.
	t.Setenv("OMNIPUS_KEY_FILE", keyPath)
	t.Setenv("OMNIPUS_MASTER_KEY", "")

	credStore := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	unlockErr := credentials.Unlock(credStore)

	// BDD: Then Unlock must return an error mentioning the unsafe permissions.
	require.Error(t, unlockErr, "Unlock must fail when key file has mode 0644")
	assert.True(t,
		strings.Contains(unlockErr.Error(), "unsafe permissions") ||
			strings.Contains(unlockErr.Error(), "0600") ||
			strings.Contains(unlockErr.Error(), "permission"),
		"error must describe the permission problem; got: %v", unlockErr)

	// Differentiation: mode 0600 must succeed (content is a valid 64-char hex key).
	keyPath2 := filepath.Join(tmpDir, "master2.key")
	err = os.WriteFile(keyPath2, []byte(hexKey), 0o600)
	require.NoError(t, err)

	t.Setenv("OMNIPUS_KEY_FILE", keyPath2)
	credStore2 := credentials.NewStore(filepath.Join(tmpDir, "credentials2.json"))
	unlockErr2 := credentials.Unlock(credStore2)
	assert.NoError(t, unlockErr2, "Unlock must succeed when key file has mode 0600")
}
