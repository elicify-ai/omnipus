package channels

// T-18, FR-015 and S-23: lifecycle comparisons use the adapter view, while
// the user-facing FR-088 conflict token includes outbound. Accepted Q8 names
// those two APIs: ChannelRevision is the pairing/adapter-view hash;
// config.ChannelConfigRevision is the whole-instance conflict token.
//
// Oracles come from the spec's "Persisted configuration" table, not observed
// hash values: adding outbound or changing its targets moves only the conflict
// token. A connection-field change still moves the adapter-view hash.
// The real reload/pairing lifecycle boundary is T-18b, in a separate file.

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// t18Config builds a config with one enabled discord instance, optionally
// carrying the outbound setting.
func t18Config(withOutbound bool) *config.Config {
	inst := config.ChannelInstanceConfig{
		Type:        "discord",
		Enabled:     true,
		WorkspaceID: "ws-t18",
		Identity:    &config.ChannelIdentity{Kind: "agent", ID: "mia"},
	}
	if withOutbound {
		inst.Outbound = &config.ChannelOutboundConfig{
			Policy:  "agent_may_start",
			Targets: []string{"discord:222"},
		}
	}
	return &config.Config{Channels: map[string]config.ChannelInstanceConfig{"discord": inst}}
}

func TestT18_ToChannelHashes_ListOnlyOutboundChangeDoesNotMoveTheRestartHash(t *testing.T) {
	// FR-015 / S-23: adding (or changing) ONLY the outbound setting is
	// invisible to the restart hash — the adapter view is compared.
	before := toChannelHashes(t18Config(false))
	after := toChannelHashes(t18Config(true))

	require.Equal(t, before["discord"], after["discord"],
		"T-18/FR-015: a list-only outbound save must not change the restart hash "+
			"(it would lock a pairing order, stamp a generation and restart the connection); "+
			"before=%q after=%q", before["discord"], after["discord"])

	// The flip side: a REAL connection change still moves the hash (the
	// adapter view keeps everything else).
	changed := t18Config(true)
	inst := changed.Channels["discord"]
	inst.AllowFrom = []string{"new-user"}
	changed.Channels["discord"] = inst
	changedHashes := toChannelHashes(changed)
	require.NotEqual(t, after["discord"], changedHashes["discord"],
		"T-18: the adapter view still sees non-outbound changes")
}

func TestT18_ChannelConfigRevision_OutboundChangeMovesTheConflictToken(t *testing.T) {
	// FR-088 / accepted Q8: this is the conflict token, not the pairing hash.
	// An outbound-only save must make a concurrent save stale (409).
	noOutbound := config.ChannelConfigRevision(t18Config(false), "discord")
	withOutbound := config.ChannelConfigRevision(t18Config(true), "discord")

	require.NotEqual(t, noOutbound, withOutbound,
		"T-18/FR-088: an outbound-only change must move the revision token, "+
			"or a concurrent outbound save can never make another save stale")

	// And a target-list change moves it too (the list is part of the token).
	altered := t18Config(true)
	alteredInst := altered.Channels["discord"]
	alteredInst.Outbound.Targets = []string{"discord:999"}
	altered.Channels["discord"] = alteredInst
	require.NotEqual(t, withOutbound, config.ChannelConfigRevision(altered, "discord"),
		"T-18/FR-088: a target-list change moves the revision token")
}

func TestT18_ChannelRevision_OutboundOnlyChangePreservesThePairingToken(t *testing.T) {
	// FR-015 / Persisted configuration: the pairing token must not move on
	// an outbound-only save, otherwise an active pairing would be interrupted.
	noOutbound := ChannelRevision(t18Config(false), "discord")
	withOutbound := ChannelRevision(t18Config(true), "discord")
	require.Equal(t, noOutbound, withOutbound,
		"T-18/FR-015: adding only outbound must preserve the pairing token")

	altered := t18Config(true)
	alteredInst := altered.Channels["discord"]
	alteredInst.Outbound.Targets = []string{"discord:999"}
	altered.Channels["discord"] = alteredInst
	require.Equal(t, withOutbound, ChannelRevision(altered, "discord"),
		"T-18/FR-015: changing only targets must preserve the pairing token")

	// Positive control: a genuine connection change still invalidates pairing.
	alteredInst.AllowFrom = []string{"new-user"}
	altered.Channels["discord"] = alteredInst
	require.NotEqual(t, withOutbound, ChannelRevision(altered, "discord"),
		"T-18/FR-015: the pairing token must still detect a connection-field change")

	// Preserve the original unknown-instance adapter-revision control. It is
	// not a claim about the config API's separate absent conflict token.
	require.Empty(t, ChannelRevision(t18Config(true), "no-such-instance"),
		"T-18: an unknown instance has no adapter revision")
}
