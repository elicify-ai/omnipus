package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/identity"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/utils"
)

type WhatsAppChannel struct {
	*channels.BaseChannel
	conn      *websocket.Conn
	config    config.WhatsAppConfig
	url       string
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	connected bool
}

func NewWhatsAppChannel(cfg config.WhatsAppConfig, bus *bus.MessageBus) (*WhatsAppChannel, error) {
	base := channels.NewBaseChannel(
		"whatsapp",
		cfg,
		bus,
		cfg.AllowFrom,
		channels.WithMaxMessageLength(65536),
		channels.WithReasoningChannelID(cfg.ReasoningChannelID),
	)

	return &WhatsAppChannel{
		BaseChannel: base,
		config:      cfg,
		url:         cfg.BridgeURL,
		connected:   false,
	}, nil
}

func (c *WhatsAppChannel) Start(ctx context.Context) error {
	logger.InfoCF("whatsapp", "Starting WhatsApp channel", map[string]any{
		"bridge_url": c.url,
	})

	c.ctx, c.cancel = context.WithCancel(ctx)

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, resp, err := dialer.Dial(c.url, nil)
	if resp != nil {
		resp.Body.Close()
	}
	if err != nil {
		c.cancel()
		return fmt.Errorf("failed to connect to WhatsApp bridge: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	c.SetRunning(true)
	logger.InfoC("whatsapp", "WhatsApp channel connected")

	go c.listen()

	return nil
}

func (c *WhatsAppChannel) Stop(ctx context.Context) error {
	logger.InfoC("whatsapp", "Stopping WhatsApp channel...")

	// Cancel context first to signal listen goroutine to exit
	if c.cancel != nil {
		c.cancel()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			logger.ErrorCF("whatsapp", "Error closing WhatsApp connection", map[string]any{
				"error": err.Error(),
			})
		}
		c.conn = nil
	}

	c.connected = false
	c.SetRunning(false)

	return nil
}

func (c *WhatsAppChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}

	// Check ctx before acquiring lock
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("whatsapp connection not established: %w", channels.ErrTemporary)
	}

	payload := map[string]any{
		"type":    "message",
		"to":      msg.ChatID,
		"content": msg.Content,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		_ = c.conn.SetWriteDeadline(time.Time{})
		return fmt.Errorf("whatsapp send: %w: %v", channels.ErrTemporary, err)
	}
	_ = c.conn.SetWriteDeadline(time.Time{})

	return nil
}

func (c *WhatsAppChannel) listen() {
	const maxReconnectAttempts = 10
	reconnectAttempts := 0

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()

			if conn == nil {
				time.Sleep(1 * time.Second)
				continue
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				if c.ctx.Err() != nil {
					// Context canceled — exit cleanly.
					return
				}
				logger.ErrorCF("whatsapp", "WhatsApp read error — attempting reconnect", map[string]any{
					"error":   err.Error(),
					"attempt": reconnectAttempts + 1,
				})

				if reconnectAttempts >= maxReconnectAttempts {
					logger.ErrorCF("whatsapp", "WhatsApp max reconnect attempts reached, giving up", map[string]any{
						"max_attempts": maxReconnectAttempts,
					})
					return
				}

				// Close stale connection.
				c.mu.Lock()
				if c.conn != nil {
					_ = c.conn.Close()
					c.conn = nil
					c.connected = false
				}
				c.mu.Unlock()

				// Back off before reconnecting.
				delay := time.Duration(reconnectAttempts+1) * 2 * time.Second
				select {
				case <-c.ctx.Done():
					return
				case <-time.After(delay):
				}

				// Attempt to reconnect.
				dialer := websocket.DefaultDialer
				dialer.HandshakeTimeout = 10 * time.Second
				newConn, resp, dialErr := dialer.Dial(c.url, nil)
				if resp != nil {
					resp.Body.Close()
				}
				if dialErr != nil {
					logger.ErrorCF("whatsapp", "WhatsApp reconnect failed", map[string]any{
						"error":   dialErr.Error(),
						"attempt": reconnectAttempts + 1,
					})
					reconnectAttempts++
					continue
				}

				c.mu.Lock()
				c.conn = newConn
				c.connected = true
				c.mu.Unlock()

				logger.InfoCF("whatsapp", "WhatsApp reconnected", map[string]any{
					"attempt": reconnectAttempts + 1,
				})
				reconnectAttempts = 0
				continue
			}
			reconnectAttempts = 0

			var msg map[string]any
			if err := json.Unmarshal(message, &msg); err != nil {
				logger.ErrorCF("whatsapp", "Failed to unmarshal WhatsApp message", map[string]any{
					"error": err.Error(),
				})
				continue
			}

			msgType, ok := msg["type"].(string)
			if !ok {
				continue
			}

			if msgType == "message" {
				c.handleIncomingMessage(msg)
			}
		}
	}
}

func (c *WhatsAppChannel) handleIncomingMessage(msg map[string]any) {
	senderID, ok := msg["from"].(string)
	if !ok {
		return
	}

	chatID, ok := msg["chat"].(string)
	if !ok {
		chatID = senderID
	}

	content, ok := msg["content"].(string)
	if !ok {
		content = ""
	}

	var mediaPaths []string
	if mediaData, ok := msg["media"].([]any); ok {
		mediaPaths = make([]string, 0, len(mediaData))
		for _, m := range mediaData {
			if path, ok := m.(string); ok {
				mediaPaths = append(mediaPaths, path)
			}
		}
	}

	metadata := make(map[string]string)
	var messageID string
	if mid, ok := msg["id"].(string); ok {
		messageID = mid
	}
	if userName, ok := msg["from_name"].(string); ok {
		metadata["user_name"] = userName
	}

	var peer bus.Peer
	if chatID == senderID {
		peer = bus.Peer{Kind: bus.PeerDirect, ID: senderID}
	} else {
		peer = bus.Peer{Kind: bus.PeerGroup, ID: chatID}
	}

	logger.InfoCF("whatsapp", "WhatsApp message received", map[string]any{
		"sender":  senderID,
		"preview": utils.Truncate(content, 50),
	})

	sender := bus.SenderInfo{
		Platform:    "whatsapp",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("whatsapp", senderID),
	}
	if display, ok := metadata["user_name"]; ok {
		sender.DisplayName = display
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	if channels.DispatchCancelIfRecognized(
		c.ctx,
		content,
		"whatsapp",
		chatID,
		senderID,
		c.GetCancelInterceptor(),
		channels.CancelSendFn(c),
	) {
		return
	}
	c.HandleMessage(c.ctx, peer, messageID, senderID, chatID, content, mediaPaths, metadata, sender)
}
