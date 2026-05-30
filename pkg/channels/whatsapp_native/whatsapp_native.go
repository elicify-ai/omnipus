//go:build whatsapp_native

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

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/identity"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/utils"
)

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
	logger.InfoCF("whatsapp", "Starting WhatsApp native channel (whatsmeow)", map[string]any{"store": c.storePath})

	// Reset lifecycle state from any previous Stop() so a restarted channel
	// behaves correctly.  Use reconnectMu to be consistent with eventHandler
	// and Stop() which coordinate under the same lock.
	c.reconnectMu.Lock()
	c.stopping.Store(false)
	c.reconnecting = false
	c.reconnectMu.Unlock()

	if err := os.MkdirAll(c.storePath, 0o700); err != nil {
		return fmt.Errorf("create session store dir: %w", err)
	}

	dbPath := filepath.Join(c.storePath, whatsappDBName)
	connStr := "file:" + dbPath + "?_foreign_keys=on"

	db, err := sql.Open(sqliteDriver, connStr)
	if err != nil {
		return fmt.Errorf("open whatsapp store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err = db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return fmt.Errorf("enable foreign keys: %w", err)
	}

	waLogger := waLog.Stdout("WhatsApp", "WARN", true)
	container := sqlstore.NewWithDB(db, sqliteDriver, waLogger)
	if err = container.Upgrade(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("open whatsapp store: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return fmt.Errorf("get device store: %w", err)
	}

	client := whatsmeow.NewClient(deviceStore, waLogger)

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

	if client.Store.ID == nil {
		qrChan, err := client.GetQRChannel(c.runCtx)
		if err != nil {
			return fmt.Errorf("get QR channel: %w", err)
		}
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		// Handle QR events in a background goroutine so Start() returns
		// promptly.  The goroutine is tracked via c.wg and respects
		// c.runCtx for cancellation.
		// Guard wg.Add with reconnectMu + stopping check (same protocol
		// as eventHandler) so a concurrent Stop() cannot enter wg.Wait()
		// while we call wg.Add(1).
		c.reconnectMu.Lock()
		if c.stopping.Load() {
			c.reconnectMu.Unlock()
			return fmt.Errorf("channel stopped during QR setup")
		}
		c.wg.Add(1)
		c.reconnectMu.Unlock()
		go func() {
			defer c.wg.Done()
			for {
				select {
				case <-c.runCtx.Done():
					return
				case evt, ok := <-qrChan:
					if !ok {
						return
					}
					if evt.Event == "code" {
						logger.InfoCF("whatsapp", "Scan this QR code with WhatsApp (Linked Devices):", nil)
						qrterminal.GenerateWithConfig(evt.Code, qrterminal.Config{
							Level:      qrterminal.L,
							Writer:     os.Stdout,
							HalfBlocks: true,
						})
						// Emit structured log for headless / WebUI consumption (US-2.5)
						logger.InfoCF("whatsapp", "QR code available", map[string]any{
							"event": "whatsapp.qr_code",
							"code":  evt.Code,
						})
					} else {
						logger.InfoCF("whatsapp", "WhatsApp login event", map[string]any{"event": evt.Event})
					}
				}
			}
		}()
	} else {
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
	}

	startOK = true
	c.SetRunning(true)
	logger.InfoC("whatsapp", "WhatsApp native channel connected")
	return nil
}

func (c *WhatsAppNativeChannel) Stop(ctx context.Context) error {
	logger.InfoC("whatsapp", "Stopping WhatsApp native channel")

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
	return nil
}

func (c *WhatsAppNativeChannel) eventHandler(evt any) {
	switch evt.(type) {
	case *events.Message:
		c.handleIncoming(evt.(*events.Message))
	case *events.LoggedOut:
		logger.WarnCF("whatsapp", "WhatsApp session logged out by server; re-pairing required", map[string]any{
			"on_connect": evt.(*events.LoggedOut).OnConnect,
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
	if content == "" && evt.Message.ExtendedTextMessage != nil {
		content = evt.Message.ExtendedTextMessage.GetText()
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
	if channels.DispatchCancelIfRecognized(c.runCtx, content, "whatsapp_native", chatID, senderID, c.GetCancelInterceptor(), channels.CancelSendFn(c)) {
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
		return fmt.Errorf("whatsapp send: %v: %w", err, channels.ErrTemporary)
	}
	return nil
}

// handleLoggedOut clears the current session and starts QR re-pairing (US-2).
// Must be called from a goroutine tracked by c.wg, with c.reconnecting already set.
func (c *WhatsAppNativeChannel) handleLoggedOut() {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
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
	err := client.Store.Delete(deleteCtx)
	cancel()
	if err != nil {
		logger.ErrorCF("whatsapp", "Failed to clear session for re-pairing", map[string]any{"error": err.Error()})
		return
	}

	qrChan, err := client.GetQRChannel(c.runCtx)
	if err != nil {
		logger.ErrorCF("whatsapp", "Failed to get QR channel for re-pairing", map[string]any{"error": err.Error()})
		return
	}
	if err := client.Connect(); err != nil {
		logger.ErrorCF("whatsapp", "Failed to connect for re-pairing", map[string]any{"error": err.Error()})
		return
	}

	for {
		select {
		case <-c.runCtx.Done():
			return
		case evt, ok := <-qrChan:
			if !ok {
				return
			}
			if evt.Event == "code" {
				logger.InfoCF("whatsapp", "Scan QR code to re-pair WhatsApp (Linked Devices):", nil)
				qrterminal.GenerateWithConfig(evt.Code, qrterminal.Config{
					Level:      qrterminal.L,
					Writer:     os.Stdout,
					HalfBlocks: true,
				})
				logger.InfoCF("whatsapp", "QR code available", map[string]any{
					"event": "whatsapp.qr_code",
					"code":  evt.Code,
				})
			} else {
				logger.InfoCF("whatsapp", "WhatsApp re-pairing event", map[string]any{"event": evt.Event})
			}
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

	if evt.Message.ExtendedTextMessage != nil {
		ci := evt.Message.ExtendedTextMessage.GetContextInfo()
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
