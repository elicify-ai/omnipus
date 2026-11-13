package gateway

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSetupAndStartServices_IPv6WildcardHostBinds_1823: gateway.host "::" used
// to build ":::port" ("too many colons"), so boot aborted at the listener bind.
// The listener must bind on the IPv6 wildcard and answer on the loopback.
func TestSetupAndStartServices_IPv6WildcardHostBinds_1823(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	probe, err := net.Listen("tcp", "[::]:0")
	require.NoError(t, err, "SETUP: this host must support an IPv6 wildcard listener")
	tcpAddr, ok := probe.Addr().(*net.TCPAddr)
	require.True(t, ok, "SETUP: listener address %T is not a TCP address", probe.Addr())
	port := tcpAddr.Port
	require.NoError(t, probe.Close())

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "::", Port: port},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	credStore := newUnlockedStore(t, tmpDir)

	rs, bootErr := setupAndStartServices(
		context.Background(), cfg, credentials.SecretBundle{}, al, msgBus, tmpDir,
		credStore, &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false,
	)
	pe := agent.GetPlanEngine(al)
	t.Cleanup(func() {
		if pe != nil {
			pe.Stop()
		}
	})
	require.NoError(t, bootErr, "boot with gateway.host \"::\" must bind the listener")
	t.Cleanup(func() { stopAndCleanupServices(rs, 5*time.Second, false) })

	c, err := net.DialTimeout("tcp", net.JoinHostPort("::1", strconv.Itoa(port)), 3*time.Second)
	require.NoError(t, err, "the listener bound on :: must accept a connection on ::1")
	require.NoError(t, c.Close())
}
