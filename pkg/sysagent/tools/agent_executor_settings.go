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
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// nativeRuntimeRefusalText is the r17 §3.9 reason the executor settings are
// not editable on a native agent.
const nativeRuntimeRefusalText = "Only external-CLI workers have a runtime environment."

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
// EX-04 length limit.
func decodeCLIArgs(args map[string]any) (*string, error) {
	raw, present := args["cli_args"]
	if !present {
		return nil, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("cli_args must be a string")
	}
	if err := thirdpartycli.ValidateCLIArgs(s); err != nil {
		return nil, err
	}
	return &s, nil
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
	if err := thirdpartycli.ValidateEnvOverrides(env); err != nil {
		return executorInvalid(err.Error())
	}
	cliArgs, err := decodeCLIArgs(ac.args)
	if err != nil {
		return executorInvalid(err.Error())
	}
	if len(env) > 0 {
		ac.execEnv = env
	}
	ac.execCLIArgs = cliArgs
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
