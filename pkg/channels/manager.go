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
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/commands"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/constants"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/health"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/media"
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
type ChannelInitError struct {
	Channel string
	Err     error
}

func (e *ChannelInitError) Error() string {
	return fmt.Sprintf("channel %q: %v", e.Channel, e.Err)
}

func (e *ChannelInitError) Unwrap() error { return e.Err }

type Manager struct {
	channels     map[string]Channel
	workers      map[string]*channelWorker
	bus          *bus.MessageBus
	config       *config.Config
	secrets      credentials.SecretBundle // resolved plaintext secrets; never written to env
	mediaStore   media.MediaStore
	dispatchTask *asyncTask
	dispatchCtx  context.Context // stored so RegisterChannel can spin up workers after Start
	mux          *dynamicServeMux
	httpServer   *http.Server
	// previewMux and previewServer serve only /serve/ and /dev/ routes on the
	// preview listener (FR-001, FR-005). Nil when preview is disabled.
	previewMux        *dynamicServeMux
	previewServer     *http.Server
	mu                sync.RWMutex
	placeholders      sync.Map           // "channel:chatID" → placeholderID (string)
	typingStops       sync.Map           // "channel:chatID" → func()
	reactionUndos     sync.Map           // "channel:chatID" → reactionEntry
	streamActive      sync.Map           // "channel:chatID" → true (set when streamer.Finalize sent the message)
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

	if err := m.initChannels(&cfg.Channels); err != nil {
		return nil, err
	}

	// Store initial config hashes for all channels
	m.channelHashes = toChannelHashes(cfg)

	return m, nil
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
		onFinalize: func() { m.streamActive.Store(key, true) },
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

// initChannel is a helper that looks up a factory by name and creates the channel.
// It returns an error if the factory is not registered or construction fails.
func (m *Manager) initChannel(name, displayName string) error {
	f, ok := getFactory(name)
	if !ok {
		return fmt.Errorf("factory not registered for channel %q", displayName)
	}
	logger.DebugCF("channels", "Attempting to initialize channel", map[string]any{
		"channel": displayName,
	})
	ch, err := f(m.config, m.secrets, m.bus)
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
	// the channel name so the observer can tag which channel emitted.
	if m.pairingObserver != nil {
		if obs, ok := ch.(PairingObservable); ok {
			chName := name
			obs.SetPairingObserver(func(status PairingStatus, qr, message string) {
				m.pairingObserver(chName, status, qr, message)
			})
		}
	}
	m.channels[name] = ch
	logger.InfoCF("channels", "Channel enabled successfully", map[string]any{
		"channel": displayName,
	})
	return nil
}

// recordChannelFailure records a failed enabled-channel init and logs it.
func (m *Manager) recordChannelFailure(name, displayName string, err error) {
	m.failedChannels = append(m.failedChannels, ChannelInitError{Channel: displayName, Err: err})
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

func (m *Manager) initChannels(channels *config.ChannelsConfig) error {
	logger.InfoC("channels", "Initializing channel manager")

	if channels.Telegram.Enabled && channels.Telegram.TokenRef != "" {
		if err := m.initChannel("telegram", "Telegram"); err != nil {
			m.recordChannelFailure("telegram", "Telegram", err)
		}
	}

	if channels.WhatsApp.Enabled {
		waCfg := channels.WhatsApp
		if waCfg.UseNative {
			if err := m.initChannel("whatsapp_native", "WhatsApp Native"); err != nil {
				m.recordChannelFailure("whatsapp_native", "WhatsApp Native", err)
			}
		} else if waCfg.BridgeURL != "" {
			if err := m.initChannel("whatsapp", "WhatsApp"); err != nil {
				m.recordChannelFailure("whatsapp", "WhatsApp", err)
			}
		}
	}

	if channels.Feishu.Enabled {
		if err := m.initChannel("feishu", "Feishu"); err != nil {
			m.recordChannelFailure("feishu", "Feishu", err)
		}
	}

	if channels.Discord.Enabled && channels.Discord.TokenRef != "" {
		if err := m.initChannel("discord", "Discord"); err != nil {
			m.recordChannelFailure("discord", "Discord", err)
		}
	}

	if channels.QQ.Enabled {
		if err := m.initChannel("qq", "QQ"); err != nil {
			m.recordChannelFailure("qq", "QQ", err)
		}
	}

	if channels.DingTalk.Enabled && channels.DingTalk.ClientID != "" {
		if err := m.initChannel("dingtalk", "DingTalk"); err != nil {
			m.recordChannelFailure("dingtalk", "DingTalk", err)
		}
	}

	if channels.Slack.Enabled && channels.Slack.BotTokenRef != "" {
		if err := m.initChannel("slack", "Slack"); err != nil {
			m.recordChannelFailure("slack", "Slack", err)
		}
	}

	if channels.Matrix.Enabled &&
		m.config.Channels.Matrix.Homeserver != "" &&
		m.config.Channels.Matrix.UserID != "" &&
		m.config.Channels.Matrix.AccessTokenRef != "" {
		if err := m.initChannel("matrix", "Matrix"); err != nil {
			m.recordChannelFailure("matrix", "Matrix", err)
		}
	}

	if channels.LINE.Enabled && channels.LINE.ChannelAccessTokenRef != "" {
		if err := m.initChannel("line", "LINE"); err != nil {
			m.recordChannelFailure("line", "LINE", err)
		}
	}

	if channels.OneBot.Enabled && channels.OneBot.WSUrl != "" {
		if err := m.initChannel("onebot", "OneBot"); err != nil {
			m.recordChannelFailure("onebot", "OneBot", err)
		}
	}

	if channels.WeCom.Enabled && channels.WeCom.BotID != "" && channels.WeCom.SecretRef != "" {
		if err := m.initChannel("wecom", "WeCom"); err != nil {
			m.recordChannelFailure("wecom", "WeCom", err)
		}
	}

	if channels.Weixin.Enabled && channels.Weixin.TokenRef != "" {
		if err := m.initChannel("weixin", "Weixin"); err != nil {
			m.recordChannelFailure("weixin", "Weixin", err)
		}
	}

	if channels.IRC.Enabled && channels.IRC.Server != "" {
		if err := m.initChannel("irc", "IRC"); err != nil {
			m.recordChannelFailure("irc", "IRC", err)
		}
	}

	if channels.GoogleChat.Enabled &&
		(channels.GoogleChat.WebhookURL.String() != "" ||
			channels.GoogleChat.ServiceAccountJSON.String() != "" ||
			channels.GoogleChat.ServiceAccountFile != "") {
		if err := m.initChannel("google-chat", "Google Chat"); err != nil {
			m.recordChannelFailure("google-chat", "Google Chat", err)
		}
	}

	logger.InfoCF("channels", "Channel initialization completed", map[string]any{
		"enabled_channels": len(m.channels),
		"failed_channels":  len(m.failedChannels),
	})

	// If any enabled channel failed to construct, abort boot per deny-by-default policy.
	if len(m.failedChannels) > 0 {
		joinedErrs := make([]error, 0, len(m.failedChannels))
		for i := range m.failedChannels {
			joinedErrs = append(joinedErrs, &m.failedChannels[i])
		}
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

// SetupPreviewServer creates a dedicated HTTP server for the preview listener.
// It owns a separate mux that registers only /serve/ and /dev/ routes — all
// other paths return 404. Must be called after SetupHTTPServer and before
// StartAll. Call RegisterPreviewHandler to add route handlers to this mux.
//
// This mirrors SetupHTTPServer but intentionally does NOT register health
// endpoints (the preview listener is not a health-check surface).
func (m *Manager) SetupPreviewServer(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.previewMux = newDynamicServeMux()
	m.previewServer = &http.Server{
		Addr:         addr,
		Handler:      m.previewMux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
}

// RegisterPreviewHandler registers an HTTP handler at the given path pattern
// on the preview mux. Must be called after SetupPreviewServer.
// Only /serve/ and /dev/ prefixes should be registered here (FR-005).
func (m *Manager) RegisterPreviewHandler(pattern string, handler http.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.previewMux != nil {
		m.previewMux.Handle(pattern, handler)
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

// WrapPreviewHandler wraps the preview server's root handler with the given
// middleware. Must be called after SetupPreviewServer and before StartAll.
// Mirrors WrapHTTPHandler for the preview listener so callers can inject
// the same cross-cutting middleware (e.g. configSnapshotMiddleware) on both
// servers. Without this, handlers on the preview mux that call
// configFromContext(r.Context()) would receive nil and fall back to a torn
// live read of the config pointer during hot-reload (F-13).
func (m *Manager) WrapPreviewHandler(middleware func(http.Handler) http.Handler) error {
	if m.previewServer == nil || m.previewServer.Handler == nil {
		return fmt.Errorf("WrapPreviewHandler: previewServer not initialized")
	}
	m.previewServer.Handler = middleware(m.previewServer.Handler)
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

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.channels) == 0 {
		logger.WarnC("channels", "No channels enabled")
	}

	logger.InfoC("channels", "Starting all channels")

	dispatchCtx, cancel := context.WithCancel(ctx)
	m.dispatchTask = &asyncTask{cancel: cancel}
	m.dispatchCtx = dispatchCtx

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
		// Lazily create worker only after channel starts successfully
		w := newChannelWorker(name, channel)
		m.workers[name] = w
		go m.runWorker(dispatchCtx, name, w)
		go m.runMediaWorker(dispatchCtx, name, w)
		startedCount++
	}

	if configuredCount > 0 && startedCount == 0 {
		cancel()
		return fmt.Errorf("channels: all %d configured channels failed to start", configuredCount)
	}

	// Start the dispatcher that reads from the bus and routes to workers
	go m.dispatchOutbound(dispatchCtx)
	go m.dispatchOutboundMedia(dispatchCtx)

	// Start the TTL janitor that cleans up stale typing/placeholder entries
	go m.runTTLJanitor(dispatchCtx)

	// Start shared HTTP server if configured
	if m.httpServer != nil {
		go func() {
			logger.InfoCF("channels", "Shared HTTP server listening", map[string]any{
				"addr": m.httpServer.Addr,
			})
			if err := m.httpServer.ListenAndServe(); err != nil &&
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

	// Start the preview listener if configured (FR-001, FR-020).
	if m.previewServer != nil {
		go func() {
			logger.InfoCF("channels", "Preview HTTP server listening", map[string]any{
				"addr": m.previewServer.Addr,
			})
			if err := m.previewServer.ListenAndServe(); err != nil &&
				!errors.Is(err, http.ErrServerClosed) &&
				!errors.Is(err, net.ErrClosed) {
				// Log at Error and return — do NOT call os.Exit here.
				// Same rationale as the shared HTTP server goroutine above.
				logger.ErrorCF("channels", "Preview HTTP server error — server stopped unexpectedly", map[string]any{
					"error": err.Error(),
				})
			}
		}()
	}

	logger.InfoC("channels", "All channels started")

	// C1 fix: invoke RegisterCommands on every started channel that implements
	// CommandRegistrarCapable (FR-28 fallback: failures are WARN-logged, channel
	// startup continues). This registers /cancel and other builtins with the
	// upstream platform (Telegram BotCommand, Slack slash commands, etc.) so they
	// appear in the platform's command menu.
	defs := commands.BuiltinDefinitions()
	for name, ch := range m.channels {
		if reg, ok := ch.(CommandRegistrarCapable); ok {
			name, reg := name, reg // capture for goroutine
			go func() {
				regCtx, regCancel := context.WithTimeout(dispatchCtx, 30*time.Second)
				defer regCancel()
				if err := reg.RegisterCommands(regCtx, defs); err != nil {
					logger.WarnCF("channels", "RegisterCommands failed — platform command menu not updated",
						map[string]any{
							"channel": name,
							"error":   err.Error(),
						})
				} else {
					logger.InfoCF("channels", "RegisterCommands succeeded",
						map[string]any{"channel": name, "command_count": len(defs)})
				}
			}()
		}
	}

	return nil
}

func (m *Manager) StopAll(ctx context.Context) error {
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

	// Shutdown preview listener (FR-001).
	if m.previewServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := m.previewServer.Shutdown(shutdownCtx); err != nil {
			logger.ErrorCF("channels", "Preview HTTP server shutdown error", map[string]any{
				"error": err.Error(),
			})
		}
		m.previewServer = nil
	}

	// Cancel dispatcher
	if m.dispatchTask != nil {
		m.dispatchTask.cancel()
		m.dispatchTask = nil
	}

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

// newChannelWorker creates a channelWorker with a rate limiter configured
// for the given channel name.
func newChannelWorker(name string, ch Channel) *channelWorker {
	rateVal := float64(defaultRateLimit)
	if r, ok := channelRateConfig[name]; ok {
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
func (m *Manager) runWorker(ctx context.Context, name string, w *channelWorker) {
	defer close(w.done)
	for {
		select {
		case msg, ok := <-w.queue:
			if !ok {
				return
			}
			maxLen := 0
			if mlp, ok := w.ch.(MessageLengthProvider); ok {
				maxLen = mlp.MaxMessageLength()
			}

			// Collect all message chunks to send
			var chunks []string

			// Step 1: Try marker-based splitting if enabled
			if m.config != nil && m.config.Agents.Defaults.SplitOnMarker {
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
				m.sendWithRetry(ctx, name, w, chunkMsg)
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
func (m *Manager) sendWithRetry(ctx context.Context, name string, w *channelWorker, msg bus.OutboundMessage) {
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
				if !enqueue(ctx, w, msg) {
					return
				}
			} else if exists {
				logger.WarnCF("channels", noWorkerMsg, map[string]any{"channel": channel})
			}
		}
	}
}

func (m *Manager) dispatchOutbound(ctx context.Context) {
	dispatchLoop(
		ctx, m,
		m.bus.OutboundChan(),
		func(msg bus.OutboundMessage) string { return msg.Channel },
		func(_ context.Context, w *channelWorker, msg bus.OutboundMessage) bool {
			select {
			case w.queue <- msg:
				return true
			default:
				logger.ErrorCF("channels", "Outbound queue full, message dropped",
					map[string]any{"channel": msg.Channel, "chat_id": msg.ChatID})
				return true
			}
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
		func(_ context.Context, w *channelWorker, msg bus.OutboundMediaMessage) bool {
			select {
			case w.mediaQueue <- msg:
				return true
			default:
				logger.ErrorCF("channels", "Outbound media queue full, message dropped",
					map[string]any{"channel": msg.Channel, "chat_id": msg.ChatID})
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

// runTTLJanitor periodically scans the typingStops and placeholders maps
// and evicts entries that have exceeded their TTL. This prevents memory
// accumulation when outbound paths fail to trigger preSend (e.g. LLM errors).
func (m *Manager) runTTLJanitor(ctx context.Context) {
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
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
		}
	}
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
	if m.dispatchTask != nil {
		m.dispatchTask.cancel()
	}

	// Fix 14 (removed loop): capture loop variable with a local copy to avoid Go closure capture bug.
	deferFuncs := make([]func(), 0, len(removed)+len(added))
	for _, name := range removed {
		n := name // local copy — prevents closure from capturing the loop variable
		// Stop all channels
		channel := m.channels[n]
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
	dispatchCtx, cancel := context.WithCancel(ctx)
	m.dispatchTask = &asyncTask{cancel: cancel}
	cc, err := toChannelConfig(cfg, added)
	if err != nil {
		logger.ErrorC("channels", fmt.Sprintf("toChannelConfig error: %v", err))
		m.config = oldConfig
		m.secrets = oldSecrets
		cancel()
		return err
	}
	// Clear failed channels before re-init so that channels that previously
	// failed but now succeed are no longer reported as degraded.
	m.failedChannels = m.failedChannels[:0]
	err = m.initChannels(cc)
	if err != nil {
		logger.ErrorC("channels", fmt.Sprintf("initChannels error: %v", err))
		m.config = oldConfig
		m.secrets = oldSecrets
		cancel()
		return err
	}
	// Fix 14 (added loop): capture loop variables with local copies.
	for _, name := range added {
		n := name // local copy — prevents closure from capturing the loop variable
		channel := m.channels[n]
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
		// Lazily create worker only after channel starts successfully
		w := newChannelWorker(n, channel)
		m.workers[n] = w
		go m.runWorker(dispatchCtx, n, w)
		go m.runMediaWorker(dispatchCtx, n, w)
		deferFuncs = append(deferFuncs, func() {
			m.registerChannelLocked(n, channel)
		})
	}

	// Commit hashes only for successfully started channels.
	m.channelHashes = list

	// Restart dispatch goroutines against the new dispatchCtx so outbound messages
	// continue to be routed after the old context was canceled above.
	go m.dispatchOutbound(dispatchCtx)
	go m.dispatchOutboundMedia(dispatchCtx)

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
		go m.runWorker(dispatchCtx, name, w)
		go m.runMediaWorker(dispatchCtx, name, w)
	}

	// Fix 15: execute deferFuncs synchronously after releasing the lock, not in a goroutine.
	// Running them in a goroutine while the mutex is still held causes a deadlock;
	// running them in a goroutine after unlock races with subsequent Reload calls.
	// We must manually unlock here so that RegisterChannel/UnregisterChannel can acquire the lock.
	m.mu.Unlock()
	for _, f := range deferFuncs {
		f()
	}
	// Re-acquire so that the deferred m.mu.Unlock() at the top of the function is balanced.
	m.mu.Lock()
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
		w := newChannelWorker(name, channel)
		m.workers[name] = w
		go m.runWorker(m.dispatchCtx, name, w)
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
	if maxLen > 0 && len([]rune(msg.Content)) > maxLen {
		for _, chunk := range SplitMessage(msg.Content, maxLen) {
			chunkMsg := msg
			chunkMsg.Content = chunk
			m.sendWithRetry(ctx, msg.Channel, w, chunkMsg)
		}
	} else {
		m.sendWithRetry(ctx, msg.Channel, w, msg)
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
