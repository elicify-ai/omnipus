// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"syscall"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Public, value-free messages and repair guidance for storage failures.
const (
	repairCredentialStore = "check that the credential store is unlocked, readable and writable and has free disk space, then submit again"
	repairSessionStorage  = "check the session storage directory permissions and free disk space, then try again"
	repairRecovery        = "repair or restore the saved secret request metadata, or unlock the credential store, then restart the gateway"
	msgValuesNotSaved     = "secret values could not be saved; the request is still pending — " + repairCredentialStore
	msgSavedUnconfirmed   = "secret saved; the request state or its continuation is not confirmed — check the request state and reopen this chat to retry; do not enter the secret again"
	msgStateUnavailable   = "saved secret request state is unavailable — " + repairRecovery
	msgStateUnreconciled  = "secret request state could not be reconciled — check the session storage, then try again"
)

const receiptCleanupRepair = "Check that the credential store file is a readable, writable file " +
	"and that its directory permissions and free disk space are intact. Cleanup is retried on the " +
	"next secret request operation and at restart; saved secrets and their bindings are not affected."

// errInvalidRequestMetadata marks saved request metadata that decoded but is
// not a valid request record (its typed cause for diagnostics).
var errInvalidRequestMetadata = errors.New("invalid saved secret request metadata")

// storageFaultError is a value-free error: Error() is a fixed, safe sentence with
// repair guidance; the typed lower-level cause stays reachable through
// errors.Is/As but its text is never shown or logged.
type storageFaultError struct {
	message string
	cause   error
}

func (e *storageFaultError) Error() string { return e.message }
func (e *storageFaultError) Unwrap() error { return e.cause }

// logStorageFault writes one Error-level, value-free diagnostic for a failed
// storage stage: the request and session ids (when known), the stage, a safe
// cause category and the repair guidance. Never the raw error text.
func logStorageFault(stage, requestID, sessionID string, err error, repair string) {
	fields := map[string]any{
		"session_id":     sessionID,
		"stage":          stage,
		"cause_category": storageCauseCategory(err),
		"repair":         repair,
	}
	if requestID != "" {
		fields["request_id"] = requestID
	}
	logger.ErrorCF("secretrequest", "Secret request storage failed at stage "+stage+"; "+repair, fields)
}

// storageCauseCategory classifies a storage failure into a safe,
// machine-readable cause. The raw error text is never logged or returned:
// it can contain paths or lower-level detail that is not ours to expose.
func storageCauseCategory(err error) string {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, credentials.ErrStoreLocked):
		return "store_locked"
	case errors.Is(err, errInvalidRequestMetadata), errors.As(err, &syntaxErr), errors.As(err, &typeErr):
		return "invalid_metadata"
	case errors.Is(err, syscall.EISDIR):
		return "is_directory"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, syscall.ENOSPC):
		return "disk_full"
	case errors.Is(err, os.ErrNotExist):
		return "not_found"
	}
	var pathErr *os.PathError
	var linkErr *os.LinkError
	var errno syscall.Errno
	if errors.As(err, &pathErr) || errors.As(err, &linkErr) || errors.As(err, &errno) {
		return "io_error"
	}
	return "storage_failed"
}

// retryReceiptMaintenanceLocked removes the encrypted-store receipts of
// requests retention already removed from published state. It deletes receipt
// metadata only, never values or recipient bindings, and pruning an absent
// receipt is a no-op. On failure the receipts stay queued and each one gets an
// Error-level, value-free diagnostic with a safe cause and repair guidance.
func (r *Registry) retryReceiptMaintenanceLocked() {
	if len(r.receiptRetry) == 0 {
		return
	}
	if r.opts.Credentials == nil {
		// No credential store is configured, so no receipt can exist.
		clear(r.receiptRetry)
		return
	}
	ids := make([]string, 0, len(r.receiptRetry))
	for id := range r.receiptRetry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	store, err := r.opts.Credentials()
	if err == nil && store == nil {
		err = errors.New("secret store is unavailable")
	}
	if err == nil {
		err = store.PruneCardReceipts(ids)
	}
	if err == nil {
		clear(r.receiptRetry)
		return
	}
	category := storageCauseCategory(err)
	for _, id := range ids {
		logger.ErrorCF("secretrequest",
			"Secret request receipt cleanup failed after the request state was saved; check the credential store file and retry",
			map[string]any{
				"request_id":     id,
				"session_id":     r.receiptRetry[id],
				"stage":          "receipt_cleanup",
				"cause_category": category,
				"repair":         receiptCleanupRepair,
			})
	}
}
