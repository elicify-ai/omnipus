// stub_for_red.go — compile-only RED stubs for epic #1094 slice S1a task T-3.
//
// DELETE when GREEN lands. Every symbol here returns errors.New("not
// implemented") or a zero value so the RED tests in agent_create_core_red_test.go
// fail on an assertion or a not-implemented error — never on an undefined
// symbol. The shapes are the architect's sign-off (ARCH-SIGNOFF-T3-T4.md, T-3
// block and adaptations C-1…C-9) against spec revision 7
// (docs/internal/specs/export-import-s1-spec.md): import's persist step calls
// CreatePendingState (with AGENT.md) instead of CreateState plus
// MutateStateWithAgentMD, so this stub declares the shared create core only —
// the pending record lives in pkg/agentstore's own stub.
package gateway

import (
	"errors"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// agentCreateInput is the shared create function's domain input (sign-off
// T-3). It is NOT a REST request type; generated leaf component types
// (gen.FallbackModel, gen.AgentColor/Figure/Role) are shared schema parts.
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

// agentCreateRefusalError is a create-rule refusal, written by the REST adapter
// exactly as today: Code set -> jsonErrCode; else Field set -> jsonErrField;
// else jsonErr.
type agentCreateRefusalError struct {
	Status int
	Msg    string
	Code   string // only for the name rules (agent_name_too_long, reserved_agent_name)
	Field  string // e.g. "max_tool_iterations", "executor.env_overrides"
}

func (r *agentCreateRefusalError) Error() string { return r.Msg }

func (r *agentCreateRefusalError) write(w http.ResponseWriter) { //nolint:unused // RED stub: GREEN's REST adapter calls this
	// RED stub: writes nothing on purpose. GREEN reproduces today's
	// jsonErr/jsonErrField/jsonErrCode call with the same status, text, code
	// and field.
}

// agentCreatePlan is a fully validated, built, NOT yet persisted agent.
type agentCreatePlan struct {
	Agent config.AgentConfig // ID = fresh uuid.New(), tools seeded and merged
	Soul  string             // strings.TrimSpace(input.Soul)
}

// toolPolicyCoverageGapError carries the coverage gaps the import-path check
// found (sign-off: "Gaps come back as *toolPolicyCoverageGapError{Gaps}").
type toolPolicyCoverageGapError struct {
	Gaps []config.CoverageGap
}

func (e *toolPolicyCoverageGapError) Error() string { return "not implemented" }

// buildAgentCreate runs every create rule that does not need configMu. It has
// no side effects (no disk or config writes), so upload's dry run (FR-020) can
// call it.
func (a *restAPI) buildAgentCreate(in agentCreateInput) (agentCreatePlan, *agentCreateRefusalError) {
	return agentCreatePlan{}, &agentCreateRefusalError{Msg: "not implemented"}
}

// persistAgentCreateLocked is the body of today's persist closure: the D10
// re-check of max_tool_iterations, then agentstore.New(a.homePath).CreateState
// (import: CreatePendingState). It returns the same error types as today
// (*requestRefusalError, *configurationMutationError). The caller MUST hold
// a.configMu.
func (a *restAPI) persistAgentCreateLocked(p *agentCreatePlan) (agentstore.MutationResult, error) {
	return agentstore.MutationResult{}, errors.New("not implemented")
}

// withToolPolicyCoverageCheck is the import-path sibling of
// withToolPolicyCoverageGuard: under a.configMu it runs the existing
// validateCandidateToolPolicyCoverage, then persist(). It never calls
// updateConfigJSONLocked, so config.json is not read, rewritten or reapplied.
func (a *restAPI) withToolPolicyCoverageCheck(mutate func(*config.Config), persist func() error) error { //nolint:unused // RED stub: GREEN's createAgentWithoutConfigWrite calls this
	return errors.New("not implemented")
}

// createAgentWithoutConfigWrite is import's E2+E4: withToolPolicyCoverageCheck
// with mutate = append(p.Agent) and persist = persistAgentCreateLocked(p).
func (a *restAPI) createAgentWithoutConfigWrite(p *agentCreatePlan) (agentstore.MutationResult, error) {
	return agentstore.MutationResult{}, errors.New("not implemented")
}

// applyImportToolFields is FR-047: the create_agent tool's rule for the three
// fields REST create does not store. It runs agentmutation.ValidateFields
// first, then systools.ApplySharedConfigArgs. args holds ONLY "voice",
// "memory_enabled", "context_window_override", and only when the package
// carries a value. It never passes model_params (C-9).
func applyImportToolFields(p *agentCreatePlan, voice string, memoryEnabled *bool, contextWindow *int) error {
	return errors.New("not implemented")
}
