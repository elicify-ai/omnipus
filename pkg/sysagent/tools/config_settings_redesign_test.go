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
//     inventory is parsed from the spec's own field tables at run time
//     (specInventory): a Settings value added there without an entry here
//     fails the count check — or an entry with neither an agent setter nor
//     a refusal that actually fires fails the refusal subtest. Per spec
//     (Hard-constraint line "The system MUST NOT add an agent tool or a new
//     set_config write path for any value in this spec"), no row has an
//     agent setter today; every row is a refusal row. The companion
//     positive control proves an unclassified key fails the comparison.
//
// Oracle: the spec's C-7 table, the agent-tool field table, D-16 and the BDD
// scenario "Performance values cannot be changed around Settings" (refused
// "with a message naming Settings → Limits or PUT /api/v1/performance").
// Expected values come from the spec, never from the tool's current output.

import (
	"context"
	"os"
	"regexp"
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

// specInventory parses the approved spec's field tables and returns the
// authoritative inventory of Settings-editable values (FR-101). It fails
// the test loudly when the document is missing or a table stops yielding
// the expected shape — a guard that silently reads nothing would be the
// false-green the guard exists to prevent (docs over memory: the doc is
// read fresh every run).
func specInventory(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("BLOCKED (FR-101): the approved spec %s could not be read — the inventory must come "+
			"from the spec's field tables, never from the code: %v", specPath, err)
	}
	doc := string(raw)
	// C-7 table: every backticked dotted config key between the C-7 rider
	// heading and the next heading. The regex admits only lowercase dotted
	// key shapes, so prose tokens (set_config, PUT /performance,
	// performanceOnlyConfigKeys, `tools.` with nothing after the dot) never
	// match.
	c7Start := strings.Index(doc, "### C-7")
	if c7Start < 0 {
		t.Fatalf("BLOCKED (FR-101): the spec's C-7 field table is gone from %s — the guard cannot enumerate", specPath)
	}
	c7 := doc[c7Start:]
	if next := strings.Index(c7, "\n### "); next >= 0 {
		c7 = c7[:next]
	}
	keyShape := regexp.MustCompile("`([a-z][a-z0-9_]*(?:\\.[a-z0-9_]+)+)`")
	seen := map[string]bool{}
	var inventory []string
	for _, m := range keyShape.FindAllStringSubmatch(c7, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			inventory = append(inventory, m[1])
		}
	}
	// The C-7 table must yield exactly its seven Performance values; a
	// different count means the table moved or the parser misread it, and
	// the guard must stop rather than compare against a wrong inventory.
	if len(inventory) != 7 {
		t.Fatalf("BLOCKED (FR-101): the C-7 table yielded %d config keys (%v); want the seven Performance "+
			"values — the field table changed, so reconcile this guard with the spec", len(inventory), inventory)
	}
	for _, r := range retentionTrio {
		// The field-table line must still name each retention value; the
		// mapping to its dotted path is cited there.
		if !strings.Contains(doc, "`"+r.undotted+"`") {
			t.Fatalf("BLOCKED (FR-101): the spec's agent-tool field table no longer names %s — reconcile", r.undotted)
		}
		inventory = append(inventory, r.dotted)
	}
	return inventory
}

// parityGaps is the FR-101 completeness comparison: every named key must be
// in the inventory; the returned slice names the UNCLASSIFIED ones — values
// the caller treats as editable that the spec's field tables neither list
// nor classify. An empty result means the enumeration covers the caller's
// universe.
func parityGaps(inventory []string, keys []string) []string {
	listed := map[string]bool{}
	for _, k := range inventory {
		listed[k] = true
	}
	var gaps []string
	for _, k := range keys {
		if !listed[k] {
			gaps = append(gaps, k)
		}
	}
	return gaps
}

// TestSettingsAgentWriteParity — FR-101, order 16. The inventory is parsed
// from the approved spec's field tables (C-7's seven Performance values +
// the field table's three retention values) and EVERY inventory value is
// proven refused through the REAL set_config tool with its listed user-only
// reason. No row has an agent setter: the spec forbids adding one ("The
// system MUST NOT add an agent tool or a new set_config write path for any
// value in this spec"). CHECK F09: the guard compares the inventory against
// exactly the rows driven here — a value added to the spec's tables fails
// the count check until it is reconciled here, and the companion positive
// control proves an unclassified key fails the comparison.
func TestSettingsAgentWriteParity(t *testing.T) {
	inventory := specInventory(t)

	// wantInRefusal pins the refusal family: "unknown config key" for the
	// not-a-known-prefix keys (spec: "not in knownConfigPrefixes"),
	// "Settings → Limits" for the #936 keys (D-16 row 7), "Settings →" where
	// the existing #904 refusal already names the screen. value is a write
	// that would change the stored value — the threat the refusals exist
	// for (an agent or a stolen token raising its own limits, or deleting
	// data on a schedule).
	rows := []struct {
		key           string
		value         any
		wantInRefusal string
	}{
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
	}

	// F09: the refusal rows and the spec's inventory must agree EXACTLY — a
	// row the spec's tables no longer name is stale, a spec value with no
	// row is the unclassified addition FR-101 exists to catch.
	if gaps := parityGaps(inventory, rowKeys(rows)); len(gaps) > 0 {
		t.Fatalf("PARITY (FR-101): unclassified editable Settings value(s) %v — every value the spec lets a "+
			"person edit needs an agent setter or a listed user-only reason with a passing refusal; "+
			"reconcile this guard with the spec's field tables", gaps)
	}
	if len(rows) != len(inventory) {
		t.Fatalf("PARITY (FR-101): the guard drives %d refusal rows for an inventory of %d — stale rows must "+
			"be removed or reconciled with the spec's field tables", len(rows), len(inventory))
	}

	for _, tc := range rows {
		t.Run(tc.key, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			compressedBefore := cfg.Tools.Manifest.Compressed
			iterationsBefore := cfg.Agents.Defaults.MaxToolIterations

			res := systools.NewConfigSetTool(deps).Execute(context.Background(),
				map[string]any{"key": tc.key, "value": tc.value})

			if !res.IsError {
				t.Fatalf("PARITY (FR-101): set_config accepted %q — a Settings value with neither an agent setter "+
					"nor a refusal. Response: %s", tc.key, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, tc.wantInRefusal) {
				t.Errorf("refusal for %q must contain %q: %s", tc.key, tc.wantInRefusal, res.ForLLM)
			}
			// The refusal happens before dotSet, so nothing can land; assert
			// the two representable fields anyway (belt and braces).
			if cfg.Tools.Manifest.Compressed != compressedBefore {
				t.Errorf("refused write changed tools.manifest.compressed")
			}
			if cfg.Agents.Defaults.MaxToolIterations != iterationsBefore {
				t.Errorf("refused write changed agents.defaults.max_tool_iterations")
			}
		})
	}
}

// rowKeys lists a refusal table's keys for the completeness comparison.
func rowKeys(rows []struct {
	key           string
	value         any
	wantInRefusal string
}) []string {
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.key)
	}
	return keys
}

// TestSettingsAgentWriteParity_PositiveControl — the guard's instrument
// check (FR-101, CHECK F09): an UNCLASSIFIED editable Settings value must
// FAIL the parity comparison. The synthetic key stands for the future
// Settings value whose row nobody added; if this control ever passes, the
// comparison cannot detect the addition and the guard is decoration.
func TestSettingsAgentWriteParity_PositiveControl(t *testing.T) {
	inventory := specInventory(t)
	future := "context.future_setting" // synthetic: under no spec field table, unlisted
	if gaps := parityGaps(inventory, []string{future}); len(gaps) == 0 {
		t.Fatalf("POSITIVE CONTROL (FR-101): the unclassified key %q was NOT reported — the parity comparison "+
			"cannot detect an unclassified addition and proves nothing", future)
	}
	// The inventory itself stays complete: no gap among its own keys.
	if gaps := parityGaps(inventory, inventory); len(gaps) != 0 {
		t.Fatalf("PARITY (FR-101): the spec's own inventory is incomplete against itself: %v", gaps)
	}
}
