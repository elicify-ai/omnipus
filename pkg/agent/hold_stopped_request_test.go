package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Founder ruling 2026-10-11 (R1 #5): a request for a STOPPED agent is held and
// delivered when that agent runs again; the asker is told it is waiting. The
// durable ledger is the store (a delivered mark, no second queue). A held
// request whose asker is gone by delivery time is dropped visibly, never
// delivered into a void.

// heldSetup builds a stopped receiver main and a real asker conversation owned
// by agent "ray".
func heldSetup(t *testing.T) (f *addrFixture, recv, asker string) {
	t.Helper()
	f = newAddrFixture(t)
	lc := wireLifecycle(t, f)
	store := f.al.GetSessionStore()
	r, err := store.GetOrCreateMainSession(addrWS, addrReceiver)
	if err != nil {
		t.Fatal(err)
	}
	src, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	if err != nil {
		t.Fatal(err)
	}
	ws := addrWS
	if err := store.SetMeta(src.ID, session.MetaPatch{WorkspaceID: &ws}); err != nil {
		t.Fatal(err)
	}
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: r.ID, Generation: 2, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: addrWS, AgentID: addrReceiver,
		Stop: &session.Stop{At: time.Now(), Generation: 2, By: session.Principal{Kind: session.PrincipalKindHuman}},
	}); err != nil {
		t.Fatal(err)
	}
	return f, r.ID, src.ID
}

func heldAsk(t *testing.T, f *addrFixture, asker string) (reqID string, held bool) {
	t.Helper()
	req := f.peerReq()
	req.SenderSessionID = asker
	got, err := f.r.SendPeer(context.Background(), req)
	if err != nil {
		t.Fatalf("SendPeer: %v", err)
	}
	return got.RequestID, got.Held
}

// forgetInMemory simulates a gateway restart: only the ledger survives.
func forgetInMemory(al *AgentLoop) {
	al.pendingRequests.Range(func(k, _ any) bool { al.pendingRequests.Delete(k); return true })
	al.deliveredRequests.Range(func(k, _ any) bool { al.deliveredRequests.Delete(k); return true })
	al.remindedRequests.Range(func(k, _ any) bool { al.remindedRequests.Delete(k); return true })
	al.heldRepublished.Range(func(k, _ any) bool { al.heldRepublished.Delete(k); return true })
}

func TestHeldRequest_AskingAStoppedAgent_IsHeldAndTheAskerIsTold(t *testing.T) {
	f, recv, asker := heldSetup(t)
	id, held := heldAsk(t, f, asker)
	if !held {
		t.Fatal("receipt.Held = false: the asker is not told its request is waiting")
	}
	pending, err := f.al.RequestLedger().Undelivered(recv)
	if err != nil || len(pending) != 1 || pending[0].RequestID != id {
		t.Fatalf("held = %+v err=%v, want exactly request %s kept in the ledger", pending, err, id)
	}
	entries, _ := f.al.GetSessionStore().ReadTranscript(recv)
	if len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("receiver transcript = %v, want the request kept", entries)
	}
}

func TestHeldRequest_AskingARunnableAgent_IsNotHeld(t *testing.T) {
	f := newAddrFixture(t)
	wireLifecycle(t, f)
	if _, held := heldAsk(t, f, "sender-session"); held {
		t.Fatal("a request to an agent that is not stopped was reported as held")
	}
}

// "Runs again" = the next admitted turn on the stopped agent's session. The
// in-memory maps are wiped first: the ledger alone must carry the request across
// a restart.
func TestHeldRequest_DeliveredWhenTheAgentRunsAgain_AcrossARestart(t *testing.T) {
	f, recv, asker := heldSetup(t)
	id, _ := heldAsk(t, f, asker)
	// The wake admission published while the agent was stopped is dropped.
	if _, ok := addrDrainInbound(t, f.bus); !ok {
		t.Fatal("SETUP: no admission wake published")
	}
	forgetInMemory(f.al)

	f.al.deliverHeldRequests(recv)

	// Delivery re-publishes the request's wake with its full text; it is NOT
	// marked delivered until that wake is processed (the model has not seen it).
	in, ok := addrDrainInbound(t, f.bus)
	if !ok || in.SessionID != recv || in.TranscriptEntryID != id || in.Metadata[ownerWakeMetadataKey] == "" ||
		!strings.Contains(in.Content, "please review X") || !strings.Contains(in.Content, id) {
		t.Fatalf("re-published wake = %+v ok=%v, want the request text for %s as an owner wake", in, ok, id)
	}
	if left, _ := f.al.RequestLedger().Undelivered(recv); len(left) != 1 {
		t.Fatalf("held = %+v, want the request still held until its wake is processed", left)
	}
	// While that wake is in flight a further admitted turn does not duplicate it.
	f.al.deliverHeldRequests(recv)
	if dup, again := addrDrainInbound(t, f.bus); again {
		t.Fatalf("a second wake was published while the first is in flight: %+v", dup)
	}

	// The session processes the wake: that is what marks it delivered.
	f.al.markRequestDelivered(recv, id)
	if left, _ := f.al.RequestLedger().Undelivered(recv); len(left) != 0 {
		t.Fatalf("still held after its wake was processed: %+v", left)
	}
	c, ok, err := f.al.RequestLedger().Get(recv, id)
	if err != nil || !ok || !c.Delivered || c.Discarded {
		t.Fatalf("capture = %+v ok=%v err=%v, want delivered and answerable", c, ok, err)
	}
	if got := f.al.takeUnansweredRequests(recv); len(got) != 1 || got[0] != id {
		t.Fatalf("unanswered requests = %v, want [%s] tracked after delivery", got, id)
	}
}

// A wake that is dropped again (the agent stopped again) must be re-published by
// the next admitted turn, not blocked by the in-flight guard.
func TestHeldRequest_DroppedAgain_IsRepublishedByTheNextTurn(t *testing.T) {
	f, recv, asker := heldSetup(t)
	id, _ := heldAsk(t, f, asker)
	addrDrainInbound(t, f.bus)
	forgetInMemory(f.al)
	f.al.deliverHeldRequests(recv)
	wake, ok := addrDrainInbound(t, f.bus)
	if !ok {
		t.Fatal("no wake published")
	}

	// R2-S5-08 / R2-X-qa-06: DRIVE the drop through the real branch instead of
	// deleting the marker by hand (the old `f.al.heldRepublished.Delete(id)`
	// asserted nothing about the product — a regression that stopped deleting
	// on a dropped wake stayed green). The wake is admitted through the real
	// ordinary-inbound path; the owner is STOPPED (heldSetup persists a Stop
	// record), so prepareOrdinaryExecution returns errOwnerWakeRetained and
	// runInboundTurnWithRevival deletes the re-published marker
	// (revive_inbound.go, "A re-published held request is dropped again").
	// Land the stop first. The held fixture leaves an IN-FLIGHT stop fence (a
	// Stop marker on a RUNNING record); the retained-wake branch is reached
	// only by a LANDED stop — a landed stopped record carries no
	// current-generation stop marker (ADR-20260928 D2). Without this the
	// admission refuses on the in-flight fence and never reaches the branch
	// under test.
	lcStore := f.al.GetSessionLifecycleStore()
	if lcStore == nil {
		t.Fatal("SETUP: the fixture must have wired a lifecycle store")
	}
	if err := lcStore.Persist(&session.LifecycleRecord{
		SessionID: recv, Generation: 2, State: session.LifecycleStopped, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: addrWS, AgentID: addrReceiver,
		StopNote: &session.StopNote{At: time.Now(), By: "human", Seq: 1, Cause: session.StopCauseStop},
	}); err != nil {
		t.Fatalf("SETUP: land the stop: %v", err)
	}

	mintGenuineBootEpochForLoop(t, f.al)
	if _, _, err := f.al.processMessageAsPrimaryForTest(context.Background(), wake); err != nil {
		t.Fatalf("a dropped owner wake must not surface as an error to anyone: %v", err)
	}
	if _, inFlight := f.al.heldRepublished.Load(id); inFlight {
		t.Fatal("the dropped-wake branch must clear the re-published marker; while it is still set the request can never be re-published")
	}

	f.al.deliverHeldRequests(recv)
	if _, ok := addrDrainInbound(t, f.bus); !ok {
		t.Fatal("the next turn did not re-publish the dropped wake")
	}
}

func TestHeldRequest_AskerGone_DroppedNotDeliveredIntoAVoid(t *testing.T) {
	f, recv, asker := heldSetup(t)
	id, _ := heldAsk(t, f, asker)
	forgetInMemory(f.al)
	if err := f.al.GetSessionStore().DeleteSession(asker); err != nil {
		t.Fatalf("SETUP: delete the asker's chat: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("SETUP: enable file logging: %v", err)
	}
	restoreConsole := logger.DisableConsole()
	f.al.deliverHeldRequests(recv)
	restoreConsole()
	logger.DisableFileLogging()
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "held request dropped, not delivered") || !strings.Contains(string(logged), id) {
		t.Fatalf("the drop must be visible in the log with the request id; log = %q", logged)
	}

	if left, _ := f.al.RequestLedger().Undelivered(recv); len(left) != 0 {
		t.Fatalf("a request with no asker is still held: %+v", left)
	}
	_, err = f.al.RequestLedger().Resolve(recv, addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, id)
	if !errors.Is(err, addressing.ErrDiscarded) {
		t.Fatalf("Resolve = %v, want ErrDiscarded: the request must no longer be answerable", err)
	}
	if got := f.al.takeUnansweredRequests(recv); len(got) != 0 {
		t.Fatalf("a dropped request is tracked for a reminder: %v", got)
	}
	if _, delivered := f.al.deliveredRequests.Load(id); delivered {
		t.Fatal("a dropped request was marked delivered")
	}
}

// Wiring (S1-01): a real resume of a stopped session delivers its held request
// TO THE MODEL. A held request's text lives only in the ledger and the chat
// transcript (the transcript entry is chat-only), so delivery must put it in
// front of the model - not just flip a flag. The scripted provider asserts the
// model actually receives the request text.
func TestHeldRequest_HumanResumeOfTheStoppedAgentDeliversIt(t *testing.T) {
	// A third scripted answer, for the turn that carries the held request, is
	// staged before the first turn starts so no field is written under a
	// running provider call.
	t.Setenv(config.EnvHome, t.TempDir())
	const receiverWorkspace = testHarnessWorkspaceMembershipID
	al, original, provider := r5AdmitScopedRootWithAnswers(t, receiverWorkspace, "partial root work", "root resumed exactly once", "request answered")
	r5StopRoot(t, al, original)
	provider.open(0)
	joinGoalFixtureRuns(t, al)
	stopped := rootReopenedRecord(t, al, original.SessionID)
	if stopped.State != session.LifecycleStopped || stopped.Stop != nil || stopped.StopNote == nil {
		t.Fatalf("SETUP: real receiver Stop has not landed: %+v", stopped)
	}
	store := al.GetSessionStore()
	receiverMeta, err := store.GetMeta(original.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if receiverMeta == nil || receiverMeta.WorkspaceID != receiverWorkspace || receiverMeta.AgentID != testDefaultAgentID {
		t.Fatalf("SETUP: receiver was not created in its declared workspace: %+v", receiverMeta)
	}
	archiveBefore, err := store.ReadArchive(t.Context(), original.SessionID)
	if err != nil || len(archiveBefore) == 0 {
		t.Fatalf("SETUP: original receiver archive must be readable and nonempty: %v %v", archiveBefore, err)
	}
	transcriptBefore, err := store.ReadTranscript(original.SessionID)
	if err != nil || len(transcriptBefore) == 0 {
		t.Fatalf("SETUP: original receiver transcript must be readable and nonempty: %v %v", transcriptBefore, err)
	}
	asker, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	if err != nil {
		t.Fatal(err)
	}
	ws := addrWS
	if err = store.SetMeta(asker.ID, session.MetaPatch{WorkspaceID: &ws}); err != nil {
		t.Fatal(err)
	}
	askerMeta, err := store.GetMeta(asker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if askerMeta == nil || askerMeta.WorkspaceID != addrWS || askerMeta.AgentID != "ray" {
		t.Fatalf("SETUP: the held request must retain its genuine asker scope: %+v", askerMeta)
	}
	const body = "Please summarise the Q3 numbers"
	c := addressing.Capture{
		RequestID: "held-1", ReceiverSessionID: original.SessionID,
		Receiver: addressing.Pair{WorkspaceID: receiverMeta.WorkspaceID, AgentID: receiverMeta.AgentID},
		Source: addressing.Source{
			Kind: addressing.SourceConversation, Owner: addressing.Pair{WorkspaceID: askerMeta.WorkspaceID, AgentID: askerMeta.AgentID}, SessionID: asker.ID,
		},
		AdmittedAt: time.Now().UTC(),
		Text:       composeRequestText("held-1", "agent ray", body),
		Held:       true,
	}
	if err = al.RequestLedger().Put(c); err != nil {
		t.Fatalf("SETUP: hold a request: %v", err)
	}
	// Match real admission's chat-only request row. Do not put the body in
	// the model archive: held delivery must still carry it through the wake.
	if err = store.AppendTranscriptStrict(original.SessionID, session.TranscriptEntry{
		ID: c.RequestID, Role: "user", AgentID: receiverMeta.AgentID, Content: c.Text, Timestamp: c.AdmittedAt,
	}); err != nil {
		t.Fatalf("SETUP: persist the held request's visible row: %v", err)
	}

	msg := rootHumanMessage(original.SessionID)
	msg.GatewayUserID = "r5-owner"
	msg.Metadata["workspace_id"] = receiverWorkspace
	if err = al.enqueueSteeringFromMessage(msg); err != nil {
		t.Fatalf("resume: %v", err)
	}
	r1AwaitProvider(t, provider, 1)
	provider.open(1)
	// R2-X-qa-14: this context bounds the GOROUTINE's lifetime, not an
	// assertion deadline — the wait that can fail is the `wakeDeadline` poll
	// below, and it is unchanged at 10 s. The context must outlive that poll
	// with room for the turn's own provider calls (which is why it is not the
	// old 5 s), so it is DERIVED from the poll rather than being a second,
	// unexplained magic number.
	const wakeDeadline = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), wakeDeadline+20*time.Second)
	defer cancel()

	// The wake the delivery publishes is processed like any inbound. (Not
	// preceded by WaitForActiveRequests: that closes intake for good.)
	provider.open(2)
	var processed chan struct{}
	var wakeResult <-chan error
	select {
	case in := <-al.bus.InboundChan():
		if in.SessionID != original.SessionID || in.TranscriptEntryID != "held-1" {
			t.Fatalf("delivered wake = %+v, want the held request for the resumed session", in)
		}
		if in.Metadata["workspace_id"] != receiverWorkspace || in.Metadata["agent_id"] != receiverMeta.AgentID ||
			in.Metadata[ownerWakeMetadataKey] != "1" || in.UserInitiated || in.OperatorPrompt {
			t.Fatalf("held wake lost its receiver scope or non-human provenance: %+v", in)
		}
		processed = make(chan struct{})
		result := make(chan error, 1)
		wakeResult = result
		go func(done chan struct{}, results chan error) {
			defer close(done)
			_, _, wakeErr := al.processMessageAsPrimaryForTest(ctx, in)
			results <- wakeErr
		}(processed, result)
	case <-time.After(5 * time.Second):
		// The text may instead have been steered straight into the resumed turn.
	}
	deadline := time.After(wakeDeadline)
	seen := func() bool {
		for _, req := range provider.Requests() {
			for _, m := range req {
				if strings.Contains(m.Content, body) {
					return true
				}
			}
		}
		return false
	}
	for !seen() {
		select {
		case wakeErr := <-wakeResult:
			if wakeErr != nil {
				t.Fatalf("held request wake execution failed before model delivery: %v", wakeErr)
			}
			wakeResult = nil
		case <-deadline:
			t.Fatalf("the model never received the held request's text; provider calls=%d", len(provider.Requests()))
		case <-time.After(50 * time.Millisecond):
		}
	}
	got, ok, err := al.RequestLedger().Get(original.SessionID, "held-1")
	if err != nil || !ok || !got.Delivered {
		t.Fatalf("held request after the model received it = %+v ok=%v err=%v, want delivered", got, ok, err)
	}
	if processed != nil {
		select {
		case <-processed:
			if wakeResult != nil {
				if wakeErr := <-wakeResult; wakeErr != nil {
					t.Fatalf("held request wake execution failed: %v", wakeErr)
				}
			}
		case <-ctx.Done():
			t.Fatal("the wake's turn did not finish")
		}
	}
	if !al.WaitForActiveRequestsContext(ctx) {
		t.Fatal("resumed receiver's active-request tail did not finish")
	}
	joinGoalFixtureRuns(t, al)
	archiveAfter, err := store.ReadArchive(t.Context(), original.SessionID)
	if err != nil || len(archiveAfter) < len(archiveBefore) || !reflect.DeepEqual(archiveBefore, archiveAfter[:len(archiveBefore)]) {
		t.Fatalf("held delivery rewrote the receiver's old model history: %v %v", archiveAfter, err)
	}
	transcriptAfter, err := store.ReadTranscript(original.SessionID)
	if err != nil || len(transcriptAfter) < len(transcriptBefore) || !reflect.DeepEqual(transcriptBefore, transcriptAfter[:len(transcriptBefore)]) {
		t.Fatalf("held delivery rewrote the receiver's old visible history: %v %v", transcriptAfter, err)
	}
	requestRows := 0
	for _, entry := range transcriptAfter {
		if entry.ID == c.RequestID {
			requestRows++
			if entry.Content != c.Text {
				t.Fatalf("held request's visible body changed: %q", entry.Content)
			}
		}
	}
	if requestRows != 1 {
		t.Fatalf("held delivery must preserve exactly one visible request row, got %d", requestRows)
	}
	metaAfter, err := store.GetMeta(original.SessionID)
	if err != nil || metaAfter == nil || metaAfter.WorkspaceID != receiverWorkspace {
		t.Fatalf("human resume changed the receiver's workspace: %+v %v", metaAfter, err)
	}
	undelivered, err := al.RequestLedger().Undelivered(original.SessionID)
	if err != nil || len(undelivered) != 0 {
		t.Fatalf("request remains held after real model delivery: %+v %v", undelivered, err)
	}
	al.deliverHeldRequests(original.SessionID)
	select {
	case duplicate := <-al.bus.InboundChan():
		t.Fatalf("an already-delivered request was republished: %+v", duplicate)
	default:
	}
}

// S1-03: an asker that cannot be READ (a corrupt or unreadable meta file) is not
// evidence that its conversation is gone. The request stays held, is not
// discarded, and is retried by the next turn; only a definitively missing asker
// drops it.
func TestHeldRequest_UnreadableAsker_StaysHeldNotDiscarded(t *testing.T) {
	f, recv, _ := heldSetup(t)
	store := f.al.GetSessionStore()
	const asker = "session_corrupt_meta_asker"
	dir := filepath.Join(store.BaseDir(), asker)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := addressing.Capture{
		RequestID: "held-unreadable", ReceiverSessionID: recv,
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver},
		Source:   addressing.Source{Kind: addressing.SourceConversation, SessionID: asker},
		Text:     composeRequestText("held-unreadable", "agent ray", "body"),
	}
	if err := f.al.RequestLedger().Put(c); err != nil {
		t.Fatal(err)
	}

	f.al.deliverHeldRequests(recv)

	left, err := f.al.RequestLedger().Undelivered(recv)
	if err != nil || len(left) != 1 || left[0].RequestID != "held-unreadable" || left[0].Discarded {
		t.Fatalf("held = %+v err=%v, want the request kept (an unreadable asker is not a gone asker)", left, err)
	}
	if in, published := addrDrainInbound(t, f.bus); published {
		t.Fatalf("a request whose asker is unreadable must not be delivered yet: %+v", in)
	}
}

// S1-13: a receiver whose lifecycle record cannot be read is not reported as
// running: the asker is told its request is held, not that it was seen.
func TestHeldRequest_UnreadableReceiverRecord_IsReportedAsHeld(t *testing.T) {
	f := newAddrFixture(t)
	dir := t.TempDir()
	f.al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(dir))
	main, err := f.al.GetSessionStore().GetOrCreateMainSession(addrWS, addrReceiver)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, main.ID+".jsonl"), []byte("{torn\n{also torn\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, held := heldAsk(t, f, "sender-session"); !held {
		t.Fatal("an unreadable receiver record was reported as not held: the asker is told its request was seen")
	}
}

// Architect ruling 2026-10-12 (PublishInbound census): the wake a held request
// is re-published with is the SAME message the admission published - one
// helper, publishRequestWake, so the two cannot drift. Compared field by field
// across a real admission and a real held delivery.
func TestHeldRequest_RepublishedWakeIsTheAdmissionWake(t *testing.T) {
	f, recv, asker := heldSetup(t)
	id, held := heldAsk(t, f, asker)
	if !held {
		t.Fatal("precondition: the request is held")
	}
	first := nextWake(t, f.al) // the wake the admission published
	forgetInMemory(f.al)
	f.al.deliverHeldRequests(recv)
	second := nextWake(t, f.al) // the wake the held path re-published

	if first.TranscriptEntryID != id {
		t.Fatalf("instrument check: first wake names request %s, got %q", id, first.TranscriptEntryID)
	}
	if first.OperatorPrompt || second.OperatorPrompt || first.UserInitiated || second.UserInitiated {
		t.Fatal("a request wake is never an operator prompt or a user-initiated turn")
	}
	if first.Metadata[ownerWakeMetadataKey] != "1" || second.Metadata[ownerWakeMetadataKey] != "1" {
		t.Fatalf("both wakes carry the owner-wake marker: %v / %v", first.Metadata, second.Metadata)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("the held re-publication differs from the admission wake:\nadmit: %+v\nheld:  %+v", first, second)
	}
}

func nextWake(t *testing.T, al *AgentLoop) bus.InboundMessage {
	t.Helper()
	select {
	case m := <-al.bus.InboundChan():
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no wake was published")
		return bus.InboundMessage{}
	}
}

// R2-S1-02: the "wake already on its way" marker expires. A republished wake
// can be lost without any signal (full inbox, panic, worker exit); the request
// must not stay skipped for the life of the process.
func TestHeldRequest_LostRepublishedWake_IsRepublishedAfterTheMarkerExpires(t *testing.T) {
	f, recv, asker := heldSetup(t)
	id, _ := heldAsk(t, f, asker)
	addrDrainInbound(t, f.bus) // the admission wake, dropped by the stopped agent
	forgetInMemory(f.al)

	f.al.deliverHeldRequests(recv)
	if _, ok := addrDrainInbound(t, f.bus); !ok {
		t.Fatal("no republished wake")
	}
	f.al.deliverHeldRequests(recv)
	if _, again := addrDrainInbound(t, f.bus); again {
		t.Fatal("a second wake was published while the first was fresh")
	}
	// The wake was lost without a trace; time passes.
	f.al.heldRepublished.Store(id, time.Now().Add(-2*heldWakeInFlightTTL))
	f.al.deliverHeldRequests(recv)
	if _, ok := addrDrainInbound(t, f.bus); !ok {
		t.Fatal("a lost republished wake was never retried: the request stays skipped until restart")
	}
}

// R2-S1-04: a request admitted to a runnable receiver has its own wake in flight
// from admission; another turn admitted before that wake is processed must not
// publish a second copy of the same text.
func TestHeldRequest_AdmissionWakeInFlight_IsNotDuplicatedByAnotherTurn(t *testing.T) {
	f := newAddrFixture(t)
	wireLifecycle(t, f)
	asker, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "ray")
	if err != nil {
		t.Fatal(err)
	}
	ws := addrWS
	if err = f.al.GetSessionStore().SetMeta(asker.ID, session.MetaPatch{WorkspaceID: &ws}); err != nil {
		t.Fatal(err)
	}
	req := f.peerReq()
	req.SenderSessionID = asker.ID
	got, err := f.r.SendPeer(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := addrDrainInbound(t, f.bus); !ok {
		t.Fatal("SETUP: no admission wake")
	}
	// Its wake sits in a worker inbox; another turn of the session is admitted.
	f.al.deliverHeldRequests(got.SessionID)
	if dup, again := addrDrainInbound(t, f.bus); again {
		t.Fatalf("a duplicate wake was published for a request whose own wake is still in flight: %+v", dup)
	}
}
