package thirdpartycli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/stretchr/testify/assert"
)

// Founder Q48 A (2026-11-09), wide R5: every check reason keeps program and
// classification, never the checked pathname or operating-system diagnostics.
// Identifiers are the public check classes; no expected prose comes from a run.
func TestR8_ProgramRefusalTextKeepsClassWithoutDiagnostics_Q48(t *testing.T) {
	checks := []string{
		"not_absolute", "not_found", "not_regular_file", "not_executable",
		"agent_writable_location", "location_unverifiable", "windows_extension",
		"windows_short_name", "windows_stream", "too_long", "contains_nul", "contains_line_break",
	}
	for _, cli := range []struct{ id, name string }{
		{"claude-code", "Claude Code"}, {"codex", "Codex"}, {"opencode", "OpenCode"},
	} {
		for _, check := range checks {
			t.Run(cli.id+"/"+check, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "r8-private-folder-marker")
				at := filepath.Join(dir, "r8-private-program-marker")
				text := thirdpartycli.CheckFailedText(cli.id, check, at)
				assert.Contains(t, text, cli.name, "Q48: refusal still identifies the program")
				assert.Regexp(t, `\b`+check+`\b`, text, "Q48: refusal still identifies the exact check class")
				for _, private := range []string{at, dir, "r8-private-folder-marker", "r8-private-program-marker"} {
					assert.NotContains(t, text, private, "Q48: client refusal must not carry checked-file diagnostics")
				}
				for _, diagnostic := range []string{"permission denied", "no such file", "not a directory", "fork/exec"} {
					assert.NotContains(t, strings.ToLower(text), diagnostic, "Q48: OS diagnostics belong only in the server log")
				}
			})
		}
	}
}
