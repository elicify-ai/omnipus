package agent

// C2-03: loop HTTP assertions must reject another route's valid encoding.
// Oracle: S-17 OpenRouter levels row; S-19 inventories foreign effort keys.
import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func assertOpenRouterLevelBody(t assert.TestingT, body map[string]any, want string) {
	if helper, ok := t.(interface{ Helper() }); ok {
		helper.Helper()
	}
	assert.Equal(t, map[string]any{"effort": want}, body["reasoning"], "S-17: OpenRouter levels require exactly reasoning:{effort:value}, with no enabled sibling")
	for _, foreign := range []string{"reasoning_effort", "thinking", "output_config", "additionalModelRequestFields", "extended_thinking"} {
		assert.NotContains(t, body, foreign, "S-17: no foreign/contradictory effort carrier on the OpenRouter request")
	}
}

type effortOracleProbe struct{ errors []string }

func (p *effortOracleProbe) Errorf(format string, args ...any) {
	p.errors = append(p.errors, fmt.Sprintf(format, args...))
}

// Instrument PIN, not a feature GREEN or implementation mutation check: the
// same assertion helper used on loop HTTP bodies must catch each wrong shape.
func TestT_Instrument_OpenRouterLevelOracleRejectsForeignEncodings(t *testing.T) {
	for _, row := range []struct {
		name         string
		body         map[string]any
		wantFailures int
	}{
		{"exact nested", map[string]any{"reasoning": map[string]any{"effort": "high"}}, 0},
		{"flat only", map[string]any{"reasoning_effort": "high"}, 2},
		{"wrong level", map[string]any{"reasoning": map[string]any{"effort": "low"}}, 1},
		{"contradictory enabled", map[string]any{"reasoning": map[string]any{"effort": "high", "enabled": false}}, 1},
		{"nested plus flat", map[string]any{"reasoning": map[string]any{"effort": "high"}, "reasoning_effort": "high"}, 1},
		{"foreign thinking", map[string]any{"reasoning": map[string]any{"effort": "high"}, "thinking": map[string]any{"type": "adaptive"}}, 1},
		{"foreign output config", map[string]any{"reasoning": map[string]any{"effort": "high"}, "output_config": map[string]any{"effort": "high"}}, 1},
		{"foreign bedrock", map[string]any{"reasoning": map[string]any{"effort": "high"}, "additionalModelRequestFields": map[string]any{"reasoningConfig": map[string]any{"maxReasoningEffort": "high"}}}, 1},
		{"retired recap key", map[string]any{"reasoning": map[string]any{"effort": "high"}, "extended_thinking": true}, 1},
	} {
		t.Run(row.name, func(t *testing.T) {
			probe := &effortOracleProbe{}
			assertOpenRouterLevelBody(probe, row.body, "high")
			assert.Equal(t, row.wantFailures, len(probe.errors), "instrument must detect this exact wrong-wire class: %s", row.name)
		})
	}
}
