// Package omnpkg is the .omnpkg package-format engine (epic #1094, slice S1a,
// task T-2): the manifest, the bounded tar+zstd reader and writer, the
// universal path and entry-type checks, the agent layout checks, the
// import-side field mapping, the name suffix rule, the staging registry, the
// journal and the rollback compensation order.
//
// It is a leaf engine: it never imports pkg/gateway (or its sub-packages) and
// never reads the data folder around the stores. The gateway fills the store
// edges (ExportSource, ImportTarget, RollbackPlan).
//
// Spec: docs/internal/specs/export-import-s1-spec.md (revision 7);
// ADR: docs/internal/architecture/ADR-export-import-packages.md.
package omnpkg

import (
	"errors"
	"sort"
	"strings"
)

// Error codes (spec "Machine-Verifiable Constraints"; ErrorResponse.code).
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
	CodeExportSelectionInvalid     = "export_selection_invalid"
	CodeStagingNotFound            = "staging_not_found"
	CodeTooManyStagedImports       = "too_many_staged_imports"
	CodeImportInProgress           = "import_in_progress"
	CodeNameSuffixExhausted        = "name_suffix_exhausted"
	CodeImportRolledBack           = "import_rolled_back"
	CodeImportRollbackIncomplete   = "import_rollback_incomplete"

	// B1 cancel lifecycle (spec "Cancel lifecycle"): a staging that is being
	// received, applied or kept for clean-up is never cancelled; a ready one
	// whose folder could not be removed is cancelled with removal pending.
	CodeStagingBusy          = "staging_busy"
	CodeStagingCleanupFailed = "staging_cleanup_failed"
)

// Values of details.limit on a package_too_large refusal (FR-008).
const (
	LimitWindow    = "window"     // zstd frame asks for a window over MaxDecoderWindow
	LimitDecoded   = "decoded"    // decoded stream over MaxDecodedBytes
	LimitEntries   = "entries"    // more than MaxEntries tar entries
	LimitManifest  = "manifest"   // manifest.json over MaxManifestBytes
	LimitAgentFile = "agent_file" // an agent-owned file over MaxAgentFileBytes
)

// Values of details.check on an agent_file_unreadable refusal (Dataset D11).
const (
	CheckLink       = "link"
	CheckNotRegular = "not_regular"
	CheckUnreadable = "unreadable"
	CheckTooLarge   = "too_large"
)

// PackageError carries the wire error code plus the details.* entries the
// error table names (field, reason, limit, file, check).
type PackageError struct {
	Code   string
	Field  string
	Detail map[string]string
}

// Error implements the error interface. The message names the field and the
// details (sorted), so a refusal always names the failing file (FR-003).
func (e *PackageError) Error() string {
	var b strings.Builder
	b.WriteString("omnpkg: ")
	b.WriteString(e.Code)
	if e.Field != "" {
		b.WriteString(" (field ")
		b.WriteString(e.Field)
		b.WriteString(")")
	}
	if len(e.Detail) > 0 {
		keys := make([]string, 0, len(e.Detail))
		for k := range e.Detail {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(" ")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(e.Detail[k])
		}
	}
	return b.String()
}

// CodeOf returns the wire error code of err, or "" when err is nil or carries
// no code.
func CodeOf(err error) string {
	var pe *PackageError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// newErr builds a coded error with optional key/value detail pairs.
func newErr(code string, kv ...string) *PackageError {
	pe := &PackageError{Code: code}
	if len(kv) > 0 {
		pe.Detail = make(map[string]string, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			pe.Detail[kv[i]] = kv[i+1]
		}
	}
	return pe
}

// fieldErr builds a coded error naming a request/record field.
func fieldErr(code, field, reason string) *PackageError {
	pe := &PackageError{Code: code, Field: field}
	if reason != "" {
		pe.Detail = map[string]string{"reason": reason}
	}
	return pe
}

// tooLarge builds a package_too_large refusal naming the limit.
func tooLarge(limit string) *PackageError {
	return newErr(CodePackageTooLarge, "limit", limit)
}
