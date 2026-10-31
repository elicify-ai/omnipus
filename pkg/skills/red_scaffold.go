package skills

// RED scaffolding — landing/skills (train T9, skills-as-folders, #1458).
//
// This file exists ONLY so the failing (RED) tests of the T9 spec
// (docs/internal/specs/skills-folder-1458-spec.md, from
// omnipus-coordination/designs-20261029/skills-spec-r13.md) compile and fail
// on behaviour instead of failing to build. Every symbol here is specified by
// the T9 spec and is NOT implemented yet: every body returns its zero value or
// a "not implemented" error, so any test that reaches one fails on the missing
// feature, never on a compile error.
//
// The implementing worker REPLACES this file's contents with the real
// implementation (keeping the declared names and signatures — the tests are
// written against them), then deletes this file's RED-SCAFFOLD markers.
// Nothing outside this file was changed by the RED phase.
//
// Fault seams: §7.6 (T-V11) mandates fault injection "by returned error (a
// writer hook that fails the blob write, the manifest write, the .swap
// removal, the audit write ...)"; §9.5 mandates an open-boundary probe and a
// consumption counter around every capture and instruction read. The
// implementation MUST route the corresponding operations through the seam
// variables below (nil seam = no fault / no observation), so the tests can
// inject errors and observe the capture boundary without any test-only build
// tag.

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// redNotImplemented is the sentinel error every scaffold body returns.
var redNotImplemented = errors.New("skills: RED scaffold — not implemented (landing/skills T9)")

// SkillKey returns the one skill identity of a slug: strings.ToLower(slug)
// (§5.13). Two slugs are the same skill when their keys are equal.
func SkillKey(slug string) string {
	_ = redNotImplemented
	return "" // RED scaffold
}

// IsBuiltin reports whether slug's skill key (§5.13) is the key of a current
// built-in skill (§8.1). Case-insensitive: Plan, PLAN and plan are all built-in.
func IsBuiltin(slug string) bool {
	_ = redNotImplemented
	return false // RED scaffold
}

// RetiredBuiltinNames returns the 14 retired built-in skill names (F2, §8.4).
func RetiredBuiltinNames() []string {
	_ = redNotImplemented
	return nil // RED scaffold
}

// GrantEditor is the one-method interface the gateway implements over the
// agent roster (§8.2). skills must not import the agent store.
type GrantEditor interface {
	// RemoveGrant removes name (by skill key, §5.13) from every agent's grant
	// list, in memory and in each stored record, changing only the skills
	// list. Idempotent.
	RemoveGrant(name string) error
}

// BuiltinSyncCase mirrors §8.2's per-built-in outcome table.
type BuiltinSyncCase string

const (
	// BuiltinInstalled: no folder existed; copyEmbeddedSkill installed it.
	BuiltinInstalled BuiltinSyncCase = "installed"
	// BuiltinUnchanged: content equals the shipped revision; folder untouched.
	BuiltinUnchanged BuiltinSyncCase = "unchanged"
	// BuiltinUpdated: content differed (stamped or not); replaced via .swap.
	BuiltinUpdated BuiltinSyncCase = "updated"
)

// BuiltinOutcome records what SyncBuiltins did for one built-in skill.
type BuiltinOutcome struct {
	Slug    string
	Case    BuiltinSyncCase
	OldRev  string // content revision before the change ("" when absent)
	NewRev  string // shipped revision after the change ("" when unchanged)
	Stamped bool   // stamp present after the case
}

// RetiredOutcome records one §8.4 retirement case.
type RetiredOutcome struct {
	Slug          string
	FolderDeleted bool     // stamped folder deleted
	Kept          bool     // unstamped folder kept (skill.builtin_retired_kept)
	AgentsChanged []string // agents whose grant list lost the name
}

// SyncReport carries the start-up facts (§8.5): the audit events are replayed
// from it later, at the existing wiring point, because no audit logger exists
// yet when SyncBuiltins runs.
type SyncReport struct {
	Updated     []BuiltinOutcome // replaced built-ins (skill.builtin_updated)
	Installed   []string         // newly installed built-in slugs
	Retired     []RetiredOutcome // §8.4 outcomes
	SweptGrants []string         // §5.13a dangling-grant sweep removals (by slug)
}

// SyncBuiltins replaces the legacy embedded-skills seed routine (§8.2). It runs at every gateway start
// under the mutation lock, after .swap recovery, before any agent is built:
// removes stale skills/.seed-* entries, installs missing built-ins, replaces
// every built-in folder that differs from the shipped version (F-28: an
// earlier in-place edit is overwritten, not kept), writes a missing stamp on
// an equal folder, and never records history for a built-in.
func SyncBuiltins(root string, grants GrantEditor) (SyncReport, error) {
	_ = redNotImplemented
	return SyncReport{}, redNotImplemented // RED scaffold
}

// RetireBuiltins processes §8.4 after every current built-in was handled
// without error in this start (otherwise it is skipped and logged). Boot
// order (§8.5): .swap recovery → SyncBuiltins → RetireBuiltins → the §5.13a
// dangling-grant sweep.
func RetireBuiltins(root string, grants GrantEditor, report *SyncReport) error {
	_ = redNotImplemented
	return redNotImplemented // RED scaffold
}

// SweepDanglingGrants is the §5.13a start-up sweep: every grant name whose
// key's newest history manifest is a delete_skill head and whose live folder
// is absent is removed (logged, idempotent), after retirement.
func SweepDanglingGrants(root string, grants GrantEditor, report *SyncReport) error {
	_ = redNotImplemented
	return redNotImplemented // RED scaffold
}

// ChangeKind enumerates §7.1's operations (the manifest `kind` values of §5.11).
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

// ChangeRequest is one ApplyChange operation (§7.1). Exactly the fields the
// Kind needs are read; the rest must stay zero.
type ChangeRequest struct {
	Kind      ChangeKind
	Slug      string // target skill slug (for copy: the SOURCE; To is the destination)
	To        string // copy destination slug (KindCopy only)
	Path      string // delete_file path, relative to the folder
	Version   string // restore version id
	Content   []byte // single-file writes (edit, create SKILL.md)
	StagedDir string // staged folder for restore / copy / install publication
	Expect    Expectation
	Actor     ChangeActor
	Grants    GrantEditor // §5.13a: delete-time grant removal (may be nil in tests)
}

// PublishOutcome is reused unchanged from the pinned code (§7.6).
// (Defined in revision.go; nothing new here.)

// ApplyChange is the ONE change pipeline (§0 principle 3, §7.1): every change
// to an installed skill — file tool, edit_skill, install, REST — goes through
// it, under WithMutationLock, with the save check, version commit and audit.
// It is also where §7.2's per-operation validation and §7.6's outcome classes
// live.
func ApplyChange(root string, req ChangeRequest) (PublishOutcome, error) {
	_ = redNotImplemented
	return PublishOutcome{}, redNotImplemented // RED scaffold
}

// Error codes (§5.10, byte-for-byte texts live in the implementation; tests
// match the sentinel + text). Sentinel errors let tests errors.Is on the code.
var (
	// ErrBuiltinReadOnly — BUILTIN_READ_ONLY.
	ErrBuiltinReadOnly = errors.New("BUILTIN_READ_ONLY")
	// ErrBuiltinNameReserved — BUILTIN_NAME_RESERVED.
	ErrBuiltinNameReserved = errors.New("BUILTIN_NAME_RESERVED")
	// ErrSkillChanged — SKILL_CHANGED.
	ErrSkillChanged = errors.New("SKILL_CHANGED")
	// ErrSkillNotRead — SKILL_NOT_READ.
	ErrSkillNotRead = errors.New("SKILL_NOT_READ")
	// ErrSkillExists — SKILL_EXISTS.
	ErrSkillExists = errors.New("SKILL_EXISTS")
	// ErrSkillNameGranted — SKILL_NAME_GRANTED (r13, §5.13a). PENDING GRILL.
	ErrSkillNameGranted = errors.New("SKILL_NAME_GRANTED")
	// ErrSkillPartiallySaved — SKILL_PARTIALLY_SAVED.
	ErrSkillPartiallySaved = errors.New("SKILL_PARTIALLY_SAVED")
	// ErrSkillWriteNotApproved — SKILL_WRITE_NOT_APPROVED.
	ErrSkillWriteNotApproved = errors.New("SKILL_WRITE_NOT_APPROVED")
	// ErrSaveCheckFailed — SAVE_CHECK_FAILED.
	ErrSaveCheckFailed = errors.New("SAVE_CHECK_FAILED")
	// ErrSecretInSkill — SECRET_IN_SKILL.
	ErrSecretInSkill = errors.New("SECRET_IN_SKILL")
	// ErrSecretCheckUnavailable — SECRET_CHECK_UNAVAILABLE.
	ErrSecretCheckUnavailable = errors.New("SECRET_CHECK_UNAVAILABLE")
	// ErrLocalChangesConfirmationRequired — LOCAL_CHANGES_CONFIRMATION_REQUIRED.
	ErrLocalChangesConfirmationRequired = errors.New("LOCAL_CHANGES_CONFIRMATION_REQUIRED")
	// ErrSkillMDRequired — SKILL_MD_REQUIRED.
	ErrSkillMDRequired = errors.New("SKILL_MD_REQUIRED")
	// ErrSkillInstructionLimit — SKILL_INSTRUCTION_LIMIT (r10).
	ErrSkillInstructionLimit = errors.New("SKILL_INSTRUCTION_LIMIT")
	// ErrSkillInstructionUnresolved — SKILL_INSTRUCTION_UNRESOLVED (r11).
	ErrSkillInstructionUnresolved = errors.New("SKILL_INSTRUCTION_UNRESOLVED")
)

// Fault seams (§7.6 T-V11 — returned-error injection). The implementation
// routes the corresponding operation through each seam: a non-nil seam whose
// result is non-nil fails that operation with the returned error.
var (
	// FaultManifestWrite fails a version-manifest WriteFileAtomic (step 6).
	FaultManifestWrite func(path string) error
	// FaultBlobWrite fails a history-blob write (step 1 capture / step 6 finalize).
	FaultBlobWrite func(path string) error
	// FaultSwapRemove fails the skills/.swap/<slug> removal (step 7).
	FaultSwapRemove func(path string) error
	// FaultPutBackRename fails the 1b-del/1b put-back rename.
	FaultPutBackRename func(oldpath, newpath string) error
	// FaultAuditWrite fails the audit emit (step 7).
	FaultAuditWrite func(event string) error
	// FaultStagingRemove fails the capture-attempt scratch disposal (§9.5).
	FaultStagingRemove func(path string) error
	// FaultForwardRename fails the step-5 live→.swap rename itself
	// (S-11.9 / S-11.15's "step 5 swap failed").
	FaultForwardRename func(oldpath, newpath string) error
	// FaultLiveWrite fails the step-5 single-file WriteFileAtomic after the
	// parent directory was created and before the file published
	// (S-11.16).
	FaultLiveWrite func(path string) error
	// FaultEmptyDirCleanup fails step 5's empty-directory cleanup os.Remove
	// after a failed first create (S-11.16 variant).
	FaultEmptyDirCleanup func(path string) error
)

// SecretValuesProvider is §7.4's secret-check input: the save check calls it
// for the current names-and-values enumeration (wired to
// credentials.Store.SensitiveNamedValues; nil = no store file, an empty
// store). RED scaffolding seam — the implementation may replace it with its
// own injection point, but the save check MUST consult the current store
// through an equivalent seam so tests can drive locked/unreadable/damaged
// stores.
var SecretValuesProvider func() ([]credentials.NamedValue, error)

// Capture probes (§9.5 canary + bounded-capture oracles). The implementation
// calls CaptureOpenProbe for EVERY open attempt on the capture/instruction
// path (op: "capture" | "instruction" | "metadata"), and CaptureConsumedProbe
// with the running byte count of every bounded read. Nil seams are no-ops.
var (
	CaptureOpenProbe     func(op, path string)
	CaptureConsumedProbe func(path string, n int64)
)

// ShippedExportHook overrides where SyncBuiltins copies the shipped package
// from (nil = the real ExportEmbeddedPackage). Test seam for S-7.9's
// "a current built-in install fails" case; the implementation routes its
// built-in staging through this seam.
var ShippedExportHook func(name, destDir string) error

// RecoverInterrupted is §9.3's start-up recovery (bootSkills step 1), under
// the lock, before SyncBuiltins: .swap and legacy <slug>.replace-backup
// put-back/removal rules, capture-blob temporaries (blobs/.incoming-*) and
// skills/.staging/.incoming-* removal.
func RecoverInterrupted(skillsRoot, historyRoot string) error {
	_ = redNotImplemented
	return redNotImplemented // RED scaffold
}

// RetentionSweep is §9.1 (runs daily from gateway executeSweepTick, per skill
// under the lock, released between skills). windowDays ≤ 0 means the 90-day
// default; disabled keeps every committed version and blob (and NEVER cleans
// capture temporaries — §9.1 scope, §9.5 owns those).
func RetentionSweep(historyRoot string, windowDays int, disabled bool, now time.Time) error {
	_ = redNotImplemented
	return redNotImplemented // RED scaffold
}

// ContentRevision is §7.3's proposed revision function: regular files hashed
// by relative path + executable bit + bytes; non-regular entries hashed as
// type + link target (never dereferenced); directories ignored;
// .versions/, .omnipus-builtin and .skill-origin.json skipped; ErrNotFound
// only when the folder is absent. It replaces revisionForDir's
// ErrNotFound-on-missing-SKILL.md behaviour (§5.11) and, unlike the pinned
// hasher, it is sensitive to the executable bit (R8-MIN-002).
func ContentRevision(dir string) (string, error) {
	_ = redNotImplemented
	return "", redNotImplemented // RED scaffold
}

// SkillHistoryRoot derives §5.11's history location from the skills root:
// `<parent-of-skills>/system/skill-history/` ($OMNIPUS_HOME is the skills
// root's parent). RED scaffolding: declares the one derivation every test
// and the pipeline share; the implementation replaces the body.
func SkillHistoryRoot(skillsRoot string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(skillsRoot)), "system", "skill-history")
}

// LoadActivationOptions carries what §5.7's activation header needs beyond
// the slug.
type LoadActivationOptions struct {
	// BuiltinRoot is the shipped built-ins root; a slug resolved there
	// loads with shelf="builtin" (no revision/version, §5.7).
	BuiltinRoot string
	// EditAllowed reports whether the effective edit_skill policy is not
	// deny (§5.7's `edit:` clause).
	EditAllowed bool
	// PATHEnv is the PATH the agent's bash gets; omnipus-requires names are
	// PATH-looked-up against it ("" = the process PATH).
	PATHEnv string
}

// LoadForActivation renders §5.7's activation document (header +
// instructions) for one granted skill, running outside-change detection
// first (§9.4) and recording "seen" (§7.3). RED scaffolding: the §5.7
// renderer replaces this body.
func LoadForActivation(skillsRoot, slug string, opts LoadActivationOptions) (string, error) {
	return "", redNotImplemented
}
