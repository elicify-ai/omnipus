//go:build linux || darwin

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// F2: these are internal runtime inputs, NOT the operator/mount exception the
// founder accepted. Inventory always wins over runtime allowances at or above it.
func wu23R1RuntimeRootsPreserveInventory(t *testing.T) {
	for _, floor := range []bool{false, true} {
		for _, kind := range []string{"at_sessions", "above_home", "at_credentials", "at_definitions", "above_agent_memory"} {
			t.Run(fmt.Sprintf("floor_%t/%s", floor, kind), func(t *testing.T) {
				f := fixture.New(t)
				root, protected := filepath.Join(f.Home, "sessions"), filepath.Join(f.Home, "sessions", "private.txt")
				switch kind {
				case "above_home":
					root = f.Home
				case "at_credentials":
					root, protected = filepath.Join(f.Home, "credentials.json"), filepath.Join(f.Home, "credentials.json")
				case "at_definitions":
					root, protected = filepath.Join(f.Home, "entities"), filepath.Join(f.Home, "entities", "agents", "self.json")
				case "above_agent_memory":
					root, protected = f.AgentHome, filepath.Join(f.AgentHome, ".omnipus", "last-session.md")
				}
				authored, err := fspolicy.EffectiveFSPolicy(t.Context(), f.AgentHome, f.Work, true, f.Home, "self", "alpha")
				if err != nil {
					t.Fatal(err)
				}
				policy := DeriveKernelPolicy(authored, TurnPolicyInput{HomePath: f.Home, Model: FilesystemModelOpen, Floor: floor, AllowRoots: []fspolicy.AllowRoot{{Path: root, Writable: true}}})
				script := `cat "$1" >/dev/null || exit 90
printf control > "$2/control.txt" || exit 91
printf 'CONTROL=ok\n'
if cat "$3" >/dev/null 2>/dev/null; then printf 'INTERNAL_READ=allowed\n'; else printf 'INTERNAL_READ=denied\n'; fi
if { printf corrupt > "$3"; } 2>/dev/null; then printf 'INTERNAL_WRITE=allowed\n'; else printf 'INTERNAL_WRITE=denied\n'; fi`
				cmd := wu23KernelCommand(t, policy, []string{"/bin/sh", "-c", script, "wu23-r1", filepath.Join(f.Work, "allowed.txt"), f.Work, protected})
				out, childErr := cmd.CombinedOutput()
				if childErr != nil {
					t.Fatalf("child failed before runtime/inventory probe: %v output=%s", childErr, out)
				}
				t.Run("same_child_workspace_control", func(t *testing.T) {
					data, readErr := os.ReadFile(filepath.Join(f.Work, "control.txt"))
					if !strings.Contains(string(out), "CONTROL=ok\n") || readErr != nil || string(data) != "control" {
						t.Fatalf("same-child read/write control failed: output=%s bytes=%q err=%v", out, data, readErr)
					}
				})
				want := "CONTROL=ok\nINTERNAL_READ=denied\nINTERNAL_WRITE=denied\n"
				if string(out) != want {
					t.Errorf("runtime AllowRoot=%s must never reopen inventory: got=%q want=%q", root, out, want)
				}
				data, readErr := os.ReadFile(protected)
				if readErr != nil || string(data) != fixture.Sentinel {
					t.Errorf("runtime allowance changed protected bytes: got=%q want=%q err=%v", data, fixture.Sentinel, readErr)
				}
			})
		}
	}
}
