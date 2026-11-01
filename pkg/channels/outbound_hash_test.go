package channels

// outbound_hash_test.go — T-18, the hash/revision split (channels-spec-r8
// T-18, FR-015, FR-088 row "FR-088 revision", S-23's unit half; mutation-list
// entry "include Outbound in toChannelHashes → S-23 fails").
//
// Oracle (spec, "Persisted configuration" table):
//   - the RESTART hash (toChannelHashes) compares the ADAPTER VIEW
//     (config.ChannelAdapterView — the instance with Outbound cleared): a
//     list-only save locks no pairing order, stamps no generation, restarts
//     nothing (FR-015, M-01);
//   - the FR-088 revision token (ChannelRevision) stays computed over the
//     whole instance INCLUDING Outbound — it is deliberately NOT the adapter
//     view: a concurrent outbound change must make another save stale (409).
//
// Today both are the same function (ChannelRevision = toChannelHashes()[name]
// and the hash marshals the whole instance), so:
//   - the equality row (restart hash ignores a list-only change) FAILS — RED;
//   - the difference row (the revision token moves on an outbound-only
//     change) PASSES today and guards the fix's direction: an implementation
//     that "fixes" the hash by dropping Outbound from the revision token
//     fails this row.
//
// The integration half — adapter identity, generation, fingerprint and the
// active pairing across a real reload — is T-18b (S-23), a separate file.

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

func TestT18_ChannelRevision_OutboundChangeMovesTheConflictToken(t *testing.T) {
	// FR-088: the revision token is deliberately NOT the adapter view — an
	// outbound-only change must move it so a concurrent save turns stale
	// (409). Passes today (the token marshals the whole instance); it fails
	// any fix that drops Outbound from the token.
	noOutbound := ChannelRevision(t18Config(false), "discord")
	withOutbound := ChannelRevision(t18Config(true), "discord")

	require.NotEqual(t, noOutbound, withOutbound,
		"T-18/FR-088: an outbound-only change must move the revision token, "+
			"or a concurrent outbound save can never make another save stale")

	// And a target-list change moves it too (the list is part of the token).
	altered := t18Config(true)
	alteredInst := altered.Channels["discord"]
	alteredInst.Outbound.Targets = []string{"discord:999"}
	altered.Channels["discord"] = alteredInst
	require.NotEqual(t, withOutbound, ChannelRevision(altered, "discord"),
		"T-18/FR-088: a target-list change moves the revision token")

	// Unknown instance: empty revision, not a crash.
	require.Empty(t, ChannelRevision(t18Config(true), "no-such-instance"),
		"FR-088: an unknown instance has no revision")
}
