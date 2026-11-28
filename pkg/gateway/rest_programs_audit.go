// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"slices"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/programs"
)

// Connected-programs audit events (FR-021). Every row is value-free: it names
// the program, agent or channel and the change, never a token.
const (
	auditProgramConnected      = "program.connected"
	auditProgramReplaced       = "program.replaced"
	auditProgramCheck          = "program.check"
	auditProgramCheckSupersede = "program.check_superseded"
	auditProgramCheckResult    = "program.check_result"
	auditProgramDisconnected   = "program.disconnected"
	auditProgramSwitchChanged  = "program.agent_switch_changed"
	auditProgramSettingChanged = "program.setting_changed"
	auditProgramCustomChanged  = "program.custom_entry_changed"
	auditProgramCardRefused    = "program.card_refused"
)

// auditProgram writes one connected-programs audit row. The auditor is read
// at call time, so a sink installed on the programs service before an
// auditor was wired still writes to it. A failed write is logged, never
// returned: the change it records has already been made.
func (a *restAPI) auditProgram(event, decision, agentID string, details map[string]any) {
	if a.auditor == nil {
		return
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:    event,
		Decision: decision,
		AgentID:  agentID,
		Details:  details,
	}); err != nil {
		logsafeWarn("audit write failed", "event", event, "error", err)
	}
}

// auditProgramSuperseded is the programs service's audit sink for a check
// whose reservation was replaced before it finished (FR-021, FR-047).
func (a *restAPI) auditProgramSuperseded(programID string, generation int64) {
	a.auditProgram(auditProgramCheckSupersede, audit.DecisionAllow, "", map[string]any{
		"program_id": programID, "generation": generation, "outcome": "superseded",
	})
}

// auditProgramCheckResult is the programs service's audit sink for a finished
// check (FR-021, ruling H50): one row per published result, value-free. A
// superseded check writes program.check_superseded instead, never this.
func (a *restAPI) auditProgramCheckResult(ev programs.CheckResultEvent) {
	a.auditProgram(auditProgramCheckResult, audit.DecisionAllow, "", map[string]any{
		"program_id": ev.ProgramID, "generation": ev.Generation, "check_revision": ev.CheckRevision,
		"outcome": ev.Outcome, "reason": ev.Reason,
	})
}

// auditAgentProgramsChange records a changed program switch list for one
// agent. An update that leaves the list as it was writes nothing.
func (a *restAPI) auditAgentProgramsChange(agentID string, before, after []string) {
	if slices.Equal(before, after) {
		return
	}
	var on, off []string
	for _, id := range after {
		if !slices.Contains(before, id) {
			on = append(on, id)
		}
	}
	for _, id := range before {
		if !slices.Contains(after, id) {
			off = append(off, id)
		}
	}
	a.auditProgram(auditProgramSwitchChanged, audit.DecisionAllow, agentID, map[string]any{
		"agent_id": agentID, "programs": after, "switched_on": on, "switched_off": off,
	})
}
