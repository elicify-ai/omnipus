package skills

// Error codes, fault seams and capture probes of the one change pipeline
// (docs/internal/specs/skills-folder-1458-spec.md §5.10, §7.6, §9.5).
//
// Fault seams (§7.6 T-V11) and probes (§9.5) are package variables so the
// pipeline's storage edges can be failed by returned error and its capture
// boundary observed without a test-only build tag. A nil seam is a no-op.

import (
	"errors"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// Error codes (§5.10). Every refusal wraps exactly one of these sentinels so
// callers can errors.Is on the code; the error text carries the byte-exact
// §5.10 wording.
var (
	ErrBuiltinReadOnly                  = errors.New("BUILTIN_READ_ONLY")
	ErrBuiltinNameReserved              = errors.New("BUILTIN_NAME_RESERVED")
	ErrSkillChanged                     = errors.New("SKILL_CHANGED")
	ErrSkillNotRead                     = errors.New("SKILL_NOT_READ")
	ErrSkillExists                      = errors.New("SKILL_EXISTS")
	ErrSkillPartiallySaved              = errors.New("SKILL_PARTIALLY_SAVED")
	ErrSkillWriteNotApproved            = errors.New("SKILL_WRITE_NOT_APPROVED")
	ErrSaveCheckFailed                  = errors.New("SAVE_CHECK_FAILED")
	ErrSecretInSkill                    = errors.New("SECRET_IN_SKILL")
	ErrSecretCheckUnavailable           = errors.New("SECRET_CHECK_UNAVAILABLE")
	ErrLocalChangesConfirmationRequired = errors.New("LOCAL_CHANGES_CONFIRMATION_REQUIRED")
	ErrSkillMDRequired                  = errors.New("SKILL_MD_REQUIRED")
	ErrSkillInstructionLimit            = errors.New("SKILL_INSTRUCTION_LIMIT")
	ErrSkillInstructionUnresolved       = errors.New("SKILL_INSTRUCTION_UNRESOLVED")
	ErrSkillGrantStale                  = errors.New("SKILL_GRANT_STALE")
	ErrUnknownVersion                   = errors.New("UNKNOWN_VERSION")
	// ErrInvalidInput marks a malformed request (an invalid slug or path
	// argument) — the tool-level INVALID_INPUT class.
	ErrInvalidInput = errors.New("INVALID_INPUT")
	// ErrStorage marks a class-1 storage error (§7.6: refused with reason
	// storage_error, REST 500).
	ErrStorage = errors.New("STORAGE_ERROR")
)

// Fault seams (§7.6 T-V11 — returned-error injection). A non-nil seam whose
// result is non-nil fails that operation with the returned error.
var (
	// FaultManifestWrite fails a version-manifest write (step 1 checkpoint and
	// step 6). path is the manifest's final path.
	FaultManifestWrite func(path string) error
	// FaultBlobWrite fails the promotion of a new blob final (step 6 and the
	// step-1 checkpoint commit). path is the final blob path.
	FaultBlobWrite func(path string) error
	// FaultSwapRemove fails the skills/.swap/<slug> removal (step 7).
	FaultSwapRemove func(path string) error
	// FaultPutBackRename fails the 1b-del/1b put-back rename.
	FaultPutBackRename func(oldpath, newpath string) error
	// FaultAuditWrite fails the audit emit (step 7).
	FaultAuditWrite func(event string) error
	// FaultStagingRemove fails the capture-attempt scratch disposal (§9.5).
	FaultStagingRemove func(path string) error
	// FaultForwardRename fails the step-5 live→.swap rename.
	FaultForwardRename func(oldpath, newpath string) error
	// FaultLiveWrite fails the step-5 single-file write after the parent
	// directory exists and before the file publishes (S-11.16).
	FaultLiveWrite func(path string) error
	// FaultEmptyDirCleanup fails step 5's empty-directory cleanup after a
	// failed first create (S-11.16 variant).
	FaultEmptyDirCleanup func(path string) error
)

// SecretValuesProvider is §7.4's secret-check input: the current names and
// values of every stored secret. The gateway wires it to
// credentials.Store.SensitiveNamedValues. nil means no credential store (an
// empty store — allowed).
var SecretValuesProvider func() ([]credentials.NamedValue, error)

// Capture probes (§9.5). CaptureOpenProbe is called for every open attempt on
// the capture and instruction paths (op: "capture" | "instruction" |
// "metadata"); CaptureConsumedProbe receives the running byte count of every
// bounded read. Nil seams are no-ops.
var (
	CaptureOpenProbe     func(op, path string)
	CaptureConsumedProbe func(path string, n int64)
)

// ShippedExportHook overrides where SyncBuiltins copies a shipped package
// from (nil = ExportEmbeddedPackage).
var ShippedExportHook func(name, destDir string) error

func probeOpen(op, path string) {
	if p := CaptureOpenProbe; p != nil {
		p(op, path)
	}
}

func probeConsumed(path string, n int64) {
	if p := CaptureConsumedProbe; p != nil {
		p(path, n)
	}
}

// skillsGeneration counts live-state changes of every skills root (§7.6 rule
// (a)): the menu and agent caches compare it to decide whether to rescan.
var skillsGeneration atomic.Uint64

// Generation returns the current skills generation counter.
func Generation() uint64 { return skillsGeneration.Load() }

func bumpGeneration() { skillsGeneration.Add(1) }

// auditLogger is the audit sink for skill.write events. It is wired after the
// agent loop builds its logger (SetAuditLogger); before that no audit line is
// written (the §8.5 audit-delivery rule applies to boot events, which travel
// in SyncReport instead).
var auditLogger atomic.Pointer[audit.Logger]

// SetAuditLogger wires the audit logger skill.write events go to.
func SetAuditLogger(l *audit.Logger) { auditLogger.Store(l) }

// emitAudit writes one audit entry. A failure is counted by the existing
// audit-degraded counter and never reported to the agent (§7.6 class 3).
func emitAudit(entry *audit.Entry) {
	if f := FaultAuditWrite; f != nil {
		if err := f(entry.Event); err != nil {
			audit.IncSkipped(entry.Event, entry.Decision)
			return
		}
	}
	audit.EmitEntry(auditLogger.Load(), entry)
}
