package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RetentionNotice is internal dispatcher state, not a gateway response. Live
// delivery maps Entry to the existing generated system replay frame.
type RetentionNotice struct {
	SessionID   string
	ReceiptID   string
	OutcomeID   string
	AgentID     string
	Body        string
	Entry       TranscriptEntry
	ChatPresent bool
}

func (us *UnifiedStore) prepareRetentionNoticeInputsLocked(sessionID string) error {
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return err
	}
	state, err := loadSavedInputLifecycle(archive.dir(), sessionID)
	if err != nil {
		return err
	}
	if len(state.Notices) == 0 {
		return nil
	}
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return err
	}
	for _, receiptID := range savedInputNoticeIDs(state) {
		notice := state.Notices[receiptID]
		if notice.ModelAdmitted {
			continue
		}
		admitted, recoveredSource, err := archive.retentionNoticeAdmitted(notice)
		if err != nil {
			return err
		}
		if admitted {
			if err := archive.updateRetentionNotice(receiptID, func(n *savedInputNotice) {
				if n.Source == nil {
					n.Source = recoveredSource
				}
				n.ChatSaved, n.ModelAdmitted = true, true
			}); err != nil {
				return err
			}
			continue
		}
		if notice.Source != nil && state.retiredNoticeSource(*notice.Source) {
			continue // Regenerate model-only at an allowed consumption fence, never UI.
		}
		e, ok := state.ExpiredReportsByReceipt(receiptID)
		if !ok {
			return fmt.Errorf("retention notice %q has no expiry receipt", receiptID)
		}
		presentation := TranscriptEntry{Type: EntryTypeSystem, Role: "system", SystemSubtype: SystemSubtypeExpiryNotice, Timestamp: e.ExpiredAt}
		addr, err := us.saveChatInputLocked(sessionID, e.NoticeID, meta.AgentID, savedInputExpiryCopy(sessionID, e), &presentation)
		if err != nil {
			return err
		}
		if !notice.ChatSaved || notice.Source == nil || *notice.Source != addr {
			archive.observeWriteStep("expiry.notice_saved")
			if err := archive.updateRetentionNotice(receiptID, func(n *savedInputNotice) {
				n.Source, n.ChatSaved = &addr, true
			}); err != nil {
				return err
			}
			archive.observeWriteStep("expiry.notice_checkpointed")
		}
	}
	return nil
}

func (s *ArchiveDayStore) updateRetentionNotice(receiptID string, update func(*savedInputNotice)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := loadSavedInputLifecycle(s.dir(), s.sessionID)
	if err != nil {
		return err
	}
	n, ok := state.Notices[receiptID]
	if !ok {
		return fmt.Errorf("retention notice %q is missing", receiptID)
	}
	update(&n)
	state.Notices[receiptID] = n
	return saveSavedInputLifecycle(s.dir(), state)
}

func (s *ArchiveDayStore) retentionNoticeAdmitted(notice savedInputNotice) (bool, *ArchiveAddress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, placed, err := s.placedInputByIDLocked("saved-input-" + notice.OutcomeID)
	if err != nil {
		return false, nil, err
	}
	if placed {
		if notice.Source != nil && *notice.Source != source {
			return false, nil, fmt.Errorf("retention notice %q has conflicting published sources", notice.ReceiptID)
		}
		return true, &source, nil
	}
	n, err := s.ordinalCountLocked()
	if err != nil {
		return false, nil, err
	}
	for from := 0; from < n; from += scanChunk {
		rows, err := s.readOrdinalRowsRepairedLocked(from, min(n, from+scanChunk))
		if err != nil {
			return false, nil, err
		}
		for _, row := range rows {
			if row.Slot.EntryID == "retention-model:"+notice.ReceiptID && row.Source == nil && row.Role == "user" {
				return true, notice.Source, nil
			}
		}
	}
	return false, nil, nil
}

// PendingRetentionNotices replays storage first, then returns only owed model
// notifications. An empty sessionID is the boot/sweep dispatcher scan; ordinary
// fences pass one owning session so they never scan other chats.
func (us *UnifiedStore) PendingRetentionNotices(ctx context.Context, sessionID string) ([]RetentionNotice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sessionID == "" {
		entries, err := os.ReadDir(us.baseDir)
		if err != nil {
			return nil, err
		}
		var out []RetentionNotice
		var failures error
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if _, err := os.Stat(filepath.Join(us.baseDir, entry.Name(), savedInputLifecycleFile)); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				failures = errors.Join(failures, err)
				continue
			}
			notices, err := us.PendingRetentionNotices(ctx, entry.Name())
			out = append(out, notices...)
			failures = errors.Join(failures, err)
		}
		return out, failures
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if err := us.recoverSavedInputLifecycleLocked(sessionID); err != nil {
		return nil, err
	}
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return nil, err
	}
	state, err := loadSavedInputLifecycle(archive.dir(), sessionID)
	if err != nil {
		return nil, err
	}
	if len(state.Notices) == 0 {
		return nil, nil
	}
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return nil, err
	}
	var out []RetentionNotice
	for _, id := range savedInputNoticeIDs(state) {
		n := state.Notices[id]
		if n.ModelAdmitted {
			continue
		}
		e, _ := state.ExpiredReportsByReceipt(id)
		item := RetentionNotice{SessionID: sessionID, ReceiptID: id, OutcomeID: n.OutcomeID, AgentID: meta.AgentID, Body: savedInputExpiryCopy(sessionID, e)}
		if n.Source != nil && !state.retiredNoticeSource(*n.Source) {
			rec, err := archive.ReadAt(*n.Source)
			if err != nil {
				return nil, err
			}
			item.Entry, item.ChatPresent = rec.TranscriptEntry, true
		}
		out = append(out, item)
	}
	return out, nil
}
