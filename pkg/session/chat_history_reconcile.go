package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type historyCapturedStream struct {
	key, path string
	end       int64
}
type historyMutation struct {
	record         *ArchiveRecord
	address        ArchiveAddress
	bytes          []byte
	disposition    *InputDisposition
	dispositionRef historyDispositionRef
	expired        string
}

func filepathJoinDisposition(dir string) string { return filepath.Join(dir, inputDispositionFileName) }
func historyCaptureStreams(dir string, cp historyCheckpoint) ([]historyCapturedStream, int64, []string, string, error) {
	mark, err := os.ReadFile(filepath.Join(dir, archiveDayMarkFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil, "", err
	}
	current := strings.TrimSpace(string(mark))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, nil, "", err
	}
	actual := map[string]historyCapturedStream{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!transcriptDayPattern.MatchString(name) && name != archiveCurrentFile) {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil {
			return nil, 0, nil, "", statErr
		}
		key := strings.TrimSuffix(name, ".jsonl")
		if name == archiveCurrentFile {
			key = current
			if key == "" {
				if info.Size() != 0 {
					return nil, 0, nil, "", historyBuildFailure("unconverted", errors.New("unmarked nonempty archive"))
				}
				continue
			}
		}
		actual[key] = historyCapturedStream{key: key, path: filepath.Join(dir, name), end: info.Size()}
	}
	var expired []string
	for key, end := range cp.ArchiveEnds {
		s, ok := actual[key]
		if !ok {
			if key == current || key == cp.CurrentKey && current == "" {
				return nil, 0, nil, "", errors.New("current archive disappeared")
			}
			expired = append(expired, key)
			continue
		}
		if s.end < end {
			return nil, 0, nil, "", errors.New("canonical archive truncated")
		}
	}
	streams := make([]historyCapturedStream, 0, len(actual))
	for key, s := range actual {
		if _, known := cp.ArchiveEnds[key]; !known && cp.CurrentKey != "" && key < cp.CurrentKey {
			return nil, 0, nil, "", errors.New("archive catalog repointed")
		}
		streams = append(streams, s)
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].key < streams[j].key })
	sort.Strings(expired)
	dispEnd := int64(0)
	info, err := os.Stat(filepathJoinDisposition(dir))
	if err == nil {
		dispEnd = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil, "", err
	}
	if dispEnd < cp.DispositionEnd {
		return nil, 0, nil, "", errors.New("disposition stream truncated")
	}
	return streams, dispEnd, expired, current, nil
}
func historyReadSuffix(ctx context.Context, s historyCapturedStream, offset int64, sid, stream, purpose string) (b []byte, err error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	addr := ArchiveAddress{PartitionKey: s.key, ByteOffset: offset}
	e := chatHistoryReadEvent{SessionID: sid, Scope: "recovery", Purpose: purpose, Stream: stream, Address: &addr, Offset: offset, Phase: "record_opened"}
	defer func() {
		err = errors.Join(err, f.Close())
		e.Phase = "record_closed"
		e.Bytes = 0
		if chatHistoryReadHook != nil {
			err = errors.Join(err, chatHistoryReadHook(ctx, e))
		}
	}()
	if err = historyReadEvent(ctx, e); err != nil {
		return nil, err
	}
	read := int64(0)
	var readErr error
	for offset+int64(len(b)) < s.end && len(b) <= archiveRecordBound {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		nbytes := min(int64(4096), s.end-offset-int64(len(b)))
		chunk := make([]byte, nbytes)
		n, e := f.ReadAt(chunk, offset+int64(len(b)))
		read += int64(n)
		chunk = chunk[:n]
		if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
			b = append(b, chunk[:i+1]...)
			break
		}
		b = append(b, chunk...)
		if e != nil {
			readErr = e
			break
		}
	}
	if stream == "archive" {
		var rec ArchiveRecord
		if json.Unmarshal(b, &rec) == nil {
			addr.EntryID = rec.ID
		}
	}
	e.Phase = "record_read"
	e.Bytes = read
	if m := historyMetrics(ctx); m != nil {
		if stream == "archive" {
			m.RecoveryArchiveRecords++
			m.RecoveryArchiveBytes += read
		} else {
			m.DispositionRecordsRead++
			m.DispositionBytesRead += read
		}
		if purpose == "overflow" {
			m.RecoveryOverflowRecords++
		}
	}
	if hookErr := historyReadEvent(ctx, e); hookErr != nil {
		return b, hookErr
	}
	if readErr != nil {
		return b, readErr
	}
	if len(b) == 0 || b[len(b)-1] != '\n' || len(b) > archiveRecordBound+1 {
		return b, historyBuildFailure("corrupt_archive", errors.New("invalid canonical suffix framing"))
	}
	return b, nil
}
func historyValidateAnchor(ctx context.Context, dir string, cp historyCheckpoint, expired []string) error {
	if cp.Anchor == nil {
		return nil
	}
	a := cp.Anchor
	if a.Stream == "archive" {
		for _, key := range expired {
			if key == a.Record.Address.PartitionKey {
				return nil
			}
		}
		_, err := historyReadArchive(ctx, dir, a.Record, ChatHistoryReadCut{sessionID: cp.SessionID, generation: cp.Generation, retentionGeneration: cp.RetentionGeneration}, "recovery", "anchor")
		return err
	}
	b, err := historyReadBytes(ctx, filepathJoinDisposition(dir), a.Offset, a.Length, chatHistoryReadEvent{SessionID: cp.SessionID, Scope: "recovery", Purpose: "anchor", Stream: "disposition"})
	if err != nil {
		return err
	}
	if historyDigest(b) != a.Digest {
		return errors.New("disposition anchor changed")
	}
	return nil
}
func historyCollectMutations(ctx context.Context, dir string, cp historyCheckpoint, streams []historyCapturedStream, dispEnd int64, expired []string) ([]historyMutation, chatHistoryReconcileResult, error) {
	var result chatHistoryReconcileResult
	items := make([]historyMutation, 0, chatHistoryRecoveryMaxItems+1)
	add := func(m historyMutation) bool {
		items = append(items, m)
		result.ObservedItems = len(items)
		if metric := historyMetrics(ctx); metric != nil {
			metric.RecoveryItems++
		}
		return len(items) > chatHistoryRecoveryMaxItems
	}
	for _, key := range expired {
		if add(historyMutation{expired: key}) {
			return items, result, historyUnavailable("catchup_limit", nil)
		}
	}
	for _, s := range streams {
		off := cp.ArchiveEnds[s.key]
		for off < s.end {
			purpose := "suffix"
			if len(items) == chatHistoryRecoveryMaxItems {
				purpose = "overflow"
			}
			b, err := historyReadSuffix(ctx, s, off, cp.SessionID, "archive", purpose)
			if err != nil {
				return nil, result, err
			}
			rec, err := historyDecodeArchive(b)
			if err != nil {
				return nil, result, err
			}
			a := ArchiveAddress{PartitionKey: s.key, ByteOffset: off, EntryID: rec.ID}
			if add(historyMutation{record: &rec, address: a, bytes: b}) {
				return items, result, historyUnavailable("catchup_limit", nil)
			}
			off += int64(len(b))
		}
	}
	stream := historyCapturedStream{path: filepathJoinDisposition(dir), end: dispEnd}
	for off := cp.DispositionEnd; off < dispEnd; {
		purpose := "suffix"
		if len(items) == chatHistoryRecoveryMaxItems {
			purpose = "overflow"
		}
		b, err := historyReadSuffix(ctx, stream, off, cp.SessionID, "disposition", purpose)
		if err != nil {
			return nil, result, err
		}
		var rec InputDisposition
		if err = json.Unmarshal(b, &rec); err != nil {
			return nil, result, err
		}
		ref := historyDispositionRef{Offset: off, Length: int64(len(b)), Digest: historyDigest(b)}
		if add(historyMutation{disposition: &rec, dispositionRef: ref}) {
			return items, result, historyUnavailable("catchup_limit", nil)
		}
		off += int64(len(b))
	}
	return items, result, nil
}
func historyApplyMutations(t *chatHistoryIndexTree, cp *historyCheckpoint, items []historyMutation) error {
	for _, m := range items {
		if err := t.ctx.Err(); err != nil {
			return err
		}
		switch {
		case m.record != nil:
			if err := historyReduceArchive(t, cp, m.address, m.bytes, *m.record); err != nil {
				return err
			}
			cp.Facts = append(cp.Facts, historyAddressKey(m.address))
		case m.disposition != nil:
			if err := historyReduceDisposition(t, cp, m.dispositionRef, *m.disposition); err != nil {
				return err
			}
			cp.Facts = append(cp.Facts, historyRankKey("disposition/", uint64(m.dispositionRef.Offset)))
		case m.expired != "":
			delete(cp.ArchiveEnds, m.expired)
			cp.Facts = append(cp.Facts, "expired/"+m.expired)
		}
	}
	return nil
}

func (us *UnifiedStore) reconcileHistoryOwned(ctx context.Context, sid string) (chatHistoryReconcileResult, error) {
	result := chatHistoryReconcileResult{}
	dir := filepath.Join(us.baseDir, sid)
	h := us.lockSession(sid)
	cp, err := historyLoadCheckpoint(ctx, dir, sid)
	if err != nil {
		h.Unlock()
		return result, err
	}
	a, err := us.archiveStore(sid)
	if err != nil {
		h.Unlock()
		return result, err
	}
	a.mu.Lock()
	streams, dispEnd, expired, current, captureErr := historyCaptureStreams(dir, cp)
	a.mu.Unlock()
	h.Unlock()
	if captureErr != nil {
		return result, historyUnavailable("index_inconsistent", captureErr)
	}
	if err = historyValidateAnchor(ctx, dir, cp, expired); err != nil {
		return result, historyUnavailable("index_inconsistent", err)
	}
	items, result, err := historyCollectMutations(ctx, dir, cp, streams, dispEnd, expired)
	if err != nil {
		if errors.Is(err, ErrChatHistoryUnavailable) || ctx.Err() != nil {
			return result, err
		}
		return result, historyUnavailable("index_inconsistent", err)
	}
	t, err := historyOpenTree(ctx, dir, cp, true)
	if err != nil {
		return result, historyUnavailable("index_inconsistent", err)
	}
	defer t.file.Close()
	saved, hasSaved, err := historyValidateIndexTail(t, cp)
	if err != nil {
		return result, historyUnavailable("index_inconsistent", err)
	}
	result.IndexGeneration = cp.Generation
	result.RetentionGeneration = cp.RetentionGeneration
	if len(items) == 0 {
		return result, nil
	}
	candidate := historyCloneCheckpoint(cp)
	for _, s := range streams {
		candidate.ArchiveEnds[s.key] = s.end
	}
	candidate.CurrentKey = current
	candidate.DispositionEnd = dispEnd
	// Build against the old root; the reducer advances coverage using each fact.
	candidate.ArchiveEnds = make(map[string]int64, len(cp.ArchiveEnds))
	for k, v := range cp.ArchiveEnds {
		candidate.ArchiveEnds[k] = v
	}
	if len(expired) > 0 {
		t, candidate, err = us.historyCompact(ctx, dir, t, candidate, expired)
		if err != nil {
			return result, historyUnavailable("index_inconsistent", err)
		}
		defer t.file.Close()
	}
	if hasSaved && len(expired) == 0 {
		expected := historyCloneCheckpoint(cp)
		if err = historyApplyMutations(t, &expected, items); err != nil {
			return result, historyUnavailable("index_inconsistent", err)
		}
		if !historySameCoverage(expected, saved) {
			return result, historyUnavailable("index_inconsistent", errors.New("saved history transaction does not match canonical suffix"))
		}
		candidate = saved
		t.buffer = nil
		t.pending = nil
		t.root = saved.Root
		t.end = saved.MutationEnd
	} else {
		if err = historyApplyMutations(t, &candidate, items); err != nil {
			return result, historyUnavailable("index_inconsistent", err)
		}
	}
	h = us.lockSession(sid)
	defer h.Unlock()
	latest, err := historyLoadCheckpoint(ctx, dir, sid)
	if err != nil {
		return result, err
	}
	if latest.Generation != cp.Generation {
		return result, historyUnavailable("index_inconsistent", errors.New("history root changed during reconciliation"))
	}
	operation := "reconcile"
	if len(expired) > 0 {
		operation = "retention"
	}
	if hasSaved && len(expired) == 0 {
		if err = t.file.Sync(); err == nil {
			err = historyPublishRoot(dir, t, candidate, chatHistoryPublicationEvent{SessionID: sid, Operation: operation, IndexGeneration: candidate.Generation, Item: len(items)})
		}
	} else {
		err = historyPublish(dir, t, &candidate, operation, len(items))
	}
	if err != nil {
		return result, historyUnavailable("storage_error", err)
	}
	for _, m := range items {
		switch {
		case m.record != nil:
			result.ArchiveRecords++
		case m.disposition != nil:
			result.DispositionRecords++
		case m.expired != "":
			result.ExpiredPartitions++
		}
	}
	result.AppliedItems = len(items)
	result.Published = true
	result.IndexGeneration = candidate.Generation
	result.RetentionGeneration = candidate.RetentionGeneration
	if len(expired) > 0 {
		err = us.historyOwner(sid).retire(filepath.Join(historyIndexPath(dir), "generations"), candidate.File)
	}
	return result, err
}
