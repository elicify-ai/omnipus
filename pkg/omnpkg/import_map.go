package omnpkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skillname"
)

// ImportTarget is the target-install lookup edge for the import-side field
// mapping (Dataset D5).
type ImportTarget interface {
	InstalledSkill(slug string) bool
	KnownTool(name string) bool
	AlwaysAskTool(name string) bool
	ProviderConfigured(name string) bool
	GlobalMaxToolIterations() int
	SeedToolPolicies(agentType string) map[string]string
}

// Warning is one preview/result warning row (code, element_source_id, message,
// items).
type Warning struct {
	Code            string
	ElementSourceID string
	Message         string
	Items           []string
}

// Warning codes the engine's import mapping lists (FR-033).
const (
	WarnSkillBoundLocal       = "skill_bound_local"
	WarnSkillDropped          = "skill_dropped"
	WarnMCPBindingDropped     = "mcp_binding_dropped"
	WarnToolPolicyDropped     = "tool_policy_dropped"
	WarnToolPolicyTightened   = "tool_policy_tightened"
	WarnProviderNotConfigured = "provider_not_configured"
	WarnFieldNotCarried       = "field_not_carried"
	// WarnRuntimeSettingsRemoved: an external worker's environment overrides
	// and CLI arguments are not imported (S1b, FR-043).
	WarnRuntimeSettingsRemoved = "runtime_settings_removed"
)

// runtimeSettingsRemovedMessage points to the per-agent destination, never to
// host Settings (FR-043, L7). It never echoes a value.
const runtimeSettingsRemovedMessage = "This worker's environment overrides and additional CLI arguments were not imported; " +
	"re-enter them in Edit → Runtime."

// AgentPlan is the engine's import-side mapping output: the record handed to
// the shared create function plus the listed warnings (Field mapping Import
// column; the create rules themselves stay in the shared function, T-3).
type AgentPlan struct {
	Record   []byte
	Warnings []Warning
}

// importNotCarriedFields are top-level stored fields the store sets itself on
// import (Field mapping rows workspace, created_at, updated_at, locked,
// skill_grants): removed silently. programs is never carried either: an
// imported agent arrives with every connected program off (connected
// programs FR-022), even from a hand-edited package.
var importNotCarriedFields = []string{"workspace", "created_at", "updated_at", "locked", "skill_grants", "programs"}

// importMapper accumulates the warnings of one record's mapping.
type importMapper struct {
	sourceID string
	target   ImportTarget
	warnings []Warning
}

func (im *importMapper) warn(code, msg string, items []string) {
	im.warnings = append(im.warnings, Warning{Code: code, ElementSourceID: im.sourceID, Message: msg, Items: items})
}

// MapAgentForImport applies the engine-level import transformations (skills
// filtered against the target, MCP assignments dropped, tool policies
// filtered/tightened, legacy fields dropped with warnings, provider and
// max_tool_iterations checks against the target).
func MapAgentForImport(record []byte, sourceID string, target ImportTarget) (*AgentPlan, error) {
	var probe map[string]any
	if err := json.Unmarshal(record, &probe); err != nil || probe == nil {
		return nil, newErr(CodeInvalidElement, "element", sourceID, "reason", "agent.json is not a JSON object")
	}
	im := &importMapper{sourceID: sourceID, target: target}
	if err := im.checkMaxToolIterations(record, sourceID); err != nil {
		return nil, err
	}
	typed, rec, err := decodeAgentRecord(record, sourceID)
	if err != nil {
		return nil, err
	}
	legacyFallbacks, err := hasLegacyModelFallbacks(record, sourceID)
	if err != nil {
		return nil, err
	}
	for _, k := range importNotCarriedFields {
		delete(rec, k)
	}
	if sub, ok := rec["subagents"].(map[string]any); ok {
		delete(sub, "allow_agents")
		delete(sub, "model")
		if exec, isMap := sub["executor"].(map[string]any); isMap {
			for _, k := range executorRuntimeFields {
				delete(exec, k)
			}
		}
	}
	if typed.IsExternalCLIWorker() {
		// Listed for every external worker: export always removes these
		// values, so they must be entered again (FR-033, FR-043).
		im.warn(WarnRuntimeSettingsRemoved, runtimeSettingsRemovedMessage, nil)
		// The create path gives an external worker the target's worker seed,
		// as REST create and create_agent do; the package's policies are not
		// an input (architect ruling 4B). Not a drop: a genuine export only
		// ever holds the source's seed.
		if tools, isMap := rec["tools"].(map[string]any); isMap {
			if builtin, isMap := tools["builtin"].(map[string]any); isMap {
				delete(builtin, "policies")
			}
		}
	}
	im.mapSkills(rec)
	im.mapModel(rec, legacyFallbacks)
	im.mapTools(rec, string(typed.Type))
	out, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("omnpkg: encode mapped record: %w", err)
	}
	return &AgentPlan{Record: out, Warnings: im.warnings}, nil
}

// decodeAgentRecord gives the one interpretation of a package record that
// validation, mapping and create all read (#1952). The record is decoded with
// config.AgentConfig, the type the created agent is stored as, and the
// mapping works on that value's own encoding. Decoding into a struct accepts
// any letter case of a field name and lets a later member override an earlier
// one; a generic map keeps such spellings as separate keys and its encoding
// sorts them, so mapping the raw members could hand create a different type,
// runtime or effort than the checks saw. After this step only the stored
// spelling of each field exists.
func decodeAgentRecord(record []byte, sourceID string) (config.AgentConfig, map[string]any, error) {
	var typed config.AgentConfig
	if err := json.Unmarshal(record, &typed); err != nil {
		return typed, nil, newErr(CodeInvalidElement, "element", sourceID, "reason", "agent.json has a field of the wrong type")
	}
	canonical, err := json.Marshal(typed)
	if err != nil {
		return typed, nil, fmt.Errorf("omnpkg: encode agent record: %w", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(canonical, &rec); err != nil {
		return typed, nil, fmt.Errorf("omnpkg: decode agent record: %w", err)
	}
	return typed, rec, nil
}

// recordFieldsView reads, with the same name matching and member order as
// config.AgentConfig, the facts its typed value loses: a present
// max_tool_iterations of zero or with a fraction, and the raw model value.
type recordFieldsView struct {
	MaxToolIterations *float64        `json:"max_tool_iterations"`
	Model             json.RawMessage `json:"model"`
}

// modelFallbacksView reads whether a model object carries the legacy
// fallbacks list (any value, even empty or null).
type modelFallbacksView struct {
	Fallbacks json.RawMessage `json:"fallbacks"`
}

// hasLegacyModelFallbacks reports a legacy model.fallbacks member, which the
// typed value drops when the list is empty, so the field_not_carried warning
// still lists it (Field mapping row D3-6).
func hasLegacyModelFallbacks(record []byte, sourceID string) (bool, error) {
	var view recordFieldsView
	if err := json.Unmarshal(record, &view); err != nil {
		return false, newErr(CodeInvalidElement, "element", sourceID, "reason", "agent.json has a field of the wrong type")
	}
	trimmed := bytes.TrimSpace(view.Model)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false, nil // absent, null or the bare-string form: no list
	}
	var model modelFallbacksView
	if err := json.Unmarshal(trimmed, &model); err != nil {
		return false, newErr(CodeInvalidElement, "element", sourceID, "reason", "agent.json has a field of the wrong type")
	}
	return model.Fallbacks != nil, nil
}

// checkMaxToolIterations refuses a present value outside the create rule's
// 1–1000 range, then one above the target's global limit (Field mapping;
// Dataset D5 row 11). An omitted field is no own limit and passes.
func (im *importMapper) checkMaxToolIterations(record []byte, sourceID string) error {
	var view recordFieldsView
	if err := json.Unmarshal(record, &view); err != nil {
		return newErr(CodeInvalidElement, "element", sourceID, "reason", "agent.json has a field of the wrong type")
	}
	if view.MaxToolIterations == nil {
		return nil
	}
	v := *view.MaxToolIterations
	if v != float64(int(v)) || config.ValidateMaxToolIterationsBound(int(v)) != nil {
		return fieldErr(CodeElementNotCreatable, "max_tool_iterations", config.MaxToolIterationsBoundMessage)
	}
	if limit := im.target.GlobalMaxToolIterations(); limit > 0 && v > float64(limit) {
		return fieldErr(CodeElementNotCreatable, "max_tool_iterations",
			"above this install's limit of "+strconv.Itoa(limit))
	}
	return nil
}

// mapSkills keeps the skills installed on the target and drops the rest,
// before the create rule (which refuses unknown skills) runs.
func (im *importMapper) mapSkills(rec map[string]any) {
	list, ok := rec["skills"].([]any)
	if !ok {
		return
	}
	var kept, dropped []string
	for _, item := range list {
		slug, isString := item.(string)
		switch {
		case !isString:
			dropped = append(dropped, fmt.Sprint(item))
		case skillname.Valid(slug) && im.target.InstalledSkill(slug):
			kept = append(kept, slug)
		default:
			dropped = append(dropped, slug)
		}
	}
	if len(kept) > 0 {
		rec["skills"] = kept
		im.warn(WarnSkillBoundLocal, "Bound to the skill of the same name installed here.", kept)
	} else {
		delete(rec, "skills")
	}
	if len(dropped) > 0 {
		im.warn(WarnSkillDropped, "Left out: not installed here.", dropped)
	}
}

// mapModel drops the legacy fallback strings and lists a provider the target
// has not configured (the name is kept).
func (im *importMapper) mapModel(rec map[string]any, legacyFallbacks bool) {
	if legacyFallbacks {
		im.warn(WarnFieldNotCarried, "Legacy fallback list left out; fallback_models is used instead.",
			[]string{"model.fallbacks"})
	}
	model, ok := rec["model"].(map[string]any)
	if !ok {
		return
	}
	delete(model, "fallbacks")
	if p, isString := model["provider"].(string); isString && p != "" && !im.target.ProviderConfigured(p) {
		im.warn(WarnProviderNotConfigured, "The model provider is not configured on this install.", []string{p})
	}
}

// mapTools drops every connector assignment and filters and tightens the
// built-in tool policies, filling tools the map lacks with the seed of the
// decoded agent type (any letter case of the "type" name, #1953).
func (im *importMapper) mapTools(rec map[string]any, agentType string) {
	tools, ok := rec["tools"].(map[string]any)
	if !ok {
		return
	}
	if mcp, isMap := tools["mcp"].(map[string]any); isMap {
		if ids := mcpServerIDs(mcp["servers"]); len(ids) > 0 {
			im.warn(WarnMCPBindingDropped, "Connector assignments are not imported; assign connectors after import.", ids)
		}
		delete(mcp, "servers")
		if len(mcp) == 0 {
			delete(tools, "mcp")
		}
	}
	builtin, isMap := tools["builtin"].(map[string]any)
	if !isMap {
		return
	}
	policies, isMap := builtin["policies"].(map[string]any)
	if !isMap {
		return
	}
	im.mapPolicies(policies, agentType)
}

func (im *importMapper) mapPolicies(policies map[string]any, agentType string) {
	names := make([]string, 0, len(policies))
	for name := range policies {
		names = append(names, name)
	}
	sort.Strings(names)
	var dropped, tightened []string
	for _, name := range names {
		if !im.target.KnownTool(name) {
			delete(policies, name)
			dropped = append(dropped, name)
			continue
		}
		if policies[name] == "allow" && im.target.AlwaysAskTool(name) {
			policies[name] = "ask"
			tightened = append(tightened, name)
		}
	}
	for name, value := range im.target.SeedToolPolicies(agentType) {
		if _, has := policies[name]; !has {
			policies[name] = value
		}
	}
	if len(dropped) > 0 {
		im.warn(WarnToolPolicyDropped, "Tool policies for tools this install does not have were left out.", dropped)
	}
	if len(tightened) > 0 {
		im.warn(WarnToolPolicyTightened, "Allow on an always-ask tool became ask.", tightened)
	}
}

// mcpServerIDs lists the server IDs (or a positional label) of an MCP
// assignment list.
func mcpServerIDs(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(list))
	for i, item := range list {
		if m, isMap := item.(map[string]any); isMap {
			if id, isString := m["id"].(string); isString && id != "" {
				ids = append(ids, id)
				continue
			}
		}
		ids = append(ids, "server "+strconv.Itoa(i+1))
	}
	return ids
}

// FreshAgentID returns a fresh UUID that is not in taken (FR-031). taken is
// not modified.
func FreshAgentID(taken map[string]bool) (string, error) {
	const attempts = 16
	for range attempts {
		id, err := uuid.NewRandom()
		if err != nil {
			return "", fmt.Errorf("omnpkg: generate agent ID: %w", err)
		}
		if s := id.String(); !taken[s] {
			return s, nil
		}
	}
	return "", fmt.Errorf("omnpkg: no fresh agent ID after %d attempts", attempts)
}

// maxImportSuffix is the highest " (imported N)" suffix (Dataset D6).
const maxImportSuffix = 99

// ResolveImportName decides the final name for an incoming agent against the
// existing names: unchanged, or " (imported)" / " (imported 2)" …
// " (imported 99)", kept within MaxAgentNameLen (FR-032, D6). Clash matching
// ignores letter case and surrounding spaces only. The incoming name is
// trimmed (create rule); an empty or over-long name is not creatable.
func ResolveImportName(incoming string, existing []string) (string, error) {
	base := strings.TrimSpace(incoming)
	if base == "" {
		return "", fieldErr(CodeElementNotCreatable, "name", "the name is empty")
	}
	if len([]rune(base)) > MaxAgentNameLen {
		return "", fieldErr(CodeElementNotCreatable, "name", "the name is longer than 100 characters")
	}
	taken := make(map[string]bool, len(existing))
	for _, n := range existing {
		taken[nameKey(n)] = true
	}
	if !taken[nameKey(base)] {
		return base, nil
	}
	for n := 1; n <= maxImportSuffix; n++ {
		suffix := " (imported)"
		if n > 1 {
			suffix = " (imported " + strconv.Itoa(n) + ")"
		}
		candidate := strings.TrimSpace(truncateRunes(base, MaxAgentNameLen-len([]rune(suffix)))) + suffix
		if !taken[nameKey(candidate)] {
			return candidate, nil
		}
	}
	return "", newErr(CodeNameSuffixExhausted, "name", base)
}

// nameKey is the clash key: surrounding spaces and letter case ignored.
func nameKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
