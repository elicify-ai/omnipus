package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Embedding only the mandatory interface intentionally hides the optional
// readiness capability. The real factory check is the unit under test; no
// mandatory driver method or external process needs to run at construction.
type opencodeGateDriverWithoutReadiness struct {
	ExternalAgentRunner
}

var _ ExternalAgentRunner = (*opencodeGateDriverWithoutReadiness)(nil)

// Team-lead 2026-11-21 08:05Z, renewed 2026-11-22 06:55Z: the factory's
// capability check refuses a driver without native-conversation reporting and
// names the tool. All three actual drivers are accepted before their first
// conversation. Canonical CLI keys come from ExternalCli.yaml, not the switch.
// The negative unit boundary is the factory check; NewDriver's closed switch
// cannot accept an injected test driver. Public-factory positives also protect
// actual construction, without pretending to inject the negative through it.
func TestOpenCodeGateBFactory_RequiresNativeConversationReporter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		driver ExternalAgentRunner
	}{
		{name: "mandatory_interface_only", driver: &opencodeGateDriverWithoutReadiness{}},
		{
			name:   "real_driver_wrapped_without_reporting_capability",
			driver: &opencodeGateDriverWithoutReadiness{ExternalAgentRunner: NewClaudeDriver(nil)},
		},
	} {
		t.Run(tc.name+"_is_refused_and_names_the_tool", func(t *testing.T) {
			_, reports := tc.driver.(NativeConversationReporter)
			require.False(t, reports, "instrument: a real underlying driver must not accidentally expose the missing capability")
			const cli = "gate-driver-without-readiness"
			got, err := requireNativeConversationReporter(cli, tc.driver)
			require.Error(t, err, "a driver without readiness reporting must not be admitted")
			assert.Contains(t, err.Error(), cli, "the refusal must identify the selected tool")
			assert.Nil(t, got, "a refused factory result must not expose an executable driver")
		})
	}
	for _, tc := range []struct {
		cli    string
		driver ExternalAgentRunner
	}{
		{cli: "claude-code", driver: NewClaudeDriver(nil)},
		{cli: "codex", driver: NewCodexDriver(nil)},
		{cli: "opencode", driver: NewOpencodeDriver(nil)},
	} {
		t.Run(tc.cli+"_real_driver_is_accepted_before_it_has_a_conversation", func(t *testing.T) {
			reporter, ok := tc.driver.(NativeConversationReporter)
			require.True(t, ok, "instrument: this is the actual readiness-reporting driver")
			require.False(t, reporter.HasNativeConversation(), "a fresh driver has not run; capability is not current readiness")
			got, err := requireNativeConversationReporter(tc.cli, tc.driver)
			require.NoError(t, err)
			assert.Same(t, tc.driver, got, "the accepted real driver is preserved")
			constructed, err := NewDriver(tc.cli, nil)
			require.NoError(t, err, "the public factory must construct each contract-listed driver")
			assert.IsType(t, tc.driver, constructed)
			fresh, ok := constructed.(NativeConversationReporter)
			require.True(t, ok, "the public factory returns a reporting driver, not an unrelated substitute")
			assert.False(t, fresh.HasNativeConversation(), "public construction must not require or start a conversation")
		})
	}
}
