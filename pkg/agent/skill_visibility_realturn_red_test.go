//go:build goolm && stdjson

package agent

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), Part L.
// TestManifest_LevelsShapeTurn_RealTurnReadsTheLevelAuthority: the real-turn
// twin of TestManifest_LevelsShapeTurn (skill_visibility_manifest_red_test.go).
//
// Oracle: skills-tools-redesign-spec.md BDD S-38 (what each level sends, with
// its literal "sent" column), S-40 ("Deny wins": the tool "is not in Mia's tool
// list and a ToolSearch for it finds nothing"), C-1 (the level table, and "An
// effective Deny for an agent overrides every level for that agent: the item is
// not listed, not found and not callable"), C-15 (one visibility authority:
// manifest building and search membership read the stored levels through one
// function, and a level takes effect from the next turn) and Dataset D2 rows
// 1–6. D2 rows 7–10 are covered where TestManifest_LevelsShapeTurn says they are.
//
// Why a twin. The seam test asks the bound authority (tools.ToolSurface.Plan,
// Ruling A) what it plans for one tool at one level. It cannot show that the
// turn asks it: a turn that keeps its own copy of the rules, or still reads the
// fixed tier lists the stored levels replace, passes the seam test and sends
// Mia the wrong request. This test runs the REAL loop and observes what the
// model is sent and what ToolSearch does with it.
//
// What runs.
//   - One real loop: a bare config and Mia from the grant fixture, the same
//     construction TestVisibilityTransitions uses.
//   - The levels are stored the way a person's settings change stores them:
//     literal JSON for sandbox.tool_visibility, made live through the loop's
//     own reload (tests/helpers/sandboxkeys). A configuration that does not carry
//     the key stops the test with BLOCKED, which is what the landing base does.
//   - Nine stub tools are registered on Mia's instance AFTER that reload (the
//     reload rebuilds the instance): a Full tool, a name_and_description tool with
//     a 300-character two-line description, one with an empty description, a
//     name_only tool, a Hidden tool, and one tool at each of the four levels whose
//     denial is Mia's OWN value (S-40's Given: "Mia's own value Deny", the global
//     value allow). Every stub is allowed globally, so a Deny is the only thing that
//     can remove one.
//   - Four new chats, each "a new chat with nothing loaded": a plain turn; a turn
//     in which the model asks ToolSearch for the Hidden tool and the four denied
//     tools by keyword; a turn in which it loads the three listed tools by name;
//     and a turn in which it calls the denied tools and one allowed tool.
//   - Two real built-in tools of the pre-conversion Full set, stored BELOW Full:
//     read_file at Hidden (S-92 and D2#9 name it) and list_directory at name_only. A
//     stub that no fixed tier list names cannot tell a turn (or a ToolSearch) that
//     reads the stored levels from one that still reads the fixed lists: both call it
//     lazy. A built-in the old lists send every turn can. (The stub stored at Full
//     cannot: the loop's ToolSearch resolver reads the levels the turn reads, so a Full
//     tool never reaches an answer whichever corpus ranked it. Observed at ca15ddc53,
//     where its row passes: that row is the spec's C-15 membership rule, not a detector
//     of the old reader.) The second chat also asks ToolSearch for "read file" (S-92's
//     own query), the third also loads list_directory by name.
//   - No kernel sandbox is enforcing (macOS, Windows, permissive mode, a failed
//     sandbox start): the gateway then registers no sandbox turn-policy home, which is
//     the state this test sets. ToolSearch must still read the levels the turn reads
//     (C-15: one visibility authority), with or without a kernel sandbox.
//
// Rows (all oracles are the spec's, none was read off the code):
//   - D2#1 / S-38 Full: the definition (with its description) is in the tool list.
//   - D2#2 / S-38, C-1: one line for the 300-character description, its first line
//     cut to 140 characters (nothing at character 141 on, nothing from line two),
//     and no definition until a ToolSearch load.
//   - D2#3 / S-38: an empty description lists the name alone.
//   - D2#4 / S-38: name_only lists the name alone, no description.
//   - D2#5 / S-38: Hidden is listed nowhere; a keyword ToolSearch finds it and
//     loads it (its definition is in the next request of that chat).
//   - D2#6 / S-40, C-1: a Deny wins at EVERY level (not only Full): the tool is
//     not in the tool list, not listed, not found by ToolSearch, and a call to it
//     does not run it.
//   - C-15 search membership: ToolSearch finds every tool whose level is not Full, so
//     a keyword search does not return the tool stored at Full (its definition is
//     already in the tool list).
//   - D2#9 / S-92 / D11#2: read_file (old Full) set to Hidden is not in a new chat's
//     first request, and ToolSearch with query "read file" returns it.
//   - D2#4 / S-38 for a built-in: list_directory (old Full) set to name_only is not in
//     the first request and a ToolSearch load by name loads it.
//   - C-15 agreement: for all nine tools the real turn does what the bound
//     authority says — in the request, in the listing (its line, byte for byte) and
//     in ToolSearch. This is an agreement check, not an oracle: the rows above carry
//     the spec's answers; this one pins that the turn and the authority cannot
//     disagree.
//
// Two instruments keep the absences honest. Every "not listed / not found" row
// first shows the same state does list an allowed tool (the allowed Full tool is
// in the request, the name_only tool is in the listing, and the keyword search
// found the allowed Hidden tool), and the "not callable" row shows an allowed tool
// called in the same turn did run.
//
// What is not asserted (UNVERIFIED, named so a green is not read wider): the
// separator between a tool's name and its description in the listing, and the
// listing's category headings — the spec fixes neither, so a line is read after
// removing its list bullet; the exact refusal text of a call to a denied tool,
// only that it did not run; the listing of the two built-ins — the system prompt
// itself names read_file and list_directory in a sentence about the working
// directory (pkg/agent/context.go), so a text scan cannot tell a listing from that
// sentence; their rows read the tool list (definitions) and ToolSearch, and the
// listing is read on the nine stubs, whose names occur nowhere else; whether the
// "read file" search also loads read_file (S-92 says "returns", nothing more).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/elicify-ai/omnipus/tests/helpers/sandboxkeys"
)

const (
	lvtAgent        = "mia"
	lvtFull         = "zq_full_probe"
	lvtNadLong      = "zq_nad_long_probe"
	lvtNadEmpty     = "zq_nad_empty_probe"
	lvtNameOnly     = "zq_name_only_probe"
	lvtHidden       = "zq_hidden_probe"
	lvtDenyFull     = "zq_deny_full_probe"
	lvtDenyNad      = "zq_deny_nad_probe"
	lvtDenyNameOnly = "zq_deny_name_only_probe"
	lvtDenyHidden   = "zq_deny_hidden_probe"

	// lvtSecondLineMarker sits on the SECOND line of the long description: C-1
	// lists "the first line of its description", so it must never reach the line.
	lvtSecondLineMarker = "SECOND-LINE-MARKER-4Q"

	// Two real built-ins of the pre-conversion Full set, stored below Full. S-92:
	// "Given read_file at Full ... When the person sets it to Hidden ... Then a new
	// chat's first request does not list read_file, and ToolSearch with query
	// "read file" returns read_file." list_directory is a second tool of that set,
	// stored at name_only (D2#4: a ToolSearch load brings its definition).
	lvtDemotedHidden   = "read_file"
	lvtDemotedNameOnly = "list_directory"
	lvtDemotedQuery    = "read file"

	lvtSearchCallID        = "lvt-search"
	lvtDemotedSearchCallID = "lvt-demoted-search"
	lvtLoadCallID          = "lvt-load"
	lvtCallIDPrefix        = "lvt-call-"
)

// lvtLongDescription is D2 row 2's "description 300 chars": the seam test's
// one-line boundary text (140 filler characters, the cap-overflow marker, a
// trailing word — 171 characters) plus a second line that pads the whole to
// exactly 300 characters.
var lvtLongDescription = longDescription + "\n" + lvtSecondLineMarker +
	strings.Repeat("y", 300-len(longDescription)-1-len(lvtSecondLineMarker))

// lvtStub is one stub tool and how the turns treat it.
type lvtStub struct {
	name        string
	level       string // the stored level (C-6 vocabulary)
	description string
	denied      bool   // Mia's own value is deny (S-40's Given); the global value is allow
	via         string // how a turn loads it through ToolSearch: "names", "query" or "" (not loaded)
	word        string // a keyword only this tool's description carries; every stub's word is in the one keyword search
}

// lvtStubs are the nine tools. The keywords occur nowhere else in the repository,
// so a query made of them can only match these descriptions. The Full tool's word
// is searched too: C-15 says a tool at Full is not in the search corpus, so the
// search must not return it.
var lvtStubs = []lvtStub{
	{name: lvtFull, level: "full", description: "Calibrate the quillfeather instrument.", word: "quillfeather"},
	{name: lvtNadLong, level: "name_and_description", description: lvtLongDescription, via: "names"},
	{name: lvtNadEmpty, level: "name_and_description", description: "", via: "names"},
	{name: lvtNameOnly, level: "name_only", description: "Calibrate the thistlemoth instrument.", via: "names"},
	{name: lvtHidden, level: "hidden", description: "Calibrate the larkspurian instrument.", via: "query", word: "larkspurian"},
	{name: lvtDenyFull, level: "full", description: "Calibrate the ferrulation instrument.", denied: true, via: "query", word: "ferrulation"},
	{name: lvtDenyNad, level: "name_and_description", description: "Calibrate the gantryline instrument.", denied: true, via: "query", word: "gantryline"},
	{name: lvtDenyNameOnly, level: "name_only", description: "Calibrate the halyardine instrument.", denied: true, via: "query", word: "halyardine"},
	{name: lvtDenyHidden, level: "hidden", description: "Calibrate the isinglassic instrument.", denied: true, via: "query", word: "isinglassic"},
}

func lvtStubNamed(t *testing.T, name string) lvtStub {
	t.Helper()
	for _, s := range lvtStubs {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("test defect: no stub named %q", name)
	return lvtStub{}
}

// lvtStubTool is the tool registered for a stub. Execute only counts the call:
// a denied tool that is "not callable" never reaches it.
type lvtStubTool struct {
	tools.BaseTool
	name        string
	description string
	calls       atomic.Int32
}

func (s *lvtStubTool) Name() string        { return s.name }
func (s *lvtStubTool) Description() string { return s.description }
func (s *lvtStubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s *lvtStubTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (s *lvtStubTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	s.calls.Add(1)
	return &tools.ToolResult{ForLLM: "stub " + s.name + " ran"}
}

// lvtRequest is one request the loop sent the provider.
type lvtRequest struct {
	msgs []providers.Message
	defs []providers.ToolDefinition
}

// lvtTurn is the requests of one turn, in order (a tool round makes a second one).
type lvtTurn struct{ requests []lvtRequest }

// lvtProvider records every request and answers the scripted responses first,
// then "done".
type lvtProvider struct {
	mu    sync.Mutex
	queue []*providers.LLMResponse
	reqs  []lvtRequest
}

func (p *lvtProvider) Chat(
	_ context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, lvtRequest{
		msgs: append([]providers.Message(nil), messages...),
		defs: append([]providers.ToolDefinition(nil), toolDefs...),
	})
	if len(p.queue) > 0 {
		next := p.queue[0]
		p.queue = p.queue[1:]
		return next, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *lvtProvider) GetDefaultModel() string { return "lvt-test-model" }

// lvtWorld is the real loop with the nine stubs registered and their levels stored.
type lvtWorld struct {
	t     *testing.T
	al    *AgentLoop
	inst  *AgentInstance
	prov  *lvtProvider
	stubs map[string]*lvtStubTool
}

func lvtJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

// newLvtWorld builds the loop, stores the levels through the loop's own reload
// and only then registers the stubs. A configuration that does not carry
// sandbox.tool_visibility ends the test here with BLOCKED, never a skip.
func newLvtWorld(t *testing.T) *lvtWorld {
	t.Helper()
	// No kernel sandbox is enforcing (macOS, Windows, permissive mode, a failed
	// sandbox start): no sandbox turn-policy home is registered, and only the loop
	// knows where the stored levels are. A home another test of this package left
	// registered would hide a ToolSearch that reads the levels from it instead of
	// from the turn, so the registration is cleared here and put back afterwards.
	if prev, registered := sandbox.RegisteredTurnPolicyBase(); registered {
		t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(&prev) })
	} else {
		t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	}
	sandbox.RegisterTurnPolicyBase(nil)

	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "skills"), 0o700))
	t.Setenv(config.EnvHome, tmp)
	t.Setenv(config.EnvConfig, filepath.Join(tmp, "config.json"))

	// Mia's own values, decoded from literal JSON so the record is exactly what a
	// person's saved agent looks like (S-40: "Mia's own value Deny").
	own := map[string]string{}
	for _, s := range lvtStubs {
		if s.denied {
			own[s.name] = "deny"
		}
	}
	var agentCfg config.AgentConfig
	require.NoError(t, json.Unmarshal(
		[]byte(lvtJSON(t, map[string]any{"tools": map[string]any{"builtin": map[string]any{"policies": own}}})),
		&agentCfg), "decode Mia's own tool values")
	agentCfg.ID = lvtAgent
	agentCfg.Home = tmp
	persisted := persistSkillGrantFixture(t, tmp, agentCfg)
	require.NotNil(t, persisted.Tools, "the saved agent keeps its own tool values")
	for name := range own {
		require.Equal(t, config.ToolPolicyDeny, persisted.Tools.Builtin.Policies[name],
			"fixture: Mia's own value for %s is Deny", name)
	}

	provider := &lvtProvider{}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "lvt-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
			List: []config.AgentConfig{persisted},
		},
	}
	// Every stub, and the two built-ins whose level is lowered, is allowed globally:
	// a Deny can only be Mia's own, and a level never grants or removes a permission.
	cfg.Sandbox.ToolPolicies = map[string]string{
		"read_file": "allow", "write_file": "allow", "Skill": "allow", lvtDemotedNameOnly: "allow",
	}
	for _, s := range lvtStubs {
		cfg.Sandbox.ToolPolicies[s.name] = "allow"
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)

	levels := make(map[string]string, len(lvtStubs)+2)
	for _, s := range lvtStubs {
		levels[s.name] = s.level
	}
	levels[lvtDemotedHidden] = "hidden"
	levels[lvtDemotedNameOnly] = "name_only"
	sandboxkeys.Store(t, al.GetConfig, reloadPublisher(al, provider),
		map[string]any{"tool_visibility": levels},
		"C-1/C-15, S-38, S-40, Dataset D2: sandbox.tool_visibility is the stored level the next turn's request, listing and ToolSearch read")

	inst, ok := al.GetRegistry().GetAgent(lvtAgent)
	require.True(t, ok, "the loop has the agent %q after the reload", lvtAgent)
	stubs := make(map[string]*lvtStubTool, len(lvtStubs))
	for _, s := range lvtStubs {
		stub := &lvtStubTool{name: s.name, description: s.description}
		inst.Tools.Register(stub)
		got, found := inst.Tools.Get(s.name)
		require.True(t, found, "fixture: %s is registered on Mia's instance", s.name)
		require.Same(t, stub, got, "fixture: the registry holds the stub for %s", s.name)
		stubs[s.name] = stub
	}
	return &lvtWorld{t: t, al: al, inst: inst, prov: provider, stubs: stubs}
}

// turn runs one real turn in a NEW chat (a session key used once) and returns the
// requests it made. script is the model's first responses; after them it says
// "done".
func (w *lvtWorld) turn(session string, script ...*providers.LLMResponse) lvtTurn {
	w.t.Helper()
	w.prov.mu.Lock()
	w.prov.queue = script
	before := len(w.prov.reqs)
	w.prov.mu.Unlock()

	_, err := w.al.runAgentLoop(context.Background(), w.inst, processOptions{
		SessionKey: session, Channel: "cli", ChatID: "direct",
		UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
	})
	require.NoError(w.t, err)

	w.prov.mu.Lock()
	defer w.prov.mu.Unlock()
	out := lvtTurn{requests: append([]lvtRequest(nil), w.prov.reqs[before:]...)}
	require.NotEmpty(w.t, out.requests, "the turn must have sent a request")
	return out
}

// lvtTurns are the four real turns the rows read.
type lvtTurns struct {
	shape  lvtTurn // a plain turn: what a new chat is sent
	search lvtTurn // the model asks ToolSearch for the stubs by keyword, and for "read file" in a second call
	load   lvtTurn // the model loads the three listed stubs and list_directory by name
	call   lvtTurn // the model calls the denied tools, and the allowed Full tool as a control
}

func (w *lvtWorld) runTurns() lvtTurns {
	w.t.Helper()
	var words, listed []string
	for _, s := range lvtStubs {
		if s.word != "" {
			words = append(words, s.word)
		}
		if s.via == "names" {
			listed = append(listed, s.name)
		}
	}
	// list_directory is loaded by name in the same call as the three stubs; read_file
	// is searched in its own call, with S-92's own words, so one search cannot answer
	// for the other.
	listed = append(listed, lvtDemotedNameOnly)
	calls := []providers.ToolCall{scriptedToolCall(lvtCallIDPrefix+lvtFull, lvtFull, "{}")}
	for _, s := range lvtStubs {
		if s.denied {
			calls = append(calls, scriptedToolCall(lvtCallIDPrefix+s.name, s.name, "{}"))
		}
	}
	search := scriptedToolCall(lvtSearchCallID, "ToolSearch", lvtJSON(w.t, map[string]any{"query": strings.Join(words, " ")}))
	demotedSearch := scriptedToolCall(lvtDemotedSearchCallID, "ToolSearch", lvtJSON(w.t, map[string]any{"query": lvtDemotedQuery}))
	load := scriptedToolCall(lvtLoadCallID, "ToolSearch", lvtJSON(w.t, map[string]any{"names": listed}))
	return lvtTurns{
		shape:  w.turn("lvt-chat-shape"),
		search: w.turn("lvt-chat-search", &providers.LLMResponse{ToolCalls: []providers.ToolCall{search, demotedSearch}}),
		load:   w.turn("lvt-chat-load", &providers.LLMResponse{ToolCalls: []providers.ToolCall{load}}),
		call:   w.turn("lvt-chat-call", &providers.LLMResponse{ToolCalls: calls}),
	}
}

// ── what a request carries ──────────────────────────────────────────────────

func lvtHasDef(defs []providers.ToolDefinition, name string) bool {
	for _, d := range defs {
		if d.Function.Name == name {
			return true
		}
	}
	return false
}

// lvtRequestText is every word of the messages the model is sent. The system
// message carries its text twice — Content is the concatenation of SystemParts
// (pkg/agent/context.go: "SystemParts carries the same content as structured
// blocks") — so a message's text is read once: its Content, or, for a message
// that has no Content, its structured parts. Reading both would list every line
// of the system prompt twice and break the "listed as ONE line" count.
func lvtRequestText(msgs []providers.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Content != "" {
			b.WriteString(m.Content)
			b.WriteString("\n")
			continue
		}
		for _, part := range m.SystemParts {
			b.WriteString(part.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func lvtIsNameRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// lvtHasToken reports whether line holds name as a whole word (a longer tool name
// that merely contains it does not count).
func lvtHasToken(line, name string) bool {
	for from := 0; from < len(line); {
		at := strings.Index(line[from:], name)
		if at < 0 {
			return false
		}
		start, end := from+at, from+at+len(name)
		before, _ := utf8.DecodeLastRuneInString(line[:start])
		after, _ := utf8.DecodeRuneInString(line[end:])
		if (start == 0 || !lvtIsNameRune(before)) && (end == len(line) || !lvtIsNameRune(after)) {
			return true
		}
		from = start + 1
	}
	return false
}

// lvtStripBullet removes a list bullet and surrounding space from a listing line.
func lvtStripBullet(line string) string {
	line = strings.TrimSpace(line)
	for _, bullet := range []string{"- ", "* ", "• "} {
		if strings.HasPrefix(line, bullet) {
			return strings.TrimSpace(strings.TrimPrefix(line, bullet))
		}
	}
	return line
}

// lvtListedLine returns the first line of text that names the tool, with its list
// bullet removed, and how many lines name it.
func lvtListedLine(text, name string) (string, int) {
	var first string
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if !lvtHasToken(line, name) {
			continue
		}
		if count == 0 {
			first = lvtStripBullet(line)
		}
		count++
	}
	return first, count
}

// lvtLoadedAfter reports whether the turn's ToolSearch call put the tool's
// definition into the tool list: absent from the first request, present in the
// request after the call.
func lvtLoadedAfter(turn lvtTurn, name string) bool {
	return len(turn.requests) >= 2 &&
		!lvtHasDef(turn.requests[0].defs, name) &&
		lvtHasDef(turn.requests[1].defs, name)
}

func lvtRequireLoadedAfter(t *testing.T, turn lvtTurn, name, how string) {
	t.Helper()
	require.GreaterOrEqual(t, len(turn.requests), 2, "the model's ToolSearch call must be followed by a second request in the same chat")
	require.False(t, lvtHasDef(turn.requests[0].defs, name), "before the load the definition is not in the tool list")
	require.True(t, lvtHasDef(turn.requests[1].defs, name),
		"S-38: after the ToolSearch load %s the definition is in the tool list (\"no definition until a ToolSearch load\")", how)
}

// lvtSearchMatches returns the names a ToolSearch query answer lists as matches:
// the answer is one summary line, then a JSON object whose "matches" array holds
// {"name", "description"} entries (any skill lines follow it). An answer with no such
// object — "No tools found" — lists none. The rows that assert a tool is FOUND read
// this list, so a tool whose name merely occurs in another match's description
// does not count as found; the rows that assert a tool is NOT found read the whole
// answer.
func lvtSearchMatches(t *testing.T, answer string) []string {
	t.Helper()
	at := strings.Index(answer, "\n{")
	if at < 0 {
		return nil
	}
	var doc struct {
		Matches []struct {
			Name string `json:"name"`
		} `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(strings.NewReader(answer[at+1:])).Decode(&doc),
		"the matches object of the ToolSearch answer decodes; the answer was:\n%s", answer)
	names := make([]string, 0, len(doc.Matches))
	for _, m := range doc.Matches {
		names = append(names, m.Name)
	}
	return names
}

// lvtObservedPlan is what the real turns did for one stub, in the authority's
// own terms (levelTurnPlan): is its definition in the request, which line lists
// it, and did ToolSearch find and load it.
func lvtObservedPlan(s lvtStub, tn lvtTurns) lvtPlan {
	first := tn.shape.requests[0]
	line, _ := lvtListedLine(lvtRequestText(first.msgs), s.name)
	plan := lvtPlan{inRequest: lvtHasDef(first.defs, s.name), manifestLine: line}
	switch s.via {
	case "names":
		plan.searchable = lvtLoadedAfter(tn.load, s.name)
	case "query":
		plan.searchable = lvtLoadedAfter(tn.search, s.name)
	}
	return plan
}

// lvtPlan is levelTurnPlan (skill_visibility_manifest_red_test.go); the alias keeps
// this file's own vocabulary readable.
type lvtPlan = levelTurnPlan

// ── the rows ────────────────────────────────────────────────────────────────

func lvtCheckInstrument(t *testing.T, tn lvtTurns) {
	first := tn.shape.requests[0]
	require.NotEmpty(t, first.defs, "the turn sent a tool list")
	require.True(t, lvtHasDef(first.defs, "ToolSearch"),
		"C-1: the lazily-listed levels are loaded through ToolSearch, so the model must be able to call it")
}

func lvtCheckFull(t *testing.T, tn lvtTurns) {
	s := lvtStubNamed(t, lvtFull)
	first := tn.shape.requests[0]
	require.True(t, lvtHasDef(first.defs, s.name), "S-38 Full: the full definition of %s is in the tool list (D2#1)", s.name)
	for _, d := range first.defs {
		if d.Function.Name == s.name {
			require.Equal(t, s.description, d.Function.Description, "S-38 Full: the definition carries the tool's description")
			require.Contains(t, d.Function.Parameters, "type", "S-38 Full: the definition carries the tool's parameters")
		}
	}
}

func lvtCheckNameAndDescriptionLong(t *testing.T, tn lvtTurns) {
	s := lvtStubNamed(t, lvtNadLong)
	first := tn.shape.requests[0]
	require.False(t, lvtHasDef(first.defs, s.name), "S-38 Name and short description: no definition until a ToolSearch load (D2#2)")
	line, count := lvtListedLine(lvtRequestText(first.msgs), s.name)
	require.Equal(t, 1, count, "S-38 Name and short description: the tool is listed as ONE line (it is on %d lines)", count)
	require.Contains(t, line, longDescription[:140], "C-1: the line carries the description up to the 140-character cap (D2#2)")
	require.NotContains(t, line, capOverflowMarker, "C-1: nothing past the 140-character cap reaches the line (D2#2)")
	require.NotContains(t, line, lvtSecondLineMarker, "C-1: only the FIRST line of the description is listed")
	lvtRequireLoadedAfter(t, tn.load, s.name, "by name")
}

func lvtCheckNameAndDescriptionEmpty(t *testing.T, tn lvtTurns) {
	s := lvtStubNamed(t, lvtNadEmpty)
	first := tn.shape.requests[0]
	require.False(t, lvtHasDef(first.defs, s.name), "S-38 Name and short description: no definition until a ToolSearch load (D2#3)")
	line, count := lvtListedLine(lvtRequestText(first.msgs), s.name)
	require.Equal(t, 1, count, "the tool is listed as ONE line (it is on %d lines)", count)
	require.Equal(t, s.name, line, "D2#3: an empty description lists the name only")
	lvtRequireLoadedAfter(t, tn.load, s.name, "by name")
}

func lvtCheckNameOnly(t *testing.T, tn lvtTurns) {
	s := lvtStubNamed(t, lvtNameOnly)
	first := tn.shape.requests[0]
	require.False(t, lvtHasDef(first.defs, s.name), "S-38 Name only: no definition until a ToolSearch load (D2#4)")
	line, count := lvtListedLine(lvtRequestText(first.msgs), s.name)
	require.Equal(t, 1, count, "the tool is listed as ONE line (it is on %d lines)", count)
	require.Equal(t, s.name, line, "S-38 Name only: the name is listed with no description (D2#4)")
	require.NotContains(t, lvtRequestText(first.msgs), s.description, "S-38 Name only: the description is nowhere in what the model is sent")
	lvtRequireLoadedAfter(t, tn.load, s.name, "by name")
}

func lvtCheckHidden(t *testing.T, tn lvtTurns) {
	s := lvtStubNamed(t, lvtHidden)
	first := tn.shape.requests[0]
	require.False(t, lvtHasDef(first.defs, s.name), "S-38 Hidden: no definition in the tool list (D2#5)")
	require.NotContains(t, lvtRequestText(first.msgs), s.name, "S-38 Hidden: nothing about the tool is listed (D2#5)")
	require.GreaterOrEqual(t, len(tn.search.requests), 2, "the model's ToolSearch call must be followed by a second request")
	result, ok := toolResultFor(tn.search.requests[1].msgs, lvtSearchCallID)
	require.True(t, ok, "the loop answered the model's ToolSearch call")
	require.Contains(t, result, s.name, "S-38 Hidden: a ToolSearch query by keyword finds the tool (D2#5)")
	lvtRequireLoadedAfter(t, tn.search, s.name, "by keyword")
}

// lvtCheckFullIsNotSearchable is C-15's membership rule for the tool stored at Full:
// ToolSearch's corpus is "every tool whose level is not full", so a keyword search
// does not return a tool whose definition is already in the tool list.
func lvtCheckFullIsNotSearchable(t *testing.T, tn lvtTurns) {
	s := lvtStubNamed(t, lvtFull)
	require.True(t, lvtHasDef(tn.shape.requests[0].defs, s.name),
		"instrument: %s is stored at Full, so its definition is in the tool list (D2#1) and a search has nothing to add", s.name)
	require.GreaterOrEqual(t, len(tn.search.requests), 2, "the model's ToolSearch call must be followed by a second request")
	answer, ok := toolResultFor(tn.search.requests[1].msgs, lvtSearchCallID)
	require.True(t, ok, "the loop answered the model's ToolSearch call")
	require.Contains(t, answer, lvtHidden,
		"instrument: the same keyword search finds the Hidden tool, so a Full tool that is not found is the rule's doing")
	require.NotContains(t, answer, s.name,
		"C-15: ToolSearch finds every tool whose level is not Full, so a search for %s's own keyword (%q) must not return it", s.name, s.word)
}

// lvtCheckDemotedHidden is S-92 / D2#9 / D11#2: read_file, a built-in the
// pre-conversion tiers send every turn, set to Hidden.
func lvtCheckDemotedHidden(t *testing.T, tn lvtTurns) {
	first := tn.shape.requests[0]
	require.True(t, lvtHasDef(first.defs, lvtFull),
		"instrument: the same request carries the Full tool's definition, so an absent %s is the level's doing", lvtDemotedHidden)
	require.False(t, lvtHasDef(first.defs, lvtDemotedHidden),
		"S-92: a new chat's first request does not list %s once the person sets it to Hidden (D2#9)", lvtDemotedHidden)
	require.GreaterOrEqual(t, len(tn.search.requests), 2, "the model's ToolSearch calls must be followed by a second request")
	answer, ok := toolResultFor(tn.search.requests[1].msgs, lvtDemotedSearchCallID)
	require.True(t, ok, "the loop answered the model's ToolSearch call for %q", lvtDemotedQuery)
	require.Contains(t, lvtSearchMatches(t, answer), lvtDemotedHidden,
		"S-92 / C-15: ToolSearch with query %q returns %s, a built-in demoted from Full to Hidden; the answer was:\n%s",
		lvtDemotedQuery, lvtDemotedHidden, answer)
}

// lvtCheckDemotedNameOnly is D2#4 / S-38 for a built-in the pre-conversion tiers
// send every turn: list_directory stored at name_only has no definition until a
// ToolSearch load, and the load by name brings it.
func lvtCheckDemotedNameOnly(t *testing.T, tn lvtTurns) {
	require.GreaterOrEqual(t, len(tn.load.requests), 2, "the model's ToolSearch call must be followed by a second request in the same chat")
	first, second := tn.load.requests[0], tn.load.requests[1]
	require.True(t, lvtHasDef(first.defs, lvtFull),
		"instrument: the same request carries the Full tool's definition, so an absent %s is the level's doing", lvtDemotedNameOnly)
	require.False(t, lvtHasDef(first.defs, lvtDemotedNameOnly),
		"S-38 Name only: a new chat's first request carries no definition of %s, a built-in too (D2#4)", lvtDemotedNameOnly)
	answer, ok := toolResultFor(second.msgs, lvtLoadCallID)
	require.True(t, ok, "the loop answered the model's ToolSearch load")
	require.True(t, lvtHasDef(second.defs, lvtDemotedNameOnly),
		"S-38 / C-15: after the ToolSearch load by name the definition of %s is in the tool list; the answer was:\n%s", lvtDemotedNameOnly, answer)
}

// lvtCheckDenied is D2#6 / S-40 for one denied stub (one level).
func lvtCheckDenied(t *testing.T, w *lvtWorld, tn lvtTurns, s lvtStub) {
	first := tn.shape.requests[0]

	// Instruments: the same state lists, finds and runs an ALLOWED tool, so each
	// absence below is the Deny's doing and not a request that carries nothing.
	require.True(t, lvtHasDef(first.defs, lvtFull),
		"instrument: the allowed Full tool is in the tool list, so an absence below is the Deny's doing")
	_, listedCount := lvtListedLine(lvtRequestText(first.msgs), lvtNameOnly)
	require.NotZero(t, listedCount,
		"instrument: the allowed name_only tool is listed, so an unlisted denied tool is the Deny's doing")
	require.GreaterOrEqual(t, len(tn.search.requests), 2, "the model's ToolSearch call must be followed by a second request")
	result, ok := toolResultFor(tn.search.requests[1].msgs, lvtSearchCallID)
	require.True(t, ok, "the loop answered the model's ToolSearch call")
	require.Contains(t, result, lvtHidden,
		"instrument: the same keyword search finds the allowed Hidden tool, so a denied tool that is not found is the Deny's doing")
	require.EqualValues(t, 1, w.stubs[lvtFull].calls.Load(),
		"instrument: the allowed Full tool the model called in the same turn ran, so a denied tool that did not run was refused")

	// S-40: not in the tool list; C-1: not listed.
	require.False(t, lvtHasDef(first.defs, s.name), "S-40: the denied %s tool is not in Mia's tool list (D2#6)", s.level)
	require.NotContains(t, lvtRequestText(first.msgs), s.name, "C-1: the denied %s tool is not listed (D2#6)", s.level)

	// S-40: "a ToolSearch for it finds nothing"; C-1: not found.
	require.NotContains(t, result, s.name, "S-40: a ToolSearch for the denied %s tool finds nothing (D2#6)", s.level)
	require.False(t, lvtHasDef(tn.search.requests[1].defs, s.name), "S-40: the denied %s tool was not loaded by the search", s.level)

	// C-1: not callable.
	require.Zero(t, w.stubs[s.name].calls.Load(), "C-1: a call to the denied %s tool does not run it", s.level)
	require.GreaterOrEqual(t, len(tn.call.requests), 2, "the model's calls must be followed by a second request")
	callResult, answered := toolResultFor(tn.call.requests[1].msgs, lvtCallIDPrefix+s.name)
	require.True(t, answered, "the loop answered the model's call to the denied tool")
	require.NotContains(t, callResult, "stub "+s.name+" ran", "C-1: the model is not told the denied tool ran")
}

// lvtCheckAgreement pins that the real turn does, for every stub, what the bound
// authority (Ruling A: tools.ToolSurface.Plan) says: in the request, in the
// listing (its line, byte for byte) and in ToolSearch.
func lvtCheckAgreement(t *testing.T, tn lvtTurns) {
	nativebinding.Require(t, "pkg/tools", levelTurnPlanOf,
		"C-15: the real turn is compared with the one visibility authority (the binding is ToolSurface.Plan)")
	for _, s := range lvtStubs {
		t.Run(s.name, func(t *testing.T) {
			want := readNativeLevelTurnPlan(t, s.level, s.name, s.description, s.denied)
			got := lvtObservedPlan(s, tn)
			require.Equal(t, want, got,
				"C-15: the turn must do for %s (%s, denied=%v) exactly what the one authority plans: want %+v, the turn did %+v",
				s.name, s.level, s.denied, want, got)
		})
	}
}

// TestManifest_LevelsShapeTurn_RealTurnReadsTheLevelAuthority — see the file
// header.
//
// Traces to: BDD S-38, S-40; spec C-1, C-15; Dataset D2 rows 1–6; FR-034, FR-036,
// FR-066/FR-070 (no second copy of the level logic).
func TestManifest_LevelsShapeTurn_RealTurnReadsTheLevelAuthority(t *testing.T) {
	w := newLvtWorld(t)
	tn := w.runTurns()

	t.Run("instrument: the turn is a request the model can use ToolSearch in", func(t *testing.T) {
		lvtCheckInstrument(t, tn)
	})
	t.Run("D2#1 S-38 full: the definition is in the tool list", func(t *testing.T) {
		lvtCheckFull(t, tn)
	})
	t.Run("D2#2 S-38 name_and_description: one line cut to 140 characters, no definition until a load", func(t *testing.T) {
		lvtCheckNameAndDescriptionLong(t, tn)
	})
	t.Run("D2#3 S-38 name_and_description with an empty description: the name alone", func(t *testing.T) {
		lvtCheckNameAndDescriptionEmpty(t, tn)
	})
	t.Run("D2#4 S-38 name_only: the name alone, no description, no definition until a load", func(t *testing.T) {
		lvtCheckNameOnly(t, tn)
	})
	t.Run("D2#5 S-38 hidden: listed nowhere, a keyword ToolSearch finds and loads it", func(t *testing.T) {
		lvtCheckHidden(t, tn)
	})
	for _, s := range lvtStubs {
		if !s.denied {
			continue
		}
		t.Run("D2#6 S-40 Deny wins over the "+s.level+" level: not listed, not found, not callable", func(t *testing.T) {
			lvtCheckDenied(t, w, tn, s)
		})
	}
	t.Run("C-15 search membership: a tool stored at Full is in the tool list and is not found by a keyword search", func(t *testing.T) {
		lvtCheckFullIsNotSearchable(t, tn)
	})
	t.Run("D2#9 S-92 read_file (old Full) set to hidden: not in the first request, ToolSearch \"read file\" returns it", func(t *testing.T) {
		lvtCheckDemotedHidden(t, tn)
	})
	t.Run("D2#4 S-38 list_directory (old Full) set to name_only: no definition until a ToolSearch load by name", func(t *testing.T) {
		lvtCheckDemotedNameOnly(t, tn)
	})
	t.Run("C-15 one authority: the real turn does for every tool what ToolSurface.Plan says", func(t *testing.T) {
		lvtCheckAgreement(t, tn)
	})
}
