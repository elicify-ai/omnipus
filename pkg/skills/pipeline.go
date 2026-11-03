package skills

// The one change pipeline (docs/internal/specs/skills-folder-1458-spec.md
// §7.1): every change to an installed skill — file tool, edit_skill,
// install, REST — goes through ApplyChange, under WithMutationLock, with the
// save check, the version commit and the audit entry. §7.2's per-operation
// validation and §7.6's outcome classes live here.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ChangeKind enumerates §7.1's operations. A copy is KindCreate with To set
// (Slug is then the source).
type ChangeKind string

const (
	KindCreate         ChangeKind = "create"
	KindEdit           ChangeKind = "edit"
	KindDeleteFile     ChangeKind = "delete_file"
	KindDelete         ChangeKind = "delete"
	KindRestore        ChangeKind = "restore"
	KindInstall        ChangeKind = "install"
	KindInstallReplace ChangeKind = "install_replace"
)

// Expectation is §7.3's one precondition input: expected absence or expected
// revision R. Exactly one field is set.
type Expectation struct {
	Absent   bool
	Revision string
}

// ChangeActor identifies who made the change (manifest `actor`, §5.11).
type ChangeActor struct {
	Kind        string // "agent" | "person" | "system" | "unknown"
	AgentID     string
	SessionID   string
	WorkspaceID string
	Tool        string
}

// label is the manifest `actor` value: the agent id, or person / system /
// unknown.
func (a ChangeActor) label() string {
	switch a.Kind {
	case "agent":
		if a.AgentID != "" {
			return a.AgentID
		}
		return "agent"
	case "person", "system":
		return a.Kind
	default:
		return "unknown"
	}
}

// ChangeRequest is one ApplyChange operation (§7.1). Exactly the fields the
// Kind needs are read.
type ChangeRequest struct {
	Kind      ChangeKind
	Slug      string // target skill slug (for copy: the SOURCE; To is the destination)
	To        string // copy destination slug (copy only)
	Path      string // file path relative to the folder (edit, delete_file)
	Version   string // restore version id
	Content   []byte // single-file writes (edit, create SKILL.md)
	StagedDir string // staged folder for install / install_replace
	Expect    Expectation
	Actor     ChangeActor
	Grants    GrantEditor // delete-time best-effort grant removal (founder Q-S3); may be nil
	// ReplaceLocalChanges confirms an install_replace of a skill whose
	// has_local_changes is true (§5.6); evaluated after step 1's detection.
	ReplaceLocalChanges bool
}

func (r ChangeRequest) isCopy() bool { return r.Kind == KindCreate && r.To != "" }

// target is the slug the change publishes to.
func (r ChangeRequest) target() string {
	if r.isCopy() {
		return r.To
	}
	return r.Slug
}

// ChangeError is a refused or failed change. Error() renders the code and
// the §5.10 text; errors.Is matches the code sentinel (and any cause).
type ChangeError struct {
	Code  error
	Text  string
	cause error
}

func (e *ChangeError) Error() string { return e.Code.Error() + ": " + e.Text }

func (e *ChangeError) Unwrap() []error {
	if e.cause != nil {
		return []error{e.Code, e.cause}
	}
	return []error{e.Code}
}

// BuiltinReadOnlyError is the §5.10 BUILTIN_READ_ONLY refusal for a change to
// the built-in skill slug.
func BuiltinReadOnlyError(slug string) *ChangeError {
	return &ChangeError{Code: ErrBuiltinReadOnly, Text: fmt.Sprintf("'%s' is a built-in skill. Built-in skills are read-only and are replaced with the shipped version on every Omnipus update, so this change was not saved. Copy it first: edit_skill action=copy skill=%s to=<new-name>, then edit the copy and grant it to the agents that should use it.", slug, slug)}
}

// ApplyChange is the ONE change pipeline (§0 principle 3, §7.1).
func ApplyChange(root string, req ChangeRequest) (PublishOutcome, error) {
	if err := validateRequestShape(req); err != nil {
		return refusedOutcome(), err
	}
	// Step 0 (before the lock): built-in names are read-only and reserved.
	if err := RefuseBuiltinTarget(req); err != nil {
		return refusedOutcome(), err
	}

	p := &pipeline{root: filepath.Clean(root), req: req, now: time.Now().UTC()}
	var out PublishOutcome
	var runErr error
	lockErr := WithMutationLock(p.root, func() error {
		out, runErr = p.run()
		return nil
	})
	if lockErr != nil {
		return refusedOutcome(), lockErr
	}
	if errors.Is(runErr, ErrStorage) && out.ErrorStage == "" {
		// §7.6 class 1: a storage failure before publication names its stage
		// so a caller can render the failure envelope (C-9).
		out.ErrorStage = PublicationErrorStageSave
		if out.Message == "" {
			var ce *ChangeError
			if errors.As(runErr, &ce) {
				out.Message = ce.Text
			}
		}
	}
	return out, runErr
}

// RefuseBuiltinTarget is the pipeline's step 0 (§7.1): a change whose target
// is a built-in key is refused before the lock — BUILTIN_NAME_RESERVED for a
// create, copy or any install (§5.6), BUILTIN_READ_ONLY otherwise — and the
// refusal is audited. It returns nil for a non-built-in target. Install
// adapters call it before their own "already installed" pre-checks, because a
// built-in folder is present on disk and those checks would otherwise answer
// first with a different refusal.
func RefuseBuiltinTarget(req ChangeRequest) error {
	target := req.target()
	if !IsBuiltin(target) {
		return nil
	}
	var err *ChangeError
	if creates(req) {
		err = BuiltinNameReservedError(target)
	} else {
		err = BuiltinReadOnlyError(target)
	}
	auditRefusal(req, target, "builtin_read_only")
	return err
}

// BuiltinNameReservedError is the §5.10 BUILTIN_NAME_RESERVED refusal.
func BuiltinNameReservedError(slug string) *ChangeError {
	return &ChangeError{Code: ErrBuiltinNameReserved, Text: fmt.Sprintf("'%s' is the name of a built-in skill and cannot be used for another skill. Choose a different name.", SkillKey(slug))}
}

// creates reports whether the request publishes a new name (create, copy,
// install) — the BUILTIN_NAME_RESERVED class. An install onto a built-in name
// is reserved whether or not it carries a revision (§5.6): installing is
// never a way to change a built-in.
func creates(req ChangeRequest) bool {
	return req.Kind == KindCreate || req.Kind == KindInstall || req.Kind == KindInstallReplace
}

func refusedOutcome() PublishOutcome {
	return PublishOutcome{PersistenceStatus: PersistenceNone, ActivationStatus: ActivationNotAttempted}
}

func invalidInput(format string, args ...any) *ChangeError {
	return &ChangeError{Code: ErrInvalidInput, Text: fmt.Sprintf(format, args...)}
}

// validateRequestShape checks the request's own arguments (INVALID_INPUT).
func validateRequestShape(req ChangeRequest) error {
	switch req.Kind {
	case KindCreate, KindEdit, KindDeleteFile, KindDelete, KindRestore, KindInstall, KindInstallReplace:
	default:
		return invalidInput("unknown change kind %q", req.Kind)
	}
	if !ValidSlug(req.Slug) {
		return invalidInput("%q is not a valid skill name (letters, digits and single hyphens, at most %d characters)", req.Slug, MaxNameLength)
	}
	if req.To != "" && req.Kind != KindCreate {
		return invalidInput("'to' is only valid for a copy")
	}
	if req.isCopy() && !ValidSlug(req.To) {
		return invalidInput("%q is not a valid skill name (letters, digits and single hyphens, at most %d characters)", req.To, MaxNameLength)
	}
	if req.Expect.Absent && req.Expect.Revision != "" {
		return invalidInput("an expectation is either absence or a revision, not both")
	}
	switch req.Kind {
	case KindCreate, KindInstall:
		if !req.Expect.Absent {
			return invalidInput("a new skill expects the name to be absent")
		}
	case KindEdit, KindDeleteFile, KindDelete, KindInstallReplace:
		if req.Expect.Revision == "" {
			return invalidInput("revision is required")
		}
	case KindRestore:
		if !req.Expect.Absent && req.Expect.Revision == "" {
			return invalidInput("revision is required")
		}
		if req.Version == "" {
			return invalidInput("version is required")
		}
	}
	if (req.Kind == KindInstall || req.Kind == KindInstallReplace) && req.StagedDir == "" {
		return invalidInput("a staged folder is required")
	}
	if (req.Kind == KindEdit || req.Kind == KindDeleteFile) && req.Path == "" {
		return invalidInput("path is required")
	}
	return nil
}

// pipeline carries one ApplyChange call through steps 1–7.
type pipeline struct {
	root     string
	req      ChangeRequest
	now      time.Time
	warnings []string // class-3 finishing warnings, rendered after the version id
	secrets  *secretSet
	secErr   *refusal
	secRead  bool
}

// state is step 1's observation of one skill key.
type state struct {
	slug    string // on-disk slug (or the requested slug when absent)
	dir     string
	present bool
	hist    historyStore
	before  *versionManifest // newest manifest when step 1 began
	head    *versionManifest // newest manifest after the checkpoint (== live when present)
	entries []treeEntry      // live entries with final blobs (present only)
	rev     string           // live content revision (present only)
}

func (p *pipeline) run() (PublishOutcome, error) {
	switch {
	case p.req.isCopy():
		return p.runCopy()
	case p.req.Kind == KindCreate:
		return p.runCreate()
	case p.req.Kind == KindEdit:
		return p.runEdit()
	case p.req.Kind == KindDeleteFile:
		return p.runDeleteFile()
	case p.req.Kind == KindDelete:
		return p.runDelete()
	case p.req.Kind == KindRestore:
		return p.runRestore()
	default:
		return p.runInstall()
	}
}

// observe resolves a key without capturing (used for expected-absence
// targets: a present key is refused SKILL_EXISTS before anything is read).
func (p *pipeline) observe(slug string) (*state, error) {
	onDisk, present, err := resolveLiveFolder(p.root, slug)
	if err != nil {
		return nil, p.storageError(slug, err)
	}
	st := &state{slug: onDisk, dir: filepath.Join(p.root, onDisk), present: present, hist: historyFor(p.root, onDisk)}
	before, err := st.hist.newest()
	if err != nil {
		return nil, p.storageError(slug, err)
	}
	st.before, st.head = before, before
	return st, nil
}

// detect is step 1: read the live revision and the newest manifest; a live
// folder with no manifest gets a baseline version, one whose revision differs
// from the newest manifest gets an external_change version (§7.1, one rule).
func (p *pipeline) detect(slug string) (*state, error) {
	st, err := p.observe(slug)
	if err != nil || !st.present {
		return st, err
	}
	if err := p.checkpoint(st, "capture"); err != nil {
		return nil, err
	}
	return st, nil
}

// checkpoint captures a present live folder (bounded, §9.5) and commits a
// baseline/external_change version when live differs from the newest one.
func (p *pipeline) checkpoint(st *state, op string) error {
	f := p.captureCheckpoint(st, op)
	switch {
	case f == nil:
		return nil
	case f.special != nil:
		return p.refuseText(st, saveCheck(unsupportedEntryReason(*f.special)), f.suffix)
	case f.capture:
		return p.captureError(st.slug, f.err, f.suffix)
	default:
		return p.storageErrorText(st.slug, f.err, f.suffix)
	}
}

// checkpointFailure is a raw step-1 failure, mapped by the caller: the change
// pipeline refuses (§7.6 class 1); an activation load degrades or fails
// (§5.7, §9.5).
type checkpointFailure struct {
	capture bool       // the capture walk failed (limit crossing or read error)
	special *treeEntry // the folder holds an unsupported entry
	err     error
	suffix  string // §7.2 cleanup-failure suffix
}

// captureCheckpoint is checkpoint's work without the refusal mapping. On
// success st carries the live entries, revision and newest manifest.
func (p *pipeline) captureCheckpoint(st *state, op string) *checkpointFailure {
	first, err := captureTree(st.dir, captureOptions{op: op})
	if err != nil {
		return &checkpointFailure{capture: true, err: err}
	}
	if first.special != nil {
		return &checkpointFailure{special: first.special}
	}
	if st.before != nil && st.before.Kind != manifestDeleteSkill && st.before.Revision == first.revision {
		st.entries = st.hist.entriesFromManifest(st.before)
		st.rev = first.revision
		return nil
	}
	attempt, err := newCaptureAttempt(st.hist)
	if err != nil {
		return &checkpointFailure{err: err}
	}
	res, err := captureTree(st.dir, captureOptions{op: op, blobDir: attempt.dir})
	if err != nil {
		return &checkpointFailure{capture: true, err: err, suffix: abortSuffix(attempt.abort())}
	}
	if res.special != nil {
		return &checkpointFailure{special: res.special, suffix: abortSuffix(attempt.abort())}
	}
	kind := manifestBaseline
	parent := ""
	if st.before != nil {
		kind = manifestExternalChange
		parent = st.before.ID
	}
	m := &versionManifest{
		ID: nextVersionID(st.before, p.now), Parent: parent, CreatedAt: p.now.Format(manifestTimeFormat),
		Slug: st.slug, Kind: kind, Actor: "unknown", Files: manifestFilesOf(res.entries),
		Paths: allPaths(res.entries), Revision: res.revision,
	}
	m.PathsTotal = len(m.Paths)
	if err := attempt.finalize(res.entries); err != nil {
		return &checkpointFailure{err: err, suffix: abortSuffix(attempt.abort())}
	}
	if err := st.hist.writeManifest(m); err != nil {
		return &checkpointFailure{err: err, suffix: abortSuffix(attempt.abort())}
	}
	attempt.committed()
	if rmErr := attempt.removeScratch(); rmErr != nil {
		p.warnings = append(p.warnings, "capture scratch cleanup failed: "+rmErr.Error())
	}
	auditCheckpoint(st.slug, m)
	st.head = m
	st.entries = res.entries
	st.rev = res.revision
	return nil
}

func unsupportedEntryReason(e treeEntry) string {
	return fmt.Sprintf("unsupported entry %s (%s) — remove it outside Omnipus and retry", e.Path, e.ModeType)
}

// abortSuffix renders §7.2's cleanup-failure suffix ("" when the cleanup
// succeeded).
func abortSuffix(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("; cleanup of partial capture output also failed (%v)", err)
}

// precondition is step 2 for an existing-skill expectation.
func (p *pipeline) precondition(st *state) error {
	exp := p.req.Expect
	if exp.Absent {
		if st.present {
			return p.refuse(st, &refusal{code: ErrSkillExists, reason: fmt.Sprintf("skill '%s' already exists; read it first", st.slug)})
		}
		return nil
	}
	if !st.present {
		return p.refuse(st, &refusal{code: ErrSkillChanged, reason: fmt.Sprintf("skill '%s' changed since you read it (now absent); read it again with get_skill, Skill or read_file", st.slug)})
	}
	if st.rev != exp.Revision {
		actor := "unknown"
		if st.head != nil && st.head.Revision == st.rev {
			actor = st.head.Actor
		}
		rev8 := st.rev
		if len(rev8) > 8 {
			rev8 = rev8[:8]
		}
		return p.refuse(st, &refusal{code: ErrSkillChanged, reason: fmt.Sprintf("skill '%s' changed since you read it (now %s by %s); read it again with get_skill, Skill or read_file", st.slug, rev8, actor)})
	}
	return nil
}

// loadSecretsOnce decrypts the store once per change (§7.4: values dropped
// with the pipeline).
func (p *pipeline) loadSecretsOnce() (*secretSet, *refusal) {
	if !p.secRead {
		p.secrets, p.secErr = loadSecrets()
		p.secRead = true
	}
	return p.secrets, p.secErr
}

// saveCheckState runs §7.2's content checks and §7.4's secret check over a
// would-be state.
func (p *pipeline) saveCheckState(st *state, slug string, entries map[string]treeEntry) error {
	if r := validateState(slug, entries); r != nil {
		return p.refuse(st, r)
	}
	secrets, r := p.loadSecretsOnce()
	if r != nil {
		return p.refuse(st, r)
	}
	if r := secrets.scan(entries); r != nil {
		return p.refuse(st, r)
	}
	return nil
}

// refusalText renders a refusal: SKILL_EXISTS / SKILL_CHANGED /
// SKILL_MD_REQUIRED / INVALID_INPUT carry their own sentence; every other code
// is "the change to '<slug>' was not saved: <reason>".
func refusalText(slug string, r *refusal) string {
	for _, own := range []error{ErrSkillExists, ErrSkillChanged, ErrSkillMDRequired, ErrInvalidInput, ErrUnknownVersion, ErrNotFound, ErrLocalChangesConfirmationRequired} {
		if errors.Is(r.code, own) {
			return r.reason
		}
	}
	return fmt.Sprintf("the change to '%s' was not saved: %s", slug, r.reason)
}

// refuse returns a class-1 refusal naming the skill's current version (§7.5)
// and writes the refused audit line.
func (p *pipeline) refuse(st *state, r *refusal) error { return p.refuseText(st, r, "") }

func (p *pipeline) refuseText(st *state, r *refusal, suffix string) error {
	slug := p.req.target()
	if st != nil {
		slug = st.slug
	}
	text := refusalText(slug, r) + suffix
	if len(p.warnings) > 0 {
		text += " (" + strings.Join(p.warnings, "; ") + ")"
	}
	if st != nil && st.head != nil {
		text += fmt.Sprintf(" The skill's current version is %s.", st.head.ID)
	}
	auditRefusal(p.req, slug, r.reason)
	return &ChangeError{Code: r.code, Text: text}
}

// captureError maps a step-1 capture failure: a limit crossing is a
// SAVE_CHECK_FAILED refusal with the capture-time reason; anything else is a
// storage error.
func (p *pipeline) captureError(slug string, err error, suffix string) error {
	var lim *captureLimitError
	var instr *instructionLimitError
	switch {
	case errors.As(err, &lim):
		return p.refuseText(&state{slug: slug}, saveCheck(lim.reason), suffix)
	case errors.As(err, &instr):
		return p.refuseText(&state{slug: slug}, saveCheck(fmt.Sprintf("SKILL.md exceeds the instruction limit of %s bytes (%d bytes); reduce it and retry", formatCount(MaxSkillMarkdownBytes), instr.n)), suffix)
	}
	return p.storageErrorText(slug, err, suffix)
}

func (p *pipeline) storageError(slug string, err error) error {
	return p.storageErrorText(slug, err, "")
}

// storageErrorText is a class-1 storage error (§7.6: refused, reason
// storage_error).
func (p *pipeline) storageErrorText(slug string, err error, suffix string) error {
	text := fmt.Sprintf("the change to '%s' was not saved: %v%s", slug, err, suffix)
	if len(p.warnings) > 0 {
		text += " (" + strings.Join(p.warnings, "; ") + ")"
	}
	auditRefusal(p.req, slug, "storage_error")
	return &ChangeError{Code: ErrStorage, Text: text, cause: err}
}

// logGrantRemoval runs the founder's Q-S3 best-effort grant hygiene after a
// committed delete. A failure never fails the delete (SD-30 keeps the safety
// rule); it is logged and surfaced as a finishing warning.
func (p *pipeline) removeGrant(slug string) {
	if p.req.Grants == nil {
		return
	}
	if err := p.req.Grants.RemoveGrant(slug); err != nil {
		slog.Warn("skills: grant removal after delete failed; the stale grant stays inert (SD-30)", "skill", logSafe(slug), "error", logSafe(err.Error()))
		p.warnings = append(p.warnings, "grant cleanup failed: "+err.Error())
	}
}

// liveMap is a copy of the live entry table.
func (st *state) liveMap() map[string]treeEntry { return entryMapOf(st.entries) }

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
