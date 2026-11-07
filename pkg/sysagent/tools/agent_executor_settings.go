// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

// agent_executor_settings.go — the external worker's own settings on the
// agent tools (tool-parity spec r17 §3.8, §3.9, AG-10..AG-13, EX-01..EX-07).
// env_overrides and cli_args are checked with the same pkg/thirdpartycli
// rules REST uses; the result block is the generated
// AgentExecutorSettingsResult and never carries an environment value.

import (
	"fmt"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// nativeRuntimeRefusalText is the r17 §3.9 reason the executor settings are
// not editable on a native agent.
const nativeRuntimeRefusalText = thirdpartycli.NativeRuntimeRefusalText

// executorInvalid wraps a refusal so the model sees the exact rule text.
func executorInvalid(message string) *tools.ToolResult {
	return tools.ErrorResult(errorJSON("INVALID_INPUT", message, ""))
}

// decodeEnvOverrides reads an env_overrides argument: an object whose
// values are all strings. present is false when the argument is absent.
func decodeEnvOverrides(args map[string]any) (env map[string]string, present bool, err error) {
	raw, present := args["env_overrides"]
	if !present {
		return nil, false, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, true, fmt.Errorf("env_overrides must be an object of names to string values")
	}
	env = make(map[string]string, len(obj))
	for name, v := range obj {
		s, ok := v.(string)
		if !ok {
			return nil, true, fmt.Errorf("env_overrides value for %q must be a string", name)
		}
		env[name] = s
	}
	return env, true, nil
}

// decodeCLIArgs reads a cli_args argument (one string), checked against the
// EX-04 length limit. present is false when the argument is absent.
func decodeCLIArgs(args map[string]any) (value string, present bool, err error) {
	raw, present := args["cli_args"]
	if !present {
		return "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("cli_args must be a string")
	}
	if err := thirdpartycli.ValidateCLIArgs(s); err != nil {
		return "", true, err
	}
	return s, true, nil
}

// validateExecutorSettings checks create_agent's env_overrides and cli_args
// before any write: refused on a native agent, otherwise validated with the
// REST rules (EX-01, EX-04).
func (ac *agentCreateToolExecute) validateExecutorSettings() *tools.ToolResult {
	_, envSent := ac.args["env_overrides"]
	_, argsSent := ac.args["cli_args"]
	if !envSent && !argsSent {
		return nil
	}
	if ac.agentType != "subagent_3p" {
		return executorInvalid(nativeRuntimeRefusalText)
	}
	env, _, err := decodeEnvOverrides(ac.args)
	if err != nil {
		return executorInvalid(err.Error())
	}
	if err = thirdpartycli.ValidateEnvOverrides(env); err != nil {
		return executorInvalid(err.Error())
	}
	cliArgs, argsPresent, err := decodeCLIArgs(ac.args)
	if err != nil {
		return executorInvalid(err.Error())
	}
	if len(env) > 0 {
		ac.execEnv = env
	}
	if argsPresent {
		ac.execCLIArgs = &cliArgs
	}
	return nil
}

// addExecutorResult adds the external worker's program status and, when
// executor settings were supplied, the settings outcome to a create_agent
// result (r17 §3.8: runnable plus program_warning; AG-12/AG-13).
func (ac *agentCreateToolExecute) addExecutorResult(result map[string]any) {
	warning := executorProgramWarning(ac.t.deps, ac.execCLI)
	runnable := warning == ""
	block := gen.AgentExecutorSettingsResult{Runnable: &runnable}
	if warning != "" {
		block.ProgramWarning = &warning
	}
	if _, sent := ac.args["env_overrides"]; sent {
		setEnvOutcome(&block, sortedEnvNames(ac.execEnv), nil, nil, nil)
	}
	if ac.execCLIArgs != nil {
		setCLIArgsOutcome(&block, ac.execCLI, *ac.execCLIArgs)
	}
	result["runnable"] = runnable
	if warning != "" {
		result["program_warning"] = warning
	}
	result["executor"] = block
}

// executorProgramWarning is the exact text a run of an external worker on
// cli would be refused with right now, read from the live Settings section
// (r17 §3.8), or "" when a run would start. An unreadable live config
// reports the no-path text: no run can start without a Settings path.
func executorProgramWarning(d *Deps, cli string) string {
	if !liveConfigAvailable(d) {
		return thirdpartycli.ProgramWarning(cli, "")
	}
	cfg := d.GetCfg()
	row := cfg.Sandbox.ThirdPartyCLI(cli)
	return thirdpartycli.ProgramWarning(cli, row.Path, thirdpartycli.AgentWritableRoots(d.Home, cfg)...)
}

// sortedEnvNames returns the names of env in sorted order (never a value).
func sortedEnvNames(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// newOf allocates a zero value of the type ptr points to. It lets the result
// fill the generated type's anonymous nested blocks without restating their
// wire shape (Hard Constraint #8).
func newOf[T any](_ *T) *T { return new(T) }

// appendZero appends a zero element to s and returns the new slice and a
// pointer to the appended element.
func appendZero[E any](s []E) ([]E, *E) {
	var zero E
	s = append(s, zero)
	return s, &s[len(s)-1]
}

// nonNilNames turns a nil list into [] so every outcome list is present.
func nonNilNames(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// setEnvOutcome fills the env block: four name lists, each [] when empty.
func setEnvOutcome(block *gen.AgentExecutorSettingsResult, added, replaced, removed, absentRemoved []string) {
	block.Env = newOf(block.Env)
	block.Env.Added = nonNilNames(added)
	block.Env.Replaced = nonNilNames(replaced)
	block.Env.Removed = nonNilNames(removed)
	block.Env.AbsentRemoved = nonNilNames(absentRemoved)
}

// setCLIArgsOutcome tokenises the stored cli_args exactly as a run does and
// names the tokens the run-time safety filter will drop (EX-04, FR-065).
func setCLIArgsOutcome(block *gen.AgentExecutorSettingsResult, cli, raw string) {
	block.CliArgs = newOf(block.CliArgs)
	block.CliArgs.Argv = nonNilNames(runner.ParseCLIArgs(raw, "agent-tool"))
	_, dropped := runner.FilterDangerousCLIArgsDetailed(runner.FilterKey(cli), block.CliArgs.Argv)
	list := emptyLike(block.CliArgs.DroppedAtRun, len(dropped))
	for _, d := range dropped {
		next, slot := appendZero(list)
		slot.Flag, slot.Reason = d.Flag, d.Reason
		list = next
	}
	block.CliArgs.DroppedAtRun = list
}

// emptyLike returns an empty, non-nil slice of s's element type with room
// for n elements, so an empty outcome list encodes as [] rather than null.
func emptyLike[E any](_ []E, n int) []E { return make([]E, 0, n) }

// executorUpdateArgNames are update_agent's executor settings arguments
// (r17 §3.9 ExecutorConfigUpdate): kind, cli and cli_path are not among them,
// so the generic unknown-argument check refuses those by name.
var executorUpdateArgNames = []string{"env_overrides", "env_overrides_remove", "cli_args"}

// executorUpdateSent reports whether args carries any executor setting.
func executorUpdateSent(args map[string]any) bool {
	for _, name := range executorUpdateArgNames {
		if _, ok := args[name]; ok {
			return true
		}
	}
	return false
}

// executorRecordFields maps the executor arguments onto the record field
// "executor" the agent role matrix (agentmutation.ValidateFields) knows.
func executorRecordFields(fields []string) []string {
	out := make([]string, 0, len(fields))
	executor := false
	for _, f := range fields {
		switch f {
		case "env_overrides", "env_overrides_remove", "cli_args":
			if !executor {
				out = append(out, "executor")
				executor = true
			}
		default:
			out = append(out, f)
		}
	}
	return out
}

// executorRefusalError is an executor-settings refusal whose message is a
// rule text returned to the model unchanged.
type executorRefusalError string

func (e executorRefusalError) Error() string { return string(e) }

// decodeEnvRemove reads env_overrides_remove: a list of names.
func decodeEnvRemove(args map[string]any) (names []string, present bool, err error) {
	raw, present := args["env_overrides_remove"]
	if !present {
		return nil, false, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, true, fmt.Errorf("env_overrides_remove must be a list of names")
	}
	names = make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, true, fmt.Errorf("env_overrides_remove must be a list of names")
		}
		names = append(names, s)
	}
	return names, true, nil
}

// applyExecutorUpdate applies update_agent's executor settings to a, inside
// the store mutation on fresh state (r17 EX-02..EX-04, FR-061): the env merge
// and remove list through the shared thirdpartycli.MergeEnvOverrides, and
// cli_args replaced. Only an external-CLI worker has a runtime environment.
// sent is false (and the block nil) when no executor setting was sent.
func applyExecutorUpdate(a *config.AgentConfig, args map[string]any) (block *gen.AgentExecutorSettingsResult, sent bool, err error) {
	if !executorUpdateSent(args) {
		return nil, false, nil
	}
	if !a.IsExternalCLIWorker() {
		return nil, true, executorRefusalError(nativeRuntimeRefusalText)
	}
	ex := a.Subagents.Executor
	set, setSent, err := decodeEnvOverrides(args)
	if err != nil {
		return nil, true, executorRefusalError(err.Error())
	}
	remove, removeSent, err := decodeEnvRemove(args)
	if err != nil {
		return nil, true, executorRefusalError(err.Error())
	}
	cliArgs, argsSent, err := decodeCLIArgs(args)
	if err != nil {
		return nil, true, executorRefusalError(err.Error())
	}
	block = &gen.AgentExecutorSettingsResult{}
	if setSent || removeSent {
		merged, out, mergeErr := thirdpartycli.MergeEnvOverrides(ex.EnvOverrides, set, remove)
		if mergeErr != nil {
			return nil, true, executorRefusalError(mergeErr.Error())
		}
		ex.EnvOverrides = merged
		setEnvOutcome(block, out.Added, out.Replaced, out.Removed, out.AbsentRemoved)
	}
	if argsSent {
		ex.CLIArgs = cliArgs
		setCLIArgsOutcome(block, ex.CLI, cliArgs)
	}
	return block, true, nil
}
