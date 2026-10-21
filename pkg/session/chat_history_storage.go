package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const historyFormatMarker = "omnipus-chat-history-index/1\n"

type historyAnchor struct { // not-wire-format: private durable coverage proof.
	Stream string        `json:"stream"`
	Record historyRecord `json:"record"`
	Offset int64         `json:"offset,omitempty"`
	Length int64         `json:"length,omitempty"`
	Digest string        `json:"digest,omitempty"`
}
type historyCheckpoint struct { // not-wire-format: private derived root, not SPA persistence.
	FormatVersion       int              `json:"format_version"`
	SessionID           string           `json:"session_id"`
	Generation          string           `json:"generation"`
	RetentionGeneration string           `json:"retention_generation"`
	File                string           `json:"file"`
	Root                int64            `json:"root"`
	MutationEnd         int64            `json:"mutation_end"`
	Rank                uint64           `json:"rank"`
	ArchiveEnds         map[string]int64 `json:"archive_ends"`
	DispositionEnd      int64            `json:"disposition_end"`
	CurrentKey          string           `json:"current_key"`
	Anchor              *historyAnchor   `json:"anchor,omitempty"`
	Facts               []string         `json:"facts,omitempty"`
}
type historyRecord struct { // not-wire-format: location and references only, never content.
	Position HistoryPosition `json:"position"`
	Rank     uint64          `json:"rank"`
	Address  ArchiveAddress  `json:"address"`
	Length   int64           `json:"length"`
	Digest   string          `json:"digest"`
	Eligible bool            `json:"eligible,omitempty"`
	Clear    bool            `json:"clear,omitempty"`
	Role     string          `json:"role,omitempty"`
	Turn     string          `json:"turn,omitempty"`
	Calls    []string        `json:"calls,omitempty"`
	Targets  []uint64        `json:"targets,omitempty"`
	Span     string          `json:"span,omitempty"`
	SpanKind string          `json:"span_kind,omitempty"`
}
type historyDispositionRef struct { // not-wire-format: bounded sidecar reference.
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	Digest string `json:"digest"`
}
type historyDuplicateRef struct { // not-wire-format: canonical duplicate lookup.
	Rank  uint64 `json:"rank"`
	Count int    `json:"count"`
}
type historySpanRef struct { // not-wire-format: actual persisted span fact references.
	Start, State, Message, End uint64
	HasRun                     *bool
}
type chatHistoryOwner struct {
	mu      sync.Mutex
	failure error
	refsMu  sync.Mutex
	refs    map[string]int
	retired map[string]bool
}

func (us *UnifiedStore) historyOwner(id string) *chatHistoryOwner {
	us.historyMu.Lock()
	defer us.historyMu.Unlock()
	if us.historyOwners == nil {
		us.historyOwners = map[string]*chatHistoryOwner{}
	}
	o := us.historyOwners[id]
	if o == nil {
		o = &chatHistoryOwner{refs: map[string]int{}, retired: map[string]bool{}}
		us.historyOwners[id] = o
	}
	return o
}
func historyRankKey(prefix string, rank uint64) string { return fmt.Sprintf("%s%020d", prefix, rank) }
func historyIDKey(prefix, id string) string            { return prefix + historyDigest([]byte(id)) }
func historyAddressKey(a ArchiveAddress) string {
	return historyIDKey("a/", a.PartitionKey+"\x00"+strconv.FormatInt(a.ByteOffset, 10)+"\x00"+a.EntryID)
}
func historyIndexPath(dir string) string    { return filepath.Join(dir, ChatHistoryIndexDirectory) }
func historyNewGeneration() (string, error) { return newArchivePayloadID() }
func historyLoadCheckpoint(ctx context.Context, dir, sid string) (historyCheckpoint, error) {
	var cp historyCheckpoint
	marker, err := os.ReadFile(filepath.Join(historyIndexPath(dir), "FORMAT"))
	historyIndexBytes(ctx, int64(len(marker)))
	if err != nil {
		reason := "storage_error"
		if errors.Is(err, os.ErrNotExist) {
			reason = "index_missing"
		}
		return cp, historyUnavailable(reason, err)
	}
	if string(marker) != historyFormatMarker {
		return cp, historyUnavailable("index_inconsistent", errors.New("incompatible index marker"))
	}
	b, err := os.ReadFile(filepath.Join(historyIndexPath(dir), "checkpoint.json"))
	historyIndexBytes(ctx, int64(len(b)))
	if err != nil {
		reason := "storage_error"
		if errors.Is(err, os.ErrNotExist) {
			reason = "index_missing"
		}
		return cp, historyUnavailable(reason, err)
	}
	if err = json.Unmarshal(b, &cp); err != nil {
		return cp, historyUnavailable("index_inconsistent", err)
	}
	if cp.FormatVersion != 1 || cp.SessionID != sid || cp.Generation == "" || cp.RetentionGeneration == "" || cp.ArchiveEnds == nil || cp.Root < 0 || cp.MutationEnd < 0 || cp.File == "" || filepath.Base(cp.File) != cp.File || cp.File == "." || cp.File == ".." {
		return cp, historyUnavailable("index_inconsistent", errors.New("invalid checkpoint"))
	}
	return cp, nil
}
func historyOpenTree(ctx context.Context, dir string, cp historyCheckpoint, write bool) (*chatHistoryIndexTree, error) {
	flags := os.O_RDONLY
	if write {
		flags = os.O_RDWR
	}
	f, err := os.OpenFile(filepath.Join(historyIndexPath(dir), "generations", cp.File), flags, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.Size() < cp.MutationEnd {
		f.Close()
		return nil, errors.New("history published index truncated")
	}
	t := &chatHistoryIndexTree{ctx: ctx, file: f, end: cp.MutationEnd, root: cp.Root, sid: cp.SessionID}
	if cp.Root != 0 {
		if _, err = t.node(cp.Root); err != nil {
			f.Close()
			return nil, err
		}
	}
	return t, nil
}
func historyCreateTree(ctx context.Context, dir, sid string) (*chatHistoryIndexTree, historyCheckpoint, error) {
	var cp historyCheckpoint
	gen, err := historyNewGeneration()
	if err != nil {
		return nil, cp, err
	}
	root := historyIndexPath(dir)
	for _, name := range []string{"generations", "staging"} {
		if err = os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			return nil, cp, err
		}
	}
	path := filepath.Join(root, "generations", gen+".chi")
	if err = fileutil.WriteFileAtomicSyncDir(path, nil, 0o600); err != nil {
		return nil, cp, err
	}
	if err = historySyncDirectory(root); err != nil {
		return nil, cp, err
	}
	if err = historySyncDirectory(dir); err != nil {
		return nil, cp, err
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, cp, err
	}
	cp = historyCheckpoint{FormatVersion: 1, SessionID: sid, Generation: gen, RetentionGeneration: gen, File: gen + ".chi", ArchiveEnds: map[string]int64{}}
	return &chatHistoryIndexTree{ctx: ctx, file: f, sid: sid}, cp, nil
}
func historyCheckpointBytes(cp historyCheckpoint) ([]byte, error) { return json.Marshal(cp) }
func historyPersistCheckpoint(dir string, cp historyCheckpoint) error {
	b, err := historyCheckpointBytes(cp)
	if err != nil {
		return err
	}
	return chatHistoryWriteCheckpoint(filepath.Join(historyIndexPath(dir), "checkpoint.json"), b, 0o600)
}

// One checksummed transaction footer freezes all candidate roots and covered
// ends. A complete unpublished transaction is reused, never applied twice.
func historyPublish(dir string, t *chatHistoryIndexTree, cp *historyCheckpoint, operation string, items int) error {
	gen, err := historyNewGeneration()
	if err != nil {
		return err
	}
	cp.Generation = gen
	cp.Root = t.root
	e := chatHistoryPublicationEvent{SessionID: cp.SessionID, Operation: operation, Item: items, IndexGeneration: gen}
	if err = t.flushNodes(); err != nil {
		return err
	}
	// The footer's end is its own final encoded end; JSON decimal width can grow.
	cp.MutationEnd = t.end
	var b []byte
	for i := 0; i < 8; i++ {
		b, err = historyEncodeFrame(historyFrame{Commit: cp})
		if err != nil {
			return err
		}
		end := t.end + int64(len(b))
		if end == cp.MutationEnd {
			break
		}
		cp.MutationEnd = end
	}
	b, err = historyEncodeFrame(historyFrame{Commit: cp})
	if err != nil {
		return err
	}
	if cp.MutationEnd != t.end+int64(len(b)) {
		return errors.New("unstable history transaction footer")
	}
	if n, writeErr := t.file.WriteAt(b, t.end); writeErr != nil {
		return writeErr
	} else if n != len(b) {
		return io.ErrShortWrite
	}
	e.Point = "mutation_written"
	if err = historyPublication(e); err != nil {
		return err
	}
	if err = t.file.Sync(); err != nil {
		return err
	}
	e.Point = "mutation_synced"
	if err = historyPublication(e); err != nil {
		return err
	}
	return historyPublishRoot(dir, t, *cp, e)
}
func historyPublishRoot(dir string, t *chatHistoryIndexTree, cp historyCheckpoint, e chatHistoryPublicationEvent) error {
	for _, point := range []string{"lookups_staged", "generation_verified", "before_checkpoint_publish"} {
		e.Point = point
		if err := historyPublication(e); err != nil {
			return err
		}
	}
	if cp.Root != 0 {
		if _, err := t.node(cp.Root); err != nil {
			return err
		}
	}
	if err := historyPersistCheckpoint(dir, cp); err != nil {
		return err
	}
	for _, point := range []string{"checkpoint_published", "root_installed"} {
		e.Point = point
		if err := historyPublication(e); err != nil {
			return err
		}
	}
	return nil
}
func historyDecodeArchive(b []byte) (ArchiveRecord, error) {
	var r ArchiveRecord
	if len(b) == 0 || b[len(b)-1] != '\n' {
		return r, historyBuildFailure("corrupt_archive", errors.New("unframed canonical history record"))
	}
	if err := json.Unmarshal(b[:len(b)-1], &r); err != nil {
		return r, historyBuildFailure("corrupt_archive", err)
	}
	if r.ViewMembership != ViewMembershipChat && r.ViewMembership != ViewMembershipModel && r.ViewMembership != ViewMembershipBoth {
		return r, historyBuildFailure("unconverted", errors.New("unsupported archive membership"))
	}
	if err := r.validateToolCallEffect(); err != nil {
		return r, historyBuildFailure("invalid_reference", err)
	}
	return r, nil
}
func historyRetained(cp historyCheckpoint) []string {
	out := make([]string, 0, len(cp.ArchiveEnds))
	for k := range cp.ArchiveEnds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (o *chatHistoryOwner) retain(path string) func() {
	o.refsMu.Lock()
	o.refs[path]++
	o.refsMu.Unlock()
	return func() {
		o.refsMu.Lock()
		defer o.refsMu.Unlock()
		o.refs[path]--
		if o.refs[path] == 0 {
			delete(o.refs, path)
			if o.retired[path] {
				if os.Remove(path) == nil {
					delete(o.retired, path)
				}
			}
		}
	}
}
func (o *chatHistoryOwner) retire(dir, keep string) error {
	o.refsMu.Lock()
	defer o.refsMu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == keep || e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if o.refs[path] > 0 {
			o.retired[path] = true
			continue
		}
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// validateIndexTail never trims framed damage or any canonical bytes. Only the
// incomplete unpublished final transaction can be redone from the known root.
func historyValidateIndexTail(t *chatHistoryIndexTree, cp historyCheckpoint) (historyCheckpoint, bool, error) {
	info, err := t.file.Stat()
	if err != nil {
		return historyCheckpoint{}, false, err
	}
	off := cp.MutationEnd
	var saved historyCheckpoint
	found := false
	for off < info.Size() {
		frame, next, readErr := t.readFrame(off, info.Size())
		if errors.Is(readErr, io.ErrUnexpectedEOF) {
			ev := chatHistoryPublicationEvent{SessionID: cp.SessionID, Operation: "reconcile", Point: "before_torn_tail_trim"}
			if err = historyPublication(ev); err != nil {
				return historyCheckpoint{}, false, err
			}
			if err = t.file.Truncate(cp.MutationEnd); err != nil {
				return historyCheckpoint{}, false, err
			}
			if err = t.file.Sync(); err != nil {
				return historyCheckpoint{}, false, err
			}
			ev.Point = "torn_tail_trimmed"
			if err = historyPublication(ev); err != nil {
				return historyCheckpoint{}, false, err
			}
			return historyCheckpoint{}, false, nil
		}
		if readErr != nil {
			return historyCheckpoint{}, false, readErr
		}
		if frame.Commit != nil {
			saved = *frame.Commit
			found = true
			if saved.MutationEnd != next || saved.SessionID != cp.SessionID || saved.File != cp.File {
				return historyCheckpoint{}, false, errors.New("invalid unpublished history checkpoint")
			}
		}
		off = next
	}
	if info.Size() > cp.MutationEnd && !found {
		return historyCheckpoint{}, false, errors.New("unpublished history transaction has no footer")
	}
	return saved, found, nil
}
func historyCloneCheckpoint(cp historyCheckpoint) historyCheckpoint {
	out := cp
	out.ArchiveEnds = make(map[string]int64, len(cp.ArchiveEnds))
	for k, v := range cp.ArchiveEnds {
		out.ArchiveEnds[k] = v
	}
	out.Facts = nil
	return out
}
func historySameCoverage(a, b historyCheckpoint) bool {
	if a.SessionID != b.SessionID || a.CurrentKey != b.CurrentKey || a.DispositionEnd != b.DispositionEnd || len(a.ArchiveEnds) != len(b.ArchiveEnds) {
		return false
	}
	for k, v := range a.ArchiveEnds {
		if w, ok := b.ArchiveEnds[k]; !ok || v != w {
			return false
		}
	}
	return true
}

// FORMAT is a checked, durable baseline marker, never an inferred zero root.
func historyWriteFormat(dir string) error {
	return fileutil.WriteFileAtomicSyncDir(filepath.Join(historyIndexPath(dir), "FORMAT"), []byte(historyFormatMarker), 0o600)
}
func historyTokenRank(t *chatHistoryIndexTree, p HistoryPosition) (uint64, error) {
	var rank uint64
	ok, err := t.get("p/"+string(p), &rank)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrHistoryPositionNotFound
	}
	return rank, nil
}
func historyReadRecordMeta(t *chatHistoryIndexTree, rank uint64) (historyRecord, bool, error) {
	var r historyRecord
	ok, err := t.get(historyRankKey("r/", rank), &r)
	return r, ok, err
}
func historyPrefix(n historyNode, prefix string) bool { return strings.HasPrefix(n.Key, prefix) }
