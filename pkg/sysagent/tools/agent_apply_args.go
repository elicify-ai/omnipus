package systools

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// agentUpdateArgNames is update_agent's field vocabulary. Per-agent tool
// policies (tool_policy_changes) and connector assignments (mcp_servers) are
// NOT in it: set_agent_permissions is the only agent-tool path for those on an
// existing agent (omnipus-system-agent FR-024), and update_agent refuses them
// by name (permissionFieldRefusal) rather than as unknown fields.
var agentUpdateArgNames = map[string]struct{}{
	"id": {}, "revision": {}, "name": {}, "description": {}, "soul": {},
	"model": {}, "provider": {}, "fallback_models": {}, "color": {},
	"figure": {}, "role": {}, // visual identity (#1303); protection per ADR-090 in ValidateFields
	"max_tool_iterations": {}, "skills": {},
	"memory_enabled": {}, "voice": {},
	"context_window_override": {}, "model_params": {},
	"agent_md": {}, // AGENT.md (#1064), written beside the record
}

// permissionFieldNames are the capability fields only set_agent_permissions
// may write on an existing agent (FR-024).
var permissionFieldNames = []string{"tool_policy_changes", "mcp_servers"}

// permissionFieldRefusal returns update_agent's refusal when args carries a
// permission field — present at all, a clear or a null included — so the
// whole call is refused before any write (BDD-14.2). Nil otherwise.
func permissionFieldRefusal(args map[string]any) *tools.ToolResult {
	for _, name := range permissionFieldNames {
		if _, present := args[name]; present {
			return tools.ErrorResult(errorJSON("USE_SET_AGENT_PERMISSIONS",
				name+" cannot be changed with update_agent; per-agent tool policies and connector assignments change only through set_agent_permissions",
				"Call set_agent_permissions with the agent id, its revision and the capability change"))
		}
	}
	return nil
}

var agentCreateArgNames = map[string]struct{}{
	"name": {}, "description": {}, "soul": {}, "model": {}, "color": {},
	"figure": {}, "role": {}, // visual identity (#1303); defaults resolved in validate
	"agent_type": {}, "cli": {}, "cli_path": {}, "provider": {}, "fallback_models": {},
	"heartbeat": {}, "max_tool_iterations": {}, "skills": {}, "mcp_servers": {},
	"tool_policy_changes": {}, "memory_enabled": {}, "voice": {},
	"context_window_override": {}, "model_params": {},
	workspaceIDParam: {}, // destination workspace (FR-040), not an agent field
}

func rejectUnknownAgentFields(args map[string]any, allowed map[string]struct{}) error {
	for name := range args {
		if _, ok := allowed[name]; !ok {
			return fieldErr(name, fmt.Sprintf("unknown field %q", name))
		}
	}
	return nil
}

func mutationFieldNames(args map[string]any) []string {
	fields := make([]string, 0, len(args))
	for name := range args {
		if name == "id" || name == "revision" || name == "agent_type" || name == "cli" || name == "cli_path" ||
			name == "heartbeat" || name == "agent_md" || name == workspaceIDParam {
			continue
		}
		fields = append(fields, name)
	}
	return fields
}

// applyAgentToolArgs applies create_agent/update_agent args onto a. defaults
// is the global agent config in force (may be nil — the resolver then uses
// the shipped default); the #904 per-agent tool-iteration limit is checked
// against it (D15).
func applyAgentToolArgs(a *config.AgentConfig, args map[string]any, known map[string]struct{}, inv AgentConfigInventory,
	defaults *config.AgentDefaults,
) error {
	if err := applyIdentityAndModelArgs(a, args, defaults); err != nil {
		return err
	}
	if err := applyCapabilityArgs(a, args, known, inv); err != nil {
		return err
	}
	return applySharedConfigArgs(a, args)
}

func applyIdentityAndModelArgs(a *config.AgentConfig, args map[string]any, defaults *config.AgentDefaults) error {
	if err := applyOptionalString(args, "name", func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fieldErr("name", "name must be nonblank")
		}
		if coreagent.AgentNameTooLong(v) {
			return fieldErr("name", coreagent.AgentNameTooLongRefusal)
		}
		a.Name = v
		return nil
	}); err != nil {
		return err
	}
	if err := applyOptionalString(args, "description", func(v string) error {
		a.Description = v
		return nil
	}); err != nil {
		return err
	}
	if err := applyOptionalString(args, "color", func(v string) error {
		canon, err := canonicalAgentColor(v)
		if err != nil {
			return err
		}
		a.Color = canon
		return nil
	}); err != nil {
		return err
	}
	if err := applyVisualIdentityArgs(a, args); err != nil {
		return err
	}
	if err := applyOptionalString(args, "model", func(v string) error {
		if a.Model == nil {
			a.Model = &config.AgentModelConfig{}
		}
		a.Model.Primary = v
		return nil
	}); err != nil {
		return err
	}
	if err := applyOptionalString(args, "provider", func(v string) error {
		if a.Model == nil {
			a.Model = &config.AgentModelConfig{}
		}
		a.Model.Provider = strings.TrimSpace(v)
		return nil
	}); err != nil {
		return err
	}
	if raw, present := args["fallback_models"]; present {
		if err := applyFallbackModels(a, raw); err != nil {
			return err
		}
	}
	if raw, present := args["max_tool_iterations"]; present {
		return applyMaxToolIterationsArg(a, raw, defaults)
	}
	return nil
}

// applyMaxToolIterationsArg applies the #904 per-agent tool-iteration limit
// (D15 — the same rules as REST): an explicit JSON null clears the own value
// (0 is omitted from the record, so the agent rides the global — FR-008);
// checked BEFORE jsonInt, whose float64 assertion would reject nil. A number
// must be a whole number within 1..1000 and not above the global in force
// (FR-006, FR-007, D10), with the resolver's exact messages.
func applyMaxToolIterationsArg(a *config.AgentConfig, raw any, defaults *config.AgentDefaults) error {
	if raw == nil {
		a.MaxToolIterations = 0
		return nil
	}
	n, err := jsonInt(raw, "max_tool_iterations")
	if err != nil {
		return err
	}
	if err := config.ValidateAgentMaxToolIterations(n, defaults); err != nil {
		return fieldErr("max_tool_iterations", err.Error())
	}
	a.MaxToolIterations = n
	return nil
}

// agentDefaults returns the global agent config in force, or nil when no
// config is wired (tests); nil resolves to the shipped default.
func (d *Deps) agentDefaults() *config.AgentDefaults {
	if d == nil || d.GetCfg == nil {
		return nil
	}
	cfg := d.GetCfg()
	if cfg == nil {
		return nil
	}
	return &cfg.Agents.Defaults
}

func applyCapabilityArgs(a *config.AgentConfig, args map[string]any, known map[string]struct{}, inv AgentConfigInventory) error {
	if _, present := args["skills"]; present && a.IsExternalCLIWorker() {
		return fieldErr("skills", "field is unknown or unsupported by this runtime")
	}
	if _, present := args["mcp_servers"]; present && a.IsExternalCLIWorker() {
		return fieldErr("mcp_servers", "field is unknown or unsupported by this runtime")
	}
	if _, present := args["tool_policy_changes"]; present && a.IsExternalCLIWorker() {
		return fieldErr("tool_policy_changes", "field is unknown or unsupported by this runtime")
	}
	if raw, present := args["skills"]; present {
		vals, err := requiredStringArray(raw, "skills")
		if err != nil {
			return err
		}
		if err := validateSkillAssignment(vals, inv); err != nil {
			return err
		}
		a.Skills = vals
	}
	if raw, present := args["mcp_servers"]; present {
		vals, err := decodeMCPBindings(raw)
		if err != nil {
			return err
		}
		if err := validateMCPAssignment(vals, inv); err != nil {
			return err
		}
		if a.Tools == nil {
			a.Tools = &config.AgentToolsCfg{}
		}
		a.Tools.MCP.Servers = vals
	}
	if raw, present := args["tool_policy_changes"]; present {
		patch, err := decodePolicyPatch(raw)
		if err != nil {
			return err
		}
		if a.Tools == nil {
			a.Tools = &config.AgentToolsCfg{}
		}
		next, err := agentmutation.ApplyToolPolicyChanges(a.Tools.Builtin.Policies, patch, known)
		if err != nil {
			return err
		}
		a.Tools.Builtin.Policies = next
	}
	return nil
}

func applySharedConfigArgs(a *config.AgentConfig, args map[string]any) error {
	if raw, present := args["memory_enabled"]; present {
		b, ok := raw.(bool)
		if !ok {
			return fieldErr("memory_enabled", "memory_enabled must be a boolean")
		}
		a.MemoryEnabled = &b
	}
	if err := applyOptionalString(args, "voice", func(v string) error {
		if a.IsWorker() && strings.TrimSpace(v) != "" {
			return fieldErr("voice", "a worker cannot have a per-agent voice (workers are not chat personas)")
		}
		a.Voice = v
		return nil
	}); err != nil {
		return err
	}
	if raw, present := args["context_window_override"]; present {
		if err := applyContextWindow(a, raw); err != nil {
			return err
		}
	}
	if raw, present := args["model_params"]; present {
		if err := applyModelParams(a, raw); err != nil {
			return err
		}
	}
	return nil
}

func applyOptionalString(args map[string]any, key string, apply func(string) error) error {
	raw, present := args[key]
	if !present {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		return fieldErr(key, key+" must be a string")
	}
	return apply(s)
}

func applyContextWindow(a *config.AgentConfig, raw any) error {
	if raw == nil {
		a.ContextWindowOverride = nil
		return nil
	}
	n, err := jsonInt(raw, "context_window_override")
	if err != nil {
		return err
	}
	if n <= 0 {
		return fieldErr("context_window_override", "context_window_override must be a positive integer")
	}
	a.ContextWindowOverride = &n
	return nil
}

func applyModelParams(a *config.AgentConfig, raw any) error {
	m, ok := raw.(map[string]any)
	if !ok {
		return fieldErr("model_params", "model_params must be a non-null object")
	}
	for key := range m {
		if key != "temperature" && key != "max_tokens" {
			return fieldErr("model_params", fmt.Sprintf("unknown model_params field %q", key))
		}
	}
	merged := &config.AgentModelParams{}
	if a.ModelParams != nil {
		cp := *a.ModelParams
		merged = &cp
	}
	if value, present := m["temperature"]; present {
		f, ok := value.(float64)
		if !ok {
			return fieldErr("model_params", "temperature must be a number")
		}
		merged.Temperature = &f
	}
	if value, present := m["max_tokens"]; present {
		n, err := jsonInt(value, "model_params.max_tokens")
		if err != nil {
			return err
		}
		merged.MaxTokens = &n
	}
	a.ModelParams = merged
	return nil
}

// jsonInt reads a whole number: a JSON-decoded float64 without a fraction,
// or a Go integer from an in-process caller.
func jsonInt(raw any, field string) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		if v < math.MinInt || v > math.MaxInt {
			return 0, fieldErr(field, field+" must be an integer")
		}
		return int(v), nil
	case float64:
		// Check the signed-int range BEFORE conversion: an out-of-range float
		// converts implementation-dependently, and ARM64 can saturate to MaxInt.
		// float64(MaxInt64) rounds up to 2^63, so a round-trip alone misses it.
		limit := math.Ldexp(1, strconv.IntSize-1)
		if v < -limit || v >= limit {
			return 0, fieldErr(field, field+" must be an integer")
		}
		n := int(v)
		if float64(n) == v {
			return n, nil
		}
	}
	return 0, fieldErr(field, field+" must be an integer")
}

func requiredStringArray(raw any, field string) ([]string, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fieldErr(field, field+" must be a non-null array")
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, fieldErr(field, field+" entries must be nonblank strings")
		}
		out = append(out, s)
	}
	return out, nil
}

func decodeMCPBindings(raw any) ([]config.AgentMCPServerBinding, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fieldErr("mcp_servers", "mcp_servers must be a non-null array")
	}
	out := make([]config.AgentMCPServerBinding, 0, len(arr))
	seen := map[string]struct{}{}
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fieldErr("mcp_servers", "mcp_servers entries must be objects")
		}
		for key := range m {
			if key != "id" && key != "tools" {
				return nil, fieldErr("mcp_servers", fmt.Sprintf("unknown mcp_servers field %q", key))
			}
		}
		id, okID := m["id"].(string)
		if !okID {
			return nil, fieldErr("mcp_servers", "mcp_servers.id must be a string")
		}
		b := config.AgentMCPServerBinding{ID: id}
		if toolsRaw, present := m["tools"]; present {
			vals, err := requiredStringArray(toolsRaw, "mcp_servers.tools")
			if err != nil {
				return nil, err
			}
			b.Tools, b.ToolsSpecified = vals, true
		}
		if err := config.ValidateAgentMCPServerBinding(b); err != nil {
			return nil, fieldErr("mcp_servers", err.Error())
		}
		if _, dup := seen[id]; dup {
			return nil, fieldErr("mcp_servers", fmt.Sprintf("duplicate mcp server %q", id))
		}
		seen[id] = struct{}{}
		out = append(out, b)
	}
	return out, nil
}

func decodePolicyPatch(raw any) (agentmutation.ToolPolicyChanges, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return agentmutation.ToolPolicyChanges{}, fieldErr("tool_policy_changes", "tool_policy_changes must be a non-null object")
	}
	for key := range m {
		if key != "set" && key != "remove" {
			return agentmutation.ToolPolicyChanges{}, fieldErr("tool_policy_changes", fmt.Sprintf("unknown tool_policy_changes field %q", key))
		}
	}
	p := agentmutation.ToolPolicyChanges{}
	if setRaw, present := m["set"]; present {
		setMap, ok := setRaw.(map[string]any)
		if !ok {
			return p, fieldErr("tool_policy_changes", "tool_policy_changes.set must be an object")
		}
		p.Set = map[string]config.ToolPolicy{}
		for name, v := range setMap {
			s, ok := v.(string)
			if !ok {
				return p, fieldErr("tool_policy_changes", fmt.Sprintf("policy for %s must be a string", name))
			}
			p.Set[name] = config.ToolPolicy(s)
		}
	}
	if remRaw, present := m["remove"]; present {
		vals, err := requiredStringArray(remRaw, "tool_policy_changes.remove")
		if err != nil {
			return p, err
		}
		p.Remove = vals
	}
	return p, nil
}

// publishAgentActivation publishes id into the live, in-memory config/registry
// that pkg/gateway/rest_agents.go::listAgents reads, and returns the resulting
// activation status. It must NEVER report ActivationActive while the agent
// might still be missing from that live list — see publishAndRespond's gate for
// the caller-side enforcement; this function's job is to block until publication
// has actually landed so that gate has a real, observable signal to trust.
//
// The fast path (UpsertAgentFastFunc, issue #571 sysagent half) is what
// AgentCreateTool/AgentUpdateTool prefer, but the production wired
// implementation (pkg/gateway/gateway.go's closure) DOES NOT promise a
// synchronous live-list update on its nil return: it re-derives
// cfg.Agents.List via MutateConfig first, then calls
// agentLoop.UpsertAgentFast — and on any step's failure (IsReloadPending at
// entry, MutateConfig error, or UpsertAgentFast error) it falls back to
// rc.reloadTrigger(), which is ASYNCHRONOUS (it only enqueues onto
// manualReloadChan / claims the coalescing slot and returns nil — the actual
// registry rebuild happens on a separate goroutine, see
// pkg/gateway/gateway_reload.go::newReloadTrigger). A nil return from
// UpsertAgentFastFunc therefore means "publication was accepted, but may still
// be in flight on the reload goroutine" — NOT "the agent is already live in
// the registry". The fallback ReloadFunc below is also fire-and-forget for the
// same reason.
//
// Closing that race: after the publish hook returns nil, call
// deps.WaitForPendingReloadFunc when wired — the wait-only IsReloadPending
// poll (pkg/gateway/rest_auth.go::waitForPendingReload), which NEVER calls
// TriggerReload / reloadTrigger. This blocks until any reload already queued
// by the fallback path has rebuilt the live registry the gate consults, so a
// nil return from THIS function is now a genuine "agent is live and listable"
// — not just "agent was queued for publication". On the fast-path-success
// branch (UpsertAgentFastFunc updated the live registry inline, nothing
// queued) the pending flag is already clear so this returns IMMEDIATELY — a
// genuine no-op, exactly what the agent-picker-freshness fix (#1009) needs.
//
// WHY WaitForPendingReloadFunc AND NOT WaitForReloadFunc: WaitForReloadFunc's
// production implementation (pkg/gateway/rest_auth.go::waitForReload, which
// delegates to that same file's waitForReloadOutcome) calls
// agentLoop.TriggerReload as its FIRST statement, unconditionally — it does
// NOT check IsReloadPending before triggering. That shape is correct for
// delete_agent (AgentDeleteTool.reload, no fast path available, reload must
// happen) but is WRONG here: on the fast-path-success branch the live
// in-memory registry is already up-to-date and NOTHING is pending, so calling
// WaitForReloadFunc would kick off a brand-new, unnecessary full reload cycle
// (channels/cron/plan-engine/schedulers all restart — up to ~60s under load,
// the exact mechanism issue #571 exists to avoid) just to wait for it. The
// round-3 version of this comment (commit 500beb747) claimed the wait "is a
// no-op in that case but a real block in the queued-reload case" — that
// claim was FALSE for WaitForReloadFunc (it always triggers) and is TRUE for
// the new WaitForPendingReloadFunc this function now uses.
//
// WaitForPendingReloadFunc is nil in tests / degraded wiring where there is no
// real reload to wait on (those tests either stub the fast path itself to
// update the registry inline, or use a synchronous ReloadFunc stand-in).
// When it is nil, fall back to the old behaviour: the publish hook's nil
// return is taken at face value — adequate for the test wiring, and
// explicitly NOT claimed as safe in production (the production gateway
// always wires both).
func publishAgentActivation(deps *Deps, id string) (agentstore.ActivationStatus, string) {
	if deps != nil && deps.UpsertAgentFastFunc != nil {
		if err := deps.UpsertAgentFastFunc(id); err != nil {
			return agentstore.ActivationFailed, err.Error()
		}
		if deps.WaitForPendingReloadFunc != nil {
			if err := deps.WaitForPendingReloadFunc(); err != nil {
				return agentstore.ActivationFailed, err.Error()
			}
		}
		return agentstore.ActivationActive, ""
	}
	if deps != nil && deps.ReloadFunc != nil {
		if err := deps.ReloadFunc(); err != nil {
			return agentstore.ActivationFailed, err.Error()
		}
		if deps.WaitForPendingReloadFunc != nil {
			if err := deps.WaitForPendingReloadFunc(); err != nil {
				return agentstore.ActivationFailed, err.Error()
			}
		}
		return agentstore.ActivationActive, ""
	}
	return agentstore.ActivationNotAttempted, ""
}
