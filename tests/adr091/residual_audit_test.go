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
func TestADR091_ResidualAudit(t *testing.T) {
	// Determine repo root by starting from this test's directory and walking up
	repoRoot := findRepoRoot(t)

	tests := []struct {
		name     string
		cmd      *exec.Cmd
		expected int
	}{
		{
			name:     "Ring gone",
			cmd:      buildGrep(repoRoot, []string{"pkg/"}, []string{"newEphemeralSession", "maxEphemeralHistorySize", "ephemeralSessionStore"}, []string{"*.go"}, []string{"*_test.go"}),
			expected: 0,
		},
		{
			name:     "Wait-inline gone",
			cmd:      buildGrep(repoRoot, []string{"pkg/", "contracts/", "src/"}, []string{"executeSync", "DelegationModeAwait", "allow_blocking_question"}, []string{"*.go", "*.yaml", "*.ts", "*.tsx"}, []string{"*_test.go", "*.test.*"}),
			expected: 0,
		},
		{
			name:     "Prompt clean",
			cmd:      buildGrepFile(repoRoot, "pkg/agent/delegation_context.go", "async=false", []string{}, []string{}),
			expected: 0,
		},
		{
			name:     "Address not borrowed",
			cmd:      buildGrep(repoRoot, []string{"pkg/agent/"}, []string{"parentTS.channel", "parentTS.chatID"}, []string{"subturn*.go"}, []string{"*_test.go"}),
			expected: 0,
		},
		{
			name:     "SubTurn config gone",
			cmd:      buildGrep(repoRoot, []string{"pkg/"}, []string{"SubTurn\\."}, []string{"*.go"}, []string{"*_test.go"}),
			expected: 0,
		},
		{
			name:     "One runner caller",
			cmd:      buildGrepExcludeFile(repoRoot, []string{"pkg/"}, []string{"runExternalCLISubTurn("}, []string{"*.go"}, []string{"*_test.go", "external_dispatch.go"}),
			expected: 1,
		},
		{
			name:     "ParentDurableKey gone",
			cmd:      buildGrep(repoRoot, []string{"pkg/", "contracts/"}, []string{"ParentDurableKey"}, []string{"*.go", "*.yaml"}, []string{"*_test.go"}),
			expected: 0,
		},
		{
			name:     "ProducingSessionID gone",
			cmd:      buildGrepFilesScoped(repoRoot, []string{"pkg/agent/events.go", "pkg/gateway/websocket_forward.go"}, []string{"ProducingSessionID"}, []string{}, []string{}),
			expected: 0,
		},
		{
			name:     "Internal channel",
			cmd:      buildGrepFile(repoRoot, "pkg/constants/channels.go", "\"subagent\"", []string{}, []string{}),
			expected: 0,
		},
		{
			name:     "Per-site booleans",
			cmd:      buildGrepFile(repoRoot, "pkg/agent/loop_run_turn_tools.go", "depth == 0", []string{}, []string{}),
			expected: 0,
		},
		{
			name:     "Sibling notifier gone",
			cmd:      buildGrep(repoRoot, []string{"pkg/"}, []string{"notifyParentIfAllSiblingsDone"}, []string{"*.go"}, []string{"*_test.go"}),
			expected: 0,
		},
		{
			name:     "Nested replay gone",
			cmd:      buildGrep(repoRoot, []string{"pkg/"}, []string{"emitNestedToolCalls"}, []string{"*.go"}, []string{"*_test.go"}),
			expected: 0,
		},
		{
			name: "FR-047 guard gone",
			cmd:  buildTestNotExists(repoRoot, "src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts"),
		},
		{
			name:     "Containments gone",
			cmd:      buildGrepFile(repoRoot, "pkg/agent/loop_inbound.go", "SendResponse:|routingSessionID", []string{}, []string{}),
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			tt.cmd.Stdout = &stdout
			tt.cmd.Stderr = &stderr

			err := tt.cmd.Run()
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("Command failed unexpectedly: %v\nStdout: %s\nStderr: %s", err, stdout.String(), stderr.String())
				}
			}

			// For FR-047 (test command), exit code is the verdict
			if strings.Contains(tt.name, "FR-047") {
				if exitCode != 0 {
					t.Errorf("FR-047 guard check failed (file should not exist): exit code %d", exitCode)
				}
				return
			}

			// For grep commands: grep exit 1 means no matches (count = 0), any other exit is a failure
			count := 0
			if exitCode == 0 {
				// Count the number of matching lines
				lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
				if len(lines) > 0 && lines[0] != "" {
					count = len(lines)
				}
			} else if exitCode == 1 {
				// No matches found
				count = 0
			} else {
				t.Fatalf("Grep command failed with unexpected exit code %d\nStdout: %s\nStderr: %s", exitCode, stdout.String(), stderr.String())
			}

			if count != tt.expected {
				t.Errorf("Expected %d matches, got %d\nGrep output:\n%s", tt.expected, count, stdout.String())
			}
		})
	}
}

// TestExternalRunner_SingleProductionCaller verifies that runExternalCLISubTurn has exactly one production caller.
func TestExternalRunner_SingleProductionCaller(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// Search for callers of runExternalCLISubTurn, excluding test files and the definition itself
	cmd := buildGrepExcludeFile(repoRoot, []string{"pkg/"}, []string{"runExternalCLISubTurn("}, []string{"*.go"}, []string{"*_test.go", "external_dispatch.go"})

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

	count := 0
	if exitCode == 0 {
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) > 0 && lines[0] != "" {
			count = len(lines)
		}
	} else if exitCode == 1 {
		count = 0
	} else {
		t.Fatalf("Grep command failed with exit code %d\nStderr: %s", exitCode, stderr.String())
	}

	if count != 1 {
		t.Errorf("Expected exactly 1 production caller of runExternalCLISubTurn, got %d\nGrep output:\n%s", count, stdout.String())
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
