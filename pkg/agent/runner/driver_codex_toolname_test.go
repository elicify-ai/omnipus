package runner

import "testing"

// TestCodexCommandToolName proves the codex tool_call ToolName is derived to a
// stable, short identifier (the invoked binary) rather than the full shell
// command, so audit logs stay compact and policy-by-tool-name matching is
// meaningful. The full command is preserved by the caller in RawInput.
func TestCodexCommandToolName(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{"simple binary", "ls", "ls"},
		{"binary with args", "ls -la /tmp", "ls"},
		{"absolute path stripped to basename", "/usr/bin/python script.py", "python"},
		{"env-assignment prefix skipped", "FOO=bar BAZ=qux git status", "git"},
		{"empty command falls back to shell", "", "shell"},
		{"whitespace-only falls back to shell", "   \t ", "shell"},
		{"long command does not bloat the name", "bash -c 'while true; do echo hi; done'", "bash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexCommandToolName(tt.command); got != tt.want {
				t.Errorf("codexCommandToolName(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}
