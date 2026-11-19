package gateway

import (
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q-OC1=A (2026-11-19): the preview must disclose a preset model option
// removed in favour of an actually emitted Omnipus model. Without that model,
// the preset remains untouched; worker cli_args still drop model options.
func TestOpenCodePresetModelPrecedence_PreviewDisclosesPresetConflictsOnlyWhenOwnModelIsPassed(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	for _, cli := range []struct {
		kind                           gen.ExternalCliTool
		model, rawModelOptions, bypass string
		modelArgs, models, dropped     []string
	}{
		{gen.ExternalCliToolClaudeCode, "sonnet", `--model "person model one" --model=person/model-two`, "--dangerously-skip-permissions",
			[]string{"--model", "person model one", "--model=person/model-two"}, []string{"person model one", "person/model-two"}, []string{"--model person model one", "--model=person/model-two"}},
		{gen.ExternalCliToolCodex, "gpt-5-codex", `--model "person model one" -m person/model-two --model=person/model-three`, "--dangerously-bypass-approvals-and-sandbox",
			[]string{"--model", "person model one", "-m", "person/model-two", "--model=person/model-three"}, []string{"person model one", "person/model-two", "person/model-three"}, []string{"--model person model one", "-m person/model-two", "--model=person/model-three"}},
		{gen.ExternalCliToolOpencode, "openrouter/deepseek/deepseek-v4.1-flash", `--model "person model one" -m person/model-two --model=person/model-three`, "--dangerously-skip-permissions",
			[]string{"--model", "person model one", "-m", "person/model-two", "--model=person/model-three"}, []string{"person model one", "person/model-two", "person/model-three"}, []string{"--model person model one", "-m person/model-two", "--model=person/model-three"}},
	} {
		t.Run(string(cli.kind), func(t *testing.T) {
			program := t11bSavedProgram(t, api, string(cli.kind))
			presetText := cli.rawModelOptions + ` --fixture-preset "kept two words" ` + cli.bypass
			require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
				row := cfg.Sandbox.ThirdPartyAgents.CLIs[string(cli.kind)]
				row.PresetArgs = presetText
				cfg.Sandbox.ThirdPartyAgents.CLIs[string(cli.kind)] = row
				return nil
			}))
			modes := []struct {
				name, model string
				emitted     bool
			}{{"own_model", cli.model, true}, {"no_own_model", "", false}}
			if cli.kind == gen.ExternalCliToolOpencode {
				modes = append(modes, struct {
					name, model string
					emitted     bool
				}{"bare_model_not_emitted", "bare-model", false})
			}
			for _, mode := range modes {
				t.Run(mode.name, func(t *testing.T) {
					workerArgs := "--model worker-model --fixture-worker worker-kept"
					code, preview := postExecutorPreview(t, api, gen.ExecutorCommandPreviewRequest{
						Cli: cli.kind, Model: &mode.model, CliArgs: &workerArgs,
					})
					require.Equal(t, http.StatusOK, code)
					assert.Equal(t, program, preview.Binary)
					wantModels := cli.models
					wantDrops := []string{"--model worker-model"}
					wantPreset := append([]string(nil), cli.modelArgs...)
					if mode.emitted {
						wantModels = []string{mode.model}
						wantDrops = append(wantDrops, cli.dropped...)
						wantPreset = nil
					}
					assert.Equal(t, wantModels, opencodeFix1880PreviewModels(t, preview.Argv), "Q-OC1=A: remove preset models only when Omnipus actually passes its own")
					assert.NotContains(t, preview.Argv, "worker-model")
					var drops []string
					for _, drop := range preview.DroppedArgs {
						drops = append(drops, drop.Flag)
						assert.Regexp(t, `(?i)(configured|omnipus|agent).*model|model.*(configured|omnipus|agent)`, drop.Reason, "each removal must explain model precedence")
					}
					assert.Equal(t, wantDrops, drops, "disclose each removed worker/preset model option, with its value and in input order; never claim an untouched preset was dropped")
					wantPreset = append(wantPreset, "--fixture-preset", "kept two words", cli.bypass)
					wantTail := append([]string{"--fixture-worker", "worker-kept"}, wantPreset...)
					switch cli.kind {
					case gen.ExternalCliToolCodex:
						wantTail = append(wantTail, "-")
					case gen.ExternalCliToolOpencode:
						wantTail = append(wantTail, "--", "<prompt>")
					}
					require.GreaterOrEqual(t, len(preview.Argv), len(wantTail))
					assert.Equal(t, wantTail, preview.Argv[len(preview.Argv)-len(wantTail):], "non-model preset parameters, even a bypass, remain unfiltered and last")
					assert.Contains(t, preview.CommandLine, `--fixture-preset 'kept two words' `+cli.bypass)
					for _, personModel := range cli.models {
						if mode.emitted {
							assert.NotContains(t, preview.CommandLine, personModel, "a dropped preset option must not remain in the copyable command")
						} else {
							assert.Contains(t, preview.CommandLine, personModel, "without an emitted own model the saved preset remains visible")
						}
					}
				})
			}
		})
	}
}
