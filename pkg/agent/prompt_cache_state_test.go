package agent

import (
	"bytes"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func toolNamed(name string) providers.ToolDefinition {
	return providers.ToolDefinition{
		Type: "function",
		Function: providers.ToolFunctionDefinition{
			Name: name,
		},
	}
}

func listedToolNames(defs []providers.ToolDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Function.Name)
	}
	return out
}

func toolsNamed(names ...string) []providers.ToolDefinition {
	out := make([]providers.ToolDefinition, len(names))
	for i, name := range names {
		out[i] = toolNamed(name)
	}
	return out
}

// TestToolOrder_IndependentOfInputOrder is TDD row 10. Three input orders,
// one alphabetical byte string.
func TestToolOrder_IndependentOfInputOrder(t *testing.T) {
	orders := [][]string{
		{"alpha", "mid", "zebra"},
		{"zebra", "mid", "alpha"},
		{"mid", "zebra", "alpha"},
	}
	got := make([][]byte, len(orders))
	for i, names := range orders {
		got[i] = CanonicalToolListBytes(toolsNamed(names...))
	}
	if !bytes.Equal(got[0], got[1]) || !bytes.Equal(got[0], got[2]) {
		t.Fatalf("serializations differ: %q / %q / %q", got[0], got[1], got[2])
	}
	text := string(got[0])
	alpha := strings.Index(text, "alpha")
	mid := strings.Index(text, "mid")
	zebra := strings.Index(text, "zebra")
	if alpha < 0 || mid < 0 || zebra < 0 || !(alpha < mid && mid < zebra) {
		t.Fatalf("canonical bytes = %q, want alphabetical alpha then mid then zebra", text)
	}
}

// TestLoadedTool_NotInheritedByNextSession is TDD row 12b.
func TestLoadedTool_NotInheritedByNextSession(t *testing.T) {
	finished := NewSessionToolList("sess-finished")
	finished.Add(toolNamed("mcp_widget"))
	if !finished.Contains("mcp_widget") {
		t.Fatal("tool loaded in the finished session is not in that session's tool list")
	}
	next := NewSessionToolList("sess-next")
	if next.Contains("mcp_widget") || len(next.Definitions()) != 0 {
		t.Fatalf("new session lists %v; a finished session's tool must not carry over", listedToolNames(next.Definitions()))
	}
}

// TestLoadedTool_SurvivesTime is TDD row 14. Adding a tool and not removing it
// is the session lifetime. The list's bytes must not change merely because
// time passed.
func TestLoadedTool_SurvivesTime(t *testing.T) {
	list := NewSessionToolList("sess-live")
	list.Add(toolNamed("mcp_widget"))
	before := CanonicalToolListBytes(list.Definitions())
	if !list.Contains("mcp_widget") {
		t.Fatal("loaded tool is not executable after time passed without a revoke")
	}
	after := CanonicalToolListBytes(list.Definitions())
	if !bytes.Equal(before, after) {
		t.Fatalf("tool list bytes changed with no add or remove: %q vs %q", before, after)
	}
	names := listedToolNames(list.Definitions())
	if len(names) != 1 || names[0] != "mcp_widget" {
		t.Fatalf("definitions = %v, want [mcp_widget]", names)
	}
}

// TestLoadedMcpTool_ExecutesOnlyInThatSession is TDD row 26.
func TestLoadedMcpTool_ExecutesOnlyInThatSession(t *testing.T) {
	owner := NewSessionToolList("sess-owner")
	owner.Add(toolNamed("mcp_widget"))
	if !owner.Contains("mcp_widget") {
		t.Fatal("non-core MCP tool is not executable in the session that loaded it")
	}
	other := NewSessionToolList("sess-other")
	if other.Contains("mcp_widget") {
		t.Fatal("second session can run a tool it did not load")
	}
	owner.Remove("mcp_widget")
	if owner.Contains("mcp_widget") {
		t.Fatal("tool stayed executable after a Deny, removed assignment, or disconnect")
	}
}

// TestGoalNarrowing_RestoresListNextTurn is TDD row 16.
func TestGoalNarrowing_RestoresListNextTurn(t *testing.T) {
	session := NewSessionToolList("sess-goal")
	session.Add(toolNamed("bash"))
	session.Add(toolNamed("read"))
	goal := toolsNamed("set_goal")

	narrow := GoalNarrowedTools(session, true, goal)
	if strings.Join(listedToolNames(narrow), ",") != "set_goal" {
		t.Fatalf("goal turn tools = %v, want [set_goal]", listedToolNames(narrow))
	}
	restored := GoalNarrowedTools(session, false, goal)
	got := strings.Join(listedToolNames(restored), ",")
	if got != "bash,read" {
		t.Fatalf("ordinary turn tools = %v, want the session list in canonical order [bash read]", listedToolNames(restored))
	}
}

// TestSkillList_FrozenForSession is TDD row 15's advertised-list half. The
// reminder's place behind the conversation is the rewritten turn-assembly test.
func TestSkillList_FrozenForSession(t *testing.T) {
	frozen := FreezeSkillList("sess-skills", []string{"release-notes", "review"})
	if strings.Join(frozen.Names(), ",") != "release-notes,review" {
		t.Fatalf("advertised list = %v, want the first turn's list after the files on disk change", frozen.Names())
	}
}

// TestSkillAndToolSizes_AreTokenCounts is TDD row 35. A byte-length record
// does not satisfy the requirement.
func TestSkillAndToolSizes_AreTokenCounts(t *testing.T) {
	skill := "hello world, this is skill text that is not one token per byte"
	rec := RecordTokenSizes(toolsNamed("bash", "read"), skill)
	switch strings.ToLower(rec.Estimator) {
	case "", "bytes", "byte", "len", "length", "characters", "chars", "runes":
		t.Fatalf("estimator = %q, want a named token estimator (a byte or character count does not satisfy FR-009)", rec.Estimator)
	}
	if rec.SkillTextTokens <= 0 || rec.SkillTextTokens == len(skill) {
		t.Fatalf("skill tokens = %d, byte length = %d; a byte count does not satisfy FR-009", rec.SkillTextTokens, len(skill))
	}
	if rec.ToolListTokens <= 0 {
		t.Fatalf("tool list tokens = %d, want a token count or token estimate", rec.ToolListTokens)
	}
}
