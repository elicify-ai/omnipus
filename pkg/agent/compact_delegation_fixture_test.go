package agent

import (
	"encoding/base64"
	"testing"
	"unicode/utf8"
)

// These ten names/IDs come from the approved team-directory comparison row.
// Descriptions are deterministic test fixtures with a known explicit cue plus
// an intro and an extra sentence. They are NOT presented as live agent config.
// Both old and new measurements use the SAME ten targets, nil/all modes,
// inherited edge depths, global cap 3 and current depth 0. The original
// renderer never included descriptions; its baseline must not be inflated by
// adding those descriptions to the old text by hand.
func compactTenAgents() []compactRosterAgent {
	return []compactRosterAgent{
		{
			ID: "11bf73e8-4a2d-4130-b6da-57f6cd37156c", Name: "Aisha-Marketing-Lead",
			Description: "Marketing coordinator. Use for: marketing launches. Internal coordination detail stays out.",
			UseFor:      "marketing launches.",
		},
		{
			ID: "a005faed-e268-43db-b059-c186d1999a71", Name: "Beatrice-Legal-Lead",
			Description: "Legal coordinator. Use for: contract reviews. Internal coordination detail stays out.",
			UseFor:      "contract reviews.",
		},
		{
			ID: "00eb3b87-61ac-4d4c-a291-921d925ad812", Name: "Dmitri-Sales-Lead",
			Description: "Sales coordinator. Use for: sales handoffs. Internal coordination detail stays out.",
			UseFor:      "sales handoffs.",
		},
		{
			ID: "8f69323c-cc6c-442c-8986-9bda5413f829", Name: "Grace-People-Lead",
			Description: "People coordinator. Use for: hiring requests. Internal coordination detail stays out.",
			UseFor:      "hiring requests.",
		},
		{
			ID: "8098d1dd-47ef-455b-b747-942aca970e4f", Name: "Ingrid-Engineering-Lead",
			Description: "Engineering coordinator. Use for: engineering decisions. Internal coordination detail stays out.",
			UseFor:      "engineering decisions.",
		},
		{
			ID: "a5e36bb7-ee41-46bb-bc52-cc343b8a26b4", Name: "Mara-Studio-Lead",
			Description: "Studio coordinator. Use for: creative production. Internal coordination detail stays out.",
			UseFor:      "creative production.",
		},
		{
			ID: "cc3428cf-d3cd-4d7b-81b9-5327b29b31e4", Name: "Petra-Librarian-Lead",
			Description: "Library coordinator. Use for: knowledge curation. Internal coordination detail stays out.",
			UseFor:      "knowledge curation.",
		},
		{
			ID: "bf705564-bd3c-4887-a509-fc0b345be125", Name: "Renata-Project-Lead",
			Description: "Project coordinator. Use for: venture planning. Internal coordination detail stays out.",
			UseFor:      "venture planning.",
		},
		{
			ID: "159624b7-0228-4e82-850f-3609482de982", Name: "Selin-Fundraising-Lead",
			Description: "Fundraising coordinator. Use for: investor requests. Internal coordination detail stays out.",
			UseFor:      "investor requests.",
		},
		{
			ID: "a97c1a8f-2e4e-498f-a88f-b195c707df9e", Name: "Wei-Lin-Finance-Lead",
			Description: "Finance coordinator. Use for: runway checks. Internal coordination detail stays out.",
			UseFor:      "runway checks.",
		},
	}
}

// Historical measurement only, NOT a correctness oracle or a size gate.
// Captured from original e59e3d54205e1ee5813b3bdbb3063d9ff4f4fd61 via this
// real loop/injector fixture before any production edits: 4,705 Unicode
// characters (utf8.RuneCountInString), 4,869 UTF-8 bytes. Exact output SHA-256:
// b36a472602fb1bd1a9aa7d341e0bc059223dabf851f6ef1d7d1e588f1b316569.
// Output and ten-agent metadata are preserved in:
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/compact-roster-20261021/qa-ten-agent-baseline.txt
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/compact-roster-20261021/qa-ten-agent-fixture.json
const compactTenBaselineCharacters = 4705
const compactTenBaselineBytes = 4869

func testCompactTenAgentMeasurement(t *testing.T) {
	agents := compactTenAgents()
	_, cb := compactRosterLoop(t, agents, 3)
	got := compactRosterBlock(t, cb, 0)
	// The initial RED run captures the actual pre-change output, not a second
	// hand-copied renderer. Base64 makes its exact UTF-8 bytes recoverable from
	// the saved test log without log indentation or newline ambiguities.
	t.Logf("TEN_AGENT_RENDER_BASE64=%s", base64.StdEncoding.EncodeToString([]byte(got)))
	t.Logf("ten_agent_render characters=%d bytes=%d targets=%d current_depth=0 global_cap=3 modes=direct/task",
		utf8.RuneCountInString(got), len(got), len(agents))
	t.Logf("ten_agent_comparison baseline_characters=%d current_characters=%d baseline_bytes=%d current_bytes=%d (measurement only; no size target)",
		compactTenBaselineCharacters, utf8.RuneCountInString(got), compactTenBaselineBytes, len(got))
	entries := compactRosterEntries(t, got)
	if len(entries) != 10 {
		t.Fatalf("measurement fixture changed its ten-agent population: got %d entries", len(entries))
	}
	for _, a := range agents {
		compactAssertEntry(t, got, a.Name, a.ID, a.UseFor, "direct/task", 3)
	}
	compactAssertSharedSyntax(t, got)
}
