// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// agent_create_core.go: the shared agent-create core (epic #1094 T-3; spec
// export-import-s1 revision 7, FR-034, FR-020, FR-047; architect sign-off
// ARCH-SIGNOFF-T3-T4.md, T-3 block). REST POST /api/v1/agents and the package
// import build an agent with the same create rules, in the same order:
// buildAgentCreate validates and builds without side effects, and
// persistAgentCreateLocked stores it under a.configMu. REST keeps its
// config-writing coverage guard (withToolPolicyCoverageGuard); the import uses
// withToolPolicyCoverageCheck, which never touches config.json.

package gateway

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
)

// agentCreateTypeRefusal is the refusal text for a missing or unknown create
// type (Main, Subagent, subagent_3p).
const agentCreateTypeRefusal = "type is required and must be one of Main, Subagent, subagent_3p"

// agentCreateInput is the shared create function's domain input. It is NOT a
// REST request type; the generated leaf component types (gen.FallbackModel,
// gen.AgentColor/Figure/Role) are shared schema parts.
type agentCreateInput struct {
	WireType          string // "Main" | "Subagent" | "subagent_3p" (S1a import never sends subagent_3p)
	Name              string // untrimmed; build trims it
	Description       *string
	Soul              string
	Model, Provider   *string
	Color             *gen.AgentColor
	Figure            *gen.AgentFigure
	Role              *gen.AgentRole
	Skills            *[]string
	FallbackModels    *[]gen.FallbackModel
	ReasoningEffort   *string
	ModelParams       *agentModelParamsInput
	MCPServers        *[]agentCreateMCPServerInput
	PolicyChanges     *agentmutation.ToolPolicyChanges
	ToolsCfg          *agentCreateToolsCfgInput
	MaxToolIterations *int
	Executor          *executorRequestInput // subagent_3p only
}

// agentCreateRefusalError is a create-rule refusal. The REST adapter writes it
// exactly as the handler wrote it before the extraction: Code set ->
// jsonErrCode; else Field set -> jsonErrField; else jsonErr.
type agentCreateRefusalError struct {
	Status int
	Msg    string
	Code   string // only for the name rules (agent_name_too_long, reserved_agent_name)
	Field  string // e.g. "max_tool_iterations", "executor.env_overrides"
	// fieldOffWire keeps Field out of the REST body. The description,
	// fallback_models, color, figure and role rules name their field for
	// import's element_not_creatable (D1 rows 23, 24, 29), while REST create
	// keeps its earlier body: the sentence alone.
	fieldOffWire bool
}

func (r *agentCreateRefusalError) Error() string { return r.Msg }

func (r *agentCreateRefusalError) write(w http.ResponseWriter) {
	switch {
	case r.Code != "":
		jsonErrCode(w, r.Status, r.Msg, r.Code)
	case r.Field != "" && !r.fieldOffWire:
		jsonErrField(w, r.Status, r.Msg, r.Field)
	default:
		jsonErr(w, r.Status, r.Msg)
	}
}

func createRefusal(status int, msg string) *agentCreateRefusalError {
	return &agentCreateRefusalError{Status: status, Msg: msg}
}

// createFieldRefusal is createRefusal naming the offending field for import,
// with the REST body unchanged (fieldOffWire).
func createFieldRefusal(status int, msg, field string) *agentCreateRefusalError {
	return &agentCreateRefusalError{Status: status, Msg: msg, Field: field, fieldOffWire: true}
}

// agentCreatePlan is a fully validated, built, NOT yet persisted agent.
type agentCreatePlan struct {
	Agent config.AgentConfig // ID = fresh uuid.New(), tools seeded and merged
	Soul  string             // strings.TrimSpace(input.Soul)
}

// toolPolicyCoverageGapError carries the coverage gaps
// withToolPolicyCoverageCheck found.
type toolPolicyCoverageGapError struct {
	Gaps []config.CoverageGap
}

func (e *toolPolicyCoverageGapError) Error() string { return agentCreateCoverageGapMessage(e.Gaps) }

// agentCreateCoverageGapMessage is the refusal text for a new agent whose
// tool-policy map leaves a static builtin uncovered.
func agentCreateCoverageGapMessage(gaps []config.CoverageGap) string {
	return fmt.Sprintf("tool policy coverage incomplete for new agent (%d gap(s)): %s",
		len(gaps), joinCoverageGapMessages(gaps))
}

// agentCreateBuild carries buildAgentCreate's state across its stages.
type agentCreateBuild struct {
	a             *restAPI
	in            agentCreateInput
	createType    config.AgentType
	toolsCfgIn    *agentCreateToolsCfgInput
	policyChanges *agentmutation.ToolPolicyChanges
	ac            config.AgentConfig
	soul          string
}

// buildAgentCreate runs every create rule that does not need configMu, in the
// order the REST handler always ran them: name, shared fields, the base
// record, the executor, then the tool configuration. It has no side effects
// (no disk or config writes), so the upload dry run (FR-020) can call it. It
// never modifies in.
func (a *restAPI) buildAgentCreate(in agentCreateInput) (agentCreatePlan, *agentCreateRefusalError) {
	if agentCreateVariantName(in.WireType) == "" {
		return agentCreatePlan{}, createRefusal(http.StatusBadRequest, agentCreateTypeRefusal)
	}
	b := &agentCreateBuild{a: a, in: in, createType: coreagent.ResolveType(gen.AgentType(in.WireType))}
	if in.ToolsCfg != nil {
		toolsCfg := *in.ToolsCfg
		b.toolsCfgIn = &toolsCfg
	}
	// Trim before the empty check so a whitespace-only name ("   ") is rejected
	// rather than silently accepted (UAT fix). Persist the trimmed value.
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return agentCreatePlan{}, createRefusal(http.StatusUnprocessableEntity, "name is required")
	}
	if refusal := reservedAgentNameRefusal(name); refusal != nil {
		return agentCreatePlan{}, refusal
	}
	if refusal := b.buildBaseConfig(name); refusal != nil {
		return agentCreatePlan{}, refusal
	}
	if refusal := b.buildExecutor(); refusal != nil {
		return agentCreatePlan{}, refusal
	}
	if refusal := b.buildToolConfig(); refusal != nil {
		return agentCreatePlan{}, refusal
	}
	return agentCreatePlan{Agent: b.ac, Soul: b.soul}, nil
}

// createFields is what validateCreateFields resolves for the base record.
type createFields struct {
	description, color, figure, role string
}

// validateCreateFields validates the incoming shared agent fields before
// constructing a config.
func (b *agentCreateBuild) validateCreateFields() (createFields, *agentCreateRefusalError) {
	var out createFields
	// Referential validation: reject unknown skill IDs before doing any work.
	if b.in.Skills != nil && len(*b.in.Skills) > 0 {
		if errMsg := b.a.validateSkillIDs(*b.in.Skills); errMsg != "" {
			return out, createRefusal(http.StatusBadRequest, errMsg)
		}
	}
	if b.in.Description != nil {
		out.description = strings.TrimSpace(*b.in.Description)
	}
	// W2 spec §4.3 / §9.2 F-02 (mirror of PUT path): Subagent and subagent_3p
	// require a non-empty description after trim. A worker without a
	// description cannot be routed to by the orchestrator.
	if b.createType == config.AgentTypeWorker && out.description == "" {
		return out, createFieldRefusal(http.StatusBadRequest, "description is required for worker agents (Subagent, subagent_3p)", "description")
	}
	// W2 spec §3.1 row 16 / §9.2 F-13 (mirror of PUT path): fallback_models
	// is capped at 2 entries. Server-enforced so direct REST callers (not the
	// SPA) cannot smuggle more entries past the schema validator. subagent_3p
	// has no fallback_models property (FallbackModels stays nil for that variant).
	if b.in.FallbackModels != nil && len(*b.in.FallbackModels) > 2 {
		return out, createFieldRefusal(http.StatusBadRequest, "fallback_models exceeds maxItems: 2", "fallback_models")
	}
	// Issue #1340 (founder rule 2026-10-18): same pair rule as the update path.
	if b.in.FallbackModels != nil {
		if msg := unpairedFallbackError(*b.in.FallbackModels); msg != "" {
			return out, createRefusal(http.StatusBadRequest, msg)
		}
	}
	// W2 spec §4.7 / §9.2 row 8: whitespace-only soul is rejected (the wire
	// schema enforces minLength:1; whitespace-only is the natural
	// soft-bypass). Backend trims before validation. r17 §7.5 AG-04 (F-05):
	// an external worker (subagent_3p) has no soul at all, so the requirement
	// applies to the native types only.
	if b.in.WireType != string(gen.AgentTypeSubagent3p) && strings.TrimSpace(b.in.Soul) == "" {
		return out, createRefusal(http.StatusBadRequest, "soul is required (whitespace-only is rejected as minLength violation)")
	}
	out.figure = coreagent.DefaultFigure
	if b.in.Figure != nil && string(*b.in.Figure) != "" {
		canon, ok := coreagent.CanonicalFigure(string(*b.in.Figure))
		if !ok {
			return out, createFieldRefusal(http.StatusBadRequest, "figure must be Robot, Man, Woman, Omnipus, or Monogram", "figure")
		}
		out.figure = canon
	}
	out.role = coreagent.DefaultRole
	if b.in.Role != nil && string(*b.in.Role) != "" {
		canon, ok := coreagent.CanonicalRole(string(*b.in.Role))
		if !ok {
			return out, createFieldRefusal(http.StatusBadRequest, "role must be one of the curated role slugs", "role")
		}
		out.role = canon
	}
	out.color = coreagent.DefaultColor
	if b.in.Color != nil && string(*b.in.Color) != "" {
		canon, ok := coreagent.CanonicalColor(string(*b.in.Color))
		if !ok {
			return out, createFieldRefusal(http.StatusBadRequest, "color must be one of the ten identity colours", "color")
		}
		out.color = canon
	}
	return out, nil
}

// buildBaseConfig validates the shared fields and builds the base persistent
// agent config.
func (b *agentCreateBuild) buildBaseConfig(name string) *agentCreateRefusalError {
	fields, refusal := b.validateCreateFields()
	if refusal != nil {
		return refusal
	}
	// #904 FR-006/FR-007/FR-014: an own tool-iteration limit must be within
	// 1..1000 and not above the global in force (D10). Re-checked under
	// configMu in persistAgentCreateLocked, which closes the window against
	// REST global writes (PUT /api/v1/performance holds configMu too).
	if b.in.MaxToolIterations != nil {
		if err := config.ValidateAgentMaxToolIterations(*b.in.MaxToolIterations,
			&b.a.agentLoop.GetConfig().Agents.Defaults); err != nil {
			return &agentCreateRefusalError{Status: http.StatusBadRequest, Msg: err.Error(), Field: maxToolIterationsFieldName}
		}
	}
	// Locked stays false for both custom and worker creates: only
	// SeedConfig-seeded agents are locked.
	b.ac = config.AgentConfig{
		ID:          uuid.New().String(),
		Name:        name,
		Description: fields.description,
		Figure:      fields.figure,
		Role:        fields.role,
		Color:       fields.color,
		Type:        b.createType,
	}
	if b.in.MaxToolIterations != nil {
		b.ac.MaxToolIterations = *b.in.MaxToolIterations
	}
	if b.in.ReasoningEffort != nil {
		b.ac.ReasoningEffort = *b.in.ReasoningEffort
	}
	if b.in.Model != nil && *b.in.Model != "" {
		b.ac.Model = &config.AgentModelConfig{Primary: *b.in.Model}
		// O3 two-field model: persist the explicit primary provider when supplied.
		if b.in.Provider != nil && strings.TrimSpace(*b.in.Provider) != "" {
			b.ac.Model.Provider = strings.TrimSpace(*b.in.Provider)
		}
	}
	b.ac.ModelParams = mergeAgentModelParams(nil, b.in.ModelParams)
	// AG-03 (#1440, r17 §7.5): the validated fallback chain is stored, not
	// only checked; an empty list stores none.
	if b.in.FallbackModels != nil && len(*b.in.FallbackModels) > 0 {
		b.ac.FallbackModels = fallbackModelsFromWire(*b.in.FallbackModels, nil)
	}
	if b.in.Skills != nil && len(*b.in.Skills) > 0 {
		b.ac.Skills = make([]string, len(*b.in.Skills))
		copy(b.ac.Skills, *b.in.Skills)
	}
	// The two forms of one setting refuse each other (rider #1914: the
	// refusal stops the create; nothing is stored).
	if b.in.MCPServers != nil || b.in.PolicyChanges != nil {
		if b.toolsCfgIn == nil {
			b.toolsCfgIn = &agentCreateToolsCfgInput{}
		}
		if b.in.MCPServers != nil {
			if b.toolsCfgIn.MCPSpecified {
				return createRefusal(http.StatusBadRequest, "mcp_servers and tools_cfg.mcp cannot both be supplied")
			}
			b.toolsCfgIn.MCPServers = *b.in.MCPServers
			b.toolsCfgIn.MCPSpecified = true
		}
	}
	b.soul = strings.TrimSpace(b.in.Soul)
	if b.in.PolicyChanges != nil {
		if b.toolsCfgIn != nil && b.toolsCfgIn.BuiltinPolicies != nil {
			return createRefusal(http.StatusBadRequest, "tool_policy_changes and tools_cfg.builtin cannot both be supplied")
		}
		b.policyChanges = b.in.PolicyChanges // applied after the server seed is constructed
	}
	return nil
}

// buildExecutor maps the worker runtime into AgentConfig.Subagents.Executor.
// A native Subagent always gets a native runtime. A subagent_3p always gets
// kind=external-cli: kind is server-derived, never read from the request.
func (b *agentCreateBuild) buildExecutor() *agentCreateRefusalError {
	if b.createType != config.AgentTypeWorker {
		return nil
	}
	if b.in.WireType != string(gen.AgentTypeSubagent3p) {
		b.ac.Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative}}
		return nil
	}
	executorIn := b.in.Executor
	if executorIn == nil {
		executorIn = &executorRequestInput{}
	}
	execCfg, errMsg := executorConfigFromRequest(string(config.ExecutorKindExternalCLI), executorIn.Cli)
	if errMsg != "" {
		return createRefusal(http.StatusBadRequest, errMsg)
	}
	// r17 §7.6 EX-01/EX-04: the same env_overrides and cli_args rules as
	// create_agent and the update merge (pkg/thirdpartycli).
	if executorIn.EnvOverrides != nil {
		if err := thirdpartycli.ValidateEnvOverrides(*executorIn.EnvOverrides); err != nil {
			return &agentCreateRefusalError{Status: http.StatusBadRequest, Msg: err.Error(), Field: "executor.env_overrides"}
		}
		execCfg.EnvOverrides = *executorIn.EnvOverrides
	}
	if executorIn.CliArgs != nil {
		if err := thirdpartycli.ValidateCLIArgs(*executorIn.CliArgs); err != nil {
			return &agentCreateRefusalError{Status: http.StatusBadRequest, Msg: err.Error(), Field: "executor.cli_args"}
		}
		execCfg.CLIArgs = *executorIn.CliArgs
	}
	b.ac.Subagents = &config.SubagentsConfig{Executor: execCfg}
	return nil
}

// buildToolConfig validates and builds the agent's builtin and MCP tool
// configuration.
func (b *agentCreateBuild) buildToolConfig() *agentCreateRefusalError {
	if b.toolsCfgIn != nil {
		configured := b.a.agentLoop.GetConfig().Tools.MCP.Servers
		for _, server := range b.toolsCfgIn.MCPServers {
			if _, ok := configured[server.ID]; !ok || server.ID == "" {
				return createRefusal(http.StatusUnprocessableEntity, fmt.Sprintf("MCP server %q is not configured", server.ID))
			}
			if err := config.ValidateAgentMCPServerBinding(config.AgentMCPServerBinding{ID: server.ID, Tools: server.Tools, ToolsSpecified: server.ToolsSpecified}); err != nil {
				return createRefusal(http.StatusUnprocessableEntity, err.Error())
			}
		}
	}
	// CLAUDE.md hard constraint 6: validate the CALLER'S OWN map first, before
	// the deny-seeded baseline below can paper over a gap (the merge backfills
	// every omitted tool from the seed, so the later coverage check could never
	// see a caller-side gap on this path). Runs only when the caller submitted
	// a builtin policy map; no tools_cfg at all falls through to the complete,
	// server-generated baseline.
	if b.toolsCfgIn != nil && b.toolsCfgIn.BuiltinPolicies != nil {
		if defects := validateSubmittedBuiltinToolPolicyMap(b.toolsCfgIn.BuiltinPolicies); !defects.Empty() {
			return createRefusal(http.StatusBadRequest, "tools_cfg.builtin.policies "+defects.String())
		}
	}
	// Channels FR-037 (security S4): an always-ask tool is refused as allow in
	// both the complete (tools_cfg) and sparse (tool_policy_changes) forms
	// before anything is built or persisted. ask and deny stay accepted.
	if b.toolsCfgIn != nil {
		if refusal := alwaysAskAllowRefusal(b.toolsCfgIn.BuiltinPolicies); refusal != nil {
			return refusal
		}
	}
	if b.policyChanges != nil {
		if refusal := alwaysAskAllowRefusal(b.policyChanges.Set); refusal != nil {
			return refusal
		}
	}
	// Seed the Main or Worker defaults (founder ruling, 2026-10-22), with every
	// static builtin explicitly classified. Worker restrictions apply BEFORE
	// caller overrides: they are creation defaults, not fixed role
	// restrictions. subagent_3p has no tools_cfg, so it keeps the Worker seed.
	baseCfg := coreagent.NewCustomAgentToolsCfg()
	if b.ac.Type == config.AgentTypeWorker {
		coreagent.RestrictForWorkerTier(baseCfg)
	}
	if b.toolsCfgIn != nil {
		builtin := config.AgentBuiltinToolsCfg{
			Policies: make(map[string]config.ToolPolicy, len(baseCfg.Builtin.Policies)),
		}
		for k, v := range baseCfg.Builtin.Policies {
			builtin.Policies[k] = v
		}
		// The caller's per-tool entry (exact name, never a wildcard) overrides
		// the corresponding seed entry.
		for k, v := range b.toolsCfgIn.BuiltinPolicies {
			builtin.Policies[k] = config.ToolPolicy(v)
		}
		b.ac.Tools = &config.AgentToolsCfg{Builtin: builtin}
		if b.toolsCfgIn.MCPSpecified {
			servers := make([]config.AgentMCPServerBinding, 0, len(b.toolsCfgIn.MCPServers))
			for _, s := range b.toolsCfgIn.MCPServers {
				servers = append(servers, config.AgentMCPServerBinding{ID: s.ID, Tools: s.Tools, ToolsSpecified: s.ToolsSpecified})
			}
			b.ac.Tools.MCP = config.AgentMCPToolsCfg{Servers: servers}
		}
	} else {
		b.ac.Tools = baseCfg
	}
	if b.policyChanges != nil {
		updated, err := agentmutation.ApplyToolPolicyChanges(b.ac.Tools.Builtin.Policies, *b.policyChanges, buildKnownBuiltinToolNames())
		if err != nil {
			return createRefusal(http.StatusBadRequest, err.Error())
		}
		b.ac.Tools.Builtin.Policies = updated
	}
	for name := range buildKnownBuiltinToolNames() {
		if _, exists := b.ac.Tools.Builtin.Policies[name]; !exists {
			b.ac.Tools.Builtin.Policies[name] = config.ToolPolicyDeny
		}
	}
	return nil
}

// persistAgentCreateLocked stores the plan: the #904 D10 re-check of
// max_tool_iterations against the global in force, then
// agentstore.Store.CreateState for the record and SOUL.md. A D10 refusal is a
// *requestRefusalError and a store failure a *configurationMutationError, as
// before the extraction. The caller MUST hold a.configMu.
func (a *restAPI) persistAgentCreateLocked(p *agentCreatePlan) (agentstore.MutationResult, error) {
	if err := a.recheckAgentMaxToolIterationsLocked(p); err != nil {
		return agentstore.MutationResult{
			PersistenceStatus: agentstore.PersistenceNone,
			ActivationStatus:  agentstore.ActivationNotAttempted,
		}, err
	}
	result, err := agentstore.New(a.homePath).CreateState(p.Agent.ID, &p.Agent, p.Soul)
	if err != nil {
		return result, &configurationMutationError{Result: result, Err: fmt.Errorf("create agent state: %w", err)}
	}
	return result, nil
}

// recheckAgentMaxToolIterationsLocked is the #904 D10 re-check of the plan's
// own max_tool_iterations against the global in force, as a
// *requestRefusalError. The caller MUST hold a.configMu.
func (a *restAPI) recheckAgentMaxToolIterationsLocked(p *agentCreatePlan) error {
	if p.Agent.MaxToolIterations <= 0 {
		return nil
	}
	if err := config.ValidateAgentMaxToolIterations(p.Agent.MaxToolIterations,
		&a.agentLoop.GetConfig().Agents.Defaults); err != nil {
		return &requestRefusalError{msg: err.Error(), field: maxToolIterationsFieldName}
	}
	return nil
}

// withToolPolicyCoverageCheck is the import-path sibling of
// withToolPolicyCoverageGuard: under a.configMu it runs
// validateCandidateToolPolicyCoverage against the live config, then persist.
// It never calls updateConfigJSONLocked, so config.json is not read,
// rewritten or reapplied. Gaps come back as *toolPolicyCoverageGapError and
// stop persist from running.
//
// It rejects every gap, as REST create does. A running gateway has none
// outside the new agent, because boot and reload fill the ceiling and abort
// on a gap.
func (a *restAPI) withToolPolicyCoverageCheck(mutate func(*config.Config), persist func() error) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	gaps, err := a.validateCandidateToolPolicyCoverage(a.agentLoop.GetConfig(), mutate)
	if err != nil {
		return fmt.Errorf("tool policy coverage check: %w", err)
	}
	if len(gaps) > 0 {
		return &toolPolicyCoverageGapError{Gaps: gaps}
	}
	return persist()
}

// createAgentWithoutConfigWrite is the import's coverage check plus persist
// (E2 + E4): withToolPolicyCoverageCheck with the plan's agent appended to the
// candidate roster and persistAgentCreateLocked as the persist step.
func (a *restAPI) createAgentWithoutConfigWrite(p *agentCreatePlan) (agentstore.MutationResult, error) {
	result := agentstore.MutationResult{
		PersistenceStatus: agentstore.PersistenceNone,
		ActivationStatus:  agentstore.ActivationNotAttempted,
	}
	err := a.withToolPolicyCoverageCheck(
		func(c *config.Config) { c.Agents.List = append(c.Agents.List, p.Agent) },
		func() error {
			var persistErr error
			result, persistErr = a.persistAgentCreateLocked(p)
			return persistErr
		})
	return result, err
}

// applyImportToolFields is FR-047: the create_agent tool's rule for the three
// fields REST create does not store. It runs agentmutation.ValidateFields
// first, then systools.ApplySharedConfigArgs. The args hold only the fields
// the package carries ("voice" when non-empty, "memory_enabled" and
// "context_window_override" when non-nil); model_params is never passed (C-9).
func applyImportToolFields(p *agentCreatePlan, voice string, memoryEnabled *bool, contextWindow *int) error {
	args := map[string]any{}
	if voice != "" {
		args["voice"] = voice
	}
	if memoryEnabled != nil {
		args["memory_enabled"] = *memoryEnabled
	}
	if contextWindow != nil {
		args["context_window_override"] = *contextWindow
	}
	if len(args) == 0 {
		return nil
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if err := agentmutation.ValidateFields(p.Agent, keys); err != nil {
		return err
	}
	return systools.ApplySharedConfigArgs(&p.Agent, args)
}
