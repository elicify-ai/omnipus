// Omnipus — System Agent Tool Tests: list_channels shows the posting list.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// T-23 — list_channels outbound (channels-spec-r8, T-23; FR-034; scenario
// S-37). Every CONFIGURED entry gains `outbound {policy, targets}` — the
// canonical stored values, the same source list_channels already reads — so
// Omnipus can see which recipients a connection may start messages to (the
// deny entry's ReadOKReason promises exactly this). Entries with no config
// entry carry no outbound key.
//
// Oracle: S-37 — `outbound: {policy: "agent_may_start", targets:
// ["discord:222"]}` and a `revision` equal to GET's (the FR-088 token over
// the live configuration, including outbound — T-18's token leg).
//
// RED expectation: today channelEntry carries no outbound field, so the
// discord row fails on the missing key. The revision row guards the token's
// source and passes today.

package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/require"
)

// seedListedOutbound builds the S-11 state: discord configured with an
// agent_may_start posting list, a namespaced instance with reply_only, and
// the known types left unconfigured.
func seedListedOutbound(cfg *config.Config) {
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"discord": {
			Type:        "discord",
			Enabled:     true,
			WorkspaceID: "workspace-alpha",
			Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "mia"},
			Outbound: &config.ChannelOutboundConfig{
				Policy:  "agent_may_start",
				Targets: []string{"discord:222"},
			},
		},
		"slack.eu": {
			Type:        "slack",
			Enabled:     true,
			WorkspaceID: "workspace-alpha",
			Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "mia"},
			Outbound: &config.ChannelOutboundConfig{
				Policy:  "reply_only",
				Targets: []string{"slack:C1"},
			},
		},
	}
}

func t23Row(t *testing.T, deps *systools.Deps, id string) map[string]any {
	t.Helper()
	listed := wu3Success(t, systools.NewChannelListTool(deps).Execute(context.Background(), map[string]any{}))
	rows, ok := listed["channels"].([]any)
	require.True(t, ok, "instrument: list_channels returns its channel rows")
	for _, value := range rows {
		row := wu3Object(t, value)
		if row["id"] == id {
			return row
		}
	}
	t.Fatalf("instrument: list_channels has no %q row", id)
	return nil
}

func TestT23_ListChannels_ConfiguredEntryCarriesOutbound_S37(t *testing.T) {
	deps, cfg := newTestDeps(t)
	seedListedOutbound(cfg)

	// S-37: the discord entry carries the stored policy and canonical
	// targets verbatim.
	row := t23Row(t, deps, "discord")
	outbound, ok := row["outbound"].(map[string]any)
	require.True(t, ok, "T-23/FR-034: the configured discord entry carries an outbound object; row: %v", row)
	require.Equal(t, "agent_may_start", outbound["policy"], "S-37: the stored policy is shown")
	targets, ok := outbound["targets"].([]any)
	require.True(t, ok, "S-37: the stored targets are shown as a list")
	require.Equal(t, []any{"discord:222"}, targets,
		"S-37: the canonical stored targets are shown verbatim")

	// Namespaced instances are configured entries too.
	row = t23Row(t, deps, "slack.eu")
	outbound, ok = row["outbound"].(map[string]any)
	require.True(t, ok, "T-23: the namespaced configured entry carries outbound too; row: %v", row)
	require.Equal(t, "reply_only", outbound["policy"])

	// A known type with no config entry carries NO outbound key.
	row = t23Row(t, deps, "feishu")
	require.NotContains(t, row, "outbound",
		"T-23: a not_configured entry has no outbound object")
}

func TestT23_ListChannels_RevisionMatchesTheLiveToken(t *testing.T) {
	// S-37: the entry's revision is the one FR-088 token over the live
	// configuration (GET's source — the REST equality itself is T-14's
	// integration row). Unit-shape: the token is present and stable across
	// calls, so tool write calls can carry it as expected_revision.
	deps, cfg := newTestDeps(t)
	seedListedOutbound(cfg)

	first := t23Row(t, deps, "discord")["revision"]
	require.NotEmpty(t, first, "S-37: the configured entry has a revision token")
	second := t23Row(t, deps, "discord")["revision"]
	require.Equal(t, first, second,
		"S-37: one stable token source for the entry's revision")
}
