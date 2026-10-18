// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"errors"
	"os"
	"sort"
	"syscall"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const receiptCleanupRepair = "Check that the credential store file is a readable, writable file " +
	"and that its directory permissions and free disk space are intact. Cleanup is retried on the " +
	"next secret request operation and at restart; saved secrets and their bindings are not affected."

// storageCauseCategory classifies a storage failure into a safe,
// machine-readable cause. The raw error text is never logged or returned:
// it can contain paths or lower-level detail that is not ours to expose.
func storageCauseCategory(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, credentials.ErrStoreLocked):
		return "store_locked"
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
	if errors.As(err, &pathErr) {
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
