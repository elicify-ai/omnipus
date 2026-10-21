package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// No native adapter replacement: these writer tests stop at ready admission.
// The existing fixture's external model/HTTP edges remain controlled. Native
// activation/QR acceptance is a separate integration gap, not simulated here.
func orderingWriterFixture(t *testing.T) (*wu6Fixture, *channels.PairingAttempts, *systools.Deps) {
	t.Helper()
	f := newWU6Fixture(t)
	require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", "{}").Code)
	require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/whatsapp/enable", "").Code)
	d := &systools.Deps{
		Home: f.f1277.home, ConfigPath: f.f1277.configPath,
		GetCfg: f.al.GetConfig, MutateConfig: f.al.MutateConfig,
		SaveConfigLocked: func(cfg *config.Config) error { return config.SaveConfig(f.f1277.configPath, cfg) },
		LockConfigWrites: func() func() { return lockConfigWrites(f.svc) },
		CredStore:        f.store,
		PairingAttempts:  f.svc.ChannelManager.PairingAttempts,
		AdmitPairing:     f.al.AdmitConnectPairing, CommitPairing: f.al.CommitPairing,
		ReleasePairing: f.al.ReleasePairing, RevokePairingForDisable: f.al.RevokePairingForDisable,
		PairingAttemptState: f.al.PairingAttemptState, SettlePairing: f.al.SettlePairing,
	}
	return f, f.svc.ChannelManager.PairingAttempts(), d
}

func orderingChat(t *testing.T, f *wu6Fixture) string {
	t.Helper()
	s, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	owner := wu6Owner
	require.NoError(t, f.al.GetSessionStore().SetMeta(s.ID, session.MetaPatch{Owner: &owner}))
	return s.ID
}

func orderingToolErrorCode(t *testing.T, result *tools.ToolResult) string {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
	failure, ok := body["error"].(map[string]any)
	require.True(t, ok, "a refused tool result carries its structured error")
	code, ok := failure["code"].(string)
	require.True(t, ok, "a refused tool result carries its machine code")
	return code
}

// The revision is an opaque token obtained through the real public read tool;
// its hash algorithm is not the oracle. DESIGN §3 requires validation first.
func orderingToolRevision(t *testing.T, d *systools.Deps) string {
	t.Helper()
	res := systools.NewChannelListTool(d).Execute(context.Background(), nil)
	require.False(t, res.IsError, res.ForLLM)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &doc))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc["channels"], &rows))
	for _, row := range rows {
		var id, revision string
		require.NoError(t, json.Unmarshal(row["id"], &id))
		if id == "whatsapp" {
			require.NoError(t, json.Unmarshal(row["revision"], &revision))
			require.NotEmpty(t, revision)
			return revision
		}
	}
	t.Fatal("instrument: real list_channels omitted WhatsApp")
	return ""
}

func orderingReady(t *testing.T, f *wu6Fixture, request, chat string) channels.PairingAttempt {
	t.Helper()
	id, err := f.al.AdmitConnectPairing("whatsapp", request, chat)
	require.NoError(t, err)
	require.True(t, f.al.CommitPairing("whatsapp", id))
	rec, ok := f.svc.ChannelManager.PairingAttempts().Attempt("whatsapp", id)
	require.True(t, ok)
	require.Equal(t, channels.PairingPhaseReady, rec.Phase)
	return rec
}

func orderingWaitStage(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("HARNESS: held stage was not acknowledged; not assertion-level RED")
	}
}

// DESIGN §4 R6-A02 / CR-T2-R6-03 / T2-SF-19 / T2-R6-S1, §5 T1.
// Covers the real stale tool revision, other-chat Retry refusal and exact-write
// obsolete-ready cleanup. The narrower internal GetConfig-to-cleanup window
// still needs a passive observation hook; this test does not pretend to hold it.
func TestChannelOrdering_StaleObservationCannotCancelCommittedWinner(t *testing.T) {
	t.Run("stale tool plan validates revision before pairing admission", func(t *testing.T) {
		f, store, d := orderingWriterFixture(t)
		require.NoError(t, os.MkdirAll(filepath.Join(f.f1277.home, "workspaces"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(f.f1277.home, "workspaces", "ordering.json"),
			[]byte(`{"id":"ordering","name":"Ordering","status":"active","core_team":["mia"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`), 0o600))
		oldRevision := orderingToolRevision(t, d)
		observed, resume, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
		result := make(chan *tools.ToolResult, 1)
		go func() {
			defer close(joined)
			close(observed) // A's old confirmed plan is captured; no scheduler delay.
			<-resume
			result <- systools.NewChannelConnectTool(d).Execute(context.Background(), map[string]any{
				"id": "whatsapp", "workspace_id": "ordering", "agent_id": "mia", "expected_revision": oldRevision,
			})
		}()
		var once sync.Once
		release := func() { once.Do(func() { close(resume) }) }
		t.Cleanup(func() { release(); orderingJoin(t, joined) })
		orderingWaitStage(t, observed)
		require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", `{"allow_from":["winner"]}`).Code)
		winner := orderingReady(t, f, "req-b", orderingChat(t, f))
		release()
		var refused *tools.ToolResult
		select {
		case refused = <-result:
		case <-time.After(3 * time.Second):
			t.Fatal("HARNESS: stale connect did not join; not assertion-level RED")
		}
		require.True(t, refused.IsError, refused.ForLLM)
		// The canonical shared-write refusal code is CONFLICT, not the pairing
		// admission error: a stale plan must not enter destructive admission.
		assert.Equal(t, "CONFLICT", orderingToolErrorCode(t, refused), "stale tool revision is checked before pairing admission")
		current, ok := store.Latest("whatsapp")
		require.True(t, ok)
		assert.Equal(t, winner, current, "refusal cannot change B's exact token, owner, revision or phase")
		assert.Equal(t, winner.AttemptID, store.TakeReady("whatsapp", winner.Revision))
	})

	t.Run("other chat Retry refuses without touching B", func(t *testing.T) {
		f, store, _ := orderingWriterFixture(t)
		winner := orderingReady(t, f, "req-b", orderingChat(t, f))
		other := orderingChat(t, f)
		rec := f.do(http.MethodPut, "/api/v1/channels/whatsapp/enable", retryBody("req-a", other))
		requireRESTConflict(t, rec)
		current, ok := store.Latest("whatsapp")
		require.True(t, ok)
		require.Equal(t, winner, current)
	})

	t.Run("settings replacement closes its obsolete ready reservation at commit", func(t *testing.T) {
		f, store, _ := orderingWriterFixture(t)
		obsolete := orderingReady(t, f, "req-old", orderingChat(t, f))
		rec := f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", `{"allow_from":["replacement"]}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		closed, ok := store.Attempt("whatsapp", obsolete.AttemptID)
		require.True(t, ok)
		require.Equal(t, channels.PairingPhaseTerminal, closed.Phase, "DESIGN §3 cleanup belongs to the successful settings write, not an unrelated next admission")
		require.Equal(t, channels.PairingOutcomeInterrupted, closed.Outcome)
	})
}

// DESIGN §4 R6-A03 / CR-T2-R6-02 / T2-SF-20, §5 T2. Gates are passive
// acknowledgements after the real writer lock releases; every pairing action,
// persistence call and handler remains real. No goroutine is awaited while
// holding a lock B needs.
func TestChannelOrdering_DisableCannotRevokeSuccessor(t *testing.T) {
	for _, surface := range []string{"REST", "tool"} {
		t.Run(surface, func(t *testing.T) {
			f, store, d := orderingWriterFixture(t)
			chatA, chatB := orderingChat(t, f), orderingChat(t, f)
			a := orderingReady(t, f, "req-a", chatA)
			entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var pause sync.Once
			gate := func() { pause.Do(func() { close(entered); <-resume }) }
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(resume) }) }
			var response *httptest.ResponseRecorder
			var toolResult *tools.ToolResult
			revision := orderingToolRevision(t, d)
			if surface == "REST" {
				previous := enableSavedHook
				enableSavedHook = func(id string) {
					if id == "whatsapp" {
						gate()
					}
				}
				t.Cleanup(func() { enableSavedHook = previous })
			} else {
				d.LockConfigWrites = func() func() {
					unlock := lockConfigWrites(f.svc)
					return func() { unlock(); gate() }
				}
			}
			go func() {
				defer close(done)
				if surface == "REST" {
					response = f.do(http.MethodPut, "/api/v1/channels/whatsapp/disable", "")
				} else {
					toolResult = systools.NewChannelDisableTool(d).Execute(context.Background(), map[string]any{
						"id": "whatsapp", "expected_revision": revision,
					})
				}
			}()
			t.Cleanup(func() { release(); orderingJoin(t, done) })
			orderingWaitStage(t, entered)
			atCommit, ok := store.Attempt("whatsapp", a.AttemptID)
			require.True(t, ok)
			assert.Equal(t, channels.PairingPhaseTerminal, atCommit.Phase, "D must commit selected revocation before releasing the local writer transaction")
			assert.Equal(t, channels.PairingOutcomeInterrupted, atCommit.Outcome)

			// End A through real Stop if the broken baseline still left it ready.
			// In the fixed case this is a harmless repeated Stop, not a rewrite of
			// the expected state. It allows B in both implementations.
			_, _, err := f.al.RequestCancelForSession(context.Background(), chatA, wu6Owner, "webchat")
			require.NoError(t, err)
			require.NoError(t, f.api.safeUpdateConfigJSON(func(doc map[string]any) error {
				doc["channels"].(map[string]any)["whatsapp"].(map[string]any)["enabled"] = true
				return nil
			}))
			b := orderingReady(t, f, "req-b", chatB)
			release()
			orderingJoin(t, done)
			if surface == "REST" {
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			} else {
				require.False(t, toolResult.IsError, toolResult.ForLLM)
			}
			current, ok := store.Latest("whatsapp")
			require.True(t, ok)
			assert.Equal(t, b, current, "D's delayed work must preserve B's exact owner/token/revision/ready state")
			assert.Equal(t, b.AttemptID, store.TakeReady("whatsapp", b.Revision), "B remains startable")
		})
	}

	t.Run("stale tool disable is effect free", func(t *testing.T) {
		f, store, d := orderingWriterFixture(t)
		staleRevision := orderingToolRevision(t, d)
		require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", `{"allow_from":["new-plan"]}`).Code)
		b := orderingReady(t, f, "req-b", orderingChat(t, f))
		res := systools.NewChannelDisableTool(d).Execute(context.Background(), map[string]any{
			"id": "whatsapp", "expected_revision": staleRevision,
		})
		require.True(t, res.IsError, res.ForLLM)
		require.Equal(t, "CONFLICT", orderingToolErrorCode(t, res))
		current, ok := store.Latest("whatsapp")
		require.True(t, ok)
		require.Equal(t, b, current)
		require.True(t, f.al.GetConfig().Channels["whatsapp"].Enabled)
	})

	for _, surface := range []string{"REST", "tool"} {
		t.Run(surface+" failed persistence is effect free", func(t *testing.T) {
			f, store, d := orderingWriterFixture(t)
			b := orderingReady(t, f, "req-b", orderingChat(t, f))
			revision := orderingToolRevision(t, d)
			dir := filepath.Dir(f.f1277.configPath)
			info, err := os.Stat(dir)
			require.NoError(t, err)
			require.NoError(t, os.Chmod(dir, 0o500))
			t.Cleanup(func() { require.NoError(t, os.Chmod(dir, info.Mode().Perm())) })
			if surface == "REST" {
				rec := f.do(http.MethodPut, "/api/v1/channels/whatsapp/disable", "")
				require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
				require.Equal(t, "could not save config: server storage or configuration failed. "+
					"Check disk space and permissions, then retry. Details are in the server log.", decodeError(t, rec).Error)
			} else {
				res := systools.NewChannelDisableTool(d).Execute(context.Background(), map[string]any{
					"id": "whatsapp", "expected_revision": revision,
				})
				require.True(t, res.IsError, res.ForLLM)
				require.Equal(t, "DISABLE_FAILED", orderingToolErrorCode(t, res))
			}
			require.NoError(t, os.Chmod(dir, info.Mode().Perm()))
			current, ok := store.Latest("whatsapp")
			require.True(t, ok)
			require.Equal(t, b, current)
			require.True(t, f.al.GetConfig().Channels["whatsapp"].Enabled)
		})
	}

	t.Run("durable disable and failed live refresh fences the old start", func(t *testing.T) {
		f, store, _ := orderingWriterFixture(t)
		a := orderingReady(t, f, "req-a", orderingChat(t, f))
		previous := f.api.credStore
		// Real locked vault, not a fake refresh function: raw config persistence
		// succeeds and the actual live loader refuses the locked prerequisite.
		f.api.credStore = credentials.NewStore(filepath.Join(f.f1277.home, "credentials.json"))
		t.Cleanup(func() { f.api.credStore = previous })
		rec := f.do(http.MethodPut, "/api/v1/channels/whatsapp/disable", "")
		require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
		require.Equal(t, "channel whatsapp setting saved, but the running configuration could not be refreshed: "+
			"server storage or configuration failed. Check disk space and permissions, then retry. Details are in the server log.", decodeError(t, rec).Error)
		raw, err := f.api.readChannelConfigRaw("whatsapp")
		require.NoError(t, err)
		require.Equal(t, false, raw["enabled"], "disable is durable")
		require.True(t, f.al.GetConfig().Channels["whatsapp"].Enabled, "the live refresh did not apply")
		old, ok := store.Attempt("whatsapp", a.AttemptID)
		require.True(t, ok)
		require.Equal(t, channels.PairingPhaseTerminal, old.Phase, "durable settings replacement must fence an obsolete start even when live refresh fails")
		require.Equal(t, channels.PairingOutcomeInterrupted, old.Outcome)
	})
}

// DESIGN §3 Stop/delete identity and §5 T5: an old transport is still being
// disposed when delete/recreate commits. The real Stop and DELETE must not let
// dropping an entry release its captured resource's stopping exclusion.
func TestChannelOrdering_StopPinsItsGenerationAndHandle(t *testing.T) {
	t.Run("pending start is stopped but another channel is untouched", func(t *testing.T) {
		f, store, _ := orderingWriterFixture(t)
		chat := orderingChat(t, f)
		a, err := f.al.AdmitConnectPairing("whatsapp", "req-a", chat)
		require.NoError(t, err)
		other, err := store.Admit("whatsapp.other", channels.PairingAdmissionConnect,
			&channels.PairingOwner{SessionID: "unrelated", RequestID: "req-other"})
		require.NoError(t, err)
		require.True(t, store.MarkReady("whatsapp.other", other.AttemptID, "other-revision"))
		_, _, err = f.al.RequestCancelForSession(context.Background(), chat, wu6Owner, "webchat")
		require.NoError(t, err)
		stopped, ok := store.Attempt("whatsapp", a)
		require.True(t, ok)
		require.Equal(t, channels.PairingPhaseTerminal, stopped.Phase)
		require.Equal(t, channels.PairingOutcomeStopped, stopped.Outcome)
		require.True(t, store.IsReady("whatsapp.other", other.AttemptID))
	})

	t.Run("a later accepted attempt survives frozen Stop selection", func(t *testing.T) {
		f, store, _ := orderingWriterFixture(t)
		chat := orderingChat(t, f)
		a := orderingReady(t, f, "req-a", chat)
		_, err := store.Activate("whatsapp", a.AttemptID, func(string) func(context.Context) error {
			return func(context.Context) error { t.Error("old terminal resource was torn down again"); return nil }
		})
		require.NoError(t, err)
		selected, resume, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var gateOnce, releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(resume) }) }
		var stopErr error
		go func() {
			defer close(joined)
			_, stopErr = f.al.StopSession(context.Background(), agent.StopRequest{
				SessionID: chat, Channel: "webchat", By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: wu6Owner},
				HooksFor: func(string) agent.CancelHooks {
					gateOnce.Do(func() { close(selected); <-resume })
					return agent.CancelHooks{}
				},
			})
		}()
		t.Cleanup(func() { release(); orderingJoin(t, joined) })
		orderingWaitStage(t, selected)
		_, ended := store.Complete("whatsapp", a.AttemptID, channels.PairingOutcomeTimeout)
		require.True(t, ended)
		b := orderingReady(t, f, "req-b", chat)
		var bDisposals atomic.Int32
		_, err = store.Activate("whatsapp", b.AttemptID, func(string) func(context.Context) error {
			return func(context.Context) error { bDisposals.Add(1); return nil }
		})
		require.NoError(t, err)
		release()
		orderingJoin(t, joined)
		require.NoError(t, stopErr)
		require.Zero(t, bDisposals.Load(), "old Stop cannot resolve a successor resource by channel name")
		require.True(t, store.IsCurrent("whatsapp", b.AttemptID))
		events := f.al.SubscribeEvents(8)
		f.al.EmitWhatsAppPairingAttempt("whatsapp", b.AttemptID, channels.PairingStatusCode, "QR-B", "")
		f.al.EmitWhatsAppPairingAttempt("whatsapp", b.AttemptID, channels.PairingStatusTimeout, "", "expired")
		var progress []agent.WhatsAppPairingPayload
	collect:
		for {
			select {
			case event := <-events.C:
				if p, ok := event.Payload.(agent.WhatsAppPairingPayload); ok {
					progress = append(progress, p)
				}
			default:
				break collect
			}
		}
		require.Len(t, progress, 2, "nonempty B progress and its closing marker")
		require.Equal(t, "QR-B", progress[0].QR)
		require.Equal(t, "req-b", progress[0].RequestID)
		require.Equal(t, chat, progress[0].SessionID)
		require.Equal(t, channels.PairingStatusTimeout, progress[1].Status)
	})

	f, store, _ := orderingWriterFixture(t)
	chatA := orderingChat(t, f)
	a := orderingReady(t, f, "req-a", chatA)
	captured, dispose := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(dispose) }) }
	active, err := store.Activate("whatsapp", a.AttemptID, func(id string) func(context.Context) error {
		return func(context.Context) error {
			if id != a.AttemptID {
				return channels.ErrPairingConflict
			}
			close(captured)
			<-dispose // only the captured external resource's final disposal is held
			return nil
		}
	})
	require.NoError(t, err)
	require.Equal(t, a.AttemptID, active.AttemptID)
	done := make(chan struct{})
	var stopErr error
	go func() {
		defer close(done)
		_, _, stopErr = f.al.RequestCancelForSession(context.Background(), chatA, wu6Owner, "webchat")
	}()
	t.Cleanup(func() { release(); orderingJoin(t, done) })
	orderingWaitStage(t, captured)
	stopping, ok := store.Attempt("whatsapp", a.AttemptID)
	require.True(t, ok)
	require.Equal(t, channels.PairingPhaseStopping, stopping.Phase)

	deleted := httptest.NewRecorder()
	f.api.deleteChannelInstance(deleted, "whatsapp")
	require.Equal(t, http.StatusNoContent, deleted.Code, deleted.Body.String())
	require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/whatsapp/configure", "{}").Code)
	b, admissionErr := f.al.AdmitConnectPairing("whatsapp", "req-b", orderingChat(t, f))
	assert.ErrorIs(t, admissionErr, channels.ErrPairingConflict, "no successor may be admitted over the captured undisposed resource, including delete/recreate")
	release()
	orderingJoin(t, done)
	require.NoError(t, stopErr)
	if admissionErr == nil {
		// The bad baseline admitted B: old completion must not release even that
		// successor. This is a separate exact-identity control, not acceptance of
		// the premature admission above.
		current, exists := store.Attempt("whatsapp", b)
		require.True(t, exists)
		assert.Equal(t, channels.PairingPhasePending, current.Phase)
	}
	rawAudit, err := os.ReadFile(filepath.Join(f.f1277.home, "system", "audit.jsonl"))
	require.NoError(t, err)
	foundCapturedStop := false
	scanner := bufio.NewScanner(strings.NewReader(string(rawAudit)))
	for scanner.Scan() {
		var row map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row))
		if row["event"] != "channel.pairing" {
			continue
		}
		details, ok := row["details"].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, details, "qr", "audit never stores a scannable secret")
		if details["status"] == "stopped" {
			assert.Equal(t, a.AttemptID, details["attempt_id"], "historical Stop identifies captured A, never B")
			assert.Equal(t, "req-a", details["request_id"])
			assert.Equal(t, chatA, details["session_id"])
			foundCapturedStop = true
		}
	}
	require.NoError(t, scanner.Err())
	assert.True(t, foundCapturedStop, "the disposed captured resource's historical Stop must still be audited after retirement")
}
