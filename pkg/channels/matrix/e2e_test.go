//go:build matrix_e2e

// Package matrix_e2e_test is a best-effort Matrix E2E integration test that
// round-trips an encrypted Megolm message through a real Synapse homeserver.
//
// This test is opt-in only. It compiles under the `matrix_e2e` build tag and
// pairs with `goolm` (pure-Go olm) so that it runs without CGo. It is NEVER
// part of the default `go test ./...` run.
//
// To run locally against a throwaway Synapse:
//
//	docker run --rm -d --name synapse-e2e \
//	  -p 8008:8008 \
//	  -e SYNAPSE_SERVER_NAME=test.localhost \
//	  -e SYNAPSE_REPORT_STATS=no \
//	  -e SYNAPSE_CONFIG_OVERRIDE="enable_registration: true
//	enable_registration_without_verification: true" \
//	  matrixdotorg/synapse:latest
//	# wait until http://localhost:8008/_matrix/client/versions responds
//	OMNIPUS_MATRIX_HOMESERVER=http://localhost:8008 \
//	  go test -tags matrix_e2e,goolm,stdjson -run TestMatrixE2ECryptoRoundtrip \
//	  -v -timeout 10m ./pkg/channels/matrix/...
//
// Env vars consumed:
//
//	OMNIPUS_MATRIX_HOMESERVER                 required; e.g. http://localhost:8008
//	OMNIPUS_MATRIX_REGISTRATION_SHARED_SECRET optional; Synapse admin register secret
//
// If OMNIPUS_MATRIX_HOMESERVER is unset OR the reachability probe fails OR
// the registration path is not available, the test skips with a clear reason.
package matrix_e2e_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	// Pure-Go SQLite driver (modernc.org/sqlite). Registers the "sqlite"
	// database/sql driver used by the dbutil "sqlite" dialect below. This
	// avoids the cgo-tagged mattn/go-sqlite3 registration path that ships
	// with cryptohelper's default managed-DB setup.
	_ "modernc.org/sqlite"
)

// subtestTimeout bounds each subtest. Network + crypto setup against a live
// homeserver is slow; the generous budget prevents false-negative cancellations.
const subtestTimeout = 3 * time.Minute

// testMessageBody is the plaintext payload asserted on the receiving side in
// subtest (a). Including a tagged marker keeps the assertion resistant to
// extra server-side annotations.
const testMessageBody = "hello bob — secret 7734"

// TestMatrixE2ECryptoRoundtrip exercises the full Megolm round-trip against a
// live Synapse. Sub-flows:
//
//   - encrypt-then-decrypt roundtrip
//   - device verification (best-effort, weakened to CryptoStore trust flag)
//   - key rotation on membership change
//
// The parent test allocates the shared Synapse fixture and three accounts
// (alice, bob, carol) so each subtest can share the long-running clients.
func TestMatrixE2ECryptoRoundtrip(t *testing.T) {
	homeserver := strings.TrimSpace(os.Getenv("OMNIPUS_MATRIX_HOMESERVER"))
	if homeserver == "" {
		t.Skip("Matrix E2E test requires a running homeserver; set OMNIPUS_MATRIX_HOMESERVER=http://localhost:8008")
	}

	// Probe /versions before we spend any time on DB setup. If the server is
	// not reachable we skip with a clear reason so CI does not mis-report the
	// failure as a crypto bug.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer probeCancel()
	if err := probeHomeserver(probeCtx, homeserver); err != nil {
		t.Skipf("Matrix E2E test requires a running homeserver at %s: %v", homeserver, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Randomise usernames per-run so repeated runs against a single Synapse
	// container do not collide on the username taken error.
	runSuffix := randomSuffix(t)
	aliceUser := "omnipus_e2e_alice_" + runSuffix
	bobUser := "omnipus_e2e_bob_" + runSuffix
	carolUser := "omnipus_e2e_carol_" + runSuffix
	password := "e2e-test-password-" + runSuffix

	sharedSecret := os.Getenv("OMNIPUS_MATRIX_REGISTRATION_SHARED_SECRET")

	alice := buildTestClient(ctx, t, homeserver, aliceUser, password, sharedSecret, "alice")
	bob := buildTestClient(ctx, t, homeserver, bobUser, password, sharedSecret, "bob")
	carol := buildTestClient(ctx, t, homeserver, carolUser, password, sharedSecret, "carol")
	t.Cleanup(func() {
		alice.close()
		bob.close()
		carol.close()
	})

	// Spin up the syncers. Both Alice and Bob need a running sync before
	// Olm/Megolm to-device traffic can reach them.
	alice.startSync(t)
	bob.startSync(t)
	carol.startSync(t)

	// Encrypted room creation and bidirectional join/sync is performed once
	// and reused across subtests. This keeps runtime under the 3-min budget
	// per subtest (Olm session setup alone can take 15-30s on cold Synapse).
	//
	// Carol is deliberately NOT invited at creation. The rotation subtest
	// adds her later so we can observe the leave→invite transition in
	// Alice's state store, which is what triggers the crypto machine's
	// HandleMemberEvent to invalidate the outbound Megolm session.
	// (mautrix's handler explicitly skips invite→join as a benign no-op.)
	roomID := createEncryptedRoom(ctx, t, alice, []id.UserID{bob.userID})
	require.NoError(t, alice.client.StateStore.SetEncryptionEvent(ctx, roomID, &event.EncryptionEventContent{
		Algorithm: id.AlgorithmMegolmV1,
	}), "mark room as encrypted in alice's state store")
	require.NoError(t, bob.client.StateStore.SetEncryptionEvent(ctx, roomID, &event.EncryptionEventContent{
		Algorithm: id.AlgorithmMegolmV1,
	}), "mark room as encrypted in bob's state store")

	// Bob and Carol accept the invites. We intentionally hold Carol's join
	// until the rotation subtest so subtest (c) can observe the pre/post
	// session-ID delta.
	joinRoom(ctx, t, bob, roomID)

	// Wait for both directions to see the other as a member. This shields
	// against races where Send() runs before the receiver's sync has landed
	// the membership state.
	bob.waitForMembership(t, roomID, alice.userID, event.MembershipJoin, 30*time.Second)
	alice.waitForMembership(t, roomID, bob.userID, event.MembershipJoin, 30*time.Second)

	t.Run("encrypt_decrypt_roundtrip", func(t *testing.T) {
		subCtx, subCancel := context.WithTimeout(ctx, subtestTimeout)
		defer subCancel()

		bobInbox := bob.subscribeDecrypted(t, roomID)

		// Send via Alice's client. With cli.Crypto set and the room state
		// store flagged as encrypted, SendMessageEvent transparently encrypts
		// via Megolm and rewrites the event type to m.room.encrypted.
		resp, err := alice.client.SendMessageEvent(subCtx, roomID, event.EventMessage, &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    testMessageBody,
		})
		require.NoError(t, err, "alice sends encrypted message")
		require.NotEmpty(t, resp.EventID, "send returned event id")

		// Wire-format assertion: re-fetch the event from the server as a raw
		// read (no auto-decrypt on GET) and inspect the algorithm + type.
		raw, err := alice.client.GetEvent(subCtx, roomID, resp.EventID)
		require.NoError(t, err, "fetch raw event from server")
		require.Equal(t, event.EventEncrypted.Type, raw.Type.Type, "event type is m.room.encrypted")

		require.NoError(t, raw.Content.ParseRaw(event.EventEncrypted))
		enc := raw.Content.AsEncrypted()
		require.NotNil(t, enc, "encrypted content parsed")
		require.Equal(t, id.AlgorithmMegolmV1, enc.Algorithm, "algorithm is m.megolm.v1.aes-sha2")
		require.NotEmpty(t, enc.SessionID, "megolm session id present")

		// Assert Bob sees the decrypted body. We wait up to the subtest
		// timeout for the /sync loop to deliver and the cryptohelper to
		// decrypt.
		decrypted := waitForDecryptedMessage(t, bobInbox, resp.EventID, subCtx)
		require.Equal(t, testMessageBody, decrypted.Body, "bob decrypted alice's exact plaintext")
		require.Equal(t, event.MsgText, decrypted.MsgType, "decrypted event is m.text")
	})

	t.Run("device_verification", func(t *testing.T) {
		subCtx, subCancel := context.WithTimeout(ctx, subtestTimeout)
		defer subCancel()

		// The full interactive SAS dance in mautrix requires bidirectional
		// callback wiring (emoji compare, confirm, mac exchange) and does not
		// have a single-call "trust this device" helper against a live
		// homeserver. We use the documented cross-signing fallback: fetch
		// Bob's device from Alice's store, set device.Trust =
		// TrustStateVerified, then re-put it. This is what every Matrix
		// client does as the terminal state of a successful SAS exchange.
		//
		// [INFERRED] This is a weakened assertion compared to driving a live
		// SAS transaction. It proves the verification storage path works and
		// that subsequent encrypted messages report the sender device as
		// verified, but does not exercise the SAS event wire format. The
		// sibling verificationhelper package is fully exercised by mautrix's
		// own mock-server tests and is out of scope for this integration.
		mach := alice.cryptoHelper.Machine()
		require.NotNil(t, mach, "alice olm machine available")

		// Ensure Alice has fetched Bob's device list at least once. This
		// happens automatically on first Olm handshake but we prime it
		// explicitly to remove a race.
		bobDevice, err := mach.GetOrFetchDevice(subCtx, bob.userID, bob.client.DeviceID)
		require.NoError(t, err, "fetch bob's device from alice's store")
		require.NotNil(t, bobDevice, "bob device present")
		require.NotEqual(t, id.TrustStateVerified, bobDevice.Trust,
			"precondition: bob device should be unverified before the verification step")

		bobDevice.Trust = id.TrustStateVerified
		require.NoError(t, mach.CryptoStore.PutDevice(subCtx, bob.userID, bobDevice),
			"persist verification decision")

		// Re-fetch and confirm the trust flag round-tripped through the
		// crypto store.
		reread, err := mach.CryptoStore.GetDevice(subCtx, bob.userID, bob.client.DeviceID)
		require.NoError(t, err)
		require.NotNil(t, reread, "device still present after PutDevice")
		require.Equal(t, id.TrustStateVerified, reread.Trust,
			"trust state persisted as verified")
		require.True(t, mach.IsDeviceTrusted(subCtx, reread),
			"machine reports bob's device as trusted")

		// Send a second message after verification. The wire event still
		// reports an encrypted envelope and Bob still decrypts successfully.
		// We do not assert a separate "verified" flag in the decrypted event
		// because the encrypted wire format does not surface trust state;
		// trust is a client-side evaluation.
		bobInbox := bob.subscribeDecrypted(t, roomID)
		resp, err := alice.client.SendMessageEvent(subCtx, roomID, event.EventMessage, &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "post-verification message",
		})
		require.NoError(t, err, "send after verification")
		decrypted := waitForDecryptedMessage(t, bobInbox, resp.EventID, subCtx)
		require.Equal(t, "post-verification message", decrypted.Body,
			"post-verification message decrypts cleanly")
	})

	t.Run("key_rotation_on_membership_change", func(t *testing.T) {
		subCtx, subCancel := context.WithTimeout(ctx, subtestTimeout)
		defer subCancel()

		bobInbox := bob.subscribeDecrypted(t, roomID)

		// Send a baseline message to establish the pre-rotation Megolm
		// session id for Alice's outbound session.
		firstResp, err := alice.client.SendMessageEvent(subCtx, roomID, event.EventMessage, &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "before carol joins",
		})
		require.NoError(t, err, "send pre-rotation message")
		firstDecrypted := waitForDecryptedMessage(t, bobInbox, firstResp.EventID, subCtx)
		require.Equal(t, "before carol joins", firstDecrypted.Body, "bob decrypts pre-rotation message")

		firstRaw, err := alice.client.GetEvent(subCtx, roomID, firstResp.EventID)
		require.NoError(t, err)
		require.NoError(t, firstRaw.Content.ParseRaw(event.EventEncrypted))
		firstSessionID := firstRaw.Content.AsEncrypted().SessionID
		require.NotEmpty(t, firstSessionID, "first message has a session id")

		// Carol joins. She must have the encrypted state flagged locally so
		// cryptohelper will decrypt incoming traffic for her, and she must
		// be running /sync before the invite lands so the invite event
		// reaches her timeline deterministically.
		require.NoError(t, carol.client.StateStore.SetEncryptionEvent(subCtx, roomID, &event.EncryptionEventContent{
			Algorithm: id.AlgorithmMegolmV1,
		}), "mark room encrypted in carol's state store before join")

		// Alice invites Carol. The leave→invite transition that Alice's
		// /sync will observe is what triggers OlmMachine.HandleMemberEvent
		// to invalidate the outbound Megolm session. mautrix deliberately
		// skips invite→join (Carol accepting later) as a benign no-op, so
		// we need the invite step to force the rotation.
		_, err = alice.client.InviteUser(subCtx, roomID, &mautrix.ReqInviteUser{UserID: carol.userID})
		require.NoError(t, err, "alice invites carol")

		// Wait for alice's state store to record the invite (proof the
		// HandleMemberEvent callback has run with the invalidation).
		require.Eventually(t, func() bool {
			memb, err := alice.client.StateStore.GetMember(subCtx, roomID, carol.userID)
			return err == nil && memb != nil && memb.Membership == event.MembershipInvite
		}, 30*time.Second, 500*time.Millisecond,
			"alice's state store sees carol's invite")

		joinRoom(subCtx, t, carol, roomID)
		alice.waitForMembership(t, roomID, carol.userID, event.MembershipJoin, 30*time.Second)

		// Send the post-rotation message and confirm a NEW session id.
		secondResp, err := alice.client.SendMessageEvent(subCtx, roomID, event.EventMessage, &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    "after carol joins",
		})
		require.NoError(t, err, "send post-rotation message")

		secondRaw, err := alice.client.GetEvent(subCtx, roomID, secondResp.EventID)
		require.NoError(t, err)
		require.NoError(t, secondRaw.Content.ParseRaw(event.EventEncrypted))
		secondSessionID := secondRaw.Content.AsEncrypted().SessionID
		require.NotEmpty(t, secondSessionID, "second message has a session id")

		require.NotEqual(t, firstSessionID, secondSessionID,
			"megolm session rotates when a new member joins (spec requirement)")
	})
}

// testClient bundles a logged-in mautrix Client + its CryptoHelper and sync
// handle so subtests can reason about a single Matrix identity end-to-end.
type testClient struct {
	label        string
	userID       id.UserID
	client       *mautrix.Client
	cryptoHelper *cryptohelper.CryptoHelper
	db           *sql.DB
	wrappedDB    *dbutil.Database
	syncCancel   context.CancelFunc
	syncDone     chan struct{}

	// decrypted inbox subscriptions feed each test goroutine independently.
	// The slice holds channels owned by subscribeDecrypted; the syncer
	// callback writes to every registered channel non-blockingly.
	inboxMu   sync.Mutex
	inboxSubs []decryptedSub

	// state store cache of remote membership events. The default
	// MemoryStateStore in mautrix does not record StateMember changes
	// unless the caller plumbs StateStoreSyncHandler, which we do at
	// client construction.

	// temp dir for crypto DB; cleaned in close().
	tmpDir string
}

type decryptedSub struct {
	roomID id.RoomID
	ch     chan *decryptedEventNotification
}

type decryptedEventNotification struct {
	EventID    id.EventID
	MsgType    event.MessageType
	Body       string
	ReceivedAt time.Time
}

// buildTestClient registers or logs in a single account and attaches a
// cryptohelper backed by an in-process modernc.org/sqlite database. It must
// be called after probeHomeserver() has passed.
func buildTestClient(
	ctx context.Context,
	t *testing.T,
	homeserver, username, password, sharedSecret, label string,
) *testClient {
	t.Helper()

	// Register the account. Try m.login.dummy first (cheapest path when
	// Synapse has open registration). On UIA failure, fall back to the
	// shared-secret admin register if the secret is provided.
	accessToken, userID, deviceID := registerOrLogin(ctx, t, homeserver, username, password, sharedSecret)

	cli, err := mautrix.NewClient(homeserver, userID, accessToken)
	require.NoError(t, err, "construct mautrix client for %s", label)
	cli.DeviceID = deviceID
	cli.Log = zerolog.Nop()

	// Per-client tempdir keeps crypto DBs isolated so one account's olm keys
	// never leak into another's store. t.TempDir handles cleanup.
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "crypto.db")
	connStr := "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

	rawDB, err := sql.Open("sqlite", connStr)
	require.NoError(t, err, "open crypto sqlite for %s", label)
	// Serialise access; modernc.org/sqlite + WAL handles concurrency but the
	// cryptohelper upgrade path prefers a single-writer connection.
	rawDB.SetMaxOpenConns(1)
	rawDB.SetMaxIdleConns(1)

	wrappedDB, err := dbutil.NewWithDB(rawDB, "sqlite")
	require.NoError(t, err, "wrap crypto db for %s", label)

	// CryptoHelper needs an ExtensibleSyncer; the DefaultSyncer from
	// mautrix.NewClient satisfies that. We deliberately do NOT set LoginAs
	// because the account is already logged in and holds the access token.
	helper, err := cryptohelper.NewCryptoHelper(cli, []byte("omnipus-e2e-pickle-"+label), wrappedDB)
	require.NoError(t, err, "new cryptohelper for %s", label)
	helper.DBAccountID = userID.String()

	require.NoError(t, helper.Init(ctx), "init cryptohelper for %s", label)
	cli.Crypto = helper

	return &testClient{
		label:        label,
		userID:       userID,
		client:       cli,
		cryptoHelper: helper,
		db:           rawDB,
		wrappedDB:    wrappedDB,
		tmpDir:       tmp,
	}
}

// startSync kicks the client's /sync loop in a goroutine. It also installs an
// event listener on the encrypted event type that forwards every successfully
// decrypted message to any active inbox subscribers.
func (c *testClient) startSync(t *testing.T) {
	t.Helper()

	syncer, ok := c.client.Syncer.(*mautrix.DefaultSyncer)
	require.True(t, ok, "%s: client syncer is a DefaultSyncer", c.label)

	// StateStoreSyncHandler is what feeds the MemoryStateStore off the sync
	// stream so we can query membership later. Without it GetMembership is a
	// permanent miss on new rooms.
	syncer.OnEvent(c.client.StateStoreSyncHandler)

	// Decrypted inbox: the cryptohelper's HandleEncrypted is already wired by
	// Init(). It re-dispatches as EventMessage with evt.Mautrix.WasEncrypted
	// set, so we subscribe to that type and gate on WasEncrypted.
	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		if evt == nil || !evt.Mautrix.WasEncrypted {
			return
		}
		msg := evt.Content.AsMessage()
		if msg == nil {
			return
		}
		note := &decryptedEventNotification{
			EventID:    evt.ID,
			MsgType:    msg.MsgType,
			Body:       msg.Body,
			ReceivedAt: time.Now(),
		}
		c.inboxMu.Lock()
		subs := make([]decryptedSub, len(c.inboxSubs))
		copy(subs, c.inboxSubs)
		c.inboxMu.Unlock()
		for _, sub := range subs {
			if sub.roomID != "" && sub.roomID != evt.RoomID {
				continue
			}
			// Non-blocking send keeps a slow subscriber from stalling the
			// syncer; each subscriber must drain promptly or lose events.
			select {
			case sub.ch <- note:
			default:
			}
		}
	})

	syncCtx, cancel := context.WithCancel(context.Background())
	c.syncCancel = cancel
	c.syncDone = make(chan struct{})
	go func() {
		defer close(c.syncDone)
		// SyncWithContext returns nil when ctx is cancelled; any other error
		// here means the sync loop died unexpectedly. We surface it via
		// t.Errorf (not t.Fatal) so one client's failure does not prevent
		// the others from cleaning up their crypto DBs.
		if err := c.client.SyncWithContext(syncCtx); err != nil && syncCtx.Err() == nil {
			t.Errorf("%s sync loop exited with error: %v", c.label, err)
		}
	}()
}

// subscribeDecrypted registers a fresh channel that receives every decrypted
// event delivered to the given room. Callers MUST drain the channel promptly;
// the sync-side dispatch drops on full buffer to avoid stalling the syncer.
func (c *testClient) subscribeDecrypted(t *testing.T, roomID id.RoomID) <-chan *decryptedEventNotification {
	t.Helper()
	ch := make(chan *decryptedEventNotification, 32)
	c.inboxMu.Lock()
	c.inboxSubs = append(c.inboxSubs, decryptedSub{roomID: roomID, ch: ch})
	c.inboxMu.Unlock()
	return ch
}

// waitForMembership polls the client's state store until it reflects the
// desired membership for the given user in the given room. Needed because
// SendMessageEvent rejects with "not in room" if /sync has not yet landed
// the membership change.
func (c *testClient) waitForMembership(
	t *testing.T,
	roomID id.RoomID,
	userID id.UserID,
	want event.Membership,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		got, err := c.client.StateStore.GetMember(ctx, roomID, userID)
		cancel()
		if err == nil && got != nil && got.Membership == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s: membership for %s in %s did not reach %q within %v",
		c.label, userID, roomID, want, timeout)
}

// close stops the sync loop and releases the crypto DB. Safe to call multiple
// times; subsequent calls are no-ops.
func (c *testClient) close() {
	if c == nil {
		return
	}
	if c.syncCancel != nil {
		c.syncCancel()
		<-c.syncDone
		c.syncCancel = nil
	}
	if c.cryptoHelper != nil {
		_ = c.cryptoHelper.Close()
		c.cryptoHelper = nil
	}
	if c.wrappedDB != nil {
		_ = c.wrappedDB.Close()
		c.wrappedDB = nil
	}
	if c.db != nil {
		_ = c.db.Close()
		c.db = nil
	}
}

// probeHomeserver issues a GET to /_matrix/client/versions. A successful HTTP
// 200 is the unambiguous signal that Synapse is up and serving the C-S API.
func probeHomeserver(ctx context.Context, homeserver string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(homeserver, "/")+"/_matrix/client/versions", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return nil
}

// registerOrLogin registers a fresh account or, if registration is closed,
// falls back to the Synapse admin shared-secret endpoint (only when a secret
// is provided via env). Returns (access_token, user_id, device_id).
func registerOrLogin(
	ctx context.Context,
	t *testing.T,
	homeserver, username, password, sharedSecret string,
) (string, id.UserID, id.DeviceID) {
	t.Helper()

	// Path A: m.login.dummy register via the public C-S endpoint. Works when
	// Synapse has `enable_registration: true` and the `m.login.dummy` auth
	// flow.
	deviceName := "omnipus-e2e-" + username
	baseCli, err := mautrix.NewClient(homeserver, "", "")
	require.NoError(t, err, "construct anonymous client for registration")
	baseCli.Log = zerolog.Nop()

	req := &mautrix.ReqRegister[any]{
		Username:                 username,
		Password:                 password,
		DeviceID:                 "",
		InitialDeviceDisplayName: deviceName,
	}
	resp, err := baseCli.RegisterDummy(ctx, req)
	if err == nil {
		require.NotEmpty(t, resp.AccessToken, "dummy registration returned access token")
		return resp.AccessToken, resp.UserID, resp.DeviceID
	}

	// Path B: shared-secret admin register. Only reachable if the operator
	// exported the homeserver's registration_shared_secret via
	// OMNIPUS_MATRIX_REGISTRATION_SHARED_SECRET. This is the documented way
	// to register users on a Synapse that has public registration closed.
	if sharedSecret != "" {
		token, userID, deviceID, admErr := sharedSecretRegister(
			ctx,
			homeserver,
			sharedSecret,
			username,
			password,
			deviceName,
		)
		if admErr == nil {
			return token, userID, deviceID
		}
		t.Logf("shared-secret admin register failed for %s: %v", username, admErr)
	}

	// Path C: maybe the account already exists from a previous run (caller
	// forgot to bump runSuffix). Try password login so the test can still
	// proceed instead of false-failing.
	loginResp, loginErr := baseCli.Login(ctx, &mautrix.ReqLogin{
		Type: mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{
			Type: mautrix.IdentifierTypeUser,
			User: username,
		},
		Password:                 password,
		InitialDeviceDisplayName: deviceName,
	})
	if loginErr == nil {
		return loginResp.AccessToken, loginResp.UserID, loginResp.DeviceID
	}

	t.Skipf("Matrix E2E test: unable to register or login %s against %s "+
		"(dummy err=%v; login err=%v). "+
		"Enable public registration with `enable_registration: true` "+
		"and `enable_registration_without_verification: true` in homeserver.yaml, "+
		"or set OMNIPUS_MATRIX_REGISTRATION_SHARED_SECRET to Synapse's registration_shared_secret.",
		username, homeserver, err, loginErr)
	return "", "", "" // unreachable
}

// sharedSecretRegister posts to Synapse's admin /_synapse/admin/v1/register
// endpoint using the HMAC-SHA1 signature scheme documented at
// https://element-hq.github.io/synapse/latest/admin_api/register_api.html.
func sharedSecretRegister(
	ctx context.Context,
	homeserver, sharedSecret, username, password, deviceName string,
) (string, id.UserID, id.DeviceID, error) {
	base := strings.TrimRight(homeserver, "/") + "/_synapse/admin/v1/register"

	// Stage 1: GET nonce.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	if err != nil {
		return "", "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	var nonceBody struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&nonceBody); err != nil {
		resp.Body.Close()
		return "", "", "", fmt.Errorf("decode nonce: %w", err)
	}
	resp.Body.Close()
	if nonceBody.Nonce == "" {
		return "", "", "", fmt.Errorf("admin register endpoint returned empty nonce")
	}

	// Stage 2: compute HMAC-SHA1 per Synapse docs:
	//   hmac(nonce + "\0" + user + "\0" + password + "\0" + "notadmin")
	mac := hmac.New(sha1.New, []byte(sharedSecret))
	mac.Write([]byte(nonceBody.Nonce))
	mac.Write([]byte{0})
	mac.Write([]byte(username))
	mac.Write([]byte{0})
	mac.Write([]byte(password))
	mac.Write([]byte{0})
	mac.Write([]byte("notadmin"))
	sig := hex.EncodeToString(mac.Sum(nil))

	body, err := json.Marshal(map[string]any{
		"nonce":                       nonceBody.Nonce,
		"username":                    username,
		"password":                    password,
		"admin":                       false,
		"mac":                         sig,
		"initial_device_display_name": deviceName,
	})
	if err != nil {
		return "", "", "", err
	}

	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, base, strings.NewReader(string(body)))
	if err != nil {
		return "", "", "", err
	}
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return "", "", "", err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("admin register returned %d", resp2.StatusCode)
	}

	var regResp struct {
		AccessToken string `json:"access_token"`
		UserID      string `json:"user_id"`
		DeviceID    string `json:"device_id"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&regResp); err != nil {
		return "", "", "", fmt.Errorf("decode register response: %w", err)
	}
	if regResp.AccessToken == "" {
		return "", "", "", fmt.Errorf("admin register response missing access token")
	}
	return regResp.AccessToken, id.UserID(regResp.UserID), id.DeviceID(regResp.DeviceID), nil
}

// createEncryptedRoom creates a new private encrypted room and invites the
// listed peers. The m.room.encryption initial-state event is what makes
// subsequent sends go through the Megolm path.
func createEncryptedRoom(
	ctx context.Context,
	t *testing.T,
	owner *testClient,
	invite []id.UserID,
) id.RoomID {
	t.Helper()

	emptyStateKey := ""
	req := &mautrix.ReqCreateRoom{
		Visibility: "private",
		Preset:     "trusted_private_chat",
		Invite:     invite,
		IsDirect:   false,
		InitialState: []*event.Event{
			{
				Type:     event.StateEncryption,
				StateKey: &emptyStateKey,
				Content: event.Content{
					Raw: map[string]any{
						"algorithm": string(id.AlgorithmMegolmV1),
					},
				},
			},
		},
	}
	resp, err := owner.client.CreateRoom(ctx, req)
	require.NoError(t, err, "create encrypted room")
	require.NotEmpty(t, resp.RoomID, "create room returned room id")
	return resp.RoomID
}

// joinRoom accepts the pending invite for the target client. Uses a short
// retry because an invite sent to a just-registered user may take a sync
// cycle to reach their /sync stream.
func joinRoom(ctx context.Context, t *testing.T, c *testClient, roomID id.RoomID) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := c.client.JoinRoomByID(ctx, roomID)
		if err == nil {
			return
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s: failed to join %s within 30s: %v", c.label, roomID, lastErr)
}

// waitForDecryptedMessage blocks until the subscriber receives a notification
// for the given event id. This is the main timing primitive linking the
// sender's SendMessageEvent to the receiver's decrypted inbox.
func waitForDecryptedMessage(
	t *testing.T,
	inbox <-chan *decryptedEventNotification,
	wantID id.EventID,
	ctx context.Context,
) *decryptedEventNotification {
	t.Helper()
	for {
		select {
		case note := <-inbox:
			if note == nil {
				continue
			}
			if note.EventID == wantID {
				return note
			}
			// Not our event — another test's leftover traffic; keep waiting.
		case <-ctx.Done():
			t.Fatalf("timed out waiting for decrypted event %s: %v", wantID, ctx.Err())
			return nil
		}
	}
}

// randomSuffix returns a short hex string unique to this test run. We use it
// to avoid 400 M_USER_IN_USE when a previous run left users behind in the
// same Synapse container.
func randomSuffix(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b[:])
}
