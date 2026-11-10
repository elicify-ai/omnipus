// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// CHECK r1 F1 / pr-test r1 F1-F2, spec BDD-8.1/8.2/8.3/8.7:
// real approved connect journeys must prove startup with card-bound
// credentials, distinguish a controlled platform 401, and refuse a non-member
// before changing configuration, selecting references, reloading or probing.
// Founder ruling 2026-10-20 (M5, option A): channel-card consent covers account,
// settings and credential field. Routing/binding/enable changes are consented
// separately by the connect approval. Both the member control and non-member
// request below use the same canonical descriptor and otherwise identical input.
package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWU7ConnectJourney_SuccessfulConnection_ProvenFactsAndReadback(t *testing.T) {
	f := newWU7LiveFixture(t)
	attempt := wu7ConnectJourney(t, f, "mia", issue1277ConnectAccess)
	wu7RequireWorking(t, f, attempt)
}

func TestWU7ConnectJourney_RejectedToken401_CountedProbe_SavedButNotWorking(t *testing.T) {
	f := newWU7LiveFixture(t)
	attempt := wu7ConnectJourney(t, f, "mia", issue1277UnauthorizedAccess)
	wu7RequireRejectedCredential(t, f, attempt)
}

func wu7RequireWorking(t *testing.T, f *wu7LiveFixture, attempt wu7ConnectAttempt) {
	t.Helper()
	// The distinct bearer is used only by THIS attempted connection: a boot
	// request from the fixture's healthy LINE channel cannot satisfy this.
	require.Greater(t, attempt.after.acceptedProbes, attempt.before.acceptedProbes,
		"the saved credential must actually reach the platform during the approved connect")
	assert.Greater(t, attempt.after.reloads, attempt.before.reloads, "connect must await a real reload")
	assert.Equal(t, tools.OutcomeWorking, wu7String(t, attempt.report, "outcome"))
	assert.Equal(t, "the channel's start after the configuration reload", wu7String(t, attempt.report, "check_scope"))
	var proven []string
	require.NoError(t, json.Unmarshal(attempt.report["proven"], &proven))
	assert.Equal(t, []string{"the channel started after the reload with the saved configuration and credentials"}, proven)
	wu7RequireUntested(t, attempt.report)
	wu7RequireSavedBinding(t, f, attempt)
	channel, exists := f.al.GetChannelManager().GetChannel(attempt.instance)
	require.True(t, exists, "the saved instance must be in the live manager")
	assert.True(t, channel.IsRunning(), "the successful connect must leave its real channel running")
	wu7RequireValueFree(t, attempt.report, issue1277ConnectAccess)
}

func wu7RequireRejectedCredential(t *testing.T, f *wu7LiveFixture, attempt wu7ConnectAttempt) {
	t.Helper()
	// Count the real rejected request BEFORE checking its reported outcome.
	// No exact retry count is specified; at least one credential-bearing 401
	// is required, and a dead reload/probe must not satisfy the test.
	require.Greater(t, attempt.after.unauthorizedProbes, attempt.before.unauthorizedProbes,
		"the saved credential must receive a controlled platform 401, not fail earlier in the fixture")
	outcome := wu7String(t, attempt.report, "outcome")
	t.Logf("WU7_CONTROLLED_401 before=%d after=%d reported_outcome=%q",
		attempt.before.unauthorizedProbes, attempt.after.unauthorizedProbes, outcome)
	assert.Equal(t, tools.OutcomeSavedNotWorking, outcome,
		"BDD-8.3: an authenticated platform 401 must never be certified as working")
	assert.Greater(t, attempt.after.reloads, attempt.before.reloads, "connect must await a real reload")
	var proven []string
	if raw, present := attempt.report["proven"]; present {
		require.NoError(t, json.Unmarshal(raw, &proven))
	}
	assert.Empty(t, proven, "a 401 cannot prove a working credential")
	wu7RequireUntested(t, attempt.report)
	wu7RequireSavedBinding(t, f, attempt)
	if channel, exists := f.al.GetChannelManager().GetChannel(attempt.instance); exists {
		assert.False(t, channel.IsRunning(), "a rejected credential must not leave a falsely healthy connection")
	}
	wu7RequireValueFree(t, attempt.report, issue1277UnauthorizedAccess)
}

func wu7RequireMembershipRule(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name, target string
		member       bool
	}{
		{name: "member_control", target: "mia", member: true},
		{name: "non_member_only_target_changes", target: "omnipus", member: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWU7LiveFixture(t)
			attempt := wu7ConnectJourney(t, f, tc.target, issue1277ConnectAccess)
			if tc.member {
				wu7RequireWorking(t, f, attempt)
				return
			}
			assert.Equal(t, tools.OutcomeFailedNothingSaved, wu7String(t, attempt.report, "outcome"))
			var refusal map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(attempt.report["error"], &refusal))
			assert.Equal(t, "INVALID_BINDING", wu7String(t, refusal, "code"), "pin the membership guard, not another refusal")
			assert.Equal(t, "agent omnipus is not a member of workspace sales; failed: nothing saved",
				wu7String(t, refusal, "message"))
			wu7RequireUnchanged(t, attempt.before, attempt.after)
			// The earlier approved configure created the disabled stub. Nothing
			// saved by CONNECT means that exact existing state is unchanged,
			// not that the configure/card ceremony is erased.
			entry, exists := f.al.GetConfig().Channels[attempt.instance]
			require.True(t, exists)
			assert.Empty(t, entry.WorkspaceID)
			assert.Nil(t, entry.Identity)
			assert.False(t, entry.Enabled)
			for ref, value := range map[string]string{
				attempt.secretRef: "qa1277-connect-secret", attempt.accessRef: issue1277ConnectAccess,
			} {
				stored, err := f.store.Get(ref)
				require.NoError(t, err)
				assert.Equal(t, value, stored, "the refused connect must not alter card-stored values")
			}
		})
	}
}

func wu7RequireSavedBinding(t *testing.T, f *wu7LiveFixture, attempt wu7ConnectAttempt) {
	t.Helper()
	disk, err := config.LoadConfigWithStore(f.f1277.configPath, f.store)
	require.NoError(t, err)
	live, present := f.al.GetConfig().Channels[attempt.instance]
	require.True(t, present)
	durable, present := disk.Channels[attempt.instance]
	require.True(t, present, "the approved connect must be durable, not merely in memory")
	assert.Equal(t, durable, live, "durable and live channel settings must agree")
	assert.Equal(t, attempt.workspace, durable.WorkspaceID)
	require.NotNil(t, durable.Identity)
	assert.Equal(t, config.ChannelIdentity{Kind: "agent", ID: attempt.agentID}, *durable.Identity)
	assert.True(t, durable.Enabled)
	assert.Equal(t, attempt.secretRef, durable.ChannelSecretRef)
	assert.Equal(t, attempt.accessRef, durable.ChannelAccessTokenRef)
	assert.Equal(t, attempt.before.inventory, attempt.after.inventory, "select existing references without rewriting their stored metadata")
	assert.Equal(t, attempt.before.credentialBytes, attempt.after.credentialBytes, "connect must not rewrite card values")
}

func wu7RequireUntested(t *testing.T, report map[string]json.RawMessage) {
	t.Helper()
	var untested, missing []string
	require.NoError(t, json.Unmarshal(report["not_tested"], &untested))
	require.NoError(t, json.Unmarshal(report["missing_parts"], &missing))
	assert.ElementsMatch(t, []string{"inbound message delivery and routing to the agent", "outbound replies"}, untested,
		"BDD-8.2: platform startup proves neither inbound routing nor outbound replies")
	assert.Equal(t, []string{}, missing)
}

func wu7RequireValueFree(t *testing.T, report map[string]json.RawMessage, accessValue string) {
	t.Helper()
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "qa1277-connect-secret")
	assert.NotContains(t, string(raw), accessValue)
	withoutNotWorking := strings.ReplaceAll(wu7String(t, report, "outcome"), "not working", "")
	if accessValue == issue1277UnauthorizedAccess {
		assert.NotContains(t, withoutNotWorking, "working", "retain the original rejected-credential oracle")
	}
}
