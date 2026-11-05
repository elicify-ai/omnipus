package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cq2RefusalCase struct {
	name, platform, code, reason string
	targets                      []string
}

// Cases derive from Q2-B §3/§6.8. invalid_targets equivalence classes and the
// 20/21 distinct-target boundary are separate, never collapsed into "any error".
func cq2WriterCases() []cq2RefusalCase {
	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = fmt.Sprint(200 + i)
	}
	return []cq2RefusalCase{
		{"malformed_id", "telegram", "INVALID_TARGETS", "invalid_targets", []string{"not-an-id"}},
		{"wildcard", "telegram", "INVALID_TARGETS", "invalid_targets", []string{"*"}},
		{"username", "telegram", "INVALID_TARGETS", "invalid_targets", []string{"@alice"}},
		{"wrong_platform", "telegram", "INVALID_TARGETS", "invalid_targets", []string{"discord:222"}},
		{"too_many_targets", "telegram", "INVALID_TARGETS", "too_many_targets", tooMany},
		{"platform_cannot_start", "slack", "PLATFORM_CANNOT_START", "platform_cannot_start", []string{}},
		{"platform_cannot_message_people", "dingtalk", "PLATFORM_CANNOT_START", "platform_cannot_message_people", []string{"222"}},
		{"connect_first", "discord", "CONNECT_FIRST", "connect_first", []string{"222"}},
		{"lookup_unavailable", "discord", "LOOKUP_UNAVAILABLE", "lookup_unavailable", []string{"222"}},
		{"not_found", "discord", "INVALID_TARGETS", "invalid_targets", []string{"222"}},
		{"unsupported_channel_kind", "discord", "INVALID_TARGETS", "invalid_targets", []string{"222"}},
		{"saved_live_mismatch", "discord", "CONFLICT", "saved_live_mismatch", []string{"222"}},
	}
}

func cq2DiscordEdge(f *wu6Fixture, running bool, result string) *t13Adapter {
	adapter := &t13Adapter{BaseChannel: channels.NewBaseChannel("discord", nil, f.bus, nil)}
	adapter.SetRunning(running)
	adapter.lookup = func(context.Context, string) channels.OutboundTargetResult {
		return channels.OutboundTargetResult{Code: channels.TargetErrorCode(result)}
	}
	f.svc.ChannelManager.RegisterChannel("discord", adapter)
	f.svc.ChannelManager.SetPolicySource(f.al.GetConfig)
	return adapter
}

func cq2RefusalSetup(t *testing.T, tc cq2RefusalCase) (*wu6Fixture, string, map[string]any) {
	t.Helper()
	f := q7Fixture(t, tc.platform, tc.platform, "jim")
	sid := cq2Caller(t, f, "mia", false, false)
	if tc.platform == "discord" {
		require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
			inst := cfg.Channels["discord"]
			inst.Enabled = true
			cfg.Channels["discord"] = inst
			return config.SaveConfig(f.f1277.configPath, cfg)
		}))
		edgeCode := ""
		if tc.name == "not_found" || tc.name == "unsupported_channel_kind" || tc.name == "lookup_unavailable" {
			edgeCode = tc.name
		}
		cq2DiscordEdge(f, tc.name != "connect_first", edgeCode)
		if tc.name == "saved_live_mismatch" {
			t32FaultRefresh(t, f)
			t32FailedCommonSave(t, f, false, t32RebindDoc(t))
		}
	}
	args := q7Args(tc.platform, "agent_may_start", t14Read(t, f, tc.platform).Revision, tc.targets)
	return f, sid, args
}

func TestChannelsQ2B08_AllWriterValidityChecksPrecedeApproval(t *testing.T) {
	for _, tc := range cq2WriterCases() {
		t.Run(tc.name, func(t *testing.T) {
			f, sid, args := cq2RefusalSetup(t, tc)
			before := t13Before(t, f)
			callID := "cq2-invalid-" + tc.name
			events := cq2Start(t, f, sid, "mia", callID, args)
			cq2NoCard(t, f, events, sid, callID)
			t13Unchanged(t, f, before)
			cq2RefusedAudit(t, f, sid, "mia", callID, tc.platform, tc.reason)
			failure := cq2Failure(t, f.provider.lastToolResult(), tc.code, tc.reason)
			if tc.name == "not_found" || tc.name == "unsupported_channel_kind" {
				items, itemsOK := cq2JSON(t, failure["target_errors"]).([]any)
				require.True(t, itemsOK, "instrument: target errors are a JSON array")
				require.Len(t, items, 1)
				item, itemOK := items[0].(map[string]any)
				require.True(t, itemOK, "instrument: per-target error is an object")
				require.Equal(t, tc.name, item["code"], "Q2-B: per-target rejection is preserved")
			}
		})
	}
	t.Run("accepted_target_boundaries", func(t *testing.T) {
		// Q2-B §3: distinct canonical target limit 20. Length -1 is not
		// representable; 0/1/19/20 and 21 above cover both ends.
		for _, count := range []int{0, 1, 19, 20} {
			t.Run(fmt.Sprint(count), func(t *testing.T) {
				f := q7Fixture(t, "telegram", "telegram", "jim")
				sid := cq2Caller(t, f, "mia", false, false)
				input, want := []string{}, []string{}
				for i := range count {
					id := fmt.Sprint(200 + i)
					input, want = append(input, id), append(want, "telegram:"+id)
				}
				if count == 20 {
					input = append(input, "telegram:200") // 21 raw but 20 canonical distinct.
				}
				args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, input)
				events := cq2Start(t, f, sid, "mia", "cq2-boundary", args)
				entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, "cq2-boundary")
				cq2Action(t, f, entry, "allow_once")
				q7WaitTurn(t, f, events, sid)
				t13AssertOutbound(t, f, "telegram", "agent_may_start", want)
			})
		}
	})
}

func TestChannelsQ2B10_PreCardRefusalsHaveExactlyOneCorrelatedAudit(t *testing.T) {
	for _, tc := range cq2WriterCases() {
		t.Run(tc.name, func(t *testing.T) {
			f, sid, args := cq2RefusalSetup(t, tc)
			callID := "cq2-audit-" + tc.name
			before := t13Before(t, f)
			events := cq2Start(t, f, sid, "mia", callID, args)
			cq2NoCard(t, f, events, sid, callID)
			cq2RefusedAudit(t, f, sid, "mia", callID, tc.platform, tc.reason)
			t13Unchanged(t, f, before)
		})
	}
	for _, kind := range []string{"own_connection", "invalid_input", "not_configured"} {
		t.Run(kind, func(t *testing.T) {
			bound := "jim"
			if kind == "own_connection" {
				bound = "mia"
			}
			f := q7Fixture(t, "telegram", "telegram", bound)
			sid := cq2Caller(t, f, "mia", false, false)
			args := q7Args("telegram", "reply_only", t14Read(t, f, "telegram").Revision, []string{})
			code := "OWN_CONNECTION"
			if kind == "invalid_input" {
				delete(args, "policy")
				code = "INVALID_INPUT"
			}
			if kind == "not_configured" {
				args["id"], code = "unconfigured-q2b", "CHANNEL_NOT_FOUND"
			}
			callID := "cq2-admission-" + kind
			before := t13Before(t, f)
			events := cq2Start(t, f, sid, "mia", callID, args)
			cq2NoCard(t, f, events, sid, callID)
			cq2Failure(t, f.provider.lastToolResult(), code, "")
			channelID, channelIDOK := args["id"].(string)
			require.True(t, channelIDOK, "instrument: refusal fixture identifies a string channel id")
			cq2RefusedAudit(t, f, sid, "mia", callID, channelID, kind)
			t13Unchanged(t, f, before)
		})
	}
	t.Run("omnipus_only", func(t *testing.T) {
		f := q7Fixture(t, "telegram", "telegram", "jim")
		sid := cq2Caller(t, f, "omnipus", false, false)
		// The loop always has an identified agent. Exercise the real preparer
		// directly for the contract's missing-caller admission equivalence class.
		const callID = "cq2-no-caller"
		ctx := tools.WithTranscriptSessionID(context.Background(), sid)
		ctx = tools.WithToolCallID(ctx, callID)
		ctx = tools.WithTurnID(ctx, "cq2-missing-caller-turn")
		before := t13Before(t, f)
		_, _, refusal := cq2RealPreparer(t, f).PrepareForApproval(ctx, q7Args("telegram", "reply_only", t14Read(t, f, "telegram").Revision, []string{}))
		require.NotNil(t, refusal)
		q7Error(t, refusal, "OMNIPUS_ONLY")
		require.Zero(t, cq2RequestCount(f, sid, callID))
		cq2RefusedAudit(t, f, sid, "", callID, "telegram", "omnipus_only")
		t13Unchanged(t, f, before)
	})
}

func TestChannelsQ2B17_PreviewAndWriterHaveIdenticalRefusalCodeAndText(t *testing.T) {
	cases := append(cq2WriterCases(), cq2RefusalCase{"stale_revision", "telegram", "CONFLICT", "stale_revision", []string{"222"}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, sid, args := cq2RefusalSetup(t, tc)
			if tc.name == "stale_revision" {
				args["expected_revision"] = "made-up-revision"
			}
			preparer := cq2RealPreparer(t, f)
			before := t13Before(t, f)
			// Drive the loop too: it must expose the same real preview refusal
			// without creating an approval, not merely offer a passing helper.
			callID := "cq2-parity-" + tc.name
			events := cq2Start(t, f, sid, "mia", callID, args)
			cq2NoCard(t, f, events, sid, callID)
			loopFailure := cq2Failure(t, f.provider.lastToolResult(), tc.code, tc.reason)
			ctx := tools.WithAgentID(context.Background(), "mia")
			_, _, refusal := preparer.PrepareForApproval(ctx, args)
			require.NotNil(t, refusal)
			previewFailure := cq2Failure(t, refusal.ForLLM, tc.code, tc.reason)
			// Compare the actual shared validator/writer, not the tool adapter's
			// deliberately different after-card conflict explanation (§2).
			expectedRevision, revisionOK := args["expected_revision"].(string)
			require.True(t, revisionOK, "instrument: parity fixture supplies an explicit string revision")
			_, err := f.api.applyOutboundChange(ctx, tc.platform, gen.ChannelOutboundUpdateRequestPolicyAgentMayStart,
				tc.targets, expectedRevision, "mia")
			var writer *outboundRejectionError
			require.True(t, errors.As(err, &writer), "Q2-B: real one-writer refuses this input with its structured rejection: %v", err)
			assert.Equal(t, tc.reason, string(writer.Body.Code))
			assert.Equal(t, writer.Body.Error, previewFailure["message"], "Q2-B: shared validation cannot drift in safe refusal text")
			assert.Equal(t, previewFailure, loopFailure, "Q2-B: loop publishes the real preparer's exact refusal")
			t13Unchanged(t, f, before)
		})
	}
}
