// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package channels

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
)

const (
	defaultChannelQueueSize = 16
	defaultRateLimit        = 10 // default 10 msg/s
	maxRetries              = 3
	rateLimitDelay          = 1 * time.Second
	baseBackoff             = 500 * time.Millisecond
	maxBackoff              = 8 * time.Second

	janitorInterval = 10 * time.Second
	typingStopTTL   = 5 * time.Minute
	placeholderTTL  = 10 * time.Minute

	// dropNoticePrefix tags user-facing drop notifications so that a
	// notification which itself gets dropped (queue still full) is only logged
	// and never re-notified — preventing an amplification loop.
	dropNoticePrefix = "\x00omnipus-drop-notice\x00"
)

// typingEntry wraps a typing stop function with a creation timestamp for TTL eviction.
type typingEntry struct {
	stop      func()
	createdAt time.Time
}

// reactionEntry wraps a reaction undo function with a creation timestamp for TTL eviction.
type reactionEntry struct {
	undo      func()
	createdAt time.Time
}

// placeholderEntry wraps a placeholder ID with a creation timestamp for TTL eviction.
type placeholderEntry struct {
	id        string
	createdAt time.Time
}

// streamEntry marks that a stream finalized a message for a channel:chatID,
// carrying a creation timestamp so the TTL janitor can evict stale markers that
// were never consumed by preSend/preSendMedia (e.g. when the agent errors after
// Finalize and never publishes an outbound message for the key).
type streamEntry struct {
	createdAt time.Time
}

// channelRateConfig maps channel name to per-second rate limit.
var channelRateConfig = map[string]float64{
	"telegram": 20,
	"discord":  1,
	"slack":    1,
	"matrix":   2,
	"line":     10,
	"qq":       5,
	"irc":      2,
}

type channelWorker struct {
	ch         Channel
	queue      chan bus.OutboundMessage
	mediaQueue chan bus.OutboundMediaMessage
	done       chan struct{}
	mediaDone  chan struct{}
	limiter    *rate.Limiter
}

// ChannelInitError records an enabled channel that failed to construct.
// Name is the registry identifier (e.g. "whatsapp_native") as passed to
// recordChannelFailure; Channel is the human-readable display name
// (e.g. "WhatsApp Native"). Both are populated on every failure.
type ChannelInitError struct {
	Name    string // registry id, e.g. "whatsapp_native"
	Channel string // human-readable display name, e.g. "WhatsApp Native"
	Err     error
}

func (e *ChannelInitError) Error() string {
	return fmt.Sprintf("channel %q: %v", e.Channel, e.Err)
}

func (e *ChannelInitError) Unwrap() error { return e.Err }

type Manager struct {
	channels          map[string]Channel
	workers           map[string]*channelWorker
	bus               *bus.MessageBus
	config            *config.Config
	secrets           credentials.SecretBundle // resolved plaintext secrets; never written to env
	mediaStore        media.MediaStore
	dispatchTask      *asyncTask
	dispatchCtx       context.Context // stored so RegisterChannel can spin up workers after Start
	mux               *dynamicServeMux
	httpServer        *http.Server
	mu                sync.RWMutex
	placeholders      sync.Map           // "channel:chatID" → placeholderEntry
	typingStops       sync.Map           // "channel:chatID" → typingEntry
	reactionUndos     sync.Map           // "channel:chatID" → reactionEntry
	streamActive      sync.Map           // "channel:chatID" → streamEntry (set when streamer.Finalize sent the message)
	channelHashes     map[string]string  // channel name → config hash
	streamFallback    bus.StreamDelegate // optional fallback for channels not in m.channels (e.g., webchat WebSocket)
	failedChannels    []ChannelInitError // enabled channels that failed to start
	cancelInterceptor CancelInterceptor  // set after construction via SetCancelInterceptor; may be nil

	// pairingObserver, if set before StartAll, is wired into every channel that
	// implements PairingObservable so linked-device pairing updates (QR/status)
	// reach the SPA (#283). channelID is the channel name (e.g. "whatsapp_native").
	pairingObserver func(channelID string, status PairingStatus, qr, message string)
}

// SetPairingObserver registers a callback to receive linked-device pairing
// updates (QR/status) from all PairingObservable channels and propagates it to
// channels already initialized — mirroring SetCancelInterceptor, since the
// channels map is populated during NewManager (#283).
func (m *Manager) SetPairingObserver(fn func(channelID string, status PairingStatus, qr, message string)) {
	m.mu.Lock()
	m.pairingObserver = fn
	for name, ch := range m.channels {
		if obs, ok := ch.(PairingObservable); ok {
			chName := name
			obs.SetPairingObserver(func(status PairingStatus, qr, message string) {
				fn(chName, status, qr, message)
			})
		}
	}
	m.mu.Unlock()
}

type asyncTask struct {
	cancel context.CancelFunc
	// wg tracks the dispatcher goroutines bound to this task's context so a
	// caller can wait for them to actually EXIT, not merely be signalled.
	//
	// Cancelling is not enough before closing a worker queue: enqueueOutbound
	// selects on `w.queue <- msg` and `<-ctx.Done()`, and a select whose send
	// case is already runnable can take it even though the context is done —
	// so a cancel-then-close still races, and a send on a closed channel
	// PANICS. The race detector caught exactly this pair once pkg/gateway
	// became race-buildable:
	//
	//	Write at … runtime.closechan() ← Manager.StopAll        manager.go:986
	//	Read  at … runtime.chansend()  ← Manager.enqueueOutbound manager.go:1328
	//
	// Never Wait() while holding m.mu: dispatchLoop takes m.mu.RLock to
	// resolve a worker, so a dispatcher mid-loop would deadlock against the
	// write lock. StopAll drops the lock for the wait deliberately.
	wg sync.WaitGroup
}

// ChannelRegistered reports whether a channel with the given name is currently
// registered (constructed and tracked) on this manager. Used by the scheduled
// runner to validate a deliver=true target before publishing, so a publish to a
// channel nobody is subscribed to is recorded as a failure instead of a silent
// success (M2).
func (m *Manager) ChannelRegistered(name string) bool {
	if name == "" {
		return false
	}
	m.mu.RLock()
	_, ok := m.channels[name]
	m.mu.RUnlock()
	return ok
}

// RecordPlaceholder registers a placeholder message for later editing.
// Implements PlaceholderRecorder.
func (m *Manager) RecordPlaceholder(channel, chatID, placeholderID string) {
	key := channel + ":" + chatID
	m.placeholders.Store(key, placeholderEntry{id: placeholderID, createdAt: time.Now()})
}

// SendPlaceholder sends a "Thinking…" placeholder for the given channel/chatID
// and records it for later editing. Returns true if a placeholder was sent.
func (m *Manager) SendPlaceholder(ctx context.Context, channel, chatID string) bool {
	m.mu.RLock()
	ch, ok := m.channels[channel]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	pc, ok := ch.(PlaceholderCapable)
	if !ok {
		return false
	}
	phID, err := pc.SendPlaceholder(ctx, chatID)
	if err != nil {
		logger.DebugCF("channels", "SendPlaceholder failed", map[string]any{
			"channel": channel, "chat_id": chatID, "error": err.Error(),
		})
		return false
	}
	if phID == "" {
		return false
	}
	m.RecordPlaceholder(channel, chatID, phID)
	return true
}

// RecordTypingStop registers a typing stop function for later invocation.
// Implements PlaceholderRecorder.
func (m *Manager) RecordTypingStop(channel, chatID string, stop func()) {
	key := channel + ":" + chatID
	entry := typingEntry{stop: stop, createdAt: time.Now()}
	if previous, loaded := m.typingStops.Swap(key, entry); loaded {
		if oldEntry, ok := previous.(typingEntry); ok && oldEntry.stop != nil {
			oldEntry.stop()
		}
	}
}

// InvokeTypingStop invokes the registered typing stop function for the given channel and chatID.
// It is safe to call even when no typing indicator is active (no-op).
// Used by the agent loop to stop typing when processing completes (success, error, or panic),
// regardless of whether an outbound message is published.
func (m *Manager) InvokeTypingStop(channel, chatID string) {
	key := channel + ":" + chatID
	if v, loaded := m.typingStops.LoadAndDelete(key); loaded {
		if entry, ok := v.(typingEntry); ok {
			entry.stop()
		}
	}
}

// RecordReactionUndo registers a reaction undo function for later invocation.
// Implements PlaceholderRecorder.
func (m *Manager) RecordReactionUndo(channel, chatID string, undo func()) {
	key := channel + ":" + chatID
	m.reactionUndos.Store(key, reactionEntry{undo: undo, createdAt: time.Now()})
}

// preSend handles typing stop, reaction undo, and placeholder editing before sending a message.
// Returns true if the message was already delivered (skip Send).
func (m *Manager) preSend(ctx context.Context, name string, msg bus.OutboundMessage, ch Channel) bool {
	key := name + ":" + msg.ChatID

	// 1. Stop typing
	if v, loaded := m.typingStops.LoadAndDelete(key); loaded {
		if entry, ok := v.(typingEntry); ok {
			entry.stop() // idempotent, safe
		}
	}

	// 2. Undo reaction
	if v, loaded := m.reactionUndos.LoadAndDelete(key); loaded {
		if entry, ok := v.(reactionEntry); ok {
			entry.undo() // idempotent, safe
		}
	}

	// 3. If a stream already finalized this message, delete the placeholder and skip send
	if _, loaded := m.streamActive.LoadAndDelete(key); loaded {
		if v, loaded := m.placeholders.LoadAndDelete(key); loaded {
			if entry, ok := v.(placeholderEntry); ok && entry.id != "" {
				// Prefer deleting the placeholder (cleaner UX than editing to same content)
				if deleter, ok := ch.(MessageDeleter); ok {
					deleter.DeleteMessage(ctx, msg.ChatID, entry.id) // best effort
				} else if editor, ok := ch.(MessageEditor); ok {
					editor.EditMessage(ctx, msg.ChatID, entry.id, msg.Content) // fallback
				}
			}
		}
		return true
	}

	// 4. Try editing placeholder
	if v, loaded := m.placeholders.LoadAndDelete(key); loaded {
		if entry, ok := v.(placeholderEntry); ok && entry.id != "" {
			if editor, ok := ch.(MessageEditor); ok {
				if err := editor.EditMessage(ctx, msg.ChatID, entry.id, msg.Content); err == nil {
					return true // edited successfully, skip Send
				} else {
					logger.WarnCF(
						"channels",
						"Placeholder edit failed, falling through to Send",
						map[string]any{
							"channel":        name,
							"chat_id":        msg.ChatID,
							"placeholder_id": entry.id,
							"error":          err.Error(),
						},
					)
				}
				// edit failed → fall through to normal Send
			}
		}
	}

	return false
}

// preSendMedia handles typing stop, reaction undo, and placeholder cleanup
// before sending media attachments. Unlike preSend for text messages, media
// delivery never edits the placeholder because there is no text payload to
// replace it with; it only attempts to delete the placeholder when possible.
func (m *Manager) preSendMedia(ctx context.Context, name string, msg bus.OutboundMediaMessage, ch Channel) {
	key := name + ":" + msg.ChatID

	// 1. Stop typing
	if v, loaded := m.typingStops.LoadAndDelete(key); loaded {
		if entry, ok := v.(typingEntry); ok {
			entry.stop() // idempotent, safe
		}
	}

	// 2. Undo reaction
	if v, loaded := m.reactionUndos.LoadAndDelete(key); loaded {
		if entry, ok := v.(reactionEntry); ok {
			entry.undo() // idempotent, safe
		}
	}

	// 3. Clear any finalized stream marker for this chat before media delivery.
	m.streamActive.LoadAndDelete(key)

	// 4. Delete placeholder if present.
	if v, loaded := m.placeholders.LoadAndDelete(key); loaded {
		if entry, ok := v.(placeholderEntry); ok && entry.id != "" {
			if deleter, ok := ch.(MessageDeleter); ok {
				deleter.DeleteMessage(ctx, msg.ChatID, entry.id) // best effort
			}
		}
	}
}

func NewManager(
	cfg *config.Config,
	secrets credentials.SecretBundle,
	messageBus *bus.MessageBus,
	store media.MediaStore,
) (*Manager, error) {
	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		bus:           messageBus,
		config:        cfg,
		secrets:       secrets,
		mediaStore:    store,
		channelHashes: make(map[string]string),
	}

	// Register as streaming delegate so the agent loop can obtain streamers
	messageBus.SetStreamDelegate(m)

	if err := m.initChannels(cfg.Channels); err != nil {
		return nil, err
	}

	// Store initial config hashes for all channels
	m.channelHashes = toChannelHashes(cfg)

	return m, nil
}

// NewManagerForTesting creates a minimal Manager with pre-seeded ChannelInitError
// entries accessible via FailedChannels(). No real channels are constructed and
// no MessageBus or MediaStore is required.
//
// This constructor exists so integration tests in external packages (e.g.
// pkg/gateway) can inject degraded state into a Manager without needing a live
// credential store, network connectivity, or a real channel factory.  Production
// code should use NewManager; callers of this function must only appear in
// _test.go files.
func NewManagerForTesting(failed []ChannelInitError) *Manager {
	m := &Manager{
		channels:      make(map[string]Channel),
		workers:       make(map[string]*channelWorker),
		channelHashes: make(map[string]string),
	}
	m.failedChannels = append(m.failedChannels, failed...)
	return m
}

// SetStreamFallback registers a fallback StreamDelegate for channels not managed
// by the Manager (e.g., the webchat WebSocket handler).
func (m *Manager) SetStreamFallback(d bus.StreamDelegate) {
	m.streamFallback = d
}

// SetCancelInterceptor registers the CancelInterceptor (typically the agent
// loop) so that Tier B text-parsing channels can fire /cancel. Must be called
// after the Manager is constructed and before channels receive messages.
// It is safe to call multiple times (last write wins).
func (m *Manager) SetCancelInterceptor(ci CancelInterceptor) {
	m.mu.Lock()
	m.cancelInterceptor = ci
	// Propagate to all channels already initialized.
	for _, ch := range m.channels {
		if setter, ok := ch.(interface{ SetCancelInterceptor(ci CancelInterceptor) }); ok {
			setter.SetCancelInterceptor(ci)
		}
	}
	m.mu.Unlock()
}

// GetStreamer implements bus.StreamDelegate.
// It checks if the named channel supports streaming and returns a Streamer.
// Falls back to streamFallback for channels not in the Manager (e.g., webchat).
func (m *Manager) GetStreamer(ctx context.Context, channelName, chatID, sessionID string) (bus.Streamer, bool) {
	m.mu.RLock()
	ch, exists := m.channels[channelName]
	m.mu.RUnlock()

	if !exists {
		if m.streamFallback != nil {
			return m.streamFallback.GetStreamer(ctx, channelName, chatID, sessionID)
		}
		return nil, false
	}

	sc, ok := ch.(StreamingCapable)
	if !ok {
		// Channel exists but doesn't implement StreamingCapable —
		// try the fallback (e.g., webchat channel delegates streaming to WSHandler)
		if m.streamFallback != nil {
			return m.streamFallback.GetStreamer(ctx, channelName, chatID, sessionID)
		}
		return nil, false
	}

	streamer, err := sc.BeginStream(ctx, chatID)
	if err != nil {
		logger.DebugCF("channels", "Streaming unavailable, falling back to placeholder", map[string]any{
			"channel": channelName,
			"error":   err.Error(),
		})
		return nil, false
	}

	// Mark streamActive on Finalize so preSend knows to clean up the placeholder
	key := channelName + ":" + chatID
	return &finalizeHookStreamer{
		Streamer:   streamer,
		onFinalize: func() { m.streamActive.Store(key, streamEntry{createdAt: time.Now()}) },
	}, true
}

// finalizeHookStreamer wraps a Streamer to run a hook on Finalize.
type finalizeHookStreamer struct {
	Streamer
	onFinalize func()
}

func (s *finalizeHookStreamer) Finalize(ctx context.Context, content string) error {
	if err := s.Streamer.Finalize(ctx, content); err != nil {
		return err
	}
	s.onFinalize()
	return nil
}

// initChannel is a helper that looks up a factory by name and creates the
// channel. instanceID is the config.Channels map key for this instance (e.g.
// "whatsapp" for a bare-type key or "whatsapp.eu" for a namespaced instance).
// It is passed through to the factory so the factory can select the right
// config entry (ADR-029 Gate 0). It returns an error if the factory is not
// registered or construction fails.
func (m *Manager) initChannel(name, instanceID, displayName string) error {
	f, ok := getFactory(name)
	if !ok {
		return fmt.Errorf("factory not registered for channel %q", displayName)
	}
	logger.DebugCF("channels", "Attempting to initialize channel", map[string]any{
		"channel":    displayName,
		"instanceID": instanceID,
	})
	ch, err := f(m.config, instanceID, m.secrets, m.bus)
	if err != nil {
		logger.ErrorCF("channels", "Failed to initialize channel", map[string]any{
			"channel": displayName,
			"error":   err.Error(),
		})
		return err
	}
	// Inject MediaStore if channel supports it
	if m.mediaStore != nil {
		if setter, ok := ch.(interface{ SetMediaStore(s media.MediaStore) }); ok {
			setter.SetMediaStore(m.mediaStore)
		}
	}
	// Inject PlaceholderRecorder if channel supports it
	if setter, ok := ch.(interface{ SetPlaceholderRecorder(r PlaceholderRecorder) }); ok {
		setter.SetPlaceholderRecorder(m)
	}
	// Inject owner reference so BaseChannel.HandleMessage can auto-trigger typing/reaction
	if setter, ok := ch.(interface{ SetOwner(ch Channel) }); ok {
		setter.SetOwner(ch)
	}
	// Inject CancelInterceptor if one has been registered (may be nil at init time;
	// SetCancelInterceptor propagates to all channels when called later).
	if m.cancelInterceptor != nil {
		if setter, ok := ch.(interface{ SetCancelInterceptor(ci CancelInterceptor) }); ok {
			setter.SetCancelInterceptor(m.cancelInterceptor)
		}
	}
	// Inject pairing observer (#283) so QR/status updates reach the SPA. Capture
	// the instanceID so the observer tags which instance emitted the event.
	if m.pairingObserver != nil {
		if obs, ok := ch.(PairingObservable); ok {
			instID := instanceID
			obs.SetPairingObserver(func(status PairingStatus, qr, message string) {
				m.pairingObserver(instID, status, qr, message)
			})
		}
	}
	// Stamp the trusted instance key onto the channel so every inbound message
	// carries the correct InstanceID (FR-020 / FR-021). The key is always the
	// config-map instanceID, never derived from message content.
	if setter, ok := ch.(interface{ SetInstanceID(id string) }); ok {
		setter.SetInstanceID(instanceID)
	}
	// Key m.channels by instanceID (FR-015 / WS-B): supports N-per-type
	// because "whatsapp.eu" and "whatsapp.us" occupy distinct slots. For
	// legacy single-instance the instanceID equals the type name, so the
	// behavior is unchanged (e.g. "telegram" → m.channels["telegram"]).
	m.channels[instanceID] = ch
	logger.InfoCF("channels", "Channel enabled successfully", map[string]any{
		"channel":    displayName,
		"instanceID": instanceID,
	})
	return nil
}

// notifyDrop surfaces user-facing feedback when an outbound message is dropped
// due to backpressure (queue full). It publishes a short notice back through the
// bus, tagged with dropNoticePrefix so that a notice which itself gets dropped is
// only logged (see the dispatch enqueue callbacks) — preventing amplification.
// The prefix is stripped by runWorker before the notice is delivered, so the user
// never sees the sentinel.
//
// The publish MUST be non-blocking. notifyDrop runs synchronously inside the
// dispatch loop, which is the SOLE consumer of the outbound channel. The drop
// condition is "outbound buffer full", so a blocking publish here would wait for
// a slot that only this same loop can drain — a self-deadlock. TryPublishOutbound
// drops the notice (logged) rather than block when the buffer is still full.
func (m *Manager) notifyDrop(channel, chatID, text string) {
	if channel == "" || chatID == "" || m.bus == nil {
		return
	}
	notice := bus.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: dropNoticePrefix + text,
	}
	if !m.bus.TryPublishOutbound(notice) {
		logger.WarnCF("channels", "Drop notice dropped (bus full or closed)", map[string]any{
			"channel": channel,
			"chat_id": chatID,
		})
	}
}

// warnMisconfigured logs a WARN when a channel is enabled in config but is
// missing a required field, so it is silently skipped by the init if-ladder.
// Without this an operator who enables a channel but forgets to set its token /
// URL gets no feedback about why the channel never started.
func warnMisconfigured(displayName, missing string) {
	logger.WarnCF("channels", "Enabled channel skipped — required configuration missing", map[string]any{
		"channel":        displayName,
		"missing_fields": missing,
	})
}

// recordChannelFailure records a failed enabled-channel init and logs it.
func (m *Manager) recordChannelFailure(name, displayName string, err error) {
	m.failedChannels = append(m.failedChannels, ChannelInitError{Name: name, Channel: displayName, Err: err})
	logger.ErrorCF("channels", "Enabled channel failed to initialize", map[string]any{
		"channel": displayName,
		"error":   err.Error(),
	})
}

// FailedChannels returns a copy of the list of enabled channels that failed to
// start. A copy is returned so the caller cannot mutate internal state and so
// that a concurrent Reload write does not race with the caller's iteration.
func (m *Manager) FailedChannels() []ChannelInitError {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ChannelInitError, len(m.failedChannels))
	copy(out, m.failedChannels)
	return out
}

// channelTypeToFactory maps a channel instance type to the factory name
// registered via channels.RegisterFactory. For most channel types the factory
// name equals the type; WhatsApp is the one exception (type="whatsapp",
// factory="whatsapp_native" — the legacy bridge was removed and all builds use
// whatsmeow).
var channelTypeToFactory = map[string]string{
	"whatsapp": "whatsapp_native",
}

// factoryNameForType returns the registered factory name for a channel type.
func factoryNameForType(channelType string) string {
	if name, ok := channelTypeToFactory[channelType]; ok {
		return name
	}
	return channelType
}

// initChannels activates all enabled instances in the channels map. The
// if-ladder over typed ChannelsConfig fields is replaced by a loop over the
// instance map + a type→factory map (Spec-2 FR-2.4).
func (m *Manager) initChannels(channels map[string]config.ChannelInstanceConfig) error {
	logger.InfoC("channels", "Initializing channel manager")

	for instanceID, inst := range channels {
		if !inst.Enabled {
			continue
		}
		// Normalize: if Type is unset, treat the map key as the type. This mirrors
		// what normalizeChannelMap does at config-load time, but initChannels is also
		// called with a synthesized subset map (toChannelConfig) whose entries may
		// have been created from a zero-value ChannelInstanceConfig (e.g. a map-key
		// lookup on an empty DefaultConfig().Channels, which skips normalizeChannelMap).
		// Without this guard, inst.Type == "" falls through every check and produces
		// a "factory not registered for channel """ error that aborts the reload.
		if inst.Type == "" {
			inst.Type = instanceID
		}
		factoryName := factoryNameForType(inst.Type)
		displayName := inst.Type // factories use the type as display name

		if inst.Type == "whatsapp" {
			// WhatsApp is always native (whatsmeow). The legacy bridge channel was
			// removed; on a lite/stub build (or an architecture where
			// modernc.org/sqlite is unavailable) whatsapp_native fails to construct
			// and we record a clear failure rather than silently no-op.
			// initChannel keys m.channels by instanceID (FR-015 / WS-B resolved);
			// for multi-instance "whatsapp.eu" and "whatsapp.us" get distinct slots.
			if err := m.initChannel(factoryName, instanceID, "WhatsApp Native"); err != nil {
				wrapped := fmt.Errorf(
					"WhatsApp requires the native build (bridge removed); "+
						"unavailable on this build variant: %w",
					err,
				)
				// Record under instanceID (not factoryName) so degraded status
				// refers to the operator-visible config key.
				m.recordChannelFailure(instanceID, "WhatsApp Native", wrapped)
			}
			continue
		}

		// Per-type prerequisite checks (misconfiguration guard). Data-driven:
		// each channel type registers a PrerequisiteChecker (see
		// prerequisites.go / channel subpackage init). A type with no
		// registered checker activates without gating.
		if checker, ok := getPrerequisite(inst.Type); ok {
			missing, ok := checker(inst)
			if !ok {
				warnMisconfigured(inst.Type, missing)
				continue
			}
		}

		// initChannel keys m.channels by instanceID (FR-015 / WS-B resolved).
		if err := m.initChannel(factoryName, instanceID, displayName); err != nil {
			m.recordChannelFailure(instanceID, displayName, err)
		}
	}

	logger.InfoCF("channels", "Channel initialization completed", map[string]any{
		"enabled_channels": len(m.channels),
		"failed_channels":  len(m.failedChannels),
	})

	// If any enabled channel failed to construct, abort boot per deny-by-default
	// policy — with the whatsapp_native exception (see bootFatalError).
	return bootFatalError(m.failedChannels)
}

// isNonFatalChannelName reports whether a channel failure recorded under name
// should NOT abort gateway boot. WhatsApp (any instance: bare "whatsapp",
// namespaced "whatsapp.eu", etc.) and the legacy factory name "whatsapp_native"
// are non-fatal: on a lite/stub build or an arch where modernc.org/sqlite is
// unavailable WhatsApp cannot construct, and the legacy bridge fallback was
// removed. Aborting the whole gateway over it would brick every other channel
// for an operator who left channels.whatsapp.enabled=true. The failure is still
// recorded and surfaced as degraded via FailedChannels, so status shows a clear
// "requires the native build" message.
//
// Matrix (any instance: bare "matrix", namespaced "matrix.eu", etc.) is the
// same story on a build without the goolm tag: matrix.go and its real init.go
// are excluded (see pkg/channels/matrix), and pkg/channels/matrix/init_stub.go
// registers a "matrix" factory that always errors with a clear "requires a
// goolm-tagged build" message. That failure must degrade the gateway, not
// abort boot, for an operator who left channels.matrix.enabled=true on a
// non-goolm/lite build.
//
// Failures are now recorded under instanceID (e.g. "whatsapp", "whatsapp.eu",
// "matrix", "matrix.eu"), so we match on the "whatsapp"/"matrix" prefix or the
// legacy factory name.
func isNonFatalChannelName(name string) bool {
	return name == "whatsapp_native" ||
		name == "whatsapp" ||
		strings.HasPrefix(name, "whatsapp.") ||
		name == "matrix" ||
		strings.HasPrefix(name, "matrix.")
}

// bootFatalError returns a joined error for the subset of failed channels whose
// failure is boot-fatal, or nil if every failure is non-fatal (or there were
// none). Non-fatal failures remain recorded in FailedChannels for degraded
// status; they simply do not abort boot.
func bootFatalError(failed []ChannelInitError) error {
	joinedErrs := make([]error, 0, len(failed))
	for i := range failed {
		if isNonFatalChannelName(failed[i].Name) {
			continue
		}
		joinedErrs = append(joinedErrs, &failed[i])
	}
	if len(joinedErrs) > 0 {
		return errors.Join(joinedErrs...)
	}
	return nil
}

// SetupHTTPServer creates a shared HTTP server with the given listen address.
// It registers health endpoints from the health server and discovers channels
// that implement WebhookHandler and/or HealthChecker to register their handlers.
func (m *Manager) SetupHTTPServer(addr string, healthServer *health.Server) {
	m.mux = newDynamicServeMux()

	// Register health endpoints
	if healthServer != nil {
		healthServer.RegisterOnMux(m.mux)
		// RegisterOnMux only attaches handlers; it does NOT mark the server
		// as ready the way Start()/StartContext() do. Without this explicit
		// SetReady(true), /ready returns 503 forever in the embedded-mux
		// path, which breaks k8s readiness probes and load balancer health
		// checks.
		healthServer.SetReady(true)
	}

	// Discover and register webhook handlers and health checkers
	m.registerHTTPHandlersLocked()

	m.httpServer = &http.Server{
		Addr:         addr,
		Handler:      m.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
}

// WrapHTTPHandler wraps the HTTP server's root handler with the given middleware.
// Must be called after SetupHTTPServer and before StartAll. This allows callers
// to inject cross-cutting concerns (e.g., config snapshot middleware) without
// modifying individual handler registrations.
func (m *Manager) WrapHTTPHandler(middleware func(http.Handler) http.Handler) error {
	if m.httpServer == nil || m.httpServer.Handler == nil {
		return fmt.Errorf("WrapHTTPHandler: httpServer not initialized")
	}
	m.httpServer.Handler = middleware(m.httpServer.Handler)
	return nil
}

// registerHTTPHandlersLocked registers webhook and health-check handlers for
// all channels currently in m.channels. Caller must hold m.mu (or ensure
// exclusive access).
func (m *Manager) registerHTTPHandlersLocked() {
	for name, ch := range m.channels {
		m.registerChannelHTTPHandler(name, ch)
	}
}

// registerChannelHTTPHandler registers the webhook/health handlers for a
// single channel onto m.mux.
func (m *Manager) registerChannelHTTPHandler(name string, ch Channel) {
	if wh, ok := ch.(WebhookHandler); ok {
		m.mux.Handle(wh.WebhookPath(), wh)
		logger.InfoCF("channels", "Webhook handler registered", map[string]any{
			"channel": name,
			"path":    wh.WebhookPath(),
		})
	}
	if hc, ok := ch.(HealthChecker); ok {
		m.mux.HandleFunc(hc.HealthPath(), hc.HealthHandler)
		logger.InfoCF("channels", "Health endpoint registered", map[string]any{
			"channel": name,
			"path":    hc.HealthPath(),
		})
	}
}

// RegisterHTTPHandler registers an arbitrary HTTP handler at the given path pattern
// on the shared mux. Must be called after SetupHTTPServer.
func (m *Manager) RegisterHTTPHandler(pattern string, handler http.Handler) {
	if m.mux != nil {
		m.mux.Handle(pattern, handler)
	}
}

// unregisterChannelHTTPHandler removes the webhook/health handlers for a
// single channel from m.mux.
func (m *Manager) unregisterChannelHTTPHandler(name string, ch Channel) {
	if wh, ok := ch.(WebhookHandler); ok {
		m.mux.Unhandle(wh.WebhookPath())
		logger.InfoCF("channels", "Webhook handler unregistered", map[string]any{
			"channel": name,
			"path":    wh.WebhookPath(),
		})
	}
	if hc, ok := ch.(HealthChecker); ok {
		m.mux.Unhandle(hc.HealthPath())
		logger.InfoCF("channels", "Health endpoint unregistered", map[string]any{
			"channel": name,
			"path":    hc.HealthPath(),
		})
	}
}

// newDispatchContext derives a fresh cancelable context from ctx and wires it
// into m.dispatchTask and m.dispatchCtx. Both StartAll and Reload use it so that
// the dispatch context (which RegisterChannel and post-start workers bind to) is
// always refreshed in lockstep with m.dispatchTask — preventing the class of bug
// where a reload canceled the old context but forgot to refresh m.dispatchCtx,
// leaving newly-added channels bound to a dead context. Caller must hold m.mu.
// Returns the new context and its cancel func (the caller is responsible for
// calling cancel on an early-abort path, e.g. all channels failed to start).
func (m *Manager) newDispatchContext(ctx context.Context) (context.Context, context.CancelFunc) {
	dispatchCtx, cancel := context.WithCancel(ctx)
	m.dispatchTask = &asyncTask{cancel: cancel}
	m.dispatchCtx = dispatchCtx
	return dispatchCtx, cancel
}

// startDispatchers launches the long-lived dispatch goroutines (text + media
// outbound dispatchers and the TTL janitor) bound to dispatchCtx. Both StartAll
// and Reload call it so the janitor is never forgotten on a reload (its omission
// previously leaked typing/placeholder/stream entries after a reload). Caller
// must hold m.mu.
func (m *Manager) startDispatchers(dispatchCtx context.Context) {
	// Track the goroutines on the task that owns dispatchCtx so StopAll can
	// wait for them to exit before closing any worker queue (see asyncTask.wg).
	// A nil task means no cancellable owner was installed — run untracked
	// rather than panicking, matching the pre-existing nil-tolerant style.
	task := m.dispatchTask
	if task != nil {
		task.wg.Add(3)
	}
	run := func(fn func(context.Context)) {
		go func() {
			if task != nil {
				defer task.wg.Done()
			}
			fn(dispatchCtx)
		}()
	}
	run(m.dispatchOutbound)
	run(m.dispatchOutboundMedia)
	run(m.runTTLJanitor)
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.channels) == 0 {
		logger.WarnC("channels", "No channels enabled")
	}

	logger.InfoC("channels", "Starting all channels")

	dispatchCtx, cancel := m.newDispatchContext(ctx)

	startedCount := 0
	configuredCount := len(m.channels)
	for name, channel := range m.channels {
		logger.InfoCF("channels", "Starting channel", map[string]any{
			"channel": name,
		})
		if err := channel.Start(ctx); err != nil {
			logger.ErrorCF("channels", "Failed to start channel", map[string]any{
				"channel": name,
				"error":   err.Error(),
			})
			continue
		}
		// Lazily create worker only after channel starts successfully. Rate
		// limiting is keyed by channel TYPE, not instance ID (name here is the
		// instanceID under N-per-type instancing) — resolve the type so a second
		// instance of a rate-limited type doesn't silently fall back to
		// defaultRateLimit.
		w := newChannelWorker(m.channelTypeForRateLimit(name), channel)
		m.workers[name] = w
		// Snapshot m.config while still holding m.mu.Lock() (see runWorker's doc
		// comment for why runWorker must never read m.config itself).
		go m.runWorker(dispatchCtx, name, w, m.config)
		go m.runMediaWorker(dispatchCtx, name, w)
		startedCount++
	}

	if configuredCount > 0 && startedCount == 0 {
		cancel()
		return fmt.Errorf("channels: all %d configured channels failed to start", configuredCount)
	}

	// Start the bus dispatchers (text + media) and the TTL janitor.
	m.startDispatchers(dispatchCtx)

	// Start shared HTTP server if configured.
	//
	// The listener is bound SYNCHRONOUSLY before StartAll returns: a
	// startup-time bind failure (e.g. "bind: address already in use") is a
	// HARD boot error. This server is the gateway's sole HTTP listener —
	// every route (SPA, /api/v1/*, /preview/, chat, WS, webhooks, health)
	// rides on m.mux, which is this server's Handler. Historically
	// ListenAndServe ran inside the goroutine and only logged its error, so
	// StartAll returned nil and the process stayed alive SERVING NOTHING — a
	// silent running-dead state. Binding up front turns a port collision into
	// an immediate, clear boot abort propagated through setupAndStartServices
	// → Run.
	//
	// Serve itself still runs in a goroutine. Its error handling keeps
	// swallowing http.ErrServerClosed / net.ErrClosed — those are EXPECTED
	// during graceful shutdown (StopAll calls httpServer.Shutdown, which
	// closes the listener mid-Accept) and MUST NOT be fatal. That is a
	// different lifecycle point from the startup bind above. Any OTHER Serve
	// error after a successful bind is unexpected (the listener was known-good
	// a moment ago) and is logged at Error; it is not made fatal because the
	// bind already succeeded and the common cause is a shutdown-time socket
	// race, not a startup misconfiguration.
	if m.httpServer != nil {
		// Bind synchronously so a startup-time failure is fatal to boot
		// (not silently swallowed as a running-dead process). Retry with a
		// short backoff: CI test harnesses launch gateways on fixed ports
		// in quick succession, and a previous shard's gateway may not have
		// released the port the instant this one starts. 3 attempts × 500ms
		// tolerates that transition race without masking a genuine conflict.
		var ln net.Listener
		var bindErr error
		for attempt := 0; attempt < 3; attempt++ {
			ln, bindErr = net.Listen("tcp", m.httpServer.Addr)
			if bindErr == nil {
				break
			}
			if attempt < 2 {
				logger.InfoCF("channels", "Shared HTTP server bind retry — port may be in transient use",
					map[string]any{"addr": m.httpServer.Addr, "attempt": attempt + 1, "error": bindErr.Error()})
				select {
				case <-time.After(500 * time.Millisecond):
				case <-ctx.Done():
				}
			}
		}
		if bindErr != nil {
			cancel()
			return fmt.Errorf("channels: bind shared HTTP server at %s: %w", m.httpServer.Addr, bindErr)
		}
		go func() {
			logger.InfoCF("channels", "Shared HTTP server listening", map[string]any{
				"addr": m.httpServer.Addr,
			})
			if err := m.httpServer.Serve(ln); err != nil &&
				!errors.Is(err, http.ErrServerClosed) &&
				!errors.Is(err, net.ErrClosed) {
				// Log at Error and return — do NOT call os.Exit here.
				// A listener race during shutdown (net.ErrClosed) or a
				// SIGPIPE / aggressive socket close must not kill the process
				// mid-shutdown, leaving audit flush and credential store in
				// an indeterminate state.
				logger.ErrorCF("channels", "Shared HTTP server error — server stopped unexpectedly", map[string]any{
					"error": err.Error(),
				})
			}
		}()
	}

	logger.InfoC("channels", "All channels started")

	// C1 fix: invoke RegisterCommands on every started channel that implements
	// CommandRegistrarCapable (FR-28 fallback: failures are WARN-logged, channel
	// startup continues). This registers canonical channel-surfaced commands with
	// the upstream platform (Telegram BotCommand, Slack slash commands, etc.) so
	// they appear in the platform's command menu.
	// Only non-hidden, Channel-surfaced commands are registered — hidden/deprecated
	// commands and web-only commands are excluded from the platform menu.
	allDefs := commands.BuiltinDefinitions()
	channelDefs := make([]commands.Definition, 0, len(allDefs))
	for _, def := range allDefs {
		if def.Hidden {
			continue
		}
		if !def.AllowsSurface(commands.SurfaceChannel) {
			continue
		}
		channelDefs = append(channelDefs, def)
	}
	for name, ch := range m.channels {
		if reg, ok := ch.(CommandRegistrarCapable); ok {
			name, reg := name, reg // capture for goroutine
			go func() {
				regCtx, regCancel := context.WithTimeout(dispatchCtx, 30*time.Second)
				defer regCancel()
				if err := reg.RegisterCommands(regCtx, channelDefs); err != nil {
					logger.WarnCF("channels", "RegisterCommands failed — platform command menu not updated",
						map[string]any{
							"channel": name,
							"error":   err.Error(),
						})
				} else {
					logger.InfoCF("channels", "RegisterCommands succeeded",
						map[string]any{"channel": name, "command_count": len(channelDefs)})
				}
			}()
		}
	}

	return nil
}

func (m *Manager) StopAll(ctx context.Context) error {
	// PHASE 1 — quiesce the dispatchers BEFORE anything closes a worker queue,
	// and do it WITHOUT holding m.mu.
	//
	// Cancelling alone is not sufficient: enqueueOutbound selects on
	// `w.queue <- msg` vs `<-ctx.Done()`, and a select whose send case is
	// already runnable may take it even after cancellation — so a
	// cancel-then-close still races, and a send on a closed channel PANICS,
	// crashing a gateway that was merely shutting down.
	//
	// The lock must be released for the wait: dispatchLoop takes m.mu.RLock to
	// resolve a worker, so waiting under m.mu.Lock would deadlock against a
	// dispatcher mid-iteration. Taking the lock only to swap the handle out
	// keeps that window as small as possible.
	m.mu.Lock()
	task := m.dispatchTask
	m.dispatchTask = nil
	m.mu.Unlock()
	if task != nil {
		task.cancel()
		task.wg.Wait() // dispatchers have EXITED; nothing can send to w.queue now
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	logger.InfoC("channels", "Stopping all channels")

	// Shutdown shared HTTP server first
	if m.httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := m.httpServer.Shutdown(shutdownCtx); err != nil {
			logger.ErrorCF("channels", "Shared HTTP server shutdown error", map[string]any{
				"error": err.Error(),
			})
		}
		m.httpServer = nil
	}

	// Dispatchers were cancelled AND waited for in phase 1 above, so the
	// close below can no longer race a send.

	// Close all worker queues and wait for them to drain
	for _, w := range m.workers {
		if w != nil {
			close(w.queue)
		}
	}
	for _, w := range m.workers {
		if w != nil {
			<-w.done
		}
	}
	// Close all media worker queues and wait for them to drain
	for _, w := range m.workers {
		if w != nil {
			close(w.mediaQueue)
		}
	}
	for _, w := range m.workers {
		if w != nil {
			<-w.mediaDone
		}
	}

	// Stop all channels
	for name, channel := range m.channels {
		logger.InfoCF("channels", "Stopping channel", map[string]any{
			"channel": name,
		})
		if err := channel.Stop(ctx); err != nil {
			logger.ErrorCF("channels", "Error stopping channel", map[string]any{
				"channel": name,
				"error":   err.Error(),
			})
		}
	}

	logger.InfoC("channels", "All channels stopped")
	return nil
}

// channelTypeForRateLimit resolves the channel TYPE (e.g. "discord") for an
// instance ID (e.g. "discord" for a bare single-instance key, or "discord.2"
// for a second Discord instance under N-per-type instancing, FR-015 / WS-B).
// channelRateConfig is keyed by TYPE, not instance ID, so a second instance of
// a rate-limited channel type must resolve to the same conservative limit as
// the first — otherwise it silently falls back to defaultRateLimit (10 msg/s)
// instead of the type's configured limit (e.g. Discord's 1 msg/s).
//
// Looks up the instance's normalized Type from m.config.Channels (populated by
// config.normalizeChannelMap at load time, so it is always present for a
// config-driven instance). Falls back to instanceID itself when no config
// entry exists (e.g. a synthetic/internal registration like "webchat" via
// RegisterChannel, which has no config.Channels entry) — matching the
// pre-existing behavior for that case. Caller must hold m.mu (all three
// production call sites — StartAll, Reload, RegisterChannel — already do).
//
// The fallback covers two distinct cases, logged at two distinct levels:
//   - No config.Channels entry at all for instanceID: a benign synthetic/
//     internal registration with nothing to look up — DEBUG.
//   - A config.Channels entry exists but its Type is empty: config-driven
//     entries always get Type populated by config.normalizeChannelMap, so
//     this means the entry and its Type went out of sync — a genuine
//     config/instance desync bug, not a benign registration. This project's
//     shipped default LogLevel is "warn" (pkg/config/defaults.go), so this
//     case is logged at WARN — a DEBUG-only line here would never reach a
//     default-level operator's logs, hiding the one diagnostic explaining why
//     a configured multi-instance channel is silently using the wrong rate
//     limit.
func (m *Manager) channelTypeForRateLimit(instanceID string) string {
	if m.config != nil {
		if inst, ok := m.config.Channels[instanceID]; ok {
			if inst.Type != "" {
				return inst.Type
			}
			logger.WarnCF(
				"channels",
				"config.Channels entry exists for instance but has no Type set; using instance ID as rate-limit type — this indicates a config/instance desync bug, not a benign synthetic registration",
				map[string]any{
					"instance_id": instanceID,
				},
			)
			return instanceID
		}
	}
	logger.DebugCF(
		"channels",
		"No config.Channels entry for instance; using instance ID as rate-limit type",
		map[string]any{
			"instance_id": instanceID,
		},
	)
	return instanceID
}

// newChannelWorker creates a channelWorker with a rate limiter configured for
// the given channel TYPE. Callers must pass the channel's TYPE (e.g.
// "discord"), not its instance ID — channelRateConfig is keyed by type so that
// every instance of a given type shares the same rate limit. Use
// Manager.channelTypeForRateLimit to resolve an instance ID to its type.
func newChannelWorker(channelType string, ch Channel) *channelWorker {
	rateVal := float64(defaultRateLimit)
	if r, ok := channelRateConfig[channelType]; ok {
		rateVal = r
	}
	burst := int(math.Max(1, math.Ceil(rateVal/2)))

	return &channelWorker{
		ch:         ch,
		queue:      make(chan bus.OutboundMessage, defaultChannelQueueSize),
		mediaQueue: make(chan bus.OutboundMediaMessage, defaultChannelQueueSize),
		done:       make(chan struct{}),
		mediaDone:  make(chan struct{}),
		limiter:    rate.NewLimiter(rate.Limit(rateVal), burst),
	}
}

// runWorker processes outbound messages for a single channel.
// Message processing follows this order:
//  1. SplitByMarker (if enabled in config) - LLM semantic marker-based splitting
//  2. SplitMessage - channel-specific length-based splitting (MaxMessageLength)
//
// cfg is a config SNAPSHOT captured by the caller (StartAll / Reload /
// RegisterChannel — all of which spawn this goroutine while already holding
// m.mu.Lock()), not a live read of m.config. This is deliberate, not merely
// lock-avoidance: runWorker previously read m.config directly with no lock at
// all, racing Reload's m.mu-protected write (`go test -race`: "WARNING: DATA
// RACE ... Read ... runWorker ... manager.go:1179" vs "Previous write ...
// Reload"). Taking m.mu.RLock() here instead was considered and rejected —
// Reload's "restart workers for existing (unchanged) channels" step
// (see below in Reload) blocks on `<-w.done`/`<-w.mediaDone` for the OLD
// runWorker generation to exit while STILL HOLDING m.mu.Lock() (the write
// lock is held across nearly all of Reload, released only briefly for the
// separate dispatch-generation quiesce earlier in that function). If this
// goroutine's select loop is mid-processing a message when cancellation
// races the queue's ready case (both become selectable simultaneously, and
// Go's select does not prefer ctx.Done()), it can keep dequeuing and
// processing messages after ctx is canceled; an RLock in that path would
// then block forever on the writer Reload is holding while Reload itself
// blocks on this very goroutine's exit — a deadlock, not just a slow path.
// A snapshot avoids taking any lock in the hot path.
//
// This does not change real-world semantics: every Reload (and StartAll/
// RegisterChannel) already restarts every runWorker goroutine — including
// ones for unchanged channels — on a fresh dispatchCtx, so a given runWorker
// generation was already conceptually paired 1:1 with the config in effect
// when it was (re)started; only the brief in-flight-message window during a
// live Reload could previously observe a torn/racy value. A worker will not
// see a config change until its next Reload-driven restart, exactly as
// before, minus the race.
func (m *Manager) runWorker(ctx context.Context, name string, w *channelWorker, cfg *config.Config) {
	defer close(w.done)
	for {
		select {
		case msg, ok := <-w.queue:
			if !ok {
				return
			}
			// Strip the internal drop-notice sentinel before delivery so the
			// user only sees the human-readable notice text. Capture whether this
			// was a manager-injected notice first — once stripped, sendWithRetry
			// can no longer detect it, so it relies on this flag to avoid a
			// notice-of-a-failed-notice amplification loop.
			isNotice := strings.HasPrefix(msg.Content, dropNoticePrefix)
			msg.Content = strings.TrimPrefix(msg.Content, dropNoticePrefix)
			maxLen := 0
			if mlp, ok := w.ch.(MessageLengthProvider); ok {
				maxLen = mlp.MaxMessageLength()
			}

			// Collect all message chunks to send
			var chunks []string

			// Step 1: Try marker-based splitting if enabled
			if cfg != nil && cfg.Agents.Defaults.SplitOnMarker {
				if markerChunks := SplitByMarker(msg.Content); len(markerChunks) > 1 {
					for _, chunk := range markerChunks {
						chunks = append(chunks, splitByLength(chunk, maxLen)...)
					}
				}
			}

			// Step 2: Fallback to length-based splitting if no chunks from marker
			if len(chunks) == 0 {
				chunks = splitByLength(msg.Content, maxLen)
			}

			// Step 3: Send all chunks
			for _, chunk := range chunks {
				chunkMsg := msg
				chunkMsg.Content = chunk
				m.sendWithRetry(ctx, name, w, chunkMsg, isNotice)
			}
		case <-ctx.Done():
			return
		}
	}
}

// splitByLength splits content by maxLen if needed, otherwise returns single chunk.
func splitByLength(content string, maxLen int) []string {
	if maxLen > 0 && len([]rune(content)) > maxLen {
		return SplitMessage(content, maxLen)
	}
	return []string{content}
}

// sendWithRetry sends a message through the channel with rate limiting and
// retry logic. It classifies errors to determine the retry strategy:
//   - ErrNotRunning / ErrSendFailed: permanent, no retry
//   - ErrRateLimit: fixed delay retry
//   - ErrTemporary / unknown: exponential backoff retry
//
// sendWithRetry delivers msg, retrying transient errors. isNotice must be true
// when msg is a manager-injected drop/failure notice (the dropNoticePrefix
// sentinel is stripped before delivery, so the prefix can no longer be detected
// from msg.Content here — the caller passes the flag instead). It gates the
// terminal-failure fallback so a notice that itself fails to send does not
// recurse into an unbounded notice storm.
func (m *Manager) sendWithRetry(
	ctx context.Context,
	name string,
	w *channelWorker,
	msg bus.OutboundMessage,
	isNotice bool,
) {
	// Rate limit: wait for token
	if err := w.limiter.Wait(ctx); err != nil {
		// ctx canceled, shutting down
		return
	}

	// Pre-send: stop typing and try to edit placeholder
	if m.preSend(ctx, name, msg, w.ch) {
		return // placeholder was edited successfully, skip Send
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = w.ch.Send(ctx, msg)
		if lastErr == nil {
			return
		}

		// Permanent failures — don't retry
		if errors.Is(lastErr, ErrNotRunning) || errors.Is(lastErr, ErrSendFailed) {
			break
		}

		// Last attempt exhausted — don't sleep
		if attempt == maxRetries {
			break
		}

		// Rate limit error — fixed delay
		if errors.Is(lastErr, ErrRateLimit) {
			select {
			case <-time.After(rateLimitDelay):
				continue
			case <-ctx.Done():
				return
			}
		}

		// ErrTemporary or unknown error — exponential backoff
		backoff := min(time.Duration(float64(baseBackoff)*math.Pow(2, float64(attempt))), maxBackoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
	}

	// All retries exhausted or permanent failure
	logger.ErrorCF("channels", "Send failed", map[string]any{
		"channel": name,
		"chat_id": msg.ChatID,
		"error":   lastErr.Error(),
		"retries": maxRetries,
	})

	// Surface a short user-facing notice so a failed text send is not silent.
	// Guard against re-notifying our own fallback (isNotice marks manager-injected
	// notices, whose dropNoticePrefix sentinel runWorker already stripped) so a
	// fallback that itself fails to send does not recurse into a notice storm. The
	// publish is non-blocking (see notifyDrop) to avoid stalling the worker when
	// the bus is saturated.
	if !isNotice {
		m.notifyDrop(name, msg.ChatID, "[message delivery failed]")
	}
}

func dispatchLoop[M any](
	ctx context.Context,
	m *Manager,
	ch <-chan M,
	getChannel func(M) string,
	enqueue func(context.Context, *channelWorker, M) bool,
	startMsg, stopMsg, unknownMsg, noWorkerMsg string,
) {
	logger.InfoC("channels", startMsg)

	for {
		select {
		case <-ctx.Done():
			logger.InfoC("channels", stopMsg)
			return

		case msg, ok := <-ch:
			if !ok {
				logger.InfoC("channels", stopMsg)
				return
			}

			channel := getChannel(msg)

			// Silently skip internal channels
			if constants.IsInternalChannel(channel) {
				continue
			}

			m.mu.RLock()
			_, exists := m.channels[channel]
			w, wExists := m.workers[channel]
			m.mu.RUnlock()

			if !exists {
				logger.WarnCF("channels", unknownMsg, map[string]any{"channel": channel})
				continue
			}

			if wExists && w != nil {
				if !safeEnqueue(ctx, w, msg, enqueue) {
					return
				}
			} else if exists {
				logger.WarnCF("channels", noWorkerMsg, map[string]any{"channel": channel})
			}
		}
	}
}

// safeEnqueue invokes the per-message enqueue closure with a recover guard. The
// enqueue closures send on a worker queue inside a select that also watches
// ctx.Done(); during shutdown/reload the worker queue may be closed concurrently
// (StopAll/Reload close(w.queue)). If the queue is closed AND the send case is
// chosen before ctx.Done() is observed (Go selects randomly among ready cases),
// the send panics with "send on closed channel". We recover from that, treat it
// as "stop dispatching" (return false), and exit the loop cleanly instead of
// crashing the process mid-shutdown.
func safeEnqueue[M any](
	ctx context.Context,
	w *channelWorker,
	msg M,
	enqueue func(context.Context, *channelWorker, M) bool,
) (cont bool) {
	defer func() {
		if r := recover(); r != nil {
			logger.WarnCF("channels", "Enqueue panic recovered (queue closed during shutdown)",
				map[string]any{"panic": fmt.Sprintf("%v", r)})
			cont = false
		}
	}()
	return enqueue(ctx, w, msg)
}

// enqueueOutbound attempts to hand a text message to the channel worker's queue.
// It returns true to keep the dispatch loop running, false to stop it (ctx done).
// On backpressure (queue full) it drops the message and publishes a user-facing
// drop notice — unless the message is itself a drop notice (the dropNoticePrefix
// guard), which would otherwise amplify notices on a saturated channel. Extracted
// from the dispatchOutbound closure so the drop/notify decision is unit-testable
// without a running dispatcher goroutine.
func (m *Manager) enqueueOutbound(ctx context.Context, w *channelWorker, msg bus.OutboundMessage) bool {
	select {
	case w.queue <- msg:
		return true
	case <-ctx.Done():
		// Shutting down/reloading: do not attempt the send (the worker
		// queue may already be closed, which would panic). Exit the loop.
		return false
	default:
		// Backpressure: the worker queue is full. Report the drop instead
		// of silently swallowing it, and surface user-facing feedback the
		// same way a failed media send does so the user is not left
		// waiting on a reply that will never arrive.
		logger.ErrorCF("channels", "Outbound queue full, message dropped",
			map[string]any{"channel": msg.Channel, "chat_id": msg.ChatID})
		// Don't re-notify if a drop notice itself got dropped (avoids
		// an amplification loop).
		if !strings.HasPrefix(msg.Content, dropNoticePrefix) {
			m.notifyDrop(msg.Channel, msg.ChatID, "[message dropped: channel is overloaded, please retry]")
		}
		return true
	}
}

func (m *Manager) dispatchOutbound(ctx context.Context) {
	dispatchLoop(
		ctx, m,
		m.bus.OutboundChan(),
		func(msg bus.OutboundMessage) string { return msg.Channel },
		func(ctx context.Context, w *channelWorker, msg bus.OutboundMessage) bool {
			// ADR-065 FR-7: the last common point before the wire. Only
			// agent-originated sends are in scope; see
			// allowAgentOriginatedSend for why a second check exists.
			// Returning false here would TERMINATE the dispatcher: dispatchLoop
			// treats this as its continuation signal (`if !safeEnqueue(...) {
			// return }`), and enqueueOutbound returns false only for
			// ctx.Done(). An earlier version of this hook returned false on a
			// refusal, which would have killed outbound messaging on EVERY
			// channel, permanently, on the first refused send. Skip the
			// message; keep the loop alive.
			if !allowAgentOriginatedSend(m.configSnapshot(), msg) {
				return true
			}
			return m.enqueueOutbound(ctx, w, msg)
		},
		"Outbound dispatcher started",
		"Outbound dispatcher stopped",
		"Unknown channel for outbound message",
		"Channel has no active worker, skipping message",
	)
}

func (m *Manager) dispatchOutboundMedia(ctx context.Context) {
	dispatchLoop(
		ctx, m,
		m.bus.OutboundMediaChan(),
		func(msg bus.OutboundMediaMessage) string { return msg.Channel },
		func(ctx context.Context, w *channelWorker, msg bus.OutboundMediaMessage) bool {
			select {
			case w.mediaQueue <- msg:
				return true
			case <-ctx.Done():
				// Shutting down/reloading: do not attempt the send (the worker
				// queue may already be closed, which would panic). Exit the loop.
				return false
			default:
				// Backpressure: the media queue is full. Report the drop and
				// surface user-facing feedback.
				logger.ErrorCF("channels", "Outbound media queue full, message dropped",
					map[string]any{"channel": msg.Channel, "chat_id": msg.ChatID})
				m.notifyDrop(msg.Channel, msg.ChatID, "[media dropped: channel is overloaded, please retry]")
				return true
			}
		},
		"Outbound media dispatcher started",
		"Outbound media dispatcher stopped",
		"Unknown channel for outbound media message",
		"Channel has no active worker, skipping media message",
	)
}

// runMediaWorker processes outbound media messages for a single channel.
func (m *Manager) runMediaWorker(ctx context.Context, name string, w *channelWorker) {
	defer close(w.mediaDone)
	for {
		select {
		case msg, ok := <-w.mediaQueue:
			if !ok {
				return
			}
			if err := m.sendMediaWithRetry(ctx, name, w, msg); err != nil {
				logger.ErrorCF("channels", "Failed to send media message", map[string]any{
					"channel": name,
					"chat_id": msg.ChatID,
					"error":   err.Error(),
				})
				// Publish a text fallback so the user knows delivery failed.
				fallbackMsg := bus.OutboundMessage{
					Channel: msg.Channel,
					ChatID:  msg.ChatID,
					Content: "[media delivery failed]",
				}
				if sendErr := m.bus.PublishOutbound(ctx, fallbackMsg); sendErr != nil {
					logger.WarnCF("channels", "Failed to publish media fallback message", map[string]any{
						"channel": name,
						"error":   sendErr.Error(),
					})
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// sendMediaWithRetry sends a media message through the channel with rate limiting and
// retry logic. It returns nil on success, or the last error after retries,
// including when the channel does not support MediaSender.
func (m *Manager) sendMediaWithRetry(
	ctx context.Context,
	name string,
	w *channelWorker,
	msg bus.OutboundMediaMessage,
) error {
	ms, ok := w.ch.(MediaSender)
	if !ok {
		err := fmt.Errorf("channel %q does not support media sending", name)
		logger.WarnCF("channels", "Channel does not support MediaSender", map[string]any{
			"channel": name,
			"error":   err.Error(),
		})
		return err
	}

	// Rate limit: wait for token
	if err := w.limiter.Wait(ctx); err != nil {
		return err
	}

	// Pre-send: stop typing and clean up any placeholder before sending media.
	m.preSendMedia(ctx, name, msg, w.ch)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = ms.SendMedia(ctx, msg)
		if lastErr == nil {
			return nil
		}

		// Permanent failures — don't retry
		if errors.Is(lastErr, ErrNotRunning) || errors.Is(lastErr, ErrSendFailed) {
			break
		}

		// Last attempt exhausted — don't sleep
		if attempt == maxRetries {
			break
		}

		// Rate limit error — fixed delay
		if errors.Is(lastErr, ErrRateLimit) {
			select {
			case <-time.After(rateLimitDelay):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// ErrTemporary or unknown error — exponential backoff
		backoff := min(time.Duration(float64(baseBackoff)*math.Pow(2, float64(attempt))), maxBackoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// All retries exhausted or permanent failure
	logger.ErrorCF("channels", "SendMedia failed", map[string]any{
		"channel": name,
		"chat_id": msg.ChatID,
		"error":   lastErr.Error(),
		"retries": maxRetries,
	})
	return lastErr
}

// runTTLJanitor periodically scans the typingStops, reactionUndos, placeholders
// and streamActive maps and evicts entries that have exceeded their TTL. This
// prevents memory accumulation when outbound paths fail to trigger preSend
// (e.g. LLM errors). The per-tick sweep is delegated to evictStale so it can be
// unit-tested deterministically without a wall-clock timer.
func (m *Manager) runTTLJanitor(ctx context.Context) {
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.evictStale(now)
		}
	}
}

// evictStale performs a single TTL sweep over all expiry-tracked maps, evicting
// (and invoking the idempotent cleanup of) every entry older than its TTL as of
// now. Extracted from runTTLJanitor so the eviction logic is testable without
// relying on the ticker.
func (m *Manager) evictStale(now time.Time) {
	m.typingStops.Range(func(key, value any) bool {
		if entry, ok := value.(typingEntry); ok {
			if now.Sub(entry.createdAt) > typingStopTTL {
				if _, loaded := m.typingStops.LoadAndDelete(key); loaded {
					entry.stop() // idempotent, safe
				}
			}
		}
		return true
	})
	m.reactionUndos.Range(func(key, value any) bool {
		if entry, ok := value.(reactionEntry); ok {
			if now.Sub(entry.createdAt) > typingStopTTL {
				if _, loaded := m.reactionUndos.LoadAndDelete(key); loaded {
					entry.undo() // idempotent, safe
				}
			}
		}
		return true
	})
	m.placeholders.Range(func(key, value any) bool {
		if entry, ok := value.(placeholderEntry); ok {
			if now.Sub(entry.createdAt) > placeholderTTL {
				m.placeholders.Delete(key)
			}
		}
		return true
	})
	// Evict stale streamActive markers. A marker is normally consumed by
	// preSend/preSendMedia, but if the agent errors after Finalize and
	// never publishes an outbound message for the key, the marker leaks.
	m.streamActive.Range(func(key, value any) bool {
		if entry, ok := value.(streamEntry); ok {
			if now.Sub(entry.createdAt) > placeholderTTL {
				m.streamActive.Delete(key)
			}
		}
		return true
	})
}

func (m *Manager) GetChannel(name string) (Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	channel, ok := m.channels[name]
	return channel, ok
}

func (m *Manager) GetStatus() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make(map[string]any)
	for name, channel := range m.channels {
		status[name] = map[string]any{
			"enabled": true,
			"running": channel.IsRunning(),
		}
	}
	return status
}

func (m *Manager) GetEnabledChannels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.channels))
	for name := range m.channels {
		names = append(names, name)
	}
	return names
}

// Reload applies configuration changes to the channel manager. It compares
// channel config hashes to detect additions and removals: removed channels are
// stopped, added channels are initialized and started, and existing (unchanged)
// channel workers are restarted on a fresh context to ensure continued routing.
func (m *Manager) Reload(ctx context.Context, cfg *config.Config, secrets credentials.SecretBundle) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Save old config and secrets so we can revert on error.
	oldConfig := m.config
	oldSecrets := m.secrets

	// Update config and secrets early: initChannel uses m.config and m.secrets via factory call.
	m.config = cfg
	m.secrets = secrets

	list := toChannelHashes(cfg)
	added, removed := compareChannels(m.channelHashes, list)

	// Fix 16: cancel the existing dispatcher before creating a new one to prevent context leak.
	//
	// Capture the OLD task locally before it is replaced: newDispatchContext
	// (below) immediately overwrites m.dispatchTask/m.dispatchCtx with a fresh
	// generation, so oldTask is the only reference we will have left to wait on
	// the OLD generation's goroutines in the quiesce phase further down.
	oldTask := m.dispatchTask
	if oldTask != nil {
		oldTask.cancel()
	}

	// Refresh the dispatch context (and m.dispatchCtx, used by RegisterChannel and
	// post-reload workers) in lockstep with m.dispatchTask. Without this, dispatch
	// goroutines for channels added after a reload run on the just-canceled context
	// and silently drop messages.
	//
	// Moved up from after the removed-channels loop (below) so it runs BEFORE the
	// quiesce wait releases m.mu: a concurrent RegisterChannel landing in that
	// released window must bind its worker to this live context, not the one we
	// just cancelled above.
	dispatchCtx, cancel := m.newDispatchContext(ctx)

	// Quiesce the OLD dispatch generation before ANY worker queue for a removed
	// channel is closed (see unregisterChannelLocked, invoked from deferFuncs at
	// the end of this function).
	//
	// Cancelling oldTask above is not sufficient: enqueueOutbound and its media
	// counterpart select on `w.queue <- msg` vs `<-ctx.Done()`, and a select whose
	// send case is already runnable can be taken even after cancellation — so
	// closing a removed channel's queue while the OLD dispatchOutbound /
	// dispatchOutboundMedia goroutines might still be mid-select races a send
	// against the close. A send on a closed channel PANICS — this is the same
	// shape as the StopAll bug fixed in f5cfa241, just triggered by Reload
	// instead of shutdown.
	//
	// The wait MUST run with m.mu released: dispatchLoop takes m.mu.RLock() to
	// resolve a worker before it can even reach that select, so waiting under
	// m.mu.Lock() — held for the rest of this function via the defer above —
	// would deadlock against an OLD dispatch goroutine that has not yet gotten
	// past the RLock gate.
	//
	// Releasing here is safe: in production, Reload is only ever invoked from the
	// gateway's single reload-consumer loop (pkg/gateway/gateway.go's `for { select
	// { ... } }`), which is itself single-flight (reloadCoalesceMu /
	// beginReload+finishReload coalesce concurrent triggers into the in-flight
	// cycle rather than starting a second one) and mutually exclusive with the
	// shutdown path that calls StopAll — both are arms of the very same select, so
	// only one of {Reload, StopAll} ever runs at a time. Nothing can therefore
	// mutate m.dispatchTask/m.workers/m.channels out from under this wait via
	// another Reload or StopAll call. The one real concurrent caller of a Manager
	// method, RegisterChannel, is handled above (it observes the already-refreshed
	// dispatchCtx). A concurrent RLock reader (GetStatus/GetChannel/
	// GetEnabledChannels/SendMessage/SendToChannel/SendMedia) can observe the NEW
	// m.config/m.secrets paired with the OLD m.channels/m.workers/m.channelHashes
	// during this window, but none of those readers combine config with the
	// channel set, so that transient mismatch is not observable as a bug.
	if oldTask != nil {
		m.mu.Unlock()
		oldTask.wg.Wait() // OLD dispatchers have EXITED; nothing can send on a removed channel's queue now
		m.mu.Lock()
	}

	// Fix 14 (removed loop): capture loop variable with a local copy to avoid Go closure capture bug.
	deferFuncs := make([]func(), 0, len(removed)+len(added))
	for _, name := range removed {
		n := name // local copy — prevents closure from capturing the loop variable
		// Stop all channels
		channel := m.channels[n]
		// Defense-in-depth: a "removed" name that never resolved to a registered
		// channel (e.g. it failed to construct on a prior boot/reload) leaves
		// m.channels[n] nil. Calling Stop would panic and crash the gateway. Skip the
		// Stop but still queue the unregister so any stale bookkeeping is cleaned up.
		if channel == nil {
			logger.WarnCF("channels", "Skipping stop for unregistered channel", map[string]any{
				"channel": n,
			})
			deferFuncs = append(deferFuncs, func() {
				m.unregisterChannelLocked(n)
			})
			continue
		}
		logger.InfoCF("channels", "Stopping channel", map[string]any{
			"channel": n,
		})
		if err := channel.Stop(ctx); err != nil {
			logger.ErrorCF("channels", "Error stopping channel", map[string]any{
				"channel": n,
				"error":   err.Error(),
			})
		}
		deferFuncs = append(deferFuncs, func() {
			m.unregisterChannelLocked(n)
		})
	}
	// dispatchCtx/cancel were already created above (before the quiesce wait), so
	// added-channel workers below bind to that same refreshed generation.
	cc, err := toChannelConfig(cfg, added)
	if err != nil {
		logger.ErrorC("channels", fmt.Sprintf("toChannelConfig error: %v", err))
		m.config = oldConfig
		m.secrets = oldSecrets
		cancel()
		return err
	}
	// Capture the current failure list before clearing so we can revert it if
	// initChannels fails — preventing a false-positive degraded signal after a
	// rolled-back reload.
	oldFailed := append([]ChannelInitError(nil), m.failedChannels...)

	// Scope the clear to (a) the channels being re-attempted (the added set)
	// and (b) any channel that is no longer enabled in the NEW config at all.
	// Entries for channels that are unchanged AND still enabled are left
	// intact so that an unchanged-but-still-failed channel continues to be
	// reported as degraded. Entries in added are dropped here; initChannels
	// will re-record a failure if they fail again.
	//
	// (b) closes a real, reproduced defect: enable_channel("irc") with no
	// credentials yet fails ITS PREREQUISITE CHECK before ever reaching
	// initChannel — so it is NEVER hash-committed to m.channelHashes (the
	// added-loop below deletes it from `list` on failure, same as a real
	// construction failure). A channel that was never hash-committed can
	// never appear in a later reload's `removed` diff either (compareChannels
	// only emits `removed` for keys PRESENT in the old m.channelHashes) — so
	// disable_channel("irc") afterward produces neither an `added` nor a
	// `removed` entry for it, and the ORIGINAL added-only clear left its
	// ChannelInitError orphaned in m.failedChannels forever. /health then
	// reported "degraded" permanently, surviving every subsequent reload,
	// because no future reload's added/removed diff could ever reach an
	// instanceID that was never successfully committed in the first place —
	// only a full gateway restart (which rebuilds m.failedChannels from
	// scratch) cleared it. `list` here is toChannelHashes(cfg) — the set of
	// instanceIDs enabled in the NEW config — computed above and not yet
	// mutated by the added-loop's own `delete(list, n)` failure bookkeeping,
	// so checking membership in it is exactly "is this channel enabled at
	// all right now", independent of whether it was ever hash-committed.
	// Disabling a channel is a clean state, never a degraded one, regardless
	// of whether it was running, never-started, or already failed.
	addedNames := make(map[string]struct{}, len(added))
	for _, n := range added {
		addedNames[n] = struct{}{}
	}
	kept := m.failedChannels[:0]
	for _, f := range m.failedChannels {
		if _, stillEnabled := list[f.Name]; !stillEnabled {
			continue // no longer enabled in the new config — stale, drop it
		}
		if _, inAdded := addedNames[f.Name]; !inAdded {
			kept = append(kept, f)
		}
	}
	m.failedChannels = kept

	err = m.initChannels(cc)
	if err != nil {
		logger.ErrorC("channels", fmt.Sprintf("initChannels error: %v", err))
		m.config = oldConfig
		m.secrets = oldSecrets
		m.failedChannels = oldFailed
		cancel()
		return err
	}
	// Fix 14 (added loop): capture loop variables with local copies.
	for _, name := range added {
		n := name // local copy — prevents closure from capturing the loop variable
		channel := m.channels[n]
		// Defense-in-depth: if the diff produced an "added" name that does not resolve
		// to a constructed channel (e.g. a config-key/registered-name mismatch, or an
		// initChannels if-ladder that skipped a misconfigured channel), m.channels[n]
		// is nil. Calling Start on it would panic and crash the entire gateway. Record
		// a degraded failure, drop the hash so the channel is re-attempted on the next
		// reload, and continue — a single missing channel must NEVER take down the bus.
		if channel == nil {
			// Give a specific, actionable reason when this is the ordinary,
			// well-understood case of an enabled channel that simply hasn't
			// been configured yet (initChannels's prerequisite-checker branch
			// skips construction silently rather than erroring, so from here
			// the only signal is that m.channels[n] never got populated).
			// Falling back to the generic "config/registration name mismatch
			// or skipped init" text for THIS case reads as an internal bug to
			// whoever is troubleshooting it, when it is normal, expected
			// behavior for "enable_channel called before configure_channel".
			// Reserve the generic text for the case it actually describes: a
			// genuinely unexpected nil registration with no missing
			// prerequisite to explain it.
			reason := fmt.Errorf(
				"reload: channel %q was marked for start but is not registered "+
					"(config/registration name mismatch or skipped init)", n)
			if inst, ok := cc[n]; ok {
				instType := inst.Type
				if instType == "" {
					instType = n
				}
				if checker, hasChecker := getPrerequisite(instType); hasChecker {
					if missing, prereqOK := checker(inst); !prereqOK {
						reason = fmt.Errorf(
							"channel %q is enabled but missing required configuration (%s) — "+
								"configure it, then it will connect on the next reload", n, missing)
					}
				}
			}
			m.recordChannelFailure(n, n, reason)
			delete(list, n)
			continue
		}
		logger.InfoCF("channels", "Starting channel", map[string]any{
			"channel": n,
		})
		if err := channel.Start(ctx); err != nil {
			logger.ErrorCF("channels", "Failed to start channel", map[string]any{
				"channel": n,
				"error":   err.Error(),
			})
			// Fix 35: don't commit the hash for channels that failed to start.
			delete(list, n)
			continue
		}
		// Lazily create worker only after channel starts successfully. Resolve
		// TYPE from instance ID for rate limiting (see channelTypeForRateLimit).
		w := newChannelWorker(m.channelTypeForRateLimit(n), channel)
		m.workers[n] = w
		// Snapshot m.config (already the NEW cfg, assigned above) while still
		// holding m.mu.Lock() — see runWorker's doc comment.
		go m.runWorker(dispatchCtx, n, w, m.config)
		go m.runMediaWorker(dispatchCtx, n, w)
		deferFuncs = append(deferFuncs, func() {
			m.registerChannelLocked(n, channel)
		})
	}

	// Commit hashes only for successfully started channels.
	m.channelHashes = list

	// Restart the bus dispatchers + TTL janitor against the new dispatchCtx so
	// outbound routing and stale-entry eviction resume after the old context was
	// canceled above. (Reload previously forgot the janitor here — startDispatchers
	// keeps StartAll and Reload in lockstep so it can't be omitted again.)
	m.startDispatchers(dispatchCtx)

	// Restart workers for existing (unchanged) channels on the new dispatch context.
	// Without this, unchanged channel workers retain the old (canceled) context
	// and stop routing messages after a Reload.
	//
	// We must wait for old worker goroutines to finish (they close w.done/w.mediaDone
	// on exit) and then reset those channels before launching new goroutines.
	// Without this, the new goroutines' defer close() would panic with
	// "close of closed channel".
	addedSet := make(map[string]struct{}, len(added))
	for _, n := range added {
		addedSet[n] = struct{}{}
	}
	for name, w := range m.workers {
		if _, isNew := addedSet[name]; isNew {
			continue // already started above
		}
		// Wait for old goroutines to finish (they were canceled above).
		<-w.done
		<-w.mediaDone
		// Reset done channels so the new goroutines can close them cleanly.
		w.done = make(chan struct{})
		w.mediaDone = make(chan struct{})
		// Snapshot m.config (already the NEW cfg) while still holding
		// m.mu.Lock() — see runWorker's doc comment.
		go m.runWorker(dispatchCtx, name, w, m.config)
		go m.runMediaWorker(dispatchCtx, name, w)
	}

	// Execute the register/unregister mutations while STILL holding m.mu.
	//
	// These call registerChannelLocked / unregisterChannelLocked, which mutate the
	// shared m.channels / m.workers / m.mux maps. Concurrent readers take m.mu.RLock
	// (SendMessage, GetStatus, GetChannel, the dispatchLoop goroutines restarted by
	// startDispatchers above) and a concurrent RegisterChannel takes m.mu.Lock, so
	// mutating these maps with the lock released is a data race (and a concurrent
	// map read+write panic). We therefore keep the lock held: the deferred
	// m.mu.Unlock() at the top releases it on return.
	//
	// This is deadlock-free: unregisterChannelLocked blocks on <-w.done / <-w.mediaDone
	// (and runs synchronously), but runWorker/runMediaWorker close those channels on
	// ctx cancellation or queue close WITHOUT acquiring m.mu, so the wait cannot block
	// on the lock we hold. The *Locked helpers must never acquire m.mu themselves
	// (only their exported wrappers RegisterChannel/UnregisterChannel do) — calling
	// them here while holding the lock relies on that invariant.
	for _, f := range deferFuncs {
		f()
	}
	return nil
}

func (m *Manager) RegisterChannel(name string, channel Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registerChannelLocked(name, channel)

	// Spin up a worker so SendMessage/SendMedia can route to this channel.
	// Without a worker, synchronous delivery (e.g., media frames) fails with
	// "channel has no active worker".
	if _, hasWorker := m.workers[name]; !hasWorker && m.dispatchCtx != nil {
		w := newChannelWorker(m.channelTypeForRateLimit(name), channel)
		m.workers[name] = w
		// Snapshot m.config while still holding m.mu.Lock() — see runWorker's
		// doc comment.
		go m.runWorker(m.dispatchCtx, name, w, m.config)
		go m.runMediaWorker(m.dispatchCtx, name, w)
	}
}

// registerChannelLocked registers a channel. Caller must hold m.mu.
func (m *Manager) registerChannelLocked(name string, channel Channel) {
	m.channels[name] = channel
	if m.mux != nil {
		m.registerChannelHTTPHandler(name, channel)
	}
}

func (m *Manager) UnregisterChannel(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unregisterChannelLocked(name)
}

// unregisterChannelLocked unregisters a channel. Caller must hold m.mu.
func (m *Manager) unregisterChannelLocked(name string) {
	if ch, ok := m.channels[name]; ok && m.mux != nil {
		m.unregisterChannelHTTPHandler(name, ch)
	}
	if w, ok := m.workers[name]; ok && w != nil {
		close(w.queue)
		<-w.done
		close(w.mediaQueue)
		<-w.mediaDone
	}
	delete(m.workers, name)
	delete(m.channels, name)
}

// SendMessage sends an outbound message synchronously through the channel
// worker's rate limiter and retry logic. It blocks until the message is
// delivered (or all retries are exhausted), which preserves ordering when
// a subsequent operation depends on the message having been sent.
func (m *Manager) SendMessage(ctx context.Context, msg bus.OutboundMessage) error {
	m.mu.RLock()
	_, exists := m.channels[msg.Channel]
	w, wExists := m.workers[msg.Channel]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %s not found", msg.Channel)
	}
	if !wExists || w == nil {
		return fmt.Errorf("channel %s has no active worker", msg.Channel)
	}

	maxLen := 0
	if mlp, ok := w.ch.(MessageLengthProvider); ok {
		maxLen = mlp.MaxMessageLength()
	}
	// The direct send path does not carry manager-injected notices (those flow
	// via notifyDrop -> bus -> runWorker), but detect the sentinel defensively so
	// the no-amplification guard holds here too.
	isNotice := strings.HasPrefix(msg.Content, dropNoticePrefix)
	if maxLen > 0 && len([]rune(msg.Content)) > maxLen {
		for _, chunk := range SplitMessage(msg.Content, maxLen) {
			chunkMsg := msg
			chunkMsg.Content = chunk
			m.sendWithRetry(ctx, msg.Channel, w, chunkMsg, isNotice)
		}
	} else {
		m.sendWithRetry(ctx, msg.Channel, w, msg, isNotice)
	}
	return nil
}

// SendMedia sends outbound media synchronously through the channel worker's
// rate limiter and retry logic. It blocks until the media is delivered (or all
// retries are exhausted), which preserves ordering when later agent behavior
// depends on actual media delivery.
func (m *Manager) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) error {
	m.mu.RLock()
	_, exists := m.channels[msg.Channel]
	w, wExists := m.workers[msg.Channel]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %s not found", msg.Channel)
	}
	if !wExists || w == nil {
		return fmt.Errorf("channel %s has no active worker", msg.Channel)
	}

	return m.sendMediaWithRetry(ctx, msg.Channel, w, msg)
}

func (m *Manager) SendToChannel(ctx context.Context, channelName, chatID, content string) error {
	m.mu.RLock()
	channel, exists := m.channels[channelName]
	w, wExists := m.workers[channelName]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("channel %s not found", channelName)
	}

	msg := bus.OutboundMessage{
		Channel: channelName,
		ChatID:  chatID,
		Content: content,
	}

	if wExists && w != nil {
		select {
		case w.queue <- msg:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Fallback: direct send (should not happen in normal operation).
	// channel was captured under the lock above, so this access is safe.
	return channel.Send(ctx, msg)
}
