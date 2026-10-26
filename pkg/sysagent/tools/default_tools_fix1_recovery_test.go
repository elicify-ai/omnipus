package systools_test

import (
	"context"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gate SF-3: keep CONFIG_UNAVAILABLE explicit and tell the caller how to
// retry/escalate safely. No exact prose is invented beyond that requirement.
func TestAgentToolsUnavailableConfigIncludesRecoveryInstruction(t *testing.T) {
	for _, row := range []struct {
		name string
		get  func() *config.Config
	}{{"missing_callback", nil}, {"nil_configuration", func() *config.Config { return nil }}} {
		t.Run(row.name, func(t *testing.T) {
			home := t.TempDir()
			deps := &systools.Deps{Home: home, GetCfg: row.get}
			defaultToolsWirePreviewCatalog(t, deps)
			result := systools.NewAgentGetToolsTool(deps).Execute(context.Background(), map[string]any{"new_agent_type": "Main"})
			body := wu3Error(t, result, "CONFIG_UNAVAILABLE")
			suggestion, ok := body["suggestion"].(string)
			require.True(t, ok, "SF-3: recovery instruction must be a string")
			assert.Regexp(t, `(?i)\bretry\b`, suggestion, "SF-3: include a safe retry step")
			assert.Regexp(t, `(?i)\b(operator|report|contact)\b`, suggestion, "SF-3: persistent wiring failure needs an escalation instruction")
			entries, err := os.ReadDir(home)
			require.NoError(t, err)
			assert.Empty(t, entries, "failed inventory lookup must not change settings or write an agent")
		})
	}
}
