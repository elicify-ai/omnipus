// rest_agents_create.go: Create an agent, and decode the create request's wire variants

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/google/uuid"
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

// agentCreateShellPolicyInput is a request-shape-agnostic normalization of
// the wire ShellPolicy object, mirroring agentCreateToolsCfgInput above.
// gen.AgentCreateRequestMain and gen.AgentCreateRequestSubagent each carry an
// anonymous ShellPolicy struct (oapi-codegen inlines it per-variant);
// gen.AgentCreateRequestSubagent3p has no shell_policy property at all (the
// external runner manages its own isolation), so this stays nil for that
// variant.
type agentCreateShellPolicyInput struct {
	EnableDenyPatterns *bool
	CustomDenyPatterns []string
}

// agentCreateShellPolicyFromWire converts either variant's shell_policy wire
// object into the common agentCreateShellPolicyInput. gen.AgentCreateRequestMain
// and gen.AgentCreateRequestSubagent each generate their own anonymous
// ShellPolicy struct, but — unlike ToolsCfg below — neither carries a
// per-variant enum type, so the two anonymous types are structurally
// identical and one non-generic helper handles both call sites.
func agentCreateShellPolicyFromWire(wp *struct {
	CustomDenyPatterns *[]string `json:"custom_deny_patterns,omitempty"`
	EnableDenyPatterns *bool     `json:"enable_deny_patterns,omitempty"`
},
) *agentCreateShellPolicyInput {
	if wp == nil {
		return nil
	}
	out := &agentCreateShellPolicyInput{EnableDenyPatterns: wp.EnableDenyPatterns}
	if wp.CustomDenyPatterns != nil {
		out.CustomDenyPatterns = *wp.CustomDenyPatterns
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
// identical across variants the way ShellPolicy is; every other field
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
		Servers *[]struct {
			Id    string    `json:"id"`
			Tools *[]string `json:"tools,omitempty"`
		} `json:"servers,omitempty"`
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
	soul              string
	toolsCfgIn        *agentCreateToolsCfgInput
	ac                config.AgentConfig
	createSoulContent string
	defaultModelName  string
	mutationResult    agentstore.MutationResult
	policyChanges     *agentmutation.ToolPolicyChanges
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
// never the generated union wrapper's As*() accessors — and normalize into a
// small set of variant-agnostic locals so the remainder of the handler
// (validation, persistence, response building) is written once.
//
// type is REQUIRED on every variant: a missing or unrecognized type value is
// a 400.
func (a *restAPI) createAgent(w http.ResponseWriter, r *http.Request) {
	cra := &restAPICreateAgent{a: a, w: w, r: r}

	if cra.prepareAgent() {
		return
	}
	if cra.buildToolConfig() {
		return
	}
	if cra.persistAgent() {
		return
	}
	cra.publishResponse()
}

// restAPICreateAgentPrepareAgent carries the shared state of prepareAgent across its stages.
type restAPICreateAgentPrepareAgent struct {
	cra            *restAPICreateAgent
	createType     config.AgentType
	name           string
	description    *string
	model          *string
	provider       *string
	color          *string
	icon           *string
	skills         *[]string
	fallbackModels *[]gen.FallbackModel
	shellPolicyIn  *agentCreateShellPolicyInput
	modelParamsIn  *agentModelParamsInput
	mcpServers     *[]agentCreateMCPServerInput
	policyChanges  *agentmutation.ToolPolicyChanges
}

// prepareAgent decodes and validates the request and builds the persistent agent config.
func (cra *restAPICreateAgent) prepareAgent() bool {
	pap := &restAPICreateAgentPrepareAgent{cra: cra}

	validateEnabled := pap.cra.a.agentLoop.GetConfig().Gateway.ValidateInbound

	raw, err := io.ReadAll(io.LimitReader(pap.cra.r.Body, 1<<20))
	if err != nil {
		jsonErr(pap.cra.w, http.StatusBadRequest, "could not read request body")
		return true
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		jsonErr(pap.cra.w, http.StatusBadRequest, "request body is required")
		return true
	}
	var presence map[string]json.RawMessage
	if err := json.Unmarshal(raw, &presence); err != nil {
		jsonErr(pap.cra.w, http.StatusBadRequest, "invalid JSON body")
		return true
	}
	for _, field := range []string{"skills", "mcp_servers", "tool_policy_changes"} {
		if value, supplied := presence[field]; supplied && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			jsonErr(pap.cra.w, http.StatusBadRequest, field+" must not be null")
			return true
		}
	}
	if rawChanges, supplied := presence["tool_policy_changes"]; supplied {
		var members map[string]json.RawMessage
		if json.Unmarshal(rawChanges, &members) == nil {
			for _, field := range []string{"set", "remove"} {
				if value, exists := members[field]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					jsonErr(pap.cra.w, http.StatusBadRequest, "tool_policy_changes."+field+" must not be null")
					return true
				}
			}
		}
	}
	if rawServers, supplied := presence["mcp_servers"]; supplied {
		var servers []map[string]json.RawMessage
		if json.Unmarshal(rawServers, &servers) == nil {
			for _, server := range servers {
				if value, exists := server["tools"]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					jsonErr(pap.cra.w, http.StatusBadRequest, "mcp_servers[].tools must not be null")
					return true
				}
			}
		}
	}

	var typePeek struct {
		Type *string `json:"type"`
	}
	if err := json.Unmarshal(raw, &typePeek); err != nil {
		jsonErr(pap.cra.w, http.StatusBadRequest, "invalid JSON body")
		return true
	}
	const typeErrMsg = "type is required and must be one of Main, Subagent, subagent_3p"
	if typePeek.Type == nil {
		jsonErr(pap.cra.w, http.StatusBadRequest, typeErrMsg)
		return true
	}
	// ADR-049 D3: System Agents (the Judge category) are seed-only. The ONLY
	// creation path is coreagent.SeedConfig — never the REST create path nor the
	// create_agent tool. Reject with a precise message before the generic
	// variant switch so a client sending {"type":"system"} gets a clear 400.
	if *typePeek.Type == "system" {
		jsonErr(pap.cra.w, http.StatusBadRequest, "system agents are not creatable")
		return true
	}
	var variantName string
	switch *typePeek.Type {
	case "Main":
		variantName = "AgentCreateRequestMain"
	case "Subagent":
		variantName = "AgentCreateRequestSubagent"
	case "subagent_3p":
		variantName = "AgentCreateRequestSubagent3p"
	default:
		// Covers "core" / "system" (seeded-only classifications — the only way
		// to obtain one is via SeedConfig, never the REST create path) and any
		// other unrecognized value.
		jsonErr(pap.cra.w, http.StatusBadRequest, typeErrMsg)
		return true
	}
	// Boundary translation between wire (Main/Subagent/subagent_3p) and
	// persisted config (custom/worker) — single source of truth in
	// coreagent.ResolveType.
	pap.createType = coreagent.ResolveType(gen.AgentType(*typePeek.Type))

	if validateEnabled {
		if errMsg, serverErr := validateBodyAgainstSchema(variantName, raw); errMsg != "" {
			if serverErr {
				jsonErr(pap.cra.w, http.StatusInternalServerError, "inbound schema unavailable")
			} else {
				jsonErr(pap.cra.w, http.StatusBadRequest,
					fmt.Sprintf("request body does not match schema %s: %s", variantName, errMsg))
			}
			return true
		}
	}

	// Normalize the chosen variant into variant-agnostic locals. Each branch
	// strictly decodes raw into the NAMED generated struct via
	// decodeAgentCreateVariant (unknown fields — including fields that ARE
	// valid on a sibling variant, e.g. Subagent's tools_cfg on a
	// subagent_3p body — are rejected 400, independent of ValidateInbound)
	// and copies out only the fields that variant actually carries.
	// AgentCreateRequestSubagent has no Executor field at all;
	// AgentCreateRequestSubagent3p has no ToolsCfg/Skills/FallbackModels/
	// ShellPolicy/Voice/MaxToolIterations
	// fields — a subagent_3p create supplying any of those is rejected at
	// decode time, both because the Go type has no matching field and
	// because the strict decoder refuses to silently drop it.

	var executorIn *executorRequestInput

	switch *typePeek.Type {
	case "Main":
		var vreq gen.AgentCreateRequestMain
		if !decodeAgentCreateVariant(pap.cra.w, raw, *typePeek.Type, variantName, &vreq) {
			return true
		}
		pap.name = vreq.Name
		pap.description = vreq.Description
		pap.model = vreq.Model
		pap.provider = vreq.Provider
		pap.color = vreq.Color
		pap.icon = vreq.Icon
		pap.cra.soul = vreq.Soul
		pap.skills = vreq.Skills
		pap.mcpServers = agentCreateMCPServersFromWire(vreq.McpServers)
		pap.policyChanges = agentCreatePolicyChangesFromWire(vreq.ToolPolicyChanges)
		pap.fallbackModels = vreq.FallbackModels
		pap.shellPolicyIn = agentCreateShellPolicyFromWire(vreq.ShellPolicy)
		pap.cra.toolsCfgIn = agentCreateToolsCfgFromWire(vreq.ToolsCfg)
		pap.modelParamsIn = agentModelParamsFromWire(vreq.ModelParams)
	case "Subagent":
		var vreq gen.AgentCreateRequestSubagent
		if !decodeAgentCreateVariant(pap.cra.w, raw, *typePeek.Type, variantName, &vreq) {
			return true
		}
		pap.name = vreq.Name
		pap.description = vreq.Description
		pap.model = vreq.Model
		pap.provider = vreq.Provider
		pap.color = vreq.Color
		pap.icon = vreq.Icon
		pap.cra.soul = vreq.Soul
		pap.skills = vreq.Skills
		pap.mcpServers = agentCreateMCPServersFromWire(vreq.McpServers)
		pap.policyChanges = agentCreatePolicyChangesFromWire(vreq.ToolPolicyChanges)
		pap.fallbackModels = vreq.FallbackModels
		pap.shellPolicyIn = agentCreateShellPolicyFromWire(vreq.ShellPolicy)
		pap.cra.toolsCfgIn = agentCreateToolsCfgFromWire(vreq.ToolsCfg)
		pap.modelParamsIn = agentModelParamsFromWire(vreq.ModelParams)
	case "subagent_3p":
		var vreq gen.AgentCreateRequestSubagent3p
		if !decodeAgentCreateVariant(pap.cra.w, raw, *typePeek.Type, variantName, &vreq) {
			return true
		}
		pap.name = vreq.Name
		pap.description = vreq.Description
		pap.model = vreq.Model
		pap.provider = vreq.Provider
		pap.color = vreq.Color
		pap.icon = vreq.Icon
		pap.cra.soul = vreq.Soul
		executorIn = &executorRequestInput{
			Cli:          executorCliStr(vreq.Executor.Cli),
			CliPath:      vreq.Executor.CliPath,
			EnvOverrides: vreq.Executor.EnvOverrides,
			CliArgs:      vreq.Executor.CliArgs,
		}
	}

	// Trim before the empty check so a whitespace-only name ("   ") is rejected
	// rather than silently accepted (UAT fix). Persist the trimmed value.
	pap.name = strings.TrimSpace(pap.name)
	if pap.name == "" {
		jsonErr(pap.cra.w, http.StatusUnprocessableEntity, "name is required")
		return true
	}
	// ADR-071 §5.1.3 part 2: "default" (case-insensitive) is reserved — it is
	// the switch_agent target sentinel that always means "the configured
	// default agent". Rejecting it at the create boundary makes the id/name
	// collision impossible going forward, rather than merely documented (the
	// part 3 upgrade-time WARN below covers agents that predate this check).
	// Create's id is always a fresh uuid.New().String() (never operator-
	// chosen), so only the name half of this rule is reachable here.
	if strings.EqualFold(pap.name, tools.SwitchAgentDefaultTarget) {
		jsonErr(pap.cra.w, http.StatusBadRequest,
			fmt.Sprintf("agent name %q is reserved — it collides with switch_agent's target:%q sentinel", pap.name, tools.SwitchAgentDefaultTarget))
		return true
	}
	// subagent_3p executor.cli_path: required (spec §9.2), whitespace-only
	// rejected. The schema requires the `executor` object itself to be
	// present for this variant, but its nested cli/cli_path properties are
	// optional strings — JSON Schema has no "non-whitespace" constraint, so
	// this stays a runtime check even with ValidateInbound enabled.
	// executor.kind is intentionally NOT checked here: it is always
	// server-derived to external-cli for subagent_3p below, regardless of
	// what (if anything) the client sent — kind "is exposed in responses but
	// is NOT a writable field on create/update — clients cannot choose kind
	// directly" (contracts/components/schemas/ExecutorConfig.yaml).
	if pap.createType == config.AgentTypeWorker && *typePeek.Type == string(gen.AgentTypeSubagent3p) {
		if executorIn == nil || executorIn.CliPath == nil || strings.TrimSpace(*executorIn.CliPath) == "" {
			jsonErr(pap.cra.w, http.StatusBadRequest, "executor.cli_path is required for subagent_3p agents")
			return true
		}
	}
	if r0, stop := pap.validateAndBuildConfig(); stop {
		return r0
	}
	// Sub-agent executor. Mapped into AgentConfig.Subagents.Executor so it is
	// actually persisted. A native Subagent (no Executor property on the wire
	// at all) always gets a native runtime here. A subagent_3p always gets
	// kind=external-cli — the wire schema requires `executor` to be present
	// for that variant and, per the field matrix, kind is server-derived
	// (never client-writable) rather than read from the request.
	if pap.createType == config.AgentTypeWorker {
		if *typePeek.Type == string(gen.AgentTypeSubagent3p) {
			execCfg, errMsg := executorConfigFromRequest(string(config.ExecutorKindExternalCLI), executorIn.Cli)
			if errMsg != "" {
				jsonErr(pap.cra.w, http.StatusBadRequest, errMsg)
				return true
			}
			if executorIn.CliPath != nil {
				execCfg.CLIPath = *executorIn.CliPath
			}
			if executorIn.EnvOverrides != nil {
				execCfg.EnvOverrides = *executorIn.EnvOverrides
			}
			if executorIn.CliArgs != nil {
				execCfg.CLIArgs = *executorIn.CliArgs
			}
			pap.cra.ac.Subagents = &config.SubagentsConfig{Executor: execCfg}
		} else {
			pap.cra.ac.Subagents = &config.SubagentsConfig{
				Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative},
			}
		}
	}
	return false
}

// validateAndBuildConfig validates shared fields and builds the base persistent agent config.
func (pap *restAPICreateAgentPrepareAgent) validateAndBuildConfig() (bool, bool) {
	// Referential validation: reject unknown skill IDs before doing any work.
	if pap.skills != nil && len(*pap.skills) > 0 {
		if errMsg := pap.cra.a.validateSkillIDs(*pap.skills); errMsg != "" {
			jsonErr(pap.cra.w, http.StatusBadRequest, errMsg)
			return true, true
		}
	}
	descTrimmed := ""
	if pap.description != nil {
		descTrimmed = strings.TrimSpace(*pap.description)
	}
	// W2 spec §4.3 / §9.2 F-02 (mirror of PUT path): Subagent and subagent_3p
	// require a non-empty description after trim. A worker without a
	// description cannot be routed to by the orchestrator.
	if pap.createType == config.AgentTypeWorker && descTrimmed == "" {
		jsonErr(pap.cra.w, http.StatusBadRequest, "description is required for worker agents (Subagent, subagent_3p)")
		return true, true
	}
	// O12.1 — voice is Main-only (form matrix row 13): no runtime check is
	// needed here any more. AgentCreateRequestSubagent / …Subagent3p
	// structurally have no `voice` property (additionalProperties: false) —
	// a worker create can no longer carry one at all. Heartbeat is
	// workspace-scoped (ADR-027) and is no longer set at create time.
	//
	// W2 spec §3.1 row 16 / §9.2 F-13 (mirror of PUT path): fallback_models
	// is capped at 2 entries. Server-enforced so direct REST callers (not the
	// SPA) cannot smuggle more entries past the schema validator. subagent_3p
	// has no fallback_models property (fallbackModels stays nil for that variant).
	if pap.fallbackModels != nil && len(*pap.fallbackModels) > 2 {
		jsonErr(pap.cra.w, http.StatusBadRequest, "fallback_models exceeds maxItems: 2")
		return true, true
	}
	// model_params.top_p (T2): removed from the wire entirely — see
	// agentModelParamsInput's doc comment. No explicit rejection is needed
	// here: gen.AgentCreateRequestMain/Subagent no longer have a top_p
	// property at all, and decodeAgentCreateVariant's unconditional
	// DisallowUnknownFields decode above already rejects a client still
	// sending {"model_params":{"top_p":...}} with a 400 "unknown field"
	// error before this point is ever reached.
	// W2 spec §4.7 / §9.2 row 8: whitespace-only soul is rejected (the wire
	// schema enforces minLength:1; whitespace-only is the natural
	// soft-bypass). Backend trims before validation.
	if pap.cra.soul == "" || strings.TrimSpace(pap.cra.soul) == "" {
		jsonErr(pap.cra.w, http.StatusBadRequest, "soul is required (whitespace-only is rejected as minLength violation)")
		return true, true
	}
	colorVal := ""
	if pap.color != nil {
		colorVal = *pap.color
	}
	iconVal := ""
	if pap.icon != nil {
		iconVal = *pap.icon
	}
	// color hex regex (spec §4.4).
	if colorVal != "" {
		if matched, _ := regexp.MatchString(`^#[0-9A-Fa-f]{6}$`, colorVal); !matched {
			jsonErr(pap.cra.w, http.StatusBadRequest, "color must be a valid hex code (e.g. #D4AF37)")
			return true, true
		}
	}
	// icon maxLength:50 (spec §4.4).
	if len(iconVal) > 50 {
		jsonErr(pap.cra.w, http.StatusBadRequest, "icon exceeds maxLength: 50")
		return true, true
	}
	// ac is the in-memory config record. Locked is left at its zero value
	// (false) for BOTH custom and worker creates — the API is the operator's
	// surface for editing; only SeedConfig-seeded agents are locked. A newly
	// created worker is therefore editable (unlike the seeded default
	// general-purpose worker, which is locked by coreagent.SeedConfig).
	pap.cra.ac = config.AgentConfig{
		ID:          uuid.New().String(),
		Name:        pap.name,
		Description: descTrimmed,
		Color:       colorVal,
		Icon:        iconVal,
		Type:        pap.createType,
	}
	if pap.model != nil && *pap.model != "" {
		pap.cra.ac.Model = &config.AgentModelConfig{Primary: *pap.model}
		// O3 two-field model: persist the explicit primary provider when supplied.
		if pap.provider != nil && strings.TrimSpace(*pap.provider) != "" {
			pap.cra.ac.Model.Provider = strings.TrimSpace(*pap.provider)
		}
	}
	// model_params (T1 fix): createAgent previously decoded model_params fine
	// (both AgentCreateRequestMain.yaml and AgentCreateRequestSubagent.yaml
	// carry it) but had nowhere to persist it — same ADR-037 anti-pattern
	// commit 2b057e15 (Q1) fixed for PUT. mergeAgentModelParams is the exact
	// helper that fix introduced for updateAgent; existing is nil here since
	// this is a brand-new agent record.
	pap.cra.ac.ModelParams = mergeAgentModelParams(nil, pap.modelParamsIn)
	// shell_policy: mapped onto AgentConfig so it is actually persisted.
	// subagent_3p has no shell_policy property on the wire (shellPolicyIn
	// stays nil for that variant — the CLI manages its own isolation), so it
	// stays unset there, matching updateAgent's rejection of this field on a
	// subagent_3p PUT.
	if pap.shellPolicyIn != nil {
		sp := &config.AgentShellPolicy{}
		if pap.shellPolicyIn.EnableDenyPatterns != nil {
			sp.EnableDenyPatterns = *pap.shellPolicyIn.EnableDenyPatterns
		}
		if len(pap.shellPolicyIn.CustomDenyPatterns) > 0 {
			sp.CustomDenyPatterns = make([]string, len(pap.shellPolicyIn.CustomDenyPatterns))
			copy(sp.CustomDenyPatterns, pap.shellPolicyIn.CustomDenyPatterns)
		}
		pap.cra.ac.ShellPolicy = sp
	}
	// Heartbeat is workspace-scoped (ADR-027); no per-agent heartbeat at create.
	if pap.skills != nil && len(*pap.skills) > 0 {
		pap.cra.ac.Skills = make([]string, len(*pap.skills))
		copy(pap.cra.ac.Skills, *pap.skills)
	}
	if pap.mcpServers != nil || pap.policyChanges != nil {
		if pap.cra.toolsCfgIn == nil {
			pap.cra.toolsCfgIn = &agentCreateToolsCfgInput{}
		}
		if pap.mcpServers != nil {
			if pap.cra.toolsCfgIn.MCPSpecified {
				jsonErr(pap.cra.w, http.StatusBadRequest, "mcp_servers and tools_cfg.mcp cannot both be supplied")
				return false, true
			}
			pap.cra.toolsCfgIn.MCPServers = *pap.mcpServers
			pap.cra.toolsCfgIn.MCPSpecified = true
		}
	}
	pap.cra.createSoulContent = strings.TrimSpace(pap.cra.soul)
	if pap.policyChanges != nil {
		if pap.cra.toolsCfgIn != nil && pap.cra.toolsCfgIn.BuiltinPolicies != nil {
			jsonErr(pap.cra.w, http.StatusBadRequest, "tool_policy_changes and tools_cfg.builtin cannot both be supplied")
			return false, true
		}
		pap.cra.ac.Tools = nil // applied after the server seed is constructed
		pap.cra.policyChanges = pap.policyChanges
	}
	return false, false
}

// buildToolConfig validates and builds the agent's builtin and MCP tool configuration.
func (cra *restAPICreateAgent) buildToolConfig() bool {
	if cra.toolsCfgIn != nil {
		configured := cra.a.agentLoop.GetConfig().Tools.MCP.Servers
		for _, server := range cra.toolsCfgIn.MCPServers {
			if _, ok := configured[server.ID]; !ok || server.ID == "" {
				jsonErr(cra.w, http.StatusUnprocessableEntity, fmt.Sprintf("MCP server %q is not configured", server.ID))
				return true
			}
			if err := config.ValidateAgentMCPServerBinding(config.AgentMCPServerBinding{ID: server.ID, Tools: server.Tools, ToolsSpecified: server.ToolsSpecified}); err != nil {
				jsonErr(cra.w, http.StatusUnprocessableEntity, err.Error())
				return true
			}
		}
	}
	// ADR-037: delegation_policy is retired from the wire entirely — the
	// per-workspace delegation graph (Team tab) is the sole delegation
	// mechanism. There is nothing left to map/validate/persist here.
	// Seed the privilege rail (FR-008/FR-022): custom agents always get every
	// static builtin tool explicitly denied except a narrow, conservative
	// read-only allow-list (coreagent.NewCustomAgentToolsCfg —
	// denyAllThenOverride), unless the caller explicitly overrides individual
	// entries. There is no wildcard involved anywhere in this model (the old
	// "system.*" glob matched zero real tool names and was retired). subagent_3p
	// has no tools_cfg property at all (toolsCfgIn stays nil), so it always
	// falls through to the seeded baseCfg — matching "the runner has its own
	// tools" (field matrix).
	//
	// CLAUDE.md hard constraint 6 — validate the CALLER'S OWN map FIRST, before
	// the deny-seeded baseline below is allowed to paper over it.
	//
	// This check has to happen here, and not (only) via the
	// config.ValidateToolPolicyCoverage guard further down, because of the
	// merge that follows: the handler starts from the fully-enumerated
	// coreagent.NewCustomAgentToolsCfg() seed and copies the caller's entries
	// ON TOP of it. A tool the caller omitted is therefore silently backfilled
	// from the seed before the coverage check ever runs, so that check can
	// structurally never observe a caller-side gap on this path — it was dead
	// code for POST /agents. Reproduced live (UAT 2026-09-02, batch 3 S48 /
	// batch 4 S83): a create omitting `bash` entirely returned 201, and a
	// create carrying a literal "*" key returned 201 with the wildcard stored
	// inertly alongside the real entries, because the merge loop copied every
	// caller key verbatim with no check that it names a real tool.
	//
	// Only runs when the caller actually submitted a builtin policy map. A
	// request with no tools_cfg at all (or a tools_cfg carrying only `mcp`)
	// legitimately falls through to the server-generated, complete, deny-seeded
	// baseline — that is not a caller gap, and subagent_3p has no tools_cfg on
	// the wire at all.
	if cra.toolsCfgIn != nil && cra.toolsCfgIn.BuiltinPolicies != nil {
		if defects := config.ValidateSubmittedToolPolicyMap(
			cra.toolsCfgIn.BuiltinPolicies, buildKnownBuiltinToolNames(),
		); !defects.Empty() {
			jsonErr(cra.w, http.StatusBadRequest,
				"tools_cfg.builtin.policies "+defects.String())
			return true
		}
	}
	baseCfg := coreagent.NewCustomAgentToolsCfg()
	if cra.toolsCfgIn != nil {
		builtin := config.AgentBuiltinToolsCfg{
			// Inherit the fully-enumerated deny-by-default seed (every static
			// tool present with an explicit "deny" or "allow" entry, no
			// wildcard) so the merged map stays complete even when the
			// caller's policies map is sparse.
			Policies: make(map[string]config.ToolPolicy, len(baseCfg.Builtin.Policies)),
		}
		for k, v := range baseCfg.Builtin.Policies {
			builtin.Policies[k] = v
		}
		// Merge caller-supplied policies; the caller's per-tool entry (exact
		// name, never a wildcard) overrides the corresponding seed entry.
		for k, v := range cra.toolsCfgIn.BuiltinPolicies {
			builtin.Policies[k] = config.ToolPolicy(v)
		}
		cra.ac.Tools = &config.AgentToolsCfg{Builtin: builtin}
		if cra.toolsCfgIn.MCPSpecified {
			servers := make([]config.AgentMCPServerBinding, 0, len(cra.toolsCfgIn.MCPServers))
			for _, s := range cra.toolsCfgIn.MCPServers {
				servers = append(servers, config.AgentMCPServerBinding{ID: s.ID, Tools: s.Tools, ToolsSpecified: s.ToolsSpecified})
			}
			cra.ac.Tools.MCP = config.AgentMCPToolsCfg{Servers: servers}
		}
	} else {
		// No caller-supplied tools config: use the full base config.
		cra.ac.Tools = baseCfg
	}
	if cra.policyChanges != nil {
		updated, err := agentmutation.ApplyToolPolicyChanges(cra.ac.Tools.Builtin.Policies, *cra.policyChanges, buildKnownBuiltinToolNames())
		if err != nil {
			jsonErr(cra.w, http.StatusBadRequest, err.Error())
			return true
		}
		cra.ac.Tools.Builtin.Policies = updated
	}
	for name := range buildKnownBuiltinToolNames() {
		if _, exists := cra.ac.Tools.Builtin.Policies[name]; !exists {
			cra.ac.Tools.Builtin.Policies[name] = config.ToolPolicyDeny
		}
	}
	return false
}

// persistAgent persists the new agent under the tool-policy coverage guard.
func (cra *restAPICreateAgent) persistAgent() bool {
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
	if ok := cra.a.withToolPolicyCoverageGuard(
		cra.w,
		func(c *config.Config) {
			c.Agents.List = append(c.Agents.List, cra.ac)
		},
		func(gaps []config.CoverageGap) string {
			return fmt.Sprintf(
				"tool policy coverage incomplete for new agent (%d gap(s)): %s",
				len(gaps), joinCoverageGapMessages(gaps),
			)
		},
		// ADR-054 D2/§11 checklist item 1: agents are per-entity records under
		// entities/agents/<id>.json, not config.json's agents.list — persist
		// via the agent store instead of splicing the raw config map. `m` is
		// deliberately left untouched (config.json no longer carries the
		// roster); this closure still runs inside updateConfigJSONLocked's
		// a.configMu-guarded critical section, so it composes with the
		// tool-policy-coverage validation above exactly as before.
		// agentstore.Store.Create (via entity.Store.Create) already performs
		// write-then-verify (D6 corollary: it reads the just-written file back
		// and confirms it parses with the expected ID) and stamps CreatedAt —
		// a nil error here means ac is durably confirmed on disk before this
		// handler ever reports success to the caller.
		func(m map[string]any) error {
			result, err := agentstore.New(cra.a.homePath).CreateState(cra.ac.ID, &cra.ac, cra.createSoulContent)
			cra.mutationResult = result
			if err != nil {
				return &configurationMutationError{Result: result, Err: fmt.Errorf("create agent state: %w", err)}
			}
			return nil
		},
		"rest: save agent entity record for new agent",
	); !ok {
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
	// GetAgent/ResolveRoute/GetDefaultAgent all observe it the instant this
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
	if cra.ac.Color != "" {
		ag.Color = &cra.ac.Color
	}
	if cra.ac.Icon != "" {
		ag.Icon = &cra.ac.Icon
	}
	// Type reflects the chosen classification (custom or worker). For
	// "custom" this matches the pre-existing hardcoded behavior. For
	// "worker" it surfaces the create-time choice so the response — and
	// subsequent GET / list reads via coreagent.ResolveType — round-trips the
	// agent kind the caller actually created (Main/Subagent/subagent_3p on the wire).
	ag.Type = coreagent.ToWireType(cra.ac)
	ag.Locked = cra.ac.Locked
	applyAgentEditableFields(&ag, cra.ac)
	applyAgentOverrides(&ag, &cra.ac)
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
	setAgentExecutorResponse(&ag, cra.ac.Subagents)
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
