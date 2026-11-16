package gateway

import (
	"net/http"
	"slices"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round 2 item 6: the preview must accept the same nested model that the real
// OpenCode control captures accepted. The oracle is that captured string, not
// another call to BuildOpencodeArgs (which could mirror the endpoint's bug).
func TestPostAgentsExecutorPreview_Opencode226R2_NestedModel(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		keep        bool
	}{
		{"nested_model", "openrouter/deepseek/deepseek-v4.1-flash", true},
		{"bare_model_control", "deepseek-v4.1-flash", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := executorDefaultsTestAPI(t)
			program := t11bSavedProgram(t, api, "opencode")
			code, resp := postExecutorPreview(t, api, gen.ExecutorCommandPreviewRequest{
				Cli: gen.ExternalCliToolOpencode, Model: &tc.model,
			})
			require.Equal(t, http.StatusOK, code, "preview must reach the real handler successfully")
			assert.Equal(t, program, resp.Binary, "preview must use the person-saved program")
			separator := slices.Index(resp.Argv, "--")
			require.GreaterOrEqual(t, separator, 0, "OpenCode preview must separate model options from the prompt")
			var modelValues []string
			for i, arg := range resp.Argv[:separator] {
				if arg == "--model" {
					require.Less(t, i+1, separator, "--model must have a value before the prompt separator")
					modelValues = append(modelValues, resp.Argv[i+1])
				}
			}
			if tc.keep {
				assert.Nil(t, resp.ModelDroppedReason, "captured nested model must not have a drop reason")
				assert.Equal(t, []string{tc.model}, modelValues, "exact nested model must be kept once as --model")
			} else {
				require.NotNil(t, resp.ModelDroppedReason, "bare-model control must expose the omission")
				assert.NotEmpty(t, *resp.ModelDroppedReason)
				assert.Empty(t, modelValues, "bare-model control must not produce --model")
			}
		})
	}
}
