package tools_test

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// #1799: recall_memory searches a ranked, stemmed word index (bleve, "en"
// analyzer, OR of the query words), not a literal substring. The `query`
// parameter text is what the model reads, so it must say what the search does:
// an agent that trusts "literal substring" reads "no match" as "nothing saved".
func recallQueryDescription(t *testing.T) string {
	t.Helper()
	params := tools.NewRecallMemoryTool(nil).Parameters()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters()[properties] has type %T, want map[string]any", params["properties"])
	}
	query, ok := props["query"].(map[string]any)
	if !ok {
		t.Fatalf("properties[query] has type %T, want map[string]any", props["query"])
	}
	desc, ok := query["description"].(string)
	if !ok || desc == "" {
		t.Fatalf("properties[query][description] = %#v, want a non-empty string", query["description"])
	}
	return desc
}

func TestRecallMemoryQueryText_DoesNotPromiseSubstringMatch(t *testing.T) {
	desc := strings.ToLower(recallQueryDescription(t))
	// The old promise was "Literal substring to search for". "substring" is
	// still legal elsewhere (a negation, and the documented index-unavailable
	// fallback), so only the old promise is banned.
	if strings.Contains(desc, "literal substring to search for") {
		t.Errorf("query description still promises a literal substring match: %q", desc)
	}
}

func TestRecallMemoryQueryText_DescribesWordMatch(t *testing.T) {
	desc := recallQueryDescription(t)
	for _, want := range []string{
		"any of the words",      // one shared word is enough to match
		"best match first",      // ranked, not filtered
		"not an exact phrase",   // the old promise, negated
		"does not mean nothing", // an empty result is not proof of absence
		"No regex",              // unchanged: still no regex
		"case-insensitive",      // unchanged
		"Past retrospectives match whole words only", // retrospectives are not stemmed
		"index is unavailable",                       // the substring fallback is disclosed
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("query description %q is missing %q", desc, want)
		}
	}
}
