//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// Amendment §2 D4/D7, T-L L3; chief ci1-fs ruling 2026-10-14:
// Landlock cannot grant an agent-home directory wholesale while excluding
// its protected interior. Pin the measured top-level creation limit without
// hiding it by dropping the ordinary-file or internal-file controls.
func TestWU23_LandlockAgentHomeCreationLimit(t *testing.T) {
	if wu23LandlockChild() {
		return
	}
	for _, floor := range []bool{false, true} {
		t.Run(fmt.Sprintf("floor_%t", floor), func(t *testing.T) {
			f := fixture.New(t)
			scratch := filepath.Join(f.AgentHome, "scratch")
			if err := os.MkdirAll(scratch, 0o700); err != nil {
				t.Fatal(err)
			}
			authored, err := fspolicy.EffectiveFSPolicy(t.Context(), f.AgentHome, "", true, f.Home, "self", "")
			if err != nil {
				t.Fatal(err)
			}
			policy := DeriveKernelPolicy(authored, TurnPolicyInput{HomePath: f.Home, Model: FilesystemModelOpen, Floor: floor})
			existing := filepath.Join(f.AgentHome, "allowed.txt")
			fresh := filepath.Join(f.AgentHome, "new-direct.txt")
			script := `cat "$1" >/dev/null || exit 90
printf changed > "$1" || exit 91
printf nested > "$2/new.txt" || exit 92
printf 'CONTROL=ok\n'
if { printf new > "$3"; } 2>/dev/null; then printf 'NEW=allowed\n'; else printf 'NEW=denied\n'; fi
if cat "$4" >/dev/null 2>/dev/null; then printf 'SOUL=allowed\n'; else printf 'SOUL=denied\n'; fi
if cat "$5" >/dev/null 2>/dev/null; then printf 'MEMORY=allowed\n'; else printf 'MEMORY=denied\n'; fi`
			cmd := wu23KernelCommand(t, policy, []string{"/bin/sh", "-c", script, "wu23-home", existing, scratch, fresh,
				filepath.Join(f.AgentHome, "SOUL.md"), filepath.Join(f.AgentHome, ".omnipus", "last-session.md")})
			out, childErr := cmd.CombinedOutput()
			if childErr != nil {
				t.Fatalf("agent-home control failed: %v output=%s", childErr, out)
			}
			if want := "CONTROL=ok\nNEW=denied\nSOUL=denied\nMEMORY=denied\n"; string(out) != want {
				t.Fatalf("agent-home creation limit: got=%q want=%q", out, want)
			}
			if _, statErr := os.Stat(fresh); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("new direct agent-home entry was created: %v", statErr)
			}
			for path, want := range map[string]string{existing: "changed", filepath.Join(scratch, "new.txt"): "nested"} {
				got, readErr := os.ReadFile(path)
				if readErr != nil || string(got) != want {
					t.Fatalf("same-child write control %s: got=%q want=%q err=%v", path, got, want, readErr)
				}
			}
		})
	}
}
