// Omnipus — ADR-068 D16.2a / FR-020h: the refusal contract, as a reusable check.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"strings"
)

// ---------------------------------------------------------------------------
// WHY THIS EXISTS
//
// The static guard in propindex_caller_guard_test.go catches a caller that
// SWALLOWS the refusal. Nothing static can catch a caller that never asks for
// it — a vault_find that queries bleve, finds no typed candidates because there
// is no properties index, and reports "no records matched".
//
// That one has to be caught by running the real entry point on a build where
// the index is absent, and asserting what comes back. This is the check to run,
// so that each of the six entry points costs one line instead of a re-derivation
// of what "refuses by name" means:
//
//	func TestVaultFind_RefusesOnSQLiteLessBuild(t *testing.T) {
//	        records.AssertRefusesWhenIndexUnavailable(t, records.CapabilityTypedFilter,
//	                func() (any, error) { return vaultFind(ctx, typedFilterRequest) })
//	}
//
// It is a no-op on SQLite-capable builds, because there is nothing to refuse
// there. Run it under the forcing tag, which is what that tag is for:
//
//	go test -tags goolm,stdjson,records_no_sqlite ./...
//
// The six entry points that owe this assertion are ADR-068 D15's: typed filters
// and relation joins and grouping and aggregation in vault_find, check_integrity
// in vault_describe, and record-type declaration in vault_configure.
// ---------------------------------------------------------------------------

// RefusalTB is the slice of *testing.T this contract needs. Declared as an
// interface so a production file never imports "testing".
type RefusalTB interface {
	Helper()
	Errorf(format string, args ...any)
}

// AssertRefusesWhenIndexUnavailable checks that one entry point honours FR-020h:
// on a build without the properties index it must return a refusal that names
// the platform, and must NOT return a successful empty result.
//
// On a SQLite-capable build it does nothing — the entry point is expected to
// work there, and asserting otherwise would be asserting the opposite contract.
func AssertRefusesWhenIndexUnavailable[T any](
	t RefusalTB,
	capability PropertyIndexCapability,
	call func() (T, error),
) {
	t.Helper()
	if PropertyIndexAvailable {
		return
	}

	_, err := call()
	if err == nil {
		t.Errorf("%s: the entry point returned SUCCESS on a build with no properties index. "+
			"This is the empty-result failure FR-020h exists to prevent: the operator is told "+
			"there is nothing to find, when the truth is that the question cannot be answered "+
			"on this platform. It must return a refusal instead.", capability)
		return
	}
	if !errors.Is(err, ErrPropertyIndexUnavailable) {
		t.Errorf("%s: the entry point failed, but not with the platform refusal — got %v. "+
			"Return records.RequirePropertyIndex's error unchanged, or wrapped with %%w so "+
			"errors.Is still finds it.", capability, err)
		return
	}
	if platform := PropertyIndexPlatform(); !strings.Contains(err.Error(), platform) {
		t.Errorf("%s: the refusal reached the caller but no longer names the platform %q — "+
			"got %q. Wrapping must preserve the message, not replace it.",
			capability, platform, err.Error())
	}
}
