package agent

import (
	"regexp"
	"strings"
	"testing"
)

// Oracle: chief fix-round-1 conditional rule. This is model-facing tool output,
// not a source-code substring test. Runtime tests separately verify each rule.
// Accept equivalent prose; do not snapshot the prompt seat's exact sentences.
func TestL11274SchemaDeclaresNativeSoulAndOptionalExternalPath(t *testing.T) {
	f := newL11274Fixture(t, "", false)
	omnipus, ok := f.al.GetRegistry().GetAgent("omnipus")
	if !ok || omnipus == nil {
		t.Fatal("BLOCKED: Omnipus not registered — required by #1274")
	}
	tool, ok := omnipus.Tools.Get("create_agent")
	if !ok {
		t.Fatal("BLOCKED: create_agent not registered — required by #1274")
	}
	params := tool.Parameters()
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatalf("instrument: cannot read the schema required list: %T", params["required"])
	}
	set := make(map[string]bool, len(required))
	for _, field := range required {
		set[field] = true
	}
	if !set["name"] || !set["description"] || !set["model"] {
		t.Fatalf("instrument: cannot see the known universally required fields: %v", required)
	}
	for _, conditional := range []string{"soul", "cli", "cli_path"} {
		if set[conditional] {
			t.Errorf("%s is not universally required; schema must preserve the runtime-specific rule", conditional)
		}
	}
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("instrument: cannot read actual tool properties")
	}
	description := func(field string) string {
		property, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("schema lost field %q", field)
		}
		text, ok := property["description"].(string)
		if !ok || text == "" {
			t.Fatalf("schema lost the model-facing rule for %q", field)
		}
		return text
	}
	soulText := description("soul")
	requirement := regexp.MustCompile(`(?i)\b(required|requires|must|mandatory)\b`)
	native := regexp.MustCompile(`(?i)\bnative\b`).MatchString(soulText) ||
		(regexp.MustCompile(`(?i)\bMain\b`).MatchString(soulText) && regexp.MustCompile(`(?i)\bSubagent\b`).MatchString(soulText))
	external := regexp.MustCompile(`(?i)\b(external|subagent_3p)\b`).MatchString(soulText)
	unsupported := regexp.MustCompile(`(?i)unsupported|not\s+(supported|accepted)|omit|no\s+(native\s+)?soul|refus`).MatchString(soulText)
	if !requirement.MatchString(soulText) || !native || !external || !unsupported {
		t.Errorf("soul guidance must distinguish native requiredness from external unsupportedness, got %q", soulText)
	}
	cliText := description("cli")
	if !requirement.MatchString(cliText) || !regexp.MustCompile(`(?i)\b(external|subagent_3p)\b`).MatchString(cliText) {
		t.Errorf("cli guidance must associate its requirement with the external runtime, got %q", cliText)
	}
	pathText := description("cli_path")
	if !regexp.MustCompile(`(?i)\b(optional|empty|omit|omitted|default)\b`).MatchString(pathText) {
		t.Errorf("cli_path guidance must name its optional/default-PATH rule, got %q", pathText)
	}
	typeText := description("agent_type")
	// Keep the optional marker in the cli_path clause, rather than accepting
	// the unrelated "Main (default)" marker elsewhere in the description.
	optionalPathClause := regexp.MustCompile(`(?i)(\bcli_path\b[^.!?;\n]*\b(optional|empty|omit|omitted|default)\b)|(\b(optional|empty|omit|omitted)\b[^.!?;\n]*\bcli_path\b)`)
	if !strings.Contains(typeText, "subagent_3p") || !optionalPathClause.MatchString(typeText) {
		t.Errorf("agent_type guidance must preserve cli-required / cli_path-optional conditional rule, got %q", typeText)
	}
}
