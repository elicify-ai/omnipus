// Authoring: creating and editing notes without ever silently losing what the
// operator wrote (ADR-067 US-12, US-14, US-15; FR-100..FR-102, FR-105..FR-107,
// FR-090).
//
// This file is the CONTENT half of the write path: it decides what bytes a new
// note starts from, and how an existing note's bytes change when one property
// is set or one section is appended. It deliberately does NOT own mutual
// exclusion — see "What this file does not do" below.
//
// # Three rules that shape everything here
//
//  1. AN EDIT IS A SPLICE, NEVER A RE-SERIALISATION. Frontmatter is edited by
//     replacing the bytes of exactly one line and copying every other byte
//     through untouched. Nothing here parses YAML into a map and writes it back
//     out: a round trip through a YAML library reorders keys, drops comments,
//     re-quotes strings and normalises anchors — all of which are "the rest of
//     the file" that FR-105 says must survive. The strongest form of that
//     requirement is testable as a byte comparison, which is exactly what
//     author_test.go asserts.
//
//  2. APPENDING ONLY EVER APPENDS. AppendSection's output always has the
//     original file as a literal byte prefix. That is a property a test can
//     assert in one line (bytes.HasPrefix) and it is impossible to satisfy
//     while destroying anything earlier in the file.
//
//  3. NAME SHAPE APPLIES TO WHAT WE CREATE, AND ONLY THEN. Reading, listing,
//     indexing and linking never apply name-shape rules (FR-0001) — the
//     operator's existing files are theirs, colons, emoji and all. Creating a
//     NEW name is Omnipus choosing a name, so the create path checks it
//     (FR-0001a) using pkg/pathsafe's rule set via the library Root helper, and
//     skips the check inside a mount (FR-0001b), where the host filesystem is
//     the only authority that matters. That decision is not re-made here: it is
//     supplied as a NameShapeCheck by the caller who knows which population the
//     destination belongs to.
//
// # What this file does NOT do, and must not start doing
//
//   - It does not IMPLEMENT locks — but it does take them. Every write below
//     runs inside WithNoteWriteLock (version.go), which is the one and only
//     implementation of D14 tier 1's mutual exclusion in this package: a
//     process-wide striped mutex plus a per-note advisory flock. An earlier
//     revision of this header said these primitives took no lock at all and
//     left that to "the write-safety layer that wraps them"; nothing ever
//     wrapped them, and the measured consequence was twelve concurrent
//     writers all told they had succeeded with one surviving on disk. A
//     layering note is not a guarantee. The lock is taken here, on the path
//     production actually calls, and the version check remains what it always
//     was — the D14 tier-3 DETECTOR for writers no lock can reach (Obsidian,
//     git, a sync agent).
//   - It does not rename, move, or rewrite links. That is the journalled
//     multi-file operation (US-13), a different unit with a different failure
//     model.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// Errors the authoring path returns. Every one of them is a sentinel so a
// caller can act on the KIND of refusal — the REST layer turns
// ErrNoteVersionStale into a KnowledgeConflictError the operator can resolve,
// and ErrNoteExists into "pick another name", and those are not the same
// conversation.
var (
	// ErrNoteExists means a create would have overwritten a file that is
	// already there. Create NEVER clobbers: on a case-insensitive filesystem
	// this also catches "Meeting.md" landing on an existing "meeting.md",
	// because the refusal comes from O_EXCL in the kernel rather than from a
	// name comparison in Go.
	ErrNoteExists = errors.New("knowledge: note already exists")

	// ErrNoteNotFound means the note an edit names is not there.
	ErrNoteNotFound = errors.New("knowledge: note not found")

	// ErrNoteNameRefused means the destination name fails the name-shape
	// rules that apply to what Omnipus CREATES (FR-0001a). It wraps the
	// pkg/pathsafe sentinel underneath, so a caller can still tell "too long"
	// from "illegal character" without parsing a message.
	ErrNoteNameRefused = errors.New("knowledge: note name refused")

	// ErrNoteVersionStale means the file changed underneath us: the version
	// token supplied with the write does not match the file's current content
	// (D14, FR-106). This is the typed conflict US-14 AS-1 requires, and it
	// names the path.
	//
	// IT IS version.go's ErrVersionConflict, not a second sentinel that means
	// the same thing. They were two, and the cost was visible: the tool layer
	// carried two conflict shapes, and the one this file produced could not
	// report actual_version because it was not a *ConflictError. One value
	// means errors.Is finds it under either name and every conflict — whoever
	// raised it — is a *ConflictError carrying the path and both tokens.
	ErrNoteVersionStale = ErrVersionConflict

	// ErrReservedLocation means the destination is inside a tool-state
	// directory — .omnipus-vault/ or .obsidian/. Notes never belong there,
	// and an agent that could write into either could rewrite the
	// collection's identity or another application's configuration.
	ErrReservedLocation = errors.New("knowledge: reserved location")

	// ErrFrontmatterUnterminated means the note opens a frontmatter block
	// that never closes. Refused rather than repaired: "repair" here means
	// guessing where the operator meant the block to end, and guessing wrong
	// turns their prose into metadata or vice versa.
	ErrFrontmatterUnterminated = errors.New("knowledge: unterminated frontmatter")

	// ErrInvalidProperty means the frontmatter key or value cannot be
	// represented on one line — a newline in either, an empty key, a key
	// carrying a colon. Refused, because writing it anyway produces a
	// frontmatter block that no longer parses, which loses every property in
	// the note and not just this one.
	ErrInvalidProperty = errors.New("knowledge: invalid frontmatter property")

	// ErrInvalidSection means the section heading is empty, multi-line, or at
	// an impossible level.
	ErrInvalidSection = errors.New("knowledge: invalid section")
)

// noteFilePerm is the mode a NEW note is created with, matching
// pkg/library's own 0o600 for files it creates. An EXISTING note keeps
// whatever mode it already had: an operator who chmodded a note to 0600 did
// so on purpose, and an edit that quietly widened it to 0644 would be a
// security regression nobody would ever see.
const noteFilePerm fs.FileMode = 0o600

// noteDirPerm is the mode intermediate directories are created with. 0o700
// rather than 0o755 for the same reason the marker directory is: this is
// Omnipus creating structure inside the operator's collection, and the
// narrower default is the one that cannot leak.
const noteDirPerm fs.FileMode = 0o700

// defaultNoteExt is appended when a create names a path with no extension at
// all. A "note" with no extension is not a note — it would be classified as an
// attachment by scan.go and never indexed as prose.
const defaultNoteExt = ".md"

// ---------------------------------------------------------------------------
// Name shape (FR-0001a / FR-0001b)
// ---------------------------------------------------------------------------

// NameShapeCheck decides whether a collection-relative path is acceptably
// NAMED for something Omnipus is about to create. It is never applied on the
// read path (FR-0001).
//
// It is a parameter rather than a fixed rule because the answer depends on the
// destination's population, which this package cannot see: a knowledge base
// reached through a mount is the operator's own disk, where FR-0001b says no
// portability rule applies, while one living in workspace storage is named by
// Omnipus and should stay portable (FR-0001c).
type NameShapeCheck func(relPath string) error

// PortableNameShape applies pkg/pathsafe's ACTIVE rule set — the build
// target's — to every segment, plus the whole-path length budget. It is the
// same rule set, reached through the same exported functions, that
// library.Root.ValidateCreateName applies to workspace storage; the rules
// themselves are decided in pkg/pathsafe and nowhere else.
//
// This is the DEFAULT when a request supplies no check, deliberately: an unset
// policy must fail closed. "No check supplied" meaning "no checking" is how a
// create path acquires a silent hole.
func PortableNameShape(relPath string) error {
	if relPath == "" || relPath == "." {
		return nil
	}
	for _, seg := range strings.Split(relPath, "/") {
		if err := pathsafe.ValidateComponent(seg); err != nil {
			return fmt.Errorf("%w: %w", ErrNoteNameRefused, err)
		}
	}
	if err := pathsafe.ValidateRelPathLength(relPath); err != nil {
		return fmt.Errorf("%w: %w", ErrNoteNameRefused, err)
	}
	return nil
}

// OperatorNameShape applies NO name-shape rule. It is the correct check for a
// collection reached through a mount (FR-0001b): those files are the
// operator's, the host filesystem is the authority on what it will accept, and
// it says so in its own error. Addressing safety — traversal, absolute paths,
// control characters — is NOT part of name shape and still applies; it is
// enforced by library.CleanRelPath and CollectionRoot.ResolveContained on every
// path in this package, mounted or not.
func OperatorNameShape(string) error { return nil }

// LibraryNameShape routes the decision through the Library's own enforcement
// point, library.Root.ValidateCreateName (ADR-067 FR-0001a), which already
// knows which paths are inside a mount and which are workspace storage.
//
// baseRel is the collection root's path relative to the Library root, in slash
// form. Use this whenever a *library.Root is available: it is what keeps the
// mount predicate stated ONCE, in pkg/library, instead of a sixth copy here.
func LibraryNameShape(r *library.Root, baseRel string) NameShapeCheck {
	return func(relPath string) error {
		if r == nil {
			return PortableNameShape(relPath)
		}
		if err := r.ValidateCreateName(path.Join(baseRel, relPath)); err != nil {
			return fmt.Errorf("%w: %w", ErrNoteNameRefused, err)
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// Audit (FR-090, US-15)
// ---------------------------------------------------------------------------

// AuthorOperation names the mutation being audited.
type AuthorOperation string

const (
	// AuthorOpCreate is a note creation.
	AuthorOpCreate AuthorOperation = "knowledge.note.create"
	// AuthorOpEdit is an in-place edit of an existing note.
	AuthorOpEdit AuthorOperation = "knowledge.note.edit"
)

// AuthorOutcome is what happened. There are exactly two, and "refused" is one
// of them: FR-090 requires every REFUSAL on the record too, and an audit trail
// that only holds successes cannot answer the question an operator actually
// asks after a surprise, which is "what did it try to do?".
type AuthorOutcome string

const (
	// AuthorOutcomeApplied means the bytes on disk changed.
	AuthorOutcomeApplied AuthorOutcome = "applied"
	// AuthorOutcomeRefused means nothing on disk changed and the caller got
	// an error.
	AuthorOutcomeRefused AuthorOutcome = "refused"
)

// AuthorAuditRecord is one audit entry for one authoring operation. It carries
// everything US-15 AS-1 enumerates: who, which collection, which paths, which
// operation, which outcome.
type AuthorAuditRecord struct {
	Operation AuthorOperation
	Outcome   AuthorOutcome

	// AgentID and WorkspaceID identify the actor. They come from the caller
	// (the tool layer reads them off the turn's context) rather than being
	// discovered here.
	AgentID     string
	WorkspaceID string

	// Collection is the collection's display name; Root is its real path.
	// Both are recorded because the name is what the operator recognises and
	// the path is what survives a rename of the display name.
	Collection string
	Root       string

	// Paths are the collection-relative paths this operation touched, or
	// would have touched. Plural from the start: US-15 AS-2 requires the FULL
	// set for a multi-file rewrite, and a single-path field is the shape that
	// makes that requirement impossible to satisfy later.
	Paths []string

	// Reason is the refusal's cause, empty when applied.
	Reason string

	// At is when it happened.
	At time.Time
}

// AuthorAudit receives one record per authoring operation, applied or refused.
//
// It is a narrow interface rather than a direct pkg/audit dependency so this
// package does not have to know about audit event-name registration, log
// rotation or HMAC chaining — and so a test can assert on records without a
// logger on disk. The gateway wiring adapts it to the real audit logger.
type AuthorAudit interface {
	RecordKnowledgeWrite(rec AuthorAuditRecord)
}

// AuthorAuditFunc adapts a plain function to AuthorAudit.
type AuthorAuditFunc func(rec AuthorAuditRecord)

// RecordKnowledgeWrite implements AuthorAudit.
func (f AuthorAuditFunc) RecordKnowledgeWrite(rec AuthorAuditRecord) { f(rec) }

// AuthorActor identifies who is writing, for the audit record.
type AuthorActor struct {
	AgentID     string
	WorkspaceID string
}

// ---------------------------------------------------------------------------
// Version tokens (D14, FR-106, FR-107)
// ---------------------------------------------------------------------------

// NoteContentVersion is the opaque version token for a note's bytes, as a
// plain string.
//
// It is ComputeVersionToken and nothing else. There is exactly ONE token
// format in this package — the "v1:"-prefixed one the wire contract's
// KnowledgeConflictError carries — because a token is only useful if the path
// that HANDS it out and the path that ACCEPTS it agree on what it is. They
// did not: this function used to return a bare 64-hex SHA-256 while
// ConflictError.Wire published a "v1:" token, so a token obtained from one
// write path was silently invalid on the other, which turns "send back what
// you read" into a refusal loop the caller cannot escape.
//
// It is a CONTENT hash and not an mtime, and that is the whole point of D14:
// Syncthing replicates a file with the source's mtime preserved, and several
// filesystems store mtime at one-second granularity, so two writes inside one
// timestamp tick are indistinguishable by time. Content is not.
func NoteContentVersion(src []byte) string {
	return string(ComputeVersionToken(src))
}

// ---------------------------------------------------------------------------
// Create (US-12, FR-100..FR-102)
// ---------------------------------------------------------------------------

// CreateNoteRequest describes one note to create.
type CreateNoteRequest struct {
	// RelPath is the note's path relative to the collection root, in slash
	// form. A path with no extension gets ".md".
	RelPath string

	// Template names a template in the collection's templates directory. When
	// empty, Body is used as-is.
	Template string

	// Body is the literal content for a template-less create. It is NEVER
	// placeholder-expanded: a caller-supplied body is content, not a
	// template, and expanding it would make "{{title}}" typed by a user into
	// something else.
	Body []byte

	// Title is what {{title}} expands to. Empty means the note's filename
	// stem, which is what a new note is called.
	Title string

	// Now is this operation's clock. It is the instant the time-derived
	// template placeholders render, and the audit record's timestamp.
	//
	// The zero value means different things to the two, on purpose: template
	// expansion leaves {{date}} LITERAL (a visibly unexpanded placeholder
	// beats a confidently wrong "0001-01-01" in the operator's file), while
	// the audit record falls back to time.Now() (a record with a zero
	// timestamp is worse than one stamped from the wall clock).
	Now time.Time

	// NameShape is the create-time name-shape rule. Nil means
	// PortableNameShape — fail closed, never "no check".
	NameShape NameShapeCheck

	// Audit receives the record. Nil disables auditing, which is correct for
	// a direct unit-level call and WRONG for anything an agent can reach:
	// FR-090 makes the tool layer responsible for supplying one.
	Audit AuthorAudit

	// Actor identifies the writer for the audit record.
	Actor AuthorActor

	// Lock configures D14 tier 1's mutual exclusion for this write. The
	// zero value still takes the process-wide striped mutex; it skips only
	// the cross-process advisory lock, which needs a LockDir under
	// $OMNIPUS_HOME. See NoteLockConfig.
	Lock NoteLockConfig
}

// CreateNoteResult reports what was created.
type CreateNoteResult struct {
	// RelPath is the note's final collection-relative path — which may
	// differ from the request's by the added ".md".
	RelPath string
	// AbsPath is its absolute path on disk.
	AbsPath string
	// Bytes is the size written.
	Bytes int
	// Version is the new note's version token, ready to be passed to a
	// subsequent edit with no intervening read.
	Version string
	// Template is the template used, empty when none.
	Template string
}

// CreateNote writes a new note into the collection, refusing anything that
// escapes it and refusing to overwrite anything already there.
//
// Order of refusal — each stage exists because skipping it has a specific
// consequence:
//
//  1. Addressing (library.CleanRelPath): traversal, absolute paths, control
//     characters and backslashes are refused before anything touches disk.
//  2. Reserved location: .omnipus-vault/ and .obsidian/ are tool state, not
//     places notes go.
//  3. Name shape (FR-0001a): applied AFTER the path is known and only to what
//     we are creating.
//  4. Containment (CollectionRoot.ResolveContained): resolves every symlink on
//     the way and refuses a destination that lands outside the collection —
//     the check a lexical-only implementation omits and a symlinked
//     subdirectory defeats.
//  5. O_EXCL create: the kernel, not a Stat-then-write race, decides whether
//     the file was already there.
func CreateNote(fsys LinkFS, c *Collection, req CreateNoteRequest) (CreateNoteResult, error) {
	if c == nil {
		return CreateNoteResult{}, fmt.Errorf("%w: nil collection", ErrCollectionRootInvalid)
	}
	audit := newAuthorAuditor(req.Audit, AuthorOpCreate, c, req.Actor, req.Now)

	rel, err := authorCleanNotePath(req.RelPath)
	if err != nil {
		return CreateNoteResult{}, audit.refuse([]string{req.RelPath}, err)
	}
	if rerr := authorRefuseReserved(rel); rerr != nil {
		return CreateNoteResult{}, audit.refuse([]string{rel}, rerr)
	}
	shape := req.NameShape
	if shape == nil {
		shape = PortableNameShape
	}
	if serr := shape(rel); serr != nil {
		return CreateNoteResult{}, audit.refuse([]string{rel}, serr)
	}
	root, err := NewCollectionRoot(fsys, c.Root())
	if err != nil {
		return CreateNoteResult{}, audit.refuse([]string{rel}, err)
	}
	abs, err := root.ResolveContained(fsys, rel)
	if err != nil {
		return CreateNoteResult{}, audit.refuse([]string{rel}, err)
	}

	content := req.Body
	if strings.TrimSpace(req.Template) != "" {
		raw, terr := ReadTemplate(fsys, c, req.Template)
		if terr != nil {
			return CreateNoteResult{}, audit.refuse([]string{rel}, terr)
		}
		title := req.Title
		if title == "" {
			title = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		}
		content = ExpandTemplate(raw, TemplateVars{Title: title, Now: req.Now})
	}

	// Everything that touches disk runs inside the note's tier-1 lock, for
	// the same reason an edit does: two creates of one path racing is exactly
	// the case where O_EXCL alone tells the loser "already exists" while a
	// concurrent EditNote on the same path is midway through a read.
	lock := req.Lock
	lock.CollectionRoot = c.Root()
	lockErr := WithNoteWriteLock(lock, rel, func() error {
		if mkErr := os.MkdirAll(filepath.Dir(abs), noteDirPerm); mkErr != nil {
			return fmt.Errorf("knowledge: create %q: %w", rel, mkErr)
		}
		// O_EXCL is the "never clobber" primitive. Deliberately NOT a
		// temp-file-plus-rename: rename REPLACES the destination, so the
		// atomic write helper used for edits is exactly the wrong tool for a
		// create that must fail when something is already there.
		f, oerr := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, noteFilePerm) //nolint:gosec // abs is proven contained by ResolveContained above
		if oerr != nil {
			if errors.Is(oerr, fs.ErrExist) {
				return fmt.Errorf("%w: %q", ErrNoteExists, rel)
			}
			return fmt.Errorf("knowledge: create %q: %w", rel, oerr)
		}
		if _, werr := f.Write(content); werr != nil {
			_ = f.Close()
			_ = os.Remove(abs)
			return fmt.Errorf("knowledge: write %q: %w", rel, werr)
		}
		if serr := f.Sync(); serr != nil {
			_ = f.Close()
			_ = os.Remove(abs)
			return fmt.Errorf("knowledge: sync %q: %w", rel, serr)
		}
		if cerr := f.Close(); cerr != nil {
			return fmt.Errorf("knowledge: close %q: %w", rel, cerr)
		}
		return nil
	})
	if lockErr != nil {
		return CreateNoteResult{}, audit.refuse([]string{rel}, lockErr)
	}

	audit.applied([]string{rel})
	return CreateNoteResult{
		RelPath:  rel,
		AbsPath:  abs,
		Bytes:    len(content),
		Version:  NoteContentVersion(content),
		Template: strings.TrimSpace(req.Template),
	}, nil
}

// authorCleanNotePath validates addressing and supplies the default extension.
func authorCleanNotePath(raw string) (string, error) {
	cleaned, err := library.CleanRelPath(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrOutsideCollection, raw, err)
	}
	if cleaned == "" {
		return "", fmt.Errorf("%w: %q names the collection root, not a note", ErrOutsideCollection, raw)
	}
	if path.Ext(cleaned) == "" {
		cleaned += defaultNoteExt
	}
	return cleaned, nil
}

// authorRefuseReserved refuses a destination inside a tool-state directory.
// Both names are checked at EVERY level, not just the first: a note at
// "projects/.obsidian/plugins/x.md" is inside Obsidian's configuration just as
// surely as one at the root.
func authorRefuseReserved(rel string) error {
	for _, seg := range strings.Split(rel, "/") {
		// scanSkippedDirNames is the AUTHORITY, not a hand-copied subset of it.
		//
		// This function previously tested MarkerDirName and ObsidianMarkerDirName
		// only — two of that set's four names — so a write into .git/ or .trash/
		// was accepted. Those directories are skipped by every walker, so a note
		// moved there vanishes from search, backlinks and the orphan check at
		// once: an untracked hard delete reachable through an operation named
		// "move". .trash matters most in practice, because Obsidian's own
		// soft-delete writes there, so it exists in most real vaults and is the
		// folder an agent asked to "archive" a note would reach for.
		//
		// Deriving the check from scanSkippedDirNames means adding a name to the
		// walker's skip set cannot silently open a new hole here.
		if _, reserved := scanSkippedDirNames[seg]; reserved {
			return fmt.Errorf("%w: %q is inside %s/", ErrReservedLocation, rel, seg)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Edit (US-14, FR-105..FR-107)
// ---------------------------------------------------------------------------

// NoteEdit transforms a note's bytes. Every edit in this package is a pure
// function of the note's current content, which is what makes an edit testable
// without a filesystem and re-appliable by the caller after a conflict.
type NoteEdit func(src []byte) ([]byte, error)

// EditNoteRequest describes one in-place edit.
type EditNoteRequest struct {
	// RelPath is the note's collection-relative path. NO name-shape rule is
	// applied: the file already exists, and FR-0001 says its name is the
	// operator's business.
	RelPath string

	// Edits are applied in order, each to the previous one's output.
	Edits []NoteEdit

	// ExpectVersion is the version token of the content this edit was
	// computed against, and FR-106 makes it MANDATORY: "the system MUST
	// require a version token for every write". A token that does not match
	// what is on disk is REFUSED with a *ConflictError naming the path and
	// carrying both tokens, and the file is left exactly as it was.
	//
	// EMPTY IS REFUSED TOO, and that is the requirement rather than
	// strictness for its own sake. Accepting an empty token would make
	// compare-and-swap opt-in, and every caller that forgot — including a
	// language model reading a tool description — would silently be back to
	// last-writer-wins. The refusal is not a dead end: it carries the note's
	// CURRENT token in ConflictError.Actual, so a caller with no token gets
	// one and retries. That is the whole protocol, and it costs one round
	// trip exactly once per note.
	ExpectVersion string

	// Now, Audit and Actor behave exactly as in CreateNoteRequest.
	Now   time.Time
	Audit AuthorAudit
	Actor AuthorActor

	// Lock configures D14 tier 1's mutual exclusion for this write; see
	// CreateNoteRequest.Lock and NoteLockConfig.
	Lock NoteLockConfig
}

// EditNoteResult reports what an edit did.
type EditNoteResult struct {
	RelPath string
	AbsPath string
	// Version is the token AFTER the edit — or the unchanged file's token
	// when Changed is false.
	Version string
	// PriorVersion is the token the edit was applied to.
	PriorVersion string
	// Changed is false when the edits produced byte-identical content. In
	// that case nothing is written at all, so the file's mtime is not
	// disturbed and no sync tool is woken up for a no-op.
	Changed bool
	Bytes   int
}

// EditNote reads a note, applies the edits, and writes the result atomically —
// or refuses.
//
// The concurrency discipline, precisely, and in this order because the order
// is what makes it a compare-and-SWAP rather than a compare and a hope:
//
//  1. The note's tier-1 lock is taken, bounded (FR-108). Everything below
//     happens inside it, so no other Omnipus writer — in this process or in
//     another one on the same machine — can interleave with any of it.
//     Without this the read, the comparison and the write are three separate
//     moments and twelve concurrent callers can all pass the comparison and
//     all write; measured, that is eleven silently lost operator edits and
//     twelve callers told they succeeded.
//  2. The file is read and hashed INSIDE the lock, and ExpectVersion must
//     equal that hash. An absent or mismatched token is a *ConflictError
//     naming the path and carrying the note's current token, the file is
//     untouched, and the refusal is audited (US-14 AS-1, AS-5; FR-106).
//  3. Immediately before writing, the file is read again and re-hashed. That
//     is the D14 tier-3 detector and it is NOT redundant with the lock: it
//     catches the writers no lock reaches — Obsidian, git, a sync agent —
//     which is the population D14 says can only be refused, never excluded.
//
// The write itself preserves the file's existing permissions and goes through
// fileutil.WriteFileAtomic (temp file, fsync, rename in the same directory) —
// the same shape ADR-067 D14 records as `ev`'s proven prior art, so a reader
// never sees a half-written note.
func EditNote(fsys LinkFS, c *Collection, req EditNoteRequest) (EditNoteResult, error) {
	if c == nil {
		return EditNoteResult{}, fmt.Errorf("%w: nil collection", ErrCollectionRootInvalid)
	}
	audit := newAuthorAuditor(req.Audit, AuthorOpEdit, c, req.Actor, req.Now)

	rel, err := library.CleanRelPath(req.RelPath)
	if err != nil || rel == "" {
		return EditNoteResult{}, audit.refuse([]string{req.RelPath},
			fmt.Errorf("%w: %q", ErrOutsideCollection, req.RelPath))
	}
	root, err := NewCollectionRoot(fsys, c.Root())
	if err != nil {
		return EditNoteResult{}, audit.refuse([]string{rel}, err)
	}
	abs, err := root.ResolveContained(fsys, rel)
	if err != nil {
		return EditNoteResult{}, audit.refuse([]string{rel}, err)
	}

	var result EditNoteResult
	lock := req.Lock
	lock.CollectionRoot = c.Root()
	lockErr := WithNoteWriteLock(lock, rel, func() error {
		fi, serr := fsys.Lstat(abs)
		if serr != nil {
			if errors.Is(serr, fs.ErrNotExist) {
				return fmt.Errorf("%w: %q", ErrNoteNotFound, rel)
			}
			return fmt.Errorf("knowledge: stat %q: %w", rel, serr)
		}
		// lstat, and a regular-file requirement: an edit must never follow a
		// symlink out of the collection, and must never truncate a directory
		// or a device node into a "note".
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%w: %q is not a regular file", ErrNoteNotFound, rel)
		}

		before, rerr := authorReadAll(fsys, abs)
		if rerr != nil {
			return fmt.Errorf("knowledge: read %q: %w", rel, rerr)
		}
		priorVersion := NoteContentVersion(before)

		// FR-106's compare, delegated to the one function that implements it,
		// so "no token" and "wrong token" are refused by the same rule the
		// Writer uses and produce the same typed error with the same fields.
		if conflict := checkVersion(rel, VersionToken(req.ExpectVersion), NoteVersion{
			Path: rel, Exists: true, Token: VersionToken(priorVersion),
			Size: fi.Size(), ModTime: fi.ModTime(),
		}); conflict != nil {
			return conflict
		}

		after := before
		for _, edit := range req.Edits {
			if edit == nil {
				continue
			}
			next, eerr := edit(after)
			if eerr != nil {
				return eerr
			}
			after = next
		}
		if bytes.Equal(after, before) {
			result = EditNoteResult{
				RelPath: rel, AbsPath: abs,
				Version: priorVersion, PriorVersion: priorVersion,
				Changed: false, Bytes: len(before),
			}
			return nil
		}

		// The tier-3 detector. Inside the lock this can only fire for a writer
		// the lock does not reach, which is exactly the population it is for.
		current, cerr := authorReadAll(fsys, abs)
		if cerr != nil {
			return fmt.Errorf("knowledge: re-read %q: %w", rel, cerr)
		}
		if !bytes.Equal(current, before) {
			return &ConflictError{
				Path:     rel,
				Expected: VersionToken(req.ExpectVersion),
				Actual:   ComputeVersionToken(current),
			}
		}

		// The same widening seam version.go's Writer uses, and the same
		// reason: the defect this lock prevents has a window measured in
		// microseconds, and a concurrency test that does not widen it passes
		// just as happily against an implementation with no lock at all.
		if hookBeforeApplyWrite != nil {
			hookBeforeApplyWrite()
		}
		if werr := fileutil.WriteFileAtomic(abs, after, fi.Mode().Perm()); werr != nil {
			return fmt.Errorf("knowledge: write %q: %w", rel, werr)
		}
		result = EditNoteResult{
			RelPath: rel, AbsPath: abs,
			Version: NoteContentVersion(after), PriorVersion: priorVersion,
			Changed: true, Bytes: len(after),
		}
		return nil
	})
	if lockErr != nil {
		return EditNoteResult{}, audit.refuse([]string{rel}, lockErr)
	}
	audit.applied([]string{rel})
	return result, nil
}

// authorReadAll reads a whole note through the LinkFS seam.
//
// There is no size cap here and there must not be one: FR-034a says a note of
// any size is handled in full, and a cap on the EDIT path would mean the one
// operation that rewrites the file silently refuses to touch the operator's
// largest notes.
func authorReadAll(fsys LinkFS, abs string) ([]byte, error) {
	f, err := fsys.Open(abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// ---------------------------------------------------------------------------
// The two edits (FR-105)
// ---------------------------------------------------------------------------

// SetProperty returns an edit that sets ONE frontmatter property, leaving
// every other byte of the note exactly as it was.
//
// Behaviour, in the three cases that exist:
//
//   - The key is already present at the top level: its line's value is
//     replaced, and any continuation lines belonging to it (an indented block,
//     a block sequence) are removed, because they were part of the old value.
//     Line ending and key ordering are preserved.
//   - The key is absent: a new line is inserted at the END of the frontmatter
//     block. Appending rather than inserting alphabetically is deliberate —
//     re-sorting an operator's frontmatter is a destructive reorder that no
//     requirement asked for.
//   - There is no frontmatter at all: a block is created at the top of the
//     file and the original content follows it, unchanged.
func SetProperty(key, value string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if err := authorValidatePropertyKey(key); err != nil {
			return nil, err
		}
		encoded, err := authorEncodeScalar(value)
		if err != nil {
			return nil, err
		}
		block, err := fmParse(src)
		if err != nil {
			return nil, err
		}
		if !block.present {
			eol := authorDominantEOL(src)
			var out bytes.Buffer
			out.WriteString("---" + eol)
			out.WriteString(key + ": " + encoded + eol)
			out.WriteString("---" + eol)
			if len(src) > 0 {
				// One blank line between the new block and the note's
				// existing first line, so the note still reads as prose
				// rather than as a run-on with the fence.
				out.WriteString(eol)
			}
			out.Write(src)
			return out.Bytes(), nil
		}
		return fmSpliceKey(src, block, key, encoded)
	}
}

// AppendSection returns an edit that appends a level-2 section to the note.
func AppendSection(heading, body string) NoteEdit { return AppendSectionAt(2, heading, body) }

// AppendSectionAt returns an edit that appends a section at the given heading
// level (1-6).
//
// The output ALWAYS has the input as a literal byte prefix. That is the whole
// safety property of this operation and it is asserted as such in the tests:
// an append that can be shown never to modify a preceding byte cannot destroy
// frontmatter, cannot corrupt a code fence, and cannot lose a paragraph, no
// matter what the note contained.
//
// body is inserted verbatim. It is never parsed, expanded, or interpreted —
// text that looks like a template placeholder or an instruction stays exactly
// what the caller passed.
func AppendSectionAt(level int, heading, body string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if level < 1 || level > 6 {
			return nil, fmt.Errorf("%w: heading level %d is not between 1 and 6", ErrInvalidSection, level)
		}
		h := strings.TrimSpace(heading)
		if h == "" {
			return nil, fmt.Errorf("%w: empty heading", ErrInvalidSection)
		}
		if strings.ContainsAny(h, "\r\n") {
			return nil, fmt.Errorf("%w: heading spans more than one line", ErrInvalidSection)
		}
		eol := authorDominantEOL(src)

		var out bytes.Buffer
		out.Write(src)
		if len(src) > 0 {
			if !authorEndsWithNewline(src) {
				out.WriteString(eol)
			}
			if !authorEndsWithBlankLine(src) {
				out.WriteString(eol)
			}
		}
		out.WriteString(strings.Repeat("#", level) + " " + h + eol)
		if body != "" {
			out.WriteString(eol)
			out.WriteString(body)
			if !authorEndsWithNewline([]byte(body)) {
				out.WriteString(eol)
			}
		}
		return out.Bytes(), nil
	}
}

// ---------------------------------------------------------------------------
// Frontmatter, as bytes
// ---------------------------------------------------------------------------

// fmBlock locates a note's YAML frontmatter in BYTE offsets. Nothing here
// understands YAML beyond "a fence line, some lines, a fence line" — which is
// exactly as much as a splice needs, and exactly as little as it takes to
// guarantee the rest of the block is copied through untouched.
type fmBlock struct {
	present bool
	// innerStart is the offset of the first byte after the opening fence's
	// line terminator; innerEnd is the offset of the first byte of the
	// closing fence line. The bytes between them are the block's properties.
	innerStart int
	innerEnd   int
	// eol is the line ending the opening fence used.
	eol string
}

// fmParse locates the frontmatter block, if there is one.
//
// A block exists only when the note's FIRST line is exactly "---" (trailing
// whitespace tolerated). A "---" further down is a horizontal rule, and
// treating it as frontmatter would move a slab of the operator's prose into
// metadata.
func fmParse(src []byte) (fmBlock, error) {
	first, firstEnd, ok := authorLineAt(src, 0)
	if !ok {
		return fmBlock{}, nil
	}
	if strings.TrimRight(string(first), " \t\r") != "---" {
		return fmBlock{}, nil
	}
	eol := "\n"
	if bytes.HasSuffix(src[:firstEnd], []byte("\r\n")) {
		eol = "\r\n"
	}
	for pos := firstEnd; pos < len(src); {
		line, end, lok := authorLineAt(src, pos)
		if !lok {
			break
		}
		trimmed := strings.TrimRight(string(line), " \t\r")
		if trimmed == "---" || trimmed == "..." {
			return fmBlock{present: true, innerStart: firstEnd, innerEnd: pos, eol: eol}, nil
		}
		pos = end
	}
	return fmBlock{}, fmt.Errorf("%w: opening fence has no closing fence", ErrFrontmatterUnterminated)
}

// fmSpliceKey replaces or inserts one property inside an existing block.
func fmSpliceKey(src []byte, block fmBlock, key, encoded string) ([]byte, error) {
	keyStart, keyEnd, found := fmFindKey(src, block, key)
	if !found {
		// Insert immediately before the closing fence, preserving the block's
		// existing order.
		var out bytes.Buffer
		out.Write(src[:block.innerEnd])
		out.WriteString(key + ": " + encoded + block.eol)
		out.Write(src[block.innerEnd:])
		return out.Bytes(), nil
	}
	// Preserve the replaced line's own terminator: a file with CRLF endings
	// must not acquire a single LF line in the middle of its frontmatter.
	lineEOL := block.eol
	if keyEnd >= 2 && bytes.HasSuffix(src[:keyEnd], []byte("\r\n")) {
		lineEOL = "\r\n"
	} else if keyEnd >= 1 && src[keyEnd-1] == '\n' {
		lineEOL = "\n"
	}
	var out bytes.Buffer
	out.Write(src[:keyStart])
	out.WriteString(key + ": " + encoded + lineEOL)
	out.Write(src[keyEnd:])
	return out.Bytes(), nil
}

// fmFindKey returns the byte range of the line (and any continuation lines)
// belonging to a TOP-LEVEL key in the block.
//
// Top-level means column zero. An indented "key:" is a property of some other
// mapping — replacing it because the names matched would edit a nested value
// the caller never named, which is precisely the "destroyed the rest of the
// file" failure FR-105 forbids.
func fmFindKey(src []byte, block fmBlock, key string) (start, end int, found bool) {
	prefix := key + ":"
	for pos := block.innerStart; pos < block.innerEnd; {
		line, lineEnd, ok := authorLineAt(src, pos)
		if !ok {
			break
		}
		text := string(line)
		if strings.HasPrefix(text, prefix) {
			// Consume the continuation lines that carry this key's value:
			// anything indented, and a block sequence written at column zero
			// ("- item"), both of which YAML attaches to the key above.
			consumed := lineEnd
			for consumed < block.innerEnd {
				nextLine, nextEnd, nok := authorLineAt(src, consumed)
				if !nok {
					break
				}
				nt := string(nextLine)
				if strings.HasPrefix(nt, " ") || strings.HasPrefix(nt, "\t") || strings.HasPrefix(nt, "- ") {
					consumed = nextEnd
					continue
				}
				break
			}
			return pos, consumed, true
		}
		pos = lineEnd
	}
	return 0, 0, false
}

// authorLineAt returns the line beginning at pos WITHOUT its terminator, the
// offset just past its terminator, and whether there was a line at all.
func authorLineAt(src []byte, pos int) (line []byte, next int, ok bool) {
	if pos >= len(src) {
		return nil, pos, false
	}
	rest := src[pos:]
	idx := bytes.IndexByte(rest, '\n')
	if idx < 0 {
		return rest, len(src), true
	}
	return rest[:idx], pos + idx + 1, true
}

// authorDominantEOL reports the line ending to use for text this package adds.
// A file that already uses CRLF keeps CRLF: mixing terminators inside one file
// is a diff the operator did not ask for and some editors "helpfully" rewrite
// the whole file to fix.
func authorDominantEOL(src []byte) string {
	if bytes.Contains(src, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

func authorEndsWithNewline(src []byte) bool {
	return len(src) > 0 && src[len(src)-1] == '\n'
}

// authorEndsWithBlankLine reports whether src already ends with an empty line,
// so an append does not accumulate blank lines on repeated use.
func authorEndsWithBlankLine(src []byte) bool {
	if !authorEndsWithNewline(src) {
		return false
	}
	trimmed := src[:len(src)-1]
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\r' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 {
		return true
	}
	idx := bytes.LastIndexByte(trimmed, '\n')
	last := trimmed[idx+1:]
	return strings.TrimSpace(string(last)) == ""
}

// authorValidatePropertyKey refuses a key that cannot be written as a plain
// YAML key on one line.
func authorValidatePropertyKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidProperty)
	}
	if key != strings.TrimSpace(key) {
		return fmt.Errorf("%w: key %q has leading or trailing whitespace", ErrInvalidProperty, key)
	}
	if strings.ContainsAny(key, ":\r\n") {
		return fmt.Errorf("%w: key %q contains a colon or a line break", ErrInvalidProperty, key)
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: key contains control character %#U", ErrInvalidProperty, r)
		}
	}
	if strings.HasPrefix(key, "#") || strings.HasPrefix(key, "-") {
		return fmt.Errorf("%w: key %q starts with a YAML structural character", ErrInvalidProperty, key)
	}
	return nil
}

// authorEncodeScalar renders value as a YAML scalar that reads back as the
// same string.
//
// The rule is conservative on purpose: anything that could possibly be
// re-interpreted (a leading structural character, an embedded ": ", a trailing
// space, something that looks like a number or a boolean) is double-quoted.
// Over-quoting is invisible to a reader; under-quoting turns a wikilink
// "[[Note]]" into a YAML flow sequence and loses the value.
//
// A newline in a value is REFUSED rather than folded into a block scalar:
// writing it wrong destroys every property after it, and this primitive has no
// requirement to carry multi-line values.
func authorEncodeScalar(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("%w: value spans more than one line", ErrInvalidProperty)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: value contains control character %#U", ErrInvalidProperty, r)
		}
	}
	if authorNeedsQuoting(value) {
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range value {
			switch r {
			case '"':
				b.WriteString(`\"`)
			case '\\':
				b.WriteString(`\\`)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
		return b.String(), nil
	}
	return value, nil
}

// authorYAMLReserved are the plain scalars YAML 1.1 readers turn into
// something other than a string. Quoting them keeps "no" the word rather than
// the boolean.
var authorYAMLReserved = map[string]bool{
	"true": true, "false": true, "yes": true, "no": true,
	"on": true, "off": true, "null": true, "~": true,
	"True": true, "False": true, "Yes": true, "No": true,
	"On": true, "Off": true, "Null": true, "NULL": true, "TRUE": true, "FALSE": true,
}

func authorNeedsQuoting(value string) bool {
	if value == "" {
		return true
	}
	if value != strings.TrimSpace(value) {
		return true
	}
	if authorYAMLReserved[value] {
		return true
	}
	if strings.Contains(value, ": ") || strings.Contains(value, " #") || strings.HasSuffix(value, ":") {
		return true
	}
	switch value[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
		return true
	}
	if authorLooksNumeric(value) {
		return true
	}
	return false
}

// authorLooksNumeric reports whether a plain scalar would be read back as a
// number rather than a string.
func authorLooksNumeric(value string) bool {
	hasDigit := false
	for i, r := range value {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '.' || r == '_':
		case (r == '+' || r == '-') && i == 0:
		case (r == 'e' || r == 'E') && hasDigit:
		default:
			return false
		}
	}
	return hasDigit
}

// ---------------------------------------------------------------------------
// Audit plumbing
// ---------------------------------------------------------------------------

// authorAuditor stamps and emits one record per operation. It exists so that
// EVERY exit from CreateNote and EditNote goes through the same two calls —
// refuse() or applied() — rather than each refusal site remembering to audit
// itself. A refusal that forgot to audit is the exact failure FR-090 names,
// and it is invisible in review.
type authorAuditor struct {
	sink AuthorAudit
	base AuthorAuditRecord
}

func newAuthorAuditor(sink AuthorAudit, op AuthorOperation, c *Collection, actor AuthorActor, now time.Time) authorAuditor {
	at := now
	if at.IsZero() {
		at = time.Now()
	}
	rec := AuthorAuditRecord{
		Operation:   op,
		AgentID:     actor.AgentID,
		WorkspaceID: actor.WorkspaceID,
		At:          at,
	}
	if c != nil {
		rec.Collection = c.DisplayName()
		rec.Root = c.Root()
	}
	return authorAuditor{sink: sink, base: rec}
}

// refuse records a refusal and returns the error unchanged, so every refusal
// site can read `return X{}, audit.refuse(paths, err)` and cannot accidentally
// return an audited error without auditing it.
func (a authorAuditor) refuse(paths []string, err error) error {
	if a.sink != nil {
		rec := a.base
		rec.Outcome = AuthorOutcomeRefused
		rec.Paths = append([]string(nil), paths...)
		if err != nil {
			rec.Reason = err.Error()
		}
		a.sink.RecordKnowledgeWrite(rec)
	}
	return err
}

func (a authorAuditor) applied(paths []string) {
	if a.sink == nil {
		return
	}
	rec := a.base
	rec.Outcome = AuthorOutcomeApplied
	rec.Paths = append([]string(nil), paths...)
	a.sink.RecordKnowledgeWrite(rec)
}
