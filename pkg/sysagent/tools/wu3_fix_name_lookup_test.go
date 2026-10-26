package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CHECK F06: get_agent_tools' name lookup is exact, letter case included. A
// case-only variant of a stored name is not that agent, and two labels that
// differ only in case each resolve to their own agent, not AMBIGUOUS (kills
// CHECK mutant M10, which case-folds the comparison).
func TestWU3GetAgentToolsNameLookupIsCaseExact(t *testing.T) {
	d, cfg := wu3Deps(t)
	wu3CreateAgent(t, d, cfg, "General Purpose fixture")
	upper := wu3CreateAgent(t, d, cfg, "Case Twin")
	lower := wu3CreateAgent(t, d, cfg, "case twin")
	tool := systools.NewAgentGetToolsTool(d)

	wu3Error(t, tool.Execute(context.Background(), map[string]any{"name": "general purpose fixture"}), "AGENT_NOT_FOUND")
	wu3Error(t, tool.Execute(context.Background(), map[string]any{"name": "GENERAL PURPOSE FIXTURE"}), "AGENT_NOT_FOUND")

	for name, id := range map[string]string{"Case Twin": upper, "case twin": lower} {
		// SPEC.md compact output: compare exact compact inventories, not obsolete JSON envelopes.
		byName, _ := defaultToolsExistingBody(t, d, id, map[string]any{"name": name})
		byID, _ := defaultToolsExistingBody(t, d, id, map[string]any{"id": id})
		assert.Equal(t, byID, byName, "%q resolves to its own agent only", name)
	}
}

// silent-failure-hunter SF-08: when an agent record cannot be read, a name
// lookup cannot know whether that record carries the name, so it is refused as
// READ_FAILED naming the unreadable record — never AGENT_NOT_FOUND or a
// claimed unique match. An exact-id read stays available.
func TestWU3GetAgentToolsNameLookupRefusesIncompleteInventory(t *testing.T) {
	d, cfg := wu3Deps(t)
	id := wu3CreateAgent(t, d, cfg, "Readable fixture")
	tool := systools.NewAgentGetToolsTool(d)
	// SPEC.md compact output: the successful read-only control now has plain-text shape.
	defaultToolsExistingBody(t, d, id, map[string]any{"name": "Readable fixture"})

	broken := filepath.Join(d.Home, "entities", "agents", "broken-record.json")
	require.NoError(t, os.WriteFile(broken, []byte("{not json"), 0o600))

	for _, name := range []string{"Readable fixture", "Some other name"} {
		body := wu3Error(t, tool.Execute(context.Background(), map[string]any{"name": name}), "READ_FAILED")
		assert.Contains(t, body["message"], "broken-record", "the unreadable record is named")
	}
	defaultToolsExistingBody(t, d, id, map[string]any{"id": id})
}
