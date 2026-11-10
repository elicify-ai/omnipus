//go:build linux || darwin

package systools_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// §3.2(6) / FR-037: inability to enumerate owned carry-forward data is a
// cleanup failure, not evidence that there are no files. Native Unix permissions
// are real; no Glob/ReadDir/Delete mock or root-user skip manufactures this RED.
func TestWU21_AgentDeleteCarryForwardScanFailureIsVisibleAndKeepsEntity(t *testing.T) {
	deps, home, revision := wu21DeleteFixture(t)
	denied := filepath.Dir(wu21PairPath(home, "alpha", "self"))
	t.Cleanup(func() {
		if err := os.Chmod(denied, 0o700); err != nil {
			t.Errorf("restore native permission fixture: %v", err)
		}
	})
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatalf("permission-denial instrument could not be installed: %v", err)
	}
	t.Run("native_denial_instrument", func(t *testing.T) {
		_, err := os.ReadDir(denied)
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("BLOCKED: fixture must produce native permission-denied enumeration (run unprivileged); got %v", err)
		}
	})
	if t.Failed() {
		return // no delete verdict when the native failure instrument is invalid.
	}
	t.Run("readable_sibling_control", func(t *testing.T) {
		entries, err := os.ReadDir(filepath.Dir(wu21PairPath(home, "beta", "self")))
		if err != nil || len(entries) != 2 { // the fixture seeds self.md and other.md.
			t.Fatalf("readable sibling control: entries=%v err=%v", entries, err)
		}
	})
	out := systools.RunAgentDeleteCascade(home, "self", revision, deps.DeleteSessionStore(), nil)
	if !out.CleanupFailed || len(out.Warnings) == 0 {
		t.Errorf("§3.2(6): denied carry-forward scan must fail visibly, never count as an empty directory: failed=%t warnings=%v", out.CleanupFailed, out.Warnings)
	}
	wu21RequireWarningWorkspace(t, out.Warnings, "alpha")
	if _, err := agentstore.New(home).Get("self"); err != nil {
		t.Errorf("§3.2(6): incomplete scan removed entity instead of retaining retryable state: %v", err)
	}
	if err := os.Chmod(denied, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Run("preserved_data_controls", func(t *testing.T) {
		for _, pair := range [][2]string{{"alpha", "self"}, {"alpha", "other"}, {"beta", "other"}} {
			bytes, err := os.ReadFile(wu21PairPath(home, pair[0], pair[1]))
			if err != nil || string(bytes) != pair[0]+":"+pair[1] {
				t.Errorf("native scan-failure control bytes changed: pair=%v bytes=%q err=%v", pair, bytes, err)
			}
		}
	})
}
