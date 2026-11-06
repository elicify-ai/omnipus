package config

// guard_outbound_test.go — T-21b (unit half), the outbound guard GuardOutbound
// over the T-8 rules (channels-spec-r8 T-8, FR-014, FR-018; S-38 rows;
// SC-013; M-02). The integration half (generic PUT through the real stack,
// casing on the wire, one row per save-path caller) is T-14/T-21b-integration
// and S-38's own When/Then — not this file.
//
// Oracle: the T-8 rule table, row by row — cleared / kept / restored /
// refused, the `changed` list (ids the guard cleared or restored, sorted),
// and "the guard never touches any other field". Rule combinations are used
// so no row can pass vacuously against a guard that does nothing: every test
// pairs the kept-rule instance with a cleared/restored instance in the same
// candidate, and asserts the changed list.
//
// RED expectation: the WP-0 shell returns (nil, nil) and adjusts nothing, so
// every clearing/restoration/refusal assertion fails; rule-2-style kept rows
// are never asserted alone (documented in the RED report).
//
// Reported spec gap (not tested): the table's conditions are defined over
// `before ∪ candidate`; a FIRST write (before nil) whose generic candidate
// introduces an outbound setting matches none of the rows cleanly — named in
// the RED report for the backend lead/architect, not guessed here.

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// t21bBoundInstance builds an instance bound to (workspace, agent) with an
// outbound setting of policy + targets.
func t21bBoundInstance(typ, workspace, agent, policy string, targets ...string) ChannelInstanceConfig {
	inst := ChannelInstanceConfig{
		Type:        typ,
		Enabled:     true,
		WorkspaceID: workspace,
		Identity:    &ChannelIdentity{Kind: "agent", ID: agent},
	}
	if policy != "" {
		inst.Outbound = &ChannelOutboundConfig{Policy: policy, Targets: targets}
	}
	return inst
}

func t21bOutboundEquals(inst ChannelInstanceConfig, policy string, targets ...string) bool {
	if inst.Outbound == nil {
		return policy == ""
	}
	if inst.Outbound.Policy != policy || len(inst.Outbound.Targets) != len(targets) {
		return false
	}
	for i, v := range targets {
		if inst.Outbound.Targets[i] != v {
			return false
		}
	}
	return true
}

const t21bWS = "ws-guard"
const t21bAgent = "mia"

func TestT21b_GuardOutbound_Rule1_RebindClearsOutbound(t *testing.T) {
	// S-38 row 6 at the unit level: a whole-object write rebinds identity to
	// agent B and carries the old outbound — the binding reset (FD-3) wins,
	// whatever the writer.
	before := &Config{Channels: map[string]ChannelInstanceConfig{
		"discord": t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:222"),
	}}
	rebound := t21bBoundInstance("discord", t21bWS, "agent-b", "agent_may_start", "discord:222")
	candidate := &Config{Channels: map[string]ChannelInstanceConfig{"discord": rebound}}

	changed, err := GuardOutbound(before, candidate, false)
	require.NoError(t, err, "rule 1: a rebind is cleared, not refused")
	got := candidate.Channels["discord"]
	require.Nil(t, got.Outbound,
		"T-8 rule 1: a changed pair clears candidate outbound whatever the writer carried")
	// The guard never touches any other field: the rebound identity stays.
	require.Equal(t, "agent-b", got.Identity.ID, "rule 1 clears only the outbound")
	changedWant := []string{"discord"}
	require.Equal(t, changedWant, changed, "rule 1: changed lists the cleared instance, sorted")
}

func TestT21b_GuardOutbound_Rule1_WorkspaceChangeAndBoundToUnboundClear(t *testing.T) {
	before := &Config{Channels: map[string]ChannelInstanceConfig{
		"discord":   t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:222"),
		"discord.b": t21bBoundInstance("discord", t21bWS, t21bAgent, "reply_only", "discord:333"),
	}}
	// discord moves workspace; namespaced discord.b goes bound → unbound.
	moved := t21bBoundInstance("discord", "ws-other", t21bAgent, "agent_may_start", "discord:222")
	unbound := t21bBoundInstance("discord", "", t21bAgent, "reply_only", "discord:333")
	unbound.Identity = nil
	candidate := &Config{Channels: map[string]ChannelInstanceConfig{
		"discord": moved, "discord.b": unbound,
	}}

	changed, err := GuardOutbound(before, candidate, false)
	require.NoError(t, err)
	require.Nil(t, candidate.Channels["discord"].Outbound,
		"rule 1: a workspace change clears the outbound")
	require.Nil(t, candidate.Channels["discord.b"].Outbound,
		"rule 1: bound → unbound clears the outbound (namespaced instance)")
	require.Equal(t, []string{"discord", "discord.b"}, changed,
		"rule 1: changed lists both cleared ids, sorted")
}

func TestT21b_GuardOutbound_Rule2_DedicatedKeptWhileReboundPeerCleared(t *testing.T) {
	// Rule 2 (dedicated → kept) is asserted together with a rule-1 peer so
	// the row cannot pass against a guard that does nothing at all.
	before := &Config{Channels: map[string]ChannelInstanceConfig{
		"discord": t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:222"),
		"slack":   t21bBoundInstance("slack", t21bWS, t21bAgent, "reply_only", "slack:C1"),
	}}
	candidate := &Config{Channels: map[string]ChannelInstanceConfig{
		// the dedicated writer's new list
		"discord": t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:999"),
		// a stale whole-object copy that rebinds slack
		"slack": t21bBoundInstance("slack", t21bWS, "agent-b", "reply_only", "slack:C1"),
	}}

	changed, err := GuardOutbound(before, candidate, true)
	require.NoError(t, err)
	require.True(t, t21bOutboundEquals(candidate.Channels["discord"], "agent_may_start", "discord:999"),
		"rule 2: a dedicated write keeps exactly what the writer set")
	require.Nil(t, candidate.Channels["slack"].Outbound,
		"rule 1 still applies beside a dedicated write: the rebound peer is cleared")
	require.Equal(t, []string{"slack"}, changed,
		"changed lists only the cleared peer, not the dedicated instance")
}

func TestT21b_GuardOutbound_Rule3_OmissionRestoresStoredValue(t *testing.T) {
	// S-38 row 3 at the unit level: a stale whole-object copy without
	// outbound must not drop the grant by omission; the other changed field
	// saves normally.
	before := &Config{Channels: map[string]ChannelInstanceConfig{
		"discord": t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:222"),
	}}
	stale := t21bBoundInstance("discord", t21bWS, t21bAgent, "" /* no outbound */)
	stale.Outbound = nil
	stale.Enabled = false // the stale copy's own change
	candidate := &Config{Channels: map[string]ChannelInstanceConfig{"discord": stale}}

	changed, err := GuardOutbound(before, candidate, false)
	require.NoError(t, err)
	require.NotNil(t, candidate.Channels["discord"].Outbound,
		"rule 3: a non-dedicated candidate with absent outbound gets the stored value restored")
	require.True(t, t21bOutboundEquals(candidate.Channels["discord"], "agent_may_start", "discord:222"),
		"rule 3: the restored value is exactly the stored one (policy and targets)")
	require.False(t, candidate.Channels["discord"].Enabled,
		"the guard never touches any other field: the candidate's own change stays")
	require.Equal(t, []string{"discord"}, changed, "rule 3: the restored id is listed")
}

func TestT21b_GuardOutbound_Rule4_EqualEchoKeptWhileOmittedPeerRestored(t *testing.T) {
	// Rule 4 (equal echo → kept) asserted beside a rule-3 peer so the row
	// cannot pass against a guard that does nothing at all.
	before := &Config{Channels: map[string]ChannelInstanceConfig{
		"discord": t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:222"),
		"slack":   t21bBoundInstance("slack", t21bWS, t21bAgent, "reply_only", "slack:C1"),
	}}
	echo := t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:222")
	omitted := t21bBoundInstance("slack", t21bWS, t21bAgent, "")
	omitted.Outbound = nil
	candidate := &Config{Channels: map[string]ChannelInstanceConfig{
		"discord": echo, "slack": omitted,
	}}

	changed, err := GuardOutbound(before, candidate, false)
	require.NoError(t, err)
	require.True(t, t21bOutboundEquals(candidate.Channels["discord"], "agent_may_start", "discord:222"),
		"rule 4: an echoed stored value is kept")
	require.True(t, t21bOutboundEquals(candidate.Channels["slack"], "reply_only", "slack:C1"),
		"rule 3 beside rule 4: the omitted peer is restored")
	require.Equal(t, []string{"slack"}, changed,
		"rule 4's kept instance is not in changed; only the restored peer is")
}

func TestT21b_GuardOutbound_Rule5_NonDedicatedChangeRefused(t *testing.T) {
	// S-38 row 1 at the unit level: a generic writer that changes the list is
	// refused before writing (REST maps the error to its 403 text — T-14's
	// surface).
	before := &Config{Channels: map[string]ChannelInstanceConfig{
		"discord": t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:222"),
	}}
	changedList := t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:999")
	candidate := &Config{Channels: map[string]ChannelInstanceConfig{"discord": changedList}}

	_, err := GuardOutbound(before, candidate, false)
	require.Error(t, err, "rule 5: a non-dedicated outbound change is refused")
	require.True(t, errors.Is(err, ErrOutboundProtected),
		"rule 5: the refusal is ErrOutboundProtected; got %v", err)

	// A policy flip alone is equally refused (present and different).
	flip := t21bBoundInstance("discord", t21bWS, t21bAgent, "reply_only", "discord:222")
	candidate2 := &Config{Channels: map[string]ChannelInstanceConfig{"discord": flip}}
	_, err = GuardOutbound(before, candidate2, false)
	require.True(t, errors.Is(err, ErrOutboundProtected),
		"rule 5: a policy flip is present-and-different, refused; got %v", err)
}

func TestT21b_GuardOutbound_ChangedListSortedAcrossRules(t *testing.T) {
	// changed is the sorted list of ids the guard cleared or restored across
	// all rules; kept ids (equal echo, dedicated) stay out.
	before := &Config{Channels: map[string]ChannelInstanceConfig{
		"a1": t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:1"),
		"b2": t21bBoundInstance("slack", t21bWS, t21bAgent, "reply_only", "slack:C2"),
		"c3": t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:3"),
	}}
	candidate := &Config{Channels: map[string]ChannelInstanceConfig{
		"a1": t21bBoundInstance("discord", t21bWS, "agent-b", "agent_may_start", "discord:1"), // cleared (rule 1)
		"b2": t21bBoundInstance("slack", t21bWS, t21bAgent, ""),                               // restored (rule 3)
		"c3": t21bBoundInstance("discord", t21bWS, t21bAgent, "agent_may_start", "discord:3"), // kept (rule 4)
	}}

	changed, err := GuardOutbound(before, candidate, false)
	require.NoError(t, err)
	require.Equal(t, []string{"a1", "b2"}, changed,
		"changed: sorted ids of cleared and restored instances only")
}
