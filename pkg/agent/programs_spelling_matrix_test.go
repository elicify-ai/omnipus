package agent

// Rev8 S26/D3 rows1-10: real loop, matcher, approvals, child and shared proxy.
// The external shell wrapper records its actual environment before exec'ing
// /bin/sh with the original argv. This makes even the builtin echo observable
// without changing the command or substituting a matcher/grant/TurnPrograms.
// Each exact leaf self-execs before applying the real enforcing Linux boot
// policy. Its actual shell must prove a kernel denial against a positively
// readable protected canary; zero approvals remain the spec's exact oracle.
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramNetwork_ActualSpellingMatrix(t *testing.T) {
	for _, row := range []struct {
		name, command, output string
		grant                 bool
	}{
		{"D3_01_plain", "gh api user", "octocat", true},
		{"D3_02_absolute", "ABSOLUTE_GH api user", "octocat", true},
		{"D3_03_wrapper", "env GH_DEBUG=1 gh api user", "octocat", true},
		{"D3_04_nested_wrappers", "nohup timeout 30 gh api user", "octocat", true},
		{"D3_05_assignment", "X=1 gh api user", "octocat", true},
		{"D3_06_argument", "echo gh", "gh", false},
		{"D3_07_lookalike", "ghx api", "S26_GHX_RAN", false},
		{"D3_08_case", "GH api user", "octocat", false},
		{"D3_09_pipeline", "gh api user | jq .", "octocat", true},
		{"D3_10_nested_shell", `bash -c "gh api user"`, "S26_GH_CONNECTION_ERROR", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			if programsKernelReexec(t) {
				return
			}
			var ordinary []map[string]any
			connectedCases := []bool{true}
			if !row.grant {
				connectedCases = []bool{false, true}
			}
			for _, connected := range connectedCases {
				observe, err := os.MkdirTemp("/dev/shm", "s1-spelling-")
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, os.RemoveAll(observe)) })
				x := newProgramsNetworkFixture(t, false, true, false)
				if !connected {
					x.f.al.GetConfig().Agents.List[0].Programs = nil
				}
				bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
				// Observations use an already-permitted scratch device, not a new
				// policy grant or a production guard exception. The command itself
				// remains unchanged and rooted in the real workspace.
				x.child, x.release = filepath.Join(observe, "proxy"), filepath.Join(observe, "release")
				countPath := filepath.Join(observe, "spelling-child-count")
				canary := programsEnforceKernel(t, x.f)
				cwd := programsForeground(t, x.f, "s26-cwd-control", "pwd")
				require.Equal(t, x.work, strings.TrimSpace(cwd), "Given: the actual shell is rooted in this fixture's workspace")
				denial := filepath.Join(observe, "spelling-kernel-denial")
				wrapper := "#!/bin/sh\n" + programsKernelReadAttempt(canary, denial) + fmt.Sprintf("printenv GH_TOKEN | wc -c > %q\nprintf '%%s' \"${HTTPS_PROXY:-}\" > %q\nmv %q %q\nwhile [ ! -f %q ]; do sleep 0.01; done\nexec /bin/sh \"$@\"\n", countPath, x.child+".tmp", x.child+".tmp", x.child, x.release)
				require.NoError(t, os.WriteFile(filepath.Join(bin, "sh"), []byte(wrapper), 0700))
				wantCount, check := "16", `[ "$GH_TOKEN" = tok_valid_12345 ] || exit 83`
				if !connected {
					wantCount, check = "0", `[ -z "${GH_TOKEN:-}" ] || exit 82`
				}
				// Real wrappers are kept; only remote command executables are
				// controlled so this test never needs public network/model calls.
				gh := "#!/bin/sh\n" + check + "\n[ \"$1:$2\" = api:user ] || exit 81\nprintf 'octocat\\n'\n"
				if row.name == "D3_10_nested_shell" {
					gh = "#!/bin/sh\n" + check + "\nprintf 'S26_GH_CONNECTION_ERROR\\n' >&2\nexit 7\n"
				}
				require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(bin, "GH"), []byte(gh), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(bin, "ghx"), []byte("#!/bin/sh\n"+check+"\n[ \"$#:$1\" = 1:api ] || exit 81\nprintf 'S26_GHX_RAN\\n'\n"), 0700))
				// This edge deliberately accepts octocat as opaque pipeline data;
				// it is not a claim that the non-JSON text is valid jq input.
				require.NoError(t, os.WriteFile(filepath.Join(bin, "jq"), []byte("#!/bin/sh\n[ \"$#:$1\" = 1:. ] || exit 81\nexec /bin/cat\n"), 0700))
				// Absolute-path equivalence class uses a disposable executable;
				// never overwrite this machine's /usr/local/bin/gh.
				command := strings.ReplaceAll(row.command, "ABSOLUTE_GH", filepath.Join(bin, "gh"))
				finish := x.start(t, command)
				token := x.childToken(t, row.grant)
				raw, err := os.ReadFile(countPath)
				require.NoError(t, err)
				require.Equal(t, wantCount, strings.TrimSpace(string(raw)), "actual shell token delivery is independent of program-word recognition")
				x.probe(t, token, "api.github.com", row.grant)
				x.probe(t, token, "github.com", row.grant)
				x.probe(t, token, "example.org", false)
				requests := x.approver.snapshot()
				var args []map[string]any
				for _, req := range requests {
					require.Equal(t, "bash", req.ToolName)
					args = append(args, req.Args)
				}
				if row.grant {
					assert.Empty(t, requests, "S26 requires no approval under the actual enforcing-sandbox Given")
				} else if !connected {
					ordinary = args
				} else {
					require.Equal(t, ordinary, args, "unmatched D3 spelling keeps exact ordinary approval arguments/count")
				}
				result := finish()
				programsAssertKernelDenied(t, canary, denial)
				if row.name == "D3_10_nested_shell" {
					require.Contains(t, result, row.output, "actual nested gh's own connection error survives; no fake success")
				} else {
					require.Equal(t, row.output, strings.TrimSpace(result), "the actual exact spelling reached its command edge")
				}
				x.probe(t, token, "api.github.com", false)
				x.probe(t, token, "github.com", false)
				require.Equal(t, requests, x.approver.snapshot(), "no second approval after the child was admitted")
				t.Logf("actual D3 row=%s connected=%t site_granted=%t child_count=%s approvals=%d revoked=true", row.name, connected, row.grant, wantCount, len(requests))
				if row.name == "D3_10_nested_shell" {
					// FR-026: compare the WHOLE result to a separate actual
					// ordinary child with the same stderr/exit pair. This pins
					// unchanged failure semantics, not a copied presentation.
					control := programsForeground(t, x.f, "s26-error-control", "printf 'S26_GH_CONNECTION_ERROR\\n' >&2; exit 7")
					require.Equal(t, 1, strings.Count(control, row.output), "ordinary control really emits the one external error line")
					require.Contains(t, control, "[Command exited with code 7]", "ToolResult's documented foreground suffix preserves the actual fixture exit7")
					require.Equal(t, control, strings.TrimSpace(result), "S26/FR-026: the complete nested gh error and exit pass through unchanged")
				}
				// Setenv cleanup is row-scoped. Do not let this completed
				// external shell observer become the next fixture's fallback sh.
				require.NoError(t, os.Remove(filepath.Join(bin, "sh")))
			}
		})
	}
}
