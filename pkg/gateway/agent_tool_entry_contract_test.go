package gateway

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/gateway/inboundschemas"
)

// TestContractDescription_NamesOnlyLiveTools pins ADR-071 D1 / spec FR-014
// (W-D1 test 11): the published AgentToolEntry.manifest_tier description —
// the wire-level documentation of the "infra" exposure level a client reads
// to understand the manifest mechanism — must name the renamed discovery
// capability ("ToolSearch") and must not name either retired predecessor:
// the pre-rename "load_tool", or the doubly-stale "search_tools_*" pair
// (search_tools_bm25/search_tools_regex) that were already merged into the
// unified tool well before this rename and should never have survived in
// the description text. The embedded copy under
// pkg/gateway/inboundschemas/ is the SAME artifact the generated
// TypeScript/zod and Go types are built from (Constraint #8's contract-first
// pipeline via scripts/gen-contracts.sh), so asserting against it transitively
// pins every generated consumer.
func TestContractDescription_NamesOnlyLiveTools(t *testing.T) {
	raw, err := inboundschemas.FS.ReadFile("AgentToolEntry.yaml")
	if err != nil {
		t.Fatalf("read embedded contract schema: %v", err)
	}
	doc := string(raw)

	if !strings.Contains(doc, "ToolSearch") {
		t.Error("AgentToolEntry.yaml's manifest_tier description must name the live capability \"ToolSearch\"")
	}
	if strings.Contains(doc, "load_tool") {
		t.Error("AgentToolEntry.yaml must not name the retired \"load_tool\" capability")
	}
	if strings.Contains(doc, "search_tools_") {
		t.Error("AgentToolEntry.yaml must not name the already-retired \"search_tools_*\" predecessors")
	}
}
