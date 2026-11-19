package omnpkg

// STUB FOR RED — DELETE when GREEN lands (task T-2, epic #1094, slice S1a).
//
// Compile-only stub of the exported API the S1 spec names, so the RED tests in
// this package compile and fail on their assertions (or on ErrNotImplemented),
// never with undefined symbols. Every function body returns ErrNotImplemented
// or a zero value. Types and constants are the spec-fixed shapes (error-code
// table, FR-002/FR-008 limits, Datasets D1–D13) and are safe to use in test
// fixtures; the behaviour behind them is not implemented.
//
// Spec: docs/internal/specs/export-import-s1-spec.md (revision 7 — the
// rollback contract C1/C2 and journal states follow the revision-7 text),
// ADR-export-import-packages.md (D1–D6), export-import-slices.md (S1a, T-2).

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotImplemented is returned by every stubbed function. GREEN replaces it
// with real behaviour; no test asserts this error (assertions name the spec
// oracle, so they fail red here and go green only on a correct implementation).
var ErrNotImplemented = errors.New("not implemented (pkg/omnpkg RED stub)")

// Spec-fixed format identity and limits (FR-002, FR-008; D1, D2, D9, D13).
const (
	FormatName          = "omnpkg"
	FormatVersion       = 1
	AgentSchemaVersion  = 1
	MaxDecodedBytes     = 256 << 20 // 256 MiB decoded stream (headers and padding included)
	MaxEntries          = 10000
	MaxManifestBytes    = 1 << 20 // 1 MiB
	MaxAgentFileBytes   = 1 << 20 // 1 MiB per agent-owned file
	MaxDecoderWindow    = 8 << 20 // 8 MiB zstd decoder max window
	MaxDecoderMemory    = 256 << 20
	MaxStagedImports    = 5
	StagingReadyTTL     = 30 * time.Minute
	MaxAgentNameLen     = 100
	ManifestEntry       = "manifest.json"
	AgentsPrefix        = "omnipus/agents/"
	AgentRecordName     = "agent.json"
	AgentSoulName       = "SOUL.md"
	AgentInstructionsNm = "AGENT.md"
)

// Error codes (Machine-Verifiable Constraints table; ErrorResponse.code).
const (
	CodeNotAPackage                = "not_a_package"
	CodePackageTooNew              = "package_too_new"
	CodeElementSchemaTooNew        = "element_schema_too_new"
	CodePackageTooLarge            = "package_too_large"
	CodeChecksumMismatch           = "checksum_mismatch"
	CodeManifestMismatch           = "manifest_mismatch"
	CodeUnsafePath                 = "unsafe_path"
	CodeUnsafeEntryType            = "unsafe_entry_type"
	CodeNoSupportedElement         = "no_supported_element"
	CodeTooManyAgents              = "too_many_agents"
	CodeElementTypeNotSupported    = "element_type_not_supported"
	CodeInvalidElement             = "invalid_element"
	CodeElementNotCreatable        = "element_not_creatable"
	CodeExternalWorkerNotSupported = "external_worker_not_supported_yet"
	CodeCoreAgentNotExportable     = "core_agent_not_exportable"
	CodeAgentFileUnreadable        = "agent_file_unreadable"
	CodeAgentPromptMissing         = "agent_prompt_missing"
	CodeStagingNotFound            = "staging_not_found"
	CodeTooManyStagedImports       = "too_many_staged_imports"
	CodeImportInProgress           = "import_in_progress"
	CodeNameSuffixExhausted        = "name_suffix_exhausted"
	CodeImportRolledBack           = "import_rolled_back"
	CodeImportRollbackIncomplete   = "import_rollback_incomplete"
)

// PackageError carries the wire error code plus the details.* entries the
// error table names (field, reason, limit, file, check).
type PackageError struct {
	Code   string
	Field  string
	Detail map[string]string
}

// Error implements the error interface.
func (e *PackageError) Error() string { return "omnpkg: " + e.Code }

// CodeOf returns the wire error code of err, or "" when err is nil or carries
// no code.
func CodeOf(err error) string {
	var pe *PackageError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// Manifest mirrors contracts/components/schemas/PackageManifest.yaml as written
// into the package's first entry (unknown manifest fields are ignored, FR-004).
type Manifest struct {
	Format           string              `json:"format"`
	FormatVersion    int                 `json:"format_version"`
	Scope            string              `json:"scope"`
	Elements         []ManifestElement   `json:"elements"`
	Counts           map[string]int      `json:"counts"`
	Files            map[string]FileInfo `json:"files"`
	SourceBuild      string              `json:"source_build"`
	CreatedAt        time.Time           `json:"created_at"`
	IncludesSessions bool                `json:"includes_sessions"`
	NeedsSecrets     []NeedsSecret       `json:"needs_secrets"`
}

// ManifestElement is one elements[] row.
type ManifestElement struct {
	Type     string   `json:"type"`
	Schema   int      `json:"schema"`
	SourceID string   `json:"source_id"`
	Name     string   `json:"name"`
	Files    []string `json:"files"`
}

// FileInfo is the per-file SHA-256 and size (FR-003).
type FileInfo struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// NeedsSecret is one needs_secrets[] row (name, used_by, kind).
type NeedsSecret struct {
	Name   string `json:"name"`
	UsedBy string `json:"used_by"`
	Kind   string `json:"kind"`
}

// ReadManifest reads and validates ONLY the manifest: first entry, bounded
// manifest size, format, format_version and element schema rules (FR-001,
// FR-004, FR-005); unknown fields ignored.
func ReadManifest(r io.Reader) (*Manifest, error) { return nil, ErrNotImplemented }

// Inspect reads only the bounded manifest and makes no integrity claim
// (FR-052); it writes nothing and needs no gateway.
func Inspect(r io.Reader) (*Manifest, error) { return nil, ErrNotImplemented }

// VerifyReport lists the payload files whose checksums verified.
type VerifyReport struct {
	FilesVerified []string
}

// Verify performs every format check (FR-001–FR-009, FR-052) and names the
// failing file.
func Verify(r io.Reader) (*VerifyReport, error) { return nil, ErrNotImplemented }

// ReadPackage performs every format check and returns the manifest and the
// decoded payload files keyed by entry path (the upload-side reader, FR-020).
func ReadPackage(r io.Reader) (*Manifest, map[string][]byte, error) {
	return nil, nil, ErrNotImplemented
}

// ValidateElements checks every element's record for the upload-time element
// rules: record JSON well-formedness, forbidden types (core/system), and the
// S1a external-worker refusal (FR-020, D1 rows 20, 22, 30, 31).
func ValidateElements(m *Manifest, payload map[string][]byte) error {
	return ErrNotImplemented
}

// Selection names what to export (S1 accepts exactly one agent, FR-010).
type Selection struct {
	AgentIDs []string
}

// ExportSource is the store edge the gateway fills (ADR D1): the engine never
// reads the data folder around the stores.
type ExportSource interface {
	// AgentRecord returns the stored record JSON for id.
	AgentRecord(id string) ([]byte, error)
	// AgentHomeDir returns the agent's home folder (holds SOUL.md / AGENT.md).
	AgentHomeDir(id string) (string, error)
	// IsCoreAgent reports built-in / system / shipped-subagent agents (D4).
	IsCoreAgent(id string) bool
}

// Export packs the selected agents into w (manifest first, FR-001/FR-002,
// Field mapping Export column, FR-018 bounded source reads).
func Export(ctx context.Context, src ExportSource, sel Selection, w io.Writer) error {
	return ErrNotImplemented
}

// ImportTarget is the target-install lookup edge for the import-side field
// mapping (Dataset D5).
type ImportTarget interface {
	InstalledSkill(slug string) bool
	KnownTool(name string) bool
	AlwaysAskTool(name string) bool
	ProviderConfigured(name string) bool
	GlobalMaxToolIterations() int
	SeedToolPolicies(agentType string) map[string]string
}

// Warning is one preview/result warning row (code, element_source_id, message,
// items).
type Warning struct {
	Code            string
	ElementSourceID string
	Message         string
	Items           []string
}

// AgentPlan is the engine's import-side mapping output: the record handed to
// the shared create function plus the listed warnings (Field mapping Import
// column; the create rules themselves stay in the shared function, T-3).
type AgentPlan struct {
	Record   []byte
	Warnings []Warning
}

// MapAgentForImport applies the engine-level import transformations (skills
// filtered against the target, MCP assignments dropped, tool policies
// filtered/tightened, legacy fields dropped with warnings, provider and
// max_tool_iterations checks against the target).
func MapAgentForImport(record []byte, sourceID string, target ImportTarget) (*AgentPlan, error) {
	return nil, ErrNotImplemented
}

// FreshAgentID returns a fresh UUID that is not in taken (FR-031).
func FreshAgentID(taken map[string]bool) (string, error) { return "", ErrNotImplemented }

// ResolveImportName decides the final name for an incoming agent against the
// existing names: unchanged, or " (imported)" / " (imported 2)" …
// " (imported 99)", kept within MaxAgentNameLen (FR-032, D6). Clash matching
// ignores letter case and surrounding spaces only.
func ResolveImportName(incoming string, existing []string) (string, error) {
	return "", ErrNotImplemented
}

// RollbackPlan carries the C1–C2 compensation steps (Rollback contract,
// revision 7: C3/C4 are retired — the agent is invisible until commit, so no
// reload step exists and a rollback answer never carries an activation).
// C1 is the two places membership/edges leave the core: remove X's team entry,
// then remove every delegation edge involving X. C2 is DiscardPending(X)'s two
// places: delete X.pending first, then delete agents/X/ with everything in it.
type RollbackPlan struct {
	// MembershipAttempted records that the membership write (E8) was attempted,
	// so C1 must run; a failure before E8 skips C1 entirely.
	MembershipAttempted  bool
	RemoveMembership     func() error // C1a: remove X's team entry
	RemoveEdges          func() error // C1b: remove every delegation edge involving X
	DiscardPendingRecord func() error // C2a: delete entities/agents/X.pending
	DiscardPendingFolder func() error // C2b: delete agents/X/ with everything inside it
}

// CompensationResult reports a rollback outcome: Complete, the failed step
// ("C1" or "C2" — the only values) and the leftovers named
// (import_rollback_incomplete shape; FR-037). There is no activation field.
type CompensationResult struct {
	Complete   bool
	FailedStep string
	Leftovers  []string
}

// RunRollback (revision 7) runs C1 (only when MembershipAttempted) and then C2
// ALWAYS — a C1 failure (membership removal, or a partial C1 after the
// membership was removed) does not skip C2. Within C2, the pending record is
// discarded first; a failed step stops the later places of the same step.
// FailedStep is "C1" or "C2"; leftovers are named either way (FR-037).
func RunRollback(plan RollbackPlan) CompensationResult {
	return CompensationResult{}
}

// Staging lifecycle states (Staging lifecycle table; FR-022, FR-039).
type StagingState string

const (
	StagingReceiving    StagingState = "receiving"
	StagingReady        StagingState = "ready"
	StagingApplying     StagingState = "applying"
	StagingDone         StagingState = "done"
	StagingNeedsCleanup StagingState = "needs-cleanup"
)

// JournalState is the journal's on-disk state (E1; Rollback contract,
// revision 7): started → committed → done, or rolled_back / rollback_incomplete.
// done and rolled_back are terminal; the rest are unfinished.
type JournalState string

const (
	JournalStarted            JournalState = "started"
	JournalCommitted          JournalState = "committed"
	JournalDone               JournalState = "done"
	JournalRolledBack         JournalState = "rolled_back"
	JournalRollbackIncomplete JournalState = "rollback_incomplete"
)

// JournalFile is the journal.json shape inside a staging folder. Declared here
// so RED fixtures and GREEN write the same bytes; the writer itself is GREEN's.
type JournalFile struct {
	State   JournalState `json:"state"`
	AgentID string       `json:"agent_id"`
}

// UnfinishedImport names a kept orphan staging for the unfinished_import_found
// warning (FR-044): agent ID and staging folder.
type UnfinishedImport struct {
	StagingID string
	AgentID   string
}

// StagingRegistry is the in-memory staging registry guarded by one lock
// (Staging lifecycle). now is the clock (fake in tests); base is the staging
// root folder for orphan handling.
type StagingRegistry struct{}

// NewStagingRegistry builds a registry over base with the given clock.
func NewStagingRegistry(base string, now func() time.Time) *StagingRegistry {
	return &StagingRegistry{}
}

// Reserve claims a slot (counting every live state plus kept orphans; at most
// MaxStagedImports) before decoding (FR-022).
func (r *StagingRegistry) Reserve() (string, error) { return "", ErrNotImplemented }

// MarkReady moves a receiving staging to ready; the expiry clock starts here.
func (r *StagingRegistry) MarkReady(id string) error { return ErrNotImplemented }

// State reports the staging's current state.
func (r *StagingRegistry) State(id string) (StagingState, error) {
	return "", ErrNotImplemented
}

// BeginApply atomically moves ready → applying (404 staging_not_found for any
// other state; 409 import_in_progress when an apply is already running
// anywhere, FR-038).
func (r *StagingRegistry) BeginApply(id string) error { return ErrNotImplemented }

// AbortApply returns an applying staging to ready after a refusal that wrote
// nothing, with the expiry clock unchanged (Staging lifecycle "End").
func (r *StagingRegistry) AbortApply(id string) error { return ErrNotImplemented }

// FinishApply completes an apply: done, folder deleted, ID consumed.
func (r *StagingRegistry) FinishApply(id string) error { return ErrNotImplemented }

// NeedsCleanup moves a staging to needs-cleanup (incomplete rollback): kept,
// never expires, counts in the slots (FR-022).
func (r *StagingRegistry) NeedsCleanup(id string) error { return ErrNotImplemented }

// Release drops a failed upload's slot and folder.
func (r *StagingRegistry) Release(id string) error { return ErrNotImplemented }

// Sweep is the per-upload maintenance: drop needs-cleanup entries whose folder
// is gone, delete expired ready stagings and orphan folders whose journal is
// absent or terminal (done / rolled_back), keep unfinished-journal orphans
// (started / committed / rollback_incomplete — they hold a slot and are
// returned for the unfinished_import_found warning, FR-044).
func (r *StagingRegistry) Sweep() []UnfinishedImport { return nil }
