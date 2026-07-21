// Package audit: redaction shim. The canonical Redactor lives in
// pkg/logredact so the runtime logger (pkg/logger) and the audit pipeline
// (pkg/audit) can share the same two-layer redaction without an import
// cycle. This file keeps the audit-package API stable for the rest of
// pkg/audit (and its tests).
package audit

import "github.com/dapicom-ai/omnipus/pkg/logredact"

// Redactor is the audit-package alias for the canonical logredact Redactor.
// All public methods of logredact.Redactor are reachable through this type.
type Redactor = logredact.Redactor

// NewRedactor is a thin wrapper over logredact.NewRedactor, kept so the
// existing audit call sites (and tests) need no change.
func NewRedactor(customPatterns []string) (*Redactor, error) {
	return logredact.NewRedactor(customPatterns)
}

// DisabledRedactor is a thin wrapper over logredact.DisabledRedactor.
func DisabledRedactor() *Redactor {
	return logredact.DisabledRedactor()
}
