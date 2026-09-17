// rest_library_write.go: Create, rename, move, delete library entries — the write doors (whole-file save with compare-and-swap, binary save, upload, mkdir, create vault, rename, move/copy, delete) and the write machinery only those doors use (version-token validation, conflict body, note write locks, post-save index refresh, write audit/refusal records)

package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/vaultimport"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// checkCreateName applies the DESTINATION root's name-shape rules to rel and
// writes the 400 itself when they refuse, returning ok=false (ADR-067
// FR-0001a). Every create/rename handler calls it on its destination path,
// after the root is open.
//
// Why a helper rather than the method inline five times: FR-0001a names
// exactly five handlers that create or rename — content-put, upload, mkdir,
// rename, transfer — and observes that "the one that forgot would silently
// accept what the other four refuse". A one-line call is the smallest thing a
// sixth handler's author can copy correctly.
//
// Two properties this signature is deliberately shaped for:
//
//   - root is the DESTINATION's root, not the caller's convenient one. For a
//     cross-workspace move or copy that is toRoot, because population
//     (workspace storage vs. mount) is a property of where the file lands.
//   - The error goes through mapLibraryErr, not a bespoke jsonErr. Root's
//     ValidateCreateName wraps ErrInvalidPath precisely so the existing 400
//     mapping covers it with no new branch; routing it anywhere else would
//     re-invent that mapping and let the two drift.
//
// Nothing on the READ path may call this. Listing, opening, downloading and
// deleting an existing file are reads of the operator's disk, and FR-0001
// removes name shape from them entirely: a file already on disk is, by
// construction, inside its own filesystem's limits, and Omnipus did not name
// it.
func checkCreateName(w http.ResponseWriter, root *library.Root, rel, op, workspaceID string) bool {
	if err := root.ValidateCreateName(rel); err != nil {
		mapLibraryErr(w, op, workspaceID, err)
		return false
	}
	return true
}

// Refusal reasons recorded on a library.write audit entry whose decision is
// NOT allow (see logLibraryWriteRefused). They exist because "the save was
// refused" on its own does not answer the question an operator opens the
// audit log to ask — a stale token, a lock they could not take, and a client
// sending the wrong SHAPE of token are three different incidents with three
// different fixes, and a single "refused" label collapses them into one.
//
// The vocabulary deliberately matches pkg/knowledge/audit.go's Mutation.
// Reason tokens ("version_conflict", "lock_timeout"), so an operator
// grepping one audit file sees ONE vocabulary across the Library door and
// the agent authoring path rather than two spellings of the same event.
const (
	// libraryRefusalVersionConflict — 409: the file's current token is not
	// the one the caller said it was replacing (EMB-002). This is the record
	// that answers "my note changed and I do not know who": it names an
	// actor who tried to write over a change they had not seen.
	libraryRefusalVersionConflict = "version_conflict"
	// libraryRefusalLockTimeout — 503: the write gave up waiting for the
	// note's tier-1 lock (FR-108's bound). Nothing was written.
	libraryRefusalLockTimeout = "lock_timeout"
	// libraryRefusalVersionMissing — 400: expect_version was absent or empty,
	// i.e. the caller never said which version it believed it was replacing.
	libraryRefusalVersionMissing = "expect_version_missing"
	// libraryRefusalVersionQuoted — 400: expect_version carried the
	// RFC-quoted wire shape instead of the bare token (EMB-007b). Split from
	// the missing case on purpose: this one is a client bug that repeats on
	// every save until someone fixes the client, and it is invisible if both
	// 400s share one label.
	libraryRefusalVersionQuoted = "expect_version_quoted"
	// libraryRefusalWriteFailed — the compare succeeded and the write itself
	// errored inside the locked closure (a case-insensitive collision, a
	// raced directory removal). Recorded with decision "error", not "deny":
	// pkg/knowledge/audit.go's MutationFailed makes the same distinction, and
	// it is the one that tells a reader whether the file on disk still needs
	// looking at.
	libraryRefusalWriteFailed = "write_failed"
)

// requireLibraryExpectVersion validates and returns the bare token from a
// whole-file save request's expect_version (EMB-001, founder ruling N2: no
// caller is exempt). Absent or empty is refused with 400. A value carrying
// the wire's RFC-quoted-strong ETag shape — a leading and trailing double
// quote — is a SHAPE error and is ALSO refused with 400, never 409
// (EMB-007b/EMB-007c): a client that forgot to strip the quotes off the
// header it read gets an actionable error it can fix, not a conflict that
// looks genuine and can never be cleared.
// expectVersion is a VALUE, not a pointer: expect_version is `required` in
// LibraryContentRequest / LibraryBinaryContentRequest, so the generated type
// cannot express absence. An omitted field therefore decodes to "" and lands in
// the same branch as an explicitly empty one — which is correct, because both
// mean the caller did not tell us which version it believes it is replacing.
//
// refusalReason names WHICH of the two 400s fired, for the caller to hand to
// logLibraryWriteRefused. It is "" when ok is true. The HTTP behaviour of
// both branches is byte-for-byte what it was before the reason was returned.
func requireLibraryExpectVersion(w http.ResponseWriter, expectVersion string) (bare, refusalReason string, ok bool) {
	v := strings.TrimSpace(expectVersion)
	if v == "" {
		jsonErr(w, http.StatusBadRequest, "expect_version is required")
		return "", libraryRefusalVersionMissing, false
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		jsonErr(w, http.StatusBadRequest,
			"expect_version must be the bare token from the ETag response header, not its quoted wire form")
		return "", libraryRefusalVersionQuoted, false
	}
	return v, "", true
}

func (e *libraryConflictError) Error() string { return e.body.Error }

// libraryConflictError wraps a typed LibraryConflictError so it can travel out
// of a knowledge.WithNoteWriteLock closure as an error and be told apart, by
// errors.As, from a *knowledge.LockTimeoutError or a library.Err* the same
// closure's own write can still produce.
type libraryConflictError struct {
	body *gen.LibraryConflictError
}

// checkLibraryVersion is the Library door's compare half of EMB-006's
// compare-and-swap — the same four-branch decision as pkg/knowledge/
// version.go's unexported checkVersion (missing token is refused upstream by
// requireLibraryExpectVersion; a token that disagrees with an existing
// file's is a conflict; TokenAbsent racing an existing file is a conflict —
// see the CAS-safety note below; a token racing a since-deleted file is a
// conflict) — reimplemented here because the Library write returns a
// LibraryConflictError, not a KnowledgeConflictError, so the two bodies
// cannot share one Wire() method. expectedBare is already validated
// non-empty and unquoted by requireLibraryExpectVersion.
//
// TokenAbsent is NOT a bypass: it asserts "I believe this file does not
// exist yet" (knowledge.TokenAbsent's own doc comment), so when current.
// Exists is true the comparison below falls straight into the ordinary
// stale-token branch and refuses with 409 — never treated as "skip the
// check". A comparison that let TokenAbsent through unconditionally would
// make every accepted write on an existing file a silent last-writer-wins,
// exactly the door EMB-006 exists to close.
func checkLibraryVersion(relPath, expectedBare string, current knowledge.NoteVersion) *libraryConflictError {
	if current.Exists {
		if expectedBare == string(current.Token) {
			return nil
		}
		return newLibraryConflictErr(relPath, expectedBare, string(current.Token))
	}
	if expectedBare == string(knowledge.TokenAbsent) {
		return nil
	}
	return newLibraryConflictErr(relPath, expectedBare, "")
}

func newLibraryConflictErr(relPath, expected, actual string) *libraryConflictError {
	body := &gen.LibraryConflictError{
		Path: relPath,
		Code: gen.LibraryConflictErrorCodeLibraryVersionConflict,
	}
	if expected != "" {
		e := expected
		body.ExpectedVersion = &e
	}
	// UAT D-126 (2026-09-13): `error` is the sentence a PERSON reads in the
	// editor's conflict banner. It used to carry the "library: " log
	// namespace, which made it read like an internal log line. The namespace
	// belongs in the log record (logLibraryWriteRefused), not the wire text.
	if actual == "" {
		body.Error = fmt.Sprintf("%s changed on disk since you opened it: it has been deleted", relPath)
	} else {
		act := actual
		body.ActualVersion = &act
		body.Error = fmt.Sprintf("%s changed on disk since you opened it", relPath)
	}
	return &libraryConflictError{body: body}
}

// handleLibraryWriteLockErr resolves the outcome of a compare-and-swap write
// performed inside knowledge.WithNoteWriteLock (EMB-006) and writes the
// matching HTTP response. nil is success — the caller continues and writes
// its own 200 body. A *libraryConflictError is written as 409 with the typed
// LibraryConflictError body (EMB-002). A *knowledge.LockTimeoutError
// (FR-108's bound) is written as 503, so a lock hang becomes an actionable
// retry rather than looking like the request hung. Anything else — most
// commonly a library.Err* from root.WriteContent (a case-insensitive
// collision, a raced directory removal) discovered inside the very same
// locked closure — falls back to mapLibraryErr, unchanged from before this
// write took a lock at all. Returns false after writing a response, ok=true
// otherwise, matching this file's existing ok-bool convention.
//
// refusalReason names WHICH of the three failure classes fired, for the
// caller to hand to logLibraryWriteRefused; it is "" when ok is true. The
// status code and body every branch writes are byte-for-byte what they were
// before the reason was returned — this function still decides the response
// and still writes it here, and the reason is only carried back out so the
// caller (which holds the request, the path and the byte count this function
// deliberately does not) can record the refusal.
func handleLibraryWriteLockErr(
	w http.ResponseWriter, op, workspaceID string, err error,
) (refusalReason string, ok bool) {
	if err == nil {
		return "", true
	}
	var conflict *libraryConflictError
	if errors.As(err, &conflict) {
		writeJSON(w, http.StatusConflict, conflict.body)
		return libraryRefusalVersionConflict, false
	}
	var timeout *knowledge.LockTimeoutError
	if errors.As(err, &timeout) {
		jsonErr(w, http.StatusServiceUnavailable,
			"timed out waiting for this file's write lock — another write is in progress; try again")
		return libraryRefusalLockTimeout, false
	}
	mapLibraryErr(w, op, workspaceID, err)
	return libraryRefusalWriteFailed, false
}

// enclosingCollectionRel returns the workspace-relative directory of the
// INNERMOST knowledge base enclosing rel — a workspace-relative FILE path
// (EMB-006a). It walks rel's ancestors, starting at its immediate parent
// directory and ending at the work-tree root itself (""), applying the SAME
// marker rule knowledge.Detect uses via detectKnowledgeBaseInRoot — the
// confined stat this file already uses for annotateKnowledgeBaseEntries —
// rather than a second, hand-rolled marker test (EMB-006a clause 1).
// Stopping at the FIRST match walking upward is what clause 2 (innermost
// wins) requires. found is false when no ancestor, the work-tree root
// included, is a knowledge base.
func enclosingCollectionRel(root *library.Root, rel string) (collRel string, found bool) {
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	for {
		if isKB, established := detectKnowledgeBaseInRoot(root, dir); established && isKB {
			return dir, true
		}
		if dir == "" {
			return "", false
		}
		dir = path.Dir(dir)
		if dir == "." {
			dir = ""
		}
	}
}

// resolveCollectionNoteLock is the ONE derivation, for a workspace-relative
// note path, of the enclosing knowledge base and the D14 tier-1 lock both
// Library doors need: the whole-file save door (resolveLibraryLock) and the
// rename/delete knowledge-cascade door (libraryNoteInCollection,
// rest_library_knowledge_cascade.go) used to carry two independent copies of
// these steps, and two copies of one rule are how a split lock happens —
// change one and not the other, and a Library save races a Library rename
// over the same note on two different locks while every single-door test
// stays green (round-3 cut list, 2026-09-14 review).
//
// enclosingCollectionRel does the ancestor walk (innermost collection wins);
// knowledge.OpenCollection is the SAME call the agent write path takes
// (AuthoringDeps.begin, pkg/knowledge/authoring_tools.go), so the
// CollectionRoot string produced here is byte-identical to col.Root there,
// and so is the LockDir knowledge.LockDirFor derives from it (SC-001b).
//
// A path with no enclosing knowledge base is reported as col == nil — the
// caller decides what degraded mode means for its door.
func resolveCollectionNoteLock(root *library.Root, home, rel string) (collRel string, col *knowledge.Collection, lock knowledge.NoteLockConfig, relInCol string, err error) {
	collRel, found := enclosingCollectionRel(root, rel)
	if !found {
		return "", nil, knowledge.NoteLockConfig{}, "", nil
	}
	col, err = knowledge.OpenCollection(root.HostPath(collRel))
	if err != nil {
		return "", nil, knowledge.NoteLockConfig{}, "", fmt.Errorf("open enclosing knowledge base %q: %w", collRel, err)
	}
	lockDir, err := knowledge.LockDirFor(home, col.Root())
	if err != nil {
		return "", nil, knowledge.NoteLockConfig{}, "", fmt.Errorf("resolve write lock directory for %q: %w", collRel, err)
	}
	return collRel, col,
		knowledge.NoteLockConfig{CollectionRoot: col.Root(), LockDir: lockDir},
		relWithinCollection(collRel, rel), nil
}

// resolveLibraryLock derives the D14 tier-1 lock a whole-file Library write
// must take before its compare-and-swap (EMB-006), so a Library save and an
// agent's EditNote over the SAME note can never believe they hold different
// locks: same collection root, same lock directory, same collection-relative
// path (SC-001b). The derivation itself is resolveCollectionNoteLock — the
// one shared rule, also used by the knowledge-cascade doors, so the save
// door and the rename/delete door can never drift apart.
//
// Where no ancestor is a knowledge base, EMB-006's degraded mode applies:
// CollectionRoot and LockDir are both empty — in-process serialisation
// only, no cross-process advisory lock (G7c). lockRel is workspaceID-
// qualified in that case purely so two DIFFERENT workspaces' degraded
// writes never share one striped-mutex key by coincidence; EMB-006a has no
// equivalent unenclosed-file case on the agent side to match.
func resolveLibraryLock(root *library.Root, home, workspaceID, rel string) (knowledge.NoteLockConfig, string, error) {
	_, col, lock, relInCol, err := resolveCollectionNoteLock(root, home, rel)
	if err != nil {
		return knowledge.NoteLockConfig{}, "", err
	}
	if col == nil {
		return knowledge.NoteLockConfig{}, workspaceID + "/" + rel, nil
	}
	return lock, relInCol, nil
}

func (a *restAPI) handleLibraryEntryDelete(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		jsonErr(w, http.StatusBadRequest, "path is required")
		return
	}
	rel, err := library.CleanRelPath(rawPath)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	// UAT #701 / D-123: a note, an attachment or a whole folder inside a
	// knowledge base goes to that knowledge base's trash (restorable), the
	// way the agent door deletes — see rest_library_knowledge_cascade.go.
	note, governed, noteErr := a.libraryManagedEntryInCollection(root, rel)
	if noteErr != nil {
		mapLibraryErr(w, "delete entry", workspaceID, noteErr)
		return
	}
	if governed {
		a.trashNoteInCollection(w, r, workspaceID, note, rel)
		return
	}

	if err := root.Delete(rel); err != nil {
		mapLibraryErr(w, "delete entry", workspaceID, err)
		return
	}
	// U-58: deleting a knowledge base's last marker demotes the folder, so
	// stop indexing it now rather than at the next restart.
	a.releaseKnowledgeBaseIfDemoted(root, rel)
	// ADR-067 FR-003d: the granted path is gone, so any preview token naming it
	// — or naming something beneath it — must stop working now rather than in
	// fifteen minutes. InvalidatePath covers the beneath-it half: deleting the
	// directory "reports" also kills a bundle token scoped to "reports/q3".
	a.revokePreviewTokensForPath(workspaceID, rel)
	a.logLibraryAudit(r, "library.delete", workspaceID, map[string]any{"path": rel})
	// D-107: other tabs' listings still show the entry that just vanished.
	a.emitLibraryChange(workspaceID, rel, "delete")
	w.WriteHeader(http.StatusNoContent)
}

// --- ADR-083 Step 0: version tokens, compare-and-swap, the shared write lock ---

// libraryWriteRaceHook is a TEST SEAM: nil in production. When set, it runs
// once inside handleLibraryContentPut/handleLibraryContentBinaryPut's locked
// closure, between a whole-file write's version comparison SUCCEEDING and
// the write itself landing — the exact gap EMB-006 exists to close ("a
// comparison followed by an unheld write is not a compare-and-swap"). A
// concurrency test that does not widen this gap passes just as happily
// against an implementation that never took a lock at all. Mirrors
// pkg/knowledge/version.go's hookBeforeApplyWrite (same reasoning, same
// shape); that one is unexported to its package and specific to
// Writer.WriteNote, so it cannot be reused from here.
var libraryWriteRaceHook func()

func (a *restAPI) handleLibraryContentPut(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.LibraryContentRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LibraryContentRequest", &req, validateEnabled) {
		return
	}
	if len(req.Content) > library.MaxContentBytes {
		jsonErr(w, http.StatusBadRequest, "content exceeds the 10 MB limit")
		return
	}
	rel, err := library.CleanRelPath(req.Path)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	// ADR-083 EMB-001, founder ruling N2: no caller is exempt. Validated
	// here in the handler, not in the schema — expect_version stays
	// contract-optional until a follow-up flips it (owned elsewhere).
	expectedBare, refusal, ok := requireLibraryExpectVersion(w, req.ExpectVersion)
	if !ok {
		a.logLibraryWriteRefused(r, workspaceID, rel, refusal, false, len(req.Content))
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	if !checkCreateName(w, root, rel, "put content", workspaceID) {
		return
	}

	lockCfg, lockRel, lockErr := resolveLibraryLock(root, a.homePath, workspaceID, rel)
	if lockErr != nil {
		logger.ErrorCF("rest", "library: resolve write lock failed",
			map[string]any{"workspace_id": workspaceID, "path": rel, "error": lockErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	content := []byte(req.Content)
	var (
		fi       os.FileInfo
		newToken knowledge.VersionToken
	)
	// EMB-006: the version comparison and the write happen inside a SINGLE
	// acquisition of the same lock the agent write path takes — a compare
	// followed by an unheld write is not a compare-and-swap. See
	// resolveLibraryLock for how the lock key (collection root, lock dir,
	// collection-relative path) is derived so it matches the agent path's.
	writeErr := knowledge.WithNoteWriteLock(lockCfg, lockRel, func() error {
		current, verErr := knowledge.ReadFileVersion(root.HostPath(rel))
		if verErr != nil {
			return verErr
		}
		if conflict := checkLibraryVersion(rel, expectedBare, current); conflict != nil {
			return conflict
		}
		if libraryWriteRaceHook != nil {
			libraryWriteRaceHook()
		}
		var writeErr error
		fi, writeErr = root.WriteContent(rel, content)
		if writeErr != nil {
			return writeErr
		}
		newToken = knowledge.ComputeVersionToken(content)
		return nil
	})
	if refusal, ok := handleLibraryWriteLockErr(w, "put content", workspaceID, writeErr); !ok {
		a.logLibraryWriteRefused(r, workspaceID, rel, refusal, false, len(content))
		return
	}

	w.Header().Set("ETag", libraryETagValue(newToken))
	a.logLibraryAudit(r, "library.write", workspaceID,
		map[string]any{"path": rel, "bytes": len(content), "binary": false})
	// C5 (Claude review 2026-09-14): a save INSIDE a knowledge base refreshes
	// that note's rows in both indexes before the caller is told the save
	// landed — the same read-your-own-write guarantee the REST record door
	// (rest_knowledge_record.go, UAT D-67) and the agent door already give.
	// The watcher is not enough here: it refreshes only the TEXT index, and
	// the properties index's count-based self-heal cannot see an edit that
	// leaves the row count unchanged, so an in-place frontmatter edit stayed
	// invisible to every view indefinitely. A `.base` save is routed to the
	// view re-derivation instead (D-119): its "index" is the view YAMLs the
	// import pipeline writes, and a raw text edit of a base must reach them
	// exactly as an import would.
	if lockCfg.CollectionRoot != "" && knowledgeIndexerSeesPath(lockRel) {
		if isLibraryBasePath(rel) {
			a.rederiveBaseViewsAfterSave(r, workspaceID, lockCfg.CollectionRoot, lockRel)
		} else if knowledge.IsMarkdownPath(rel) {
			a.refreshLibraryWriteIndexes(r, workspaceID, lockCfg.CollectionRoot, lockRel)
		}
	}
	// D-107: the file's size/mtime in other tabs' listings is now wrong (and
	// a first-time create adds a row).
	a.emitLibraryChange(workspaceID, rel, "write")
	jsonOK(w, library.EntryFromInfo(rel, fi))
}

// isLibraryBasePath reports whether rel names an Obsidian `.base` file — the
// same rule rest_knowledge_base_views.go applies at its own door
// (case-insensitive extension, matching Windows/APFS case-folding) rather
// than a second, stricter spelling that would classify the same file
// differently at two doors.
func isLibraryBasePath(rel string) bool {
	return strings.EqualFold(path.Ext(rel), ".base")
}

// knowledgeIndexerSeesPath reports whether a collection-relative path is one
// the collection WALKER indexes — every path is, except those inside the four
// directories the walk skips. The post-save index refresh and the `.base`
// re-derivation must not run for a save inside those directories (a trashed
// note re-saved through the text editor would otherwise re-enter the LIVE
// index from `.omnipus-vault/trash/`, which the next full sync would then
// strip again as drift), and equally must not be broader than the walk: a
// refresh the walker cannot confirm is a row the next sync must delete.
//
// The authority for the four names is pkg/knowledge/scan.go's
// scanSkippedDirNames, which is UNEXPORTED — the two marker names are taken
// from the exported constants the markers themselves define, and `.git` and
// `.trash` are spelled here with this comment naming their home. If that set
// grows, this one must grow with it; a future change that wants the check in
// one place should export the predicate from pkg/knowledge and delete this
// copy (it could not be made in this change without editing a file this
// change does not own).
func knowledgeIndexerSeesPath(relInCollection string) bool {
	seg := relInCollection
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	switch seg {
	case records.VaultMarkerDirName, knowledge.ObsidianMarkerDirName, ".git", ".trash":
		return false
	}
	return true
}

// refreshLibraryWriteIndexes re-derives one note's rows in the text and
// properties indexes right after a Library save landed inside a knowledge
// base — the same call, contract and failure posture as the record door's
// refreshRecordIndexes (never a refusal: the write is already on disk; a
// refresh that fails is logged at Error inside knowledge.RefreshIndexesForNote
// and named again here, and the engine's own stale-record flag remains the
// user-visible signal until the next scheduled reconcile).
func (a *restAPI) refreshLibraryWriteIndexes(r *http.Request, workspaceID, collectionRoot, relInCollection string) {
	if warning := knowledge.RefreshIndexesForNote(r.Context(), a.homePath, collectionRoot, relInCollection); warning != "" {
		logger.WarnCF("rest", "library: write landed but an index could not be refreshed",
			map[string]any{"workspace_id": workspaceID, "path": relInCollection, "warning": warning})
	}
}

// rederiveBaseViewsAfterSave re-translates one `.base` file the Library text
// editor just saved (D-119's index half, deferred here with a full spec in
// FIX2-REPORT-index-find.md): the raw bytes on disk become the view YAMLs an
// import would have written, so a text edit of a base reaches the view index
// the same way an import does instead of being write-only.
//
// Like the index refresh it sits beside, NEVER a refusal: the save is already
// on disk, and a re-derivation that fails or refuses is logged so an operator
// can see the base no longer translates — the previously written views stay
// as they were rather than being destroyed over a refusal.
func (a *restAPI) rederiveBaseViewsAfterSave(r *http.Request, workspaceID, collectionRoot, relInCollection string) {
	res, err := vaultimport.RederiveBase(collectionRoot, relInCollection)
	if err != nil {
		logger.ErrorCF("rest", "library: base save landed but its views could not be re-derived",
			map[string]any{"workspace_id": workspaceID, "path": relInCollection, "error": err.Error()})
		return
	}
	if res.Status == vaultimport.OutcomeRefused {
		logger.WarnCF("rest", "library: base save landed but its views were not re-derived — the base no longer translates",
			map[string]any{"workspace_id": workspaceID, "path": relInCollection, "reason": res.RefusedReason,
				"kept_existing_views": len(res.KeptExisting)})
		return
	}
	logger.InfoCF("rest", "library: base save re-derived its views",
		map[string]any{"workspace_id": workspaceID, "path": relInCollection,
			"written": len(res.Written), "unchanged": len(res.Unchanged), "deleted": len(res.Deleted),
			"status": string(res.Status)})
}

// maxLibraryBinaryContentBytes is the decoded-byte cap for PUT
// .../content-binary (LibraryBinaryContentRequest.content_base64) — 25 MB,
// matching the size a filled PDF or other binary attachment realistically
// needs and the cap the schema documents. It intentionally does NOT reuse
// library.MaxContentBytes (10 MB): that constant also gates GET .../content's
// inline-render threshold for TEXT files, and binary attachments are never
// rendered inline through that path.
const maxLibraryBinaryContentBytes = 25 * 1024 * 1024

// maxLibraryBinaryContentBodyBytes bounds the raw JSON request body read for
// PUT .../content-binary. It is NOT decodeAndValidate's usual 1 MB cap:
// standard base64 inflates the payload to ~4/3 of the decoded size, so a
// legal 25 MB attachment needs room for its ~33.3 MB encoded form plus the
// JSON envelope and the "path" field. The +4096 is slack for that envelope,
// not part of the size budget being enforced.
const maxLibraryBinaryContentBodyBytes = (maxLibraryBinaryContentBytes/3+1)*4 + 4096

// handleLibraryContentBinaryPut is the binary-capable sibling of
// handleLibraryContentPut: PUT .../content carries UTF-8 text as a JSON
// string, which corrupts arbitrary bytes, so this route instead carries the
// content as standard base64 (LibraryBinaryContentRequest.content_base64) and
// writes the decoded bytes verbatim. It cannot go through decodeAndValidate
// unmodified because that helper hard-caps the body read at 1 MB regardless
// of schema — far too small for a base64-encoded PDF — so this handler reads
// and validates the body itself, at a size ceiling sized for the 25 MB
// decoded cap, before decoding into the generated type.
func (a *restAPI) handleLibraryContentBinaryPut(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	lr := io.LimitReader(r.Body, maxLibraryBinaryContentBodyBytes+1)
	raw, err := io.ReadAll(lr)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if int64(len(raw)) > maxLibraryBinaryContentBodyBytes {
		jsonErr(w, http.StatusBadRequest, "content exceeds the 25 MB limit")
		return
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		jsonErr(w, http.StatusBadRequest, "request body is required")
		return
	}

	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if validateEnabled {
		if errMsg, serverErr := validateBodyAgainstSchema("LibraryBinaryContentRequest", raw); errMsg != "" {
			if serverErr {
				jsonErr(w, http.StatusInternalServerError, "inbound schema unavailable")
			} else {
				jsonErr(w, http.StatusBadRequest,
					fmt.Sprintf("request body does not match schema LibraryBinaryContentRequest: %s", errMsg))
			}
			return
		}
	}

	var req gen.LibraryBinaryContentRequest
	if unmarshalErr := json.Unmarshal(raw, &req); unmarshalErr != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	rel, err := library.CleanRelPath(req.Path)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	decoded, err := base64.StdEncoding.DecodeString(req.ContentBase64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "content_base64 is not valid base64")
		return
	}
	if len(decoded) > maxLibraryBinaryContentBytes {
		jsonErr(w, http.StatusBadRequest, "content exceeds the 25 MB limit")
		return
	}
	// ADR-083 EMB-001, founder ruling N2: no caller is exempt — including
	// the annotated-PDF editor, the only production caller of this route
	// (EMB-007 is what gives it a read that can return a token to send).
	expectedBare, refusal, ok := requireLibraryExpectVersion(w, req.ExpectVersion)
	if !ok {
		a.logLibraryWriteRefused(r, workspaceID, rel, refusal, true, len(decoded))
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	if !checkCreateName(w, root, rel, "put content", workspaceID) {
		return
	}

	lockCfg, lockRel, lockErr := resolveLibraryLock(root, a.homePath, workspaceID, rel)
	if lockErr != nil {
		logger.ErrorCF("rest", "library: resolve write lock failed",
			map[string]any{"workspace_id": workspaceID, "path": rel, "error": lockErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var (
		fi       os.FileInfo
		newToken knowledge.VersionToken
	)
	writeErr := knowledge.WithNoteWriteLock(lockCfg, lockRel, func() error {
		current, verErr := knowledge.ReadFileVersion(root.HostPath(rel))
		if verErr != nil {
			return verErr
		}
		if conflict := checkLibraryVersion(rel, expectedBare, current); conflict != nil {
			return conflict
		}
		if libraryWriteRaceHook != nil {
			libraryWriteRaceHook()
		}
		var writeErr error
		fi, writeErr = root.WriteContent(rel, decoded)
		if writeErr != nil {
			return writeErr
		}
		newToken = knowledge.ComputeVersionToken(decoded)
		return nil
	})
	if refusal, ok := handleLibraryWriteLockErr(w, "put content", workspaceID, writeErr); !ok {
		a.logLibraryWriteRefused(r, workspaceID, rel, refusal, true, len(decoded))
		return
	}

	w.Header().Set("ETag", libraryETagValue(newToken))
	a.logLibraryAudit(r, "library.write", workspaceID,
		map[string]any{"path": rel, "bytes": len(decoded), "binary": true})
	// D-107: same listing-staleness reason as the text PUT above.
	a.emitLibraryChange(workspaceID, rel, "write")
	jsonOK(w, library.EntryFromInfo(rel, fi))
}

// --- POST /library/{workspace_id}/upload ---

func (a *restAPI) handleLibraryUpload(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	targetDir, err := library.CleanRelPath(r.URL.Query().Get("path"))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	// Upload does not auto-create nested directories beyond the work-tree
	// root itself (OpenRoot already guarantees that one exists) — matches
	// putLibraryContent's "parent directory must already exist" contract.
	if targetDir != "" {
		if _, statErr := root.StatDir(targetDir); statErr != nil {
			mapLibraryErr(w, "upload", workspaceID, statErr)
			return
		}
	}

	reader, err := r.MultipartReader()
	if err != nil {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart request: %v", err))
		return
	}

	var resp gen.LibraryUploadResponse
	var createdRelPaths []string
	rollback := func() {
		for _, rel := range createdRelPaths {
			if rmErr := root.Delete(rel); rmErr != nil {
				logger.WarnCF("rest", "library: upload rollback failed",
					map[string]any{"workspace_id": workspaceID, "path": rel, "error": rmErr.Error()})
			}
		}
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			rollback()
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("multipart read error: %v", err))
			return
		}
		fileName := part.FileName()
		if fileName == "" {
			// Non-file field — discard.
			if _, discardErr := io.Copy(io.Discard, part); discardErr != nil {
				logger.WarnCF("rest", "library: upload: discard field failed",
					map[string]any{"workspace_id": workspaceID, "error": discardErr.Error()})
			}
			part.Close()
			continue
		}

		sanitized, sanErr := agent.SanitizeUploadFilename(path.Base(fileName))
		if sanErr != nil {
			part.Close()
			rollback()
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid filename: %v", sanErr))
			return
		}
		destRel := sanitized
		if targetDir != "" {
			destRel = targetDir + "/" + sanitized
		}

		// The full destination, not just the leaf. SanitizeUploadFilename above
		// already judged the leaf, so on a POSIX build this adds nothing a
		// caller can observe — every POSIX shape rule is a per-component byte
		// budget the host filesystem enforces anyway. What it adds on a Windows
		// build is the two rules the leaf check cannot see: targetDir's own
		// segments, and the whole-path MAX_PATH budget that a short filename in
		// a deep directory blows without any single component coming close.
		// FR-0001a names upload as one of the five for that reason.
		if nameErr := root.ValidateCreateName(destRel); nameErr != nil {
			part.Close()
			rollback()
			mapLibraryErr(w, "upload", workspaceID, nameErr)
			return
		}

		finalRel, f, createErr := root.CreateUnique(destRel)
		if createErr != nil {
			part.Close()
			rollback()
			logger.ErrorCF("rest", "library: upload: create file failed",
				map[string]any{"workspace_id": workspaceID, "path": destRel, "error": createErr.Error()})
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not create file: %v", createErr))
			return
		}

		limited := io.LimitReader(part, maxUploadFileSize+1)
		written, copyErr := io.Copy(f, limited)
		f.Close()
		part.Close()
		if copyErr != nil {
			if rmErr := root.Delete(finalRel); rmErr != nil {
				logger.WarnCF("rest", "library: upload: remove partial file failed",
					map[string]any{"workspace_id": workspaceID, "path": finalRel, "error": rmErr.Error()})
			}
			rollback()
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("file write failed: %v", copyErr))
			return
		}
		if written > maxUploadFileSize {
			if rmErr := root.Delete(finalRel); rmErr != nil {
				logger.WarnCF("rest", "library: upload: remove oversized file failed",
					map[string]any{"workspace_id": workspaceID, "path": finalRel, "error": rmErr.Error()})
			}
			rollback()
			jsonErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file %q exceeds 100 MB limit", sanitized))
			return
		}

		createdRelPaths = append(createdRelPaths, finalRel)
		fi, statErr := root.StatFile(finalRel)
		if statErr != nil {
			rollback()
			logger.ErrorCF("rest", "library: upload: stat uploaded file failed",
				map[string]any{"workspace_id": workspaceID, "path": finalRel, "error": statErr.Error()})
			jsonErr(w, http.StatusInternalServerError, "internal server error")
			return
		}
		resp.Entries = append(resp.Entries, library.EntryFromInfo(finalRel, fi))
	}

	if len(resp.Entries) == 0 {
		jsonErr(w, http.StatusBadRequest, "no files found in upload")
		return
	}
	a.logLibraryAudit(r, "library.upload", workspaceID, map[string]any{
		"path": targetDir, "count": len(resp.Entries),
	})
	// D-107: the uploaded entries appear in other tabs' listings of this
	// folder. Path names the FOLDER uploaded into (the mutation is
	// multi-entry; the frame's path is informational, never a scoping
	// instruction).
	a.emitLibraryChange(workspaceID, targetDir, "upload")
	jsonCreated(w, resp)
}

// --- POST /library/{workspace_id}/mkdir ---

// handleLibraryMkdir creates a directory in workspaceID's work tree,
// creating any missing intermediate directories along the way (UAT Issue 4:
// previously there was no way for a caller to create a folder at all, and a
// clean, non-malicious nested Move/Copy destination like "subfolder/test.txt"
// had no path to success because those operations deliberately do not
// auto-create missing parents — see requireParentDir's doc). Idempotent:
// creating a directory that already exists returns 200 with its existing
// entry rather than an error; 409 if a regular FILE already occupies path.
func (a *restAPI) handleLibraryMkdir(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.LibraryMkdirRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LibraryMkdirRequest", &req, validateEnabled) {
		return
	}
	rel, err := library.CleanRelPath(req.Path)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	if !checkCreateName(w, root, rel, "mkdir", workspaceID) {
		return
	}

	fi, created, err := root.Mkdir(rel)
	if err != nil {
		mapLibraryErr(w, "mkdir", workspaceID, err)
		return
	}
	entry := library.EntryFromInfo(rel, fi)
	a.logLibraryAudit(r, "library.mkdir", workspaceID, map[string]any{"path": rel, "created": created})
	// D-107: only an actual CREATE changes the tree — this handler is
	// idempotent, and the no-op branch changed nothing any tab needs to see.
	if created {
		a.emitLibraryChange(workspaceID, rel, "mkdir")
	}
	if created {
		jsonCreated(w, entry)
	} else {
		jsonOK(w, entry)
	}
}

// handleLibraryCreateVault creates a new Omnipus knowledge base ("vault") at
// parent_rel_path/name inside workspaceID's work tree.
//
// Unlike handleLibraryMkdir this is NOT idempotent and does NOT auto-create
// missing intermediate directories: it behaves like content-put/rename
// (requires the immediate parent to already exist, 404 otherwise) and rejects
// (409) ANY entry — file, plain directory, or existing vault — already at
// the target path, because adopting an existing folder into a vault or
// silently reusing one is never this endpoint's job (CreateVaultRequest's
// description).
//
// SEEDING DECISION: after knowledge.CreateInWorkspace writes the
// .omnipus-vault/ marker, this handler additionally creates empty
// records/ and views/ control-plane directories (records.SchemaDir,
// records.ViewsDir) so knowledge_configure has somewhere to write into
// immediately. It deliberately does NOT seed a starter saved view. A view
// is validated against the vault's schema set and must name an existing
// record TYPE (pkg/records/view.go's RejectViewMissingType /
// ValidateViewAgainstSchemas) — a brand-new vault has zero record types, so
// there is no type this handler could reference without inventing a schema
// shape, which view.go's own doc comment reserves to knowledge_configure's
// write path alone ("THERE IS NO WRITER [here], on purpose"). Empty
// records/ + views/ plus the marker is a valid, detectable, immediately
// usable vault; a fabricated view would not be.
func (a *restAPI) handleLibraryCreateVault(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.CreateVaultRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "CreateVaultRequest", &req, validateEnabled) {
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		jsonErr(w, http.StatusBadRequest, "invalid knowledge base name")
		return
	}

	parentRel := ""
	if req.ParentRelPath != nil {
		cleaned, err := library.CleanRelPath(*req.ParentRelPath)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid parent_rel_path")
			return
		}
		parentRel = cleaned
	}
	joined := name
	if parentRel != "" {
		joined = parentRel + "/" + name
	}
	rel, err := library.CleanRelPath(joined)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	if !checkCreateName(w, root, rel, "create vault", workspaceID) {
		return
	}
	if parentRel != "" {
		if _, statErr := root.StatDir(parentRel); statErr != nil {
			mapLibraryErr(w, "create vault", workspaceID, statErr)
			return
		}
	}

	// ValidateCreateName only judges name SHAPE (FR-0001a), not collision —
	// check for an existing entry at rel ourselves so this route can refuse
	// with 409 rather than silently adopting or converting whatever is
	// already there.
	switch _, statErr := root.StatDir(rel); {
	case statErr == nil, errors.Is(statErr, library.ErrNotDir):
		jsonErr(w, http.StatusConflict, "an entry already exists at that path")
		return
	case errors.Is(statErr, library.ErrNotFound):
		// Expected: nothing there yet.
	default:
		mapLibraryErr(w, "create vault", workspaceID, statErr)
		return
	}

	collection, err := knowledge.CreateInWorkspace(a.homePath, workspaceID, rel, knowledge.Marker{DisplayName: name})
	if err != nil {
		switch {
		case errors.Is(err, knowledge.ErrAlreadyKnowledgeBase):
			jsonErr(w, http.StatusConflict, "an entry already exists at that path")
		case errors.Is(err, knowledge.ErrNestedKnowledgeBase):
			jsonErr(w, http.StatusConflict, "that location is inside an existing knowledge base — a knowledge base cannot be created inside another one")
		case errors.Is(err, knowledge.ErrMarkerInvalid):
			jsonErr(w, http.StatusBadRequest, "invalid knowledge base name")
		case errors.Is(err, knowledge.ErrOutsideCollection):
			jsonErr(w, http.StatusForbidden, "path resolves outside the workspace work tree")
		default:
			logger.ErrorCF("rest", "library: create vault failed",
				map[string]any{"workspace_id": workspaceID, "path": rel, "error": err.Error()})
			jsonErr(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	if mkErr := os.MkdirAll(records.SchemaDir(collection.Root()), 0o755); mkErr != nil {
		logger.ErrorCF("rest", "library: create vault: seed records dir failed",
			map[string]any{"workspace_id": workspaceID, "path": rel, "error": mkErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if mkErr := os.MkdirAll(records.ViewsDir(collection.Root()), 0o755); mkErr != nil {
		logger.ErrorCF("rest", "library: create vault: seed views dir failed",
			map[string]any{"workspace_id": workspaceID, "path": rel, "error": mkErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	fi, err := root.StatDir(rel)
	if err != nil {
		mapLibraryErr(w, "create vault", workspaceID, err)
		return
	}
	entry := library.EntryFromInfo(rel, fi)
	a.logLibraryAudit(r, "library.create_vault", workspaceID, map[string]any{"path": rel})
	// D-107: a new knowledge base folder appears in the listing (and, with
	// hidden files shown, so does its marker).
	a.emitLibraryChange(workspaceID, rel, "vault")
	jsonCreated(w, entry)
}

// --- POST /library/{workspace_id}/rename ---

func (a *restAPI) handleLibraryRename(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.LibraryRenameRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LibraryRenameRequest", &req, validateEnabled) {
		return
	}
	fromRel, err := library.CleanRelPath(req.From)
	if err != nil || fromRel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid from path")
		return
	}
	toRel, err := library.CleanRelPath(req.To)
	if err != nil || toRel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid to path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	// The DESTINATION only. fromRel names something that already exists —
	// judging its shape would be judging a name Omnipus is not creating, and
	// would make an operator's existing file un-renameable precisely because
	// its current name is the thing they want to fix.
	if !checkCreateName(w, root, toRel, "rename", workspaceID) {
		return
	}

	// UAT #701 / D-123: a note, an attachment or a folder renamed within its
	// knowledge base has every inbound link and embed rewritten (for a folder,
	// every link to anything under it), the way the agent door renames — see
	// rest_library_knowledge_cascade.go.
	note, governed, noteErr := a.libraryManagedEntryInCollection(root, fromRel)
	if noteErr != nil {
		mapLibraryErr(w, "rename", workspaceID, noteErr)
		return
	}
	if governed && sameCollectionDestination(root, note, toRel) {
		a.renameNoteInCollection(w, r, "rename", workspaceID, root, note, fromRel, toRel)
		return
	}

	fi, err := root.Rename(fromRel, toRel)
	if err != nil {
		mapLibraryErr(w, "rename", workspaceID, err)
		return
	}
	// U-58: renaming a knowledge base's last marker away demotes the folder.
	a.releaseKnowledgeBaseIfDemoted(root, fromRel)
	// ADR-067 FR-003d: the granted path has MOVED, so every token naming it —
	// or naming something beneath it — must stop working now.
	//
	// The SOURCE only, and that is not an oversight. A token over the
	// DESTINATION cannot exist: minting requires the path to be readable at mint
	// time, and this handler refuses a destination that already exists (409,
	// root.Rename's ErrExists). If that ever stops being true — an overwrite
	// mode, a force flag — the destination becomes a live grant over bytes its
	// holder never saw, and this is the line that has to grow a second call.
	a.revokePreviewTokensForPath(workspaceID, fromRel)
	a.logLibraryAudit(r, "library.rename", workspaceID, map[string]any{"from": fromRel, "to": toRel})
	// D-107: the old name is gone and the new one appeared — path names the
	// NEW entry (the one a stale listing is missing).
	a.emitLibraryChange(workspaceID, toRel, "rename")
	jsonOK(w, library.EntryFromInfo(toRel, fi))
}

func (a *restAPI) handleLibraryTransfer(w http.ResponseWriter, r *http.Request, mode libraryTransferMode) {
	var req gen.LibraryTransferRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LibraryTransferRequest", &req, validateEnabled) {
		return
	}

	if err := validateEntityID(req.FromWorkspaceId); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid from_workspace_id")
		return
	}
	if err := validateEntityID(req.ToWorkspaceId); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid to_workspace_id")
		return
	}
	if !workspace.Exists(a.homePath, req.FromWorkspaceId) {
		jsonErr(w, http.StatusNotFound, "from_workspace_id not found")
		return
	}
	if !workspace.Exists(a.homePath, req.ToWorkspaceId) {
		jsonErr(w, http.StatusNotFound, "to_workspace_id not found")
		return
	}

	fromRel, err := library.CleanRelPath(req.FromPath)
	if err != nil || fromRel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid from_path")
		return
	}
	toRel, err := library.CleanRelPath(req.ToPath)
	if err != nil || toRel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid to_path")
		return
	}

	sameWorkspace := req.FromWorkspaceId == req.ToWorkspaceId

	fromRoot, ok := a.openLibraryRoot(w, req.FromWorkspaceId, "from-root")
	if !ok {
		return
	}
	defer fromRoot.Close()

	toRoot := fromRoot
	if !sameWorkspace {
		toRoot, ok = a.openLibraryRoot(w, req.ToWorkspaceId, "to-root")
		if !ok {
			return
		}
		defer toRoot.Close()
	}

	// toRoot, never fromRoot: for a cross-workspace transfer the two are
	// different roots with different mount tables, and the question
	// ValidateCreateName answers — "is Omnipus about to create this name in
	// storage it owns?" — is a property of where the file LANDS. Asking
	// fromRoot would consult the wrong workspace's mounts and, for a copy out
	// of a mount into workspace storage, would skip the check entirely.
	if !checkCreateName(w, toRoot, toRel, string(mode), req.ToWorkspaceId) {
		return
	}

	// UAT #701 / D-123: a same-workspace MOVE of a note, an attachment or a
	// folder to another folder of the SAME knowledge base is a rename in the
	// knowledge layer's terms — every inbound link and embed is rewritten. A copy
	// duplicates bytes and rewrites nothing; a cross-workspace or
	// cross-knowledge-base move is a real departure the link graph cannot
	// follow, so both keep the plain filesystem semantics.
	if mode == transferModeMove && sameWorkspace {
		note, governed, noteErr := a.libraryManagedEntryInCollection(fromRoot, fromRel)
		if noteErr != nil {
			mapLibraryErr(w, string(mode), req.FromWorkspaceId, noteErr)
			return
		}
		if governed && sameCollectionDestination(fromRoot, note, toRel) {
			a.renameNoteInCollection(w, r, string(mode), req.FromWorkspaceId, fromRoot, note, fromRel, toRel)
			return
		}
	}

	var fi os.FileInfo
	var opErr error
	switch mode {
	case transferModeMove:
		fi, opErr = library.MoveInto(fromRoot, toRoot, fromRel, toRel)
	case transferModeCopy:
		fi, opErr = library.CopyInto(fromRoot, toRoot, fromRel, toRel)
	}
	if opErr != nil {
		mapLibraryErr(w, string(mode), req.FromWorkspaceId, opErr)
		return
	}

	// ADR-067 FR-003d. A MOVE vacates from_path, so every token over it dies —
	// in the SOURCE workspace, which for a cross-workspace transfer is not the
	// one the entry landed in. A COPY destroys nothing and moves nothing, so it
	// is not one of FR-003d's events and revokes nothing; the destination cannot
	// hold a live grant either, because both modes refuse an existing
	// destination (409) and a token can only be minted over a path that exists.
	if mode == transferModeMove {
		a.revokePreviewTokensForPath(req.FromWorkspaceId, fromRel)
		// U-58: moving a knowledge base's last marker away demotes the folder
		// it left.
		a.releaseKnowledgeBaseIfDemoted(fromRoot, fromRel)
	}

	entry := library.EntryFromInfo(toRel, fi)
	a.logLibraryAudit(r, "library."+string(mode), req.FromWorkspaceId, map[string]any{
		"from_workspace_id": req.FromWorkspaceId, "from_path": fromRel,
		"to_workspace_id": req.ToWorkspaceId, "to_path": toRel,
	})
	// D-107: the destination tree gained an entry, always; a cross-workspace
	// MOVE additionally vacated one in the source tree, whose tabs need their
	// own frame (a same-workspace move is covered by the destination frame —
	// it is the same tree).
	a.emitLibraryChange(req.ToWorkspaceId, toRel, string(mode))
	if mode == transferModeMove && !sameWorkspace {
		a.emitLibraryChange(req.FromWorkspaceId, fromRel, string(mode))
	}
	if mode == transferModeCopy {
		jsonCreated(w, entry)
	} else {
		jsonOK(w, entry)
	}
}

// logLibraryAudit emits a best-effort audit event for a mutating Library
// operation. Audit write failures are logged, never surfaced to the HTTP
// caller — an audit gap must not block an otherwise-successful operation,
// matching the convention rest_workspace_media.go's logMediaDeleteAudit and
// rest_workspaces.go's workspace.create/update events already establish.
func (a *restAPI) logLibraryAudit(r *http.Request, event, workspaceID string, details map[string]any) {
	a.logLibraryAuditDecision(r, event, audit.DecisionAllow, workspaceID, details)
}

// logLibraryWriteRefused records a whole-file save that NEVER REACHED DISK —
// a 400 shape error on expect_version, a 409 stale token, a 503 lock timeout,
// or a write that errored inside the locked closure.
//
// # Why a refusal needs a record at all
//
// The premise of the version door is that a lost note is undetectable after
// the fact: nothing on disk says a second writer was ever here. Recording
// only the saves that SUCCEEDED gives an operator investigating "my note
// changed and I do not know who" exactly the population that is not the
// answer. The attempts that were REFUSED are the ones that name a second
// writer working from a version they had not re-read — the same reasoning
// pkg/knowledge/audit.go's header sets out for the agent authoring path
// ("no REFUSAL happens without one either"), applied to the Library door
// that path's own comment already points at.
//
// # Shape
//
// Same event name as the success record ("library.write"), so one query
// returns the whole population of attempted saves; DECISION and the `reason`
// detail are what separate them. Decision is "deny" for the four deliberate
// refusals and "error" for libraryRefusalWriteFailed, mirroring
// knowledge.MutationRefused vs MutationFailed — for the reader of an audit
// log those are different events, because a refusal means the file is intact
// and a failure means the file on disk needs looking at.
//
// The SUCCESS record's shape is untouched: it still carries exactly
// path/bytes/binary (plus the actor and workspace_id every library.* record
// gets), and never a `reason`. Existing tooling reading allow rows sees no
// change.
//
// # It cannot turn a 409 into a 500
//
// Every call site invokes this AFTER its response has already been written,
// and logLibraryAuditDecision swallows sink failures into a WARN exactly as
// the success path does. An audit sink problem therefore cannot change the
// status code the caller already received. A nil a.auditor (sandbox.
// audit_log off) is a documented operator choice and stays a silent no-op.
//
// attemptedBytes is the size of the content the caller TRIED to write, which
// for every reason here is a size that never landed; it is recorded because
// "who tried to overwrite my 40 KB note with 3 bytes" is a question the
// refused population exists to answer.
func (a *restAPI) logLibraryWriteRefused(
	r *http.Request, workspaceID, relPath, reason string, binary bool, attemptedBytes int,
) {
	decision := audit.DecisionDeny
	if reason == libraryRefusalWriteFailed {
		decision = audit.DecisionError
	}
	a.logLibraryAuditDecision(r, "library.write", decision, workspaceID, map[string]any{
		"path":   relPath,
		"bytes":  attemptedBytes,
		"binary": binary,
		"reason": reason,
	})
}

// logLibraryAuditDecision is the shared body of logLibraryAudit and
// logLibraryWriteRefused. It exists so a refusal and a success travel the
// same nil-auditor guard, the same actor/workspace_id stamping and the same
// swallow-the-sink-error handling, and so the only difference between them
// is the Decision and the details the caller chose.
func (a *restAPI) logLibraryAuditDecision(
	r *http.Request, event, decision, workspaceID string, details map[string]any,
) {
	if a.auditor == nil {
		return
	}
	if details == nil {
		details = map[string]any{}
	}
	details["actor"] = a.callerIdentity(r).Username
	details["workspace_id"] = workspaceID
	if err := a.auditor.Log(&audit.Entry{
		Event:    event,
		Decision: decision,
		Details:  details,
	}); err != nil {
		logger.WarnCF("rest", "library: audit write failed",
			map[string]any{"event": event, "workspace_id": workspaceID, "error": err.Error()})
	}
}
