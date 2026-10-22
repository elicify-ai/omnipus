package agent

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Oracle: founder's 2026-10-21 compact-roster mandate R1-R4 and agreed entry
// grammar, not observations of the old renderer. Only measurement is historical.
// RED stops before GREEN/mutation proof; an independent CHECK owns those steps.
func TestCompactDelegationRoster(t *testing.T) {
	t.Run("EveryAllowedEntry", testCompactEveryAllowedEntry)
	t.Run("UseForExcerpts", testCompactUseForExcerpts)
	t.Run("DescriptionLookupNormalizesConfigID", testCompactDescriptionLookupNormalizesConfigID)
	t.Run("XMLAttributeEscaping", testCompactXMLAttributeEscaping)
	t.Run("GenericGuidanceOnce", testCompactGenericGuidanceOnce)
	t.Run("StableRepeatedTurns", testCompactStableRepeatedTurns)
	t.Run("DescriptionChangesAfterReload", testCompactDescriptionChangesAfterReload)
	t.Run("ModeSubsetsAndResolvedCaps", testCompactModeSubsetsAndResolvedCaps)
	t.Run("TenAgentMeasurement", testCompactTenAgentMeasurement)
}

func testCompactEveryAllowedEntry(t *testing.T) {
	agents := compactTenAgents()
	_, cb := compactRosterLoop(t, agents, 3)
	got := compactRosterBlock(t, cb, 0)
	entries := compactRosterEntries(t, got)
	if len(entries) != len(agents) {
		t.Fatalf("entry count = %d, want every one of %d allowed agents", len(entries), len(agents))
	}
	for _, a := range agents {
		t.Run(a.Name, func(t *testing.T) {
			compactAssertEntry(t, got, a.Name, a.ID, a.UseFor, "direct/task", 3)
		})
	}
	directory := compactDelimitedBlock(t, got, "team_directory")
	for _, forbidden := range []string{"delegate(", "create_task(", "ToolSearch", "### →", "Tools:", "- `"} {
		if strings.Contains(directory, forbidden) {
			t.Errorf("team_directory must not contain per-agent tool lists/calls %q", forbidden)
		}
	}
}

func testCompactUseForExcerpts(t *testing.T) {
	// Small sentence classes, not an NLP framework: ., !, ?, no terminator,
	// later explicit cue, colon/no-colon, multiline and empty descriptions.
	cases := []struct {
		name, description, want string
	}{
		{"LaterUseFor", "General introduction. Use for: contract reviews. Private detail must stay out.", "contract reviews."},
		{"LaterUseWhen", "General introduction. Another introduction. Use when a launch needs review. Private detail must stay out.", "a launch needs review."},
		{"UseWhenColon", "General introduction. Use when: invoices need checking! Private detail must stay out.", "invoices need checking!"},
		{"UseForNoColon", "Use for legal handoffs? Private detail must stay out.", "legal handoffs?"},
		{"Multiline", "General introduction.\nUse for:\tcoordinate  design\n\treviews.\nPrivate detail must stay out.", "coordinate design reviews."},
		{"FirstSentencePeriod", "Review contracts. Private detail must stay out.", "Review contracts."},
		{"FirstSentenceExclamation", "Review releases! Private detail must stay out.", "Review releases!"},
		{"FirstSentenceQuestion", "Need a finance check? Private detail must stay out.", "Need a finance check?"},
		{"SingleUnterminatedSentence", "Review the final handoff", "Review the final handoff"},
		{"EmptyDescription", "", "(no description)"},
		{"WhitespaceOnly", " \n\t ", "(no description)"},
		{"PlainTextEscaping", `Intro. Use for: "urgent" R&D <handoffs> & checks. Private detail must stay out.`, `"urgent" R&D <handoffs> & checks.`},
		{"Unicode", "Intro. Use for: résumé reviews — café teams. Private detail must stay out.", "résumé reviews — café teams."},
		{"PreserveSourceHQAndJarvis", "Intro. Use for: HQ requests from Jarvis. Private detail must stay out.", "HQ requests from Jarvis."},
		{"UsefulIsNotCue", "Useful for planning. Private detail must stay out.", "Useful for planning."},
		{"IncidentalLowercaseIsNotCue", "General role. This is a tool we use for research. Private detail must stay out.", "General role."},
		{"QuotedSentenceEnd", `Answer "what is the pipeline." Private detail must stay out.`, `Answer "what is the pipeline."`},
		{"QuotedCueSentenceEnd", `Intro. Use for: answering "what is the pipeline." Private detail must stay out.`, `answering "what is the pipeline."`},
	}
	agents := make([]compactRosterAgent, 0, len(cases))
	for _, tc := range cases {
		agents = append(agents, compactRosterAgent{
			ID: "excerpt-" + strings.ToLower(tc.name), Name: tc.name, Description: tc.description, UseFor: tc.want,
		})
	}
	_, cb := compactRosterLoop(t, agents, 3)
	got := compactRosterBlock(t, cb, 0)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := agents[i]
			compactAssertEntry(t, got, a.Name, a.ID, tc.want, "direct/task", 3)
		})
	}
}

func testCompactDescriptionLookupNormalizesConfigID(t *testing.T) {
	a := compactRosterAgent{
		ID: "normalized-target", ConfigID: "  normalized-target  ", Name: "Normalized Target",
		Description: "Intro. Use for: normalized config lookup. Private detail.",
	}
	_, cb := compactRosterLoop(t, []compactRosterAgent{a}, 3)
	compactAssertEntry(t, compactRosterBlock(t, cb, 0), a.Name, a.ID, "normalized config lookup.", "direct/task", 3)
}

func testCompactXMLAttributeEscaping(t *testing.T) {
	// Direct renderer input lets us check ID metacharacters without pretending
	// that such an ID is legal in the workspace/registry ID vocabulary.
	const name = `Ops "R&D" <Lead>`
	const id = `fixture"<&'id`
	got := buildDelegationContext([]delegationTarget{{ID: id, Label: name}}, 3, 0)
	compactAssertEntry(t, got, name, id, "(no description)", "direct/task", 3)
	if n := len(compactRosterEntries(t, got)); n != 1 {
		t.Fatalf("escaped attribute text injected entries: count=%d, want 1", n)
	}
}

func testCompactGenericGuidanceOnce(t *testing.T) {
	_, cb := compactRosterLoop(t, compactTenAgents(), 5)
	got := compactRosterBlock(t, cb, 1)
	guidance := compactDelimitedBlock(t, got, "delegation_guidance")
	compactDelimitedBlock(t, got, "team_directory")
	if strings.Index(got, "</delegation_guidance>") >= strings.Index(got, "<team_directory>") {
		t.Error("single guidance block must precede single team_directory")
	}
	// Scope generic-wording checks to instructions ONLY. Descriptions may
	// legitimately mention HQ and Jarvis and are tested unchanged above.
	if regexp.MustCompile(`(?i)\b(?:HQ|Jarvis)\b`).MatchString(guidance) {
		t.Errorf("generic guidance must not hardcode HQ/Jarvis:\n%s", guidance)
	}
	calls := regexp.MustCompile(`delegate\(agent_id="([^"]+)"[^\n)]*\)`).FindAllStringSubmatch(guidance, -1)
	if len(calls) != 1 {
		t.Fatalf("generic delegate call count = %d, want 1; guidance:\n%s", len(calls), guidance)
	}
	placeholder := strings.ToLower(calls[0][1])
	if !strings.Contains(placeholder, "listed") || !strings.Contains(placeholder, "id") {
		t.Errorf("delegate target = %q, want listed-ID placeholder", calls[0][1])
	}
	if !strings.Contains(calls[0][0], `task="`) {
		t.Errorf("generic delegate syntax must supply task: %q", calls[0][0])
	}
	compactAssertSharedSyntax(t, got)
	for _, word := range []string{"create_task", "ToolSearch", "criteria", "dod", "status", "result", "completion", "question", "problem"} {
		if !strings.Contains(guidance, word) {
			t.Errorf("generic guidance missing required fact %q", word)
		}
	}
	compactAssertGuidanceMeaning(t, guidance)
}

func compactAssertSharedSyntax(t *testing.T, got string) {
	t.Helper()
	if n := strings.Count(got, "delegate(agent_id="); n != 1 {
		t.Errorf("whole-roster delegate call syntax count = %d, want exactly 1", n)
	}
	// A full create_task schema need not be copied, but any example is shared,
	// never one copy per target. ToolSearch + criteria/dod are checked separately.
	if n := strings.Count(got, "create_task("); n > 1 {
		t.Errorf("whole-roster create_task call syntax count = %d, want at most 1", n)
	}
	for _, a := range compactTenAgents() {
		if strings.Contains(got, `delegate(agent_id="`+a.ID+`"`) || strings.Contains(got, `create_task(agent_id="`+a.ID+`"`) {
			t.Errorf("concrete target %q still has a repeated call example", a.ID)
		}
	}
}

func compactAssertGuidanceMeaning(t *testing.T, guidance string) {
	t.Helper()
	// Assert required facts, not one particular prose rendering of the block.
	patterns := []struct{ fact, pattern string }{
		{"task loading", `(?is)(?:create_task[^.!?\n]*ToolSearch|ToolSearch[^.!?\n]*create_task)`},
		{"task criteria AND dod", `(?is)create_task[^.!?\n]*(?:criteria[^.!?\n]*dod|dod[^.!?\n]*criteria)`},
		{"helper goal needs both", `(?is)(?:helper[^.!?\n]*goal|goal[^.!?\n]*helper)[^.!?\n]*(?:criteria[^.!?\n]*dod|dod[^.!?\n]*criteria|both(?:\s+too)?\s*[.!?])`},
		{"avoid repeated polling", `(?is)(?:rather than|instead of|avoid|not|never|do not)[^.!?\n]*repeated[^.!?\n]*poll`},
		{"missing route reports dependency", `(?is)(?:missing[^.!?\n]*route|route[^.!?\n]*missing)[^.!?\n]*report[^.!?\n]*dependenc`},
		{"do not take specialist work", `(?is)(?:not|never|rather than|instead of)[^.!?\n]*take over[^.!?\n]*specialist`},
		{"current depth fact", `(?i)current(?: main| chain| delegation)? depth\s*[:=]\s*1\b`},
		{"maximum cap fact", `(?i)(?:max chain depth|global[^\n.:;]*(?:maximum|cap))\s*[:=]\s*5\b`},
	}
	for _, p := range patterns {
		if !regexp.MustCompile(p.pattern).MatchString(guidance) {
			t.Errorf("guidance missing %s (fact pattern %q):\n%s", p.fact, p.pattern, guidance)
		}
	}
}

func testCompactStableRepeatedTurns(t *testing.T) {
	a := compactRosterAgent{ID: "stable-target", Name: "Stable Target", Description: "Intro. Use for: stable handoffs. Extra detail.", UseFor: "stable handoffs."}
	_, cb := compactRosterLoop(t, []compactRosterAgent{a}, 3)
	first := compactRosterBlock(t, cb, 0)
	for turn := 1; turn <= 3; turn++ {
		next := compactRosterBlock(t, cb, 0)
		if next != first {
			t.Fatalf("unchanged graph/config changed roster bytes at turn %d\nfirst=%q\nnext=%q", turn, first, next)
		}
	}
	compactAssertEntry(t, first, a.Name, a.ID, a.UseFor, "direct/task", 3)
}

func testCompactDescriptionChangesAfterReload(t *testing.T) {
	a := compactRosterAgent{ID: "reload-target", Name: "Reload Target", Description: "Intro. Use for: initial handoffs. Extra detail.", UseFor: "initial handoffs."}
	al, oldCB := compactRosterLoop(t, []compactRosterAgent{a}, 3)
	before := compactRosterBlock(t, oldCB, 0)
	live := al.GetConfig()
	next, err := live.Clone()
	if err != nil {
		t.Fatal("clone live config for reload: ", err)
	}
	found := false
	for i := range next.Agents.List {
		if next.Agents.List[i].ID == a.ID {
			next.Agents.List[i].Description = "Different intro. Use when refreshed handoffs arrive. New private detail."
			found = true
		}
	}
	if !found {
		t.Fatal("reload fixture target absent from live config")
	}
	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, next); err != nil {
		t.Fatalf("real ReloadProviderAndConfig: %v", err)
	}
	// The old in-flight builder and the newly published builder must both
	// consult current live config, just as the existing graph freshness path does.
	for name, cb := range map[string]*ContextBuilder{"old-builder": oldCB, "new-builder": compactRosterBuilder(t, al)} {
		t.Run(name, func(t *testing.T) {
			after := compactRosterBlock(t, cb, 0)
			compactAssertEntry(t, after, a.Name, a.ID, "refreshed handoffs arrive.", "direct/task", 3)
			if after == before || strings.Contains(after, a.UseFor) {
				t.Errorf("reload must replace old excerpt: before=%q after=%q", before, after)
			}
		})
	}
	compactAssertEntry(t, before, a.Name, a.ID, a.UseFor, "direct/task", 3)
}

func testCompactModeSubsetsAndResolvedCaps(t *testing.T) {
	cases := []struct {
		name  string
		modes []config.DelegationMode
		want  string
	}{
		{"NilMeansBoth", nil, "direct/task"},
		{"EmptyMeansBoth", []config.DelegationMode{}, "direct/task"},
		{"DirectOnly", []config.DelegationMode{config.DelegationModeBackground}, "direct"},
		{"TaskOnly", []config.DelegationMode{config.DelegationModeTask}, "task"},
		{"Both", []config.DelegationMode{config.DelegationModeBackground, config.DelegationModeTask}, "direct/task"},
		{"RetiredAwait", []config.DelegationMode{"await"}, "none"},
		{"Unrecognized", []config.DelegationMode{"unknown-mode"}, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDelegationContext([]delegationTarget{{ID: "mode-target", Label: "Mode Target", Modes: tc.modes, Depth: ptr(5), DepthCap: 5}}, 3, 3)
			compactAssertEntry(t, got, "Mode Target", "mode-target", "(no description)", tc.want, 5)
		})
	}
	t.Run("RealWiringUsesEdgeAndGlobalCaps", func(t *testing.T) {
		agents := []compactRosterAgent{
			{ID: "direct-target", Name: "Direct Target", Description: "Use for: direct work.", Modes: []string{"direct"}, Depth: ptr(2)},
			{ID: "task-target", Name: "Task Target", Description: "Use for: tracked work.", Modes: []string{"task"}, Depth: ptr(7)},
		}
		_, cb := compactRosterLoop(t, agents, 4)
		got := compactRosterBlock(t, cb, 1)
		compactAssertEntry(t, got, "Direct Target", "direct-target", "direct work.", "direct", 2)
		compactAssertEntry(t, got, "Task Target", "task-target", "tracked work.", "task", 4)
		atDirectCap := compactRosterBlock(t, cb, 2)
		if _, ok := compactFindEntry(t, atDirectCap, "direct-target"); ok {
			t.Error("direct-target must disappear at its cap 2")
		}
		compactAssertEntry(t, atDirectCap, "Task Target", "task-target", "tracked work.", "task", 4)
	})
}
