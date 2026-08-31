// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_restructure_trash.go — trash and restore: the two operations of
// knowledge_restructure that FR-048/FR-048a/FR-048b/FR-038a describe and that
// had no engine at all before Stage 4 (ADR-068 D15.3 item 5, D20; spec
// §4.1.5; docs/internal/design/vault-trash-convention-2026-08-28.md, "the
// trash convention" — every location/receipt/refusal decision below is taken
// from that document, not re-derived here).
//
// # Why this is its own engine, not a call into Renamer
//
// The trash convention's F1 finding (RESOLVED) is the reason: `Renamer.Plan`
// now calls authorRefuseReserved on BOTH `from` and `to`, so a rename/move
// that crosses the `.omnipus-vault/` boundary in EITHER direction is refused
// — correctly, because a note-destroying operation reachable through "move"
// was exactly the untracked-hard-delete defect F1 fixed. Trash's destination
// and restore's source are BOTH inside `.omnipus-vault/trash/` by design, so
// neither operation can be built on Renamer.Plan without re-opening that
// hole. This file is deliberately narrower than rename.go: it does not
// rewrite links (FR-048 says dangling links are NOT repaired — there is
// nothing to repair them to), so it needs no journal, no crash-recovery
// primitive and no link-rewrite plan. It still shares rename.go's other load-
// bearing habits: containment on the real path, the tier-1 note lock via
// WithNoteWriteLock, and an audit record for every outcome including a
// refusal.
//
// # Reusing the canonical VersionToken without inventing a second CAS
//
// ADR-068 AC-15.5d and spec §4.1.5 AC-X3 are explicit: knowledge_restructure
// declares no `expect_version` and accepts none from the caller — a
// single-file token cannot honestly guard an operation whose blast radius is
// many notes (trash breaks N notes' relations; a rename rewrites them). That
// is a decision about the TOOL'S WIRE CONTRACT. It says nothing about how
// this file protects its OWN write from a tier-3 writer (Obsidian, Syncthing,
// git) racing the move: version.go's ReadNoteVersion/VersionToken IS reused
// here, internally, exactly as WriteNote's own "read-compare-write inside the
// lock" discipline does — readNoteVersionAbs is called once to confirm the
// note exists, the bytes are read, and it is called again immediately before
// the move to catch a change in between, all inside the note's tier-1 lock.
// No second notion of "stale" is invented; the trashed copy's receipt simply
// RECORDS the VersionToken read at trash time (SourceVersion), for
// diagnostics, the same way NoteVersion's Size/ModTime are carried without
// being decisive.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/records"
)

const (
	// trashDirName is the fixed subdirectory of MarkerDirName trash entries
	// live under. Per the trash convention, THE TRASH LOCATION MUST NOT
	// BECOME CONFIGURABLE: everything about a trashed note's invisibility to
	// search, links and the orphan check depends on it sitting under a
	// directory name in scanSkippedDirNames, which this is.
	trashDirName = "trash"

	// trashReceiptFileName is the small JSON document beside each trashed
	// note (design note §7). It never carries note content.
	trashReceiptFileName = "entry.json"

	// trashTimestampLayout is the "colon-free timestamp" FR-048 specifies —
	// deliberately built without Go's "Z07:00" token (see trashTimestamp)
	// so the trailing "Z" is a plain literal rather than a timezone-offset
	// directive that happens to render as "Z" only for UTC.
	trashTimestampLayout = "20060102T150405"

	// trashDirMaxCollisionSuffix bounds the same-second collision loop
	// (allocateTrashDir) so a pathological caller cannot spin forever.
	trashDirMaxCollisionSuffix = 1000
)

// Audit operation names for the trash/restore engine. Local to this file, the
// same way authoring_tools.go's knowledgeRenameOp is local rather than added
// to author.go's AuthorOperation block — this package's audit vocabulary is
// intentionally not centralised in one file.
const (
	trashOpTrash   = "knowledge.note.trash"
	trashOpRestore = "knowledge.note.restore"
)

// Sentinel errors from the trash/restore engine.
var (
	// ErrTrashSourceMissing means the path `trash` was asked to move does not
	// name an existing regular note.
	ErrTrashSourceMissing = errors.New("knowledge: trash source not found")

	// ErrRestoreNotFound means no trashed copy exists at the original path
	// `restore` was given (or, with trashed_at set, none at that timestamp).
	ErrRestoreNotFound = errors.New("knowledge: no trashed note at that path")

	// ErrRestoreIdentifierCollision means a LIVE record already holds the
	// identifier the trashed copy carries (FR-038a). The counter is never
	// lowered by a trash, so this can only happen when a new note was
	// created at that identifier in the interval.
	ErrRestoreIdentifierCollision = errors.New("knowledge: a live record already holds this identifier")

	// ErrRestoreDestinationExists means a live note already occupies the
	// note's original path. Restoring would silently overwrite it, which
	// this package never does to any write (version.go's whole header).
	ErrRestoreDestinationExists = errors.New("knowledge: restore destination already exists")
)

// trashReceipt is entry.json's shape (design note §7). It is read back by
// both trash (to report prior trashings of the same path) and restore (as
// the primary addressing/collision source, with a directory-layout fallback
// when it is missing or malformed — see findTrashCopies).
type trashReceipt struct {
	OriginalPath  string   `json:"original_path"`
	Collection    string   `json:"collection"`
	TrashedAt     string   `json:"trashed_at"`
	AgentID       string   `json:"agent_id,omitempty"`
	RecordType    string   `json:"record_type,omitempty"`
	RecordID      string   `json:"record_id,omitempty"`
	SourceVersion string   `json:"source_version,omitempty"`
	DanglingLinks int      `json:"dangling_links"`
	DanglingNotes []string `json:"dangling_notes,omitempty"`
}

// TrashAuditEvent is one trash-engine outcome, applied or refused.
type TrashAuditEvent struct {
	Op         string
	AgentID    string
	Collection string
	Paths      []string
	Outcome    string // "applied" | "refused"
	Reason     string
	At         time.Time
}

// TrashAuditFunc receives one TrashAuditEvent per attempt.
type TrashAuditFunc func(TrashAuditEvent)

// TrashRequest is one trash operation.
type TrashRequest struct {
	// Path is the note's collection-relative path.
	Path string
}

// TrashResult reports what trash did.
type TrashResult struct {
	OriginalPath           string
	TrashID                string // the (possibly collision-suffixed) timestamp directory name
	TrashPath              string // collection-relative path of the trashed copy
	DanglingLinkCount      int    // total inbound links now unrepairable (AC-X1)
	DanglingNotes          []string
	DanglingNotesTruncated bool
	RecordType             string
	RecordID               string
	PriorTrashings         []string // other TrashIDs already holding this original path
}

// RestoreRequest is one restore operation.
type RestoreRequest struct {
	// Path is the note's ORIGINAL collection-relative path — the address the
	// design note settles on (§4, resolving F3): the caller names the note
	// the way it would have named it before trashing it, never a path inside
	// .omnipus-vault/trash/.
	Path string
	// TrashedAt optionally selects an older copy when the path was trashed
	// more than once. Empty means the most recently trashed copy.
	TrashedAt string
}

// RestoreResult reports what restore did.
type RestoreResult struct {
	OriginalPath       string
	RestoredFrom       string // the TrashID that was restored
	OtherAvailable     []string
	RecordType         string
	RecordID           string
	ResolvedLinksCount int // inbound links that resolve again after the restore
}

// Trasher performs trash and restore inside one collection. It holds no
// state between calls, matching Renamer's own contract.
type Trasher struct {
	// FS is the filesystem surface used for every read. Nil means the real
	// filesystem.
	FS LinkFS
	// Root is the validated collection root.
	Root CollectionRoot
	// AgentID is recorded in audit events, when the caller has one.
	AgentID string
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Audit receives one event per attempt, including refusals. Nil is a
	// wiring defect for anything an agent can reach — see rename.go's
	// Renamer.Audit doc comment, which states the same rule for the same
	// reason.
	Audit TrashAuditFunc
	// Lock configures D14 tier-1 mutual exclusion for the note being trashed
	// or restored.
	Lock NoteLockConfig
}

func (tr *Trasher) fs() LinkFS {
	if tr.FS != nil {
		return tr.FS
	}
	return OSLinkFS()
}

func (tr *Trasher) now() time.Time {
	if tr.Now != nil {
		return tr.Now()
	}
	return time.Now()
}

func (tr *Trasher) lockConfig() NoteLockConfig {
	cfg := tr.Lock
	cfg.CollectionRoot = tr.Root.Path()
	return cfg
}

func (tr *Trasher) emit(op, outcome string, paths []string, reason string) {
	if tr.Audit == nil {
		return
	}
	tr.Audit(TrashAuditEvent{
		Op: op, AgentID: tr.AgentID, Collection: tr.Root.Path(),
		Paths: paths, Outcome: outcome, Reason: reason, At: tr.now().UTC(),
	})
}

// trashTimestamp renders now as FR-048's "colon-free timestamp" — manual
// concatenation rather than a "Z07:00" layout token, so the trailing "Z" is
// unconditionally a literal rather than a timezone directive that only
// happens to print "Z" because the input is UTC.
func trashTimestamp(now time.Time) string {
	return now.UTC().Format(trashTimestampLayout) + "Z"
}

// ---------------------------------------------------------------------------
// Trash
// ---------------------------------------------------------------------------

// Trash moves one note into `.omnipus-vault/trash/<timestamp>/<original
// path>`, bytes untouched (design note §1), and writes its receipt (§7).
// Inbound links are counted and named but never repaired (FR-048) — there is
// nothing to repair them to.
func (tr *Trasher) Trash(req TrashRequest) (*TrashResult, error) {
	fsys := tr.fs()

	from, err := cleanNoteArg(req.Path)
	if err != nil {
		tr.emit(trashOpTrash, "refused", nil, err.Error())
		return nil, err
	}
	from = ensureMarkdown(from)

	// F6: a trash source already inside .omnipus-vault/ (or .obsidian/,
	// .git/) would move our own bookkeeping into our own trash. Refused by
	// name, same guard authorWriteTarget uses for a create's destination.
	if rerr := authorRefuseReserved(from); rerr != nil {
		tr.emit(trashOpTrash, "refused", []string{from}, rerr.Error())
		return nil, rerr
	}

	abs, err := tr.Root.ResolveContainedNoSymlink(fsys, from)
	if err != nil {
		tr.emit(trashOpTrash, "refused", []string{from}, err.Error())
		return nil, err
	}

	// Inbound links are computed BEFORE the move, while `from` still
	// resolves — a graph built after the move would see no citations to
	// report at all, which is the opposite of AC-X1.
	graph, gerr := BuildLinkGraph(fsys, tr.Root)
	if gerr != nil {
		tr.emit(trashOpTrash, "refused", []string{from}, gerr.Error())
		return nil, gerr
	}
	backlinks := graph.Backlinks(from)
	danglingNotes, truncated := dedupeAndCapNotePaths(backlinks)

	var result TrashResult
	lockErr := WithNoteWriteLock(tr.lockConfig(), from, func() error {
		before, verr := readNoteVersionAbs(from, abs)
		if verr != nil {
			return verr
		}
		if !before.Exists {
			return fmt.Errorf("%w: %q", ErrTrashSourceMissing, from)
		}
		content, rerr := os.ReadFile(abs) //nolint:gosec // abs is contained by ResolveContainedNoSymlink above
		if rerr != nil {
			return fmt.Errorf("knowledge: read %q: %w", from, rerr)
		}
		// Reused VersionToken discipline (see file header): re-read the
		// token immediately before the move, inside the same lock, to catch
		// a tier-3 writer that changed the file between the read above and
		// here. This is NOT a caller-facing expect_version — it is the same
		// internal compare-then-act pattern WriteNote already uses.
		after, verr2 := readNoteVersionAbs(from, abs)
		if verr2 != nil {
			return verr2
		}
		if after.Token != before.Token {
			return fmt.Errorf("%w: %q changed while it was being trashed; try again", ErrVersionConflict, from)
		}

		now := tr.now()
		trashID, trashDirRel, aerr := tr.allocateTrashDir(fsys, now)
		if aerr != nil {
			return aerr
		}
		trashFileRel := path.Join(trashDirRel, from)
		trashFileAbs := filepath.Join(tr.Root.Path(), filepath.FromSlash(trashFileRel))
		if mkErr := os.MkdirAll(filepath.Dir(trashFileAbs), markerDirPerm); mkErr != nil {
			return fmt.Errorf("knowledge: create trash directory: %w", mkErr)
		}
		if mvErr := moveFile(abs, trashFileAbs); mvErr != nil {
			return fmt.Errorf("knowledge: move %q to trash: %w", from, mvErr)
		}

		rec := records.ParseRecord(from, content)
		receipt := trashReceipt{
			OriginalPath: from, Collection: tr.Root.Path(), TrashedAt: now.UTC().Format(time.RFC3339),
			AgentID: tr.AgentID, RecordType: rec.TypeName(), RecordID: rec.ID(),
			SourceVersion: string(before.Token), DanglingLinks: len(backlinks), DanglingNotes: danglingNotes,
		}
		receiptBytes, jerr := json.MarshalIndent(receipt, "", "  ")
		if jerr != nil {
			return fmt.Errorf("knowledge: encode trash receipt: %w", jerr)
		}
		receiptAbs := filepath.Join(tr.Root.Path(), filepath.FromSlash(path.Join(trashDirRel, trashReceiptFileName)))
		if werr := fileutil.WriteFileAtomic(receiptAbs, receiptBytes, markerFilePerm); werr != nil {
			return fmt.Errorf("knowledge: write trash receipt: %w", werr)
		}

		priors, perr := tr.priorTrashings(fsys, from, trashID)
		if perr != nil {
			// Best-effort reporting only (design note §7's "PriorTrashings"
			// is informational, never a refusal) — logged rather than
			// silently dropped, per the no-`_ = err`-on-I/O rule.
			slog.Warn("knowledge: could not enumerate prior trashings", "path", from, "error", perr)
		}

		result = TrashResult{
			OriginalPath: from, TrashID: trashID, TrashPath: trashFileRel,
			DanglingLinkCount: len(backlinks), DanglingNotes: danglingNotes, DanglingNotesTruncated: truncated,
			RecordType: rec.TypeName(), RecordID: rec.ID(), PriorTrashings: priors,
		}
		return nil
	})
	if lockErr != nil {
		tr.emit(trashOpTrash, "refused", []string{from}, lockErr.Error())
		return nil, lockErr
	}
	tr.emit(trashOpTrash, "applied", []string{from, result.TrashPath}, "")
	return &result, nil
}

// dedupeAndCapNotePaths reduces a set of inbound ResolvedLinks to the sorted,
// deduplicated list of notes that carry them, capped at
// IntegrityFindingsPerCategory — the same clamp check_integrity's own
// per-category bound uses (integrity.go), so a mass-trash cannot make this
// response the one uncapped list in the package.
func dedupeAndCapNotePaths(links []ResolvedLink) (notes []string, truncated bool) {
	set := make(map[string]struct{}, len(links))
	for _, l := range links {
		set[l.From] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	if len(out) > IntegrityFindingsPerCategory {
		return out[:IntegrityFindingsPerCategory], true
	}
	return out, false
}

// allocateTrashDir picks the timestamp directory for one trash operation,
// resolving a same-second collision by appending "-2", "-3", ... rather than
// silently overwriting an existing trash entry. FR-048 names the layout
// "<timestamp>/<original path>" and says nothing about two operations in the
// same second; the alternative — a second trash clobbering the first's
// receipt and file inside one directory — is the "delete a file that looks
// like ours" failure class the trash convention's §6 was written specifically
// against, so refusing to reuse an occupied directory is the conservative
// reading.
func (tr *Trasher) allocateTrashDir(fsys LinkFS, now time.Time) (trashID, dirRel string, err error) {
	base := trashTimestamp(now)
	candidate := base
	for i := 1; ; i++ {
		dirRel = path.Join(MarkerDirName, trashDirName, candidate)
		abs := filepath.Join(tr.Root.Path(), filepath.FromSlash(dirRel))
		if _, statErr := fsys.Lstat(abs); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				return candidate, dirRel, nil
			}
			return "", "", fmt.Errorf("knowledge: check trash directory %q: %w", dirRel, statErr)
		}
		if i >= trashDirMaxCollisionSuffix {
			return "", "", fmt.Errorf("knowledge: could not allocate a trash directory for %s", base)
		}
		candidate = fmt.Sprintf("%s-%d", base, i+1)
	}
}

// moveFile relocates src to dst, preferring an atomic rename (the common
// case: both paths are inside the same collection, hence the same
// filesystem) and falling back to copy-then-remove when the rename fails for
// any reason (typically EXDEV, but the fallback is safe regardless because
// dst is always freshly allocated — see allocateTrashDir and
// findTrashCopies' existence check — so a copy can never silently clobber
// something).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	content, rerr := os.ReadFile(src) //nolint:gosec // src is contained by the caller
	if rerr != nil {
		return fmt.Errorf("knowledge: read %q for move: %w", src, rerr)
	}
	if werr := fileutil.WriteFileAtomic(dst, content, markerFilePerm); werr != nil {
		return fmt.Errorf("knowledge: write %q for move: %w", dst, werr)
	}
	if remErr := os.Remove(src); remErr != nil {
		// The copy already landed; leaving it AND the source would be two
		// copies of one note, silently. Removing the copy on this failure
		// keeps "moveFile either moved the file or made no visible change"
		// true even on this rare path.
		_ = os.Remove(dst)
		return fmt.Errorf("knowledge: remove %q after copy: %w", src, remErr)
	}
	return nil
}

// listTrashDirs returns every top-level directory name under
// .omnipus-vault/trash/, unsorted. A missing trash directory (nothing has
// ever been trashed) is not an error — it reports no entries.
func (tr *Trasher) listTrashDirs(fsys LinkFS) ([]string, error) {
	trashRoot := filepath.Join(tr.Root.Path(), MarkerDirName, trashDirName)
	entries, err := fsys.ReadDir(trashRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("knowledge: list trash: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// readReceipt reads and decodes one trash entry's entry.json. A missing or
// malformed receipt is reported as ok=false rather than an error — design
// note §7: "A missing or malformed receipt must therefore degrade, not
// fail."
func (tr *Trasher) readReceipt(trashID string) (trashReceipt, bool) {
	p := filepath.Join(tr.Root.Path(), MarkerDirName, trashDirName, trashID, trashReceiptFileName)
	data, err := os.ReadFile(p) //nolint:gosec // p is built from a trash directory name this package enumerated
	if err != nil {
		return trashReceipt{}, false
	}
	var rec trashReceipt
	if jerr := json.Unmarshal(data, &rec); jerr != nil {
		return trashReceipt{}, false
	}
	return rec, true
}

// priorTrashings reports every OTHER trash entry (by receipt) whose
// OriginalPath matches originalPath — the informational "already trashed at
// <ts>" line FR-048a's second-copy rule describes. Best-effort: a receipt
// that cannot be read is simply not counted, never a hard failure of the
// trash that is otherwise complete.
func (tr *Trasher) priorTrashings(fsys LinkFS, originalPath, excludeID string) ([]string, error) {
	ids, err := tr.listTrashDirs(fsys)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		if id == excludeID {
			continue
		}
		receipt, ok := tr.readReceipt(id)
		if ok && receipt.OriginalPath == originalPath {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ---------------------------------------------------------------------------
// Restore
// ---------------------------------------------------------------------------

// trashCopy is one candidate a restore might pick, discovered by checking
// whether `<timestampDir>/<originalPath>` exists — receipt-INDEPENDENT
// discovery, which is what keeps a hand-edited or missing receipt from being
// able to redirect where restore looks: the destination restore ultimately
// writes to is always ResolveContainedNoSymlink(originalPath), never a value
// read out of a receipt or off the trash directory's own layout.
type trashCopy struct {
	TrashID    string
	FileAbs    string
	HasReceipt bool
	Receipt    trashReceipt
}

// findTrashCopies enumerates every trash entry holding a copy of
// originalPath, most recently trashed first.
func (tr *Trasher) findTrashCopies(fsys LinkFS, originalPath string) ([]trashCopy, error) {
	ids, err := tr.listTrashDirs(fsys)
	if err != nil {
		return nil, err
	}
	var out []trashCopy
	for _, id := range ids {
		fileAbs := filepath.Join(tr.Root.Path(), MarkerDirName, trashDirName, id, filepath.FromSlash(originalPath))
		info, statErr := fsys.Lstat(fileAbs)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		receipt, ok := tr.readReceipt(id)
		out = append(out, trashCopy{TrashID: id, FileAbs: fileAbs, HasReceipt: ok, Receipt: receipt})
	}
	// Lexicographic descending sort matches chronological descending order:
	// the fixed-width timestamp prefix dominates the comparison, and a
	// collision suffix ("-2", "-3", ...) only ever distinguishes entries that
	// share that prefix, sorting the later-created one first (see
	// allocateTrashDir; the one documented limit is a double-digit-or-higher
	// suffix count within one second, which this package does not expect to
	// occur in practice).
	sort.Slice(out, func(i, j int) bool { return out[i].TrashID > out[j].TrashID })
	return out, nil
}

// findLiveRecordByID walks the LIVE collection (never descending into
// .omnipus-vault/, per WalkContained) looking for a note whose declared
// identifier equals id — FR-038a's collision check. It is a full walk rather
// than an index lookup: correctness of a data-loss-preventing refusal is
// worth more here than the cost of one restore, and the properties index
// (pkg/records/propindex) is not reachable from this package without an
// import cycle (see integrity.go's PropertyIndexReader doc comment).
func (tr *Trasher) findLiveRecordByID(fsys LinkFS, id string) (foundPath string, found bool, err error) {
	wr, werr := WalkContained(fsys, tr.Root)
	if werr != nil {
		return "", false, werr
	}
	for _, rel := range wr.Files {
		if !IsMarkdownPath(rel) {
			continue
		}
		abs := filepath.Join(tr.Root.Path(), filepath.FromSlash(rel))
		data, rerr := os.ReadFile(abs) //nolint:gosec // rel is a WalkContained result, already proven inside the root
		if rerr != nil {
			// An unreadable note cannot be PROVEN to collide; it is also not
			// content this collection can address at all today, so it is
			// skipped rather than failing the whole restore.
			continue
		}
		if records.ParseRecord(rel, data).ID() == id {
			return rel, true, nil
		}
	}
	return "", false, nil
}

// Restore puts the most recently trashed copy of `path` (or the copy named
// by TrashedAt) back at its original location (design note §4).
func (tr *Trasher) Restore(req RestoreRequest) (*RestoreResult, error) {
	fsys := tr.fs()

	orig, err := cleanNoteArg(req.Path)
	if err != nil {
		tr.emit(trashOpRestore, "refused", nil, err.Error())
		return nil, err
	}
	orig = ensureMarkdown(orig)

	if rerr := authorRefuseReserved(orig); rerr != nil {
		tr.emit(trashOpRestore, "refused", []string{orig}, rerr.Error())
		return nil, rerr
	}

	copies, err := tr.findTrashCopies(fsys, orig)
	if err != nil {
		tr.emit(trashOpRestore, "refused", []string{orig}, err.Error())
		return nil, err
	}
	if len(copies) == 0 {
		// NOT "knowledge_describe reports the trash contents" — it does not.
		// knowledge_describe renders four sections (index, types, views,
		// templates) and none of them reads the trash directory, so the old
		// remedy sent the caller to a surface that would never answer, which
		// is a dead end dressed up as a next step.
		rerr := fmt.Errorf("%w: no trashed note at %s; `path` must be the note's ORIGINAL path from before it was trashed, not a path inside the trash", ErrRestoreNotFound, orig)
		tr.emit(trashOpRestore, "refused", []string{orig}, rerr.Error())
		return nil, rerr
	}

	wantID := strings.TrimSpace(req.TrashedAt)
	chosen := &copies[0]
	if wantID != "" {
		chosen = nil
		for i := range copies {
			if copies[i].TrashID == wantID {
				chosen = &copies[i]
				break
			}
		}
		if chosen == nil {
			avail := make([]string, len(copies))
			for i, c := range copies {
				avail[i] = c.TrashID
			}
			rerr := fmt.Errorf("%w: no trashed note at %s trashed at %s; available: %s",
				ErrRestoreNotFound, orig, wantID, strings.Join(avail, ", "))
			tr.emit(trashOpRestore, "refused", []string{orig}, rerr.Error())
			return nil, rerr
		}
	}

	// FR-048b: the reconstructed destination is resolved through
	// ResolveContainedNoSymlink and refused on escape, `..`, an out-of-root
	// symlink, or a reserved location (already refused above by name).
	destAbs, err := tr.Root.ResolveContainedNoSymlink(fsys, orig)
	if err != nil {
		tr.emit(trashOpRestore, "refused", []string{orig}, err.Error())
		return nil, err
	}
	if _, statErr := fsys.Lstat(destAbs); statErr == nil {
		rerr := fmt.Errorf("%w: %s — move or rename it first", ErrRestoreDestinationExists, orig)
		tr.emit(trashOpRestore, "refused", []string{orig}, rerr.Error())
		return nil, rerr
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		tr.emit(trashOpRestore, "refused", []string{orig}, statErr.Error())
		return nil, statErr
	}

	content, rerr := os.ReadFile(chosen.FileAbs) //nolint:gosec // chosen.FileAbs is built from a trash directory this package enumerated, joined with the caller's own cleaned path
	if rerr != nil {
		wrapped := fmt.Errorf("knowledge: read trashed copy of %q: %w", orig, rerr)
		tr.emit(trashOpRestore, "refused", []string{orig}, wrapped.Error())
		return nil, wrapped
	}
	rec := records.ParseRecord(orig, content)
	if id := rec.ID(); id != "" {
		collidingPath, found, cerr := tr.findLiveRecordByID(fsys, id)
		if cerr != nil {
			tr.emit(trashOpRestore, "refused", []string{orig}, cerr.Error())
			return nil, cerr
		}
		if found {
			crerr := fmt.Errorf("%w: %s already holds identifier %q, the identifier the trashed copy of %s carries",
				ErrRestoreIdentifierCollision, collidingPath, id, orig)
			tr.emit(trashOpRestore, "refused", []string{orig, collidingPath}, crerr.Error())
			return nil, crerr
		}
	}

	var result RestoreResult
	lockErr := WithNoteWriteLock(tr.lockConfig(), orig, func() error {
		if mkErr := os.MkdirAll(filepath.Dir(destAbs), markerDirPerm); mkErr != nil {
			return fmt.Errorf("knowledge: create %q: %w", path.Dir(orig), mkErr)
		}
		if mvErr := moveFile(chosen.FileAbs, destAbs); mvErr != nil {
			return fmt.Errorf("knowledge: restore %q: %w", orig, mvErr)
		}
		if chosen.HasReceipt {
			receiptAbs := filepath.Join(tr.Root.Path(), MarkerDirName, trashDirName, chosen.TrashID, trashReceiptFileName)
			if remErr := os.Remove(receiptAbs); remErr != nil && !errors.Is(remErr, fs.ErrNotExist) {
				slog.Warn("knowledge: could not remove trash receipt after restore", "trash_id", chosen.TrashID, "error", remErr)
			}
		}
		if pruneErr := removeEmptyDirsRecursively(filepath.Join(tr.Root.Path(), MarkerDirName, trashDirName, chosen.TrashID)); pruneErr != nil {
			slog.Warn("knowledge: could not prune empty trash directory", "trash_id", chosen.TrashID, "error", pruneErr)
		}

		resolvedLinks := 0
		if graph, gerr := BuildLinkGraph(fsys, tr.Root); gerr == nil {
			resolvedLinks = len(graph.Backlinks(orig))
		} else {
			slog.Warn("knowledge: could not recompute backlinks after restore", "path", orig, "error", gerr)
		}

		other := make([]string, 0, len(copies)-1)
		for _, c := range copies {
			if c.TrashID != chosen.TrashID {
				other = append(other, c.TrashID)
			}
		}
		result = RestoreResult{
			OriginalPath: orig, RestoredFrom: chosen.TrashID, OtherAvailable: other,
			RecordType: rec.TypeName(), RecordID: rec.ID(), ResolvedLinksCount: resolvedLinks,
		}
		return nil
	})
	if lockErr != nil {
		tr.emit(trashOpRestore, "refused", []string{orig}, lockErr.Error())
		return nil, lockErr
	}
	tr.emit(trashOpRestore, "applied", []string{orig, path.Join(MarkerDirName, trashDirName, chosen.TrashID)}, "")
	return &result, nil
}

// removeEmptyDirsRecursively removes dir, and any directory under it, that is
// empty after this restore's move — cheap tidiness, not a correctness
// requirement (a leftover empty directory under .omnipus-vault/trash/ is
// invisible to every walker and harmless if it survives). Errors are reported
// to the caller as a warning rather than failing the restore, which has
// already succeeded by the time this runs.
func removeEmptyDirsRecursively(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			if rErr := removeEmptyDirsRecursively(filepath.Join(dir, e.Name())); rErr != nil {
				return rErr
			}
		}
	}
	entries, err = os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return os.Remove(dir)
	}
	return nil
}
