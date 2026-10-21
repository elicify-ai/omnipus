package session

import (
	"context"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// placeRetentionNoticeModelOnly regenerates only fixed operational context
// after its original UI copy legitimately expired before admission. The caller
// is the existing allowed consumption fence; this never revives a stopped turn.
func (b *archiveBackend) placeRetentionNoticeModelOnly(ctx context.Context, key string, e savedInputExpiration) (ModelSlot, WindowView, error) {
	if err := ctx.Err(); err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.store(key)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	state, err := loadSavedInputLifecycle(store.dir(), store.sessionID)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	notice, ok := state.Notices[e.ReceiptID]
	if !ok || notice.Source == nil || !state.retiredNoticeSource(*notice.Source) {
		return ModelSlot{}, WindowView{}, fmt.Errorf("retention notice has no authorized retired control source")
	}
	admitted, _, err := store.retentionNoticeAdmitted(notice)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	if admitted {
		return ModelSlot{}, WindowView{}, ErrAlreadyConsumed
	}
	body := savedInputExpiryCopy(store.sessionID, e)
	message := providers.Message{Role: "user", Content: body}
	payload, err := EncodeModelPayload(message)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	addr, row, err := store.AppendIndexed(ArchiveRecord{
		TranscriptEntry: TranscriptEntry{ID: "retention-model:" + e.ReceiptID, ViewMembership: ViewMembershipModel, Timestamp: time.Now().UTC()},
		ModelMessage:    &payload, Source: &EntrySource{Kind: "system"},
	})
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	meta.Count = row.Ordinal + 1
	if err := b.saveMetaLocked(key, meta); err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	view, err := b.viewLocked(key)
	return ModelSlot{Ordinal: row.Ordinal, Addr: addr, Message: message, TS: row.TS, UserTurn: row.UserTurn}, view, err
}

func (us *UnifiedStore) acknowledgeRetentionNoticeLocked(sessionID string, source ArchiveAddress) error {
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return err
	}
	state, err := loadSavedInputLifecycle(archive.dir(), sessionID)
	if err != nil {
		return err
	}
	for receiptID, notice := range state.Notices {
		if notice.Source == nil || *notice.Source != source {
			continue
		}
		admitted, _, err := archive.retentionNoticeAdmitted(notice)
		if err != nil {
			return err
		}
		if !admitted {
			return fmt.Errorf("retention notice %q has no durable model admission", receiptID)
		}
		if err := archive.updateRetentionNotice(receiptID, func(n *savedInputNotice) { n.ModelAdmitted = true }); err != nil {
			return err
		}
		archive.observeWriteStep("expiry.notice_admitted")
		return nil
	}
	return nil
}
