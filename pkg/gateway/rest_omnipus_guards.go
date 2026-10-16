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
)

// Machine-readable codes for the refusals below (ErrorResponse.code).
const (
	codeReservedAgentName  = "reserved_agent_name"
	codeOmnipusPerAgentAsk = "omnipus_per_agent_ask"
	codeOmnipusMembership  = "omnipus_default_workspace_only"
	codeAgentNameTooLong   = "agent_name_too_long"
)

// refuseReservedAgentName answers 400 when name is longer than the contract's
// 100 characters (#1075), else 409 "the name Omnipus is reserved" when name
// (trimmed, case-insensitive) is the system agent's name (FR-002, §6.2), and
// reports whether it did. A similar name such as "Omnipus Helper" passes.
func refuseReservedAgentName(w http.ResponseWriter, name string) bool {
	if coreagent.AgentNameTooLong(name) {
		// #1075: the read schema caps the name at 100 characters; a longer one
		// is refused here (validation on or off) so it is never stored.
		jsonErrCode(w, http.StatusBadRequest, coreagent.AgentNameTooLongRefusal, codeAgentNameTooLong)
		return true
	}
	if !coreagent.IsReservedAgentName(name) {
		return false
	}
	jsonErrCode(w, http.StatusConflict, coreagent.ReservedNameRefusal, codeReservedAgentName)
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
