package systools

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// compactAgentTools renders real metadata, not the forward-reserved policy
// universe. Reading a permission never grants or executes the corresponding tool.
func compactAgentTools(state *agentstore.State, resolver *tools.ToolPolicyCfg, catalog []tools.Tool, previewKind string) (string, error) {
	ordered := append([]tools.Tool(nil), catalog...)
	for _, tool := range ordered {
		if tool == nil || tool.Name() == "" {
			return "", fmt.Errorf("tool catalog contains missing tool metadata")
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name() < ordered[j].Name() })

	kind := "main"
	if state.Agent.IsWorker() {
		kind = "worker"
	}
	if state.Agent.IsSystem() || coreagent.IsSystemAgentID(coreagent.CoreAgentID(state.Agent.ID)) || state.Agent.ID == string(coreagent.IDOmnipus) {
		kind = "system"
	}
	var out strings.Builder
	if previewKind != "" {
		fmt.Fprintf(&out, "preview: %s | kind: %s\n", previewKind, kind)
	} else {
		name := strings.Join(strings.Fields(state.Agent.Name), " ")
		fmt.Fprintf(&out, "agent: %s | name: %s | kind: %s | revision: %s\n", state.Agent.ID, name, kind, state.Revision)
	}
	for _, tool := range ordered {
		// Share the runtime assignment predicate, but do not filter Deny:
		// the operator needs to see denied tools in this read-only inventory.
		if !tools.MCPAssignmentAllows(tool, resolver) {
			continue
		}
		policy := tools.ResolveEffectivePolicy(resolver, tool.Name())
		fmt.Fprintf(&out, "%s | %s | %s\n", tool.Name(), policy, shortAgentToolDescription(tool.Description()))
	}
	return out.String(), nil
}

// shortAgentToolDescription follows the compact-output sentence and character
// rules. A word that cannot fit is omitted rather than split mid-word.
func shortAgentToolDescription(description string) string {
	text := strings.Join(strings.Fields(description), " ")
	end := len(text)
	for _, boundary := range []string{". ", "! ", "? "} {
		if i := strings.Index(text, boundary); i >= 0 && i+1 < end {
			end = i + 1
		}
	}
	text = text[:end]
	characters := []rune(text)
	if len(characters) <= 80 {
		return text
	}
	cut := 79
	for cut > 0 && !unicode.IsSpace(characters[cut]) {
		cut--
	}
	return strings.TrimSpace(string(characters[:cut])) + "…"
}
