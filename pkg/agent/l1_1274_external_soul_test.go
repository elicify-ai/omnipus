package agent

import (
	"strings"
	"testing"
)

// Chief clarification Q1 B, 2026-10-14: an external worker has no native soul.
// That field stays unsupported; the fix must not merely bypass role validation
// to accommodate create_agent's erroneous unconditional required field.
func TestL11274ExternalSoulRefusesWithModelVisibleUnsupportedFieldReason(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			path := l11274Executable(t, f.home, cli, true)
			args := l11274CreateArgs("Unsupported soul", cli, path)
			args["soul"] = l11274Soul
			result := f.execute(t, "create_agent", args)
			message := l11274FailureDetails(t, result, "INVALID_INPUT")
			if !strings.Contains(message, "soul") || !strings.Contains(message, "unsupported") {
				t.Errorf("external soul refusal must identify the unsupported field for the model, got %q", message)
			}
			l11274AssertAbsent(t, f, "unsupported-soul")
		})
	}
}
