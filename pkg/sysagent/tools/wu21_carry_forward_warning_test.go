package systools_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// §3.2(6), chief's CHECK code-note repair: the diagnostic must identify the
// owning workspace, not merely include its path inside a wrapped OS error.
func TestWU21_AgentDeleteCarryForwardWarningNamesWorkspace(t *testing.T) {
	for _, ws := range []string{"alpha", "beta"} {
		t.Run(ws, func(t *testing.T) {
			deps, home, revision := wu21DeleteFixture(t)
			blocked := wu21PairPath(home, ws, "self")
			if err := os.Remove(blocked); err != nil {
				t.Fatal(err)
			}
			wu21DeleteWrite(t, filepath.Join(blocked, "unremovable-child"), "blocker")
			out := systools.RunAgentDeleteCascade(home, "self", revision, deps.DeleteSessionStore(), nil)
			t.Run("failure_and_sibling_controls", func(t *testing.T) {
				if !out.CleanupFailed || len(out.Warnings) != 1 {
					t.Fatalf("fixture must produce exactly its one cleanup failure: failed=%t warnings=%v", out.CleanupFailed, out.Warnings)
				}
				if _, err := agentstore.New(home).Get("self"); err != nil {
					t.Fatalf("failed cleanup removed its retryable agent: %v", err)
				}
				for _, otherWS := range []string{"alpha", "beta"} {
					bytes, err := os.ReadFile(wu21PairPath(home, otherWS, "other"))
					if err != nil || string(bytes) != otherWS+":other" {
						t.Fatalf("unrelated summary control changed: workspace=%s bytes=%q err=%v", otherWS, bytes, err)
					}
				}
			})
			wu21RequireWarningWorkspace(t, out.Warnings, ws)
		})
	}
}

func wu21RequireWarningWorkspace(t *testing.T, warnings []string, want string) {
	t.Helper()
	// Read the labelled workspace value, never a workspace name that only
	// appears in the wrapped error's absolute path. Quote style is immaterial.
	label := regexp.MustCompile(`(?i)\bworkspace\s+(?:"([^"]+)"|'([^']+)'|([a-z0-9_-]+))`)
	for _, warning := range warnings {
		if !strings.Contains(warning, "carry-forward") {
			continue
		}
		if match := label.FindStringSubmatch(warning); len(match) == 4 {
			if match[1]+match[2]+match[3] == want {
				return
			}
		}
	}
	t.Errorf("§3.2(6): carry-forward cleanup warning must label owning workspace %q, not its parent collection; warnings=%v", want, warnings)
}
