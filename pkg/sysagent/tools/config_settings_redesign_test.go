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
//     CHECK R4 repairs (same author, part 19): the C-7 rider section's TWO
//     tables are parsed separately (the seven Performance values; the
//     ruling table's user-only values with `sandbox.rate_limits.*` and the
//     CLI-runner placeholders expanded); FR-101's closing sentence is
//     enforced both ways (an observed refusal no table lists is a parity
//     failure, and a listed value with no refusal row fails); and the two
//     unconditional accepted-setter invariants are restored beside the
//     stored-target readback (CHECK R4-01: an accepted write for one key
//     must not move either protected Performance value).
//
// Oracle: the spec's C-7 table, the agent-tool field table, D-16 and the BDD
// scenario "Performance values cannot be changed around Settings" (refused
// "with a message naming Settings → Limits or PUT /api/v1/performance").
// Expected values come from the spec, never from the tool's current output.
//
// Superseded in part by the Skills & Tools spec (#1963, C-22 row 3): D-16
// rows 7 and 8 and the tools.manifest.compressed parity row keep their
// refusal but now expect c22RetiredToolLoadingRefusal and no Settings path;
// the agents.defaults.max_tool_iterations rows are unchanged.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"gopkg.in/yaml.v3"
)

// c22RetiredToolLoadingRefusal is the refusal text the Skills & Tools spec
// (docs/internal/specs/skills-tools-redesign-spec.md: C-6, C-21 "retired
// manifest switches" row, C-22 row 3 "Moves the guard", BDD S-44) gives
// set_config for the retired tool-loading key tools.manifest.compressed. It
// supersedes the Settings spec's "Settings → Limits" wording for that key
// (D-16 rows 7 and 8): the key stays refused, only the text and the path it
// names move. The Performance row for agents.defaults.max_tool_iterations is
// not retired and keeps its Settings wording.
const c22RetiredToolLoadingRefusal = "This setting was replaced by levels in Skills & Tools."

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

	t.Run("D16 row 7: tools.manifest.compressed is refused with the C-22 text, not the retired Settings path", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		if !cfg.Tools.Manifest.Compressed {
			t.Fatalf("fixture: DefaultConfig().Tools.Manifest.Compressed must be true (pkg/config pins this default)")
		}
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "tools.manifest.compressed", "value": false})
		// Re-aimed to the Skills & Tools spec (C-22 row 3, C-6, S-44): the
		// refusal of D-16 row 7 stays; its text becomes "This setting was
		// replaced by levels in Skills & Tools." and it no longer names Settings.
		if !res.IsError {
			t.Fatalf("D-16 row 7 / C-22: set_config accepted tools.manifest.compressed — the key must stay refused, "+
				"now with the text %q. Got: %s", c22RetiredToolLoadingRefusal, res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, c22RetiredToolLoadingRefusal) {
			t.Errorf("refusal must carry the C-22 text %q: %s", c22RetiredToolLoadingRefusal, res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "Settings →") {
			t.Errorf("refusal must not name the retired Settings path (C-22 moved the guard): %s", res.ForLLM)
		}
		if !cfg.Tools.Manifest.Compressed {
			t.Errorf("refused write changed the config: Compressed must stay true")
		}
	})

	t.Run("D16 row 8: parent tools.manifest is refused at or under the key, with the C-22 text", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		if !cfg.Tools.Manifest.Compressed {
			t.Fatalf("fixture: DefaultConfig().Tools.Manifest.Compressed must be true")
		}
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "tools.manifest", "value": map[string]any{}})
		// Re-aimed to the Skills & Tools spec (C-22 row 3): D-16 row 8
		// ("refused — at or under the key") stays — a write at the parent of
		// the retired key is refused too, whatever the value carries — and the
		// refusal carries the C-22 text instead of pointing at Settings.
		if !res.IsError {
			t.Fatalf("D-16 row 8 / C-22: set_config accepted a write AT tools.manifest, the parent of a "+
				"retired tool-loading key — required: refused at or under the key, with the text %q. Got: %s",
				c22RetiredToolLoadingRefusal, res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, c22RetiredToolLoadingRefusal) {
			t.Errorf("refusal must carry the C-22 text %q: %s", c22RetiredToolLoadingRefusal, res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "Settings →") {
			t.Errorf("refusal must not name the retired Settings path (C-22 moved the guard): %s", res.ForLLM)
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

// c7Tables holds the C-7 rider section's two tables, parsed separately
// (CHECK R4-02: the section grew a second table — team-lead spec edits
// 1dd97e82bd/2edc763e13 — and the old whole-section scan broke the guard's
// premise). performance is the FIRST table's #936 rider list; userOnly is
// the SECOND table's "Other values a person edits that agents cannot write"
// (team-lead ruling 2026-11-21, closing CHECK R3-02), with the table's
// wildcard and placeholder tokens expanded to the concrete keys the guard
// drives.
type c7Tables struct {
	performance []string
	userOnly    []string
}

// The seven Performance values of the FIRST C-7 table, transcribed from the
// approved spec (C-7 #936 rider rows 1–7). The parser must yield exactly
// this set from the live document; any other count or name means the table
// moved and the guard stops instead of comparing against a wrong inventory.
var c7PerformanceKeys = []string{
	"performance.max_parallel_agents",
	"planning.goal_max_rounds",
	"planning.goal_reminder_quiet_seconds",
	"planning.goal_stalled_checkin_enabled",
	"planning.goal_stalled_checkin_minutes",
	"tools.manifest.compressed",
	"agents.defaults.max_tool_iterations",
}

// placeholderShape matches the second table's placeholder tokens — the
// CLI-runner row names `sandbox.third_party_agents.clis.<codex,
// claude-code, opencode>.path` — capturing head, placeholder names and tail.
var placeholderShape = regexp.MustCompile("`([a-z][a-z0-9_.]*)<([a-z0-9-, ]+)>([a-z0-9_.]*)`")

// bareSuffixShape matches the row's second token — the bare `.<suffix>` the
// CLI-runner row uses for the preset arguments ("…<codex, claude-code,
// opencode>.path` and `.preset_args`") — which carries the SAME placeholder
// names as the token before it.
var bareSuffixShape = regexp.MustCompile("`((?:\\.[a-z0-9_.]+))`")

// wildcardShape matches a backticked `prefix.*` token — the second table
// names the sandbox rate limits as `sandbox.rate_limits.*`.
var wildcardShape = regexp.MustCompile("`([a-z][a-z0-9_.]*)\\.\\*`")

// c7Keys parses the C-7 rider section's two tables separately. The FIRST
// table must yield exactly the seven Performance values above; the SECOND
// table yields its concrete backticked keys PLUS the expansions of its
// wildcard and placeholder tokens:
//
//   - `sandbox.rate_limits.*` expands against the contract schema the spec
//     names for these values (contracts/components/schemas/RateLimitConfig.yaml,
//     reached through the spec's C-4 writer list and the Settings section the
//     table's row names): every writable leaf property becomes
//     sandbox.rate_limits.<property>. The GET-only derived `enabled` is
//     excluded by its own schema description ("Only present in GET
//     responses") — it is not a stored value a person edits.
//   - `sandbox.third_party_agents.clis.<codex, claude-code, opencode>.path`
//     expands over the three named CLIs; the row's bare `.<suffix>` token
//     (`.preset_args`) expands over the same three names.
//
// The parsed set must equal the spec's transcription in c7UserOnlyKeys — a
// spec edit or a parser misread fails loudly instead of comparing against a
// wrong inventory.
func c7Keys(t *testing.T) c7Tables {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("BLOCKED (FR-101): the approved spec %s could not be read: %v", specPath, err)
	}
	doc := string(raw)
	c7Start := strings.Index(doc, "### C-7")
	if c7Start < 0 {
		t.Fatalf("BLOCKED (FR-101): the spec's C-7 section is gone from %s — the guard cannot enumerate", specPath)
	}
	c7 := doc[c7Start:]
	if next := strings.Index(c7, "\n### "); next >= 0 {
		c7 = c7[:next]
	}
	var perfTable, userTable []string
	for _, table := range markdownTables(c7) {
		header := strings.ToLower(table[0])
		switch {
		case strings.Contains(header, "config key") && strings.Contains(header, "put /config"):
			perfTable = table
		case strings.Contains(header, "user-only reason"):
			userTable = table
		}
	}
	if perfTable == nil || userTable == nil {
		t.Fatalf("BLOCKED (FR-101): the C-7 section must hold two tables — the Performance rider (its header "+
			"names a config key and PUT /config) and the user-only ruling table (its header names a "+
			"User-only reason column); found %d of them in %s", len(perfTable)+len(userTable), specPath)
	}

	// First table: exactly the seven Performance values.
	seen := map[string]bool{}
	var perf []string
	for _, line := range perfTable[1:] {
		for _, m := range keyShape.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				perf = append(perf, m[1])
			}
		}
	}
	if len(perf) != len(c7PerformanceKeys) {
		t.Fatalf("BLOCKED (FR-101): the C-7 FIRST table yielded %d config keys (%v); want exactly the seven "+
			"Performance values — the rider table changed, so reconcile this guard with the spec",
			len(perf), perf)
	}
	for _, want := range c7PerformanceKeys {
		if !contains(perf, want) {
			t.Fatalf("BLOCKED (FR-101): the C-7 FIRST table no longer names %s — reconcile this guard with the spec", want)
		}
	}

	// Second table: concrete keys, then the wildcard and placeholder tokens
	// in row order (the bare-suffix token binds to the placeholder before it).
	seen = map[string]bool{}
	var userOnly []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			userOnly = append(userOnly, k)
		}
	}
	var lastNames []string
	var lastHead string
	for _, line := range userTable[1:] {
		// A bare-suffix token binds to a placeholder token on the SAME row
		// only — names never leak across rows.
		lastNames, lastHead = nil, ""
		for _, m := range keyShape.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
		for _, m := range placeholderShape.FindAllStringSubmatch(line, -1) {
			lastNames, lastHead = nil, m[1]
			for _, name := range strings.Split(m[2], ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					t.Fatalf("BLOCKED (FR-101): an empty name inside the C-7 placeholder token %s", m[0])
				}
				lastNames = append(lastNames, name)
				add(m[1] + name + m[3])
			}
		}
		for _, m := range wildcardShape.FindAllStringSubmatch(line, -1) {
			for _, leaf := range rateLimitLeafKeys(t) {
				add(m[1] + "." + leaf)
			}
		}
		for _, m := range bareSuffixShape.FindAllStringSubmatch(line, -1) {
			if lastNames == nil {
				t.Fatalf("BLOCKED (FR-101): the bare suffix token %s has no placeholder token before it — "+
					"the expansion has no names to fan out over", m[0])
			}
			for _, name := range lastNames {
				add(lastHead + name + m[1])
			}
		}
	}
	if !equalSets(userOnly, c7UserOnlyKeys) {
		t.Fatalf("BLOCKED (FR-101): the C-7 SECOND table (with the wildcard and placeholder expansions) "+
			"yielded %v; want the ruling's listed values %v — the ruling table or the expansion changed, "+
			"so reconcile this guard with the spec", userOnly, c7UserOnlyKeys)
	}
	return c7Tables{performance: perf, userOnly: userOnly}
}

// c7UserOnlyKeys is the SECOND C-7 table's listed values, transcribed from
// the approved spec (team-lead ruling 2026-11-21): the seven Context values
// of its first row, the Active transcriber, the Sandbox row's concrete keys
// plus `sandbox.rate_limits.*` expanded over RateLimitConfig.yaml's two
// writable leaves (max_agent_llm_calls_per_hour, max_agent_tool_calls_per_minute),
// the egress proxy switch, and the CLI-runner row's three CLIs × {path,
// preset_args}.
var c7UserOnlyKeys = []string{
	"context.mcp_result_cap",
	"context.builtin_success_cap",
	"context.builtin_failure_cap",
	"context.tool_result_share_fraction",
	"context.ingest_bound_bytes",
	"context.default_context_window",
	"context.model_overrides",
	"integrations.active_transcriber",
	"sandbox.auto_approve",
	"sandbox.prompt_injection_level",
	"sandbox.mode",
	"sandbox.filesystem_model",
	"sandbox.workspace_path_guard",
	"sandbox.allowed_paths",
	"sandbox.ssrf.allow_internal",
	"sandbox.rate_limits.max_agent_llm_calls_per_hour",
	"sandbox.rate_limits.max_agent_tool_calls_per_minute",
	"sandbox.god_mode",
	"sandbox.tool_policies",
	"tools.exec.enable_proxy",
	"sandbox.third_party_agents.clis.codex.path",
	"sandbox.third_party_agents.clis.codex.preset_args",
	"sandbox.third_party_agents.clis.claude-code.path",
	"sandbox.third_party_agents.clis.claude-code.preset_args",
	"sandbox.third_party_agents.clis.opencode.path",
	"sandbox.third_party_agents.clis.opencode.preset_args",
}

// rateLimitContractPath is the contract schema the sandbox rate-limit values
// are stored under — the wildcard expansion source named in c7Keys.
const rateLimitContractPath = "../../../contracts/components/schemas/RateLimitConfig.yaml"

// rateLimitLeafKeys reads the contract schema and returns its writable leaf
// property names, sorted — the `sandbox.rate_limits.*` expansion. A property
// whose own description says it is GET-only ("Only present in GET responses",
// the derived `enabled`) is not a stored value and is excluded.
func rateLimitLeafKeys(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(rateLimitContractPath)
	if err != nil {
		t.Fatalf("BLOCKED (FR-101): the rate-limit contract %s could not be read — the `sandbox.rate_limits.*` "+
			"wildcard expands against it: %v", rateLimitContractPath, err)
	}
	var schema struct {
		Properties map[string]struct {
			Description string `yaml:"description"`
		} `yaml:"properties"`
	}
	if err := yaml.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("BLOCKED (FR-101): the rate-limit contract %s did not parse: %v", rateLimitContractPath, err)
	}
	var leaves []string
	for name, prop := range schema.Properties {
		if strings.Contains(prop.Description, "Only present in GET responses") {
			continue
		}
		leaves = append(leaves, name)
	}
	if len(leaves) == 0 {
		t.Fatalf("BLOCKED (FR-101): the rate-limit contract %s yielded no writable leaf properties — "+
			"the wildcard expansion cannot stand in for nothing", rateLimitContractPath)
	}
	sort.Strings(leaves)
	return leaves
}

// equalSets reports whether the two slices hold the same keys, ignoring
// order and duplicates.
func equalSets(a, b []string) bool {
	set := map[string]int{}
	for _, k := range a {
		set[k]++
	}
	for _, k := range b {
		set[k]--
		if set[k] < 0 {
			return false
		}
	}
	for _, n := range set {
		if n != 0 {
			return false
		}
	}
	return true
}

// retentionDottedKeys verifies the agent-tool field-table line still names
// the three retention values and returns their dotted paths (spec field
// table: "user-only, because they delete data on a schedule").
func retentionDottedKeys(t *testing.T, doc string) []string {
	t.Helper()
	keys := make([]string, 0, len(retentionTrio))
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
	// The FIRST table's concrete keys must also surface in the whole-document
	// field-table scan. The SECOND table's wildcard and placeholder tokens
	// (`sandbox.rate_limits.*`, the `<codex, claude-code, opencode>` fans)
	// exist only in the C-7 ruling table — keyShape cannot see them — so
	// their expansions are added to the inventory directly below instead.
	for _, k := range c7.performance {
		if !contains(tables, k) {
			t.Fatalf("BLOCKED (FR-101): the C-7 Performance key %s is not yielded by the whole-document field-table scan", k)
		}
	}
	keys := append(append([]string{}, tables...), retentionDottedKeys(t, doc)...)
	keys = append(keys, c7.userOnly...)
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

// listedUserOnlyReasons — FR-101's "listed user-only reason" set: the two
// C-7 tables (the FIRST table's seven Performance values and the SECOND
// table's ruling values, wildcard and placeholder tokens expanded) plus the
// agent-tool field table's retention trio, which FR-101's sentence lists in
// the same breath ("retention values: user-only, 'deletes data on a
// schedule'").
func listedUserOnlyReasons(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("BLOCKED (FR-101): the approved spec %s could not be read: %v", specPath, err)
	}
	c7 := c7Keys(t)
	keys := append(append([]string{}, c7.performance...), c7.userOnly...)
	return append(keys, retentionDottedKeys(t, string(raw))...)
}

// refusalListingViolations — FR-101's closing sentence (team-lead spec edit
// 1dd97e82bd, CHECK R4-02): "A refusal the test observes but no C-7 table
// lists is a parity failure, not a pass." BOTH directions fail: a refusal
// row whose key no listed reason covers is a parity failure, and a listed
// key with no refusal row is a listing the guard never honors through the
// real tool.
func refusalListingViolations(listed []string, rows []guardRow) []string {
	var violations []string
	drivenRefusals := map[string]bool{}
	for _, r := range rows {
		if r.wantInRefusal == "" {
			continue
		}
		drivenRefusals[r.key] = true
		if !contains(listed, r.key) {
			violations = append(violations, fmt.Sprintf(
				"refusal row %s observes a refusal no C-7 table or retention listing covers (FR-101: a refusal the "+
					"test observes but no C-7 table lists is a parity failure)", r.key))
		}
	}
	for _, k := range listed {
		if !drivenRefusals[k] {
			violations = append(violations, fmt.Sprintf(
				"listed user-only value %s has no refusal row — a listing is pinned only by a passing refusal "+
					"through the real set_config tool", k))
		}
	}
	sort.Strings(violations)
	return violations
}

// guardRow is one driven row of the FR-101 guard. wantInRefusal pins a
// refusal family ("unknown config key", "not writable via this tool",
// c22RetiredToolLoadingRefusal, "Settings →"); an empty wantInRefusal marks a setter
// row whose receipt JSON is verified instead. value is a write that would
// change the stored value — the threat the refusals exist for (an agent or
// a stolen token raising its own limits, or deleting data on a schedule).
type guardRow struct {
	key           string
	value         any
	wantInRefusal string
}

// storedAsOverrides names the rows whose contract-stored shape differs from
// the accepted request shorthand; the R3-02 readback asserts THIS shape for
// them. The only entry is recap_fallback_models: the accepted request carries
// plain slugs, while the stored contract type — MemorySettings.yaml items →
// FallbackModel.yaml — is the object form {model, provider?, reasoning_effort?}.
// The expectation comes from those schemas, never from the tool's behaviour.
var storedAsOverrides = map[string]any{
	"agents.defaults.recap_fallback_models": []any{map[string]any{"model": "fallback-x"}},
}

// allGuardRows drives the real set_config tool once per inventory value.
// The ten spec-listed rows keep their spec-cited refusals verbatim (D-16,
// the agent-tool field table, #936/#904); every other row's outcome is a
// labeled characterization pin recorded from an observed set_config run —
// these document today's tool, they are not spec claims, and any drift the
// tool makes fails here loudly.
func allGuardRows() []guardRow {
	refusals := []guardRow{
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
		{"tools.manifest.compressed", false, c22RetiredToolLoadingRefusal},
		{"agents.defaults.max_tool_iterations", float64(900), "Settings →"},

		// The second C-7 table's listed user-only reasons (team-lead ruling
		// 2026-11-21, closing CHECK R3-02 — no longer characterization pins:
		// FR-101 now names these rows' listing, and a refusal the tables do
		// not list is a parity failure, not a pass). The refusal FAMILY each
		// row pins below is the family the tool shows today; the spec's
		// listing is the reason, the observed family stays the receipt.
		// The seven Context values: "They bound how much tool output and
		// history an agent itself receives; like the Performance values, an
		// agent must not change its own limits." Their write path is
		// PUT /settings/context (FR-074), the transcriber's is the provider
		// configure call.
		{"context.mcp_result_cap", float64(30), "unknown config key"},
		{"context.builtin_success_cap", float64(9), "unknown config key"},
		{"context.builtin_failure_cap", float64(11), "unknown config key"},
		{"context.tool_result_share_fraction", 0.6, "unknown config key"},
		{"context.ingest_bound_bytes", float64(1048577), "unknown config key"},
		{"context.default_context_window", float64(32000), "unknown config key"},
		{"context.model_overrides", []any{}, "unknown config key"},
		// Active transcriber: "It chooses which outside service receives the
		// person's voice recordings; that is the person's choice, behind the
		// password."
		{"integrations.active_transcriber", "elevenlabs", "unknown config key"},

		// The Sandbox row: "The sandbox is the boundary that confines agents;
		// an agent must never loosen its own confinement" — the concrete
		// keys the ruling table names plus `sandbox.rate_limits.*` expanded
		// over RateLimitConfig.yaml's writable leaves.
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
		// The egress proxy switch ("Part of the same boundary: it controls
		// how an agent's shell commands reach the network") and the
		// CLI-runner row's placeholder expansion (`sandbox.third_party_agents.
		// clis.<codex, claude-code, opencode>.path` / `.preset_args`: "They
		// name the program the app starts for a worker; an agent that could
		// change them could run any program outside its tool policy").
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
	rows := make([]guardRow, 0, len(refusals)+len(setters))
	rows = append(rows, refusals...)
	rows = append(rows, setters...)
	return rows
}

// guardRowKeys lists the driven rows' keys for the completeness comparison.
func guardRowKeys(rows []guardRow) []string {
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.key)
	}
	return keys
}

// storedValueAt reads a dotted key back out of the STORED config, through a
// plain JSON round trip in the test — deliberately not the tool's own dotGet,
// so a mutant that breaks the write and the tool's read helper together still
// cannot hide (CHECK R3-02: the parity guard must certify the real effect,
// not a receipt). The inventory's keys have no channel-namespace segments, so
// a plain split resolves every path the guard drives.
func storedValueAt(t *testing.T, cfg *config.Config, key string) any {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("stored config did not marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("stored config did not unmarshal to a map: %v", err)
	}
	cur := any(m)
	for _, seg := range strings.Split(key, ".") {
		asMap, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("stored config has no object at a segment of %q", key)
		}
		v, ok := asMap[seg]
		if !ok {
			t.Fatalf("stored config has no key %q", key)
		}
		cur = v
	}
	return cur
}

// TestSettingsAgentWriteParity — FR-101, order 16. The inventory is built
// from the two independent sources (specFieldTableKeys over the whole spec +
// the retention trio, and the wireframe control map's value rows) and the
// driven rows must match it EXACTLY, both ways, before anything runs. Every
// inventory value is then proven through the REAL set_config tool: refusal
// rows with their pinned family, setter rows with the documented receipt —
// and, since CHECK R3-02 (mutant N36: a receipt that answers while the port
// write is undone), the receipt alone is never the proof. A setter row reads
// the stored config back and asserts the new value; a refusal asserts the
// stored config is byte-identical to before.
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

	// R4-02 (FR-101's closing sentence): every driven refusal must be listed
	// by one of the two C-7 tables or the retention listing, and every listed
	// value must have its refusal row — an observed-but-unlisted refusal is a
	// parity failure, not a pass.
	if violations := refusalListingViolations(listedUserOnlyReasons(t), rows); len(violations) > 0 {
		t.Fatalf("PARITY (FR-101): the driven refusals and the spec's listed user-only reasons disagree:\n  %s\n"+
			"a refusal the test observes but no C-7 table lists is a parity failure, not a pass; "+
			"reconcile this guard with the two C-7 tables and the agent-tool field table",
			strings.Join(violations, "\n  "))
	}

	for _, tc := range rows {
		t.Run(tc.key, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			// The stored config before the write, marshaled once — the
			// refusal-unchanged oracle (any JSON-visible field that moves is a
			// changed state, whatever the receipt claims) — plus the two
			// C-7-protected Performance values the accepted-setter branch
			// asserts below (restored, CHECK R4-01).
			compressedBefore := cfg.Tools.Manifest.Compressed
			iterationsBefore := cfg.Agents.Defaults.MaxToolIterations
			before, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("fixture config did not marshal: %v", err)
			}

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
				// C-22: a retired tool-loading key's refusal says "replaced by
				// levels in Skills & Tools" and never names the retired Settings path.
				if tc.wantInRefusal == c22RetiredToolLoadingRefusal && strings.Contains(res.ForLLM, "Settings →") {
					t.Errorf("refusal for %q must not name the retired Settings path (C-22 moved the guard): %s", tc.key, res.ForLLM)
				}
				// R3-02: after a refusal, the state is unchanged. Replaces the
				// old two-field spot check (tools.manifest.compressed and
				// agents.defaults.max_tool_iterations) with the whole config —
				// strictly stronger; both replaced checks are subsumed.
				after, err := json.Marshal(cfg)
				if err != nil {
					t.Fatalf("stored config did not marshal after the refusal: %v", err)
				}
				if !bytes.Equal(before, after) {
					t.Errorf("refused write for %q changed the stored config (FR-101: a refusal stores nothing)", tc.key)
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
				// R3-02: the receipt is not the effect — read the stored
				// config back and assert the sent value landed. (Mutant N36
				// answered a correct receipt while restoring the old port.)
				// storedAsOverrides carries the rows whose contract-stored
				// shape differs from the accepted request shorthand.
				wantStored := tc.value
				if override, ok := storedAsOverrides[tc.key]; ok {
					wantStored = override
				}
				if got := storedValueAt(t, cfg, tc.key); !reflect.DeepEqual(got, wantStored) {
					t.Errorf("stored config holds %#v at %q after an accepted write, want %#v",
						got, tc.key, wantStored)
				}
				// R4-01 (restored — part 17 dropped these two when it moved
				// the whole-config comparison into the refusal branch): an
				// accepted write for ONE key must not move either protected
				// Performance value. The stored-target readback above cannot
				// see a side effect on a different key, and C-7 gives these
				// values exactly one write path (Settings → Limits, with the
				// password) — set_config is not it, for the target or as a
				// side effect of another accepted write.
				if cfg.Tools.Manifest.Compressed != compressedBefore {
					t.Errorf("accepted write for %q changed tools.manifest.compressed "+
						"(C-7 #936: a Performance value has one write path and set_config is not it)", tc.key)
				}
				if cfg.Agents.Defaults.MaxToolIterations != iterationsBefore {
					t.Errorf("accepted write for %q changed agents.defaults.max_tool_iterations "+
						"(C-7 #936/#904: the protected default must not move)", tc.key)
				}
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

	// R4-02: the refusal-listing check's own instrument arms, over the REAL
	// listed set and the REAL rows. A refusal row no table lists must fail
	// it, and a listed value with no refusal row must fail it — if either
	// passes, the check cannot see the fault FR-101's closing sentence names.
	listed := listedUserOnlyReasons(t)
	if violations := refusalListingViolations(listed, allGuardRows()); len(violations) > 0 {
		t.Fatalf("POSITIVE CONTROL (FR-101): the real listed set and the real guard rows already disagree, so "+
			"the listing check cannot run: %v", violations)
	}
	unlistedRow := append(append([]guardRow{}, allGuardRows()...), guardRow{key: "gateway.future_setting", value: "x", wantInRefusal: "unknown config key"})
	if violations := refusalListingViolations(listed, unlistedRow); len(violations) == 0 {
		t.Fatalf("POSITIVE CONTROL (FR-101): an unlisted refusal row was NOT reported — the listing check cannot " +
			"detect a refusal no C-7 table lists and proves nothing")
	}
	if violations := refusalListingViolations(append(append([]string{}, listed...), "context.orphan_setting"), allGuardRows()); len(violations) == 0 {
		t.Fatalf("POSITIVE CONTROL (FR-101): a listed value with no refusal row was NOT reported — the listing " +
			"check cannot detect an unpinned listing and proves nothing")
	}
}
