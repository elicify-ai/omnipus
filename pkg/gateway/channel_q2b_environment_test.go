package gateway

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The final founder Q1 B amendment supersedes §3's old post-click lookup rule.
// A resolver/connection outage with the SAME revision must not cause another
// request, another lookup or a refusal after the person approved valid targets.
func TestChannelsQ2B_Q1B_OutageAfterCardSavesWithoutSecondLookup(t *testing.T) {
	for _, outage := range []string{"resolver_unavailable", "connection_stopped"} {
		t.Run(outage, func(t *testing.T) {
			f := q7Fixture(t, "discord", "discord", "jim")
			sid := cq2Caller(t, f, "mia", false, false)
			require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
				inst := cfg.Channels["discord"]
				inst.Enabled = true
				cfg.Channels["discord"] = inst
				return config.SaveConfig(f.f1277.configPath, cfg)
			}))
			adapter := cq2DiscordEdge(f, true, "")
			var unavailable atomic.Bool
			adapter.lookup = func(context.Context, string) channels.OutboundTargetResult {
				if unavailable.Load() {
					return channels.OutboundTargetResult{Code: channels.TargetErrLookupUnavailable}
				}
				return channels.OutboundTargetResult{}
			}
			revision := t14Read(t, f, "discord").Revision
			args := q7Args("discord", "agent_may_start", revision, []string{"600", "601"})
			const callID = "cq2-q1b-outage"
			events := cq2Start(t, f, sid, "mia", callID, args)
			entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
			wantLookups := []string{"discord:600", "discord:601"}
			preCardLookups := adapter.lookupSnapshot()
			assert.Equal(t, wantLookups, preCardLookups, "Q1 B: every added target was verified BEFORE the card")
			// Flip only the network/lifecycle boundary, not the config token.
			unavailable.Store(true)
			if outage == "connection_stopped" {
				adapter.SetRunning(false)
			}
			require.Equal(t, revision, t14Read(t, f, "discord").Revision, "instrument: outage did not change settings revision")
			cq2Action(t, f, entry, "allow_once")
			q7WaitTurn(t, f, events, sid)
			assert.Equal(t, preCardLookups, adapter.lookupSnapshot(), "Q1 B: no second Discord lookup after approval, even when unavailable")
			q7Saved(t, &tools.ToolResult{ForLLM: f.provider.lastToolResult()}, "agent_may_start", wantLookups)
			t13AssertOutbound(t, f, "discord", "agent_may_start", wantLookups)
			require.Equal(t, 1, cq2RequestCount(f, sid, callID))
			rows := cq2OutcomeRows(t, f, sid, callID)
			require.Len(t, rows, 1)
			require.Equal(t, "saved", rows[0]["outcome"])
			require.Equal(t, entry.ApprovalID, rows[0]["approval_id"])
			for _, target := range wantLookups {
				assert.True(t, q7AuditContainsValue(rows[0], target), "Q1 B: saved outcome must identify the pre-card result for each saved target (%s), not only the target count", target)
			}
			// The contract gives no audit field names or result vocabulary.
			// Keep that missing oracle loud until team-lead specifies it; a
			// target's presence alone never certifies its verification status.
			t.Fatal("BLOCKED: per-target pre-card verification audit representation is unspecified — required by Q2-B contract final founder Q1 B amendment; define field/result vocabulary before CHECK")
		})
	}
}

func TestChannelsQ2B_Q1B_ChangedDiscordRevisionRefusesWithoutWriting(t *testing.T) {
	f := q7Fixture(t, "discord", "discord", "jim")
	sid := cq2Caller(t, f, "mia", false, false)
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		inst := cfg.Channels["discord"]
		inst.Enabled = true
		cfg.Channels["discord"] = inst
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	adapter := cq2DiscordEdge(f, true, "")
	revision := t14Read(t, f, "discord").Revision
	args := q7Args("discord", "agent_may_start", revision, []string{"600"})
	const callID = "cq2-q1b-stale"
	events := cq2Start(t, f, sid, "mia", callID, args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
	assert.Equal(t, []string{"discord:600"}, adapter.lookupSnapshot(), "Q1 B: pin follows actual pre-card target verification")
	// Change reply_only to agent_may_start with an empty list: a real policy
	// change, with no Discord additions and thus no concurrent-writer lookup.
	rec := t14Put(t, f, "discord", "agent_may_start", []string{}, revision)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEqual(t, revision, t14Read(t, f, "discord").Revision)
	before := t13Before(t, f)
	lookupsAtClick := adapter.lookupSnapshot()
	adapter.SetRunning(false)
	cq2Action(t, f, entry, "allow_once")
	q7WaitTurn(t, f, events, sid)
	t13Unchanged(t, f, before)
	failure := cq2Failure(t, f.provider.lastToolResult(), "CONFLICT", "stale_revision")
	assert.Equal(t, cq2AfterCard, failure["message"])
	require.Equal(t, lookupsAtClick, adapter.lookupSnapshot(), "Q1 B: stale revision wins without a post-card lookup")
	rows := cq2OutcomeRows(t, f, sid, callID)
	require.Len(t, rows, 1)
	require.Equal(t, "refused", rows[0]["outcome"])
	require.Equal(t, "stale_revision", rows[0]["reason"])
	require.Equal(t, entry.ApprovalID, rows[0]["approval_id"])
}
