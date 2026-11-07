// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for agent guidance and the watching line (BS-7.1, BS-7.3, BS-6.16,
// FR-023, FR-026, FR-027, D-F1).
package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

func TestBaseInstructions_LinkSection(t *testing.T) {
	prompt := NewContextBuilder(t.TempDir()).BuildSystemPrompt()
	start := strings.Index(prompt, "<operating_rules>")
	end := strings.Index(prompt, "</operating_rules>")
	if start < 0 || end < start {
		t.Fatalf("operating_rules block missing from BuildSystemPrompt")
	}
	body := prompt[start:end]

	const retired = "After reading back saved artifacts, provide their verified full absolute paths."
	if strings.Contains(body, retired) {
		t.Errorf("operating rules still contain %q (FR-027 / D-F1 / BS-7.1)", retired)
	}

	// Topics the rewritten "### Files and artifacts" group must state (BS-7.1).
	for _, phrase := range []string{
		"show_to_user",
		"User watching: yes",
		"omnipus://",
		"link:",
	} {
		if !strings.Contains(body, phrase) {
			t.Errorf("operating rules missing %q (BS-7.1 / FR-023)", phrase)
		}
	}
	if !strings.Contains(body, "Save code, HTML, scripts, CSS, JSON, YAML, configuration,") {
		t.Errorf("artifacts-over-chat sentence (today's rule 25) was dropped (BS-7.1 / A3)")
	}

	nums := regexp.MustCompile(`(?m)^(\d+)\. `).FindAllStringSubmatch(body, -1)
	if len(nums) == 0 {
		t.Fatal("no numbered rules found inside operating_rules")
	}
	for i, m := range nums {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("rule number %q: %v", m[1], err)
		}
		if n != i+1 {
			t.Fatalf("rule numbers are not continuous: position %d is %d (BS-7.1)", i+1, n)
		}
	}
}

func TestDynamicContext_UserWatching(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	web := cb.buildDynamicContext(0, "W", "webchat", "chat-1", "", "")
	if !strings.Contains(web, "User watching: no") {
		t.Errorf("web dynamic context = %q, want a line %q when nothing is watching (FR-026; never default to yes, never omit the line)", web, "User watching: no")
	}
	if strings.Contains(web, "User watching: yes") {
		t.Errorf("a turn with no watching claim rendered yes: %q", web)
	}
	other := cb.buildDynamicContext(0, "W", "telegram", "chat-1", "", "")
	if strings.Contains(other, "User watching") {
		t.Errorf("non-web dynamic context included the watching line: %q (FR-026)", other)
	}
}

func TestToolDescriptions_OneSentence(t *testing.T) {
	// send_file's description gains one sentence (FR-023 / BS-7.2). The spec
	// does not quote it; the sentence has to tell the model about the link line.
	// show_to_user's sentence is specified under Watching: call it when the line
	// says yes, and do not expect the Library to open when it says no.
	mia := coreagent.GetPrompt("mia")
	if !strings.Contains(mia, "tool:show_to_user") {
		t.Errorf("mia prompt does not name tool:show_to_user (BS-6.14 / U9)")
	}
	jim := coreagent.GetPrompt("jim")
	if !strings.Contains(jim, "tool:show_to_user") {
		t.Errorf("jim prompt does not name tool:show_to_user (BS-6.14 / U9)")
	}
	if strings.Contains(coreagent.GetPrompt("omnipus"), "tool:show_to_user") {
		t.Errorf("omnipus prompt names tool:show_to_user (D-F2)")
	}
}

func TestDocsAgents_PathRule(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	docs := filepath.Join(filepath.Dir(file), "..", "..", "docs", "agents.md")
	raw, err := os.ReadFile(docs)
	if err != nil {
		t.Fatalf("read docs/agents.md: %v", err)
	}
	body := string(raw)
	const retired = "full absolute paths reported"
	if strings.Contains(body, retired) {
		t.Errorf("docs/agents.md still contains %q (FR-027 / BS-7.3)", retired)
	}
	const replacement = "agents name files (or give a relative path) with a link, and give the absolute path when you ask or when the file is outside every Library"
	if !strings.Contains(body, replacement) {
		t.Errorf("docs/agents.md missing the D-F1 sentence %q (docs TODO / BS-7.3)", replacement)
	}
	const who = "Mia and Jim can open the Library for you and Omnipus cannot"
	if !strings.Contains(body, who) {
		t.Errorf("docs/agents.md missing %q (docs TODO / D-F2)", who)
	}

	toolsDoc := filepath.Join(filepath.Dir(file), "..", "..", "docs", "tools.md")
	rawTools, err := os.ReadFile(toolsDoc)
	if err != nil {
		t.Fatalf("read docs/tools.md: %v", err)
	}
	toolsBody := string(rawTools)
	const omnipusNote = "show_to_user is allowed by default and Omnipus does not have it"
	if !strings.Contains(toolsBody, omnipusNote) {
		t.Errorf("docs/tools.md missing %q (docs TODO)", omnipusNote)
	}
	if !strings.Contains(toolsBody, "show_to_user") || !strings.Contains(toolsBody, "Main Allow") {
		t.Errorf("docs/tools.md missing the show_to_user Main Allow row (docs TODO)")
	}
}
