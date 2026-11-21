// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// config_settings_redesign_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order rows 6b and 16 (spec docs/internal/specs/
// settings-redesign-spec.md):
//
//   - TestSetConfig_RefusesPerformanceValues — Dataset D-16 rows 5–8 (C-7,
//     #936, FR-100): the Performance values have ONE write path (Settings →
//     Limits, PUT /api/v1/performance, password). set_config must refuse all
//     of them. Rows 5 and 6 are refused today ("already refused today") —
//     preserved behaviour, pinned so a knownConfigPrefixes change cannot
//     silently open them. Rows 7 and 8 are the NEW refusals
//     (tools.manifest.compressed, and its parent tools.manifest "at or under
//     the key") — RED today, because tools. is a writable prefix and the
//     value guard only watches the two agents.defaults keys.
//   - TestSettingsAgentWriteParity — FR-101: every value this spec lets a
//     person edit has either an agent setter or a listed user-only reason
//     with a passing refusal through the REAL set_config tool. The
//     inventory is enumerated independently of this guard's own rows from
//     TWO sources (CHECK F09, round 2): the spec's field tables (every
//     table whose header names a config-key column — parsed from the
//     document at run time, plus the agent-tool field table's retention
//     trio) and the wireframe control map (design-notes §2, 88 rows,
//     transcribed and count-pinned), whose value rows carry the dotted keys
//     the spec and the contracts it names define. The comparison is exact
//     set equality BOTH ways with duplicate protection: a value added to
//     either source without a row fails, a stale row fails, and a duplicate
//     row cannot stand in for a missing one. Per spec (Hard-constraint line
//     "The system MUST NOT add an agent tool or a new set_config write path
//     for any value in this spec"), the spec-listed keys are refusal rows;
//     keys the spec lists nowhere carry their observed set_config outcome as
//     a labeled characterization pin (refusal family or a verified setter
//     receipt). The companion positive control runs the REAL comparison —
//     an unclassified key must fail the complete instrument, not a helper.
//
// Oracle: the spec's C-7 table, the agent-tool field table, D-16 and the BDD
// scenario "Performance values cannot be changed around Settings" (refused
// "with a message naming Settings → Limits or PUT /api/v1/performance").
// Expected values come from the spec, never from the tool's current output.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func TestSetConfig_RefusesPerformanceValues(t *testing.T) {
	t.Run("D16 row 5: performance.max_parallel_agents is an unknown section", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		stored := cfg.Performance.MaxParallelAgents
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "performance.max_parallel_agents", "value": float64(64)})
		// Preserved behaviour (spec D-16 row 5: "already refused today") —
		// pinned: performance. is not in knownConfigPrefixes.
		if !res.IsError {
			t.Fatalf("set_config accepted performance.max_parallel_agents — the pin fired: "+
				"performance. must stay outside the writable prefixes (#936 C-7); got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "unknown config key") {
			t.Errorf("refusal must say the section is not settable via this tool: %s", res.ForLLM)
		}
		if cfg.Performance.MaxParallelAgents != stored {
			t.Errorf("refused write changed the config: %d → %d", stored, cfg.Performance.MaxParallelAgents)
		}
	})

	t.Run("D16 row 6: planning.goal_max_rounds is an unknown section", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		stored := cfg.Planning.GoalMaxRounds
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "planning.goal_max_rounds", "value": float64(99)})
		// Preserved behaviour (spec D-16 row 6: "already refused today").
		if !res.IsError {
			t.Fatalf("set_config accepted planning.goal_max_rounds — the pin fired: "+
				"planning. must stay outside the writable prefixes (#936 C-7); got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "unknown config key") {
			t.Errorf("refusal must say the section is not settable via this tool: %s", res.ForLLM)
		}
		if cfg.Planning.GoalMaxRounds != stored {
			t.Errorf("refused write changed the config: %d → %d", stored, cfg.Planning.GoalMaxRounds)
		}
	})

	t.Run("D16 row 7: tools.manifest.compressed is newly refused, message names Settings → Limits", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		if !cfg.Tools.Manifest.Compressed {
			t.Fatalf("fixture: DefaultConfig().Tools.Manifest.Compressed must be true (pkg/config pins this default)")
		}
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "tools.manifest.compressed", "value": false})
		// RED: the spec's new refusal (FR-100, D-16 row 7). Today tools. is a
		// writable prefix and the value guard watches only agents.defaults.
		if !res.IsError {
			t.Fatalf("RED (D-16 row 7, FR-100): set_config accepted tools.manifest.compressed — "+
				"the #936 refusal does not exist yet; required: refusal whose message names Settings → Limits. Got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "Settings → Limits") {
			t.Errorf("refusal must name Settings → Limits (spec D-16 row 7): %s", res.ForLLM)
		}
		if !cfg.Tools.Manifest.Compressed {
			t.Errorf("refused write changed the config: Compressed must stay true")
		}
	})

	t.Run("D16 row 8: parent tools.manifest is refused at or under the key", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		if !cfg.Tools.Manifest.Compressed {
			t.Fatalf("fixture: DefaultConfig().Tools.Manifest.Compressed must be true")
		}
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "tools.manifest", "value": map[string]any{}})
		// RED: D-16 row 8 ("refused — at or under the key"): a write at the
		// parent of a performance-only key is refused too, whatever the value
		// carries.
		if !res.IsError {
			t.Fatalf("RED (D-16 row 8, FR-100): set_config accepted a write AT tools.manifest, the parent of a "+
				"performance-only key — required by the spec: refused at or under the key. Got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "Settings →") {
			t.Errorf("refusal must point at Settings (scenario: names Settings → Limits or PUT /api/v1/performance): %s", res.ForLLM)
		}
		if !cfg.Tools.Manifest.Compressed {
			t.Errorf("refused write changed the config: Compressed must stay true")
		}
	})
}

// specPath is the approved spec the inventory is taken from (FR-101,
// spec :2000: the guard MUST enumerate every value the spec lets a person
// edit). The C-7 table's config keys are parsed from the document at run
// time — the inventory is the spec's, never this file's transcription —
// and the agent-tool field table's retention trio is mapped to its
// storage.retention.* paths (the spec names them undotted there; the
// dotted paths are the spec's own retention sections').
const specPath = "../../../docs/internal/specs/settings-redesign-spec.md"

// retentionTrio maps the agent-tool field table's three retention values to
// their dotted config paths (spec field table: "session_days,
// memory_retros_days, memory_retros_forever — user-only, because they
// delete data on a schedule"; the paths are the spec's own retention
// sections'). `keep_everything_forever` / `disabled` are NOT in the
// inventory: the spec classifies them "config-file only, no write path at
// all" — no Settings path, so FR-101's "lets a person edit" does not
// reach them (their refusal stays pinned below, outside the inventory).
var retentionTrio = []struct{ undotted, dotted string }{
	{"session_days", "storage.retention.session_days"},
	{"memory_retros_days", "storage.retention.memory_retros_days"},
	{"memory_retros_forever", "storage.retention.memory_retros_forever"},
}

// keyShape admits only lowercase dotted key shapes, so prose tokens
// (set_config, PUT /performance, performanceOnlyConfigKeys, `tools.` with
// nothing after the dot) never match.
var keyShape = regexp.MustCompile("`([a-z][a-z0-9_]*(?:\\.[a-z0-9_]+)+)`")

// specFieldTableKeys scans the WHOLE approved spec for field tables — every
// Markdown table whose header row names a config-key column — and returns
// the dotted keys their body rows carry (FR-101). Scanning the whole
// document, not only the C-7 section, is the F09 round-2 fix: a new editable
// field documented in any spec field table enters the inventory and must be
// classified here (check mutants N16/N17 document `gateway.future_setting`
// in a table outside C-7 and must fail). It fails loudly when the document
// is missing or a table yields nothing — a guard that silently reads
// nothing would be the false-green the guard exists to prevent.
func specFieldTableKeys(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("BLOCKED (FR-101): the approved spec %s could not be read — the inventory must come "+
			"from the spec's field tables, never from the code: %v", specPath, err)
	}
	var inventory []string
	seen := map[string]bool{}
	for _, table := range markdownTables(string(raw)) {
		header := strings.ToLower(table[0])
		if !strings.Contains(header, "config key") {
			continue
		}
		before := len(inventory)
		for _, line := range table[1:] {
			for _, m := range keyShape.FindAllStringSubmatch(line, -1) {
				if !seen[m[1]] {
					seen[m[1]] = true
					inventory = append(inventory, m[1])
				}
			}
		}
		if len(inventory) == before {
			t.Fatalf("BLOCKED (FR-101): a spec field table names a config-key column but yielded no key — "+
				"the parser misread it; reconcile: %s", table[0])
		}
	}
	if len(inventory) == 0 {
		t.Fatalf("BLOCKED (FR-101): the spec %s yielded no field-table keys — the guard cannot enumerate", specPath)
	}
	return inventory
}

// markdownTables splits a document into its Markdown tables: one slice per
// table, first element the header row.
func markdownTables(doc string) [][]string {
	var tables [][]string
	var current []string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			current = append(current, line)
			continue
		}
		if len(current) > 0 {
			tables = append(tables, current)
			current = nil
		}
	}
	if len(current) > 0 {
		tables = append(tables, current)
	}
	return tables
}

// c7Keys parses the C-7 rider section's own field table and requires it to
// yield exactly its seven Performance values — the spec oracle the guard's
// ten listed rows are cited against. A different count means the table moved
// or the parser misread it, and the guard must stop rather than compare
// against a wrong inventory.
func c7Keys(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("BLOCKED (FR-101): the approved spec %s could not be read: %v", specPath, err)
	}
	doc := string(raw)
	c7Start := strings.Index(doc, "### C-7")
	if c7Start < 0 {
		t.Fatalf("BLOCKED (FR-101): the spec's C-7 field table is gone from %s — the guard cannot enumerate", specPath)
	}
	c7 := doc[c7Start:]
	if next := strings.Index(c7, "\n### "); next >= 0 {
		c7 = c7[:next]
	}
	seen := map[string]bool{}
	var keys []string
	for _, m := range keyShape.FindAllStringSubmatch(c7, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			keys = append(keys, m[1])
		}
	}
	if len(keys) != 7 {
		t.Fatalf("BLOCKED (FR-101): the C-7 table yielded %d config keys (%v); want the seven Performance "+
			"values — the field table changed, so reconcile this guard with the spec", len(keys), keys)
	}
	return keys
}

// retentionDottedKeys verifies the agent-tool field-table line still names
// the three retention values and returns their dotted paths (spec field
// table: "user-only, because they delete data on a schedule").
func retentionDottedKeys(t *testing.T, doc string) []string {
	t.Helper()
	var keys []string
	for _, r := range retentionTrio {
		if !strings.Contains(doc, "`"+r.undotted+"`") {
			t.Fatalf("BLOCKED (FR-101): the spec's agent-tool field table no longer names %s — reconcile", r.undotted)
		}
		keys = append(keys, r.dotted)
	}
	return keys
}

// wireframeRow is one row of the wireframe control map (design-notes §2,
// "Every current control and its new place" — the spec's control inventory,
// transcribed; the spec's FR-059 and D-9 cite it). kind classifies the row
// for FR-101:
//
//	value      — a stored Settings value; keys[] name its dotted config key(s)
//	              from the spec where it names one, otherwise from the contract
//	              schema the spec's API table names for the section
//	action     — opens, checks, links or restarts; stores no Settings value
//	credential — lives in credentials.json (ADR-004), never in config.json
//	connection — a provider-connection field of the picker/configure sheets
//	readonly   — a display, copy button or link
//	out        — not a Settings surface in this package (the map's own
//	             Removed/Hidden/Moved-out statuses; merged rows are the Save
//	             bar itself)
type wireframeRow struct {
	row     int
	status  string
	control string
	kind    string
	keys    []string
	source  string
}

// wireframeMap is the 88-row control map, transcribed from design-notes §2
// (counted there "2026-11-19, after #1919": Kept 40 · Moved (within
// Settings) 29 · Moved out 8 · Merged into Save bar 3 · Removed 5 · Hidden
// 3). The transcription is checked against those counts below, so a stale
// transcription fails loudly instead of under-covering the inventory.
var wireframeMap = []wireframeRow{
	{1, "Kept", "Default model card — Change (model + effort)", "value", []string{"agents.defaults.default_model"}, "spec C-6 writer table (PUT /providers/default-model); contracts DefaultModelUpdateRequest"},
	{2, "Kept", `"No context length" link`, "action", nil, "a deep link, not a stored value"},
	{3, "Kept", "Connect a provider", "action", nil, "opens the picker Sheet"},
	{4, "Kept", "Provider row — Configure", "action", nil, "opens the configure Sheet"},
	{5, "Kept", "Provider row — Sign in / Manage", "credential", nil, "provider sign-in, not config.json"},
	{6, "Kept", "Provider row — Set as default model…", "action", nil, "drafts the row-1 value on the Save bar"},
	{7, "Kept", "Provider row — Check with my account", "action", nil, "a check, saves nothing"},
	{8, "Kept", "Sheet — AWS region", "connection", nil, "a provider-connection field of the configure Sheet"},
	{9, "Kept", "Sheet — API key + show/hide", "credential", nil, "credentials.json (ADR-004), never config.json"},
	{10, "Kept", "Sheet — Models (manual providers)", "connection", nil, "provider-connection models"},
	{11, "Kept", "Sheet — Test", "action", nil, "a connection test"},
	{12, "Kept", "Sheet — Remove provider", "action", nil, "the connection-removal flow"},
	{13, "Kept", "Sheet — Save / Save & Connect / Connect", "action", nil, "the Sheet's own save"},
	{14, "Moved", "MCP tool result cap", "value", []string{"context.mcp_result_cap"}, "spec-named (C-6 entry-key example)"},
	{15, "Moved", "Built-in tool result cap", "value", []string{"context.builtin_success_cap"}, "contracts ContextSettings.yaml (spec API table: PUT /settings/context)"},
	{16, "Moved", "Failed tool result cap", "value", []string{"context.builtin_failure_cap"}, "contracts ContextSettings.yaml"},
	{17, "Moved", "Tool-result share limit", "value", []string{"context.tool_result_share_fraction"}, "contracts ContextSettings.yaml"},
	{18, "Moved", "Ingest bound", "value", []string{"context.ingest_bound_bytes"}, "contracts ContextSettings.yaml"},
	{19, "Moved", "Default context window (+ Source line)", "value", []string{"context.default_context_window"}, "contracts ContextSettings.yaml"},
	{20, "Moved", "Model overrides (add / edit / remove rows)", "value", []string{"context.model_overrides"}, "contracts ContextSettings.yaml"},
	{21, "Merged", "Save button + SaveStatus", "out", nil, "the Save bar itself"},
	{22, "Kept", "Edit (Judge, Plan Supervisor, Omnipus)", "action", nil, "opens the shared agent editor (spec field table: no agent-managed object changes)"},
	{23, "Kept", "Program path (×3)", "value", []string{"sandbox.third_party_agents.clis.codex.path", "sandbox.third_party_agents.clis.claude-code.path", "sandbox.third_party_agents.clis.opencode.path"}, "spec :107 names the codex path; contracts ThirdPartyAgentSettings.yaml names the rows"},
	{24, "Kept", "Parameters (×3)", "value", []string{"sandbox.third_party_agents.clis.codex.preset_args", "sandbox.third_party_agents.clis.claude-code.preset_args", "sandbox.third_party_agents.clis.opencode.preset_args"}, "contracts ThirdPartyAgentSettings.yaml"},
	{25, "Kept", "Check CLI", "action", nil, "the map: renamed Check, saves nothing"},
	{26, "Merged", "Save", "out", nil, "the Save bar"},
	{27, "Moved out", "Web search — Default card Change", "out", nil, "Skills & Tools side panel (R30a)"},
	{28, "Moved out", "Web search — Fallback card Change", "out", nil, "same side panel"},
	{29, "Moved out", "Web search row — Add / Edit key", "out", nil, "same side panel"},
	{30, "Moved out", "Web search row — Check connection", "out", nil, "same side panel"},
	{31, "Moved out", "Web search row — Remove key", "out", nil, "same side panel"},
	{32, "Moved", "Voice — Set active", "value", []string{"integrations.active_transcriber"}, "the Active-transcriber write's saved-state key"},
	{33, "Moved", "Voice — Add / Edit key (Save & activate)", "credential", nil, "the voice key Sheet (credentials.json)"},
	{34, "Kept", "Run diagnostics", "action", nil, "runs the doctor check"},
	{35, "Kept", "Health issue jump links", "action", nil, "a link per finding"},
	{36, "Kept", "Auto-approve switch", "value", []string{"sandbox.auto_approve"}, "spec-named"},
	{37, "Kept", `"Always asks when set to Ask" list`, "value", []string{"sandbox.tool_policies"}, "the per-tool ask list is the config tool-policy ceiling"},
	{38, "Moved out", "Skill trust (3 radios)", "out", nil, "Skills & Tools (S-Q8, founder 2026-11-19)"},
	{39, "Moved out", "Tool Access — Global Policies grid", "out", nil, "Skills & Tools (R30)"},
	{40, "Moved", "SSRF proxy switch + refresh", "value", []string{"tools.exec.enable_proxy"}, "spec-named"},
	{41, "Moved", "Prompt injection defense (3 radios)", "value", []string{"sandbox.prompt_injection_level"}, "spec-named"},
	{42, "Moved", "Sandbox mode (3 radios)", "value", []string{"sandbox.mode"}, "spec-named"},
	{43, "Moved", "Filesystem model (2 radios)", "value", []string{"sandbox.filesystem_model"}, "spec-named"},
	{44, "Moved", "Shell workspace limit (2 radios)", "value", []string{"sandbox.workspace_path_guard"}, "spec-named"},
	{45, "Moved", "Allowed paths editor", "value", []string{"sandbox.allowed_paths"}, "spec-named"},
	{46, "Moved", "SSRF allow-internal presets + list", "value", []string{"sandbox.ssrf.allow_internal"}, "contracts SandboxConfig.yaml"},
	{47, "Moved", "Per-agent rate limits (2 numbers)", "value", []string{"sandbox.rate_limits.max_agent_llm_calls_per_hour", "sandbox.rate_limits.max_agent_tool_calls_per_minute"}, "contracts RateLimitConfig.yaml (spec C-4 writer list)"},
	{48, "Moved", "Audit log — View log (filters, refresh)", "action", nil, "opens the audit dialog"},
	{49, "Moved", "Rotate master key", "credential", nil, "Keys & secrets → Master key"},
	{50, "Moved", "Add key", "credential", nil, "Keys & secrets main action"},
	{51, "Moved", "Delete credential", "credential", nil, "Keys & secrets row Delete (B7: confirm only)"},
	{52, "Kept", "Bind address", "value", []string{"gateway.host"}, "spec names gateway.host (FR-063 new-address derivation)"},
	{53, "Kept", "Port", "value", []string{"gateway.port"}, "spec-named"},
	{54, "Kept", "Gateway token — Copy", "readonly", nil, "a copy button"},
	{55, "Kept", "Gateway token — Rotate", "credential", nil, "rotates the token credential"},
	{56, "Kept", "Preview server switch", "value", []string{"gateway.preview_enabled"}, "the config gateway section"},
	{57, "Kept", "Log level", "value", []string{"gateway.log_level"}, "spec-named (C-6 entry-key example)"},
	{58, "Kept", "Restart gateway", "action", nil, "the restart action"},
	{59, "Moved", "God Mode switch", "value", []string{"sandbox.god_mode"}, "spec-named"},
	{60, "Kept", "Remote access disclosure", "readonly", nil, "a disclosure, not a control"},
	{61, "Kept", "Retention period", "value", []string{"storage.retention.session_days"}, "spec agent-tool field table"},
	{62, "Removed", "Create backup", "out", nil, "#1919 (founder decision 2026-11-19)"},
	{63, "Removed", "Restore (per backup)", "out", nil, "#1919"},
	{64, "Kept", "Clear sessions", "action", nil, "the Danger zone action (FR-108 wording)"},
	{65, "Kept", "Auto recap", "value", []string{"agents.defaults.auto_recap_enabled"}, "contracts MemorySettings.yaml (spec API table: PUT /settings/memory)"},
	{66, "Kept", "Idle timeout", "value", []string{"agents.defaults.idle_timeout_minutes"}, "contracts MemorySettings.yaml"},
	{67, "Kept", "Bootstrap recap", "value", []string{"agents.defaults.bootstrap_recap_enabled"}, "contracts MemorySettings.yaml"},
	{68, "Kept", "Bootstrap recap rate limit", "value", []string{"agents.defaults.bootstrap_recap_max_per_minute"}, "contracts MemorySettings.yaml"},
	{69, "Kept", "Summarization model — Primary + effort", "value", []string{"agents.defaults.recap_model", "agents.defaults.recap_provider", "agents.defaults.recap_reasoning_effort"}, "contracts MemorySettings.yaml"},
	{70, "Kept", "Fallback models (add, order, remove, effort)", "value", []string{"agents.defaults.recap_fallback_models"}, "contracts MemorySettings.yaml"},
	{71, "Kept", "Recap limits (10 numbers + Reset)", "value", []string{"agents.defaults.recap_max_output_tokens", "agents.defaults.recap_input_tokens", "agents.defaults.recap_summary_words", "agents.defaults.recap_list_items", "agents.defaults.recap_fallback_context_chars", "agents.defaults.recap_timeout_seconds", "agents.defaults.recap_stream_retries", "agents.defaults.recap_retry_pause_ms", "agents.defaults.recap_chars_per_token", "agents.defaults.bootstrap_recap_min_spacing_seconds"}, "contracts MemorySettings.yaml (the ten numbers)"},
	{72, "Removed", "Session retention", "out", nil, "duplicate of row 61 (problem 1)"},
	{73, "Moved", "Memory retrospective retention", "value", []string{"storage.retention.memory_retros_days"}, "spec agent-tool field table"},
	{74, "Merged", "Save", "out", nil, "the Save bar"},
	{75, "Hidden", "Pair a device (show steps)", "out", nil, "#1916"},
	{76, "Hidden", "Pending request — Approve / Reject", "out", nil, "#1916"},
	{77, "Hidden", "Paired device row", "out", nil, "#1916"},
	{78, "Moved", "Max parallel agents", "value", []string{"performance.max_parallel_agents"}, "spec C-7 field table"},
	{79, "Removed", "Tool loading switch", "value", []string{"tools.manifest.compressed"}, "S-Q3: stays until R31 ships; C-7 row 6 pins the key"},
	{80, "Moved", "Tries per goal", "value", []string{"planning.goal_max_rounds"}, "spec C-7 field table"},
	{81, "Moved", "Idle reminder quiet window", "value", []string{"planning.goal_reminder_quiet_seconds"}, "spec C-7 field table"},
	{82, "Moved", "Stalled-work check-ins switch", "value", []string{"planning.goal_stalled_checkin_enabled"}, "spec C-7 field table"},
	{83, "Moved", "Stalled-work check-in interval", "value", []string{"planning.goal_stalled_checkin_minutes"}, "spec C-7 field table"},
	{84, "Moved", "Max tool calls per turn (+ lowering preview)", "value", []string{"agents.defaults.max_tool_iterations"}, "spec C-7 field table"},
	{85, "Removed", "Per-control hidden Save + debounce gate", "out", nil, "replaced by the Save bar (problem 2)"},
	{86, "Moved out", "Verbose chat", "out", nil, "Profile → Chat display; browser storage, not config.json"},
	{87, "Kept", "Copy system info", "readonly", nil, "a copy button"},
	{88, "Kept", "GitHub repository link", "readonly", nil, "a link"},
}

// wireframeValueKeys checks the transcription and returns the dotted keys of
// every map row that is a stored Settings value. The counts are the map's
// own (design-notes §2 count line); a drift fails loudly. Keys must not
// repeat inside the map — a duplicated key would silently stand in for a
// missing one in the both-ways comparison.
func wireframeValueKeys(t *testing.T) []string {
	t.Helper()
	statusCounts := map[string]int{}
	seenKeys := map[string]bool{}
	var keys []string
	for _, r := range wireframeMap {
		statusCounts[r.status]++
		if r.kind == "value" {
			if len(r.keys) == 0 {
				t.Fatalf("BLOCKED (FR-101): wireframe row %d (%s) is classified as a value with no key", r.row, r.control)
			}
			for _, k := range r.keys {
				if seenKeys[k] {
					t.Fatalf("BLOCKED (FR-101): wireframe row %d repeats key %s — a duplicate would stand in "+
						"for a missing row in the parity comparison", r.row, k)
				}
				seenKeys[k] = true
				keys = append(keys, k)
			}
		}
	}
	want := map[string]int{"Kept": 40, "Moved": 29, "Moved out": 8, "Merged": 3, "Removed": 5, "Hidden": 3}
	for status, n := range want {
		if statusCounts[status] != n {
			t.Fatalf("BLOCKED (FR-101): the wireframe transcription drifted from the map's own counts: "+
				"%s = %d, want %d — re-transcribe design-notes §2", status, statusCounts[status], n)
		}
	}
	if len(wireframeMap) != 88 {
		t.Fatalf("BLOCKED (FR-101): the wireframe transcription holds %d rows, want the map's 88", len(wireframeMap))
	}
	return keys
}

// fr101Inventory enumerates every Settings-editable value the guard must
// classify (FR-101), from two independent sources: the spec's field tables
// (whole-document scan + the agent-tool field table's retention trio) and
// the wireframe control map's value rows. Sources overlap by design (the
// same key documented in a spec table AND carried by a map row); a key
// found in both legs is deduplicated here, while a duplicate INSIDE one leg
// fails loudly (see wireframeValueKeys).
func fr101Inventory(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("BLOCKED (FR-101): the approved spec %s could not be read: %v", specPath, err)
	}
	doc := string(raw)

	c7 := c7Keys(t)
	tables := specFieldTableKeys(t)
	for _, k := range c7 {
		if !contains(tables, k) {
			t.Fatalf("BLOCKED (FR-101): the C-7 key %s is not yielded by the whole-document field-table scan", k)
		}
	}
	keys := append(append([]string{}, tables...), retentionDottedKeys(t, doc)...)
	keys = append(keys, wireframeValueKeys(t)...)
	seen := map[string]bool{}
	unique := keys[:0]
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			unique = append(unique, k)
		}
	}
	return unique
}

func contains(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

// parityViolations is the FR-101 completeness comparison (F09, round 2):
// exact set equality BOTH ways with duplicate protection. It reports a
// driven row the inventory does not list, an inventory value with no driven
// row (the unclassified addition FR-101 exists to catch), and ANY duplicate
// on either side — a duplicate row can no longer stand in for a missing one
// the way it could under the old membership+length check.
func parityViolations(inventory, keys []string) []string {
	var violations []string
	count := func(keys []string) map[string]int {
		m := map[string]int{}
		for _, k := range keys {
			m[k]++
		}
		return m
	}
	inventoryCount, rowCount := count(inventory), count(keys)
	for _, k := range inventory {
		if inventoryCount[k] > 1 {
			violations = append(violations, fmt.Sprintf("inventory lists %s %d times", k, inventoryCount[k]))
		}
		if rowCount[k] == 0 {
			violations = append(violations, fmt.Sprintf(
				"unclassified editable Settings value %s — no agent setter and no driven refusal row", k))
		}
	}
	for _, k := range keys {
		if rowCount[k] > 1 {
			violations = append(violations, fmt.Sprintf("the guard drives %s %d times", k, rowCount[k]))
		}
		if inventoryCount[k] == 0 {
			violations = append(violations, fmt.Sprintf("stale row %s — the spec's field tables and the wireframe map no longer name it", k))
		}
	}
	sort.Strings(violations)
	return violations
}

// guardRow is one driven row of the FR-101 guard. wantInRefusal pins a
// refusal family ("unknown config key", "not writable via this tool",
// "Settings → Limits", "Settings →"); an empty wantInRefusal marks a setter
// row whose receipt JSON is verified instead. value is a write that would
// change the stored value — the threat the refusals exist for (an agent or
// a stolen token raising its own limits, or deleting data on a schedule).
type guardRow struct {
	key           string
	value         any
	wantInRefusal string
}

// allGuardRows drives the real set_config tool once per inventory value.
// The ten spec-listed rows keep their spec-cited refusals verbatim (D-16,
// the agent-tool field table, #936/#904); every other row's outcome is a
// labeled characterization pin recorded from an observed set_config run —
// these document today's tool, they are not spec claims, and any drift the
// tool makes fails here loudly.
func allGuardRows() []guardRow {
	rows := []guardRow{
		// The spec's listed user-only reasons (kept verbatim from the round-1
		// guard; spec: retention "user-only, because they delete data on a
		// schedule", C-7 rider + D-16 rows 5-8 for #936, #904's screen naming).
		{"storage.retention.session_days", float64(30), "unknown config key"},
		{"storage.retention.memory_retros_days", float64(400), "unknown config key"},
		{"storage.retention.memory_retros_forever", true, "unknown config key"},
		{"performance.max_parallel_agents", float64(64), "unknown config key"},
		{"planning.goal_max_rounds", float64(99), "unknown config key"},
		{"planning.goal_reminder_quiet_seconds", float64(45), "unknown config key"},
		{"planning.goal_stalled_checkin_enabled", true, "unknown config key"},
		{"planning.goal_stalled_checkin_minutes", float64(30), "unknown config key"},
		{"tools.manifest.compressed", false, "Settings → Limits"},
		{"agents.defaults.max_tool_iterations", float64(900), "Settings →"},

		// Characterization pins — refused as "unknown config key" (observed;
		// not a spec-listed reason). The seven Context values and the Active
		// transcriber have no set_config write path today; the C-6 writer is
		// PUT /settings/context (FR-074) and the transcriber write is the
		// provider configure call.
		{"context.mcp_result_cap", float64(30), "unknown config key"},
		{"context.builtin_success_cap", float64(9), "unknown config key"},
		{"context.builtin_failure_cap", float64(11), "unknown config key"},
		{"context.tool_result_share_fraction", 0.6, "unknown config key"},
		{"context.ingest_bound_bytes", float64(1048577), "unknown config key"},
		{"context.default_context_window", float64(32000), "unknown config key"},
		{"context.model_overrides", []any{}, "unknown config key"},
		{"integrations.active_transcriber", "elevenlabs", "unknown config key"},

		// Characterization pins — refused as "not writable via this tool"
		// (observed; the sandbox namespace is the enforcement boundary, the
		// egress-proxy toggle and the CLI runner paths are runner-owned).
		{"sandbox.auto_approve", true, "not writable via this tool"},
		{"sandbox.prompt_injection_level", "high", "not writable via this tool"},
		{"sandbox.mode", "permissive", "not writable via this tool"},
		{"sandbox.filesystem_model", "open", "not writable via this tool"},
		{"sandbox.workspace_path_guard", "workspace-only", "not writable via this tool"},
		{"sandbox.allowed_paths", []any{"/x"}, "not writable via this tool"},
		{"sandbox.ssrf.allow_internal", []any{"10.0.0.0/8"}, "not writable via this tool"},
		{"sandbox.rate_limits.max_agent_llm_calls_per_hour", float64(120), "not writable via this tool"},
		{"sandbox.rate_limits.max_agent_tool_calls_per_minute", float64(75), "not writable via this tool"},
		{"sandbox.god_mode", true, "not writable via this tool"},
		{"sandbox.tool_policies", map[string]any{}, "not writable via this tool"},
		{"tools.exec.enable_proxy", true, "not writable via this tool"},
		{"sandbox.third_party_agents.clis.codex.path", "/usr/bin/env", "not writable via this tool"},
		{"sandbox.third_party_agents.clis.codex.preset_args", "--x", "not writable via this tool"},
		{"sandbox.third_party_agents.clis.claude-code.path", "/usr/bin/env", "not writable via this tool"},
		{"sandbox.third_party_agents.clis.claude-code.preset_args", "--y", "not writable via this tool"},
		{"sandbox.third_party_agents.clis.opencode.path", "/usr/bin/env", "not writable via this tool"},
		{"sandbox.third_party_agents.clis.opencode.preset_args", "--z", "not writable via this tool"},
	}
	// Setter rows (observed: set_config accepts these and returns the receipt
	// JSON). The spec's Hard-constraint line forbids ADDING a write path for
	// the spec-listed values; these keys are pre-existing writable config the
	// spec neither lists nor forbids, so the guard pins the receipt instead
	// of a refusal — a tool that starts refusing them is a parity break too.
	setters := []guardRow{
		{"agents.defaults.default_model", map[string]any{"provider": "prov-x", "model": "model-x"}, ""},
		{"gateway.host", "0.0.0.0", ""},
		{"gateway.port", float64(8081), ""},
		{"gateway.log_level", "debug", ""},
		{"gateway.preview_enabled", false, ""},
		{"agents.defaults.auto_recap_enabled", true, ""},
		{"agents.defaults.idle_timeout_minutes", float64(45), ""},
		{"agents.defaults.bootstrap_recap_enabled", true, ""},
		{"agents.defaults.bootstrap_recap_max_per_minute", float64(6), ""},
		{"agents.defaults.recap_model", "recap-x", ""},
		{"agents.defaults.recap_provider", "recap-provider-x", ""},
		{"agents.defaults.recap_reasoning_effort", "high", ""},
		{"agents.defaults.recap_fallback_models", []any{"fallback-x"}, ""},
		{"agents.defaults.recap_max_output_tokens", float64(700), ""},
		{"agents.defaults.recap_input_tokens", float64(100), ""},
		{"agents.defaults.recap_summary_words", float64(80), ""},
		{"agents.defaults.recap_list_items", float64(9), ""},
		{"agents.defaults.recap_fallback_context_chars", float64(400), ""},
		{"agents.defaults.recap_timeout_seconds", float64(50), ""},
		{"agents.defaults.recap_stream_retries", float64(3), ""},
		{"agents.defaults.recap_retry_pause_ms", float64(500), ""},
		{"agents.defaults.recap_chars_per_token", float64(4), ""},
		{"agents.defaults.bootstrap_recap_min_spacing_seconds", float64(120), ""},
	}
	return append(rows, setters...)
}

// guardRowKeys lists the driven rows' keys for the completeness comparison.
func guardRowKeys(rows []guardRow) []string {
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.key)
	}
	return keys
}

// TestSettingsAgentWriteParity — FR-101, order 16. The inventory is built
// from the two independent sources (specFieldTableKeys over the whole spec +
// the retention trio, and the wireframe control map's value rows) and the
// driven rows must match it EXACTLY, both ways, before anything runs. Every
// inventory value is then proven through the REAL set_config tool: refusal
// rows with their pinned family, setter rows with the documented receipt.
func TestSettingsAgentWriteParity(t *testing.T) {
	inventory := fr101Inventory(t)
	rows := allGuardRows()

	// F09 (round 2): exact set equality BOTH ways with duplicate protection —
	// a value added to either source without a row fails, a stale row fails,
	// and a duplicate row cannot stand in for a missing one.
	if violations := parityViolations(inventory, guardRowKeys(rows)); len(violations) > 0 {
		t.Fatalf("PARITY (FR-101): the guard's rows and the two-source inventory disagree:\n  %s\n"+
			"every value the spec lets a person edit needs an agent setter or a listed user-only reason "+
			"with a passing refusal; reconcile this guard with the spec's field tables and the wireframe map",
			strings.Join(violations, "\n  "))
	}

	for _, tc := range rows {
		t.Run(tc.key, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			compressedBefore := cfg.Tools.Manifest.Compressed
			iterationsBefore := cfg.Agents.Defaults.MaxToolIterations

			res := systools.NewConfigSetTool(deps).Execute(context.Background(),
				map[string]any{"key": tc.key, "value": tc.value})

			if tc.wantInRefusal != "" {
				if !res.IsError {
					t.Fatalf("PARITY (FR-101): set_config accepted %q — a Settings value with neither an agent setter "+
						"nor a refusal. Response: %s", tc.key, res.ForLLM)
				}
				if !strings.Contains(res.ForLLM, tc.wantInRefusal) {
					t.Errorf("refusal for %q must contain %q: %s", tc.key, tc.wantInRefusal, res.ForLLM)
				}
			} else {
				// Setter row: the receipt must be the documented JSON with the
				// written key, the sent value, and a previous value the write
				// actually changed (a receipt echoing the sent value as BOTH
				// before and after would hide a no-op write).
				if res.IsError {
					t.Fatalf("PARITY (FR-101): set_config refused %q (characterization pin says it is accepted). "+
						"Response: %s", tc.key, res.ForLLM)
				}
				var receipt map[string]any
				if err := json.Unmarshal([]byte(res.ForLLM), &receipt); err != nil {
					t.Fatalf("PARITY (FR-101): accepted write for %q must return the receipt JSON, got: %s (%v)",
						tc.key, res.ForLLM, err)
				}
				if got, _ := receipt["key"].(string); got != tc.key {
					t.Errorf("receipt key = %q, want %q", got, tc.key)
				}
				if !reflect.DeepEqual(receipt["value"], tc.value) {
					t.Errorf("receipt value = %#v, want the sent %#v", receipt["value"], tc.value)
				}
				if reflect.DeepEqual(receipt["previous_value"], tc.value) {
					t.Errorf("receipt previous_value equals the sent value for %q — the pin's value must differ "+
						"from the stored one, or the write proves nothing", tc.key)
				}
			}
			// Refusals happen before dotSet, so nothing can land; assert the
			// two representable fields anyway (belt and braces).
			if cfg.Tools.Manifest.Compressed != compressedBefore {
				t.Errorf("write changed tools.manifest.compressed")
			}
			if cfg.Agents.Defaults.MaxToolIterations != iterationsBefore {
				t.Errorf("write changed agents.defaults.max_tool_iterations")
			}
		})
	}
}

// TestSettingsAgentWriteParity_PositiveControl — the guard's instrument
// check (FR-101, CHECK F09 round 2): the REAL comparison over the REAL
// inventory and the REAL rows must be clean, and an UNCLASSIFIED editable
// Settings value must FAIL that real comparison — not a helper invocation.
// If this control ever passes, the comparison cannot detect the addition
// and the guard is decoration.
func TestSettingsAgentWriteParity_PositiveControl(t *testing.T) {
	inventory := fr101Inventory(t)
	rowKeys := guardRowKeys(allGuardRows())

	if violations := parityViolations(inventory, rowKeys); len(violations) > 0 {
		t.Fatalf("POSITIVE CONTROL (FR-101): the real inventory and the real guard rows already disagree, so "+
			"the control cannot run: %v", violations)
	}

	future := "gateway.future_setting" // synthetic: under no spec field table, unlisted
	if violations := parityViolations(inventory, append(append([]string{}, rowKeys...), future)); len(violations) == 0 {
		t.Fatalf("POSITIVE CONTROL (FR-101): the unclassified key %q was NOT reported — the parity comparison "+
			"cannot detect an unclassified addition and proves nothing", future)
	}

	// The other direction: an inventory value nobody drives must fail too.
	orphan := "context.orphan_setting"
	if violations := parityViolations(append(append([]string{}, inventory...), orphan), rowKeys); len(violations) == 0 {
		t.Fatalf("POSITIVE CONTROL (FR-101): the undriven inventory value %q was NOT reported — the parity "+
			"comparison cannot detect a value with no row and proves nothing", orphan)
	}
}
