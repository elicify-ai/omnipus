// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
)

// resolveRunProgram applies r17 §3.6 before anything is dispatched: the
// program is only the Settings clis.<cli>.path, re-checked with
// ValidateProgramPath; the run execs the returned resolved file. With no
// saved path the run is refused with NoPath, with a failing path with
// CheckFailed. The old per-agent cli_path is never read and there is no PATH
// fallback. The person's preset parameters (§3.7) are read from the same
// Settings row. A refusal is the error the caller returns as written.
func (ed *runExternalCLISubTurnState) resolveRunProgram(cli string) error {
	row, refusal := ed.al.runProgramFor(cli)
	if refusal != "" {
		return ed.reportProgramRefusal(refusal)
	}
	resolved := row.resolved
	ed.programEntered = row.settings.Path
	ed.programResolved = resolved
	ed.presetText = row.settings.PresetArgs
	ed.presetArgs = runner.ParseCLIArgs(row.settings.PresetArgs, ed.runID)
	return nil
}

// runProgram is the Settings row for one CLI and the checked file it runs.
type runProgram struct {
	settings config.ThirdPartyCLIConfig
	resolved string
}

// runProgramFor applies r17 §3.6 steps 1–3 for cli against the live config:
// the Settings clis.<cli>.path only, re-checked; refusal is the NoPath or
// CheckFailed text, "" when the program may run. Shared by the spawn
// (resolveRunProgram) and the delegation pre-flight (SteerLauncher.Launch).
func (al *AgentLoop) runProgramFor(cli string) (runProgram, string) {
	cfg := al.GetConfig()
	if cfg == nil {
		return runProgram{}, thirdpartycli.NoPathText(cli)
	}
	row := cfg.Sandbox.ThirdPartyCLI(cli)
	resolved, refusal := thirdpartycli.RunProgram(cli, row.Path, thirdpartycli.AgentWritableRoots(omnipusHome(), cfg)...)
	if refusal != "" {
		return runProgram{}, refusal
	}
	return runProgram{settings: row, resolved: resolved}, ""
}

// reportProgramRefusal records the refusal on the child session like the
// workspace refusal does and returns it as an Omnipus-authored error, so a
// task shows the text as its result (r17 §3.6 step 5). A delegation is
// refused earlier, at launch, with the same text as the parent's tool error
// (SteerLauncher.Launch); reaching this for a delegated child means the
// Settings program changed between launch and spawn.
func (ed *runExternalCLISubTurnState) reportProgramRefusal(text string) error {
	refusal := &curatedTurnError{text: text}
	llm := TranslateTurnError(refusal)
	chatID := ed.childTS.chatID
	if chatID == "" {
		chatID = ed.childTS.opts.ChatID
	}
	ed.al.emitEvent(
		EventKindError,
		ed.childTS.eventMeta("runTurn", "turn.error"),
		ErrorPayload{
			Stage:     "external_cli",
			ChatID:    chatID,
			SessionID: string(ed.childTS.routingSessionID),
			Code:      string(llm.Code),
			Message:   text,
		},
	)
	ed.childTS.appendClassifiedError(EventKindError.String(), "external_cli", llm)
	return refusal
}

// auditRunSpawn writes the external_cli.run audit entry for one spawn
// (r17 §3.10): agent, CLI, entered and resolved program, the agent flags the
// filter drops, the preset as typed when set, and
// filter_bypassed_by_person_preset with the filter's reasons whenever a
// preset token is one the filter would drop from an agent. Write failures
// are logged by audit.Emit.
func (ed *runExternalCLISubTurnState) auditRunSpawn(ctx context.Context, cli string) {
	key := runner.FilterKey(cli)
	_, droppedAgent := runner.FilterDangerousCLIArgsDetailed(key, ed.cliArgs)
	agentFlags := make([]string, 0, len(droppedAgent))
	for _, d := range droppedAgent {
		agentFlags = append(agentFlags, d.Flag)
	}
	fields := map[string]any{
		"agent_id":            ed.agent.ID,
		"cli":                 cli,
		"program_entered":     ed.programEntered,
		"program_resolved":    ed.programResolved,
		"agent_flags_dropped": agentFlags,
	}
	if ed.presetText != "" {
		fields["preset_args"] = ed.presetText
		if _, bypassed := runner.FilterDangerousCLIArgsDetailed(key, ed.presetArgs); len(bypassed) > 0 {
			reasons := make([]string, 0, len(bypassed))
			for _, d := range bypassed {
				reasons = append(reasons, d.Flag+": "+d.Reason)
			}
			fields["filter_bypassed_by_person_preset"] = true
			fields["filter_reason"] = reasons
		}
	}
	audit.Emit(ctx, ed.al.AuditLogger(), audit.EventExternalCLIRun, audit.SeverityInfo, fields)
}
