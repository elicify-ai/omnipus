package coreagent

// RED pack (w4 spec §9.1 row 11, boot-panic guard half): the three
// attachment tool names must be in allStaticToolNames — an override key
// absent from that literal panics validateOverrideKeys at boot (§12.1 point
// 3). The spec assigns this oracle to the config ceiling file; Go packages
// cannot span pkg/config and pkg/coreagent, so it lives here next to the
// literal it guards (oracle unchanged, one test per oracle — register R-4).

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestAllStaticToolNamesCarryAttachmentTools(t *testing.T) {
	have := map[string]bool{}
	for _, n := range AllStaticToolNames() {
		have[n] = true
	}
	for _, want := range []string{"list_email_attachments", "read_email_attachment", "download_email_attachment"} {
		if !have[want] {
			t.Fatalf("AllStaticToolNames() lacks %q — an override key absent from the literal panics validateOverrideKeys at boot (spec §12.1 point 3)", want)
		}
	}
}

// The shipped ceiling is complete for the whole static catalog (Hard
// Constraint #6: the reconciled global ceiling IS the default; seed.go's own
// doc states the length identity).
func TestShippedCeilingCoversEveryStaticTool(t *testing.T) {
	names := AllStaticToolNames()
	ceiling := config.DefaultConfig().Sandbox.ToolPolicies
	if len(ceiling) != len(names) {
		t.Fatalf("shipped ceiling has %d entries for %d static tools — the ceiling must be complete for the whole static catalog (Hard Constraint #6); missing = an agent with no explicit entry",
			len(ceiling), len(names))
	}
	for _, n := range names {
		if _, ok := ceiling[n]; !ok {
			t.Fatalf("static tool %q has no shipped ceiling entry (Hard Constraint #6)", n)
		}
	}
}
