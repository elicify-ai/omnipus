package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestIssue1277_RESTMCPMissingRemovedUnreadableSecretNamesRef(t *testing.T) {
	for _, state := range []string{"missing", "removed", "unreadable"} {
		t.Run(state, func(t *testing.T) {
			f, api := newIssue1277WireMCP(t, state)
			rows := issue1277WireMCPRows(t, api)
			require.Len(t, rows, 2, "one connector error must not hide either connection")
			good := rows["qa1277-wire-good"]
			require.JSONEq(t, `true`, string(good["enabled"]))
			require.Equal(t, "connected", issue1277WireString(t, good, "status"))
			require.JSONEq(t, `1`, string(good["tool_count"])) // the single real SDK tool.
			require.NotContains(t, good, "status_reason", "healthy connector must not inherit the sibling's missing-ref error")
			bad := rows["qa1277-wire-bad"]
			require.JSONEq(t, `true`, string(bad["enabled"]))
			require.Equal(t, "error", issue1277WireString(t, bad, "status"))
			require.JSONEq(t, `0`, string(bad["tool_count"]))
			issue1277RequireHealthyProvider(t, api.agentLoop)
			require.Equal(t, int64(1), f.providerCalls.Load())
			issue1277WireRefReason(t, bad, "status_reason", issue1277WireBadMCPRef)
		})
	}
}

func TestIssue1277_RESTMCPHealthySecretHasNoErrorReason(t *testing.T) {
	f, api := newIssue1277WireMCP(t, "healthy")
	rows := issue1277WireMCPRows(t, api)
	require.Len(t, rows, 2)
	for _, id := range []string{"qa1277-wire-bad", "qa1277-wire-good"} {
		row := rows[id]
		require.JSONEq(t, `true`, string(row["enabled"]))
		require.Equal(t, "connected", issue1277WireString(t, row, "status"))
		require.JSONEq(t, `1`, string(row["tool_count"]))
		require.NotContains(t, row, "status_reason", "healthy restored reference must not leave a stale error")
	}
	issue1277RequireHealthyProvider(t, api.agentLoop)
	require.Equal(t, int64(1), f.providerCalls.Load())
}

func TestIssue1277_RESTMailboxMissingRemovedUnreadableSecretNamesRef(t *testing.T) {
	for _, state := range []string{"missing", "removed", "unreadable"} {
		t.Run(state, func(t *testing.T) {
			f, api := newIssue1277WireMailbox(t, state)
			rows := issue1277WireMailboxRows(t, api)
			require.Len(t, rows, 2, "one mailbox error must not hide either mailbox")
			good := rows["healthy"]
			require.Equal(t, "main", issue1277WireString(t, good, "agent_id"))
			require.JSONEq(t, `true`, string(good["enabled"]))
			require.JSONEq(t, `true`, string(good["configured"]))
			// 82ce6aea6 explicitly permits status to be omitted by servers
			// that do not report it; if reported, the healthy value is exactly ok.
			if _, reported := good["status"]; reported {
				require.Equal(t, "ok", issue1277WireString(t, good, "status"))
			}
			require.NotContains(t, good, "status_reason", "healthy mailbox must not inherit the sibling's error")
			bad := rows["default"]
			require.Equal(t, "main", issue1277WireString(t, bad, "agent_id"))
			require.JSONEq(t, `true`, string(bad["enabled"]))
			require.JSONEq(t, `false`, string(bad["configured"]))
			issue1277RequireHealthyProvider(t, api.agentLoop)
			require.Equal(t, int64(1), f.providerCalls.Load())
			require.Equal(t, "degraded", issue1277WireString(t, bad, "status"), "#1277 requires an explicit error state for the affected mailbox")
			issue1277WireRefReason(t, bad, "status_reason", issue1277WireBadMailRef)
		})
	}
}

func TestIssue1277_RESTMailboxHealthySecretIsConfiguredWithoutError(t *testing.T) {
	f, api := newIssue1277WireMailbox(t, "healthy")
	rows := issue1277WireMailboxRows(t, api)
	require.Len(t, rows, 2)
	for _, id := range []string{"default", "healthy"} {
		row := rows[id]
		require.Equal(t, "main", issue1277WireString(t, row, "agent_id"))
		require.JSONEq(t, `true`, string(row["enabled"]))
		require.JSONEq(t, `true`, string(row["configured"]))
		if _, reported := row["status"]; reported {
			require.Equal(t, "ok", issue1277WireString(t, row, "status"))
		}
		require.NotContains(t, row, "status_reason", "healthy credential must have no stale error reason")
	}
	issue1277RequireHealthyProvider(t, api.agentLoop)
	require.Equal(t, int64(1), f.providerCalls.Load())
}

func TestIssue1277_RESTProviderMissingRemovedSecretNamesRef(t *testing.T) {
	for _, state := range []string{"missing", "removed"} {
		t.Run(state, func(t *testing.T) {
			f := newIssue1277Fixture(t)
			require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-good-line"))
			cfg, bundle, _ := f.boot(t)
			issue1277WireSecretState(t, f, issue1277WireBadProvRef, state)
			if state == "removed" {
				// A removed live credential may still have its old injected value.
				// REST must diagnose the missing store reference, not trust that value.
				t.Setenv(issue1277WireBadProvRef, issue1277WireSecret)
			}
			cfg.Providers = append(cfg.Providers, &config.ModelConfig{
				Provider: "qa1277-wire-bad", Model: "health-probe", Models: []string{"health-probe"}, Custom: true,
				Protocol: "openai-compatible", APIBase: cfg.Providers[0].APIBase, APIKeyRef: issue1277WireBadProvRef,
			})
			api := f.startAPI(t, cfg, bundle)
			rows := issue1277WireProviderRows(t, api)
			require.Len(t, rows, 2)
			good := rows["qa1277"]
			require.Equal(t, "connected", issue1277WireString(t, good, "status"))
			require.NotContains(t, good, "error", "healthy provider must not inherit the missing credential error")
			issue1277RequireHealthyProvider(t, api.agentLoop)
			require.Equal(t, int64(1), f.providerCalls.Load())
			bad := rows["qa1277-wire-bad"]
			require.Equal(t, "error", issue1277WireString(t, bad, "status"), "the missing ref must be a visible error, not merely disconnected or stale-connected")
			issue1277WireRefReason(t, bad, "error", issue1277WireBadProvRef)
		})
	}
}

func TestIssue1277_RESTProviderHealthySecretIsConnectedWithoutError(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-good-line"))
	issue1277WireSecretState(t, f, issue1277WireBadProvRef, "healthy")
	f.cfg.Providers = append(f.cfg.Providers, &config.ModelConfig{
		Provider: "qa1277-wire-bad", Model: "health-probe", Models: []string{"health-probe"}, Custom: true,
		Protocol: "openai-compatible", APIBase: f.cfg.Providers[0].APIBase, APIKeyRef: issue1277WireBadProvRef,
	})
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	cfg, bundle, _ := f.boot(t)
	api := f.startAPI(t, cfg, bundle)
	rows := issue1277WireProviderRows(t, api)
	require.Len(t, rows, 2)
	for _, id := range []string{"qa1277", "qa1277-wire-bad"} {
		row := rows[id]
		require.Equal(t, "connected", issue1277WireString(t, row, "status"))
		require.NotContains(t, row, "error")
	}
	data, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(data), issue1277WireSecret, "REST must not serialize even the healthy credential value")
	issue1277RequireHealthyProvider(t, api.agentLoop)
	require.Equal(t, int64(1), f.providerCalls.Load())
}
