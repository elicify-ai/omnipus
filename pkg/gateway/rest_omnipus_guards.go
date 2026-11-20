// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_omnipus_guards.go: the REST refusals the Omnipus system agent spec
// requires on every agent and workspace write path (FR-002, FR-003, FR-022).
// Each runs before any write, so a refusal changes nothing.

package gateway

import (
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Machine-readable codes for the refusals below (ErrorResponse.code).
const (
	codeReservedAgentName  = "reserved_agent_name"
	codeOmnipusPerAgentAsk = "omnipus_per_agent_ask"
	codeOmnipusMembership  = "omnipus_default_workspace_only"
	codeAgentNameTooLong   = "agent_name_too_long"
)

// reservedAgentNameRefusal is the name rule shared by create and update: 400
// with agent_name_too_long when name is longer than the contract's 100
// characters (#1075; the read schema caps it, so a longer one is never
// stored), else 409 "the name Omnipus is reserved" when name (trimmed,
// case-insensitive) is the system agent's name (FR-002, §6.2), else nil. A
// similar name such as "Omnipus Helper" passes.
func reservedAgentNameRefusal(name string) *agentCreateRefusalError {
	if coreagent.AgentNameTooLong(name) {
		return &agentCreateRefusalError{Status: http.StatusBadRequest, Msg: coreagent.AgentNameTooLongRefusal, Code: codeAgentNameTooLong}
	}
	if !coreagent.IsReservedAgentName(name) {
		return nil
	}
	return &agentCreateRefusalError{Status: http.StatusConflict, Msg: coreagent.ReservedNameRefusal, Code: codeReservedAgentName}
}

// refuseReservedAgentName writes reservedAgentNameRefusal's answer, if any,
// and reports whether it did.
func refuseReservedAgentName(w http.ResponseWriter, name string) bool {
	refusal := reservedAgentNameRefusal(name)
	if refusal == nil {
		return false
	}
	refusal.write(w)
	return true
}

// refuseOmnipusPerAgentAsk answers 422 when agentID is Omnipus and policies —
// the per-agent policy values this request would store — contain "ask"
// (FR-022, §6.2), and reports whether it did. "deny" is accepted.
func refuseOmnipusPerAgentAsk(w http.ResponseWriter, agentID string, policies map[string]config.ToolPolicy) bool {
	if _, bad := coreagent.OmnipusPerAgentAskViolation(agentID, policies); !bad {
		return false
	}
	jsonErrCode(w, http.StatusUnprocessableEntity, coreagent.OmnipusPerAgentAskRefusal, codeOmnipusPerAgentAsk)
	return true
}

// alwaysAskAllowRefusal is 400 with the always-ask text when policies (the
// policy values a request would store, global or per agent) set an always-ask
// tool to allow (channels-spec-r8 FR-037, T-10 rows 15-16), else nil. ask and
// deny pass; for Omnipus the per-agent ask refusal (refuseOmnipusPerAgentAsk)
// still applies after this check.
func alwaysAskAllowRefusal[V ~string](policies map[string]V) *agentCreateRefusalError {
	name, bad := tools.AlwaysAskAllowViolation(policies)
	if !bad {
		return nil
	}
	return &agentCreateRefusalError{Status: http.StatusBadRequest, Msg: tools.AlwaysAskAllowRefusal(name)}
}

// refuseAlwaysAskAllow writes alwaysAskAllowRefusal's answer, if any, and
// reports whether it did.
func refuseAlwaysAskAllow[V ~string](w http.ResponseWriter, policies map[string]V) bool {
	refusal := alwaysAskAllowRefusal(policies)
	if refusal == nil {
		return false
	}
	refusal.write(w)
	return true
}

// refuseOmnipusMembership answers 422 when a workspace team change would put
// Omnipus outside the default workspace or take it out of the default
// workspace (FR-003, §6.2), and reports whether it did.
func refuseOmnipusMembership(w http.ResponseWriter, isDefault bool, currentTeam, newTeam []string) bool {
	if !coreagent.OmnipusMembershipRefusal(isDefault, currentTeam, newTeam) {
		return false
	}
	jsonErrCode(w, http.StatusUnprocessableEntity, coreagent.OmnipusMembershipRefusalText, codeOmnipusMembership)
	return true
}

// policyMapOf converts any string-valued policy map (a generated wire map or a
// plain map[string]string) into the config policy type the coreagent rules
// take.
func policyMapOf[V ~string](in map[string]V) map[string]config.ToolPolicy {
	out := make(map[string]config.ToolPolicy, len(in))
	for name, value := range in {
		out[name] = config.ToolPolicy(value)
	}
	return out
}
