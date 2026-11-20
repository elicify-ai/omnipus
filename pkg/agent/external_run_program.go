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
// thirdpartycli.CheckProgramPath; the run execs the returned resolved file.
// With no saved path the run is refused with NoPath, with a failing path with
// CheckFailed. The old per-agent cli_path is never read and there is no PATH
// fallback. The person's preset parameters (§3.7) are read from the same
// Settings row. A refusal is the error the caller returns as written.
func (ed *runExternalCLISubTurnState) resolveRunProgram(cli string) error {
	row, refusal := ed.al.runProgramFor(cli)
	if refusal != "" {
		return ed.reportProgramRefusal(cli, refusal)
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
	writable, rootsErr := thirdpartycli.AgentWritableRoots(omnipusHome(), cfg)
	resolved, refusal := thirdpartycli.RunProgram(cli, row.Path, writable, rootsErr)
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
//
// #1857 C8 (§13.8.5): the curated refusal travels on its own typed code,
// external_cli_not_found; its text stays the error's text.
// The live frame carries the rendered catalogue sentence (llm.Message), the
// same text the transcript saves: the curated text is not a catalogue
// sentence, so WireLLMError would demote it to the generic sentence
// (gate finding sfh F1). The returned error keeps the curated text for task
// surfaces (known limit F9).
func (ed *runExternalCLISubTurnState) reportProgramRefusal(cli, text string) error {
	refusal := newExternalCLIRefusal(CodeExternalCLINotFound, "", cli, &curatedTurnError{text: text})
	llm := TranslateTurnError(refusal)
	// The frame carries the same rendered C8 sentence the transcript saves
	// (gate finding sfh F1, above) and addresses the run's OWN session, as
	// reportWorkspaceRefusal does (#1895, ADR-091 D1).
	ed.al.emitExternalErrorFrame(ed.childTS, "external_cli", llm, "")
	ed.childTS.appendClassifiedError(EventKindError.String(), "external_cli", llm)
	return refusal
}

// auditRunSpawn writes the external_cli.run audit entry for one spawn
// (r17 §3.10): agent, CLI, entered and resolved program, the agent flags the
// filter drops, the preset through thirdpartycli.PresetAuditFilter and
// thirdpartycli.PresetUseNote when set, preset_flags_dropped (through the same
// filter) when the preset's model option is dropped because the driver passes
// the worker's own model (#1880), and filter_bypassed_by_person_preset with the
// filter's reasons, through the same filter, whenever
// a preset token is one the safety denylist would drop from an agent (a model
// option is not a safety bypass, #1880). Write failures
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
		filter := thirdpartycli.PresetAuditFilter(ed.al.GetConfig())
		fields["preset_args"] = filter(ed.presetText)
		fields["note"] = thirdpartycli.PresetUseNote
		// The preset's model option, dropped when the driver passes the
		// worker's own model (#1880) — the same decision as buildArgs.
		if droppedPreset := runner.PresetArgsDroppedAtRun(key, ed.agentModel, ed.presetArgs); len(droppedPreset) > 0 {
			presetFlags := make([]string, 0, len(droppedPreset))
			for _, d := range droppedPreset {
				presetFlags = append(presetFlags, filter(d.Flag))
			}
			fields["preset_flags_dropped"] = presetFlags
		}
		// The safety denylist only: a preset model option is not a safety
		// bypass (#1880 model precedence applies to Additional CLI arguments).
		if _, bypassed := runner.SafetyCLIArgsDetailed(key, ed.presetArgs); len(bypassed) > 0 {
			reasons := make([]string, 0, len(bypassed))
			for _, d := range bypassed {
				reasons = append(reasons, filter(d.Flag+": "+d.Reason))
			}
			fields["filter_bypassed_by_person_preset"] = true
			fields["filter_reason"] = reasons
		}
	}
	audit.Emit(ctx, ed.al.AuditLogger(), audit.EventExternalCLIRun, audit.SeverityInfo, fields)
}
