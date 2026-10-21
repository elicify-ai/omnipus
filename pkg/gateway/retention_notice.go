package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func (h *WSHandler) bindRetentionNoticeDispatcher() {
	if h == nil || h.agentLoop == nil || h.agentLoop.GetSessionStore() == nil {
		return
	}
	h.agentLoop.GetSessionStore().SetRetentionNoticeDispatcher(func() error {
		return h.dispatchRetentionNotices(context.Background())
	})
}

func (h *WSHandler) dispatchRetentionNotices(ctx context.Context) error {
	if h == nil || h.agentLoop == nil {
		return nil
	}
	return h.agentLoop.DispatchRetentionNotices(ctx, h.publishRetentionNotice)
}

func (h *WSHandler) publishRetentionNotice(notice session.RetentionNotice) error {
	entry := notice.Entry
	if !notice.ChatPresent || notice.SessionID == "" || entry.Role != "system" || entry.Type != session.EntryTypeSystem ||
		entry.SystemSubtype != session.SystemSubtypeExpiryNotice || !strings.HasPrefix(entry.ID, "saved-input-retention-expiry:") || entry.Content != notice.Body {
		return fmt.Errorf("retention notice: invalid server-owned system presentation")
	}
	id, agentID := entry.ID, entry.AgentID
	// Use the same whole-second UTC display as the operational copy. The
	// receipt retains the full committed timestamp for integrity/recovery.
	timestamp := entry.Timestamp.UTC().Format(time.RFC3339)
	frame := generated.ReplayMessageFrame{Type: string(generated.WsFrameTypeReplayMessage), SessionId: notice.SessionID,
		Role: "system", Content: entry.Content, Id: &id, Timestamp: &timestamp}
	if agentID != "" {
		frame.AgentId = &agentID
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("retention notice: encode existing replay frame: %w", err)
	}
	// The common hub retains/sequences the frame even with no attached tab.
	// Repeated publications are safe through the stable persisted entry ID.
	h.hubPublishAndDeliver(notice.SessionID, string(generated.WsFrameTypeReplayMessage), data)
	return nil
}
