// preset_model.go — #1880 model precedence for the person's preset parameters
// (Settings → Third-party agents, r17 §3.7). Founder decision Q-OC1 = A,
// 2026-11-19 (ADR-032 amendment): when the driver passes the worker's own
// model, a model option in the preset (--model, and -m on codex and opencode)
// is dropped too, so the CLI never receives two model options. When the
// driver passes no model of its own, the preset is left untouched. The rest
// of the preset is never filtered and keeps its place after the agent's own
// (filtered) cli_args.

package runner

import (
	"log/slog"
	"strings"
)

// presetModelPrecedenceReason is the reason reported for a model option
// dropped from the person's preset parameters.
const presetModelPrecedenceReason = "the worker's configured model takes precedence: Omnipus gives the CLI the worker's own model itself, so a model option in the standard parameters (Settings → Third-party agents) is ignored for this worker"

// driverPassesModel reports whether the driver for cli (the filter key:
// "claude", "codex", "opencode") adds its own model option for model. It
// mirrors each driver's buildArgs: claude and codex pass any non-empty model;
// opencode passes it only when it is shaped like provider/model.
func driverPassesModel(cli, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	if cli == "opencode" {
		return looksLikeProviderModel(model)
	}
	return true
}

// presetModelFlags is modelPrecedenceCLIFlagsByCLI for cli, re-worded for the
// preset (the same options, a reason naming the standard parameters).
func presetModelFlags(cli string) []dangerousCLIFlag {
	src := modelPrecedenceCLIFlagsByCLI[cli]
	out := make([]dangerousCLIFlag, 0, len(src))
	for _, f := range src {
		f.reason = presetModelPrecedenceReason
		out = append(out, f)
	}
	return out
}

// filterPresetArgs drops the preset's model options when the driver passes
// its own model for cli, and returns the preset unchanged otherwise. It is the
// one decision behind every driver's buildArgs, the command preview and the
// run and smoke-test audits.
func filterPresetArgs(cli, model string, preset []string) (kept []string, dropped []droppedCLIArg) {
	if !driverPassesModel(cli, model) {
		return preset, nil
	}
	return filterCLIArgsAgainst(presetModelFlags(cli), preset)
}

// presetArgsForBuild is filterPresetArgs for a driver's buildArgs: it logs one
// WARN per dropped option (its name only — the preset is the person's text and
// is never written to the log in full) and returns the preset to append.
func presetArgsForBuild(logPrefix, cli string, opts RunOptions) []string {
	kept, dropped := filterPresetArgs(cli, opts.Model, opts.PresetArgs)
	for _, d := range dropped {
		name, _, _ := splitFlagArg(strings.Fields(d.flag)[0])
		slog.Warn(logPrefix+": standard-parameters model option dropped — the worker's configured model takes precedence",
			"run_id", opts.RunID, "cli", cli, "flag", name)
	}
	return kept
}

// PresetArgsDroppedAtRun returns the preset tokens a run of cli (the filter
// key, see FilterKey) with the given configured model drops, with reasons:
// the model options when the driver passes its own model, nothing otherwise.
// Exported for the command preview and the run and smoke-test audits, so
// they report the same decision the driver's buildArgs makes.
func PresetArgsDroppedAtRun(cli, model string, preset []string) []DroppedCLIArg {
	_, detailed := filterPresetArgs(cli, model, preset)
	if len(detailed) == 0 {
		return nil
	}
	out := make([]DroppedCLIArg, len(detailed))
	for i, d := range detailed {
		out[i] = DroppedCLIArg{Flag: d.flag, Reason: d.reason}
	}
	return out
}
