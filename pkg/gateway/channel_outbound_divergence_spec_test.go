package gateway

// T-32: normative S-25/S-28/T-6b persistence, permission sets and recovery.
// The real outbound REST writer is never replaced by a test implementation.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestT32_SavedNotRefreshed_ReplyOnlyIsDurableLiveAndReadBackAtTheReturnedRevision(t *testing.T) {
	f, _ := t32Fixture(t, "discord", []string{"discord:222"})
	t32FaultRefresh(t, f)
	r1 := t14Read(t, f, "discord").Revision
	rec := t14Put(t, f, "discord", "reply_only", []string{"222"}, r1)
	require.Equal(t, http.StatusOK, rec.Code, "S-25: saved revocation is enforced despite failed refresh: %s", rec.Body.String())
	var written gen.ChannelOutboundWriteResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &written))
	require.Equal(t, "reply_only", string(written.Outbound.Policy))
	require.Equal(t, []string{"discord:222"}, written.Outbound.Targets)
	require.NotNil(t, written.Warning, "T-6b: live-refresh failure must not be reported as an unqualified save")
	require.True(t, strings.HasPrefix(*written.Warning, "Saved and in force for starting messages."), "S-25 exact warning prefix: %s", *written.Warning)
	require.NotEqual(t, r1, written.Revision)
	require.Equal(t, written.Revision, t14Read(t, f, "discord").Revision, "response uses the new live conflict token")
	t13AssertOutbound(t, f, "discord", "reply_only", []string{"discord:222"})
	t32Binding(t, f, "discord", "mia", "mia")
}

func TestT32_ReadFailureBeforeWrite_ChangesNothingAndNeverClaimsSaved(t *testing.T) {
	f, _ := t32Fixture(t, "discord", []string{"discord:222"})
	before := t13Before(t, f)
	revision := t14Read(t, f, "discord").Revision
	path, backup := f.f1277.configPath, f.f1277.configPath+".t32-read-fixture"
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700)) // real config read errors on a directory.
	var once sync.Once
	restore := func() {
		once.Do(func() { require.NoError(t, os.Remove(path)); require.NoError(t, os.Rename(backup, path)) })
	}
	t.Cleanup(restore)
	rec := t14Put(t, f, "discord", "reply_only", []string{"222"}, revision)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.JSONEq(t, `{"error":"could not save the setting; nothing was changed"}`, rec.Body.String(), "S-25: exact pre-write failure, not a saved warning")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "instrument: rejected read did not replace the failing filesystem entry")
	restore()
	t13Unchanged(t, f, before)
}

func TestT32_S28_PermissionSetCasesNeverTransferOrSilentlyExtendAGrant(t *testing.T) {
	cases := []struct {
		name, shape, policy string
		candidate, want     []string
		refused             bool
	}{
		{"a_rebind_cleared_saved_is_not_a_live_subset_exemption", "rebind", "agent_may_start", []string{"222"}, nil, true},
		{"b_full_revocation_across_rebind_is_unconditional", "rebind", "reply_only", []string{"222"}, []string{"discord:222"}, false},
		{"c_pure_removal_survives_nonbinding_divergence", "allow_from", "agent_may_start", []string{"222"}, []string{"discord:222"}, false},
		{"d_subset_of_both_target_sets_is_allowed", "targets", "agent_may_start", []string{"222"}, []string{"discord:222"}, false},
		{"e_live_subset_that_extends_saved_is_refused", "targets", "agent_may_start", []string{"222", "333"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targets := []string{"discord:222", "discord:333"}
			if tc.shape == "rebind" {
				targets = []string{"discord:222"}
			}
			f, _ := t32Fixture(t, "discord", targets)
			t32FaultRefresh(t, f)
			savedAgent := "mia"
			switch tc.shape {
			case "rebind":
				t32FailedCommonSave(t, f, false, t32RebindDoc)
				savedAgent = "qa-b"
				require.Nil(t, t32Saved(t, f, "discord")["outbound"], "T-8: real non-dedicated guard clears a rebound instance")
			case "allow_from":
				t32FailedCommonSave(t, f, false, func(doc map[string]any) error {
					doc["channels"].(map[string]any)["discord"].(map[string]any)["allow_from"] = []string{"qa-saved-sender"}
					return nil
				})
				require.Equal(t, []any{"qa-saved-sender"}, t32Saved(t, f, "discord")["allow_from"])
				require.Empty(t, f.al.GetConfig().Channels["discord"].AllowFrom, "instrument: non-binding field really diverges")
			case "targets":
				t32FailedCommonSave(t, f, true, t32SmallerSavedDoc)
				require.Equal(t, []any{"discord:222"}, t32Saved(t, f, "discord")["outbound"].(map[string]any)["targets"])
				require.Equal(t, []string{"discord:222", "discord:333"}, []string(f.al.GetConfig().Channels["discord"].Outbound.Targets))
			}
			t32Binding(t, f, "discord", savedAgent, "mia")
			if tc.refused {
				t32RequireMismatch(t, f, "discord", tc.candidate)
			} else {
				rec := t14Put(t, f, "discord", tc.policy, tc.candidate, t14Read(t, f, "discord").Revision)
				require.Equal(t, http.StatusOK, rec.Code, "S-28: non-extending write is not blocked by divergence: %s", rec.Body.String())
				t13AssertOutbound(t, f, "discord", tc.policy, tc.want)
			}
			t32Binding(t, f, "discord", savedAgent, "mia")
		})
	}
}

func TestT32_S28F_SuccessfulRefreshMakesThePreviouslyRefusedExtensionAvailable(t *testing.T) {
	f, _ := t32Fixture(t, "discord", []string{"discord:222", "discord:333"})
	restore := t32FaultRefresh(t, f)
	t32FailedCommonSave(t, f, true, t32SmallerSavedDoc)
	t32RequireMismatch(t, f, "discord", []string{"222", "333"})
	restore()
	require.NoError(t, f.api.safeUpdateConfigJSON(func(map[string]any) error { return nil }), "S-28f: real successful refresh, not a test pointer swap")
	require.Equal(t, []string{"discord:222"}, []string(f.al.GetConfig().Channels["discord"].Outbound.Targets))
	rec := t14Put(t, f, "discord", "agent_may_start", []string{"222", "333"}, t14Read(t, f, "discord").Revision)
	require.Equal(t, http.StatusOK, rec.Code, "fresh read after recovery permits the extension: %s", rec.Body.String())
	t13AssertOutbound(t, f, "discord", "agent_may_start", []string{"discord:222", "discord:333"})
}

func TestT32_CanonicalCorrespondence_BulkOmittedTypeIsNotASavedLiveMismatch(t *testing.T) {
	for _, id := range []string{"discord", "discord.qa"} {
		t.Run(id, func(t *testing.T) {
			f, _ := t32Fixture(t, id, []string{"discord:222"})
			require.NoError(t, f.api.safeUpdateConfigJSON(func(doc map[string]any) error {
				delete(doc["channels"].(map[string]any)[id].(map[string]any), "type")
				return nil
			}), "valid ordinary bulk/common save can omit type")
			_, explicit := t32Saved(t, f, id)["type"]
			require.False(t, explicit, "instrument: saved omission remains visible to the correspondence reader")
			require.Equal(t, "discord", f.al.GetConfig().Channels[id].Type, "instrument: actual loader normalized the live type")
			rec := t14Put(t, f, id, "agent_may_start", []string{"222", "333"}, t14Read(t, f, id).Revision)
			require.Equal(t, http.StatusOK, rec.Code, "T-6b canonical equality, not raw decoded equality: %s", rec.Body.String())
			t13AssertOutbound(t, f, id, "agent_may_start", []string{"discord:222", "discord:333"})
		})
	}
}

func TestT32_SavedNotRefreshed_RealDispatchRefusesTheNewReplyOnlyPolicy(t *testing.T) {
	msg := t15Start(t, "QA t32 must be refused")
	msg.WorkspaceID = "ws-t32"
	f, adapter := t32Fixture(t, "discord", []string{"discord:222"})
	t32FaultRefresh(t, f)
	rec := t14Put(t, f, "discord", "reply_only", []string{"222"}, t14Read(t, f, "discord").Revision)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	fenceDone, finalDone := make(chan struct{}), make(chan struct{})
	adapter.send = func(_ context.Context, msg bus.OutboundMessage) error {
		if msg.Content == "QA t32 dispatch fence" {
			close(fenceDone)
		}
		if msg.Content == "QA t32 final fence" {
			close(finalDone)
		}
		return nil
	}
	require.NoError(t, f.bus.PublishOutbound(context.Background(), msg))
	for _, fence := range []struct {
		text string
		done <-chan struct{}
	}{{"QA t32 dispatch fence", fenceDone}, {"QA t32 final fence", finalDone}} {
		require.NoError(t, f.bus.PublishOutbound(context.Background(), bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: fence.text}))
		select {
		case <-fence.done:
		case <-time.After(5 * time.Second):
			t.Fatal("instrument: real dispatch completion fence did not Send")
		}
	}
	attempts := adapter.attemptSnapshot()
	contents := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		contents = append(contents, attempt.Content)
	}
	require.Equal(t, []string{"QA t32 dispatch fence", "QA t32 final fence"}, contents, "S-25: narrow-published revocation applies at real dispatch, no fallback")
}

func TestT32_SavedNotRefreshed_ToolRefusalNeedsTheRealStartPolicyWiring(t *testing.T) {
	f, adapter := t32Fixture(t, "discord", []string{"discord:222"})
	inst, present := f.al.GetRegistry().GetAgent("mia")
	require.True(t, present)
	executable, present := inst.Tools.Get("send_message")
	require.True(t, present, "instrument: real registry contains constructor-wired send_message")
	require.IsType(t, (*tools.MessageTool)(nil), executable)
	meta, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	ctx := tools.WithTranscriptSessionID(tools.WithToolContext(tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "mia"), "ws-t32"), "webchat", "qa-t32-chat"), meta.ID)
	delivered := make(chan bus.OutboundMessage, 4)
	adapter.send = func(_ context.Context, msg bus.OutboundMessage) error { delivered <- msg; return nil }
	accepted := executable.Execute(ctx, map[string]any{"channel": "discord", "chat_id": "222", "content": "QA T32 allowed instrument"})
	require.NoError(t, accepted.Err)
	require.False(t, accepted.IsError)
	require.Equal(t, "accepted for sending to discord:222 (delivery not confirmed)", accepted.ForLLM)
	select {
	case msg := <-delivered:
		require.Equal(t, "QA T32 allowed instrument", msg.Content)
		require.Equal(t, "start", msg.Operation, "instrument: actual agent callback copies start classification to the real bus")
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: allowed real-tool start never reached the adapter")
	}

	t32FaultRefresh(t, f)
	rec := t14Put(t, f, "discord", "reply_only", []string{"222"}, t14Read(t, f, "discord").Revision)
	require.Equal(t, http.StatusOK, rec.Code, "S-25: saved revocation remains enforced despite failed refresh: %s", rec.Body.String())
	t13AssertOutbound(t, f, "discord", "reply_only", []string{"discord:222"})
	// Join only the dispatcher after the real save, so the actual constructor
	// callback's bus is directly observable. Do not replace that callback or
	// let a second consumer steal its publication. Policy must refuse BEFORE
	// publishing, not merely rely on the manager's later refusal.
	stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	manager := f.svc.ChannelManager
	// Take over this fixture resource's teardown exactly once. Its shared
	// cleanup must not call StopAll a second time on already closed queues.
	// AgentLoop retains the SAME manager for the actual policy read below.
	f.svc.ChannelManager = nil
	require.NoError(t, manager.StopAll(stopCtx))
	require.Len(t, f.bus.OutboundChan(), 0, "instrument: no pending traffic after the completed positive control")
	refused := executable.Execute(ctx, map[string]any{"channel": "discord", "chat_id": "222", "content": "QA T32 forbidden after revocation"})
	require.True(t, refused.IsError)
	require.ErrorIs(t, refused.Err, tools.ErrStartRefused)
	require.Equal(t, `cannot start a message on "discord": this connection is set to "Only answer". The operator can change it in Connectors > Discord > Configure ("Answer and also start messages"). Nothing was sent.`, refused.ForLLM, "S-25/FR-020: SAME real tool sees the narrowly published live revocation")
	select {
	case msg := <-f.bus.OutboundChan():
		t.Fatalf("S-25: refused tool call published to the real bus: %+v", msg)
	default:
	}
	// Positive control on this exact observable bus: the same unmodified tool
	// still publishes an ordinary webchat send, with checked (not start).
	control := executable.Execute(ctx, map[string]any{"channel": "webchat", "chat_id": "qa-t32-chat", "content": "QA T32 bus still observable"})
	require.NoError(t, control.Err)
	require.False(t, control.IsError)
	require.Equal(t, "Message sent to webchat:qa-t32-chat", control.ForLLM)
	select {
	case msg := <-f.bus.OutboundChan():
		require.Equal(t, "QA T32 bus still observable", msg.Content)
		require.Equal(t, "webchat", msg.Channel)
		require.Equal(t, "checked", msg.Operation)
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: actual callback/bus positive control disappeared")
	}
	require.Len(t, adapter.attemptSnapshot(), 1, "only the pre-revocation positive control reached Send")
}

func TestT32_GuardToPublish_UsesTransformedGuardOutputNotRequestedValue(t *testing.T) {
	t.Fatal("BLOCKED: actual guarded writer has no test-only guard-transform injection seam — required by T-32/S-25 provenance/T-6b; the dedicated identity guard alone cannot distinguish accepted value from requested value")
}
