package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

type ChatHistoryIndexBuildOptions struct{ ScratchRoot string }
type ChatHistoryIndexBuildSummary struct {
	SessionID, Action string
	SourceGroupCount  int64
	PartitionCount    int
	// ArchiveRecordCount is every archive record the build read.
	ArchiveRecordCount int64
}
type historySourceFact struct {
	Size   int64
	Mode   os.FileMode
	Mtime  int64
	Digest string
}

// ChatHistoryIndexBuild is an explicit offline plan. Only Publish may write the
// designated derived subtree. It never creates a store, flusher or converter.
type ChatHistoryIndexBuild struct {
	summary    ChatHistoryIndexBuildSummary
	dir        string
	source     map[string]historySourceFact
	tree       *chatHistoryIndexTree
	checkpoint historyCheckpoint
	closed     bool
}

func fileutilHistoryEmptyTranscript(dir string) error {
	path := filepath.Join(dir, archiveCurrentFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return fileutil.WriteFileAtomicSyncDir(path, nil, 0o600)
}
func historyEncodeDisposition(rec InputDisposition) ([]byte, error) {
	b, err := json.Marshal(rec)
	return append(b, '\n'), err
}
func historySourceInventory(ctx context.Context, dir string) (map[string]historySourceFact, error) {
	out := map[string]historySourceFact{}
	err := filepath.WalkDir(dir, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if path == historyIndexPath(dir) && e.IsDir() {
			return filepath.SkipDir
		}
		if e.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe canonical symlink")
		}
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("unsafe canonical file kind")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, readErr := io.Copy(h, f)
		closeErr := f.Close()
		if err = errors.Join(readErr, closeErr); err != nil {
			return err
		}
		out[path] = historySourceFact{Size: info.Size(), Mode: info.Mode().Perm(), Mtime: info.ModTime().UnixNano(), Digest: hex.EncodeToString(h.Sum(nil))}
		return nil
	})
	return out, err
}
func PrepareChatHistoryIndex(ctx context.Context, sessionDir string, opts ChatHistoryIndexBuildOptions) (build *ChatHistoryIndexBuild, buildErr error) {
	defer func() { buildErr = historyBuildFailure("storage_error", buildErr) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(sessionDir) {
		return nil, errors.New("absolute session directory required")
	}
	dir, err := filepath.EvalSymlinks(sessionDir)
	if err != nil {
		return nil, err
	}
	sid := filepath.Base(dir)
	if err = validateSessionID(sid); err != nil {
		return nil, err
	}
	if opts.ScratchRoot != "" {
		if !filepath.IsAbs(opts.ScratchRoot) {
			return nil, errors.New("absolute out-of-tree scratch required")
		}
		scratch := filepath.Clean(opts.ScratchRoot)
		rel, e := filepath.Rel(dir, scratch)
		if e != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, errors.New("scratch overlaps source")
		}
	}
	metaBytes, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err = json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, historyBuildFailure("corrupt_archive", err)
	}
	if !IsValidSessionType(UnifiedSessionType(meta.Type)) {
		return nil, historyBuildFailure("unconverted", errors.New("unsupported canonical session format"))
	}
	if meta.ID != sid {
		return nil, historyBuildFailure("corrupt_archive", errors.New("session identity mismatch"))
	}
	source, err := historySourceInventory(ctx, dir)
	if err != nil {
		return nil, err
	}
	planning := &chatHistoryIndexTree{ctx: ctx, sid: sid, planning: map[string]json.RawMessage{}, knownRanks: map[HistoryPosition]uint64{}}
	retainedRanks := map[uint64]bool{}
	oldProofValid := false
	cp := historyCheckpoint{FormatVersion: 1, SessionID: sid, ArchiveEnds: map[string]int64{}}
	old, oldErr := historyLoadCheckpoint(ctx, dir, sid)
	if oldErr == nil {
		t, e := historyOpenTree(ctx, dir, old, false)
		if e == nil {
			e = t.walk(func(n historyNode) error {
				if strings.HasPrefix(n.Key, "r/") {
					var record historyRecord
					if decodeErr := json.Unmarshal(n.Value, &record); decodeErr != nil {
						return decodeErr
					}
					if record.Rank == 0 || n.Key != historyRankKey("r/", record.Rank) {
						return errors.New("invalid retained history rank")
					}
					retainedRanks[record.Rank] = true
				}
				if strings.HasPrefix(n.Key, "p/") {
					var rank uint64
					if decodeErr := json.Unmarshal(n.Value, &rank); decodeErr != nil {
						return decodeErr
					}
					position, positionErr := ParseHistoryPosition(n.Key[2:])
					if positionErr != nil || rank == 0 || rank > old.Rank {
						return errors.New("invalid history position/rank binding")
					}
					planning.knownRanks[position] = rank
				}
				return nil
			})
			e = errors.Join(e, t.file.Close())
			if e == nil {
				oldProofValid = true
				cp.Rank = old.Rank
				cp.RetentionGeneration = old.RetentionGeneration
			} else {
				planning.knownRanks = map[HistoryPosition]uint64{}
			}
		}
	}
	streams, dispEnd, _, current, err := historyCaptureStreams(dir, historyCheckpoint{ArchiveEnds: map[string]int64{}})
	if err != nil {
		return nil, err
	}
	cp.CurrentKey = current
	if oldProofValid {
		retained := make(map[string]bool, len(streams))
		for _, stream := range streams {
			retained[stream.key] = true
		}
		// The complete old tree proves token/rank-only expiry; the captured
		// catalog independently proves the target partition is absent. Missing
		// addresses in a retained partition never inherit this exception.
		planning.expiredAddress = func(a ArchiveAddress) bool {
			if retained[a.PartitionKey] {
				return false
			}
			position, positionErr := historyPositionForAddress(sid, a)
			rank := planning.knownRanks[position]
			return positionErr == nil && rank > 0 && !retainedRanks[rank]
		}
	}
	var groups, records int64
	for _, stream := range streams {
		for off := int64(0); off < stream.end; {
			b, e := historyReadSuffix(ctx, stream, off, sid, "archive", "suffix")
			if e != nil {
				return nil, e
			}
			rec, e := historyDecodeArchive(b)
			if e != nil {
				return nil, e
			}
			a := ArchiveAddress{PartitionKey: stream.key, ByteOffset: off, EntryID: rec.ID}
			if e = historyReduceArchive(planning, &cp, a, b, rec); e != nil {
				return nil, e
			}
			records++
			if rec.ViewMembership != ViewMembershipModel && rec.Type != EntryTypeToolCallEffect && rec.Type != EntryTypeModelRef {
				groups++
			}
			off += int64(len(b))
		}
		cp.ArchiveEnds[stream.key] = stream.end
	}
	planning.expiredAddress = nil // Proof is construction-local, never a runtime/lifetime cache.
	stream := historyCapturedStream{path: filepathJoinDisposition(dir), end: dispEnd}
	for off := int64(0); off < dispEnd; {
		b, e := historyReadSuffix(ctx, stream, off, sid, "disposition", "suffix")
		if e != nil {
			return nil, e
		}
		var rec InputDisposition
		if e = json.Unmarshal(b, &rec); e != nil {
			return nil, e
		}
		ref := historyDispositionRef{Offset: off, Length: int64(len(b)), Digest: historyDigest(b)}
		if e = historyReduceDisposition(planning, &cp, ref, rec); e != nil {
			return nil, e
		}
		off += int64(len(b))
	}
	for p, rank := range planning.knownRanks {
		key := "p/" + string(p)
		if _, ok := planning.planning[key]; !ok {
			if err = planning.set(key, rank); err != nil {
				return nil, err
			}
		}
	}
	action := "create"
	if oldErr == nil {
		action = "rebuild"
		if historySameCoverage(cp, old) {
			t, e := historyOpenTree(ctx, dir, old, false)
			if e == nil {
				same := true
				count := 0
				e = t.walk(func(n historyNode) error {
					count++
					v, ok := planning.planning[n.Key]
					if !ok || !historyJSONEqual(v, n.Value) {
						same = false
					}
					return nil
				})
				t.file.Close()
				if e == nil && same && count == len(planning.planning) {
					action = "unchanged"
					cp = old
				}
			}
		}
	} else if !errors.Is(oldErr, ErrChatHistoryUnavailable) {
		return nil, oldErr
	}
	final, err := historySourceInventory(ctx, dir)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(source, final) {
		return nil, historyBuildFailure("source_changed", errors.New("canonical source changed during offline operation"))
	}
	return &ChatHistoryIndexBuild{summary: ChatHistoryIndexBuildSummary{SessionID: sid, Action: action, SourceGroupCount: groups, PartitionCount: len(streams), ArchiveRecordCount: records}, dir: dir, source: source, tree: planning, checkpoint: cp}, nil
}
func historyJSONEqual(a, b []byte) bool {
	var av, bv any
	d := json.NewDecoder(strings.NewReader(string(a)))
	d.UseNumber()
	if d.Decode(&av) != nil {
		return false
	}
	d = json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	return d.Decode(&bv) == nil && reflect.DeepEqual(av, bv)
}
func (b *ChatHistoryIndexBuild) Summary() ChatHistoryIndexBuildSummary {
	if b == nil {
		return ChatHistoryIndexBuildSummary{}
	}
	return b.summary
}
func (b *ChatHistoryIndexBuild) Publish(ctx context.Context) error {
	// Offline only: no live reader can hold a generation pin, so a fresh
	// owner may retire every older generation.
	return b.publish(ctx, func(generations, keep string) error {
		owner := &chatHistoryOwner{refs: map[string]int{}, retired: map[string]bool{}}
		return owner.retire(generations, keep)
	})
}

// publish is Publish with the generation-retirement step supplied by the
// caller. The in-process self-heal retires through the runtime owner so the
// pins of concurrent page readers are honoured.
func (b *ChatHistoryIndexBuild) publish(ctx context.Context, retire func(generations, keep string) error) (buildErr error) {
	defer func() { buildErr = historyBuildFailure("storage_error", buildErr) }()
	if b == nil || b.closed {
		return errors.New("history build is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := historySourceInventory(ctx, b.dir)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(source, b.source) {
		return historyBuildFailure("source_changed", errors.New("canonical source changed during offline operation"))
	}
	if b.summary.Action == "unchanged" {
		return nil
	}
	t, cp, err := historyCreateTree(ctx, b.dir, b.summary.SessionID)
	if err != nil {
		return err
	}
	defer t.file.Close()
	cp.Rank = b.checkpoint.Rank
	cp.ArchiveEnds = b.checkpoint.ArchiveEnds
	cp.CurrentKey = b.checkpoint.CurrentKey
	cp.DispositionEnd = b.checkpoint.DispositionEnd
	cp.Anchor = b.checkpoint.Anchor
	if b.checkpoint.RetentionGeneration != "" {
		cp.RetentionGeneration = b.checkpoint.RetentionGeneration
	}
	if err = b.tree.walk(func(n historyNode) error { return t.set(n.Key, n.Value) }); err != nil {
		return err
	}
	if err = historyWriteFormat(b.dir); err != nil {
		return err
	}
	if err = historyPublish(b.dir, t, &cp, "repair", 0); err != nil {
		return err
	}
	if err = retire(filepath.Join(historyIndexPath(b.dir), "generations"), cp.File); err != nil {
		return err
	}
	b.checkpoint = cp
	b.summary.Action = "unchanged"
	return nil
}
func (b *ChatHistoryIndexBuild) Close() error {
	if b != nil {
		b.closed = true
		b.tree = nil
		b.source = nil
	}
	return nil
}
func VerifyChatHistoryIndex(ctx context.Context, sessionDir string) (buildErr error) {
	defer func() { buildErr = historyBuildFailure("storage_error", buildErr) }()
	b, err := PrepareChatHistoryIndex(ctx, sessionDir, ChatHistoryIndexBuildOptions{})
	if err != nil {
		return err
	}
	defer b.Close()
	if b.summary.Action != "unchanged" {
		return historyBuildFailure("corrupt_archive", errors.New("history index does not match canonical data"))
	}
	return nil
}
