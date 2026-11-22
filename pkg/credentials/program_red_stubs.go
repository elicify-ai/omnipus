// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED stubs — connected command-line programs, slice S1 (#1960).
//
// This file is qa-lead RED scaffolding, sanctioned by the dispatch brief
// ("add minimal empty stubs ONLY if the spec says where they live"). The spec
// names this package and these symbols (spec invariant I-3, FR-048, FR-049):
//
//   - ErrProgramOwned            (FR-048 names the sentinel)
//   - ErrStoreBusy               (FR-049 names it)
//   - CommitProgramCredential    (I-3: program-scoped store method)
//   - RemoveProgramCredential    (I-3: program-scoped store method)
//   - TransactHook               (the "test seam" of S-135/S-136/S-146; GREEN's
//     s.transact must invoke it, production keeps it nil)
//
// Every body here is an EMPTY stub (no-op / zero value) so the S1 tests
// compile and fail against real expected-vs-actual instead of not compiling.
// GREEN (backend-lead) replaces the bodies with the real implementations and
// deletes this file's scaffolding comments; the tests must not change.
// Symbol names the spec does not pin are listed in
// /workspace/logs/1960/s1-red/test-disputes.md.

package credentials

import "errors"

// ErrProgramOwned is returned by every ordinary mutating Store method when the
// target entry belongs to a connected program (spec FR-048, invariant I-3).
// The caller-visible text is "…belongs to the program <name>. Change it in
// Settings → Programs."; the REST/tool layers wrap this sentinel.
var ErrProgramOwned = errors.New("credential entry is owned by a connected program")

// ErrStoreBusy is returned when a store transaction cannot be admitted within
// its bound (5 s for ordinary writes, the verifying deadline for a verified
// commit), leaving the file unchanged (spec FR-049, R5-M3).
var ErrStoreBusy = errors.New("the credential store is busy")

// KindProgram is the card-secret recipient kind of a program-owned entry
// (spec contract table: recipient kind "program"; invariant I-3: "A
// program-owned entry is a card secret with recipient kind program").
const KindProgram = "program"

// TransactHook is the test seam of scenarios S-135, S-136 and S-146: the store
// transaction helper (s.transact, FR-049) invokes it with a stage name at each
// step, so a test can pause a writer INSIDE its transaction after the in-lock
// read ("after-read"). Production keeps it nil; only tests set it.
var TransactHook func(stage string)

// CommitProgramCredential is the ONLY method that may create or replace a
// program-owned credential entry (invariant I-3; only pkg/programs may call
// it — enforced by the TestCredentialStore_ProgramOwnedGuard source check).
//
// RED stub: no-op. GREEN writes the value, the optional CardReceipt and the
// program metadata (new generation, state) in one s.transact write and returns
// the new generation.
func (s *Store) CommitProgramCredential(reference string, value string, receipt *CardReceipt) (int64, error) {
	return 0, nil
}

// RemoveProgramCredential is the ONLY method that may remove a program-owned
// credential entry (invariant I-3; only pkg/programs may call it).
//
// RED stub: no-op. GREEN removes the value and program metadata under
// s.transact and returns the new generation after the disconnect.
func (s *Store) RemoveProgramCredential(reference string) error {
	return nil
}

// ProgramEntryGeneration reads the stored generation metadata of a
// program-owned entry (S-135 asserts "GitHub's generation 2 metadata" survives
// a concurrent unrelated write). Read-only; classified "read-only" by the
// S-127 classification table.
//
// RED stub: always absent.
func (s *Store) ProgramEntryGeneration(reference string) (int64, bool) {
	return 0, false
}
