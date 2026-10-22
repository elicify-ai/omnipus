//go:build goolm && stdjson

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// DESIGN Q15: current read authorization and the ORIGINAL mount identity are
// both required. Same bytes at an old path or a rebound mount are not proof.
func TestCrashRecovery_ReadAuthorizationAndMountAreRechecked(t *testing.T) {
	for _, name := range []string{"append_file", "write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) {
			crashRequireFileProof(t)
			external := t.TempDir()
			f := crashFileCut(t, name, func(f *crashFileFixture) {
				mount, _, err := workspace.CreateMount(f.h.home, testHarnessWorkspaceMembershipID, "recovery-mount", external)
				require.NoError(t, err)
				f.path = filepath.Join(mount.HostPath, "mounted.txt")
				f.args["path"] = f.path
			})
			raw, err := os.ReadFile(f.witnessFile(t, f.snapshot))
			require.NoError(t, err)
			f.h.restore(t, f.snapshot, &crashScript{})
			control := crashReadback(t, f, raw, testHarnessWorkspaceMembershipID, true)
			require.Equal(t, "effect_present", control["disposition"], "instrument: a currently authorized read-confined mount can prove the exact postimage")
			require.NoError(t, workspace.DeleteMount(f.h.home, testHarnessWorkspaceMembershipID, "recovery-mount"))
			denied := crashReadback(t, f, raw, testHarnessWorkspaceMembershipID, true)
			assert.Equal(t, "unknown", denied["disposition"], "removing current read authorization cannot be replaced by old witness authority")
			unconfined := crashReadback(t, f, raw, testHarnessWorkspaceMembershipID, false)
			assert.Equal(t, "unknown", unconfined["disposition"], "general host readability does not restore the original mount binding")
			other := t.TempDir()
			_, _, err = workspace.CreateMount(f.h.home, testHarnessWorkspaceMembershipID, "recovery-mount", other)
			require.NoError(t, err)
			rebound := crashReadback(t, f, raw, testHarnessWorkspaceMembershipID, false)
			assert.Equal(t, "unknown", rebound["disposition"], "same mount name bound elsewhere never proves the historical target")
			bytes, err := os.ReadFile(f.path)
			require.NoError(t, err)
			assert.Equal(t, f.after, string(bytes), "every reconciliation attempt is read-only at the work target")
		})
	}
}
