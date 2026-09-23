package adr091

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestADR091_ResidualAudit verifies that no deletion-manifest symbols remain in production code.
// Each sub-test runs a grep against the repository tree, compares the count to the exact expected count,
// and treats grep exit 1 as "0 matches" and any other non-zero exit as a failure of the check.
//
// Binding rule (lane 4, ADR-091 fix round): every row that asserts "zero matches" MUST first prove its
// own search mechanism is alive by asserting a stated positive lower bound over the SAME scope/flags —
// see `sentinel` below. Without that, a typo'd pattern, a wrong --include glob, or a wrong regex dialect
// passes silently and the row proves nothing. Rows that instead assert a positive count ("One runner
// caller"; "allow_blocking_question refused by name, not accepted anywhere") are exempt — their own
// exact-count assertion already proves the mechanism, and for the latter `expectedSoleFile` additionally
// pins WHERE the surviving occurrence must live, which a raw count alone cannot do.
func TestADR091_ResidualAudit(t *testing.T) {
	// Determine repo root by starting from this test's directory and walking up
	repoRoot := findRepoRoot(t)

	// Reusable sentinel: "does grep, with this exact scope (dirs/includes/excludes), find ANY real
	// Go source at all?" Every non-test .go file under the scanned tree starts with a package clause,
	// so this is always >=1 when the scope actually resolves to real files.
	pkgGoSentinel := func(dirs []string) *exec.Cmd {
		return buildGrep(repoRoot, dirs, []string{"^package "}, []string{"*.go"}, []string{"*_test.go"})
	}

	tests := []struct {
		name          string
		cmd           *exec.Cmd
		expected      int
		sentinel      *exec.Cmd // nil = row is exempt (see binding rule above)
		sentinelLabel string
		// mustDirExist: for existence-style checks (FR-047), the positive lower bound isn't a grep
		// match, it's "the directory that would have held the file still exists" — otherwise a
		// deleted/renamed directory makes the check pass by accident, proving nothing (mirrors the
		// "Address not borrowed" bug where a --include glob matched zero files).
		mustDirExist string
		// expectedSoleFile: for a row whose `expected` is exactly 1 (not 0), the ONE occurrence must
		// live in this file — not just "somewhere in scope". Pins the surviving occurrence to the
		// legitimate site instead of merely counting it, so the row fails just as loudly if the
		// literal moves (or is duplicated) to any other file as it does if it vanishes entirely.
		expectedSoleFile string
	}{
		{
			name:          "Ring gone",
			cmd:           buildGrep(repoRoot, []string{"pkg/"}, []string{"newEphemeralSession", "maxEphemeralHistorySize", "ephemeralSessionStore"}, []string{"*.go"}, []string{"*_test.go"}),
			expected:      0,
			sentinel:      pkgGoSentinel([]string{"pkg/"}),
			sentinelLabel: "pkg/**/*.go (excl _test.go) contains real package declarations",
		},
		{
			name: "Wait-inline gone",
			cmd: buildGrep(repoRoot, []string{"pkg/", "contracts/", "src/"},
				[]string{"executeSync", "DelegationModeAwait"},
				[]string{"*.go", "*.yaml", "*.ts", "*.tsx"}, []string{"*_test.go", "*.test.*"}),
			expected: 0,
			sentinel: buildGrep(repoRoot, []string{"pkg/", "contracts/", "src/"}, []string{"^package ", "openapi", "^import "}, []string{"*.go", "*.yaml", "*.ts", "*.tsx"}, []string{"*_test.go", "*.test.*"}),
			sentinelLabel: "pkg/, contracts/ and src/ combined (with these includes/excludes) contain " +
				"known-present content",
		},
		{
			// Finding 3 (refined per coordinator review, second pass): `allow_blocking_question`
			// does NOT belong on a "zero occurrences" row. ADR-091 deleted the argument, but the
			// tool STILL has to name it in order to refuse it — pkg/tools/delegate_run.go's
			// rejection loop must contain the literal to return "invalid_argument: allow_blocking_
			// question" for a caller that still sends it. A "zero occurrences" row is unsatisfiable
			// by construction: fixing production code to stop evading the check (see delegate_run.go
			// history — it used to build the literal via `"allow_" + "blocking_question"` specifically
			// so a plain-string grep could not see it) would make the row permanently red, and the
			// next person to touch it "fixes" the row instead of the code. That is how the original
			// evasion happened.
			//
			// So this is a POSITIVE, PINNED assertion instead: exactly one occurrence in pkg/tools/
			// (production code), and it must be in delegate_run.go — the file that actually refuses
			// the argument, not any file that might silently accept it. This is strictly stronger
			// than a zero-check: it fails if the rejection is deleted (count drops to 0) AND fails if
			// a second acceptance site appears anywhere in pkg/tools/ (count rises to 2, or the sole
			// hit moves out of delegate_run.go). Same shape as "One runner caller" below, the one
			// original row that already proved its own mechanism this way.
			name:             "allow_blocking_question refused by name, not accepted anywhere",
			cmd:              buildGrep(repoRoot, []string{"pkg/tools/"}, []string{"allow_blocking_question"}, []string{"*.go"}, []string{"*_test.go"}),
			expected:         1,
			expectedSoleFile: "pkg/tools/delegate_run.go",
		},
		{
			name:          "Prompt clean",
			cmd:           buildGrepFile(repoRoot, "pkg/agent/delegation_context.go", "async=false", []string{}, []string{}),
			expected:      0,
			sentinel:      buildGrepFile(repoRoot, "pkg/agent/delegation_context.go", "^package ", []string{}, []string{}),
			sentinelLabel: "pkg/agent/delegation_context.go exists and has real content",
		},
		{
			// Finding 4: "subturn*.go" no longer names where a borrowed parent address could
			// reappear. subturn.go and subturn_result.go (the files that glob was written for) are
			// deleted; the surviving parent-address-bearing code is the launcher, the frame plumbing
			// and turn reconstruction. Re-point at those files by name so the check can observe
			// something again.
			name: "Address not borrowed",
			cmd: buildGrepFilesScoped(repoRoot, []string{
				"pkg/agent/steer_launcher.go", "pkg/agent/steer_frames.go", "pkg/agent/steer_reconstruct.go",
			}, []string{"parentTS.channel", "parentTS.chatID"}, []string{}, []string{}),
			expected: 0,
			sentinel: buildGrepFilesScoped(repoRoot, []string{
				"pkg/agent/steer_launcher.go", "pkg/agent/steer_frames.go", "pkg/agent/steer_reconstruct.go",
			}, []string{"ChatID"}, []string{}, []string{}),
			sentinelLabel: "steer_launcher.go/steer_frames.go/steer_reconstruct.go exist and mention ChatID",
		},
		{
			name:          "SubTurn config gone",
			cmd:           buildGrep(repoRoot, []string{"pkg/"}, []string{"SubTurn\\."}, []string{"*.go"}, []string{"*_test.go"}),
			expected:      0,
			sentinel:      pkgGoSentinel([]string{"pkg/"}),
			sentinelLabel: "pkg/**/*.go (excl _test.go) contains real package declarations",
		},
		{
			// The one row that asserts a positive count: its own >=1 assertion already proves the
			// search mechanism works, so it is exempt from the sentinel rule above.
			name:     "One runner caller",
			cmd:      buildGrepExcludeFile(repoRoot, []string{"pkg/"}, []string{"runExternalCLISubTurn("}, []string{"*.go"}, []string{"*_test.go", "external_dispatch.go"}),
			expected: 1,
		},
		{
			name:     "ParentDurableKey gone",
			cmd:      buildGrep(repoRoot, []string{"pkg/", "contracts/"}, []string{"ParentDurableKey"}, []string{"*.go", "*.yaml"}, []string{"*_test.go"}),
			expected: 0,
			sentinel: buildGrep(repoRoot, []string{"pkg/", "contracts/"}, []string{"^package ", "openapi"}, []string{"*.go", "*.yaml"}, []string{"*_test.go"}),
			sentinelLabel: "pkg/ and contracts/ combined (with these includes/excludes) contain " +
				"known-present content",
		},
		{
			name:          "ProducingSessionID gone",
			cmd:           buildGrepFilesScoped(repoRoot, []string{"pkg/agent/events.go", "pkg/gateway/websocket_forward.go"}, []string{"ProducingSessionID"}, []string{}, []string{}),
			expected:      0,
			sentinel:      buildGrepFilesScoped(repoRoot, []string{"pkg/agent/events.go", "pkg/gateway/websocket_forward.go"}, []string{"SessionID"}, []string{}, []string{}),
			sentinelLabel: "events.go and websocket_forward.go exist and mention SessionID",
		},
		{
			name:          "Internal channel",
			cmd:           buildGrepFile(repoRoot, "pkg/constants/channels.go", "\"subagent\"", []string{}, []string{}),
			expected:      0,
			sentinel:      buildGrepFile(repoRoot, "pkg/constants/channels.go", "\"cli\"", []string{}, []string{}),
			sentinelLabel: "pkg/constants/channels.go exists and still lists the \"cli\" internal channel",
		},
		{
			// Finding 5: the requirement is "every per-site 'is this a delegate?' boolean is gone"
			// (ADR-091 D10), not "loop_run_turn_tools.go doesn't say depth==0". That file's own
			// gate was already replaced by toolFeedbackReachesUser (steer_boundary.go) — this row
			// widens the search to the whole steering package (pkg/agent/) and narrows the pattern
			// to the banned SHAPE specifically: a depth check combined with `&&`, which is how the
			// deleted `ts.depth == 0 && !ts.opts.IsTaskRun` gate looked. Plain `depth == 0` alone is
			// legitimate structural root-turn lookup used elsewhere in this same package (turn.go's
			// GetActiveTurnHookForSession / resolveSessionIDByChannelChat, both `||`-joined, both
			// about finding the root turnState for cancellation — not gating audience), so banning
			// bare `depth == 0` package-wide would false-positive on code ADR-091 keeps.
			name:          "Per-site booleans",
			cmd:           buildGrep(repoRoot, []string{"pkg/agent/"}, []string{"depth == 0 &&"}, []string{"*.go"}, []string{"*_test.go"}),
			expected:      0,
			sentinel:      pkgGoSentinel([]string{"pkg/agent/"}),
			sentinelLabel: "pkg/agent/*.go (excl _test.go) contains real package declarations",
		},
		{
			name:          "Sibling notifier gone",
			cmd:           buildGrep(repoRoot, []string{"pkg/"}, []string{"notifyParentIfAllSiblingsDone"}, []string{"*.go"}, []string{"*_test.go"}),
			expected:      0,
			sentinel:      pkgGoSentinel([]string{"pkg/"}),
			sentinelLabel: "pkg/**/*.go (excl _test.go) contains real package declarations",
		},
		{
			name:          "Nested replay gone",
			cmd:           buildGrep(repoRoot, []string{"pkg/"}, []string{"emitNestedToolCalls"}, []string{"*.go"}, []string{"*_test.go"}),
			expected:      0,
			sentinel:      pkgGoSentinel([]string{"pkg/"}),
			sentinelLabel: "pkg/**/*.go (excl _test.go) contains real package declarations",
		},
		{
			name:         "FR-047 guard gone",
			cmd:          buildTestNotExists(repoRoot, "src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts"),
			mustDirExist: "src/lib",
		},
		{
			// Finding 1: the pattern below contains `|` alternation, which only means "alternation"
			// under EXTENDED regex syntax (-E). Plain `grep -n` (basic regex) treats `|` as a
			// literal character, so the old check searched for the 30-character literal string
			// "SendResponse:|routingSessionID", never matched anything, and reported a false "clean"
			// no matter what the file contained. buildGrepFileExtended runs `grep -nE` so the
			// alternation is real.
			name:          "Containments gone",
			cmd:           buildGrepFileExtended(repoRoot, "pkg/agent/loop_inbound.go", "SendResponse:|routingSessionID"),
			expected:      0,
			sentinel:      buildGrepFile(repoRoot, "pkg/agent/loop_inbound.go", "^package ", []string{}, []string{}),
			sentinelLabel: "pkg/agent/loop_inbound.go exists and has real content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mustDirExist != "" {
				dir := filepath.Join(repoRoot, tt.mustDirExist)
				info, err := os.Stat(dir)
				if err != nil || !info.IsDir() {
					t.Fatalf(
						"audit mechanism unproven: %q does not exist (or isn't a directory) under "+
							"repo root. This check's whole verdict is \"the file doesn't exist\" — if "+
							"the directory that would hold it is also gone, the check passes no matter "+
							"what, proving nothing (the exact bug 'Address not borrowed' had: a glob "+
							"that matched zero files). err=%v", tt.mustDirExist, err,
					)
				}
			}

			if tt.sentinel != nil {
				sentCount, sentOut := runGrepCmd(t, tt.sentinel)
				if sentCount < 1 {
					t.Fatalf(
						"audit mechanism unproven: sentinel (%s) found 0 matches using the exact "+
							"scope/flags this row uses. A 0-match verdict from the real check below "+
							"would be worthless — the search itself may be broken (wrong directory, "+
							"wrong --include glob, or a regex-dialect mismatch), not proof the banned "+
							"pattern is gone.\nSentinel command: %v\nOutput: %s",
						tt.sentinelLabel, tt.sentinel.Args, sentOut,
					)
				}
			}

			count, out := runGrepCmd(t, tt.cmd)
			if count != tt.expected {
				t.Errorf("Expected %d matches, got %d\nGrep output:\n%s", tt.expected, count, out)
				return
			}

			if tt.expectedSoleFile != "" {
				wantPath := filepath.Join(repoRoot, tt.expectedSoleFile)
				for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
					if line == "" {
						continue
					}
					// grep -n output is "path:lineno:content"; the path is everything before the
					// first ":lineno:" — split on the first colon only, since content may itself
					// contain colons.
					filePath := line
					if idx := strings.Index(line, ":"); idx >= 0 {
						filePath = line[:idx]
					}
					if filePath != wantPath {
						t.Errorf(
							"the sole occurrence must be in %s, but found one in %s — either the "+
								"rejection moved (fine, update expectedSoleFile) or a SECOND, "+
								"unreviewed site now references this banned literal (not fine):\n%s",
							tt.expectedSoleFile, filePath, out,
						)
					}
				}
			}
		})
	}
}

// TestExternalRunner_SingleProductionCaller verifies that runExternalCLISubTurn has exactly one production caller.
func TestExternalRunner_SingleProductionCaller(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// Search for callers of runExternalCLISubTurn, excluding test files and the definition itself
	cmd := buildGrepExcludeFile(repoRoot, []string{"pkg/"}, []string{"runExternalCLISubTurn("}, []string{"*.go"}, []string{"*_test.go", "external_dispatch.go"})

	count, out := runGrepCmd(t, cmd)
	if count != 1 {
		t.Errorf("Expected exactly 1 production caller of runExternalCLISubTurn, got %d\nGrep output:\n%s", count, out)
	}
}

// Helper functions

func findRepoRoot(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("Could not find go.mod file")
		}
		wd = parent
	}
}

// runGrepCmd runs a grep-shaped *exec.Cmd and returns the number of matching lines and the raw
// stdout. grep exit 1 ("no matches") is treated as count 0; any other non-zero exit fails the test
// outright — that means the check itself could not run, not that it observed a clean tree.
func runGrepCmd(t *testing.T, cmd *exec.Cmd) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("Command failed unexpectedly: %v\nStdout: %s\nStderr: %s", err, stdout.String(), stderr.String())
		}
	}

	switch exitCode {
	case 0:
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) > 0 && lines[0] != "" {
			return len(lines), stdout.String()
		}
		return 0, stdout.String()
	case 1:
		return 0, stdout.String()
	default:
		t.Fatalf("Grep command failed with unexpected exit code %d\nStdout: %s\nStderr: %s", exitCode, stdout.String(), stderr.String())
		return 0, ""
	}
}

func buildGrep(repoRoot string, dirs []string, patterns []string, includes []string, excludes []string) *exec.Cmd {
	args := []string{"grep", "-rn"}

	// Add patterns with -e flags
	for _, pattern := range patterns {
		args = append(args, "-e", pattern)
	}

	// Add directories
	for _, dir := range dirs {
		args = append(args, filepath.Join(repoRoot, dir))
	}

	// Add includes
	for _, inc := range includes {
		args = append(args, "--include="+inc)
	}

	// Add excludes
	for _, exc := range excludes {
		args = append(args, "--exclude="+exc)
	}

	return exec.Command("grep", args[1:]...)
}

func buildGrepFile(repoRoot string, filePath string, pattern string, _ []string, _ []string) *exec.Cmd {
	args := []string{"grep", "-n", pattern, filepath.Join(repoRoot, filePath)}
	return exec.Command("grep", args[1:]...)
}

// buildGrepFileExtended is buildGrepFile's extended-regex ("grep -nE") counterpart, for patterns
// that rely on alternation (`|`), which is a literal character under grep's default basic regex
// syntax. See "Containments gone" above.
func buildGrepFileExtended(repoRoot string, filePath string, pattern string) *exec.Cmd {
	return exec.Command("grep", "-nE", pattern, filepath.Join(repoRoot, filePath))
}

func buildGrepFilesScoped(repoRoot string, filePaths []string, patterns []string, _ []string, _ []string) *exec.Cmd {
	args := []string{"grep", "-n"}

	// Add patterns with -e flags
	for _, pattern := range patterns {
		args = append(args, "-e", pattern)
	}

	// Add specific file paths
	for _, file := range filePaths {
		args = append(args, filepath.Join(repoRoot, file))
	}

	return exec.Command("grep", args[1:]...)
}

func buildGrepExcludeFile(repoRoot string, dirs []string, patterns []string, includes []string, excludes []string) *exec.Cmd {
	args := []string{"grep", "-rn"}

	// Add patterns with -e flags
	for _, pattern := range patterns {
		args = append(args, "-e", pattern)
	}

	// Add directories
	for _, dir := range dirs {
		args = append(args, filepath.Join(repoRoot, dir))
	}

	// Add includes
	for _, inc := range includes {
		args = append(args, "--include="+inc)
	}

	// Add excludes
	for _, exc := range excludes {
		args = append(args, "--exclude="+exc)
	}

	return exec.Command("grep", args[1:]...)
}

func buildTestNotExists(repoRoot string, filePath string) *exec.Cmd {
	args := []string{"test", "!", "-e", filepath.Join(repoRoot, filePath)}
	return exec.Command("test", args[1:]...)
}
