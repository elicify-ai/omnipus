package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q-OC1=A (2026-11-19): a preset model option is removed only when Omnipus
// actually passes its own model. This supersedes ADR-032's older preset
// exception for model conflicts, not its unfiltered/non-model ordering rule.
// Worker cli_args always drop model options, including with no own model.
func TestOpenCodePresetModelPrecedence_ConfiguredModelWinsWithoutFilteringOtherPresetArgs(t *testing.T) {
	for _, driver := range []struct {
		name, model, bypass string
		build               func(RunOptions) []string
		shortModel          bool
	}{
		{"claude", "sonnet", "--dangerously-skip-permissions", BuildClaudeArgs, false},
		{"codex", "gpt-5-codex", "--dangerously-bypass-approvals-and-sandbox", BuildCodexArgs, true},
		{"opencode", "openrouter/deepseek/deepseek-v4.1-flash", "--dangerously-skip-permissions", BuildOpencodeArgs, true},
	} {
		t.Run(driver.name, func(t *testing.T) {
			options := []struct {
				name   string
				args   []string
				values []string
			}{
				{"long_separate", []string{"--model", "person/model-one"}, []string{"person/model-one"}},
				{"long_equals", []string{"--model=person/model-two"}, []string{"person/model-two"}},
				{"repeated", []string{"--model", "person/model-one", "--model=person/model-two", "--model", "person/model-three"}, []string{"person/model-one", "person/model-two", "person/model-three"}},
			}
			if driver.shortModel {
				options = append(options, struct {
					name   string
					args   []string
					values []string
				}{"short_separate", []string{"-m", "person/model-one"}, []string{"person/model-one"}}, struct {
					name   string
					args   []string
					values []string
				}{"mixed_aliases", []string{"-m", "person/model-one", "--model=person/model-two", "--model", "person/model-three"}, []string{"person/model-one", "person/model-two", "person/model-three"}})
			}
			models := []struct {
				name, value string
				emitted     bool
			}{{"own_model", driver.model, true}, {"no_own_model", "", false}}
			if driver.name == "opencode" {
				models = append(models, struct {
					name, value string
					emitted     bool
				}{"bare_model_not_emitted", "bare-model", false})
			}
			for _, model := range models {
				for _, nativeID := range []string{"", "original-native-preset-conversation"} {
					phase := "initial"
					if nativeID != "" {
						phase = "resume"
					}
					for _, option := range options {
						t.Run(model.name+"/"+phase+"/"+option.name, func(t *testing.T) {
							preset := append([]string{"--preset-before", "p-before"}, option.args...)
							preset = append(preset, "--preset-after", "p-after", driver.bypass)
							originalPreset := append([]string(nil), preset...)
							opts := RunOptions{Input: "task", Model: model.value, MaxTurns: 7, resumeNativeID: nativeID,
								CLIArgs: []string{"--worker-before", "w-before", "--model", "worker-model", "--worker-after", "w-after"}, PresetArgs: preset}
							argv := driver.build(opts)
							wantModels := option.values
							wantPreset := originalPreset
							if model.emitted {
								wantModels = []string{model.value}
								wantPreset = []string{"--preset-before", "p-before", "--preset-after", "p-after", driver.bypass}
							}
							assert.Equal(t, wantModels, opencodeFix1880ModelValues(t, argv), "Q-OC1=A: exactly Omnipus's model when emitted, otherwise preserve every preset model; argv=%q", argv)
							assert.NotContains(t, argv, "worker-model", "the worker's Additional CLI arguments never supply the model")
							wantTail := append([]string{"--worker-before", "w-before", "--worker-after", "w-after"}, wantPreset...)
							switch driver.name {
							case "codex":
								wantTail = append(wantTail, "-")
							case "opencode":
								wantTail = append(wantTail, "--", "task")
							}
							require.GreaterOrEqual(t, len(argv), len(wantTail))
							assert.Equal(t, wantTail, argv[len(argv)-len(wantTail):], "only conflicting preset model tokens may disappear; even the person's bypass stays unfiltered and last")
							assert.Equal(t, originalPreset, opts.PresetArgs, "building a command cannot mutate the saved preset tokens")
						})
					}
				}
			}
		})
	}
}
