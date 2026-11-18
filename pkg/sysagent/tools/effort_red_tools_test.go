// Omnipus — RED tests for the effort-levels feature (train T7),
// system agent tools area (pkg/sysagent/tools).
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// RED receipts: the TestT_TL* tests here are written against
// designs-20261029/effort-levels-spec-r10.md BEFORE the feature exists in this
// package. RED tests are expected to FAIL today with a missing-feature reason
// (missing key/field, dropped or refused value, missing refusal); PIN tests are
// expected to PASS today and must keep passing once the feature lands.
// Expected values come from the spec's tables (D1, D8) and pinned sentences,
// never from code. New wire/storage fields are asserted at the JSON level
// (marshal to map[string]any) so the file compiles against today's structs.
//
// Every tool call goes through the production tool registry
// (systools.BuildRegistry → ToolRegistry.Execute), the seam the spec names for
// the agent tools (T-A1/T-A2/T-L5), never a direct unregistered call.

package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---- spec fixture constants (from the spec tables, not from code) ----

// S-67's fixture slots: the primary is OpenRouter deepseek/deepseek-v4.1-flash
// (D8 row 1: levels none, low, high, max → options provider_default + the four),
// fallback 1 is OpenRouter minimax/minimax-m3 (D8 row 2: switch only), and
// fallback 2 is a custom row with no catalog data (D8 row 6: null).
const (
	tlPrimaryModel      = "deepseek/deepseek-v4.1-flash"
	tlFallbackSwitch    = "minimax/minimax-m3"
	tlFallbackCustom    = "local/custom-row"
	tlPrimaryProvider   = "openrouter"
	tlFallback2Provider = "custom"
)

// tlRedacted mirrors the config tool's redaction marker (S-92).
const tlRedacted = "[REDACTED]"

// D1 rows 8 and 9: the 32-character maximum and its 33-character overrun.
var (
	tlEffortMaxLen  = strings.Repeat("x", 32)
	tlEffortOverLen = strings.Repeat("x", 33)
)

// Pinned machine-verifiable refusal sentences (effort-levels-spec-r10.md,
// "Machine-verifiable constraints" and the S-77 parity row).
const (
	tlNullEffortMsg = "reasoning_effort must be a string; send \"\" to clear it"
	tlLenEffortMsg  = "reasoning_effort must be at most 32 characters"
	tlTypeMsgCore   = "must be an actual JSON object or array, not a string containing JSON"
	tlExtCLIMsg     = "reasoning_effort does not apply to external-CLI workers; they use their own CLI's model settings."
)

// ---- helpers (tl-prefixed to stay clear of the shared harness) ----

// tlPersistentDeps keeps the normal entity/session fixture, but configuration
// saves are real disk writes (CHECK-11), not the shared harness's no-op.
func tlPersistentDeps(t *testing.T) (*systools.Deps, *config.Config) {
	t.Helper()
	tlInstallEffortCatalog(t)
	deps, cfg := newTestDeps(t)
	deps.SaveConfigLocked = func(c *config.Config) error { return config.SaveConfig(deps.ConfigPath, c) }
	// S-76/S-77: get_agent fields ["all"] reads tool_permissions from the
	// central tool metadata catalog (r17 §7.12.1); with none wired the read
	// is refused CATALOG_UNAVAILABLE. Wire the real builtin metadata, the
	// same catalog defaultToolsExistingBody uses.
	metadata := append(systools.AllTools(deps), tools.GeneralBuiltinMetadata()...)
	deps.ToolCatalog = func() []tools.Tool { return metadata }
	if err := config.SaveConfig(deps.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	return deps, cfg
}

func tlDurableValue(t *testing.T, deps *systools.Deps, key string) any {
	t.Helper()
	loaded, err := config.LoadConfig(deps.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	fresh := *deps
	fresh.GetCfg = func() *config.Config { return loaded }
	return tlConfigValue(t, tlGetConfig(t, tlRegistry(t, &fresh), key))
}

func tlRegistry(t *testing.T, deps *systools.Deps) *tools.ToolRegistry {
	t.Helper()
	return systools.BuildRegistry(deps)
}

func tlExec(t *testing.T, reg *tools.ToolRegistry, name string, args map[string]any) *tools.ToolResult {
	t.Helper()
	return reg.Execute(context.Background(), name, args)
}

func tlSetConfig(t *testing.T, reg *tools.ToolRegistry, key string, value any) *tools.ToolResult {
	t.Helper()
	return tlExec(t, reg, "set_config", map[string]any{"key": key, "value": value})
}

func tlGetConfig(t *testing.T, reg *tools.ToolRegistry, key string) *tools.ToolResult {
	t.Helper()
	return tlExec(t, reg, "get_config", map[string]any{"key": key})
}

// tlConfigValue returns the "value" member of a successful get_config result.
func tlConfigValue(t *testing.T, res *tools.ToolResult) any {
	t.Helper()
	m := parseSuccess(t, res.ForLLM)
	value, ok := m["value"]
	if !ok {
		t.Fatalf("get_config result carries no value member: %s", res.ForLLM)
	}
	return value
}

// tlErrorCode returns a tool refusal's error code ("" when the refusal is the
// registry's argument-validation normalization, which carries no code).
func tlErrorCode(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	m := parseError(t, res.ForLLM)
	block, _ := m["error"].(map[string]any)
	code, _ := block["code"].(string)
	return code
}

// tlRefusalText returns the human-readable refusal text whether the result is
// a tool errorJSON envelope, a change tool's plain-text normalization, or the
// registry's raw argument-validation refusal on a read tool.
func tlRefusalText(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &m); err == nil {
		if block, ok := m["error"].(map[string]any); ok {
			if msg, ok := block["message"].(string); ok && msg != "" {
				return msg
			}
		}
	}
	return res.ForLLM
}

// tlReadEntityJSON reads the agent's stored entity record at the JSON level
// (ADR-054: $OMNIPUS_HOME/entities/agents/<id>.json) so assertions on fields
// that do not exist as Go struct members yet still compile.
func tlReadEntityJSON(t *testing.T, home, id string) map[string]any {
	t.Helper()
	path := filepath.Join(home, "entities", "agents", id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read agent entity record %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("agent entity record %s is not a JSON object: %v", path, err)
	}
	return m
}

// tlIDFromCreate extracts the new agent's id from a create_agent result.
func tlIDFromCreate(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	payload := parseSuccess(t, res.ForLLM)
	id, _ := payload["id"].(string)
	if id == "" {
		t.Fatalf("create_agent result carries no id: %s", res.ForLLM)
	}
	return id
}

// tlCreateMainAgent creates a native Main agent with the given primary model
// and provider (fallbacks may be nil) and returns its id.
func tlCreateMainAgent(t *testing.T, reg *tools.ToolRegistry, name, model, provider string, fallbacks any) string {
	t.Helper()
	systools.UseNameKeyedAgentIDsForTest(t)
	args := map[string]any{
		"name":        name,
		"description": "RED fixture for the effort-levels spec",
		"soul":        "You are a RED test fixture agent.",
		"model":       model,
		"provider":    provider,
	}
	if fallbacks != nil {
		args["fallback_models"] = fallbacks
	}
	res := tlExec(t, reg, "create_agent", args)
	if res.IsError {
		t.Fatalf("test setup: create_agent(%s) failed: %s", name, res.ForLLM)
	}
	return tlIDFromCreate(t, res)
}

// tlCreateExternalCLIWorker creates a subagent_3p worker (S-77) and returns
// its id.
func tlCreateExternalCLIWorker(t *testing.T, reg *tools.ToolRegistry, name string) string {
	t.Helper()
	systools.UseNameKeyedAgentIDsForTest(t)
	res := tlExec(t, reg, "create_agent", map[string]any{
		"name":        name,
		"description": "RED fixture: external-CLI worker",
		"model":       tlPrimaryModel,
		"provider":    tlPrimaryProvider,
		"agent_type":  "subagent_3p",
		"cli":         "claude-code",
	})
	if res.IsError {
		t.Fatalf("test setup: create_agent(subagent_3p) failed: %s", res.ForLLM)
	}
	return tlIDFromCreate(t, res)
}

// tlWantStringSlice asserts got is exactly the wanted list of strings.
func tlWantStringSlice(t *testing.T, what string, got any, want ...string) {
	t.Helper()
	list, ok := got.([]any)
	if !ok {
		t.Fatalf("%s = %#v (%T), want the JSON array %v", what, got, got, want)
	}
	if len(list) != len(want) {
		t.Fatalf("%s = %#v, want %v", what, list, want)
	}
	for i, w := range want {
		if list[i] != w {
			t.Fatalf("%s[%d] = %#v, want %q (full: %#v vs %v)", what, i, list[i], w, list, want)
		}
	}
}

func tlStringListContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ---- Group K: agent tools ----

// TestT_TL63_UpdateAgentSavesAndReadsEffort covers S-63: update_agent accepts
// an offered per-agent primary-model effort and a fallback effort, reports
// them as changed, and a later get_agent reads the same values back.
//
// Traces to: effort-levels-spec-r10.md S-63
func TestT_TL63_UpdateAgentSavesAndReadsEffort(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)
	id := tlCreateMainAgent(t, reg, "Effort Bot", tlPrimaryModel, tlPrimaryProvider, nil)
	rev := currentAgentRevision(t, deps, id)

	res := tlExec(t, reg, "update_agent", map[string]any{
		"id":               id,
		"revision":         rev,
		"reasoning_effort": "high",
		"fallback_models": []any{
			map[string]any{"provider": tlPrimaryProvider, "model": tlFallbackSwitch, "reasoning_effort": "on"},
		},
	})
	if res.IsError {
		t.Fatalf("RED: update_agent refused an offered reasoning_effort — per-agent effort setting missing: %s", res.ForLLM)
	}
	payload := parseSuccess(t, res.ForLLM)
	rawChanged, _ := payload["changed_fields"].([]any)
	changed := make([]string, 0, len(rawChanged))
	for _, c := range rawChanged {
		if s, ok := c.(string); ok {
			changed = append(changed, s)
		}
	}
	if !tlStringListContains(changed, "reasoning_effort") || !tlStringListContains(changed, "fallback_models") {
		t.Fatalf("RED: changed_fields = %v, want both \"reasoning_effort\" and \"fallback_models\" reported as changed", changed)
	}

	read := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"reasoning_effort", "fallback_models"}})
	if read.IsError {
		t.Fatalf("RED: get_agent with fields [reasoning_effort,fallback_models] refused — fields projection missing: %s", read.ForLLM)
	}
	view := parseSuccess(t, read.ForLLM)
	if view["reasoning_effort"] != "high" {
		t.Fatalf("RED: get_agent fields [reasoning_effort] = %#v, want \"high\" (read-back equals stored)", view["reasoning_effort"])
	}
	fb, ok := view["fallback_models"].([]any)
	if !ok || len(fb) != 1 {
		t.Fatalf("RED: get_agent fields [fallback_models] = %#v, want exactly one entry", view["fallback_models"])
	}
	fb0, _ := fb[0].(map[string]any)
	if fb0 == nil || fb0["reasoning_effort"] != "on" {
		t.Fatalf("RED: fallback_models[0].reasoning_effort = %#v, want \"on\" — the fallback effort was dropped", fb0)
	}

	stored := tlReadEntityJSON(t, deps.Home, id)
	if stored["reasoning_effort"] != "high" {
		t.Fatalf("RED: stored entity reasoning_effort = %#v, want \"high\" persisted on the record", stored["reasoning_effort"])
	}
}

// TestT_TL64_UpdateAgentRefusesUnofferedAndOversized covers S-64: an unoffered
// level is refused with the offered options listed, a 33-character value is
// refused (D1 row 9), and neither changes anything.
//
// Traces to: effort-levels-spec-r10.md S-64
func TestT_TL64_UpdateAgentRefusesUnofferedAndOversized(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)
	id := tlCreateMainAgent(t, reg, "Offered Bot", tlPrimaryModel, tlPrimaryProvider, nil)
	revBefore := currentAgentRevision(t, deps, id)

	// The fixture primary offers provider_default, none, low, high, max
	// (D8 row 1), so "xhigh" is unoffered for this slot.
	res := tlExec(t, reg, "update_agent", map[string]any{
		"id": id, "revision": revBefore, "reasoning_effort": "xhigh",
	})
	if !res.IsError {
		t.Fatalf("RED: update_agent accepted the unoffered level \"xhigh\" — no offered-options check")
	}
	msg := tlRefusalText(t, res)
	for _, opt := range []string{"none", "low", "high", "max"} {
		if !strings.Contains(msg, opt) {
			t.Fatalf("RED: unoffered-level refusal must list the offered options (provider_default,none,low,high,max); message: %s", msg)
		}
	}

	over := tlExec(t, reg, "update_agent", map[string]any{
		"id": id, "revision": revBefore, "reasoning_effort": tlEffortOverLen,
	})
	if !over.IsError {
		t.Fatalf("RED: update_agent accepted a 33-character reasoning_effort (D1 row 9: refused by every writer)")
	}
	if overMsg := tlRefusalText(t, over); !strings.Contains(overMsg, tlLenEffortMsg) {
		t.Fatalf("RED: over-length refusal message = %q, want it to contain %q", overMsg, tlLenEffortMsg)
	}

	// Nothing changed: the revision is byte-identical and no effort was stored.
	if revAfter := currentAgentRevision(t, deps, id); revAfter != revBefore {
		t.Fatalf("revision changed across two refused updates: %s -> %s", revBefore, revAfter)
	}
	stored := tlReadEntityJSON(t, deps.Home, id)
	if _, present := stored["reasoning_effort"]; present {
		t.Fatalf("RED: a refused update stored reasoning_effort anyway: %#v", stored["reasoning_effort"])
	}
}

// TestT_TL66_CreateAgentKeepsFallbackEfforts covers S-66: create_agent keeps
// two fallback entries each with its own effort, and both survive a read-back
// in order (differentiation: entry 1 low, entry 2 high).
//
// Traces to: effort-levels-spec-r10.md S-66
func TestT_TL66_CreateAgentKeepsFallbackEfforts(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)
	systools.UseNameKeyedAgentIDsForTest(t)
	res := tlExec(t, reg, "create_agent", map[string]any{
		"name":        "Fallback Effort Bot",
		"description": "RED fixture: fallbacks carry effort",
		"soul":        "You are a RED test fixture agent.",
		"model":       tlPrimaryModel,
		"provider":    tlPrimaryProvider,
		"fallback_models": []any{
			map[string]any{"provider": tlPrimaryProvider, "model": tlFallbackSwitch, "reasoning_effort": "on"},
			map[string]any{"provider": tlFallback2Provider, "model": tlFallbackCustom, "reasoning_effort": "provider_default"},
		},
	})
	if res.IsError {
		t.Fatalf("RED: create_agent refused fallback entries carrying reasoning_effort — the per-fallback effort is currently dropped: %s", res.ForLLM)
	}
	id := tlIDFromCreate(t, res)

	read := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"fallback_models"}})
	if read.IsError {
		t.Fatalf("RED: get_agent with fields [fallback_models] refused: %s", read.ForLLM)
	}
	view := parseSuccess(t, read.ForLLM)
	fb, ok := view["fallback_models"].([]any)
	if !ok || len(fb) != 2 {
		t.Fatalf("RED: get_agent fallback_models = %#v, want both fallback entries", view["fallback_models"])
	}
	fb0, _ := fb[0].(map[string]any)
	fb1, _ := fb[1].(map[string]any)
	if fb0 == nil || fb0["model"] != tlFallbackSwitch || fb0["reasoning_effort"] != "on" {
		t.Fatalf("RED: fallback_models[0] = %#v, want model %q with reasoning_effort \"on\"", fb0, tlFallbackSwitch)
	}
	if fb1 == nil || fb1["model"] != tlFallbackCustom || fb1["reasoning_effort"] != "provider_default" {
		t.Fatalf("RED: fallback_models[1] = %#v, want model %q with reasoning_effort \"provider_default\"", fb1, tlFallbackCustom)
	}
}

// TestT_TL67_GetAgentEffortOptionsPerSlot covers S-67: get_agent exposes
// effort_options per model_chain slot — levels+provider_default on the
// primary (D8 row 1), provider_default + the on/off switch on fallback 1
// (D8 row 2), and an explicit null on the custom row with no catalog data
// (D8 row 6).
//
// Traces to: effort-levels-spec-r10.md S-67
func TestT_TL67_GetAgentEffortOptionsPerSlot(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)
	id := tlCreateMainAgent(t, reg, "Options Bot", tlPrimaryModel, tlPrimaryProvider, []any{
		map[string]any{"provider": tlPrimaryProvider, "model": tlFallbackSwitch},
		map[string]any{"provider": tlFallback2Provider, "model": tlFallbackCustom},
	})

	read := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"model_chain"}})
	if read.IsError {
		t.Fatalf("RED: get_agent with fields [model_chain] refused — fields support missing: %s", read.ForLLM)
	}
	view := parseSuccess(t, read.ForLLM)
	mc, ok := view["model_chain"].([]any)
	if !ok || len(mc) != 3 {
		t.Fatalf("RED: get_agent model_chain = %#v, want three slots (primary + two fallbacks)", view["model_chain"])
	}
	slot0, _ := mc[0].(map[string]any)
	tlWantStringSlice(t, "model_chain[0].effort_options", slot0["effort_options"],
		"provider_default", "none", "low", "high", "max") // D8 row 1
	slot1, _ := mc[1].(map[string]any)
	tlWantStringSlice(t, "model_chain[1].effort_options", slot1["effort_options"],
		"provider_default", "off", "on") // D8 row 2: switch only
	slot2, ok2 := mc[2].(map[string]any)
	if !ok2 {
		t.Fatalf("model_chain[2] is not an object: %#v", mc[2])
	}
	opts2, present := slot2["effort_options"]
	if !present {
		t.Fatalf("RED: model_chain[2] carries no effort_options key; D8 row 6 wants an explicit JSON null for a custom row with no catalog data")
	}
	if opts2 != nil {
		t.Fatalf("model_chain[2].effort_options = %#v, want null (no catalog row)", opts2)
	}
}

// TestT_TL76_GetAgentFieldsProjection covers S-76: reasoning_effort is a valid
// get_agent fields name; the narrow projection returns exactly
// id/revision/reasoning_effort; "all" includes the effort fields; an unknown
// name is refused with T8's wording listing reasoning_effort as valid.
//
// Traces to: effort-levels-spec-r10.md S-76
func TestT_TL76_GetAgentFieldsProjection(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)
	id := tlCreateMainAgent(t, reg, "Fields Bot", tlPrimaryModel, tlPrimaryProvider,
		[]any{map[string]any{"provider": tlPrimaryProvider, "model": tlFallbackSwitch}})
	rev := currentAgentRevision(t, deps, id)

	seed := tlExec(t, reg, "update_agent", map[string]any{
		"id":               id,
		"revision":         rev,
		"reasoning_effort": "high",
		"fallback_models": []any{
			map[string]any{"provider": tlPrimaryProvider, "model": tlFallbackSwitch, "reasoning_effort": "on"},
		},
	})
	if seed.IsError {
		t.Fatalf("RED baseline: could not store reasoning_effort \"high\" via update_agent (feature missing): %s", seed.ForLLM)
	}

	one := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"reasoning_effort"}})
	if one.IsError {
		t.Fatalf("RED: get_agent with fields [reasoning_effort] refused: %s", one.ForLLM)
	}
	view := parseSuccess(t, one.ForLLM)
	if len(view) != 3 {
		t.Fatalf("RED: fields [reasoning_effort] returned %d keys (%v), want exactly id, revision, reasoning_effort", len(view), view)
	}
	if view["reasoning_effort"] != "high" {
		t.Fatalf("RED: fields [reasoning_effort] value = %#v, want \"high\"", view["reasoning_effort"])
	}
	if _, hasID := view["id"]; !hasID {
		t.Fatalf("RED: fields [reasoning_effort] result is missing id: %v", view)
	}
	if _, hasRev := view["revision"]; !hasRev {
		t.Fatalf("RED: fields [reasoning_effort] result is missing revision: %v", view)
	}

	all := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"all"}})
	if all.IsError {
		t.Fatalf("RED: get_agent with fields [all] refused: %s", all.ForLLM)
	}
	full := parseSuccess(t, all.ForLLM)
	if full["reasoning_effort"] != "high" {
		t.Fatalf("RED: fields [all] reasoning_effort = %#v, want \"high\"", full["reasoning_effort"])
	}
	fb, _ := full["fallback_models"].([]any)
	if len(fb) != 1 {
		t.Fatalf("RED: fields [all] fallback_models = %#v, want the one stored fallback", full["fallback_models"])
	}
	fb0, _ := fb[0].(map[string]any)
	if fb0 == nil || fb0["reasoning_effort"] != "on" {
		t.Fatalf("RED: fields [all] fallback_models[0] = %#v, want its reasoning_effort \"on\" included", fb0)
	}
	mc, _ := full["model_chain"].([]any)
	if len(mc) == 0 {
		t.Fatalf("RED: fields [all] model_chain missing or empty — effort_options not exposed")
	}
	for i, slot := range mc {
		slotMap, _ := slot.(map[string]any)
		if _, has := slotMap["effort_options"]; !has {
			t.Fatalf("RED: fields [all] model_chain[%d] carries no effort_options key: %#v", i, slotMap)
		}
	}

	bad := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"effort"}})
	if !bad.IsError {
		t.Fatalf("RED: get_agent accepted the unknown fields name \"effort\"")
	}
	badMsg := tlRefusalText(t, bad)
	if !strings.Contains(badMsg, `unknown field "effort" for agent`) {
		t.Fatalf("RED: unknown-fields-name refusal = %q, want T8's `unknown field \"effort\" for agent. Valid: all, ...`", badMsg)
	}
	if !strings.Contains(badMsg, "reasoning_effort") {
		t.Fatalf("RED: unknown-fields-name refusal must list reasoning_effort among the valid names: %q", badMsg)
	}
}

// TestT_TL77_ExternalCLIWorkerRefusesEffort covers S-77: update_agent refuses
// reasoning_effort on a subagent_3p worker with the pinned sentence, nothing
// changes, get_agent returns reasoning_effort null, and editable_fields lists
// the field as not editable with that reason.
//
// Traces to: effort-levels-spec-r10.md S-77
func TestT_TL77_ExternalCLIWorkerRefusesEffort(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)
	id := tlCreateExternalCLIWorker(t, reg, "External Bot")
	rev := currentAgentRevision(t, deps, id)

	res := tlExec(t, reg, "update_agent", map[string]any{
		"id": id, "revision": rev, "reasoning_effort": "high",
	})
	if !res.IsError {
		t.Fatalf("RED: update_agent accepted reasoning_effort on a subagent_3p external-CLI worker")
	}
	if msg := tlRefusalText(t, res); msg != tlExtCLIMsg {
		t.Fatalf("RED: refusal message = %q, want the exact spec sentence %q", msg, tlExtCLIMsg)
	}
	if revAfter := currentAgentRevision(t, deps, id); revAfter != rev {
		t.Fatalf("refused update changed the revision: %s -> %s", rev, revAfter)
	}

	read := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"all"}})
	if read.IsError {
		t.Fatalf("test setup: get_agent failed: %s", read.ForLLM)
	}
	view := parseSuccess(t, read.ForLLM)
	if v, present := view["reasoning_effort"]; !present || v != nil {
		t.Fatalf("RED: get_agent reasoning_effort = %#v (present=%v), want an explicit null for an external-CLI worker", v, present)
	}
	descriptors, _ := view["editable_fields"].([]any)
	var found map[string]any
	for _, d := range descriptors {
		if dm, ok := d.(map[string]any); ok && dm["name"] == "reasoning_effort" {
			found = dm
			break
		}
	}
	if found == nil {
		t.Fatalf("RED: editable_fields carries no reasoning_effort descriptor: %v", view["editable_fields"])
	}
	if found["editable"] != false {
		t.Fatalf("RED: editable_fields reasoning_effort editable = %#v, want false", found["editable"])
	}
	if found["reason"] != tlExtCLIMsg {
		t.Fatalf("RED: editable_fields reasoning_effort reason = %q, want %q", found["reason"], tlExtCLIMsg)
	}
}

// ---- Config tools ----

// TestT_TL68_SetConfigWritesDefaultModelEffort covers S-68: set_config writes
// the default-model effort key with NO catalog check (max is stored even
// though the fixture default model offers only low, high), while the
// 33-character value is refused with the pinned over-32 sentence.
//
// Traces to: effort-levels-spec-r10.md S-68
func TestT_TL68_SetConfigWritesDefaultModelEffort(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)

	key := "agents.defaults.default_model.reasoning_effort"
	set := tlSetConfig(t, reg, key, "max")
	if set.IsError {
		t.Fatalf("RED: set_config refused %s=max (spec: stored without a catalog check; the key is not writable today): %s", key, set.ForLLM)
	}
	got := tlConfigValue(t, tlGetConfig(t, reg, key))
	if got != "max" {
		t.Fatalf("RED: read-back of %s = %#v, want max", key, got)
	}
	if disk := tlDurableValue(t, deps, key); disk != "max" {
		t.Fatalf("reopened %s=%v, want max", key, disk)
	}

	over := tlSetConfig(t, reg, key, tlEffortOverLen)
	if !over.IsError {
		t.Fatalf("RED: set_config accepted a 33-character effort value (D1 row 9)")
	}
	if code := tlErrorCode(t, over); code != "INVALID_INPUT" {
		t.Fatalf("RED: over-length refusal code = %q, want INVALID_INPUT: %s", code, over.ForLLM)
	}
	if msg := tlRefusalText(t, over); !strings.Contains(msg, tlLenEffortMsg) {
		t.Fatalf("RED: over-length refusal message = %q, want it to contain %q", msg, tlLenEffortMsg)
	}
	if gotAfter := tlConfigValue(t, tlGetConfig(t, reg, key)); gotAfter != "max" {
		t.Fatalf("refused over-length write changed the stored value: %#v, want max unchanged", gotAfter)
	}
}

// TestT_TL83_RecapLimitKeysRoundTrip is S-83's outline over all ten recap
// limit keys: set_config writes agents.defaults.<key>, get_config reads it
// back directly, and the section read of agents.defaults contains the key
// unredacted. The three rows whose key contains the word token are the
// R2-MAJ-003 regression rows (today's name-fragment classifier refuses them
// outright; after the feature they must be exactly-path classifiable).
//
// The effect leg of the outline (the next recap honouring the value) is the
// recap engine's acceptance test (T-L3 / S-80), not the config tool's.
//
// Traces to: effort-levels-spec-r10.md S-83
func TestT_TL83_RecapLimitKeysRoundTrip(t *testing.T) {
	rows := []struct {
		key   string
		value float64
	}{
		{"recap_max_output_tokens", 4096},
		{"recap_input_tokens", 500},
		{"recap_summary_words", 100},
		{"recap_list_items", 3},
		{"recap_fallback_context_chars", 300},
		{"recap_timeout_seconds", 15},
		{"recap_stream_retries", 0},
		{"recap_retry_pause_ms", 200},
		{"recap_chars_per_token", 3},
		{"bootstrap_recap_min_spacing_seconds", 20},
	}
	for _, row := range rows {
		t.Run(row.key, func(t *testing.T) {
			deps, _ := tlPersistentDeps(t)
			reg := tlRegistry(t, deps)
			key := "agents.defaults." + row.key

			set := tlSetConfig(t, reg, key, row.value)
			if set.IsError {
				t.Fatalf("RED: set_config(%s) refused — recap limit keys not writable: %s", key, set.ForLLM)
			}
			direct := tlConfigValue(t, tlGetConfig(t, reg, key))
			if direct != row.value {
				t.Fatalf("RED: direct read of %s = %#v, want %v", key, direct, row.value)
			}
			if disk := tlDurableValue(t, deps, key); disk != row.value {
				t.Fatalf("reopened %s=%v, want %v", key, disk, row.value)
			}
			section := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults"))
			secMap, ok := section.(map[string]any)
			if !ok {
				t.Fatalf("section read of agents.defaults = %#v (%T), want an object", section, section)
			}
			secVal, present := secMap[row.key]
			if !present {
				t.Fatalf("RED: section read of agents.defaults does not contain %s: %v", row.key, secMap)
			}
			if s, isStr := secVal.(string); isStr && s == tlRedacted {
				t.Fatalf("RED: section read shows %s as %s — the value must be readable unredacted (FR-076)", row.key, tlRedacted)
			}
			if secVal != row.value {
				t.Fatalf("RED: section read %s = %#v, want %v", row.key, secVal, row.value)
			}
		})
	}
}

// TestT_TL83_RecapSummaryWordsStoresThousand is S-83's And row: writing
// recap_summary_words 1000 is STORED (the clamp to 500 happens when the value
// is used, with one warning — that leg is the recap engine's, not the config
// tool's), so the direct read-back must return the written 1000.
//
// Traces to: effort-levels-spec-r10.md S-83
func TestT_TL83_RecapSummaryWordsStoresThousand(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)
	key := "agents.defaults.recap_summary_words"

	set := tlSetConfig(t, reg, key, float64(1000))
	if set.IsError {
		t.Fatalf("RED: set_config(%s=1000) refused: %s", key, set.ForLLM)
	}
	got := tlConfigValue(t, tlGetConfig(t, reg, key))
	if got != float64(1000) {
		t.Fatalf("RED: stored value = %#v, want 1000 stored unclamped (clamping happens at use, not at storage)", got)
	}
}

// TestT_TL92_CredentialMaskingStillWorks is S-92's PIN (must pass today and
// after the recap keys are opened): direct reads and writes of credential
// keys are refused, section reads show the redaction marker for them, and a
// key that is merely token-SHAPED but not a recap limit (the _extra variant)
// stays refused on both sides.
//
// Traces to: effort-levels-spec-r10.md S-92
func TestT_TL92_CredentialMaskingStillWorks(t *testing.T) {
	deps, cfg := tlPersistentDeps(t)
	cfg.Gateway.Token = "bearer-secret-value"
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram": {Type: "telegram", Enabled: true, TokenRef: "telegram-bot-token-ref"},
	}
	cfg.Providers = []*config.ModelConfig{
		{Provider: "openrouter", Model: "glm-5.2", APIKeyRef: "OPENROUTER_API_KEY"},
	}
	reg := tlRegistry(t, deps)

	for _, key := range []string{"channels.telegram.token", "gateway.token"} {
		res := tlGetConfig(t, reg, key)
		if !res.IsError {
			t.Fatalf("PIN: get_config(%s) succeeded — credential read must stay refused: %s", key, res.ForLLM)
		}
		if code := tlErrorCode(t, res); code != "FORBIDDEN" {
			t.Fatalf("PIN: get_config(%s) code = %q, want FORBIDDEN: %s", key, code, res.ForLLM)
		}
	}

	write := tlSetConfig(t, reg, "channels.telegram.token", "swapped-token-value")
	if !write.IsError {
		t.Fatalf("PIN: set_config(channels.telegram.token) succeeded — credential write must stay refused")
	}
	if code := tlErrorCode(t, write); code != "FORBIDDEN" {
		t.Fatalf("PIN: set_config(channels.telegram.token) code = %q, want FORBIDDEN: %s", code, write.ForLLM)
	}

	gwRaw := tlConfigValue(t, tlGetConfig(t, reg, "gateway"))
	gw, ok := gwRaw.(map[string]any)
	if !ok {
		t.Fatalf("PIN: section read of gateway = %#v (%T), want an object", gwRaw, gwRaw)
	}
	if gw["token"] != tlRedacted {
		t.Fatalf("PIN: section read of gateway shows token = %#v, want %s", gw["token"], tlRedacted)
	}

	chRaw := tlConfigValue(t, tlGetConfig(t, reg, "channels"))
	ch, ok := chRaw.(map[string]any)
	if !ok {
		t.Fatalf("PIN: section read of channels = %#v (%T), want an object", chRaw, chRaw)
	}
	tg, ok := ch["telegram"].(map[string]any)
	if !ok {
		t.Fatalf("PIN: section read of channels has no telegram instance: %v", ch)
	}
	if tg["token_ref"] != tlRedacted {
		t.Fatalf("PIN: section read of channels.telegram shows token_ref = %#v, want %s", tg["token_ref"], tlRedacted)
	}

	provRaw := tlConfigValue(t, tlGetConfig(t, reg, "providers"))
	prov, ok := provRaw.([]any)
	if !ok || len(prov) == 0 {
		t.Fatalf("PIN: section read of providers = %#v, want at least one row", provRaw)
	}
	row0, _ := prov[0].(map[string]any)
	if row0 == nil || row0["api_key_ref"] != tlRedacted {
		t.Fatalf("PIN: section read of providers shows api_key_ref = %#v, want %s", row0["api_key_ref"], tlRedacted)
	}

	// A key that is token-shaped but not one of the opened recap limits stays
	// refused on both the read and the write side (classification by exact
	// path, not by prefix).
	extra := "agents.defaults.recap_max_output_tokens_extra"
	getExtra := tlGetConfig(t, reg, extra)
	if !getExtra.IsError || tlErrorCode(t, getExtra) != "FORBIDDEN" {
		t.Fatalf("PIN: get_config(%s) must stay refused with FORBIDDEN, got: %s", extra, getExtra.ForLLM)
	}
	setExtra := tlSetConfig(t, reg, extra, float64(1))
	if !setExtra.IsError || tlErrorCode(t, setExtra) != "FORBIDDEN" {
		t.Fatalf("PIN: set_config(%s) must stay refused with FORBIDDEN, got: %s", extra, setExtra.ForLLM)
	}
}

// TestT_TL94_UpdateToolNullEffortRefusedBeforeRun covers S-94's tool-boundary
// rows: through the production tool registry, reasoning_effort null is
// refused before the tool runs and nothing changes; omitting the key keeps
// the stored value; the empty string clears it (read back as an explicit
// null).
//
// Traces to: effort-levels-spec-r10.md S-94
func TestT_TL94_UpdateToolNullEffortRefusedBeforeRun(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)
	id := tlCreateMainAgent(t, reg, "Null Guard Bot", tlPrimaryModel, tlPrimaryProvider, nil)

	rev := currentAgentRevision(t, deps, id)
	seed := tlExec(t, reg, "update_agent", map[string]any{
		"id": id, "revision": rev, "reasoning_effort": "high",
	})
	if seed.IsError {
		t.Fatalf("RED baseline: could not store reasoning_effort high via update_agent (feature missing): %s", seed.ForLLM)
	}
	// Compare refusals against the acknowledged seed's revision, not the
	// pre-seed revision (CHECK-11).
	rev = currentAgentRevision(t, deps, id)

	nullRes := tlExec(t, reg, "update_agent", map[string]any{
		"id": id, "revision": rev, "reasoning_effort": nil,
	})
	if !nullRes.IsError {
		t.Fatalf("RED: update_agent accepted reasoning_effort null — the registry's pre-run validation must refuse it (D1 row 13)")
	}
	if msg := tlRefusalText(t, nullRes); !strings.Contains(msg, "reasoning_effort") {
		t.Fatalf("RED: null-effort refusal must name reasoning_effort: %q", msg)
	}
	if revAfter := currentAgentRevision(t, deps, id); revAfter != rev {
		t.Fatalf("null refusal changed the revision: %s -> %s", rev, revAfter)
	}
	if stored := tlReadEntityJSON(t, deps.Home, id)["reasoning_effort"]; stored != "high" {
		t.Fatalf("RED: stored effort after the null refusal = %#v, want high unchanged", stored)
	}

	// Omitting the key keeps the stored value.
	omit := tlExec(t, reg, "update_agent", map[string]any{
		"id": id, "revision": currentAgentRevision(t, deps, id), "description": "updated without an effort key",
	})
	if omit.IsError {
		t.Fatalf("update without reasoning_effort failed: %s", omit.ForLLM)
	}
	if stored := tlReadEntityJSON(t, deps.Home, id)["reasoning_effort"]; stored != "high" {
		t.Fatalf("an update omitting reasoning_effort changed the stored value: %#v, want high kept", stored)
	}

	// The empty string clears it to Not set; get_agent reads the cleared value
	// as an explicit null.
	cleared := tlExec(t, reg, "update_agent", map[string]any{
		"id": id, "revision": currentAgentRevision(t, deps, id), "reasoning_effort": "",
	})
	if cleared.IsError {
		t.Fatalf("RED: update_agent refused reasoning_effort as empty string (spec: empty clears to Not set): %s", cleared.ForLLM)
	}
	read := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"reasoning_effort"}})
	if read.IsError {
		t.Fatalf("RED: get_agent with fields [reasoning_effort] refused: %s", read.ForLLM)
	}
	view := parseSuccess(t, read.ForLLM)
	if v, present := view["reasoning_effort"]; !present || v != nil {
		t.Fatalf("RED: cleared effort reads back as %#v (present=%v), want an explicit null", v, present)
	}
}

// TestT_TL94_SetConfigNullAndCompositeRefusals covers S-94's set_config rows:
// null, non-string and over-32 effort members are refused with INVALID_INPUT
// at any depth (leaf, array item, object, section write with a valid sibling,
// any-case member names), the JSON-encoded composite string aimed at an
// effort-bearing composite key is refused as a type error, every refusal is
// atomic (nothing saved, valid siblings unsaved), and the same composite
// writes with valid effort strings succeed and read back exactly.
//
// Traces to: effort-levels-spec-r10.md S-94
func TestT_TL94_SetConfigNullAndCompositeRefusals(t *testing.T) {
	deps, _ := tlPersistentDeps(t)
	reg := tlRegistry(t, deps)

	// Baselines (RED today: none of these keys is writable yet).
	seed := func(key string, value any) {
		t.Helper()
		if res := tlSetConfig(t, reg, key, value); res.IsError {
			t.Fatalf("RED baseline: set_config(%s) refused (composite effort settings missing): %s", key, res.ForLLM)
		}
	}
	seed("agents.defaults.recap_summary_words", float64(150))
	seed("agents.defaults.recap_fallback_models", []any{
		map[string]any{"provider": "openrouter", "model": "m1", "reasoning_effort": "low"},
	})
	seed("agents.defaults.default_model", map[string]any{
		"provider": "openrouter", "model": "m1", "reasoning_effort": "high",
	})

	// encodedComposite is the D1 row 14 shape: a JSON-encoded ARRAY as a Go
	// string, aimed at the effort-bearing composite key recap_fallback_models.
	encodedComposite := `[{"provider":"openrouter","model":"m1","reasoning_effort":null}]`

	// refuse runs one refusal row and proves atomicity by reading back.
	refuse := func(name, key string, value any, wantMsgCore, wantKeyNamed string, readBack func(t *testing.T)) {
		t.Run(name, func(t *testing.T) {
			res := tlSetConfig(t, reg, key, value)
			if !res.IsError {
				t.Fatalf("RED: set_config accepted %s = %#v; spec wants an INVALID_INPUT refusal", key, value)
			}
			if code := tlErrorCode(t, res); code != "INVALID_INPUT" {
				t.Fatalf("RED: refusal code = %q, want INVALID_INPUT: %s", code, res.ForLLM)
			}
			msg := tlRefusalText(t, res)
			if !strings.Contains(msg, wantMsgCore) {
				t.Fatalf("RED: refusal message = %q, want it to contain %q", msg, wantMsgCore)
			}
			if wantKeyNamed != "" && !strings.Contains(msg, wantKeyNamed) {
				t.Fatalf("RED: refusal message = %q, want it to name the key %q", msg, wantKeyNamed)
			}
			readBack(t)
		})
	}

	// Leaf null on the default-model effort key (S-94's set_config row).
	refuse("null_leaf_default_model",
		"agents.defaults.default_model.reasoning_effort", nil,
		tlNullEffortMsg, "",
		func(t *testing.T) {
			dmRaw := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.default_model"))
			dm, _ := dmRaw.(map[string]any)
			if dm == nil || dm["model"] != "m1" || dm["reasoning_effort"] != "high" {
				t.Fatalf("atomicity: default model after the leaf-null refusal = %#v, want model m1 with effort high unchanged", dmRaw)
			}
		})

	// Null inside an array item.
	refuse("null_array_item",
		"agents.defaults.recap_fallback_models", []any{
			map[string]any{"provider": "openrouter", "model": "m1", "reasoning_effort": nil},
		},
		tlNullEffortMsg, "",
		func(t *testing.T) {
			fbRaw := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.recap_fallback_models"))
			fb, _ := fbRaw.([]any)
			if len(fb) != 1 {
				t.Fatalf("atomicity: recap fallback list = %#v, want the one stored entry unchanged", fbRaw)
			}
			fb0, _ := fb[0].(map[string]any)
			if fb0 == nil || fb0["model"] != "m1" || fb0["reasoning_effort"] != "low" {
				t.Fatalf("atomicity: recap fallback[0] = %#v, want model m1 at low unchanged", fb[0])
			}
		})

	// Over-32 inside an array item.
	refuse("over32_array_item",
		"agents.defaults.recap_fallback_models", []any{
			map[string]any{"provider": "openrouter", "model": "m1", "reasoning_effort": tlEffortOverLen},
		},
		tlLenEffortMsg, "",
		func(t *testing.T) {
			fb, _ := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.recap_fallback_models")).([]any)
			fb0, _ := fb[0].(map[string]any)
			if fb0 == nil || fb0["reasoning_effort"] != "low" {
				t.Fatalf("atomicity: recap fallback[0] = %#v, want effort low unchanged", fb[0])
			}
		})

	// The JSON-encoded composite STRING aimed at an effort-bearing composite
	// key is refused as a type error — never decoded (D1 row 14; r7 scope
	// R5-MAJ-001 names recap_fallback_models).
	refuse("encoded_string_type_refusal",
		"agents.defaults.recap_fallback_models", encodedComposite,
		tlTypeMsgCore, "recap_fallback_models",
		func(t *testing.T) {
			fb, _ := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.recap_fallback_models")).([]any)
			if len(fb) != 1 {
				t.Fatalf("atomicity: recap fallback list = %#v, want the stored array unchanged (the string was never decoded)", fb)
			}
		})

	// Null inside the default-model object.
	refuse("null_object_default_model",
		"agents.defaults.default_model", map[string]any{
			"provider": "openrouter", "model": "m2", "reasoning_effort": nil,
		},
		tlNullEffortMsg, "",
		func(t *testing.T) {
			dm, _ := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.default_model")).(map[string]any)
			if dm == nil || dm["model"] != "m1" || dm["reasoning_effort"] != "high" {
				t.Fatalf("atomicity: default model = %#v, want model m1 with effort high (nothing of the refused write saved)", dm)
			}
		})

	// Any-case, non-string member inside the object.
	refuse("anycase_nonstring_object_default_model",
		"agents.defaults.default_model", map[string]any{
			"provider": "openrouter", "model": "m2", "Reasoning_Effort": float64(7),
		},
		tlNullEffortMsg, "",
		func(t *testing.T) {
			dm, _ := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.default_model")).(map[string]any)
			if dm == nil || dm["model"] != "m1" || dm["reasoning_effort"] != "high" {
				t.Fatalf("atomicity: default model = %#v, want model m1 with effort high unchanged", dm)
			}
		})

	// Section write: a valid sibling plus a null effort member — refused
	// atomically, so the sibling is NOT saved either.
	refuse("null_section_with_valid_sibling",
		"agents.defaults", map[string]any{
			"recap_summary_words": float64(100), "recap_reasoning_effort": nil,
		},
		tlNullEffortMsg, "",
		func(t *testing.T) {
			if got := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.recap_summary_words")); got != float64(150) {
				t.Fatalf("atomicity: recap_summary_words = %#v, want 150 — the valid sibling of a refused section write must not be saved", got)
			}
		})

	// Section write: valid sibling plus an over-32 effort member.
	refuse("over32_section_with_valid_sibling",
		"agents.defaults", map[string]any{
			"recap_summary_words": float64(100), "recap_reasoning_effort": tlEffortOverLen,
		},
		tlLenEffortMsg, "",
		func(t *testing.T) {
			if got := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.recap_summary_words")); got != float64(150) {
				t.Fatalf("atomicity: recap_summary_words = %#v, want 150 unchanged", got)
			}
		})

	// Valid composite writes succeed and read back exactly (the
	// differentiation half of S-94).
	t.Run("valid_composites_succeed", func(t *testing.T) {
		valid := []struct {
			name  string
			key   string
			value any
			check func(t *testing.T)
		}{
			{"default_model_object", "agents.defaults.default_model", map[string]any{
				"provider": "openrouter", "model": "m2", "reasoning_effort": "high",
			}, func(t *testing.T) {
				dm, _ := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.default_model")).(map[string]any)
				if dm == nil || dm["model"] != "m2" || dm["reasoning_effort"] != "high" {
					t.Fatalf("valid composite write did not read back: %#v, want model m2 with effort high", dm)
				}
			}},
			{"fallback_array", "agents.defaults.recap_fallback_models", []any{
				map[string]any{"provider": "openrouter", "model": "m1", "reasoning_effort": "high"},
			}, func(t *testing.T) {
				fb, _ := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.recap_fallback_models")).([]any)
				fb0, _ := fb[0].(map[string]any)
				if fb0 == nil || fb0["reasoning_effort"] != "high" {
					t.Fatalf("valid composite write did not read back: %#v, want effort high", fb)
				}
			}},
			{"section_with_valid_effort", "agents.defaults", map[string]any{
				"recap_summary_words": float64(100), "recap_reasoning_effort": "high",
			}, func(t *testing.T) {
				if words := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.recap_summary_words")); words != float64(100) {
					t.Fatalf("valid section write lost the sibling: recap_summary_words = %#v, want 100", words)
				}
				if effort := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.recap_reasoning_effort")); effort != "high" {
					t.Fatalf("valid section write did not store recap_reasoning_effort: %#v, want high", effort)
				}
			}},
			{"max_length_leaf", "agents.defaults.default_model.reasoning_effort", tlEffortMaxLen, func(t *testing.T) {
				if got := tlConfigValue(t, tlGetConfig(t, reg, "agents.defaults.default_model.reasoning_effort")); got != tlEffortMaxLen {
					t.Fatalf("32-character effort did not read back exactly: %#v", got)
				}
			}},
		}
		for _, row := range valid {
			t.Run(row.name, func(t *testing.T) {
				res := tlSetConfig(t, reg, row.key, row.value)
				if res.IsError {
					t.Fatalf("RED: valid composite write refused: set_config(%s) failed: %s", row.key, res.ForLLM)
				}
				row.check(t)
			})
		}
	})
}

// TestT_TL94_AllowFromScalarToListControl is S-94's PIN (r7 R5-MAJ-001): the
// supported scalar-to-list write channels.telegram.allow_from = alice must
// keep succeeding and normalise to a one-element list — an unscoped
// string-into-composite refusal in the new effort check would break exactly
// this.
//
// Traces to: effort-levels-spec-r10.md S-94
func TestT_TL94_AllowFromScalarToListControl(t *testing.T) {
	deps, cfg := tlPersistentDeps(t)
	seedOwnedChannel(cfg)
	reg := tlRegistry(t, deps)

	res := tlSetConfig(t, reg, "channels.telegram.allow_from", "alice")
	if res.IsError {
		t.Fatalf("PIN: the supported scalar-to-list write set_config(channels.telegram.allow_from=alice) was refused: %s", res.ForLLM)
	}
	payload := parseSuccess(t, res.ForLLM)
	stored, ok := payload["stored_value"].([]any)
	if !ok || len(stored) != 1 || stored[0] != "alice" {
		t.Fatalf("PIN: stored_value = %#v, want the one-element alice list (FlexibleStringSlice normalisation reported)", payload["stored_value"])
	}
	back := tlConfigValue(t, tlGetConfig(t, reg, "channels.telegram.allow_from"))
	tlWantStringSlice(t, "get_config(channels.telegram.allow_from)", back, "alice")
}
