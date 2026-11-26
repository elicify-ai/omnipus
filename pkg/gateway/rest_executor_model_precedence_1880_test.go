package gateway

import (
	"net/http"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1880, founder 2026-11-18 Q2=B: the real preview must both remove the
// person's model options and disclose each removal. A corrected argv with an
// empty dropped_args list would still silently ignore the person's setting.
// Existing dropped_args contract: two-token options are reported as one
// space-joined flag/value descriptor, while --flag=value retains its spelling.
func TestOpenCodeFix1880_PreviewDisclosesEveryRemovedModelOption(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	for _, tc := range []struct {
		cli   gen.ExternalCliTool
		model string
		args  string
		drops []string
	}{
		{cli: gen.ExternalCliToolOpencode, model: "openrouter/deepseek/deepseek-v4.1-flash",
			args:  "--model person/model-one -m 'person/model two' --model=person/model-three",
			drops: []string{"--model person/model-one", "-m person/model two", "--model=person/model-three"}},
		{cli: gen.ExternalCliToolClaudeCode, model: "sonnet",
			args:  "--model 'person/model one' --model=person/model-two",
			drops: []string{"--model person/model one", "--model=person/model-two"}},
		{cli: gen.ExternalCliToolCodex, model: "gpt-5-codex",
			args:  "--model person/model-one -m 'person/model two' --model=person/model-three",
			drops: []string{"--model person/model-one", "-m person/model two", "--model=person/model-three"}},
	} {
		t.Run(string(tc.cli), func(t *testing.T) {
			program := t11bSavedProgram(t, api, string(tc.cli))
			extra := "--fixture-before kept-before " + tc.args + " --fixture-after kept-after"
			turnCap := 7 // Positive valid cap; this test is about model precedence, not cap boundaries.
			code, preview := postExecutorPreview(t, api, gen.ExecutorCommandPreviewRequest{
				Cli: tc.cli, Model: &tc.model, CliArgs: &extra, MaxToolIterations: &turnCap,
			})
			require.Equal(t, http.StatusOK, code)
			assert.Equal(t, program, preview.Binary, "the actual person-saved program is still previewed")
			assert.Equal(t, []string{tc.model}, opencodeFix1880PreviewModels(t, preview.Argv),
				"#1880 Q2=B: preview must contain exactly one model, Omnipus's; argv=%q", preview.Argv)
			assert.Equal(t, 1, strings.Count(preview.CommandLine, tc.model), "the displayed command must show Omnipus's model exactly once")
			assert.NotContains(t, preview.CommandLine, "person/model", "the displayed command cannot contain a removed model option")
			assert.Nil(t, preview.ModelDroppedReason, "Q2 drops the person's override, NOT Omnipus's valid model")
			var flags []string
			for _, drop := range preview.DroppedArgs {
				flags = append(flags, drop.Flag)
				// Copy is not frozen by the spec. Require its semantic explanation,
				// without pinning punctuation or inventing exact product wording.
				assert.Regexp(t, `(?i)(configured|omnipus|agent).*model|model.*(configured|omnipus|agent)`, drop.Reason,
					"#1880: a removal reason must explain the configured model's precedence, not just say a flag vanished")
			}
			assert.Equal(t, tc.drops, flags, "#1880: disclose EVERY removed option in input order, including aliases and its value")
			// The requested override is the only input being removed. Its value
			// must not be left as a stray task, and neighbouring arguments survive.
			joined := strings.Join(preview.Argv, " ")
			assert.Contains(t, joined, "--fixture-before kept-before --fixture-after kept-after")
			assert.NotContains(t, joined, "person/model")
		})
	}
}

func opencodeFix1880PreviewModels(t *testing.T, argv []string) []string {
	t.Helper()
	var values []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			break // OpenCode's positional task is not an option.
		}
		switch {
		case arg == "--model" || arg == "-m":
			require.Less(t, i+1, len(argv), "model flag must retain its value")
			i++
			values = append(values, argv[i])
		case strings.HasPrefix(arg, "--model="):
			values = append(values, strings.TrimPrefix(arg, "--model="))
		}
	}
	return values
}
