package programs

// S-106 / CHECK R06: these are explicit limits of the current intraprocedural
// scanner, not proof of complete source coverage. Keep them loud until the
// same author reconciles a site inventory and helper-aware discovery.

import (
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramSpawnGuard_InventoryBoundaries(t *testing.T) {
	t.Run("new_launch_in_same_listed_function", func(t *testing.T) {
		src := `package agent
import "os/exec"
func NewProcessHook() {
    _ = exec.Command("go", "version").Run()
    _ = exec.Command("gh", "api", "user").Run()
}`
		sites := scanFileForSpawnSites(t, token.NewFileSet(), "pkg/agent/hook_process.go", src)
		require.Len(t, sites, 2, "instrument: both actual launch sites are discovered")
		if len(spawnClassify(sites)) == 0 {
			t.Fatal("BLOCKED: spawn classification is still file+function, so a second launch inherits its non-program exemption — required per-site inventory by S-106/R06")
		}
	})
	t.Run("helper_constructs_launcher_receives_command", func(t *testing.T) {
		src := `package agent
import "os/exec"
func makeChild() *exec.Cmd { return exec.Command("gh", "api", "user") }
func startChild(cmd *exec.Cmd) error { return cmd.Run() }`
		sites := scanOneSpawnSource(t, src)
		if len(sites) != 1 {
			t.Fatalf("BLOCKED: helper-split launch discovery is not implemented (got %d sites, want 1) — required by S-106/R06; constructor-only helper is not a discovered launch", len(sites))
		}
		require.False(t, sites[0].AttachSameCommand, "no Attach exists on the planted helper-split path")
	})
}
