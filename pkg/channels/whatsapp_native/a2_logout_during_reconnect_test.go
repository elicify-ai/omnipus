//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// Round 11 of issue #1687 (docs-verifier note A2, CHECK note N1): a logout that
// reaches the channel while reconnectWithBackoff is running is dropped.
// whatsapp_native.go::eventHandler returns early for events.LoggedOut when
// c.reconnecting is set, so the loop goes on trying a device the phone has
// removed and the status keeps saying the connection "is being re-established
// automatically", which is false.
//
// TEST PLAN (elicify-test-writing, step 1)
//
// Behaviour under test: a logout ends in the same observable state whichever
// comes first — (a) it arrives while no reconnect runs (handleLoggedOut's path),
// or (b) it arrives while the reconnect loop runs.
//
// Specification source: the round-11 brief from team-lead (order independence),
// three clauses for case (b), after the loop has had its chance to act:
//  1. the status reason must not say the connection is being re-established
//     automatically;
//  2. the loop must stop redialling: no connect attempt after the logout is seen;
//  3. the state must match what case (a) leaves: same running flag, same reason,
//     same stored-device outcome.
//
// Round 12 adds a fourth clause (CHECK4 finding F1, the round-12 brief from
// team-lead): the logout is handled once. Clauses 1-3 look at the end state, and
// a second repair attempt for the same logout fails exactly as the first one
// does (open issue #1705: the attempt always ends start_failed), so it leaves
// that state unchanged and no end-state clause can see it. CHECK4's mutant
// "handle logout twice" (endReconnect runs the logout handling a second time)
// ran two real repair attempts and every clause above stayed green. Clause 4
// therefore counts what the logout did, against case (a) counted the same way:
//
//	4a. the repair attempts the logout started (distinct attempt ids on the
//	    manager's attempt observer) and the repair attempts the manager's
//	    attempt store admitted;
//	4b. the pairing events those attempts reported, in order.
//
// The user documentation says the same in words (docs/connectors/whatsapp_native.md,
// "After pairing": after a phone logout "reconnecting cannot repair the link").
//
// Where the expected values come from. Clause 1 is the phrase "re-established",
// held below as a literal; it is not read from production code. Clauses 2 and 3
// have NO expected value written in this file: case (a) runs first, in the same
// test, on its own fixture, and the outcome it leaves is the expectation for
// case (b). Case (a) additionally pins two properties of itself (the device is
// removed from the store; no re-established text) so the baseline is known to
// be a real logout outcome and not an empty one. Clause 4 follows the same
// rule: nothing is written down for case (b). Case (a) pins that one logout
// starts exactly one repair attempt (the round-12 brief names two attempts for
// one logout as the defect) and that the attempt store admitted it, so the
// counts compared are real counts and not two zeros.
//
// Cases.
//
//	(a) a logout on a healthy, linked, running channel.
//	(b) a logout while the reconnect loop is running and failing. The remote
//	    end drops the connection; every reconnect dial is refused by a counting
//	    endpoint; the loop's next iteration is parked just before its dial
//	    (fix1HoldCapturedReconnect, the log-clock seam, which replaces no native
//	    method); the logout is delivered through the channel's real handler; the
//	    loop is released and given its chance.
//
// Both cases measure the same things the same way: the running flag, the
// availability state and reason the manager reports (the only status surface
// that carries a reason), the channel's own NotRunningReason, the connect
// attempts made after the logout, whether the reconnect/repair flag is still
// set, the repair attempts the logout started, the pairing events they
// reported and the admissions the attempt store accepted (all three read before
// the channel stops, because a stop reports events of its own), and the linked
// devices left in the on-disk store (read after the channel has stopped, so no
// writer and no reader overlap).
//
// Fixture wiring. The production factory (init.go) sets the channel's
// originConfig to its config snapshot; the bare test fixture leaves it nil, and
// PairingOrder.MatchesConfig refuses a nil config, so ActivateRepair would
// refuse the repair and handleLoggedOut would stop at "Re-pairing was not
// admitted" without deleting anything. a2WireLikeTheFactory sets it the same
// way the factory does, under c.mu, which guards every read of the field.
//
// Instruments checked in the test itself: the counting endpoint sees the
// refused dial that opens the failing window; the stored-device reader sees
// the seeded device in a fresh store; before the logout the failing window
// shows the re-established text, so a change after it is the logout's doing;
// the loop is running (c.reconnecting) when the logout is delivered; the pairing
// recorder sees the idle logout's one repair attempt and the attempt store
// admits it (case (a) pins both), so equal counts in the two cases are not two
// unread zeros.
//
// What would break this (mutations for CHECK): keep dropping the logout while
// c.reconnecting is set (this test is red today); handle the logout but leave
// reconnectFailing set (the reason clauses fail); stop the dials but leave the
// device in the store (the stored-device clause fails); delete the device but
// let the loop dial once more (the dial clause fails); run the logout handling
// a second time after the loop's hand-off, CHECK4's "handle logout twice"
// (clauses 1-3 stay green, clause 4 fails; the round-12 report shows the run).
//
// Known gaps, deliberately not covered here: the loop asleep in its backoff
// wait when the logout arrives; the logout that arrives during the reconnect's
// own dial (OnConnect true); the library's own store deletion on a logout
// (connectionevents.go deletes the store itself in both logout paths, which
// this test does not simulate); a Stop racing the logout; a duplicate that
// neither admits an attempt nor reports an event (a second Disconnect or store
// delete is idempotent and leaves no trace at this level); a duplicate that
// lands later than the quiet window.
//
// What the baseline (a) leaves for the running flag and the availability state
// is observed at run time, not assumed; see the round-11 report for what it was.

const (
	// a2Instance is the key the channel is registered under, so the manager's
	// availability answer can be read.
	a2Instance = "wa-a2"
	// a2ReconnectPhrase is the phrase from the brief that the status must not
	// carry after a logout.
	a2ReconnectPhrase = "re-established"
	// a2SettleBound bounds the wait for the channel to finish acting on a logout.
	a2SettleBound = 5 * time.Second
	// a2QuietWindow is observed after the channel has settled, so a late stray
	// dial or a late state change can still show up before the measurement.
	a2QuietWindow = time.Second
	// a2HandlerGrace bounds how long the delivery waits for eventHandler to
	// return before the test carries on.
	a2HandlerGrace = 2 * time.Second
)

// a2DialCounter stands in for a network that never lets a connect through: it
// accepts every TCP connection, closes it at once and counts it. The channel's
// client is pointed at it as its HTTP proxy, so each connect attempt reaches it,
// fails, and is counted. It is the instrument that tells "the loop dialled
// again" from "the loop stopped".
type a2DialCounter struct {
	accepts  atomic.Int32
	proxyURL *url.URL
}

func a2NewDialCounter(t *testing.T) *a2DialCounter {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxyURL, err := url.Parse("http://" + listener.Addr().String())
	require.NoError(t, err)
	counter := &a2DialCounter{proxyURL: proxyURL}
	acceptorDone := make(chan struct{})
	go func() {
		defer close(acceptorDone)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return // the cleanup closed the listener
			}
			counter.accepts.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptorDone
	})
	return counter
}

// total is every connect attempt so far: the refused ones this endpoint
// counted plus the handshakes the healthy fake server completed.
func (d *a2DialCounter) total(server *fix1NoiseServer) int32 {
	return d.accepts.Load() + server.handshakes.Load()
}

// a2Outcome is what the channel looks like once a logout has been handled.
type a2Outcome struct {
	running       bool
	availState    channels.AvailabilityState
	availReason   string
	ownReason     string // the channel's own NotRunningReason()
	dialsSince    int32  // connect attempts made after the logout was delivered
	loopActive    bool   // the reconnect/repair flag (c.reconnecting) is still set
	storedDevices int    // linked devices left in the on-disk store
	repairs       a2Repairs
}

// a2Repairs counts what a logout's handling did to the pairing machinery. The
// end state cannot show it: a second repair attempt for the same logout fails
// like the first one (open issue #1705) and leaves the same state behind.
type a2Repairs struct {
	// attempts is the number of distinct pairing attempts that reported an event
	// after the logout.
	attempts int
	// events is those events in order, "<status>: <message>". Attempt ids and QR
	// codes are left out: they differ on every run.
	events []string
	// admissions is the number of repair attempts the manager's attempt store
	// admitted after the logout.
	admissions uint64
}

// a2PairingEvent is one pairing event as the manager's attempt observer gets it.
type a2PairingEvent struct {
	channelID string
	attemptID string
	status    channels.PairingStatus
	message   string
}

// a2PairingRecorder is the manager's pairing-attempt observer. It keeps, in
// order, every pairing event a channel reports through the manager, which is the
// stream the gateway turns into the whatsapp_pairing frames the SPA shows. The
// QR payload is not kept.
type a2PairingRecorder struct {
	mu     sync.Mutex
	events []a2PairingEvent
}

func (r *a2PairingRecorder) record(channelID, attemptID string, status channels.PairingStatus, _, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, a2PairingEvent{channelID: channelID, attemptID: attemptID, status: status, message: message})
}

func (r *a2PairingRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// after returns a copy of the events recorded after the first skip ones.
func (r *a2PairingRecorder) after(skip int) []a2PairingEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]a2PairingEvent(nil), r.events[skip:]...)
}

// a2AdmissionStamp is the generation stamp of the latest pairing attempt the
// manager's attempt store holds for the channel, zero while it holds none. The
// store advances its stamp once for every repair it admits
// (pairing_attempts.go::ActivateRepair), so the difference between two readings
// counts the admissions between them while nothing else touches the store.
func a2AdmissionStamp(m *channels.Manager) uint64 {
	latest, ok := m.PairingAttempts().Latest(a2Instance)
	if !ok {
		return 0
	}
	return latest.Generation
}

// a2WatchRepairs marks the moment before the logout is delivered and returns
// the reader of what the logout's handling did to the pairing machinery since.
func a2WatchRepairs(rec *a2PairingRecorder, m *channels.Manager) func() a2Repairs {
	mark, stamp0 := rec.count(), a2AdmissionStamp(m)
	return func() a2Repairs {
		attempts := make(map[string]struct{})
		events := make([]string, 0)
		for _, ev := range rec.after(mark) {
			if ev.channelID != a2Instance {
				continue
			}
			attempts[ev.attemptID] = struct{}{}
			events = append(events, string(ev.status)+": "+ev.message)
		}
		return a2Repairs{attempts: len(attempts), events: events, admissions: a2AdmissionStamp(m) - stamp0}
	}
}

// a2StoredDevices counts the linked devices in the store file under storeDir.
// Call it only when no channel has the file open for writing.
func a2StoredDevices(t *testing.T, storeDir string) int {
	t.Helper()
	db, err := sql.Open(sqliteDriver, "file:"+filepath.Join(storeDir, whatsappDBName)+"?_foreign_keys=on")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	devices, err := sqlstore.NewWithDB(db, sqliteDriver, waLog.Noop).GetAllDevices(context.Background())
	require.NoError(t, err, "instrument: the store file can be read")
	return len(devices)
}

// a2WireLikeTheFactory gives the channel the config snapshot the production
// factory gives it (init.go: native.originConfig = cfg). Every read of the field
// holds c.mu, so the write does too.
func a2WireLikeTheFactory(ch *WhatsAppNativeChannel) {
	ch.mu.Lock()
	ch.originConfig = &config.Config{}
	ch.mu.Unlock()
}

// a2RegisterWithManager registers the channel on a real manager, whose
// Availability is the only status surface that carries a reason, and returns the
// recorder that receives every pairing event the channel reports through it.
func a2RegisterWithManager(t *testing.T, ch *WhatsAppNativeChannel) (*channels.Manager, *a2PairingRecorder) {
	t.Helper()
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := channels.NewManager(&config.Config{Channels: map[string]config.ChannelInstanceConfig{}},
		credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	startCtx, cancelStart := context.WithCancel(context.Background())
	t.Cleanup(cancelStart)
	require.NoError(t, m.StartAll(startCtx))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	// The observer goes in before the channel does: the manager hands it to a
	// channel only at the moment it wires that channel in.
	rec := &a2PairingRecorder{}
	m.SetPairingAttemptObserver(rec.record)
	m.RegisterChannel(a2Instance, ch)
	return m, rec
}

// a2DeliverLogout hands the channel a phone-initiated logout through the
// handler Start registered, as the library does for a device_removed stream
// error (whatsmeow connectionevents.go: LoggedOut{OnConnect: false,
// Reason: ConnectFailureLoggedOut}). It runs the handler on its own goroutine
// and waits for it to return, so a handler that blocks until the reconnect loop
// is released cannot deadlock the test.
func a2DeliverLogout(t *testing.T, ch *WhatsAppNativeChannel) {
	t.Helper()
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		ch.eventHandler(&events.LoggedOut{OnConnect: false, Reason: events.ConnectFailureLoggedOut})
	}()
	select {
	case <-returned:
	case <-time.After(a2HandlerGrace):
		t.Logf("note: eventHandler(LoggedOut) had not returned after %v; carrying on", a2HandlerGrace)
	}
}

// a2Await polls cond until it holds or bound has passed. It reports whether it
// held and never fails the test: it only decides when to measure.
func a2Await(bound time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(bound)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

// a2Measure records the channel's state, then stops the channel so the store
// file can be read with no writer open. The repair counts are read with the rest
// of the state, before the stop, because a stop reports pairing events of its own.
func a2Measure(t *testing.T, f *nativeFixture, m *channels.Manager, dialsSince func() int32, repairsSince func() a2Repairs) a2Outcome {
	t.Helper()
	avail := m.Availability(a2Instance)
	out := a2Outcome{
		running:     f.ch.IsRunning(),
		availState:  avail.State,
		availReason: avail.Reason,
		ownReason:   f.ch.NotRunningReason(),
		dialsSince:  dialsSince(),
		loopActive:  w2ReconnectingUnderLock(f.ch),
		repairs:     repairsSince(),
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelStop()
	assert.NoError(t, f.ch.Stop(stopCtx), "instrument: the channel stops cleanly so its store file can be read")
	out.storedDevices = a2StoredDevices(t, f.ch.storePath)
	t.Logf("outcome: running=%v availability=%s/%q ownReason=%q dialsSince=%d loopActive=%v storedDevices=%d",
		out.running, out.availState, out.availReason, out.ownReason, out.dialsSince, out.loopActive, out.storedDevices)
	t.Logf("repairs: attempts=%d admissions=%d events=%q", out.repairs.attempts, out.repairs.admissions, out.repairs.events)
	return out
}

// a2RunIdleLogout is case (a): a logout on a healthy, linked, running channel.
func a2RunIdleLogout(t *testing.T) a2Outcome {
	t.Helper()
	f, server, captured, _ := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	refused := a2NewDialCounter(t)
	captured.SetProxy(http.ProxyURL(refused.proxyURL))
	a2WireLikeTheFactory(f.ch)
	m, rec := a2RegisterWithManager(t, f.ch)
	require.True(t, f.ch.IsRunning(), "instrument: the linked channel is running before the logout")
	require.False(t, w2ReconnectingUnderLock(f.ch), "instrument: no reconnect runs when the logout arrives")
	before := refused.total(server)
	repairsSince := a2WatchRepairs(rec, m)

	a2DeliverLogout(t, f.ch)
	settled := a2Await(a2SettleBound, func() bool { return !w2ReconnectingUnderLock(f.ch) })
	time.Sleep(a2QuietWindow)
	t.Logf("case (a): settled=%v before the quiet window", settled)
	return a2Measure(t, f, m, func() int32 { return refused.total(server) - before }, repairsSince)
}

// a2RunLogoutDuringReconnect is case (b): a logout while the reconnect loop is
// running and failing.
func a2RunLogoutDuringReconnect(t *testing.T) a2Outcome {
	t.Helper()
	f, server, captured, remote := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	refused := a2NewDialCounter(t)
	captured.SetProxy(http.ProxyURL(refused.proxyURL))
	a2WireLikeTheFactory(f.ch)
	m, rec := a2RegisterWithManager(t, f.ch)

	w7EnterFailingReconnectWindow(t, f, captured, remote)
	require.GreaterOrEqual(t, refused.accepts.Load(), int32(1),
		"instrument: the refused reconnect dial that opened the failing window reached the counting endpoint")
	require.Equal(t, w7ApprovedReconnectReason, f.ch.NotRunningReason(),
		"instrument: before the logout the failing window shows the re-established text, so a change after it is the logout's doing")

	// Park the loop's next iteration just before its dial, so the loop is
	// provably running when the logout arrives.
	held, release := fix1HoldCapturedReconnect(t)
	select {
	case <-held:
	case <-time.After(12 * time.Second):
		t.Fatal("HARNESS: the managed reconnect loop never reached its next iteration")
	}
	require.True(t, w2ReconnectingUnderLock(f.ch), "instrument: the reconnect loop is running when the logout arrives")
	before := refused.total(server)
	repairsSince := a2WatchRepairs(rec, m)

	a2DeliverLogout(t, f.ch)
	release() // the loop now has its chance to act
	settled := a2Await(a2SettleBound, func() bool {
		return !w2ReconnectingUnderLock(f.ch) || refused.total(server) > before
	})
	time.Sleep(a2QuietWindow)
	t.Logf("case (b): settled=%v before the quiet window", settled)
	return a2Measure(t, f, m, func() int32 { return refused.total(server) - before }, repairsSince)
}

// TestLogout_ArrivingDuringAReconnectEndsInTheSameStateAsAnIdleLogout is the
// round-11 order-independence test for issue #1687 (A2). See the plan above.
func TestLogout_ArrivingDuringAReconnectEndsInTheSameStateAsAnIdleLogout(t *testing.T) {
	require.Equal(t, 1, a2StoredDevices(t, w2SeedLinkedStore(t)),
		"instrument: the stored-device reader sees the device a freshly seeded linked store holds")

	var idle a2Outcome
	idleRan := false
	t.Run("a_logout_while_no_reconnect_runs", func(t *testing.T) {
		idle = a2RunIdleLogout(t)
		idleRan = true
		// Characterization of the baseline, from handleLoggedOut's contract (it
		// "clears the current session", US-2): the logged-out device is gone.
		assert.Zero(t, idle.storedDevices,
			"case (a): an idle logout must remove the logged-out device from the store")
		// No reconnect failed in this case, so no re-established text can apply.
		assert.NotContains(t, strings.ToLower(idle.availReason), a2ReconnectPhrase,
			"case (a): the availability reason after an idle logout must not say the connection is being re-established")
		assert.NotContains(t, strings.ToLower(idle.ownReason), a2ReconnectPhrase,
			"case (a): the channel's own reason after an idle logout must not say the connection is being re-established")
		// Instruments for clause 4 (round 12). One logout starts one repair
		// attempt; two attempts for one logout is the defect CHECK4 found (F1).
		// The pins also show the recorder and the attempt store read real
		// activity, so equal counts in case (b) are not two unread zeros.
		assert.Equal(t, 1, idle.repairs.attempts,
			"case (a): one logout must start exactly one repair attempt on the manager's attempt observer (events seen: %q)", idle.repairs.events)
		assert.GreaterOrEqual(t, idle.repairs.admissions, uint64(1),
			"instrument: the manager's attempt store must have admitted the idle logout's repair attempt, or case (b)'s admission count has nothing to be compared with")
	})
	if !idleRan {
		t.Fatal("instrument: case (a) did not complete, so case (b) has no baseline to compare with")
	}

	t.Run("b_logout_while_the_reconnect_loop_runs", func(t *testing.T) {
		// The scenario runs once; each brief clause is then judged on the
		// recorded outcome as its own subtest, so a log shows which clauses hold.
		during := a2RunLogoutDuringReconnect(t)

		t.Run("clause_1_status_does_not_claim_an_automatic_reconnect", func(t *testing.T) {
			// The device is logged out; the loop cannot re-establish it, so the
			// status must not say it is doing that.
			assert.NotContains(t, strings.ToLower(during.availReason), a2ReconnectPhrase,
				"the availability reason must not say the connection is being re-established automatically (reason now %q)", during.availReason)
			assert.NotContains(t, strings.ToLower(during.ownReason), a2ReconnectPhrase,
				"the channel's own reason must not say the connection is being re-established automatically (reason now %q)", during.ownReason)
		})

		t.Run("clause_2_the_loop_stops_redialling", func(t *testing.T) {
			// The expectation is what an idle logout does once it is seen (case (a),
			// measured the same way), not a number written here.
			assert.Equal(t, idle.dialsSince, during.dialsSince,
				"connect attempts after the logout was seen must be what an idle logout makes (%d)", idle.dialsSince)
			assert.Equal(t, idle.loopActive, during.loopActive,
				"the reconnect loop must be gone once the logout is handled, as after an idle logout (loop flag %v there)", idle.loopActive)
		})

		t.Run("clause_3a_running_state_and_reason_match_an_idle_logout", func(t *testing.T) {
			assert.Equal(t, idle.running, during.running,
				"the running flag must be what an idle logout leaves (%v)", idle.running)
			assert.Equal(t, idle.availState, during.availState,
				"the availability state must be what an idle logout leaves (%s)", idle.availState)
			assert.Equal(t, idle.availReason, during.availReason,
				"the availability reason must be what an idle logout leaves (%q)", idle.availReason)
			assert.Equal(t, idle.ownReason, during.ownReason,
				"the channel's own reason must be what an idle logout leaves (%q)", idle.ownReason)
		})

		t.Run("clause_3b_stored_device_matches_an_idle_logout", func(t *testing.T) {
			assert.Equal(t, idle.storedDevices, during.storedDevices,
				"the stored-device outcome must be what an idle logout leaves (%d linked devices in the store)", idle.storedDevices)
		})

		// Clause 4 (round 12, CHECK4 F1): the logout is handled once. The end state
		// cannot show a second repair attempt, so the attempts are counted, against
		// what an idle logout makes (case (a), counted the same way).
		t.Run("clause_4a_repair_attempts_match_an_idle_logout", func(t *testing.T) {
			assert.Equal(t, idle.repairs.attempts, during.repairs.attempts,
				"the logout must start as many repair attempts as an idle logout does (%d there); events seen here: %q",
				idle.repairs.attempts, during.repairs.events)
			assert.Equal(t, idle.repairs.admissions, during.repairs.admissions,
				"the attempt store must admit as many repair attempts as after an idle logout (%d there)", idle.repairs.admissions)
		})

		t.Run("clause_4b_pairing_events_match_an_idle_logout", func(t *testing.T) {
			assert.Equal(t, idle.repairs.events, during.repairs.events,
				"the pairing events the logout reports must be what an idle logout reports (%q)", idle.repairs.events)
		})
	})
}
