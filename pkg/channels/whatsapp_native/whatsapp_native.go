//go:build !mipsle && !netbsd && !(freebsd && arm)

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/identity"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// NativeAvailable reports whether the native whatsmeow-based WhatsApp transport
// was compiled into this binary. True in every build except on architectures
// that exclude modernc.org/sqlite (mipsle, netbsd, freebsd/arm), where the
// stub sets it false. REST callers use this to know whether to offer the QR-pairing
// flow or restrict the UI to bridge mode only.
const NativeAvailable = true

const (
	sqliteDriver   = "sqlite"
	whatsappDBName = "store.db"

	reconnectInitial    = 5 * time.Second
	reconnectMax        = 5 * time.Minute
	reconnectMultiplier = 2.0

	// Typing indicator constants (US-3 / US-8)
	waTypingInterval    = 10 * time.Second
	waMaxTypingDuration = 5 * time.Minute
)

// WhatsAppNativeChannel implements the WhatsApp channel using whatsmeow (in-process, no external bridge).
type WhatsAppNativeChannel struct {
	*channels.BaseChannel
	config       config.WhatsAppConfig
	storePath    string
	client       *whatsmeow.Client
	container    *sqlstore.Container
	mu           sync.Mutex
	runCtx       context.Context
	runCancel    context.CancelFunc
	reconnectMu  sync.Mutex
	reconnecting bool
	stopping     atomic.Bool    // set once Stop begins; prevents new wg.Add calls
	wg           sync.WaitGroup // tracks background goroutines (QR handler, reconnect)

	// attempts is the manager-owned pairing-attempt store and instance this
	// channel's id in it (gate r3). attemptID is the pairing attempt the QR
	// loop currently runs, guarded by mu; every event carries it (M8).
	attempts           *channels.PairingAttempts
	instance           string
	attemptID          string
	resourceGeneration uint64
	originConfig       *config.Config // immutable snapshot used by this factory
	attemptObserver    func(attemptID string, status channels.PairingStatus, qr, message string)
	// unfinishedTeardowns pins each captured attempt's known teardown before
	// clearing its active id. Stop/replacement handles join that exact work,
	// never treating an empty active id as completion. This does not establish
	// the separate late-connected client's final disposal (T6). Guarded by mu.
	unfinishedTeardowns map[string]<-chan struct{}

	// pairingObserver, if set, receives linked-device pairing updates (QR code +
	// status) so the gateway can surface them in the SPA (#283). Wired at boot
	// via SetPairingObserver; guarded by mu.
	pairingObserver func(status channels.PairingStatus, qr, message string)
}

// SetPairingObserver installs a callback that receives WhatsApp linked-device
// pairing updates (qr set only when status==channels.PairingStatusCode).
// Satisfies channels.PairingObservable. Safe to call before Start; the QR
// goroutine reads it under mu.
func (c *WhatsAppNativeChannel) SetPairingObserver(fn func(status channels.PairingStatus, qr, message string)) {
	c.mu.Lock()
	c.pairingObserver = fn
	c.mu.Unlock()
}

// SetPairingAttempts hands the channel the manager's attempt store and its
// instance id (channels.PairingAttemptAware).
func (c *WhatsAppNativeChannel) SetPairingAttempts(store *channels.PairingAttempts, instance string) {
	c.mu.Lock()
	c.attempts, c.instance = store, instance
	c.mu.Unlock()
}

// SetPairingAttemptObserver installs the attempt-carrying pairing callback
// (channels.PairingAttemptObservable). It takes precedence over the plain
// observer.
func (c *WhatsAppNativeChannel) SetPairingAttemptObserver(fn func(attemptID string, status channels.PairingStatus, qr, message string)) {
	c.mu.Lock()
	c.attemptObserver = fn
	c.mu.Unlock()
}

// nativeClientHook is a test seam (gate r4 R4-T2): it adjusts a new client
// before any platform call — tests point its transport at a closed local
// port so a start exercises this channel's real pairing lifecycle without the
// WhatsApp servers. Nil in production.
var nativeClientHook func(*whatsmeow.Client)

// restartInterruptedMessage is what a chat card and Connectors show for a
// pairing a gateway restart cut off (founder ruling 2026-10-21).
const restartInterruptedMessage = "A server restart interrupted this pairing. Press Retry to get a new code."

// emitPairing invokes the pairing observer if one is set (best-effort; never
// blocks the QR loop). qr is non-empty only for status=="code". When no observer
// is wired but a QR is ready, warn so an unwired/regressed bridge is visible
// rather than a silently blank SPA panel (#283); the gateway terminal still
// shows the code as a fallback.
func (c *WhatsAppNativeChannel) emitPairing(status channels.PairingStatus, qr, message string) {
	c.mu.Lock()
	attemptID := c.attemptID
	c.mu.Unlock()
	c.emitAttempt(attemptID, status, qr, message)
}

// emitAttempt reports one pairing event of attemptID.
func (c *WhatsAppNativeChannel) emitAttempt(attemptID string, status channels.PairingStatus, qr, message string) {
	c.mu.Lock()
	obs := c.pairingObserver
	attemptObs := c.attemptObserver
	c.mu.Unlock()
	if attemptObs != nil {
		attemptObs(attemptID, status, qr, message)
		return
	}
	if obs == nil {
		if status == channels.PairingStatusCode {
			logger.WarnCF(
				"whatsapp",
				"QR code ready but no pairing observer wired; SPA will not show it (use the gateway terminal)",
				nil,
			)
		}
		return
	}
	obs(status, qr, message)
}

// handleQREvent renders a whatsmeow QR-channel event to the terminal/log and
// forwards it to the SPA via emitPairing (#283). repairing only changes the log
// wording (initial login vs. re-pairing after an unlink). Shared by both QR
// loops so re-pairing surfaces to the SPA identically to first-time login.
func (c *WhatsAppNativeChannel) handleQREvent(attemptID string, evt whatsmeow.QRChannelItem, repairing bool) {
	if evt.Event == "code" {
		scanMsg := "Scan this QR code with WhatsApp (Linked Devices):"
		if repairing {
			scanMsg = "Scan QR code to re-pair WhatsApp (Linked Devices):"
		}
		logger.InfoCF("whatsapp", scanMsg, nil)
		qrterminal.GenerateWithConfig(evt.Code, qrterminal.Config{
			Level:      qrterminal.L,
			Writer:     os.Stdout,
			HalfBlocks: true,
		})
		// Structured log for headless / WebUI consumption (US-2.5).
		logger.InfoCF("whatsapp", "QR code available", map[string]any{
			"event": "whatsapp.qr_code",
			"code":  evt.Code,
		})
		c.emitAttempt(attemptID, channels.PairingStatusCode, evt.Code, "")
		return
	}

	logger.InfoCF("whatsapp", "WhatsApp login event", map[string]any{"event": evt.Event})
	status, message := mapQRLifecycle(evt.Event)
	// A terminal event ends exactly this loop's attempt; when the attempt is
	// no longer the connector's current one (a Stop is tearing it down, or a
	// successor started) the event is not published (gate r3/r4 M8).
	c.mu.Lock()
	store, instance := c.attempts, c.instance
	c.mu.Unlock()
	if store != nil && attemptID != "" {
		if _, applied := store.Complete(instance, attemptID, pairingOutcomeFor(status)); !applied {
			return
		}
		c.clearAttempt(attemptID)
	}
	c.emitAttempt(attemptID, status, "", message)
}

func pairingOutcomeFor(status channels.PairingStatus) channels.PairingOutcome {
	switch status {
	case channels.PairingStatusLinked:
		return channels.PairingOutcomeLinked
	case channels.PairingStatusTimeout:
		return channels.PairingOutcomeTimeout
	default:
		return channels.PairingOutcomeError
	}
}

// beginAttempt makes this start's own admitted pairing attempt active before
// the platform call (gate r3 M2, M6): token is the admitted attempt the start
// consumes. The attempt's teardown handle stops this channel only while that
// attempt is still the one it runs. Without a store (no manager wiring) the
// loop is untracked.
func (c *WhatsAppNativeChannel) beginAttempt(token string) (string, error) {
	return c.activate(func(store *channels.PairingAttempts, instance string, newCancel func(string) func(context.Context) error) (channels.PairingAttempt, error) {
		return store.Activate(instance, token, newCancel)
	})
}

// beginRepair admits and activates the unowned automatic re-pairing of a
// logged-out device (gate r3 M1, gate r4 M2): refused while another attempt
// is reserved or stopping, or while the connector is being deleted.
func (c *WhatsAppNativeChannel) beginRepair() (string, error) {
	return c.activate(func(store *channels.PairingAttempts, instance string, newCancel func(string) func(context.Context) error) (channels.PairingAttempt, error) {
		return store.ActivateRepair(instance, newCancel, channels.PairingRepairOrigin{
			Generation: c.resourceGeneration, Config: c.originConfig,
		})
	})
}

func (c *WhatsAppNativeChannel) activate(
	start func(*channels.PairingAttempts, string, func(string) func(context.Context) error) (channels.PairingAttempt, error),
) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	store, instance := c.attempts, c.instance
	if c.stopping.Load() || c.runCtx != nil && c.runCtx.Err() != nil {
		return "", channels.ErrPairingConflict
	}
	if store == nil {
		return "", nil
	}
	// Holding mu across the activation keeps a Stop that selects the new
	// attempt from observing this channel before its attempt id is recorded.
	rec, err := start(store, instance, func(attemptID string) func(context.Context) error {
		return func(ctx context.Context) error { return c.stopAttempt(ctx, attemptID) }
	})
	if err != nil {
		return "", err
	}
	c.attemptID, c.resourceGeneration = rec.AttemptID, rec.Generation
	return rec.AttemptID, nil
}

// stopAttempt is an attempt's own teardown (its Stop handle): it stops the
// channel in place only while attemptID is still the attempt it runs, and
// reports a teardown that did not finish (gate r3 M6, M7).
func (c *WhatsAppNativeChannel) stopAttempt(ctx context.Context, attemptID string) error {
	c.mu.Lock()
	current := c.attemptID
	unfinished := c.unfinishedTeardowns[attemptID]
	c.mu.Unlock()
	if attemptID != "" && current == attemptID {
		err := c.stop(ctx, false)
		if err == nil {
			c.mu.Lock()
			delete(c.unfinishedTeardowns, attemptID)
			c.mu.Unlock()
		}
		return err
	}
	if unfinished == nil {
		return nil // that attempt's loop is gone and its teardown completed
	}
	// An earlier teardown of this attempt timed out with its work still
	// running: this retry joins that same work (gate r5 R5-A02, M6, M7).
	select {
	case <-unfinished:
		c.mu.Lock()
		delete(c.unfinishedTeardowns, attemptID)
		c.mu.Unlock()
		return nil
	case <-ctx.Done():
		return fmt.Errorf("the WhatsApp client did not finish stopping: %w", ctx.Err())
	}
}

// mapQRLifecycle maps a non-"code" whatsmeow QR event to an SPA pairing status
// and a human-readable, actionable message. Known err-* events get specific
// guidance; unknown events are logged at warn so whatsmeow vocabulary drift is
// visible rather than silently coerced into an opaque raw-enum dump (#283).
func mapQRLifecycle(event string) (status channels.PairingStatus, message string) {
	switch event {
	case "success":
		return channels.PairingStatusLinked, ""
	case "timeout":
		return channels.PairingStatusTimeout, "the QR code expired before it was scanned"
	case "err-scanned-without-multidevice":
		return channels.PairingStatusError, "enable multi-device in WhatsApp, then scan the code again"
	case "err-client-outdated":
		return channels.PairingStatusError, "the gateway's WhatsApp client is outdated and must be updated"
	default:
		logger.WarnCF(
			"whatsapp",
			"unhandled whatsmeow QR event mapped to pairing error",
			map[string]any{"event": event},
		)
		return channels.PairingStatusError, fmt.Sprintf("pairing failed (%s)", event)
	}
}

// NewWhatsAppNativeChannel creates a WhatsApp channel that uses whatsmeow for connection.
// storePath is the directory for the SQLite session store (e.g. workspace/whatsapp).
func NewWhatsAppNativeChannel(
	cfg config.WhatsAppConfig,
	bus *bus.MessageBus,
	storePath string,
) (channels.Channel, error) {
	base := channels.NewBaseChannel("whatsapp_native", cfg, bus, cfg.AllowFrom,
		channels.WithMaxMessageLength(65536),
		channels.WithGroupTrigger(cfg.GroupTrigger),
		channels.WithReasoningChannelID(cfg.ReasoningChannelID),
	)
	if storePath == "" {
		storePath = "whatsapp"
	}
	c := &WhatsAppNativeChannel{
		BaseChannel: base,
		config:      cfg,
		storePath:   storePath,
	}
	return c, nil
}

func (c *WhatsAppNativeChannel) Start(ctx context.Context) error {
	if err := c.pinStart(ctx); err != nil {
		return err
	}
	logger.InfoCF("whatsapp", "Starting WhatsApp native channel (whatsmeow)", map[string]any{"store": c.storePath})

	// Reset lifecycle state from any previous Stop() so a restarted channel
	// behaves correctly.  Use reconnectMu to be consistent with eventHandler
	// and Stop() which coordinate under the same lock.
	c.reconnectMu.Lock()
	c.stopping.Store(false)
	c.reconnecting = false
	c.reconnectMu.Unlock()

	if err := os.MkdirAll(c.storePath, 0o700); err != nil {
		return fmt.Errorf("create session store dir: %w: %w", channels.ErrLocalState, err)
	}

	dbPath := filepath.Join(c.storePath, whatsappDBName)
	connStr := "file:" + dbPath + "?_foreign_keys=on"

	db, err := sql.Open(sqliteDriver, connStr)
	if err != nil {
		return fmt.Errorf("open whatsapp store: %w: %w", channels.ErrLocalState, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err = db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return fmt.Errorf("enable foreign keys: %w: %w", channels.ErrLocalState, err)
	}

	waLogger := waLog.Stdout("WhatsApp", "WARN", true)
	container := sqlstore.NewWithDB(db, sqliteDriver, waLogger)
	if err = container.Upgrade(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("open whatsapp store: %w: %w", channels.ErrLocalState, err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return fmt.Errorf("get device store: %w: %w", channels.ErrLocalState, err)
	}

	client := whatsmeow.NewClient(deviceStore, waLogger)
	if nativeClientHook != nil {
		nativeClientHook(client)
	}

	// Create runCtx/runCancel BEFORE registering event handler and starting
	// goroutines so that Stop() can cancel them at any time, including during
	// the QR-login flow.
	c.runCtx, c.runCancel = context.WithCancel(ctx)

	client.AddEventHandler(c.eventHandler)

	c.mu.Lock()
	c.container = container
	c.client = client
	c.mu.Unlock()

	// cleanupOnError clears struct references and releases resources when
	// Start() fails after fields are already assigned.  This prevents
	// Stop() from operating on stale references (double-close, disconnect
	// of a partially-initialized client, or stray event handler callbacks).
	startOK := false
	defer func() {
		if startOK {
			return
		}
		c.runCancel()
		client.Disconnect()
		c.mu.Lock()
		c.client = nil
		c.container = nil
		c.mu.Unlock()
		_ = container.Close()
	}()

	token, boot := channels.PairingStartFrom(ctx)
	if client.Store.ID == nil {
		started, err := c.startPairing(client, token, boot)
		if err != nil || !started {
			return err
		}
	} else if stamp, _ := channels.PairingStartStampFrom(ctx); stamp.Cancelled {
		return nil // enabled but idle: Stop revoked this dispatch, even if linked
	} else if err := c.connectLinked(client, token); err != nil {
		return err
	}

	startOK = true
	c.SetRunning(true)
	logger.InfoC("whatsapp", "WhatsApp native channel connected")
	return nil
}

// startPairing runs the unpaired branch of Start. A pairing loop starts only
// for this start's own admitted token (gate r4 R4-A01, M2, M14): a boot start
// records the restart interruption (founder ruling 2026-10-21), and any other
// start without a token leaves the connector idle — never a fallback unowned
// pairing. It reports whether the pairing loop runs (the channel is then
// started).
func (c *WhatsAppNativeChannel) startPairing(client *whatsmeow.Client, token string, boot bool) (bool, error) {
	c.mu.Lock()
	store, instance := c.attempts, c.instance
	c.mu.Unlock()
	if store != nil && token == "" {
		if boot {
			// A gateway restart cut off this connector's pairing: nothing is
			// resumed and no ownership is inferred. Record it interrupted and
			// unowned and say so; the chat Retry or Connectors starts a new
			// one. The device is not paired, so there is nothing to connect.
			rec := store.RecordRestartInterrupted(instance)
			// Headless operators read the log, not the pairing view.
			logger.WarnCF("whatsapp", restartInterruptedMessage, map[string]any{"channel_id": instance})
			c.emitAttempt(rec.AttemptID, channels.PairingStatusError, "", restartInterruptedMessage)
			return false, nil
		}
		logger.InfoCF("whatsapp", "WhatsApp is not linked and no pairing was requested for this start; no pairing runs",
			map[string]any{"channel_id": instance})
		return false, nil
	}
	attemptID, err := c.beginAttempt(token)
	if err != nil {
		return false, fmt.Errorf("begin pairing: %w", err)
	}
	qrChan, err := client.GetQRChannel(c.runCtx)
	if err != nil {
		c.failAttempt(attemptID, err)
		return false, fmt.Errorf("get QR channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		c.failAttempt(attemptID, err)
		return false, fmt.Errorf("connect: %w", err)
	}
	// Handle QR events in a background goroutine so Start() returns
	// promptly. The goroutine is tracked via c.wg and respects c.runCtx.
	// Guard wg.Add with reconnectMu + stopping check (same protocol as
	// eventHandler) so a concurrent Stop() cannot enter wg.Wait() while we
	// call wg.Add(1).
	c.reconnectMu.Lock()
	if c.stopping.Load() {
		c.reconnectMu.Unlock()
		stopErr := fmt.Errorf("channel stopped during QR setup")
		c.failAttempt(attemptID, stopErr)
		return false, stopErr
	}
	c.wg.Add(1)
	c.reconnectMu.Unlock()
	// A new pairing starts: "waiting" evicts any terminal frame a previous
	// chat-started pairing left cached (BDD-8.8).
	c.emitAttempt(attemptID, channels.PairingStatusWaiting, "", "")
	go c.runQRLoop(attemptID, qrChan, false)
	return true, nil
}

// connectLinked runs the linked branch of Start: an admitted start needs no
// pairing, so its admission closes and its chat is told the device is already
// linked once the client connected (gate r4 R4-A05, M14).
func (c *WhatsAppNativeChannel) connectLinked(client *whatsmeow.Client, token string) error {
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	c.mu.Lock()
	store, instance := c.attempts, c.instance
	c.mu.Unlock()
	if token != "" && store != nil {
		if rec, closed := store.CloseNoPairing(instance, token); closed {
			c.emitAttempt(rec.AttemptID, channels.PairingStatusLinked, "", channels.PairingAlreadyLinkedMessage)
		}
	}
	return nil
}

// runQRLoop forwards one pairing loop's QR events, each stamped with that
// loop's own attempt id — never the channel's current one, which may already
// belong to a successor (gate r4 M8).
func (c *WhatsAppNativeChannel) runQRLoop(attemptID string, qrChan <-chan whatsmeow.QRChannelItem, repairing bool) {
	defer c.wg.Done()
	for {
		select {
		case <-c.runCtx.Done():
			return
		case evt, ok := <-qrChan:
			if !ok {
				return
			}
			c.handleQREvent(attemptID, evt, repairing)
		}
	}
}

// failAttempt ends attemptID as start_failed and tells its chat the
// value-free cause and repair (gate r4 T2-SF-16). A no-op when the attempt is
// no longer this channel's current one.
func (c *WhatsAppNativeChannel) failAttempt(attemptID string, cause error) {
	c.mu.Lock()
	store, instance := c.attempts, c.instance
	c.mu.Unlock()
	if store == nil || attemptID == "" {
		return
	}
	if _, applied := store.Complete(instance, attemptID, channels.PairingOutcomeStartFailed); !applied {
		return
	}
	c.clearAttempt(attemptID)
	logger.ErrorCF("whatsapp", "WhatsApp pairing could not start", map[string]any{
		"channel_id": instance, "attempt_id": attemptID, "error": cause.Error(),
	})
	c.emitAttempt(attemptID, channels.PairingStatusError, "", channels.PairingStartFailureMessage(cause))
}

// clearAttempt forgets attemptID as the running loop's attempt.
func (c *WhatsAppNativeChannel) clearAttempt(attemptID string) {
	c.mu.Lock()
	if c.attemptID == attemptID {
		c.attemptID = ""
	}
	c.mu.Unlock()
}

func (c *WhatsAppNativeChannel) Stop(ctx context.Context) error {
	return c.stop(ctx, true)
}

// stop stops the channel. interrupt marks a pairing attempt still running as
// interrupted (a disable, reload or shutdown cut it off); an attempt's own
// Stop handle passes false because its Stop publishes the stopped result.
// A teardown whose goroutines did not finish within ctx is reported.
func (c *WhatsAppNativeChannel) stop(ctx context.Context, interrupt bool) error {
	logger.InfoC("whatsapp", "Stopping WhatsApp native channel")
	c.mu.Lock()
	attemptID, store, instance := c.attemptID, c.attempts, c.instance
	resourceDone := make(chan struct{})
	if attemptID != "" {
		// Publish the captured resource's join before clearing its active id.
		// A concurrent retained handle must join this cleanup, not infer that
		// an empty active id means it already finished.
		if c.unfinishedTeardowns == nil {
			c.unfinishedTeardowns = make(map[string]<-chan struct{})
		}
		c.unfinishedTeardowns[attemptID] = resourceDone
	}
	c.attemptID = ""
	c.mu.Unlock()
	var replacement channels.PairingAttempt
	if interrupt && store != nil && attemptID != "" {
		// Capture this resource's existing cleanup barrier. A later config
		// write cannot make it somebody else's teardown or release a Stop.
		if captured, applied := store.BeginResourceTeardown(instance, attemptID); applied {
			replacement = captured
			c.emitAttempt(attemptID, channels.PairingStatusError, "", channels.PairingInterruptedMessage)
		}
	}
	var teardownErr error

	// Mark as stopping under reconnectMu so the flag is visible to
	// eventHandler atomically with respect to its wg.Add(1) call.
	// This closes the TOCTOU window where eventHandler could check
	// stopping (false), then Stop sets it true + enters wg.Wait,
	// then eventHandler calls wg.Add(1) — causing a panic.
	c.reconnectMu.Lock()
	c.stopping.Store(true)
	c.reconnectMu.Unlock()

	if c.runCancel != nil {
		c.runCancel()
	}

	// Disconnect the client first so any blocking Connect()/reconnect loops
	// can be interrupted before we wait on the goroutines.
	c.mu.Lock()
	client := c.client
	container := c.container
	c.mu.Unlock()

	if client != nil {
		client.Disconnect()
	}

	// Wait for background goroutines (QR handler, reconnect) to finish in a
	// context-aware way so Stop can be bounded by ctx.
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines have finished.
	case <-ctx.Done():
		// Context canceled or timed out; log and proceed with best-effort cleanup.
		logger.WarnC("whatsapp", fmt.Sprintf("Stop context canceled before all goroutines finished: %v", ctx.Err()))
		teardownErr = fmt.Errorf("the WhatsApp client did not finish stopping: %w", ctx.Err())
	}

	// Now it is safe to clear and close resources.
	c.mu.Lock()
	c.client = nil
	c.container = nil
	c.mu.Unlock()

	if container != nil {
		_ = container.Close()
	}
	c.SetRunning(false)
	if teardownErr == nil {
		close(resourceDone)
	} else {
		go func() { <-done; close(resourceDone) }()
	}
	if replacement.AttemptID != "" {
		if teardownErr == nil {
			store.FinishReplacement(instance, replacement.AttemptID, replacement.StopGeneration, nil)
		} else {
			// Release only this captured replacement barrier after its known
			// native work joins. Late-client final disposal is the separate T6.
			go func() {
				<-done
				store.FinishReplacement(instance, replacement.AttemptID, replacement.StopGeneration, nil)
			}()
		}
	}
	return teardownErr
}

func (c *WhatsAppNativeChannel) eventHandler(evt any) {
	switch ev := evt.(type) {
	case *events.Message:
		c.handleIncoming(ev)
	case *events.LoggedOut:
		logger.WarnCF("whatsapp", "WhatsApp session logged out by server; re-pairing required", map[string]any{
			"on_connect": ev.OnConnect,
		})
		c.reconnectMu.Lock()
		if c.stopping.Load() || c.reconnecting {
			c.reconnectMu.Unlock()
			return
		}
		c.reconnecting = true
		c.wg.Add(1)
		c.reconnectMu.Unlock()
		go func() {
			defer c.wg.Done()
			defer func() {
				c.reconnectMu.Lock()
				c.reconnecting = false
				c.reconnectMu.Unlock()
			}()
			c.handleLoggedOut()
		}()
	case *events.Disconnected:
		logger.InfoCF("whatsapp", "WhatsApp disconnected, will attempt reconnection", nil)
		c.reconnectMu.Lock()
		if c.reconnecting {
			c.reconnectMu.Unlock()
			return
		}
		// Check stopping while holding the lock so the check and wg.Add
		// are atomic with respect to Stop() setting the flag + calling
		// wg.Wait(). This prevents the TOCTOU race.
		if c.stopping.Load() {
			c.reconnectMu.Unlock()
			return
		}
		c.reconnecting = true
		c.wg.Add(1)
		c.reconnectMu.Unlock()
		go func() {
			defer c.wg.Done()
			c.reconnectWithBackoff()
		}()
	}
}

func (c *WhatsAppNativeChannel) reconnectWithBackoff() {
	defer func() {
		c.reconnectMu.Lock()
		c.reconnecting = false
		c.reconnectMu.Unlock()
	}()

	backoff := reconnectInitial
	for {
		select {
		case <-c.runCtx.Done():
			return
		default:
		}

		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return
		}

		logger.InfoCF("whatsapp", "WhatsApp reconnecting", map[string]any{"backoff": backoff.String()})
		err := client.Connect()
		if err == nil {
			logger.InfoC("whatsapp", "WhatsApp reconnected")
			return
		}

		logger.WarnCF("whatsapp", "WhatsApp reconnect failed", map[string]any{"error": err.Error()})

		select {
		case <-c.runCtx.Done():
			return
		case <-time.After(backoff):
			if backoff < reconnectMax {
				next := time.Duration(float64(backoff) * reconnectMultiplier)
				if next > reconnectMax {
					next = reconnectMax
				}
				backoff = next
			}
		}
	}
}

func (c *WhatsAppNativeChannel) handleIncoming(evt *events.Message) {
	if evt.Message == nil {
		return
	}
	senderID := evt.Info.Sender.String()
	chatID := evt.Info.Chat.String()
	content := evt.Message.GetConversation()
	if content == "" && evt.Message.GetExtendedTextMessage() != nil {
		content = evt.Message.GetExtendedTextMessage().GetText()
	}
	content = utils.SanitizeMessageContent(content)

	if content == "" {
		return
	}

	// For group chats, apply group trigger filtering (US-3 / mention-only / prefix matching)
	if evt.Info.Chat.Server == types.GroupServer {
		isMentioned := c.isBotMentioned(evt)
		respond, cleaned := c.ShouldRespondInGroup(isMentioned, content)
		if !respond {
			return
		}
		content = cleaned
	}

	var mediaPaths []string

	metadata := make(map[string]string)
	metadata["message_id"] = evt.Info.ID
	if evt.Info.PushName != "" {
		metadata["user_name"] = evt.Info.PushName
	}
	if evt.Info.Chat.Server == types.GroupServer {
		metadata["peer_kind"] = "group"
		metadata["peer_id"] = chatID
	} else {
		metadata["peer_kind"] = "direct"
		metadata["peer_id"] = senderID
	}

	peerKind := bus.PeerDirect
	if evt.Info.Chat.Server == types.GroupServer {
		peerKind = bus.PeerGroup
	}
	peer := bus.Peer{Kind: peerKind, ID: chatID}
	messageID := evt.Info.ID
	sender := bus.SenderInfo{
		Platform:    "whatsapp",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("whatsapp", senderID),
		DisplayName: evt.Info.PushName,
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	logger.DebugCF(
		"whatsapp",
		"WhatsApp message received",
		map[string]any{"sender_id": senderID, "content_preview": utils.Truncate(content, 50)},
	)
	if channels.DispatchCancelIfRecognized(
		c.runCtx,
		content,
		c.Name(),
		chatID,
		senderID,
		c.GetCancelInterceptor(),
		channels.CancelSendFn(c),
	) {
		return
	}
	c.HandleMessage(c.runCtx, peer, messageID, senderID, chatID, content, mediaPaths, metadata, sender)
}

func (c *WhatsAppNativeChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	client := c.client
	c.mu.Unlock()

	if client == nil || !client.IsConnected() {
		return fmt.Errorf("whatsapp connection not established: %w", channels.ErrTemporary)
	}

	// Detect unpaired state: the client is connected (to WhatsApp servers)
	// but has not completed QR-login yet, so sending would fail.
	if client.Store.ID == nil {
		return fmt.Errorf("whatsapp not yet paired (QR login pending): %w", channels.ErrTemporary)
	}

	to, err := parseJID(msg.ChatID)
	if err != nil {
		return fmt.Errorf("invalid chat id %q: %w", msg.ChatID, err)
	}

	waMsg := &waE2E.Message{
		Conversation: proto.String(msg.Content),
	}

	if _, err = client.SendMessage(ctx, to, waMsg); err != nil {
		return fmt.Errorf("whatsapp send: %w: %w", err, channels.ErrTemporary)
	}
	return nil
}

// handleLoggedOut clears the current session and starts QR re-pairing (US-2).
// Must be called from a goroutine tracked by c.wg, with c.reconnecting already set.
func (c *WhatsAppNativeChannel) handleLoggedOut() {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil || c.stopping.Load() {
		return
	}

	// Validate this originating client and admit its unowned repair BEFORE
	// destructive session/connection work. It never resolves a new client by id.
	attemptID, err := c.beginRepair()
	if err != nil {
		logger.ErrorCF("whatsapp", "Re-pairing was not admitted; no pairing runs", map[string]any{"error": err.Error()})
		return
	}

	client.Disconnect()

	// Clear session so the next connect triggers QR pairing. Use the channel's
	// runCtx (rather than context.Background) with a bounded timeout so a Stop
	// that fires while re-pairing is in flight cancels the Delete call and
	// avoids blocking shutdown. Store.Delete hits a local SQLite file so a
	// 10-second ceiling is generous; in practice the call completes in
	// milliseconds but a disk stall should not be able to pin shutdown.
	deleteCtx, cancel := context.WithTimeout(c.runCtx, 10*time.Second)
	err = client.Store.Delete(deleteCtx)
	cancel()
	if err != nil {
		c.failAttempt(attemptID, err)
		logger.ErrorCF("whatsapp", "Failed to clear session for re-pairing", map[string]any{"error": err.Error()})
		return
	}

	qrChan, err := client.GetQRChannel(c.runCtx)
	if err != nil {
		c.failAttempt(attemptID, err)
		logger.ErrorCF("whatsapp", "Failed to get QR channel for re-pairing", map[string]any{"error": err.Error()})
		return
	}
	if err := client.Connect(); err != nil {
		c.failAttempt(attemptID, err)
		logger.ErrorCF("whatsapp", "Failed to connect for re-pairing", map[string]any{"error": err.Error()})
		return
	}
	c.emitAttempt(attemptID, channels.PairingStatusWaiting, "", "")
	for {
		select {
		case <-c.runCtx.Done():
			return
		case evt, ok := <-qrChan:
			if !ok {
				return
			}
			c.handleQREvent(attemptID, evt, true)
		}
	}
}

// isBotMentioned returns true when the bot's JID appears in the message's
// mentioned JID list (WhatsApp group @-mentions in extended text messages).
func (c *WhatsAppNativeChannel) isBotMentioned(evt *events.Message) bool {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil || client.Store.ID == nil {
		return false
	}
	botJID := client.Store.ID.ToNonAD().String()

	if evt.Message.GetExtendedTextMessage() != nil {
		ci := evt.Message.GetExtendedTextMessage().GetContextInfo()
		if ci != nil {
			for _, jid := range ci.GetMentionedJID() {
				if jid == botJID {
					return true
				}
			}
		}
	}
	return false
}

// StartTyping implements channels.TypingCapable (US-3 / US-8).
// Sends WhatsApp "composing" presence immediately and re-sends every
// waTypingInterval to keep the indicator alive for long-running processing.
// The returned stop func is idempotent and safe to call multiple times.
func (c *WhatsAppNativeChannel) StartTyping(ctx context.Context, chatID string) (func(), error) {
	to, err := parseJID(chatID)
	if err != nil {
		return func() {}, err
	}

	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return func() {}, nil
	}

	// Send initial composing presence
	if err := client.SendChatPresence(ctx, to, types.ChatPresenceComposing, types.ChatPresenceMediaText); err != nil {
		logger.DebugCF(
			"whatsapp",
			"Failed to send composing presence",
			map[string]any{"chat_id": chatID, "error": err.Error()},
		)
	}

	typingCtx, cancel := context.WithCancel(ctx)
	maxCtx, maxCancel := context.WithTimeout(typingCtx, waMaxTypingDuration)

	go func() {
		defer maxCancel()
		ticker := time.NewTicker(waTypingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-maxCtx.Done():
				// Send paused presence when the indicator stops
				c.mu.Lock()
				cl := c.client
				c.mu.Unlock()
				if cl != nil && cl.IsConnected() {
					if err := cl.SendChatPresence(
						maxCtx,
						to,
						types.ChatPresencePaused,
						types.ChatPresenceMediaText,
					); err != nil {
						logger.DebugCF(
							"whatsapp",
							"Failed to send paused presence",
							map[string]any{"chat_id": chatID, "error": err.Error()},
						)
					}
				}
				return
			case <-ticker.C:
				c.mu.Lock()
				cl := c.client
				c.mu.Unlock()
				if cl != nil && cl.IsConnected() {
					if err := cl.SendChatPresence(
						maxCtx,
						to,
						types.ChatPresenceComposing,
						types.ChatPresenceMediaText,
					); err != nil {
						logger.DebugCF(
							"whatsapp",
							"Failed to re-send composing presence",
							map[string]any{"chat_id": chatID, "error": err.Error()},
						)
					}
				}
			}
		}
	}()

	return cancel, nil
}

// parseJID converts a chat ID (phone number or JID string) to types.JID.
func parseJID(s string) (types.JID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return types.JID{}, fmt.Errorf("empty chat id")
	}
	if strings.Contains(s, "@") {
		return types.ParseJID(s)
	}
	return types.NewJID(s, types.DefaultUserServer), nil
}
