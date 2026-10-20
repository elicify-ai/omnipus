package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const connectPlaintextSentinel = "GATE-R1-CONNECT-PLAINTEXT-SENTINEL"

// TestConnectChannel_PlaintextNamesRefusedBeforeTheRegistryLog is Train 2
// gate r1 M2 (CR-T2-02, SFH T2-SF-01, security T2-S3; founder: fix now):
// connect_channel must refuse every plaintext credential argument name
// before the registry writes its argument log (and before native history),
// exactly like configure_channel. The sentinel is never registered for
// masking, so only an early refusal keeps it out of the real log sink.
func TestConnectChannel_PlaintextNamesRefusedBeforeTheRegistryLog(t *testing.T) {
	cfg := &config.Config{}
	connect := systools.NewChannelConnectTool(&systools.Deps{GetCfg: func() *config.Config { return cfg }})
	for _, name := range []string{"token", "bot_token", "app_token", "app_secret", "encrypt_key", "verification_token",
		"client_secret", "access_token", "crypto_passphrase", "channel_secret", "channel_access_token",
		"secret", "password", "nick_serv_password", "nickserv_password", "sasl_password", "webhook_url", "service_account_json"} {
		t.Run(name, func(t *testing.T) {
			refusal := tools.ToolArgumentRefusal(connect, map[string]any{name: connectPlaintextSentinel})
			require.NotNil(t, refusal, "connect_channel must refuse plaintext %q by name", name)
			require.NotContains(t, refusal.ForLLM, connectPlaintextSentinel)

			registry := tools.NewToolRegistry()
			registry.Register(connect)
			path := filepath.Join(t.TempDir(), "registry.jsonl")
			level := logger.GetLevel()
			logger.SetLevel(logger.INFO)
			t.Cleanup(func() { logger.SetLevel(level) })
			require.NoError(t, logger.EnableFileLogging(path))
			t.Cleanup(logger.DisableFileLogging)
			result := registry.ExecuteWithContext(context.Background(), "connect_channel", map[string]any{
				"id": "telegram", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent",
				name: connectPlaintextSentinel,
			}, "webchat", "gate-r1-m2", nil)
			require.True(t, result.IsError)
			require.NotContains(t, result.ForLLM, connectPlaintextSentinel)
			// Positive control: a public argument IS written to this sink, so
			// an empty or disabled log cannot pass the sentinel check.
			registry.ExecuteWithContext(context.Background(), "connect_channel", map[string]any{
				"id": "telegram", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent",
				"label": "GATE-R1-CONNECT-PUBLIC-CONTROL",
			}, "webchat", "gate-r1-m2", nil)
			logger.DisableFileLogging()
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			require.True(t, strings.Contains(string(raw), "GATE-R1-CONNECT-PUBLIC-CONTROL"), "instrument: the sink records connect_channel arguments")
			require.False(t, strings.Contains(string(raw), connectPlaintextSentinel), "the plaintext reached the registry log")
			require.Empty(t, cfg.Channels, "nothing saved")
		})
	}
}
