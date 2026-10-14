//go:build windows

package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

// Oracle: chief F01/F02. Windows executability requires an extension permitted
// by PATHEXT; an existing plain .txt file is not enough. No LookPath precondition
// can abort the negative fixture before the model-facing product result.
func TestL11274WindowsRejectsCLIPathOutsidePATHEXT(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
			path := l11274Executable(t, f.home, cli, false)
			if filepath.Ext(path) != ".txt" {
				t.Fatalf("SETUP: plain-data fixture has unexpected extension %q", filepath.Ext(path))
			}
			before := l11274Fix1ReadResources(t, f)
			result := f.execute(t, "create_agent", l11274CreateArgs("Windows rejected worker", cli, path))
			message := l11274FailureDetails(t, result, "INVALID_INPUT")
			if !strings.Contains(message, "cli_path") || !l11274ReasonNamesPath(message, path) {
				t.Errorf("Windows refusal must identify cli_path %q, got %q", path, message)
			}
			lower := strings.ToLower(message)
			if !strings.Contains(lower, "extension") && !strings.Contains(lower, "pathext") {
				t.Errorf("Windows refusal must explain the executable-extension fault, got %q", message)
			}
			l11274Fix1AssertNoWrites(t, f, "windows-rejected-worker", before)
		})
	}
}
