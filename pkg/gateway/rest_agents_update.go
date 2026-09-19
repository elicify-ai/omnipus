// rest_agents_update.go: Update an agent

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// firstForbiddenSubagent3pField moved to agent_field_rules.go (W2a) — it now
// only handles the AgentUpdateRequest shape (create-time forbidden fields are
// enforced structurally by the discriminated-union AgentCreateRequestSubagent3p
// type, which has no matching properties at all).

// restAPIUpdateAgent carries the shared state of updateAgent across its stages.
type restAPIUpdateAgent struct {
	a                           *restAPI
	id                          string
	req                         gen.AgentUpdateRequest
	now                         time.Time
	clearsContextWindowOverride bool
	updatedExecutor             *config.ExecutorConfig
	newName                     string
	newModel                    string
	defaultAgentIDChanged       bool
	modelIdentityChanged        bool
	mutationResult              agentstore.MutationResult
	activationFailed            string
}

// restAPIUpdateAgentFlow carries the shared state of updateAgent across its stages.
type restAPIUpdateAgentFlow struct {
	w                   http.ResponseWriter
	r                   *http.Request
	ru                  *restAPIUpdateAgent
	cfg                 *config.Config
	foundIdx            int
	rawBody             []byte
	foundAgent          config.AgentConfig
	toolsCoverageMutate func(*config.Config)
	workspace           string
}

func suppliedRESTAgentFields(req *gen.AgentUpdateRequest) []string {
	if req == nil {
		return nil
	}
	fields := make([]string, 0, 20)
	add := func(present bool, name string) {
		if present {
			fields = append(fields, name)
		}
	}
	add(req.Name != nil, "name")
	add(req.Description != nil, "description")
	add(req.Color != nil, "color")
	add(req.Icon != nil, "icon")
	add(req.Soul != nil, "soul")
	add(req.Skills != nil, "skills")
	add(req.McpServers != nil, "mcp_servers")
	add(req.ToolsCfg != nil, "tools_cfg")
	add(req.ToolPolicyChanges != nil, "tool_policy_changes")
	add(req.Model != nil, "model")
	add(req.Provider != nil, "provider")
	add(req.FallbackModels != nil, "fallback_models")
	add(req.ContextWindowOverride != nil, "context_window_override")
	add(req.ModelParams != nil, "model_params")
	add(req.MaxToolIterations != nil, "max_tool_iterations")
	add(req.MemoryEnabled != nil, "memory_enabled")
	add(req.Default != nil, "default")
	add(req.Voice != nil, "voice")
	add(req.Executor != nil, "executor")
	add(req.ShellPolicy != nil, "shell_policy")
	return fields
}

func (a *restAPI) updateAgent(w http.ResponseWriter, r *http.Request, id string) {
	uf := &restAPIUpdateAgentFlow{w: w, r: r}

	uf.ru = &restAPIUpdateAgent{a: a, id: id}

	if uf.validateRequest() {
		return
	}

	// Locked core agents: reject identity and prompt mutations.
	// Allowed: model selection, heartbeat schedule (enabled/interval), tools (via updateAgentTools).
	if uf.validateTarget() {
		return
	}
	// ADR-037: delegation_policy is retired from the wire entirely — the
	// per-workspace delegation graph (Team tab) is the sole delegation
	// mechanism. There is nothing left to merge/validate/persist here. See
	// the raw-body sniff below (before decode) for the loud-400 rejection of
	// a client still sending this retired field.
	// Persist to config.json BEFORE mutating the live config.
	// Capture the new values to apply after persistence succeeds.
	if uf.preparePersistence() {
		return
	}
	if uf.persistAndReload() {
		return
	}
	uf.respond()
}

// validateRequest finds the target and validates the request body and context-window input.
func (uf *restAPIUpdateAgentFlow) validateRequest() bool {
	uf.cfg = uf.ru.a.agentLoop.GetConfig()
	uf.foundIdx = -1
	for i := range uf.cfg.Agents.List {
		if uf.cfg.Agents.List[i].ID == uf.ru.id {
			uf.foundIdx = i
			break
		}
	}
	if uf.foundIdx < 0 {
		jsonErr(uf.w, http.StatusNotFound, fmt.Sprintf("agent %q not found", uf.ru.id))
		return true
	}

	// ADR-035 / ADR-037: sandbox_profile and delegation_policy are retired from
	// the wire entirely. decodeAgentCreateVariant already rejects retired
	// fields on the create path via unconditional DisallowUnknownFields, but
	// decodeAndValidate's fast path below is non-strict by default
	// (validate_inbound defaults false) and gen.AgentUpdateRequest no longer
	// has either field at all — without this explicit raw-body sniff a client
	// still sending {"sandbox_profile":...} or {"delegation_policy":...} would
	// have the field silently dropped by Go's default JSON decode, and the PUT
	// would report 200 with no change applied instead of the loud 400 this
	// codebase's own create-path convention expects (ADR-035 §7 established
	// this raw-body-sniff pattern for exactly this failure mode; ADR-037
	// follows the same precedent rather than accepting the silent drop). Read
	// +restore r.Body so the normal decode below is unaffected.
	var readErr error
	uf.rawBody, readErr = io.ReadAll(io.LimitReader(uf.r.Body, 1<<20))
	if readErr != nil {
		jsonErr(uf.w, http.StatusBadRequest, "could not read request body")
		return true
	}
	uf.r.Body = io.NopCloser(bytes.NewReader(uf.rawBody))
	// The strict generated-shape validator intentionally rejects retired or
	// unknown fields before semantic handlers run. A system-agent disable
	// attempt uses exactly such an unknown field (`enabled`/`disabled`), so
	// check that protection before shape validation to preserve its specific
	// operator-facing error.
	if uf.rejectSystemAgentDisable() {
		return true
	}
	if bytes.Contains(uf.rawBody, []byte(`"sandbox_profile"`)) {
		jsonErr(uf.w, http.StatusBadRequest,
			`sandbox_profile is retired — use the global god-mode switch (POST /api/v1/gateway/god-mode)`)
		return true
	}
	if bytes.Contains(uf.rawBody, []byte(`"delegation_policy"`)) {
		jsonErr(
			uf.w,
			http.StatusBadRequest,
			`delegation_policy is retired — delegation is now configured exclusively via the workspace Team tab (PUT /api/v1/workspaces/{id}/delegation)`,
		)
		return true
	}
	// model_params.top_p (T2): removed from the wire entirely (see
	// agentModelParamsInput's doc comment — no provider adapter implements
	// nucleus sampling and there is no global default to fall back to).
	// gen.AgentUpdateRequest.ModelParams no longer has a TopP field at all,
	// and decodeAndValidate's fast path below is non-strict by default
	// (validate_inbound defaults false) — without this explicit raw-body
	// sniff a client still sending {"model_params":{"top_p":...}} would have
	// the field silently dropped by Go's default JSON decode, and the PUT
	// would report 200 with no change applied instead of the loud 400 this
	// codebase's own create-path convention expects (same
	// sandbox_profile/delegation_policy raw-body-sniff precedent above).
	if bytes.Contains(uf.rawBody, []byte(`"top_p"`)) {
		jsonErr(uf.w, http.StatusBadRequest,
			"model_params.top_p is not supported by any provider adapter")
		return true
	}

	validateEnabled := uf.cfg.Gateway.ValidateInbound
	if !decodeAndValidate(uf.w, uf.r, "AgentUpdateRequest", &uf.ru.req, validateEnabled) {
		return true
	}
	if uf.ru.req.Revision == "" {
		jsonErr(uf.w, http.StatusBadRequest, "revision is required")
		return true
	}
	if err := agentstore.ValidateRevision(uf.ru.req.Revision); err != nil {
		jsonErr(uf.w, http.StatusBadRequest, err.Error())
		return true
	}
	var presence map[string]json.RawMessage
	if err := json.Unmarshal(uf.rawBody, &presence); err == nil {
		if _, supplied := presence["updated_at"]; supplied {
			writeJSON(uf.w, http.StatusBadRequest, gen.ErrorResponse{
				Error: "updated_at is read-only; use revision for configuration updates",
				Code:  strPtr("invalid_input"),
				Field: strPtr("updated_at"),
			})
			return true
		}
		for _, field := range []string{"skills", "mcp_servers", "tool_policy_changes", "soul"} {
			if raw, ok := presence[field]; ok && string(bytes.TrimSpace(raw)) == "null" {
				jsonErr(uf.w, http.StatusBadRequest, field+" must not be null")
				return true
			}
		}
	}
	if !validateAgentUpdateShape(uf.w, uf.rawBody) {
		return true
	}
	if uf.ru.req.ToolsCfg != nil && uf.ru.req.ToolPolicyChanges != nil {
		jsonErr(uf.w, http.StatusBadRequest, "tools_cfg and tool_policy_changes cannot be supplied together")
		return true
	}
	// ADR-071 §5.1.3 part 2: "default" (case-insensitive) is reserved — see
	// the identical check in createAgent for the full rationale. An agent's
	// id is never editable via PUT (it comes from the URL path, matched
	// against cfg.Agents.List above), so only the name half is reachable
	// here too; a pre-existing agent literally id'd "default" is covered by
	// the boot-time WARN (part 3), not this rejection.
	if uf.ru.req.Name != nil && strings.EqualFold(strings.TrimSpace(*uf.ru.req.Name), tools.SwitchAgentDefaultTarget) {
		jsonErr(uf.w, http.StatusBadRequest,
			fmt.Sprintf("agent name %q is reserved — it collides with switch_agent's target:%q sentinel", strings.TrimSpace(*uf.ru.req.Name), tools.SwitchAgentDefaultTarget))
		return true
	}
	// Timestamp applied to the persisted agent on every successful save.
	uf.ru.now = time.Now().UTC()
	// Validate any custom deny patterns in shell_policy — each must be a valid Go regexp.
	if uf.ru.req.ShellPolicy != nil && uf.ru.req.ShellPolicy.CustomDenyPatterns != nil {
		for _, pat := range *uf.ru.req.ShellPolicy.CustomDenyPatterns {
			if _, compileErr := regexp.Compile(pat); compileErr != nil {
				jsonErr(uf.w, http.StatusBadRequest,
					fmt.Sprintf("shell_policy.custom_deny_patterns: invalid regexp %q: %v", pat, compileErr))
				return true
			}
		}
	}
	// ADR-066 D2 rung 1 — context_window_override. The generated *int
	// collapses "absent" and "null" to nil, but the contract gives them
	// different meanings ("send null to clear"), so peek the raw body for an
	// explicit null exactly as the sandbox_profile sniff above does.
	uf.ru.clearsContextWindowOverride = false
	if uf.ru.req.ContextWindowOverride != nil {
		if *uf.ru.req.ContextWindowOverride < 1 {
			jsonErr(uf.w, http.StatusBadRequest,
				"context_window_override must be ≥ 1 (send null to clear it)")
			return true
		}
	} else {
		var windowPeek map[string]json.RawMessage
		if json.Unmarshal(uf.rawBody, &windowPeek) == nil {
			if v, present := windowPeek["context_window_override"]; present &&
				string(bytes.TrimSpace(v)) == "null" {
				uf.ru.clearsContextWindowOverride = true
			}
		}
	}
	return false
}

// rejectSystemAgentDisable inspects the raw update body before strict wire-shape
// validation. AgentUpdateRequest deliberately has no enabled/disabled field; a
// disable attempt is therefore an unknown-field request, but System Agents need
// their explicit protection message rather than a generic schema error.
func (uf *restAPIUpdateAgentFlow) rejectSystemAgentDisable() bool {
	uf.foundAgent = uf.cfg.Agents.List[uf.foundIdx]
	if !coreagent.IsSystemAgentID(coreagent.CoreAgentID(uf.foundAgent.ID)) && !uf.foundAgent.IsSystem() {
		return false
	}

	var statePeek struct { // not-wire-format: decode-only local peek at raw body fields to reject a disable attempt, never serialized to any response
		Enabled  *bool `json:"enabled"`
		Disabled *bool `json:"disabled"`
	}
	if peekErr := json.Unmarshal(uf.rawBody, &statePeek); peekErr != nil {
		// Unreachable by construction — the caller has already verified that
		// this exact body is JSON. Logged rather than discarded so a future
		// reordering that makes it reachable cannot silently disarm this guard.
		logsafeWarn("gateway: could not peek enabled/disabled on System Agent update; disable guard not evaluated",
			"agent_id", uf.foundAgent.ID, "error", peekErr)
	}
	if (statePeek.Enabled != nil && !*statePeek.Enabled) ||
		(statePeek.Disabled != nil && *statePeek.Disabled) {
		name := strings.TrimSpace(uf.foundAgent.Name)
		if name == "" {
			name = uf.foundAgent.ID
		}
		jsonErr(uf.w, http.StatusBadRequest,
			fmt.Sprintf("the %s System Agent cannot be disabled", name))
		return true
	}
	return false
}

// validateTarget enforces agent-type, lock, skill, and executor constraints.
func (uf *restAPIUpdateAgentFlow) validateTarget() bool {
	uf.foundAgent = uf.cfg.Agents.List[uf.foundIdx]
	// Worker agents can never be the routing default — they are not chat targets
	// (invoked only via delegation). Reject an attempt to star a worker before
	// any work is done so the single-default invariant and routing stay coherent.
	if uf.ru.req.Default != nil && *uf.ru.req.Default && uf.foundAgent.IsWorker() {
		jsonErr(
			uf.w,
			http.StatusBadRequest,
			"a worker agent cannot be set as the default agent (workers are not chat targets)",
		)
		return true
	}
	// Worker agents have no heartbeat — they execute one delegated task at a
	// Heartbeat is workspace-scoped (ADR-027); per-agent heartbeat writes on the
	// agent PUT path are silently ignored. Workers still cannot carry heartbeat.
	// Per-agent voice is a chat-persona attribute (TTS persona) — workers are
	// not chat personas. Reject setting a non-empty voice on a worker at the
	// write gate so a worker never carries a TTS persona. An explicit null
	// (clearing) is fine, and so is omitting the field. Runs BEFORE the
	// locked-agent identity check so a locked worker is also blocked.
	if uf.foundAgent.IsWorker() && uf.ru.req.Voice != nil && strings.TrimSpace(*uf.ru.req.Voice) != "" {
		jsonErr(uf.w, http.StatusBadRequest, "a worker cannot have a per-agent voice (workers are not chat personas)")
		return true
	}

	// W2 spec §4.19.1 / §9.2: subagent_3p agents (External CLI workers) reject
	// PUTs on any of the 7 forbidden fields. These properties are CLI-owned and
	// cannot be tuned at runtime — they are fixed at the create call when the
	// executor was wired. A silent-drop would be a foot-gun.
	if isExternalSubagent(uf.foundAgent) {
		if field, forbidden := firstForbiddenSubagent3pField(&uf.ru.req); forbidden {
			jsonErr(uf.w, http.StatusBadRequest,
				fmt.Sprintf("subagent_3p agents do not support %s; this is fixed at create time.", field))
			return true
		}
	}

	// W2 spec §4.3 / §9.2: worker types (Subagent, subagent_3p) MUST have a
	// non-empty description (after trim). A blank/whitespace PUT is rejected
	// 400 rather than silently stripping the field — the routing layer
	// depends on the description to pick a worker for delegation.
	if uf.foundAgent.IsWorker() && uf.ru.req.Description != nil && strings.TrimSpace(*uf.ru.req.Description) == "" {
		jsonErr(uf.w, http.StatusBadRequest, "description is required for worker agents (Subagent, subagent_3p)")
		return true
	}

	// W2 spec §3.1 row 16 / §9.2 row 11+15: fallback_models is capped at 2
	// entries (maxItems: 2). Server-enforced on every PUT/CREATE so a direct
	// REST caller (not the SPA) cannot smuggle a 3rd entry past the schema.
	if uf.ru.req.FallbackModels != nil && len(*uf.ru.req.FallbackModels) > 2 {
		jsonErr(uf.w, http.StatusBadRequest, "fallback_models exceeds maxItems: 2")
		return true
	}

	if fieldErr := agentmutation.ValidateFields(uf.foundAgent, suppliedRESTAgentFields(&uf.ru.req)); fieldErr != nil {
		var classified *agentmutation.FieldError
		if errors.As(fieldErr, &classified) && classified.Code == agentmutation.ProtectedField {
			jsonErr(uf.w, http.StatusForbidden, classified.Error())
		} else {
			jsonErr(uf.w, http.StatusBadRequest, fieldErr.Error())
		}
		return true
	}
	// Referential validation: reject unknown skill IDs before doing any work.
	if uf.ru.req.Skills != nil && len(*uf.ru.req.Skills) > 0 {
		if errMsg := uf.ru.a.validateSkillIDs(*uf.ru.req.Skills); errMsg != "" {
			jsonErr(uf.w, http.StatusBadRequest, errMsg)
			return true
		}
	}
	// Validate the executor (kind/cli) before any work so a bad request 400s
	// rather than persisting an invalid combination.

	if uf.ru.req.Executor != nil {
		// CLI-lock rule (spec §4.16 / F-10): once an agent is created with a
		// non-empty executor.cli, the cli is IMMUTABLE. Subsequent PUTs may
		// only mutate cli_path (allows binary upgrades without re-creating the
		// agent), env_overrides, and cli_args. cli_path IS mutable on PUT;
		// env_overrides and cli_args are too. cli itself is fixed at create.
		if uf.foundAgent.Subagents != nil && uf.foundAgent.Subagents.Executor != nil &&
			uf.foundAgent.Subagents.Executor.CLI != "" {
			reqCLI := executorCliStr(uf.ru.req.Executor.Cli)
			if reqCLI != "" && reqCLI != uf.foundAgent.Subagents.Executor.CLI {
				jsonErr(uf.w, http.StatusBadRequest,
					"executor.cli is locked after create; create a new agent to switch CLIs.")
				return true
			}
		}
		// env_overrides OMNIPUS_-prefix guard (spec §4.18 / F-04 STRIDE).
		// A user-submitted env_overrides key starting with OMNIPUS_ would
		// override gateway-managed secrets (master key, audit chain, etc.)
		// for the spawned CLI process — a defense-in-depth gap.
		if uf.ru.req.Executor.EnvOverrides != nil {
			for k := range *uf.ru.req.Executor.EnvOverrides {
				if strings.HasPrefix(strings.ToUpper(k), "OMNIPUS_") {
					jsonErr(uf.w, http.StatusBadRequest,
						fmt.Sprintf("env_overrides key %q is reserved: OMNIPUS_* is gateway-internal", k))
					return true
				}
			}
		}
		execCfg, errMsg := executorConfigFromRequest(
			executorKindStr(uf.ru.req.Executor.Kind),
			executorCliStr(uf.ru.req.Executor.Cli),
		)
		if errMsg != "" {
			jsonErr(uf.w, http.StatusBadRequest, errMsg)
			return true
		}
		// Merge the request into the existing executor so mutable CLI-owned
		// fields (cli_path, env_overrides, cli_args) and any supplied kind/cli
		// survive the update. If there is no existing executor, fall back to
		// the validated config from the request.
		var merged *config.ExecutorConfig
		if uf.foundAgent.Subagents != nil && uf.foundAgent.Subagents.Executor != nil {
			cp := *uf.foundAgent.Subagents.Executor
			merged = &cp
		} else {
			merged = execCfg
		}
		if merged == nil {
			merged = &config.ExecutorConfig{Kind: config.ExecutorKindNative}
		}
		if execCfg != nil {
			if execCfg.Kind != "" {
				merged.Kind = execCfg.Kind
			}
			if execCfg.CLI != "" {
				merged.CLI = execCfg.CLI
			}
		}
		if uf.ru.req.Executor.CliPath != nil {
			merged.CLIPath = *uf.ru.req.Executor.CliPath
		}
		if uf.ru.req.Executor.EnvOverrides != nil {
			merged.EnvOverrides = *uf.ru.req.Executor.EnvOverrides
		}
		if uf.ru.req.Executor.CliArgs != nil {
			merged.CLIArgs = *uf.ru.req.Executor.CliArgs
		}
		// Native-only executor on a non-worker (worker property-model
		// correction): only a sub-agent worker may declare a non-native
		// executor. A base or custom agent updated to kind="external-cli"
		// or "remote-a2a" is rejected at the update gate. A native (or
		// absent) executor on a non-worker stays allowed.
		if !uf.foundAgent.IsWorker() && merged.EffectiveKind() != config.ExecutorKindNative {
			jsonErr(uf.w, http.StatusBadRequest,
				"only sub-agent workers can use an external executor; base agents run native")
			return true
		}
		uf.ru.updatedExecutor = merged
	}
	return false
}

// preparePersistence prepares normalized values and tool-policy coverage validation.
func (uf *restAPIUpdateAgentFlow) preparePersistence() bool {
	uf.ru.newName = uf.foundAgent.Name
	uf.ru.newModel = ""
	if uf.foundAgent.Model != nil {
		uf.ru.newModel = uf.foundAgent.Model.Primary
	}
	if uf.ru.req.Name != nil {
		// Trim before the empty check so a whitespace-only name is rejected
		// rather than silently accepted (UAT fix). Persist the trimmed value.
		trimmedName := strings.TrimSpace(*uf.ru.req.Name)
		if trimmedName == "" {
			jsonErr(uf.w, http.StatusUnprocessableEntity, "name must not be empty")
			return true
		}
		uf.ru.newName = trimmedName
		uf.ru.req.Name = &trimmedName
	}
	if uf.ru.req.Model != nil {
		uf.ru.newModel = *uf.ru.req.Model
	}
	// CLAUDE.md hard constraint 6 / config.ValidateToolPolicyCoverage: when
	// the caller sends tools_cfg, the whole per-tool policies map is a full
	// replacement of what gets persisted (see the tools_cfg branch in the
	// updateConfigJSONLocked closure below) — so re-validate coverage for THIS
	// agent's prospective policies (the request's map when tools_cfg.builtin
	// is sent, else the agent's existing stored policies when tools_cfg is
	// omitted or only touches mcp) against the current global
	// sandbox.tool_policies, before persisting. Reject 400 with the full gap
	// list on any hole rather than silently writing an incomplete map.
	// Validated against a candidate config snapshot (a shallow copy of the
	// live config with this one agent's Tools spliced in) — nothing here
	// mutates the live in-memory config or disk, so a rejected request
	// leaves no partial state behind.
	//
	// The validate step and the persist step below run inside ONE
	// a.configMu-locked critical section (closing a TOCTOU race two
	// concurrent updates could otherwise open — see updateConfigJSONLocked's
	// doc comment), via withToolPolicyCoverageGuard: unlike the fast-path
	// checks above (which read the top-of-function cfg/foundIdx snapshot —
	// fine for those, since none of them persist anything), the guard always
	// fetches the config FRESH, inside a.configMu, right before validating —
	// so a concurrent write (e.g. another request's TriggerReload, or this
	// same agent being deleted) cannot slip in between fetch and lock and go
	// unobserved by the coverage check or the persist step. Returns false
	// once it has already written the HTTP response (error case), so the
	// caller just returns.
	// Coverage validation only runs when this request actually touches
	// tools_cfg: config.ValidateToolPolicyCoverage checks EVERY agent in the
	// roster, not just this one, so running it unconditionally would turn an
	// entirely unrelated field update (default flag, model, skills, …) into
	// a spurious 400 whenever ANY other agent's tools map is incomplete
	// (e.g. a bare/legacy fixture, or a pre-migration config). toolsCoverageMutate
	// stays nil (skipping the check — see withToolPolicyCoverageGuard's doc
	// comment) unless the caller actually sent tools_cfg.

	// defaultAgentIDChanged is set INSIDE the persist closure below, iff this
	// request actually flips cfg.Agents.Defaults.DefaultAgentID (the settings
	// singleton registry.GetDefaultAgent/routing.resolveDefaultAgentID
	// consult). It gates needsReload further down: AgentProfile.tsx's autosave
	// sends `default: <current value>` on EVERY save (not only the deliberate
	// ★ toggle), so gating on mere req.Default != nil would force a full
	// reload — dropping the WebSocket — on every unrelated profile edit. A
	// full reload is genuinely required here (not merely convenient) because
	// both registry.GetDefaultAgent's cached defaultAgentOverride field and
	// AgentRegistry's nested *routing.RouteResolver each capture their own
	// config.Config snapshot at last full-registry-rebuild time — a bare
	// SwapConfig (what every OTHER config-only field on this handler relies
	// on) never reaches either, so without a rebuild the two ladders would
	// keep disagreeing exactly as this bug fix set out to close.

	// modelIdentityChanged is set INSIDE the persist closure below, iff this
	// request actually changed the stored primary model, its provider, or the
	// fallback chain. Compared against the stored record rather than keyed on
	// req.Model/req.Provider/req.FallbackModels being present, for the same
	// reason as defaultAgentIDChanged: AgentProfile.tsx's autosave resends all
	// three on every save, and an unchanged model must not rebuild the agent.

	// CLAUDE.md hard constraint 6 — same caller-side completeness check
	// createAgent performs, for the same reason: a tools_cfg.builtin sent here
	// REPLACES the agent's builtin policy map wholesale, so an incomplete map
	// silently drops the agent's own tightening for every omitted tool and
	// leaves it inheriting the (typically permissive) global ceiling at
	// resolution time. config.ValidateToolPolicyCoverage below cannot catch
	// that: it counts a tool as covered when EITHER side has an entry, and the
	// seeded global map covers the whole catalog. Reproduced live (UAT
	// 2026-09-02, batch 4 S83): a PUT omitting `stop_plan` returned 200.
	if uf.ru.req.ToolsCfg != nil && uf.ru.req.ToolsCfg.Builtin != nil {
		if defects := config.ValidateSubmittedToolPolicyMap(
			uf.ru.req.ToolsCfg.Builtin.Policies, buildKnownBuiltinToolNames(),
		); !defects.Empty() {
			jsonErr(uf.w, http.StatusBadRequest,
				"tools_cfg.builtin.policies "+defects.String())
			return true
		}
	}
	if uf.ru.req.ToolsCfg != nil {
		uf.toolsCoverageMutate = func(c *config.Config) {
			// Search by ID against the FRESHLY-fetched clone — never the
			// pre-lock foundIdx, which can point at the wrong slot (or be out
			// of bounds) if the agent list changed shape between the top-of-
			// function fetch and this locked section (see
			// errAgentVanishedDuringUpdate's doc comment for the sharper,
			// persist-side consequence of the same staleness).
			for i := range c.Agents.List {
				if c.Agents.List[i].ID != uf.ru.id {
					continue
				}
				var candidatePolicies map[string]config.ToolPolicy
				if uf.ru.req.ToolsCfg.Builtin != nil {
					candidatePolicies = agentToolPolicyMapFromWire(uf.ru.req.ToolsCfg.Builtin.Policies)
				} else if c.Agents.List[i].Tools != nil {
					candidatePolicies = c.Agents.List[i].Tools.Builtin.Policies
				}
				candidateAgent := c.Agents.List[i]
				candidateAgent.Tools = &config.AgentToolsCfg{
					Builtin: config.AgentBuiltinToolsCfg{Policies: candidatePolicies},
				}
				c.Agents.List[i] = candidateAgent
				break
			}
		}
	}
	return false
}

// persistAndReload persists the update, writes agent files, and rebuilds cached runtime state when needed.
func (uf *restAPIUpdateAgentFlow) persistAndReload() bool {
	if ok := uf.ru.a.withToolPolicyCoverageGuard(
		uf.w,
		uf.toolsCoverageMutate,
		func(gaps []config.CoverageGap) string {
			return fmt.Sprintf(
				"tool policy coverage incomplete for agent %q (%d gap(s)): %s",
				uf.ru.id, len(gaps), joinCoverageGapMessages(gaps),
			)
		},
		// ADR-054 D2/§11 checklist item 3: agents are per-entity records under
		// entities/agents/<id>.json, not config.json's agents.list — persist
		// via the agent store instead of splicing the raw config map. `m` is
		// deliberately left untouched. entity.Store.Update (via
		// agentstore.Store.Update) performs the read-modify-write under its
		// own striped-mutex + sidecar-flock (ADR-054 D3), nested inside this
		// closure's a.configMu hold — same lock-ordering rule as the
		// tool-policy-coverage validation above (workspace/agent locks are
		// never held across this call).
		func(m map[string]any) error {
			return uf.ru.persistAgent(m)
		},
		"rest: save agent entity record for agent update",
	); !ok {
		return true
	}
	// Write SOUL.md, HEARTBEAT.md, and AGENT.md BEFORE triggering reload,
	// so the new AgentInstance reads the updated files.
	// Capture agentWorkspace into a local to avoid TOCTOU on cfg.Agents.List (M1).
	capturedWorkspace := uf.cfg.Agents.List[uf.foundIdx].Home
	var wsErr error
	uf.workspace, wsErr = agentWorkspacePath(uf.cfg, uf.ru.id, capturedWorkspace, uf.ru.a.homePath)
	if wsErr != nil {
		logsafeError("rest: agentWorkspacePath for update", "agent_id", uf.ru.id, "error", wsErr)
		jsonErr(uf.w, http.StatusInternalServerError, fmt.Sprintf("could not resolve workspace: %v", wsErr))
		return true
	}
	// Rebuild the running agent only when a changed field is one the
	// AgentInstance caches at construction (soul, skills, model params, context
	// window, the model/provider/fallbacks, or a default-agent flip). Fields the
	// turn path reads from config on every call need no rebuild. The rebuild is
	// fastAgentUpsert's single-agent swap, not a full reload, so the WebSocket
	// and mid-conversation context survive.
	//
	// ADR-037: delegation_policy is retired, so it no longer appears in this
	// condition. Delegation edits now go exclusively through the per-workspace
	// delegation graph (PUT /api/v1/workspaces/{id}/delegation), which
	// buildDelegationDenyChecker reads fresh from disk on every delegation
	// call (workspace.ReadDelegation) rather than from a closure baked at
	// agent-instance construction time — so a graph edit already takes effect
	// on the NEXT turn with no reload required at all, unlike the old
	// per-agent policy this replaced.
	//
	// defaultAgentIDChanged (set inside the persist closure above, iff this
	// request actually flipped agents.defaults.default_agent_id) also forces
	// a reload — see that variable's doc comment. This is deliberately NOT
	// keyed off req.Default != nil (which AgentProfile.tsx's autosave sends
	// on every save, unrelated edits included); only a real transition of the
	// singleton earns the WebSocket-dropping cost of a full rebuild, because
	// nothing short of one re-syncs registry.GetDefaultAgent's cached
	// override AND the registry's nested RouteResolver's own config
	// snapshot — the two ladders this bug fix makes agree.
	//
	// fastAgentUpsert (issue #571), not a full config reload: a default-agent
	// flip is only real once the registry's cached override AND its nested
	// RouteResolver snapshot are rebuilt (ADR-037 — a control that looks like
	// it worked and changed no routing is the anti-pattern this project
	// bans), and a soul change is only real once a fresh AgentInstance/
	// ContextBuilder picks up the new SOUL.md — but neither requires
	// restarting channels/cron/schedulers/the plan engine or rebuilding
	// every OTHER agent's instance, only this one agent's. See
	// AgentLoop.UpsertAgentFast for how the resolver/override/wiring parity
	// is achieved without that cost; it falls back to the slow, hardened
	// full reload on any wiring error.
	// ADR-066 D2/D9: "every write triggers a registry reload so the next turn
	// uses the new window" — AgentInstance resolves and CACHES its window at
	// construction (instance.go), so a bare config swap would leave the
	// running instance on the old window until a restart.
	//
	// req.Skills != nil (ADR-072 Finding A): ContextBuilder.skillAllowed
	// (pkg/agent/context.go) reads from a skillAllowlist snapshot installed
	// ONCE at agent-instance construction (instance.go's
	// contextBuilder.WithSkillAllowlist(agentCfg.Skills)). Like Soul, a
	// Skills-only edit is persisted to config above but never reaches the
	// running instance without a rebuild — grantPredicateFor (skill.go)
	// re-reads config live per-call so list_skills reflects the change
	// immediately, but the Skill tool and the /<skill> path (both gated via
	// skillAllowed) would keep serving the stale allowlist durably until some
	// unrelated field forced a reload. Folding Skills into needsReload keeps
	// both paths in sync via the same fastAgentUpsert rebuild Soul already
	// uses.
	contextWindowOverrideChanged := uf.ru.req.ContextWindowOverride != nil || uf.ru.clearsContextWindowOverride
	// req.ModelParams != nil (Q1 fix): AgentInstance.MaxTokens/Temperature
	// are resolved and CACHED once at construction (pkg/agent/instance.go),
	// same as the context-window and skill-allowlist cases documented
	// above — a bare config swap would leave the running instance serving
	// the old sampling params until a restart. fastAgentUpsert rebuilds
	// just this one AgentInstance via the same NewAgentInstance constructor
	// that now reads agentCfg.ModelParams, so folding this into needsReload
	// is sufficient; no separate live-apply path (like ApplyAgentModel) is
	// needed.
	//
	// modelIdentityChanged (UAT E-7): a change to the primary model, its
	// provider, or the fallback chain is applied the same way — by rebuilding
	// this one AgentInstance from the just-saved record with NewAgentInstance,
	// the constructor boot uses. It used to be applied in place through
	// AgentLoop.ApplyAgentModel, which ran only when `model` itself was sent (a
	// provider-only or fallback-only change never reached the running agent),
	// resolved candidates without the agent's pinned provider or its saved
	// fallbacks, and on any resolution failure left the running agent on the
	// previous model while this handler answered 200 with a warning: the
	// saved-but-changed-nothing pattern ADR-037 bans. A model or provider the
	// install cannot serve is still saved AND applied: the rebuilt agent refuses
	// turns with a typed error, and this response carries needs_model /
	// degraded_reason (ADR-068 FR-014, ADR-067 US-6), so the saved config and
	// the running agent always describe the same model. fastAgentUpsert swaps
	// only this agent, never a full reload, so the WebSocket survives (#73).
	needsReload := uf.ru.req.Soul != nil || uf.ru.defaultAgentIDChanged || contextWindowOverrideChanged ||
		uf.ru.req.Skills != nil || uf.ru.req.ModelParams != nil || uf.ru.modelIdentityChanged
	if needsReload {
		// fastAgentUpsert returns a non-empty message only when neither the
		// single-agent swap nor its full-reload fallback could publish the
		// rebuilt agent. The change is saved but the running agent still serves
		// the old settings, so this is not a success: fail the request, as
		// updateConfigJSONLocked does when config is written but the in-memory
		// refresh fails.
		if rebuildErr := uf.ru.a.fastAgentUpsert(uf.ru.id); rebuildErr != "" {
			logsafeError("updateAgent: change saved but the running agent could not be rebuilt",
				"agent_id", uf.ru.id, "error", rebuildErr)
			uf.ru.activationFailed = rebuildErr
		}
	}
	return false
}

// respond builds the response from the newly persisted live agent state.
func (uf *restAPIUpdateAgentFlow) respond() {
	// Re-read the files so the response reflects what was just persisted.
	soul, _ := readAgentFiles(uf.workspace)
	// Build the response from defaults, then override with request values.
	agentID := uf.cfg.Agents.List[uf.foundIdx].ID
	model := uf.cfg.Agents.Defaults.DefaultModel.Model
	if uf.ru.newModel != "" {
		model = uf.ru.newModel
	}
	activeIDs := uf.ru.a.activeAgentIDSet()
	ag := buildAgentDefaults(uf.cfg)
	ag.Id = agentID
	ag.Name = uf.ru.newName
	// Description: use the just-updated value when provided, else fall back
	// to what's on disk (which will be the previously-persisted value because
	// TriggerReload has refreshed cfg.Agents.List).
	if uf.ru.req.Description != nil {
		desc := strings.TrimSpace(*uf.ru.req.Description)
		if desc != "" {
			ag.Description = &desc
		}
	} else {
		// Re-read from the current config after reload.
		if cur := uf.ru.a.agentLoop.GetConfig(); cur != nil {
			for _, ac := range cur.Agents.List {
				if ac.ID == agentID && ac.Description != "" {
					ag.Description = &ac.Description
					break
				}
			}
		}
	}
	ag.Locked = uf.foundAgent.Locked
	ag.Model = &model
	// O3 two-field model: echo the explicit primary provider. When the request
	// touched provider, the request value is authoritative (a non-empty string
	// sets it; an empty string clears it → absent on the wire). Otherwise reflect
	// the persisted provider from the reloaded config.
	switch {
	case uf.ru.req.Provider != nil:
		if p := strings.TrimSpace(*uf.ru.req.Provider); p != "" {
			ag.Provider = &p
		}
	default:
		if cur := uf.ru.a.agentLoop.GetConfig(); cur != nil {
			for i := range cur.Agents.List {
				if cur.Agents.List[i].ID == agentID {
					setAgentModelProvider(&ag, cur.Agents.List[i].Model)
					break
				}
			}
		}
	}
	ag.Status = gen.AgentStatus(computeAgentStatus(agentID, activeIDs, soul, uf.foundAgent.Locked))
	// Hide compiled prompts for locked (core) agents.
	// ADR-052 FR-038: System Agents (the Judge) are exempted — the PUT response
	// must echo back what was just persisted to SOUL.md, or a client's next
	// edit (built on a blank round-trip) would clobber the just-saved content.
	if uf.foundAgent.Locked && !uf.foundAgent.IsSystem() {
		soul = ""
	}
	ag.Soul = soul
	// Populate Default, Skills, and Executor from the live config after the write
	// (handles both the req.Default=true case and the leave-unchanged case, and
	// ensures a GET→edit→PUT round-trip echoes the persisted executor).
	if liveCfg := uf.ru.a.agentLoop.GetConfig(); liveCfg != nil {
		for _, ac := range liveCfg.Agents.List {
			if ac.ID == agentID {
				ag.Type = coreagent.ToWireType(ac)
				// Derived from the settings singleton — see listAgents' comment
				// for the full rationale. liveCfg is fetched fresh above, and
				// (when defaultAgentIDChanged fired) TriggerReload has already
				// rebuilt it from the just-written config.json, so this reflects
				// the singleton this exact request just persisted.
				ag.Default = boolPtr(ac.ID == liveCfg.Agents.Defaults.DefaultAgentID)
				// ADR-068 FR-014 (T068-08): needs_model is derived, never stored.
				ag.NeedsModel = agentNeedsModel(liveCfg, &ac)
				// ADR-067 FR-016/FR-031 (T067-09): the PUT response carries
				// the degrade the request just cleared (or created) — a
				// repair is visible in the very response that made it, with
				// no restart beyond the reload this handler already triggers
				// (US-6.AC3).
				ag.DegradedReason = agentDegradedReason(uf.ru.a.providerCatalog, liveCfg, &ac)
				if len(ac.Skills) > 0 {
					skills := make([]string, len(ac.Skills))
					copy(skills, ac.Skills)
					ag.Skills = &skills
				}
				setAgentExecutorResponse(&ag, ac.Subagents)
				if ac.UpdatedAt != nil {
					ag.UpdatedAt = ac.UpdatedAt
				}
				// P-F2: echo shell_policy/fallback_models on the PUT response too —
				// this loop previously never called applyAgentOverrides at all, so
				// updateAgent's own response (unlike list/get) never reflected either
				// field even though both persist correctly above. Runs before the
				// request-value overrides below so an explicit req.MaxToolIterations
				// (also touched by applyAgentOverrides) still wins.
				applyAgentOverrides(&ag, &ac)
				// ADR-066 D2/D9: echo the just-persisted rung-1 override and
				// re-derive the effective window from liveCfg, so the form
				// round-trips instead of coming back blank.
				applyAgentContextWindow(&ag, liveCfg, &ac)
				break
			}
		}
	}
	// Override defaults with request values when provided.
	if uf.ru.req.MaxToolIterations != nil {
		ag.MaxToolIterations = *uf.ru.req.MaxToolIterations
	}
	ag.Revision = uf.ru.mutationResult.Revision
	applyAgentEditableFields(&ag, uf.foundAgent)
	persistence := gen.AgentPersistenceStatusComplete
	ag.PersistenceStatus = &persistence
	activation := gen.AgentActivationStatusActive
	if uf.ru.activationFailed != "" {
		activation = gen.AgentActivationStatusFailed
		message := fmt.Sprintf("saved but activation failed: %s", uf.ru.activationFailed)
		ag.Message = &message
	}
	ag.ActivationStatus = &activation
	changed := suppliedRESTAgentFields(&uf.ru.req)
	ag.ChangedFields = &changed
	jsonOK(uf.w, ag)
}

// restAPIUpdateAgentPersistAgent carries the shared state of persistAgent across its stages.
type restAPIUpdateAgentPersistAgent struct {
	ru          *restAPIUpdateAgent
	conflictErr error
}

// persistAgent persists the requested agent changes while the config guard holds its lock.
func (ru *restAPIUpdateAgent) persistAgent(m map[string]any) error {
	rp := &restAPIUpdateAgentPersistAgent{ru: ru}

	store := agentstore.New(rp.ru.a.homePath)

	result, updateErr := store.MutateState(rp.ru.id, rp.ru.req.Revision, func(agentRec *config.AgentConfig) error {
		return rp.updateRecord(agentRec)
	}, rp.ru.req.Soul)
	rp.ru.mutationResult = result
	if updateErr != nil {
		if rp.conflictErr != nil {
			return errConflict
		}
		if errors.Is(updateErr, entity.ErrNotFound) {
			// The fast-path existence check at the top of updateAgent found
			// this agent, but by the time this locked store update ran it
			// was gone — e.g. a concurrent DELETE /agents/{id} raced this
			// PUT. Mapped to 404 by withToolPolicyCoverageGuard (see
			// errAgentVanishedDuringUpdate's doc comment).
			return fmt.Errorf("%w: agent %q not found", errAgentVanishedDuringUpdate, rp.ru.id)
		}
		if errors.Is(updateErr, agentstore.ErrRevisionConflict) {
			return errConflict
		}
		if errors.Is(updateErr, agentstore.ErrInvalidRevision) {
			return fmt.Errorf("invalid revision: %w", updateErr)
		}
		return &configurationMutationError{Result: result, Err: fmt.Errorf("update agent entity record: %w", updateErr)}
	}
	// Single-default invariant, for real this time: the settings
	// singleton (agents.defaults.default_agent_id) is the ONLY thing
	// registry.GetDefaultAgent and routing.resolveDefaultAgentID
	// consult, and the ONLY thing the wire `default` field is derived
	// from (listAgents/getAgent above, this handler's response
	// below) — so it is also the only thing that needs writing here.
	// There is no more N-write fan-out across every OTHER agent's
	// entity record: a per-entity bool never had to be reconciled
	// once "the one default" became a single string behind the
	// existing config-write lock (this closure already holds
	// a.configMu via withToolPolicyCoverageGuard), and that old loop
	// was itself racy (each Update below was its own
	// independently-locked write, so two concurrent PUTs to two
	// different agents could each "win" with no shared lock to
	// serialize them — precisely the failure mode ADR-054 D6.4
	// retired RepairMultipleDefaults over).
	//
	// true  → point the singleton at this agent (worker guard
	//         already rejected this request above if foundAgent is a
	//         worker, so `id` is always a valid chat-target here).
	// false → clear the singleton ONLY if it currently names this
	//         agent, so un-starring the actual default reverts to
	//         the registry's own fallback ladder (main sentinel,
	//         then first non-worker) instead of leaving the
	//         singleton pointed at an agent that just un-defaulted
	//         itself. Un-starring an agent that the singleton
	//         doesn't currently name is a no-op — matches the old
	//         per-entity semantics ("clear this agent only, leave
	//         others unchanged").
	if rp.ru.req.Default != nil {
		defaultsMap := ensureMap(m, "agents", "defaults")
		cur, _ := defaultsMap["default_agent_id"].(string)
		if *rp.ru.req.Default {
			if cur != rp.ru.id {
				defaultsMap["default_agent_id"] = rp.ru.id
				rp.ru.defaultAgentIDChanged = true
			}
		} else if cur == rp.ru.id {
			defaultsMap["default_agent_id"] = ""
			rp.ru.defaultAgentIDChanged = true
		}
	}
	// O6: heartbeat is now fully per-agent (written inside the agent-found
	// block above). The legacy global heartbeat block was removed; there is
	// no cfg.Heartbeat mirror to maintain.
	return nil
}

// updateRecord applies the validated request fields to the locked agent record.
func (rp *restAPIUpdateAgentPersistAgent) updateRecord(agentRec *config.AgentConfig) error {
	// MutateState checks the reviewed revision under the entity lock before
	// applying these fields. UpdatedAt is display metadata, never a precondition.
	storedModelBefore, storedFallbacksBefore := agentModelIdentity(agentRec)
	rp.updateIdentityAndModel(agentRec)
	rp.updatePresentationAndFallbacks(agentRec)
	rp.updateToolsConfig(agentRec)
	if err := rp.updateMCPServers(agentRec); err != nil {
		return err
	}
	if err := rp.updateToolPolicies(agentRec); err != nil {
		return err
	}
	rp.updateExecutorAndSkills(agentRec)

	// Refresh the display timestamp with sub-second precision;
	// the frontend compares it as an ordinal when incorporating autosaves.
	storedModelAfter, storedFallbacksAfter := agentModelIdentity(agentRec)
	rp.ru.modelIdentityChanged = !sameAgentModelIdentity(
		storedModelBefore, storedFallbacksBefore, storedModelAfter, storedFallbacksAfter)
	agentRec.UpdatedAt = &rp.ru.now
	return nil
}

func (rp *restAPIUpdateAgentPersistAgent) updateIdentityAndModel(agentRec *config.AgentConfig) {
	if rp.ru.req.Name != nil {
		agentRec.Name = rp.ru.newName
	}
	if rp.ru.req.Description != nil {
		agentRec.Description = strings.TrimSpace(*rp.ru.req.Description)
	}
	if rp.ru.req.Model != nil {
		if agentRec.Model == nil {
			agentRec.Model = &config.AgentModelConfig{}
		}
		agentRec.Model.Primary = rp.ru.newModel
	}
	// O3 two-field model: persist (or clear) the explicit primary
	// provider. A non-empty value pins the provider; an explicit empty
	// string clears it (fall back to default-provider resolution).
	if rp.ru.req.Provider != nil {
		if agentRec.Model == nil {
			agentRec.Model = &config.AgentModelConfig{}
		}
		agentRec.Model.Provider = strings.TrimSpace(*rp.ru.req.Provider)
	}
	// NOTE (discovered during ADR-054 conversion, pre-existing gap,
	// out of scope here): req.TimeoutSeconds and req.RateLimits
	// have NO corresponding config.AgentConfig field —
	// config.AgentConfig has no TimeoutSeconds/RateLimits at all
	// (only agents.defaults.timeout_seconds, a global setting).
	// The pre-conversion code wrote them to raw map keys with no
	// Go struct field to read them back into, so they never
	// survived a struct-based config reload even before this
	// change — this conversion does not persist them either,
	// matching (not worsening) that pre-existing behavior.
	//
	// req.ModelParams (Q1 fix, 2026-09-14; extracted to the shared
	// mergeAgentModelParams helper for the T1 fix so createAgent
	// reuses the identical merge instead of drifting from it):
	// this USED to be in the same "no field to persist into"
	// bucket as the two fields above — model_params decoded fine
	// but AgentConfig had nowhere to write it, so the PUT
	// returned 200 and changed nothing on disk, and GET always
	// echoed model_params: null (the ADR-037 anti-pattern).
	// config.AgentModelParams now exists for exactly this, and
	// pkg/agent/instance.go reads it at AgentInstance
	// construction time so the override reaches the next turn's
	// provider call. Field-level merge (mirrors ShellPolicy
	// below): only the sub-fields the caller actually sent
	// overwrite the persisted value; an omitted sub-field leaves
	// it untouched, so a partial patch (e.g. only max_tokens)
	// does not clobber an existing temperature. top_p is
	// rejected 400 earlier in this handler (no provider adapter
	// implements it) and never reaches here.
	if rp.ru.req.ModelParams != nil {
		agentRec.ModelParams = mergeAgentModelParams(agentRec.ModelParams, agentModelParamsFromWire(rp.ru.req.ModelParams))
	}
	if rp.ru.req.MaxToolIterations != nil {
		agentRec.MaxToolIterations = *rp.ru.req.MaxToolIterations
	}
	// ADR-066 D2 rung 1: the per-agent context-window override.
	// Nil-and-not-null leaves the persisted value untouched; an
	// explicit null clears it. Without this the PUT returned 200
	// and changed nothing at all — the ADR-037 anti-pattern.
	if rp.ru.req.ContextWindowOverride != nil {
		v := *rp.ru.req.ContextWindowOverride
		agentRec.ContextWindowOverride = &v
	} else if rp.ru.clearsContextWindowOverride {
		agentRec.ContextWindowOverride = nil
	}
}

func (rp *restAPIUpdateAgentPersistAgent) updatePresentationAndFallbacks(agentRec *config.AgentConfig) {
	// tool_feedback was removed from the wire in W1 (it's now per-channel
	// runtime behavior driven by pkg/agent/loop.go: webchat skips). The
	// global config-level agents.defaults.tool_feedback stays.
	if rp.ru.req.ShellPolicy != nil {
		// Load the existing shell_policy (if any) so a partial PATCH
		// (e.g. only custom_deny_patterns) does not clobber fields the
		// caller did not send.
		existing := agentRec.ShellPolicy
		if existing == nil {
			existing = &config.AgentShellPolicy{}
		}
		// Only overwrite enable_deny_patterns when the caller explicitly
		// sent it (non-nil pointer).
		if rp.ru.req.ShellPolicy.EnableDenyPatterns != nil {
			existing.EnableDenyPatterns = *rp.ru.req.ShellPolicy.EnableDenyPatterns
		}
		// An explicitly-sent array overwrites, INCLUDING the empty array —
		// that is how the SPA clears all deny patterns. Only a nil (field
		// absent from the request) leaves the persisted list untouched.
		if rp.ru.req.ShellPolicy.CustomDenyPatterns != nil {
			existing.CustomDenyPatterns = *rp.ru.req.ShellPolicy.CustomDenyPatterns
		}
		agentRec.ShellPolicy = existing
	}
	if rp.ru.req.Color != nil {
		agentRec.Color = *rp.ru.req.Color
	}
	if rp.ru.req.Icon != nil {
		agentRec.Icon = *rp.ru.req.Icon
	}
	// voice: ADR-090 FR-002 supported editable persona field. Persisted
	// even though TTS playback is inactive. Worker non-empty values are
	// rejected before this persist step. Empty string clears.
	if rp.ru.req.Voice != nil {
		agentRec.Voice = strings.TrimSpace(*rp.ru.req.Voice)
	}
	// memory_enabled (ADR-052 FR-039): "Allowed on all agents" per
	// AgentUpdateRequest.yaml — including locked/system agents (the
	// Judge), which is why this is not gated behind the
	// foundAgent.Locked identity-mutation check above (that block
	// only forbids name/description/soul/color/icon/skills).
	if rp.ru.req.MemoryEnabled != nil {
		agentRec.MemoryEnabled = rp.ru.req.MemoryEnabled
	}
	if rp.ru.req.FallbackModels != nil {
		fbs := make(config.FallbackModelSlice, 0, len(*rp.ru.req.FallbackModels))
		for _, fm := range *rp.ru.req.FallbackModels {
			provider := ""
			if fm.Provider != nil {
				provider = *fm.Provider
			}
			fbs = append(fbs, config.FallbackModel{Model: fm.Model, Provider: provider})
		}
		agentRec.FallbackModels = fbs
	}
	// Default flag: ADR-054 D6.4 moved default-agent RESOLUTION
	// entirely to the settings singleton
	// (cfg.Agents.Defaults.DefaultAgentID — see registry.go's
	// GetDefaultAgent and route.go's resolveDefaultAgentID). This
	// per-entity bool is retained only for backward display
	// compatibility (see config.go's ADR-054 D6.4 note) — it is
	// NOT read by any resolution logic, and the wire `default`
	// field is derived from the singleton at every response site
	// (listAgents/getAgent above, updateAgent's response below),
	// never from this field. The actual singleton write happens
	// further down in THIS SAME a.configMu-locked closure (see the
	// "agents.defaults.default_agent_id" write after the entity
	// write below succeeds), so both land atomically or not at
	// all — that replaces the old racy N-write fan-out that used
	// to clear Default on every OTHER agent's entity record (see
	// git history: independently-locked per-entity writes with no
	// shared lock could each set Default=true, which is exactly
	// the composition ADR-054 D6.4 retired RepairMultipleDefaults
	// over).
	if rp.ru.req.Default != nil {
		agentRec.Default = *rp.ru.req.Default
	}
}

func (rp *restAPIUpdateAgentPersistAgent) updateToolsConfig(agentRec *config.AgentConfig) {
	if rp.ru.req.ToolsCfg != nil {
		newTools := &config.AgentToolsCfg{}
		if rp.ru.req.ToolsCfg.Builtin != nil {
			newTools.Builtin = config.AgentBuiltinToolsCfg{
				Policies: agentToolPolicyMapFromWire(rp.ru.req.ToolsCfg.Builtin.Policies),
			}
		} else if agentRec.Tools != nil {
			// req.ToolsCfg is present (e.g. it only touches mcp) but
			// omitted builtin — the coverage-validation block above
			// assumed the agent's EXISTING persisted builtin.policies
			// survives untouched in this case.
			newTools.Builtin = agentRec.Tools.Builtin
		}
		if rp.ru.req.ToolsCfg.Mcp != nil && rp.ru.req.ToolsCfg.Mcp.Servers != nil {
			servers := make([]config.AgentMCPServerBinding, 0, len(*rp.ru.req.ToolsCfg.Mcp.Servers))
			for _, s := range *rp.ru.req.ToolsCfg.Mcp.Servers {
				binding := config.AgentMCPServerBinding{ID: s.Id}
				if s.Tools != nil {
					binding.Tools = *s.Tools
				}
				servers = append(servers, binding)
			}
			newTools.MCP = config.AgentMCPToolsCfg{Servers: servers}
		} else if agentRec.Tools != nil {
			// Symmetric preservation for mcp: req.ToolsCfg present but
			// omitted mcp (e.g. a builtin-only policy update) must not
			// silently drop the agent's existing MCP server bindings.
			newTools.MCP = agentRec.Tools.MCP
		}
		agentRec.Tools = newTools
	}
}

func (rp *restAPIUpdateAgentPersistAgent) updateMCPServers(agentRec *config.AgentConfig) error {
	if rp.ru.req.McpServers == nil {
		return nil
	}
	if agentRec.Tools == nil {
		agentRec.Tools = &config.AgentToolsCfg{}
	}
	servers := make([]config.AgentMCPServerBinding, 0, len(*rp.ru.req.McpServers))
	for _, s := range *rp.ru.req.McpServers {
		binding := config.AgentMCPServerBinding{ID: s.Id, ToolsSpecified: s.Tools != nil}
		if s.Tools != nil {
			binding.Tools = *s.Tools
		}
		if err := config.ValidateAgentMCPServerBinding(binding); err != nil {
			return err
		}
		if _, ok := rp.ru.a.agentLoop.GetConfig().Tools.MCP.Servers[s.Id]; !ok {
			return fmt.Errorf("MCP server %q is not configured", s.Id)
		}
		servers = append(servers, binding)
	}
	agentRec.Tools.MCP.Servers = servers
	return nil
}

func (rp *restAPIUpdateAgentPersistAgent) updateToolPolicies(agentRec *config.AgentConfig) error {
	if rp.ru.req.ToolPolicyChanges == nil {
		return nil
	}
	if agentRec.Tools == nil {
		agentRec.Tools = &config.AgentToolsCfg{}
	}
	patch := agentmutation.ToolPolicyChanges{}
	if rp.ru.req.ToolPolicyChanges.Set != nil {
		patch.Set = make(map[string]config.ToolPolicy, len(*rp.ru.req.ToolPolicyChanges.Set))
		for name, policy := range *rp.ru.req.ToolPolicyChanges.Set {
			patch.Set[name] = config.ToolPolicy(policy)
		}
	}
	if rp.ru.req.ToolPolicyChanges.Remove != nil {
		patch.Remove = *rp.ru.req.ToolPolicyChanges.Remove
	}
	known := make(map[string]struct{})
	for name := range buildKnownBuiltinToolNames() {
		known[name] = struct{}{}
	}
	next, err := agentmutation.ApplyToolPolicyChanges(agentRec.Tools.Builtin.Policies, patch, known)
	if err != nil {
		return err
	}
	agentRec.Tools.Builtin.Policies = next
	return nil
}

func (rp *restAPIUpdateAgentPersistAgent) updateExecutorAndSkills(agentRec *config.AgentConfig) {
	// Executor: write the sub-agent executor under Subagents.Executor
	// when the caller sends it. kind="native" with no cli clears any
	// prior external-cli config (updatedExecutor == nil → clear).
	if rp.ru.req.Executor != nil {
		if rp.ru.updatedExecutor == nil {
			if agentRec.Subagents != nil {
				agentRec.Subagents.Executor = nil
			}
		} else {
			if agentRec.Subagents == nil {
				agentRec.Subagents = &config.SubagentsConfig{}
			}
			agentRec.Subagents.Executor = rp.ru.updatedExecutor
		}
	}
	// Skills: replace the agent's skill list when the caller sends the field.
	// An explicit empty array removes all skills. Nil (absent) leaves unchanged.
	if rp.ru.req.Skills != nil {
		if len(*rp.ru.req.Skills) > 0 {
			agentRec.Skills = *rp.ru.req.Skills
		} else {
			agentRec.Skills = nil
		}
	}
}
