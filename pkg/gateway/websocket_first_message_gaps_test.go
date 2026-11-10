package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1090 RED4 brief, m5/m6/m7; design-1090.md D3/D5 and the founder's
// process-local "middle way": principal kinds cannot alias, a stale owner
// cannot authorize a cached receipt, and simultaneous identical first sends
// save/admit exactly once. Expectations are from that brief, not observed output.
//
// Boundary: generated MessageFrame -> real WS dispatcher -> real intake,
// UnifiedStore, outbound queues and admission bus. As in the existing retry
// tests, connections carry already-authenticated identities. Only the external
// model provider is replaced; no agent loop or writer consumes the queues.
// Dispatch completion (both goroutines joined for m7) is the observation barrier.
// No dedupe helpers, cache entries or serialization locks are accessed.
//
// Deliberate gaps: m5 targets the account/CLI collision with equal username and
// owner, not every legacy-token/bypass handshake. m7 samples start-barrier
// schedules; it does not certify all schedules or independent gateway processes.
// Restart/eviction limits and message-size boundaries belong to other #1090 tests.
// Starting-point runs are not RED/GREEN or mutation proof: a separate CHECK
// worker must apply the three patches and confirm these assertions fail.

func issue1090GapsFixture(t *testing.T) (*WSHandler, *bus.MessageBus, *session.UnifiedStore) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store, "RED4 must inspect the real session store")
	return handler, msgBus, store
}

func issue1090GapsConn(t *testing.T, userID string, isCLI bool) *wsConn {
	t.Helper()
	wc := makeTestConn()
	// These are authenticateWS's resolved connection inputs, not payload claims.
	// A human account named "cli" and the CLI token can have the same userID;
	// authenticateWS distinguishes them by isCLIToken, including on cookie auth.
	wc.userID, wc.isCLIToken = userID, isCLI
	t.Cleanup(wc.close)
	return wc
}

func issue1090GapsFrame(clientID, content string) generated.MessageFrame {
	agentID := "mia"
	return generated.MessageFrame{
		Type: string(generated.WsFrameTypeMessage), ClientMessageId: &clientID,
		Content: content, AgentId: &agentID,
	}
}

func issue1090GapsSend(t *testing.T, handler *WSHandler, userID string, isCLI bool, chatID string, frame generated.MessageFrame) (*wsConn, []map[string]any) {
	t.Helper()
	wc := issue1090GapsConn(t, userID, isCLI)
	data, err := json.Marshal(frame)
	require.NoError(t, err, "RED4 must serialize the generated client frame")
	reader := &wsHandlerReadLoop{h: handler, ctx: context.Background(), wc: wc, chatID: chatID}
	require.Equal(t, wsHandlerReadLoopNext, reader.dispatchFrame(data, wsTypeOnly{Type: frame.Type}),
		"RED4 must finish the real message dispatcher")
	return wc, issue1090DrainQueuedFrames(t, wc)
}

func issue1090GapsAssertFresh(t *testing.T, frames []map[string]any, clientID, content string) string {
	t.Helper()
	require.Equal(t, []any{"session_started", "user_message", "message_status"}, issue1090FrameTypes(frames),
		"RED4: a different principal kind must be a fresh saved send, not a cached receipt")
	sessionID := issue1090StartedSessionID(t, frames)
	assert.Equal(t, clientID, frames[0]["client_message_id"], "fresh acknowledgement must correlate the exact request")
	assert.NotEqual(t, true, frames[0]["recovered"], "fresh acknowledgement must not report recovered:true")
	assert.Equal(t, sessionID, frames[1]["session_id"], "fresh echo must identify its own saved session")
	assert.Equal(t, clientID, frames[1]["client_message_id"], "fresh echo must retain the original client ID")
	assert.Equal(t, content, frames[1]["content"], "fresh echo must contain the exact requested text")
	assert.Equal(t, sessionID, frames[2]["session_id"], "received receipt must identify the saved session")
	assert.Equal(t, clientID, frames[2]["client_message_id"], "received receipt must correlate the exact request")
	assert.Equal(t, "received", frames[2]["state"], "fresh save must report received before an agent runs")
	return sessionID
}

func TestFirstUserMessage_PrincipalKindsDoNotShareRetry(t *testing.T) {
	// m5 erases kind in BOTH insertion and lookup while leaving identity intact.
	// Different usernames would miss that mutation. Equal Owner values also
	// prevent the stale-owner guard from hiding the cross-kind cache collision.
	for _, tc := range []struct {
		name       string
		firstIsCLI bool
	}{
		{"account_then_cli", false},
		{"cli_then_account", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const userID, clientID, content = "cli", "red4-m5-shared-id", "Same text and identity, different authentication kinds"
			handler, msgBus, store := issue1090GapsFixture(t)
			frame := issue1090GapsFrame(clientID, content)

			firstConn, first := issue1090GapsSend(t, handler, userID, tc.firstIsCLI, "red4-m5-first", frame)
			firstSession := issue1090GapsAssertFresh(t, first, clientID, content)
			firstEntry := issue1090AssertSavedFirstEntry(t, store, firstSession, content, clientID)
			_, control := issue1090GapsSend(t, handler, userID, tc.firstIsCLI, "red4-m5-first-control", frame)
			issue1090AssertRecovered(t, control, firstSession, clientID, "m5 populated-cache control")
			require.Equal(t, [][3]string{{firstSession, content, userID}}, issue1090AdmittedTurns(msgBus),
				"m5 control: the real original send, but not its retry, must admit one turn")

			secondConn, second := issue1090GapsSend(t, handler, userID, !tc.firstIsCLI, "red4-m5-other-kind", frame)
			secondSession := issue1090GapsAssertFresh(t, second, clientID, content)
			require.NotEqual(t, firstSession, secondSession, "m5: equal identity text must not merge authentication kinds")
			secondEntry := issue1090AssertSavedFirstEntry(t, store, secondSession, content, clientID)
			wire, err := json.Marshal(second)
			require.NoError(t, err)
			assert.NotContains(t, string(wire), firstSession, "m5: no response to the other kind may disclose the first session")
			assert.Equal(t, [][3]string{{secondSession, content, userID}}, issue1090AdmittedTurns(msgBus),
				"m5: the other kind must admit exactly its own fresh turn")

			for index, owned := range []struct {
				isCLI     bool
				sessionID string
				entry     []session.TranscriptEntry
			}{
				{tc.firstIsCLI, firstSession, firstEntry},
				{!tc.firstIsCLI, secondSession, secondEntry},
			} {
				meta, err := store.GetMeta(owned.sessionID)
				require.NoError(t, err)
				require.NotNil(t, meta)
				require.Equal(t, userID, meta.Owner, "m5 fixture: both kinds have the SAME stored owner")
				_, retry := issue1090GapsSend(t, handler, userID, owned.isCLI, fmt.Sprintf("red4-m5-retry-%d", index), frame)
				issue1090AssertRecovered(t, retry, owned.sessionID, clientID, "m5 independent same-kind recovery")
				assert.Equal(t, owned.entry, issue1090DiskTranscriptEntries(t, store, owned.sessionID),
					"m5: each kind's retry must leave its exact original transcript unchanged")
			}
			assert.ElementsMatch(t, []string{firstSession, secondSession}, issue1090SessionDirectories(t, store),
				"m5: exactly two independent chats must remain after both same-kind retries")
			assert.Empty(t, issue1090AdmittedTurns(msgBus), "m5: neither same-kind retry may admit another turn")
			assert.Empty(t, issue1090DrainQueuedFrames(t, firstConn), "m5: the other kind and retries must not re-echo to the first hub")
			assert.Empty(t, issue1090DrainQueuedFrames(t, secondConn), "m5: same-kind retries must not re-echo to the second hub")
			t.Log("m5 control: equal identity/owner, two distinct saved chats, and recovery within each kind")
		})
	}
}

func issue1090GapsInvalidateOwner(t *testing.T, store *session.UnifiedStore, sessionID, scenario string) {
	t.Helper()
	// Flush before damaging metadata: no pending stats writer can repair the
	// fixture accidentally. Eviction forces GetMeta to inspect disk, not a
	// cached copy of the original valid owner. No private store seam is used.
	require.NoError(t, store.FlushAndEvictSessionMeta(sessionID), "m6 fixture must flush and evict valid metadata")
	metaPath := filepath.Join(store.BaseDir(), sessionID, "meta.json")
	switch scenario {
	case "wrong_owner", "owner_removed":
		owner := "red4-m6-foreign-owner"
		if scenario == "owner_removed" {
			owner = ""
		}
		require.NoError(t, store.SetMeta(sessionID, session.MetaPatch{Owner: &owner}), "m6 fixture must persist the owner change")
		require.NoError(t, store.FlushAndEvictSessionMeta(sessionID), "m6 fixture must verify the changed owner from disk")
		meta, err := store.GetMeta(sessionID)
		require.NoError(t, err)
		require.NotNil(t, meta)
		require.Equal(t, owner, meta.Owner, "m6 instrument: the stored owner really changed")
	case "missing_meta", "unreadable_meta":
		require.NoError(t, os.Remove(metaPath), "m6 fixture must remove only the saved metadata file")
		if scenario == "unreadable_meta" {
			// A directory cannot be read as metadata, even by a privileged user;
			// chmod-based failures would not reliably test the filesystem edge.
			require.NoError(t, os.Mkdir(metaPath, 0o700), "m6 fixture must make the metadata read fail")
		}
		meta, err := store.GetMeta(sessionID)
		require.Nil(t, meta, "m6 instrument: failed metadata lookup must not return the former owner")
		if scenario == "missing_meta" {
			require.ErrorIs(t, err, os.ErrNotExist, "m6 instrument: metadata must actually be missing")
		} else {
			var pathErr *os.PathError
			require.ErrorAs(t, err, &pathErr, "m6 instrument: metadata read must fail at the real filesystem edge")
			require.Equal(t, metaPath, pathErr.Path, "m6 instrument: the read error must be for the saved chat's metadata")
			require.False(t, errors.Is(err, os.ErrNotExist), "m6 instrument: unreadable is distinct from missing")
		}
	default:
		t.Fatalf("unknown m6 fixture scenario %q", scenario)
	}
}

func TestFirstUserMessage_StaleRetryReturnsVisibleErrorWithoutRemint(t *testing.T) {
	// The exact m6 patch removes only the Owner comparison. wrong_owner and
	// owner_removed target that loss; missing/error cases pin the other guards.
	for _, scenario := range []string{"wrong_owner", "owner_removed", "missing_meta", "unreadable_meta"} {
		t.Run(scenario, func(t *testing.T) {
			const userID, clientID, content = "red4-m6-alice", "red4-m6-first", "Do not disclose or remint a chat with stale ownership"
			handler, msgBus, store := issue1090GapsFixture(t)
			frame := issue1090GapsFrame(clientID, content)
			originalConn, initial := issue1090GapsSend(t, handler, userID, false, "red4-m6-original", frame)
			sessionID := issue1090GapsAssertFresh(t, initial, clientID, content)
			before := issue1090AssertSavedFirstEntry(t, store, sessionID, content, clientID)
			meta, err := store.GetMeta(sessionID)
			require.NoError(t, err)
			require.NotNil(t, meta)
			require.Equal(t, userID, meta.Owner, "m6 control: the original session must really belong to the caller")
			_, valid := issue1090GapsSend(t, handler, userID, false, "red4-m6-valid-control", frame)
			issue1090AssertRecovered(t, valid, sessionID, clientID, "m6 valid-owner recovery control")
			require.Equal(t, [][3]string{{sessionID, content, userID}}, issue1090AdmittedTurns(msgBus),
				"m6 control: the original send admits one turn; valid recovery admits none")

			issue1090GapsInvalidateOwner(t, store, sessionID, scenario)
			// A second identical retry must stay blocked too, never consume the
			// stale cache hit and turn a later attempt into a fresh mint.
			for attempt := 0; attempt < 2; attempt++ {
				_, rejected := issue1090GapsSend(t, handler, userID, false, fmt.Sprintf("red4-m6-retry-%d", attempt), frame)
				require.Equal(t, []any{"error"}, issue1090FrameTypes(rejected),
					"m6: stale metadata must produce only a visible error, not a cached session or a fresh acknowledgement")
				assert.Equal(t, clientID, rejected[0]["client_message_id"], "m6: generic error must correlate the exact retry")
				// RED4 explicitly requires this generic user-facing wording, not
				// filesystem details or an implementation-derived error string.
				assert.Equal(t, "Could not check this chat", rejected[0]["message"], "m6: stale retry must report the generic chat-check error")
				assert.NotContains(t, rejected[0], "session_id", "m6: rejection must never include the cached session ID")
				assert.NotContains(t, rejected[0], "recovered", "m6: rejection must never claim a cached recovery")
				wire, err := json.Marshal(rejected)
				require.NoError(t, err)
				assert.NotContains(t, string(wire), sessionID, "m6: no serialized error field may disclose the saved session ID")
				assert.NotContains(t, string(wire), store.BaseDir(), "m6: generic error must not reveal filesystem details")
				assert.Equal(t, []string{sessionID}, issue1090SessionDirectories(t, store), "m6: retry must never remint after a stale cache hit")
				assert.Equal(t, before, issue1090DiskTranscriptEntries(t, store, sessionID), "m6: blocked retry must not append or replace the saved entry")
				assert.Empty(t, issue1090AdmittedTurns(msgBus), "m6: stale retry must never admit another turn")
				assert.Empty(t, issue1090DrainQueuedFrames(t, originalConn), "m6: blocked retry must not publish cached bytes to the original hub")
			}
			t.Logf("m6 instrument: valid recovery first, then %s on disk, then two visible blocking errors", scenario)
		})
	}
}

func TestFirstUserMessage_ConcurrentIdenticalFirstSendsAreAdmittedOnce(t *testing.T) {
	const userID = "red4-m7-alice"
	// Sixteen independent pairs sample scheduling rather than assuming which
	// socket wins. This is a harness sample count, not a product limit. All
	// pairs run serially and stay below the specified per-principal cache cap.
	const pairs = 16
	handler, msgBus, store := issue1090GapsFixture(t)
	expectedSessions := []string{}
	require.Empty(t, issue1090SessionDirectories(t, store), "m7 fixture must begin with no saved chat sessions")
	for pair := 0; pair < pairs; pair++ {
		t.Run(fmt.Sprintf("pair_%02d", pair), func(t *testing.T) {
			clientID := fmt.Sprintf("red4-m7-identical-%02d", pair)
			content := fmt.Sprintf("Concurrent copies of first user message %d", pair)
			frame := issue1090GapsFrame(clientID, content)
			data, err := json.Marshal(frame)
			require.NoError(t, err)
			connections := [2]*wsConn{
				issue1090GapsConn(t, userID, false), issue1090GapsConn(t, userID, false),
			}
			var ready, finished sync.WaitGroup
			ready.Add(len(connections))
			finished.Add(len(connections))
			start := make(chan struct{})
			var flows [2]wsHandlerReadLoopFlow
			for index, wc := range connections {
				go func() {
					defer finished.Done()
					reader := &wsHandlerReadLoop{
						h: handler, ctx: context.Background(), wc: wc,
						chatID: fmt.Sprintf("red4-m7-pair-%d-socket-%d", pair, index),
					}
					ready.Done()
					<-start
					flows[index] = reader.dispatchFrame(data, wsTypeOnly{Type: frame.Type})
				}()
			}
			ready.Wait()
			close(start)    // Both sockets are ready before either identical send enters intake.
			finished.Wait() // Inspect only after BOTH complete: no timing proxy for no second turn.
			require.Equal(t, [2]wsHandlerReadLoopFlow{wsHandlerReadLoopNext, wsHandlerReadLoopNext}, flows,
				"m7: both identical messages must finish the real dispatcher")

			var sessionIDs [2]string
			fresh, recovered := 0, 0
			for index, wc := range connections {
				replies := issue1090DrainQueuedFrames(t, wc)
				sessionIDs[index] = issue1090StartedSessionID(t, replies)
				if replies[0]["recovered"] == true {
					recovered++
					issue1090AssertRecovered(t, replies, sessionIDs[index], clientID, "m7 concurrent duplicate")
				} else {
					fresh++
					issue1090GapsAssertFresh(t, replies, clientID, content)
				}
			}
			require.Equal(t, sessionIDs[0], sessionIDs[1], "m7: simultaneous identical first sends must acknowledge the SAME session")
			assert.Equal(t, 1, fresh, "m7: exactly one socket may receive a fresh-save acknowledgement and echo")
			assert.Equal(t, 1, recovered, "m7: the other socket must receive only a recovered receipt")
			sessionID := sessionIDs[0]
			expectedSessions = append(expectedSessions, sessionID)
			assert.ElementsMatch(t, expectedSessions, issue1090SessionDirectories(t, store), "m7: each pair must create exactly ONE session")
			issue1090AssertSavedFirstEntry(t, store, sessionID, content, clientID)
			assert.Equal(t, [][3]string{{sessionID, content, userID}}, issue1090AdmittedTurns(msgBus),
				"m7: two concurrent first sends must admit exactly ONE turn with the original arguments")
		})
	}
	// Count every on-disk entry, not just the acknowledged session's entry:
	// an unacknowledged duplicate chat must also make this invariant fail.
	entries := 0
	for _, sessionID := range issue1090SessionDirectories(t, store) {
		entries += len(issue1090DiskTranscriptEntries(t, store, sessionID))
	}
	assert.Equal(t, pairs, entries, "m7: all pairs together must leave exactly ONE transcript entry per pair")
	assert.Empty(t, issue1090AdmittedTurns(msgBus), "m7: no additional turn may remain after inspecting every pair's admission")
	t.Logf("m7 instrument: %d start-barrier pairs joined; one session, one disk entry and one real admission per pair", pairs)
}
