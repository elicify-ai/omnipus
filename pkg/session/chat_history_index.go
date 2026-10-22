package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

const chatHistoryRecoveryMaxItems = 64
const ChatHistoryIndexDirectory = ".chat-history"
const ChatHistoryIndexFormatVersion = 1

type chatHistoryReconcileResult struct {
	ObservedItems, AppliedItems, ArchiveRecords, DispositionRecords, ExpiredPartitions int
	Published                                                                          bool
	IndexGeneration, RetentionGeneration                                               string
}

// CheckChatHistoryReadiness validates the derived baseline and bounded suffix
// without preparing or claiming that a public history page was loaded. The
// gateway uses it for journal-only control catch-up.
func (us *UnifiedStore) CheckChatHistoryReadiness(ctx context.Context, sessionID string) error {
	_, err := us.reconcileChatHistoryIndex(ctx, sessionID)
	return err
}

func (us *UnifiedStore) reconcileChatHistoryIndex(ctx context.Context, sessionID string) (chatHistoryReconcileResult, error) {
	if err := validateSessionID(sessionID); err != nil {
		return chatHistoryReconcileResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return chatHistoryReconcileResult{}, err
	}
	owner := us.historyOwner(sessionID)
	owner.mu.Lock()
	defer owner.mu.Unlock()
	result, err := us.reconcileHistoryOwned(ctx, sessionID)
	owner.failure = err
	return result, err
}
func (us *UnifiedStore) initEmptyChatHistoryIndexLocked(sessionID string) error {
	dir := filepath.Join(us.baseDir, sessionID)
	// This entry point is only called by the new-identity publication path.
	// Never replace content or a derived baseline. A verified empty baseline
	// whose identity was never published can resume that same publication.
	info, err := os.Stat(filepath.Join(dir, archiveCurrentFile))
	if err == nil && info.Size() != 0 {
		return errors.New("cannot initialize history over existing content")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, indexErr := os.Lstat(historyIndexPath(dir)); indexErr == nil {
		if !info.IsDir() {
			return errors.New("invalid existing history baseline")
		}
		cp, resumeErr := historyVerifyEmptyUnpublished(dir, sessionID)
		if resumeErr != nil {
			return resumeErr
		}
		return historyPublication(chatHistoryPublicationEvent{SessionID: sessionID, Operation: "create", Point: "empty_checkpoint_ready", IndexGeneration: cp.Generation})
	} else if !errors.Is(indexErr, os.ErrNotExist) {
		return indexErr
	}
	if err = fileutilHistoryEmptyTranscript(dir); err != nil {
		return err
	}
	t, cp, err := historyCreateTree(context.Background(), dir, sessionID)
	if err != nil {
		return err
	}
	defer t.file.Close()
	if err = historyWriteFormat(dir); err != nil {
		return err
	}
	if err = historyPublish(dir, t, &cp, "create", 0); err != nil {
		return err
	}
	return historyPublication(chatHistoryPublicationEvent{SessionID: sessionID, Operation: "create", Point: "empty_checkpoint_ready", IndexGeneration: cp.Generation})
}

// Canonical acceptance and derived readiness are separate outcomes. Every
// failure below is retained on the owner and revalidated by the page/open path;
// it must never turn a successfully fsynced canonical save into an unsaved one.
func (us *UnifiedStore) historyAccepted(sessionID string, addr ArchiveAddress, rec ArchiveRecord) {
	e := chatHistoryPublicationEvent{SessionID: sessionID, Operation: "append", Point: "canonical_synced", Item: 1, Address: &addr}
	owner := us.historyOwner(sessionID)
	hookErr := historyPublication(e)
	if !owner.mu.TryLock() {
		return
	}
	defer owner.mu.Unlock()
	if hookErr != nil {
		owner.failure = historyUnavailable("storage_error", hookErr)
		return
	}
	if owner.failure != nil {
		return
	}
	dir := filepath.Join(us.baseDir, sessionID)
	cp, err := historyLoadCheckpoint(context.Background(), dir, sessionID)
	if err != nil {
		owner.failure = err
		return
	}
	// A gap is left for the same bounded reconciler, never covered by this row.
	if addr.ByteOffset != cp.ArchiveEnds[addr.PartitionKey] || cp.CurrentKey != "" && addr.PartitionKey < cp.CurrentKey {
		owner.failure = historyUnavailable("index_inconsistent", errors.New("pending canonical suffix"))
		return
	}
	t, err := historyOpenTree(context.Background(), dir, cp, true)
	if err != nil {
		owner.failure = historyUnavailable("index_inconsistent", err)
		return
	}
	defer t.file.Close()
	info, err := t.file.Stat()
	if err != nil || info.Size() != cp.MutationEnd {
		owner.failure = historyUnavailable("index_inconsistent", errors.New("pending derived suffix"))
		return
	}
	line, err := encodeArchiveLine(rec)
	if err == nil {
		line = append(line, '\n')
		err = historyReduceArchive(t, &cp, addr, line, rec)
	}
	if err == nil {
		err = historyPublish(dir, t, &cp, "append", 1)
	}
	if err != nil {
		owner.failure = historyUnavailable("storage_error", err)
	} else {
		owner.failure = nil
	}
}
func (us *UnifiedStore) historyDispositionAccepted(sessionID string, rec InputDisposition) {
	e := chatHistoryPublicationEvent{SessionID: sessionID, Operation: "append", Point: "canonical_synced", Item: 1}
	owner := us.historyOwner(sessionID)
	hookErr := historyPublication(e)
	if !owner.mu.TryLock() {
		return
	}
	defer owner.mu.Unlock()
	if hookErr != nil {
		owner.failure = historyUnavailable("storage_error", hookErr)
		return
	}
	if owner.failure != nil {
		return
	}
	dir := filepath.Join(us.baseDir, sessionID)
	cp, err := historyLoadCheckpoint(context.Background(), dir, sessionID)
	if err != nil {
		owner.failure = err
		return
	}
	b, err := historyEncodeDisposition(rec)
	if err != nil {
		owner.failure = err
		return
	}
	stat, err := os.Stat(filepathJoinDisposition(dir))
	if err != nil || stat.Size() != cp.DispositionEnd+int64(len(b)) {
		owner.failure = historyUnavailable("index_inconsistent", errors.New("pending disposition suffix"))
		return
	}
	t, err := historyOpenTree(context.Background(), dir, cp, true)
	if err != nil {
		owner.failure = err
		return
	}
	defer t.file.Close()
	stat, err = t.file.Stat()
	if err != nil || stat.Size() != cp.MutationEnd {
		owner.failure = historyUnavailable("index_inconsistent", errors.New("pending derived suffix"))
		return
	}
	ref := historyDispositionRef{Offset: cp.DispositionEnd, Length: int64(len(b)), Digest: historyDigest(b)}
	err = historyReduceDisposition(t, &cp, ref, rec)
	if err == nil {
		err = historyPublish(dir, t, &cp, "append", 1)
	}
	if err != nil {
		owner.failure = historyUnavailable("storage_error", err)
	} else {
		owner.failure = nil
	}
}

// Try before acquiring a shard. A canonical writer never queues behind a
// recoverer's slow content read; its new fact simply remains the next suffix.
func (us *UnifiedStore) prepareHistoryAppend(sessionID string) {
	o := us.historyOwner(sessionID)
	if !o.mu.TryLock() {
		return
	}
	defer o.mu.Unlock()
	_, err := us.reconcileHistoryOwned(context.Background(), sessionID)
	o.failure = err
}
func (us *UnifiedStore) reconcileExistingChatHistory(ctx context.Context) error {
	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		return err
	}
	var failures error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sid := e.Name()
		if validateSessionID(sid) != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(us.baseDir, sid, ChatHistoryIndexDirectory)); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		_, err := us.reconcileChatHistoryIndex(ctx, sid)
		if err != nil {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}
