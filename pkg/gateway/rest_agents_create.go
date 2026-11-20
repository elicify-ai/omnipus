// rest_agents_create.go: Create an agent, and decode the create request's wire variants

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// decodeAgentCreateVariant strictly decodes raw into out (a pointer to one of
// the three generated AgentCreateRequest{Main,Subagent,Subagent3p} structs)
// using a json.Decoder with DisallowUnknownFields, so a field that variant
// does not carry — at any nesting depth (e.g. a "system.*" key inside a
// tools_cfg.builtin.policies map is fine; an unrelated top-level or nested
// property is not) — is rejected with 400 rather than silently dropped. This
// runs UNCONDITIONALLY (independent of cfg.Gateway.ValidateInbound, which
// defaults to false) — it is the mechanism that makes "a field sent on the
// wrong variant is a schema violation, never silently persisted" actually
// true regardless of config.
//
// On success it returns true and leaves w untouched. On failure it writes the
// 400 response itself and returns false so the caller can `return` directly.
func decodeAgentCreateVariant(w http.ResponseWriter, raw []byte, wireType, variantName string, out any) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf(
				"field not allowed on agent type %q: %v — see the %s schema", wireType, err, variantName,
			))
		} else {
			jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		}
		return false
	}
	return true
}

// wireStringMap converts a generated map whose values are a string-based enum
// (e.g. map[string]AgentCreateRequestMainToolsCfgBuiltinPolicies) into a plain
// map[string]string, preserving nil-vs-non-nil: a nil input returns nil
// (field absent); a non-nil (possibly empty) input returns a newly allocated
// map with every value stringified. Builtin.Policies is a required,
// non-pointer map on the wire (there is no default_policy fallback any more —
// CLAUDE.md hard constraint 6), so this takes the map directly rather than a
// pointer-to-map.
func wireStringMap[V ~string](in map[string]V) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = string(v)
	}
	return out
}

// agentCreateMCPServerInput is one entry of agentCreateToolsCfgInput.MCPServers.
type agentCreateMCPServerInput struct {
	ID             string
	Tools          []string
	ToolsSpecified bool
}

// agentCreateToolsCfgInput is a request-shape-agnostic normalization of the
// wire ToolsCfg object. gen.AgentCreateRequestMain and gen.AgentCreateRequestSubagent
// each carry an anonymous ToolsCfg struct (oapi-codegen inlines it per-variant,
// distinct named enum types per variant) — createAgent normalizes whichever
// variant was sent into this shape so the ac.Tools construction below is
// written once. gen.AgentCreateRequestSubagent3p has no tools_cfg property at
// all (the external runner manages its own tools), so this stays nil for that
// variant.
type agentCreateToolsCfgInput struct {
	BuiltinPolicies map[string]string
	MCPServers      []agentCreateMCPServerInput
	MCPSpecified    bool
}

// agentCreateToolsCfgFromWire converts either variant's tools_cfg wire object
// into the common agentCreateToolsCfgInput, or nil when tc is nil. Generic
// over P — the per-variant enum type oapi-codegen emits for Builtin.Policies'
// map values (AgentCreateRequestMainToolsCfgBuiltinPolicies vs
// AgentCreateRequestSubagentToolsCfgBuiltinPolicies, both underlying type
// string) — since that's the only reason ToolsCfg isn't structurally
// identical across variants; every other field
// (including the Mcp.Servers element shape, which carries no enum) is
// identical, so the whole tools_cfg object is accepted directly (Go's
// generic type inference resolves P from tc's concrete argument type) rather
// than pre-extracting each sub-field per call site.
//
// There is no default_policy field on the wire (CLAUDE.md hard constraint
// 6): Builtin.Policies is a required, fully-enumerated per-tool map.
// createAgent enforces completeness via config.ValidateToolPolicyCoverage
// against a candidate config snapshot before persisting — this helper only
// normalizes the wire shape.
func agentCreateToolsCfgFromWire[P ~string](tc *struct {
	Builtin *struct {
		Policies map[string]P `json:"policies"`
	} `json:"builtin,omitempty"`
	Mcp *struct {
		Servers *[]gen.AgentToolsMcpServerBinding `json:"servers,omitempty"`
	} `json:"mcp,omitempty"`
},
) *agentCreateToolsCfgInput {
	if tc == nil {
		return nil
	}
	out := &agentCreateToolsCfgInput{}
	if tc.Builtin != nil {
		out.BuiltinPolicies = wireStringMap(tc.Builtin.Policies)
	}
	if tc.Mcp != nil && tc.Mcp.Servers != nil {
		out.MCPSpecified = true
		for _, s := range *tc.Mcp.Servers {
			var t []string
			if s.Tools != nil {
				t = *s.Tools
			}
			out.MCPServers = append(out.MCPServers, agentCreateMCPServerInput{ID: s.Id, Tools: t, ToolsSpecified: s.Tools != nil})
		}
	}
	return out
}

func agentCreateMCPServersFromWire(servers *[]struct {
	Id    string    `json:"id"`
	Tools *[]string `json:"tools,omitempty"`
}) *[]agentCreateMCPServerInput {
	if servers == nil {
		return nil
	}
	out := make([]agentCreateMCPServerInput, 0, len(*servers))
	for _, server := range *servers {
		var names []string
		if server.Tools != nil {
			names = make([]string, len(*server.Tools))
			copy(names, *server.Tools)
		}
		out = append(out, agentCreateMCPServerInput{ID: server.Id, Tools: names, ToolsSpecified: server.Tools != nil})
	}
	return &out
}

func agentCreatePolicyChangesFromWire[P ~string](changes *struct {
	Remove *[]string     `json:"remove,omitempty"`
	Set    *map[string]P `json:"set,omitempty"`
}) *agentmutation.ToolPolicyChanges {
	if changes == nil {
		return nil
	}
	out := &agentmutation.ToolPolicyChanges{}
	if changes.Remove != nil {
		out.Remove = append(out.Remove, (*changes.Remove)...)
	}
	if changes.Set != nil {
		out.Set = make(map[string]config.ToolPolicy, len(*changes.Set))
		for name, policy := range *changes.Set {
			out.Set[name] = config.ToolPolicy(policy)
		}
	}
	return out
}

// restAPICreateAgent carries the shared state of createAgent across its stages.
type restAPICreateAgent struct {
	a                 *restAPI
	w                 http.ResponseWriter
	r                 *http.Request
	ac                config.AgentConfig
	createSoulContent string
	defaultModelName  string
	mutationResult    agentstore.MutationResult
}

// createAgent handles POST /api/v1/agents.
//
// The wire contract is a discriminated union: AgentCreateRequestMain /
// AgentCreateRequestSubagent / AgentCreateRequestSubagent3p, each a distinct
// schema (contracts/components/schemas/AgentCreateRequest{Main,Subagent,Subagent3p}.yaml,
// additionalProperties: false) selected by the request's `type` field. This
// handler mirrors the WsFrame dispatch pattern (websocket.go's peek+switch on
// frame.Type): buffer the body, peek `type` from the raw JSON, resolve the
// matching variant schema name, optionally validate the FULL body against
// that JSON Schema (when cfg.Gateway.ValidateInbound is enabled — richer
// errors, off by default), then strictly decode into the NAMED variant
// struct via decodeAgentCreateVariant (a field the chosen variant does not
// carry is rejected 400 unconditionally, independent of ValidateInbound) —
// never the generated union wrapper's As*() accessors — and normalize into
// the shared create core's agentCreateInput (agent_create_core.go), which
// applies the create rules (buildAgentCreate) and the persist step
// (persistAgentCreateLocked) shared with the package import.
//
// type is REQUIRED on every variant: a missing or unrecognized type value is
// a 400.
func (a *restAPI) createAgent(w http.ResponseWriter, r *http.Request) {
	cra := &restAPICreateAgent{a: a, w: w, r: r}

	in, stop := cra.decodeAgentCreateInput()
	if stop {
		return
	}
	plan, refusal := a.buildAgentCreate(in)
	if refusal != nil {
		// Rider #1914: a refusal ends the request; nothing is stored.
		refusal.write(w)
		return
	}
	if cra.persistAgent(&plan) {
		return
	}
	// agent-picker-freshness fix (#1009): the new agent is now durably
	// persisted (persistAgent returned success) — fan the agent_created
	// frame out so every OTHER connected tab's Agent Picker drops its stale
	// ['agents'] listing without waiting out the 30s query staleTime. Called
	// before publishResponse deliberately: publishResponse can still report
	// a partial "warning" (live-registry publish failed) in its 201 body,
	// but the entity itself already exists either way — the frame means
	// exactly that, nothing more.
	cra.a.emitAgentCreated(cra.ac.ID)
	cra.publishResponse()
}

// restAPICreateAgentPrepareAgent carries the shared state of
// decodeAgentCreateInput across its stages.
type restAPICreateAgentPrepareAgent struct {
	cra *restAPICreateAgent
	in  agentCreateInput
}

// decodeAgentCreateInput is the HTTP half of create: it reads, checks and
// strictly decodes the request into the shared create core's input. It
// reports stop once it has written an error response.
func (cra *restAPICreateAgent) decodeAgentCreateInput() (agentCreateInput, bool) {
	pap := &restAPICreateAgentPrepareAgent{cra: cra}
	raw, wireType, variantName, stop := pap.decodeAndValidateRequest()
	if stop {
		return agentCreateInput{}, true
	}
	pap.in.WireType = wireType
	executorIn, stop := pap.normalizeVariant(raw, wireType, variantName)
	if stop {
		return agentCreateInput{}, true
	}
	pap.in.Executor = executorIn
	return pap.in, false
}

func (pap *restAPICreateAgentPrepareAgent) decodeAndValidateRequest() ([]byte, string, string, bool) {
	raw, readOK := readAgentWriteBody(pap.cra.w, pap.cra.r)
	if !readOK {
		return nil, "", "", true
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		jsonErr(pap.cra.w, http.StatusBadRequest, "request body is required")
		return nil, "", "", true
	}
	presence, err := decodeRawJSONFields(raw)
	if err != nil {
		jsonErr(pap.cra.w, http.StatusBadRequest, "invalid JSON body")
		return nil, "", "", true
	}
	if pap.rejectNullCreateMembers(presence) {
		return nil, "", "", true
	}
	// Effort members are checked on the raw body in both validation modes
	// (FR-060, S-94).
	if refuseInvalidEffortBody(pap.cra.w, raw) {
		return nil, "", "", true
	}

	var typePeek struct {
		Type *string `json:"type"`
	}
	if err := json.Unmarshal(raw, &typePeek); err != nil {
		jsonErr(pap.cra.w, http.StatusBadRequest, "invalid JSON body")
		return nil, "", "", true
	}
	if typePeek.Type == nil {
		jsonErr(pap.cra.w, http.StatusBadRequest, agentCreateTypeRefusal)
		return nil, "", "", true
	}
	if *typePeek.Type == "system" {
		jsonErr(pap.cra.w, http.StatusBadRequest, "system agents are not creatable")
		return nil, "", "", true
	}
	variantName := agentCreateVariantName(*typePeek.Type)
	if variantName == "" {
		jsonErr(pap.cra.w, http.StatusBadRequest, agentCreateTypeRefusal)
		return nil, "", "", true
	}
	// r17 §3.11/§7.11: executor.kind is derived and executor.cli_path is
	// Settings-owned; each is refused with its own text and field before the
	// schema validator or the strict decoder could answer generically.
	if *typePeek.Type == string(gen.AgentTypeSubagent3p) &&
		refuseForbiddenExecutorFields(pap.cra.w, raw, "kind", "cli_path") {
		return nil, "", "", true
	}

	if pap.cra.a.agentLoop.GetConfig().Gateway.ValidateInbound &&
		!validateAgentIdentitySchema(pap.cra.w, variantName, raw, true) {
		return nil, "", "", true
	}
	return raw, *typePeek.Type, variantName, false
}

func (pap *restAPICreateAgentPrepareAgent) rejectNullCreateMembers(presence rawJSONFields) bool {
	// Identity null/empty is intentionally a create-default request. Only the
	// non-identity members below reject null; do not apply PUT's identity guard.
	for _, field := range []string{"skills", "mcp_servers", "tool_policy_changes"} {
		if presence.hasNull(field) {
			jsonErr(pap.cra.w, http.StatusBadRequest, field+" must not be null")
			return true
		}
	}
	// Inspect every occurrence of each parent as well as its nested members:
	// decoding either level into a map would hide null before a duplicate value.
	for _, rawChanges := range presence.valuesFor("tool_policy_changes") {
		members, err := decodeRawJSONFields(rawChanges)
		if err != nil {
			jsonErr(pap.cra.w, http.StatusBadRequest, "invalid JSON body")
			return true
		}
		for _, field := range []string{"set", "remove"} {
			if members.hasNull(field) {
				jsonErr(pap.cra.w, http.StatusBadRequest, "tool_policy_changes."+field+" must not be null")
				return true
			}
		}
	}
	for _, rawServers := range presence.valuesFor("mcp_servers") {
		var servers []json.RawMessage
		if err := json.Unmarshal(rawServers, &servers); err != nil {
			jsonErr(pap.cra.w, http.StatusBadRequest, "invalid JSON body")
			return true
		}
		for _, rawServer := range servers {
			server, err := decodeRawJSONFields(rawServer)
			if err != nil {
				jsonErr(pap.cra.w, http.StatusBadRequest, "invalid JSON body")
				return true
			}
			if server.hasNull("tools") {
				jsonErr(pap.cra.w, http.StatusBadRequest, "mcp_servers[].tools must not be null")
				return true
			}
		}
	}
	return false
}

func agentCreateVariantName(wireType string) string {
	switch wireType {
	case "Main":
		return "AgentCreateRequestMain"
	case "Subagent":
		return "AgentCreateRequestSubagent"
	case "subagent_3p":
		return "AgentCreateRequestSubagent3p"
	default:
		return ""
	}
}

// normalizeVariant decodes the selected wire variant into shared create fields.
func (pap *restAPICreateAgentPrepareAgent) normalizeVariant(raw []byte, wireType, variantName string) (*executorRequestInput, bool) {
	// Normalize the chosen variant into variant-agnostic locals. Each branch
	// strictly decodes raw into the NAMED generated struct via
	// decodeAgentCreateVariant (unknown fields — including fields that ARE
	// valid on a sibling variant, e.g. Subagent's tools_cfg on a
	// subagent_3p body — are rejected 400, independent of ValidateInbound)
	// and copies out only the fields that variant actually carries.
	// AgentCreateRequestSubagent has no Executor field at all;
	// AgentCreateRequestSubagent3p has no ToolsCfg/Skills/FallbackModels/
	// Voice fields (it does carry max_tool_iterations — #904 D14)
	// — a subagent_3p create supplying any of those is rejected at
	// decode time, both because the Go type has no matching field and
	// because the strict decoder refuses to silently drop it.

	switch wireType {
	case "Main":
		var vreq gen.AgentCreateRequestMain
		if !decodeAgentCreateVariant(pap.cra.w, raw, wireType, variantName, &vreq) {
			return nil, true
		}
		pap.in.Name = vreq.Name
		pap.in.Description = vreq.Description
		pap.in.Model = vreq.Model
		pap.in.Provider = vreq.Provider
		pap.in.Figure = vreq.Figure
		pap.in.Role = vreq.Role
		pap.in.Color = vreq.Color
		pap.in.Soul = vreq.Soul
		pap.in.Skills = vreq.Skills
		pap.in.MCPServers = agentCreateMCPServersFromWire(vreq.McpServers)
		pap.in.PolicyChanges = agentCreatePolicyChangesFromWire(vreq.ToolPolicyChanges)
		pap.in.FallbackModels = vreq.FallbackModels
		pap.in.ReasoningEffort = vreq.ReasoningEffort
		pap.in.ToolsCfg = agentCreateToolsCfgFromWire(vreq.ToolsCfg)
		pap.in.ModelParams = agentModelParamsFromWire(vreq.ModelParams)
		pap.in.MaxToolIterations = vreq.MaxToolIterations
		return nil, false
	case "Subagent":
		var vreq gen.AgentCreateRequestSubagent
		if !decodeAgentCreateVariant(pap.cra.w, raw, wireType, variantName, &vreq) {
			return nil, true
		}
		pap.in.Name = vreq.Name
		pap.in.Description = vreq.Description
		pap.in.Model = vreq.Model
		pap.in.Provider = vreq.Provider
		pap.in.Figure = vreq.Figure
		pap.in.Role = vreq.Role
		pap.in.Color = vreq.Color
		pap.in.Soul = vreq.Soul
		pap.in.Skills = vreq.Skills
		pap.in.MCPServers = agentCreateMCPServersFromWire(vreq.McpServers)
		pap.in.PolicyChanges = agentCreatePolicyChangesFromWire(vreq.ToolPolicyChanges)
		pap.in.FallbackModels = vreq.FallbackModels
		pap.in.ReasoningEffort = vreq.ReasoningEffort
		pap.in.ToolsCfg = agentCreateToolsCfgFromWire(vreq.ToolsCfg)
		pap.in.ModelParams = agentModelParamsFromWire(vreq.ModelParams)
		pap.in.MaxToolIterations = vreq.MaxToolIterations
		return nil, false
	case "subagent_3p":
		var vreq gen.AgentCreateRequestSubagent3p
		if !decodeAgentCreateVariant(pap.cra.w, raw, wireType, variantName, &vreq) {
			return nil, true
		}
		pap.in.Name = vreq.Name
		pap.in.Description = vreq.Description
		pap.in.Model = vreq.Model
		pap.in.Provider = vreq.Provider
		pap.in.Figure = vreq.Figure
		pap.in.Role = vreq.Role
		pap.in.Color = vreq.Color
		pap.in.MaxToolIterations = vreq.MaxToolIterations
		return &executorRequestInput{
			Cli:          string(vreq.Executor.Cli),
			EnvOverrides: vreq.Executor.EnvOverrides,
			CliArgs:      vreq.Executor.CliArgs,
		}, false
	}
	return nil, true
}

// persistAgent persists the planned agent under the tool-policy coverage
// guard and reports stop once it has written an error response.
func (cra *restAPICreateAgent) persistAgent(plan *agentCreatePlan) bool {
	// CLAUDE.md hard constraint 6 / config.ValidateToolPolicyCoverage: reject
	// the create if the new agent's tool-policy map — together with the
	// global sandbox.tool_policies — would leave any static builtin tool
	// without an explicit policy entry. Validated against a candidate config
	// snapshot (the live config plus this new agent appended); nothing here
	// persists or mutates the live in-memory config, so a rejected request
	// leaves no partial state behind.
	//
	// The validate step and the persist step below run inside ONE
	// a.configMu-locked critical section (closing a TOCTOU race two
	// concurrent creates could otherwise open — see updateConfigJSONLocked's
	// doc comment), via withToolPolicyCoverageGuard: it returns false once it
	// has already written the HTTP response (error case), so the caller just
	// returns.
	ok := cra.a.withToolPolicyCoverageGuard(
		cra.w,
		func(c *config.Config) {
			c.Agents.List = append(c.Agents.List, plan.Agent)
		},
		agentCreateCoverageGapMessage,
		// ADR-054 D2/§11 checklist item 1: agents are per-entity records under
		// entities/agents/<id>.json, not config.json's agents.list — persist
		// via the agent store (persistAgentCreateLocked) instead of splicing
		// the raw config map. `m` is deliberately left untouched; this closure
		// still runs inside updateConfigJSONLocked's a.configMu-guarded
		// critical section, so it composes with the coverage validation above.
		// CreateState performs write-then-verify, so a nil error here means
		// the agent is durably confirmed on disk before this handler ever
		// reports success to the caller.
		func(m map[string]any) error {
			result, err := cra.a.persistAgentCreateLocked(plan)
			cra.mutationResult = result
			return err
		},
		"rest: save agent entity record for new agent",
	)
	// The store stamps CreatedAt and binds skill grants on plan.Agent.
	cra.ac = plan.Agent
	cra.createSoulContent = plan.Soul
	if !ok {
		return true
	}
	cra.defaultModelName = cra.a.agentLoop.GetConfig().Agents.Defaults.DefaultModel.Model
	return false
}

// publishResponse publishes the agent to the live registry and returns the created response.
func (cra *restAPICreateAgent) publishResponse() {
	// Persistence succeeded. Publish the new agent into the live AgentRegistry
	// BEFORE we answer 201 — via the ADR-054 fast path (issue #571), not a
	// full config reload: creating one agent must not restart channels, cron,
	// the plan engine, or rebuild every OTHER agent's instance. fastAgentUpsert
	// re-registers just this one agent (registry.UpsertAgent + a fresh
	// resolver/default-agent-override, see AgentLoop.UpsertAgentFast) so
	// GetAgent/ResolveRoute all observe it the instant this
	// handler returns — closing the exact "201 followed by POST /tasks 400 on
	// the agent we just created" split a bare cfg.Agents.List append used to
	// leave open. It falls back to the slow, already-hardened full reload on
	// any wiring error, so a failure degrades to "slow but correct" instead of
	// a half-wired agent.
	//
	// The "warning" field signals a partial success — frontend must check this field.
	createReloadWarning := cra.a.fastAgentUpsert(cra.ac.ID)
	// Build the response from local variables only (do NOT read from live config — race).
	respModel := cra.defaultModelName
	if cra.ac.Model != nil && cra.ac.Model.Primary != "" {
		respModel = cra.ac.Model.Primary
	}
	// Capture execution config AFTER reload (TriggerReload may have swapped the live config).
	cfgAfterCreate := cra.a.agentLoop.GetConfig()
	ag := buildAgentDefaults(cfgAfterCreate)
	ag.Id = cra.ac.ID
	ag.Name = cra.ac.Name
	if cra.ac.Description != "" {
		ag.Description = &cra.ac.Description
	}
	applyStoredAgentIdentity(&ag, cra.ac)

	// Type reflects the chosen classification (custom or worker). For
	// "custom" this matches the pre-existing hardcoded behavior. For
	// "worker" it surfaces the create-time choice so the response — and
	// subsequent GET / list reads via coreagent.ResolveType — round-trips the
	// agent kind the caller actually created (Main/Subagent/subagent_3p on the wire).
	ag.Type = coreagent.ToWireType(cra.ac)
	ag.Locked = cra.ac.Locked
	applyAgentEditableFields(&ag, cra.ac)
	applyAgentOverrides(&ag, &cfgAfterCreate.Agents.Defaults, &cra.ac)
	ag.Model = &respModel
	setAgentModelProvider(&ag, cra.ac.Model)
	// Echo the just-persisted soul so the FE round-trip works. Status is
	// derived from the soul too: a non-empty soul moves the agent out of "draft".
	ag.Soul = cra.createSoulContent
	ag.Status = gen.AgentStatus(computeAgentStatus(cra.ac.ID, nil, cra.createSoulContent, cra.ac.Locked))
	if len(cra.ac.Skills) > 0 {
		skillsResp := make([]string, len(cra.ac.Skills))
		copy(skillsResp, cra.ac.Skills)
		ag.Skills = &skillsResp
	}
	setAgentExecutorResponse(&ag, cra.ac.Subagents, cfgAfterCreate, cra.a.homePath)
	ag.Revision = cra.mutationResult.Revision
	persistence := gen.AgentPersistenceStatusComplete
	ag.PersistenceStatus = &persistence
	activation := gen.AgentActivationStatusActive
	if createReloadWarning != "" {
		ag.Warning = &createReloadWarning
		activation = gen.AgentActivationStatusFailed
		ag.Message = &createReloadWarning
	}
	ag.ActivationStatus = &activation
	changed := []string{"name", "type", "soul"}
	ag.ChangedFields = &changed
	jsonCreated(cra.w, ag)
}
