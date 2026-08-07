// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// configGetter returns the current gateway config (thread-safe).
type configGetter func() *config.Config

// SSEHandler handles the `/api/v1/chat` endpoint, streaming LLM response tokens
// to the browser as Server-Sent Events (SSE).
//
// Request:  POST /api/v1/chat   Content-Type: application/json
//
//	{"message": "Hello, world!"}
//
// Response: text/event-stream
//
//	event: token
//	data: {"content":"Hello"}
//
//	event: token
//	data: {"content":", world"}
//
//	event: done
//	data: {}
//
// Implements US-8 acceptance criteria.
type SSEHandler struct {
	msgBus        *bus.MessageBus
	partitions    *session.PartitionStore // may be nil before Wave 2 full wiring
	allowedOrigin string                  // CORS allowed origin (localhost:port)
	cfg           configGetter            // returns the current config for auth checks
	mu            sync.Mutex
	sessions      map[string]*sseSession // chatID → session
}

type sseSession struct {
	ch          chan string // token chunks
	doneCh      chan struct{}
	closeOnce   sync.Once // guards close(doneCh)
	chCloseOnce sync.Once // guards close(ch)
}

func newSSEHandler(
	msgBus *bus.MessageBus,
	ps *session.PartitionStore,
	allowedOrigin string,
	cfg func() *config.Config,
) *SSEHandler {
	h := &SSEHandler{
		msgBus:        msgBus,
		partitions:    nil, // always nil — SSE is legacy; use WebSocket for persistent sessions
		allowedOrigin: allowedOrigin,
		cfg:           cfg,
		sessions:      make(map[string]*sseSession),
	}
	slog.Info("sse: session recording disabled (use WebSocket for persistent sessions)")
	// NOTE: Do NOT call msgBus.SetStreamDelegate(h) here.
	// The WebSocket handler (Wave 5a) is the primary stream delegate.
	// SSE is kept for backward compatibility. It is not registered as the bus stream
	// delegate (the channel Manager handles that), so it won't receive streaming tokens
	// via the bus. GetStreamer is retained for potential direct-call use or future fallback.
	return h
}

// GetStreamer implements bus.StreamDelegate.
// Returns a token-streaming Streamer for webchat SSE sessions.
func (h *SSEHandler) GetStreamer(ctx context.Context, channel, chatID, _ string) (bus.Streamer, bool) {
	if channel != "webchat" {
		return nil, false
	}
	h.mu.Lock()
	sess, ok := h.sessions[chatID]
	h.mu.Unlock()
	if !ok {
		return nil, false
	}
	return &sseStreamer{sess: sess}, true
}

// ServeHTTP handles POST /api/v1/chat and OPTIONS preflight.
func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := h.allowedOrigin
	if origin == "" {
		origin = "http://localhost:5000"
	}

	// Handle CORS preflight before auth or body parsing.
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !checkBearerAuth(r.Context(), w, r, h.cfg()).Authenticated {
		return
	}

	// Restrict CORS to the configured origin (localhost only by default).
	w.Header().Set("Access-Control-Allow-Origin", origin)

	var body gen.SseChatRequest
	cfg := h.cfg()
	validateEnabled := cfg != nil && cfg.Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SseChatRequest", &body, validateEnabled) {
		return
	}
	if body.Message == "" {
		http.Error(w, "message must not be empty", http.StatusBadRequest)
		return
	}

	chatID := "webchat:" + uuid.New().String()
	sess := &sseSession{
		ch:     make(chan string, 128),
		doneCh: make(chan struct{}),
	}
	h.mu.Lock()
	h.sessions[chatID] = sess
	h.mu.Unlock()
	// defer runs even if a panic propagates from the streaming goroutine below,
	// since panics in spawned goroutines propagate to the HTTP handler's defer chain.
	defer func() {
		h.mu.Lock()
		delete(h.sessions, chatID)
		h.mu.Unlock()
	}()

	// Record user message to PartitionStore if available (US-5).
	var sessionID string
	if h.partitions != nil {
		meta, err := h.partitions.NewSession("webchat", "", "")
		if err != nil {
			slog.Warn("sse: could not create session", "error", err)
		} else {
			sessionID = meta.ID
			userEntry := session.TranscriptEntry{
				ID:        uuid.New().String(),
				Role:      "user",
				Content:   body.Message,
				Timestamp: time.Now().UTC(),
			}
			if err := h.partitions.AppendMessage(sessionID, userEntry); err != nil {
				slog.Warn("sse: could not record user message", "session_id", sessionID, "error", err)
			}
		}
	}

	// Check flusher support before committing headers — no error responses possible after WriteHeader.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers and commit — no error responses possible after this point.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Publish the inbound message to the bus.
	msg := bus.InboundMessage{
		Channel: "webchat",
		Sender: bus.SenderInfo{
			CanonicalID: "webchat_user",
		},
		ChatID:  chatID,
		Content: body.Message,
	}
	if err := h.msgBus.PublishInbound(r.Context(), msg); err != nil {
		writeSSEEvent(w, "error", map[string]string{"error": err.Error()})
		flusher.Flush()
		return
	}

	// Collect full response for PartitionStore recording.
	var responseBuilder strings.Builder

	// Stream tokens as SSE events.
	for {
		select {
		case token, more := <-sess.ch:
			if !more {
				h.recordAssistantMessage(sessionID, responseBuilder.String())
				writeSSEEvent(w, "done", struct{}{})
				flusher.Flush()
				return
			}
			responseBuilder.WriteString(token)
			writeSSEEvent(w, "token", map[string]string{"content": token})
			flusher.Flush()
		case <-sess.doneCh:
			h.recordAssistantMessage(sessionID, responseBuilder.String())
			writeSSEEvent(w, "done", struct{}{})
			flusher.Flush()
			return
		case <-r.Context().Done():
			// FR-019/020: save partial response as "interrupted" when client disconnects
			// or the request context is canceled (e.g., server shutdown).
			if partial := responseBuilder.String(); partial != "" {
				h.recordAssistantMessageStatus(sessionID, partial, "interrupted")
			} else if sessionID != "" && h.partitions != nil {
				if err := h.partitions.SetStatus(sessionID, session.StatusInterrupted); err != nil {
					slog.Warn("sse: could not set session status interrupted",
						"session_id", sessionID, "error", err)
				}
			}
			slog.Debug("sse: client disconnected — partial response saved",
				"chat_id", chatID, "session_id", sessionID)
			return
		}
	}
}

// recordAssistantMessage persists a successfully completed assistant response.
func (h *SSEHandler) recordAssistantMessage(sessionID, content string) {
	h.recordAssistantMessageStatus(sessionID, content, "ok")
}

// recordAssistantMessageStatus persists an assistant response with the given status.
// status should be "ok", "interrupted", or "error".
func (h *SSEHandler) recordAssistantMessageStatus(sessionID, content, status string) {
	if h.partitions == nil || sessionID == "" || content == "" {
		return
	}
	entry := session.TranscriptEntry{
		ID:        uuid.New().String(),
		Role:      "assistant",
		Content:   content,
		Timestamp: time.Now().UTC(),
		Status:    status,
	}
	if err := h.partitions.AppendMessage(sessionID, entry); err != nil {
		slog.Warn("sse: could not record assistant message", "session_id", sessionID, "error", err)
	}
}

func writeSSEEvent(w http.ResponseWriter, event string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		slog.Error("sse: failed to marshal event data", "event", event, "error", err)
		raw = []byte(`{"error":"internal marshal failure"}`)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
}

// sseStreamer implements bus.Streamer by pushing tokens into the session channel.
type sseStreamer struct {
	sess *sseSession
}

func (s *sseStreamer) Update(_ context.Context, content string) error {
	select {
	case s.sess.ch <- content:
	default:
		slog.Warn("sse: token dropped — client buffer full")
		return fmt.Errorf("sse: token channel full, token dropped")
	}
	return nil
}

func (s *sseStreamer) Finalize(_ context.Context, _ string) error {
	s.sess.chCloseOnce.Do(func() { close(s.sess.ch) })
	return nil
}

func (s *sseStreamer) Cancel(_ context.Context) {
	s.sess.closeOnce.Do(func() { close(s.sess.doneCh) })
	// Drain any buffered tokens so goroutines blocked on ch send can unblock.
	for {
		select {
		case <-s.sess.ch:
		default:
			return
		}
	}
}
