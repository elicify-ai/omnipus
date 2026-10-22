package agent

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// These fixtures exercise the real config -> loop -> graph -> injector path.
// Description is an existing config field, not an AgentInstance field.
type compactRosterAgent struct {
	ID, Name, Description, UseFor string
	ConfigID                      string // optional raw config key before normalization
	Modes                         []string
	Depth                         *int
}

const compactRosterCaller = "compact-roster-caller"
const compactRosterWorkspace = "01JWCOMPACTROSTER000000001"

func compactRosterLoop(t *testing.T, agents []compactRosterAgent, globalCap int) (*AgentLoop, *ContextBuilder) {
	t.Helper()
	edges := make([]graphEdge, 0, len(agents))
	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Performance.MaxDelegationDepth = globalCap
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: compactRosterCaller, Name: "Coordinator"})
	for _, a := range agents {
		configID := a.ID
		if a.ConfigID != "" {
			configID = a.ConfigID
		}
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
			ID: configID, Name: a.Name, Description: a.Description,
		})
		edges = append(edges, edge(compactRosterCaller, a.ID, a.Modes, a.Depth))
	}
	seedWorkspaceGraph(t, compactRosterWorkspace, true, edges)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop with configured descriptions: %v", err)
	}
	t.Cleanup(al.Close)
	return al, compactRosterBuilder(t, al)
}

func compactRosterBuilder(t *testing.T, al *AgentLoop) *ContextBuilder {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(compactRosterCaller)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("caller %q missing ContextBuilder", compactRosterCaller)
	}
	return inst.ContextBuilder
}

func compactRosterBlock(t *testing.T, cb *ContextBuilder, depth int) string {
	t.Helper()
	dynamic := cb.buildDynamicContext(depth, compactRosterWorkspace, "", "", "", "")
	start := strings.Index(dynamic, "## Delegation\n")
	if start < 0 {
		t.Fatalf("dynamic context missing ## Delegation heading: %q", dynamic)
	}
	block := dynamic[start:]
	if end := strings.Index(block[len("## Delegation"):], "\n\n## "); end >= 0 {
		block = block[:len("## Delegation")+end]
	}
	return block
}

func compactDelimitedBlock(t *testing.T, got, tag string) string {
	t.Helper()
	open, closeTag := "<"+tag+">", "</"+tag+">"
	if n := strings.Count(got, open); n != 1 {
		t.Fatalf("%s opening count = %d, want exactly 1; got:\n%s", tag, n, got)
	}
	if n := strings.Count(got, closeTag); n != 1 {
		t.Fatalf("%s closing count = %d, want exactly 1; got:\n%s", tag, n, got)
	}
	start, end := strings.Index(got, open), strings.Index(got, closeTag)
	if end <= start {
		t.Fatalf("%s must close after it opens", tag)
	}
	return got[start : end+len(closeTag)]
}

type compactXMLElement struct {
	XMLName xml.Name `xml:""`
}

type compactRosterEntry struct {
	Name     string              `xml:"name,attr"`
	ID       string              `xml:"id,attr"`
	Text     string              `xml:",chardata"`
	Children []compactXMLElement `xml:",any"`
	Extra    []xml.Attr          `xml:",any,attr"`
}

func compactRosterEntries(t *testing.T, got string) []compactRosterEntry {
	t.Helper()
	directory := compactDelimitedBlock(t, got, "team_directory")
	var parsed struct {
		XMLName xml.Name             `xml:"team_directory"`
		Entries []compactRosterEntry `xml:"agent"`
		Extra   []compactXMLElement  `xml:",any"`
	}
	if err := xml.Unmarshal([]byte(directory), &parsed); err != nil {
		t.Fatalf("team_directory must be valid escaped XML: %v\n%s", err, directory)
	}
	if len(parsed.Extra) != 0 {
		t.Fatalf("unexpected directory child tags: %+v", parsed.Extra)
	}
	seen := make(map[string]bool)
	for _, e := range parsed.Entries {
		if e.Name == "" || e.ID == "" || seen[e.ID] {
			t.Fatalf("entries need unique IDs and nonempty names: %+v", parsed.Entries)
		}
		seen[e.ID] = true
		if len(e.Children) != 0 || len(e.Extra) != 0 {
			t.Fatalf("entry %q has injected child tags or extra attributes: %+v", e.ID, e)
		}
	}
	return parsed.Entries
}

func compactEntryLines(t *testing.T, e compactRosterEntry) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(e.Text), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "Use for: ") || !strings.HasPrefix(lines[1], "Modes: ") {
		t.Fatalf("entry %q must have exactly one Use for line then one Modes line; got %q", e.ID, e.Text)
	}
	if !regexp.MustCompile(`^Modes: (direct/task|direct|task|none); maximum chain depth: [1-9][0-9]*\.$`).MatchString(lines[1]) {
		t.Fatalf("entry %q has noncanonical modes/depth: %q", e.ID, lines[1])
	}
	return lines
}

func compactFindEntry(t *testing.T, got, id string) (compactRosterEntry, bool) {
	t.Helper()
	// Empty/depth-exhausted/fail-closed branches deliberately have no directory.
	if !strings.Contains(got, "<team_directory>") {
		return compactRosterEntry{}, false
	}
	for _, e := range compactRosterEntries(t, got) {
		if e.ID == id {
			return e, true
		}
	}
	return compactRosterEntry{}, false
}

func compactHasEntryID(t *testing.T, got, id string) bool {
	t.Helper()
	_, ok := compactFindEntry(t, got, id)
	return ok
}

func compactHasNamedEntry(t *testing.T, got, name, id string) bool {
	t.Helper()
	e, ok := compactFindEntry(t, got, id)
	return ok && e.Name == name
}

func compactHasMode(t *testing.T, got, id, mode string) bool {
	t.Helper()
	e, ok := compactFindEntry(t, got, id)
	if !ok {
		return false
	}
	line := compactEntryLines(t, e)[1]
	modes, _, found := strings.Cut(strings.TrimPrefix(line, "Modes: "), "; maximum chain depth: ")
	if !found {
		t.Fatalf("entry %q has invalid mode/depth line %q", id, line)
	}
	for _, m := range strings.Split(modes, "/") {
		if m == mode {
			return true
		}
	}
	return false
}

func compactAssertEntry(t *testing.T, got, name, id, useFor, modes string, depthCap int) {
	t.Helper()
	e, ok := compactFindEntry(t, got, id)
	if !ok {
		t.Fatalf("missing compact entry name=%q id=%q; got:\n%s", name, id, got)
	}
	if e.Name != name {
		t.Errorf("entry %q name = %q, want %q", id, e.Name, name)
	}
	lines := compactEntryLines(t, e)
	if want := "Use for: " + useFor; lines[0] != want {
		t.Errorf("entry %q excerpt = %q, want %q", id, lines[0], want)
	}
	if want := fmt.Sprintf("Modes: %s; maximum chain depth: %d.", modes, depthCap); lines[1] != want {
		t.Errorf("entry %q permissions = %q, want %q", id, lines[1], want)
	}
}
