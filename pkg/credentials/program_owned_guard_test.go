// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-127 (TestCredentialStore_ProgramOwnedGuard), invariant
// I-3, FR-048, FR-045 (guard-test requirement).
//
// Expected values are derived from the SPEC only (the I-3 classification
// table, FR-048's refusal contract and its exceptions). No value was read
// from an implementation: the guarded behaviour does not exist yet, and the
// RED stubs in program_red_stubs.go are empty no-ops.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- fixtures ---------------------------------------------------------------

const testKeyLen = 32 // AES-256 key; existing store tests use 32-byte keys.

// newUnlockedGuardStore opens a fresh store at dir/credentials.json, unlocked
// with a fixed 32-byte test key.
func newUnlockedGuardStore(t *testing.T) (*Store, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store := NewStore(path)
	key := make([]byte, testKeyLen)
	for i := range key {
		key[i] = byte(0x41 + i) // arbitrary, deterministic, non-zero
	}
	require.NoError(t, store.UnlockWithKey(key), "unlock fresh store")
	return store, path, key
}

// reopenUnlockedGuardStore reopens the store file to prove on-disk state.
func reopenUnlockedGuardStore(t *testing.T, path string, key []byte) *Store {
	t.Helper()
	store := NewStore(path)
	require.NoError(t, store.UnlockWithKey(key), "reopen store")
	return store
}

// decryptedSnapshot returns every name → decrypted value plus the card-secret
// metadata and receipts, for byte-identical before/after comparisons.
type storeSnapshot struct {
	values   map[string]string
	cardMeta map[string]CardSecret
	receipts map[string]CardReceipt
	progGen  map[string]int64
}

func snapshotStore(t *testing.T, store *Store) storeSnapshot {
	t.Helper()
	names, err := store.List()
	require.NoError(t, err)
	snap := storeSnapshot{
		values:   map[string]string{},
		cardMeta: map[string]CardSecret{},
		receipts: map[string]CardReceipt{},
		progGen:  map[string]int64{},
	}
	for _, n := range names {
		v, getErr := store.Get(n)
		require.NoError(t, getErr, "decrypt %s for snapshot", n)
		snap.values[n] = v
		gen, ok := store.ProgramEntryGeneration(n)
		if ok {
			snap.progGen[n] = gen
		}
	}
	meta, err := store.CardSecretMetadata()
	require.NoError(t, err)
	for k, v := range meta {
		snap.cardMeta[k] = v
	}
	// Receipts are enumerable through the metadata's request bindings; the
	// guard snapshot covers the ones this fixture creates (fixed ids).
	for _, id := range []string{guardReqID, guardReqID2} {
		if r, ok, err := store.CardReceipt(id); err == nil && ok {
			snap.receipts[id] = r
		}
	}
	return snap
}

func requireSnapshotsEqual(t *testing.T, before, after storeSnapshot) {
	t.Helper()
	require.Equal(t, before.values, after.values, "decrypted values changed")
	require.Equal(t, before.cardMeta, after.cardMeta, "card-secret metadata changed")
	require.Equal(t, before.receipts, after.receipts, "card receipts changed")
	require.Equal(t, before.progGen, after.progGen, "program generation metadata changed")
}

const (
	guardRef    = "prog_github_token" // reference R of the program-owned entry
	guardRef2   = "prog_fly_token"
	guardReqID  = "req-guard-1"
	guardReqID2 = "req-guard-2"
)

// --- S-127 (a): classification covers every exported method -----------------

// guardMethodClass is the SPEC's I-3 classification (rev 8 table "Existing raw
// credential writers"): read-only, guarded, program-scoped, re-encrypt-only,
// receipt-only. Every exported *Store method must have exactly one row here;
// an unclassified method (a NEW mutator) fails the test — that is the guard.
var guardMethodClass = map[string]string{
	// read-only (no credential value may change through them)
	"CardInventory": "read-only", "CardReceipt": "read-only",
	"CardSecretMetadata": "read-only", "Close": "read-only",
	"DeriveSubkey": "read-only", "Exists": "read-only", "Get": "read-only",
	"HealthCheck": "read-only", "IsLocked": "read-only", "List": "read-only",
	"Path": "read-only", "PrivateEnvironment": "read-only",
	"ProgramEntryGeneration": "read-only", "SensitiveNamedValues": "read-only",
	"SensitiveValues": "read-only", "UnlockWithKey": "read-only",
	"UnlockWithPassphrase": "read-only",
	// guarded mutators: refuse a program-owned name with ErrProgramOwned
	"Set": "guarded", "Delete": "guarded", "RemoveReference": "guarded",
	"SelectPrivateVersion": "guarded", "SetPrivateChannelAvailability": "guarded",
	"CommitCard": "guarded",
	// program-scoped: the only writers of program-owned entries
	"CommitProgramCredential": "program-scoped", "RemoveProgramCredential": "program-scoped",
	// re-encrypt-only: may re-encrypt, never change values
	"Rotate": "re-encrypt-only", "RotateWithPassphrase": "re-encrypt-only",
	// receipt-only: may remove receipts only, never values
	"PruneCardReceipts": "receipt-only", "PruneOrphanCardReceipts": "receipt-only",
}

func TestCredentialStore_ProgramOwnedGuard(t *testing.T) {
	t.Run("classification_covers_every_exported_method", func(t *testing.T) {
		// Oracle: spec I-3 — "reflection over the exported method set of
		// *Store: each method must have a row in the
		// classification table … an unclassified method fails the test".
		typ := reflect.TypeFor[*Store]()
		seen := map[string]bool{}
		require.Empty(t, unclassifiedStoreMethods(typ), "every exported Store method needs an I-3 row")
		for i := 0; i < typ.NumMethod(); i++ {
			name := typ.Method(i).Name
			seen[name] = true
			require.NotEmpty(t, guardMethodClass[name], "Store.%s classified with empty class", name)
		}
		// Every table row must still name a real method, so the table cannot
		// rot silently.
		for name := range guardMethodClass {
			require.True(t, seen[name], "classification row names non-existent method Store.%s", name)
		}
	})

	t.Run("commit_card_refuses_program_recipient_kind", func(t *testing.T) {
		// Oracle: I-3 table — CommitCard is "guarded for recipient kind
		// program: only CommitProgramCredential may write it" (FR-048).
		store, _, _ := newUnlockedGuardStore(t)
		_, err := store.CommitCard(guardReqID, "sess-1", []CardValue{{
			BaseName:  guardRef,
			Recipient: CardRecipient{Kind: KindProgram, Field: guardRef},
			Value:     "tok_evil_999999",
		}}, time.Now())
		require.ErrorIs(t, err, ErrProgramOwned,
			"CommitCard must refuse recipient kind program: only CommitProgramCredential may write it")
	})

	t.Run("guarded_mutators_refuse_program_owned_reference", func(t *testing.T) {
		// Oracle: FR-048 — every guarded method refuses a program-owned name
		// with ErrProgramOwned and leaves the file unchanged. Setup needs a
		// program-owned entry, which only CommitProgramCredential may create
		// (I-3): the RED stub is a no-op, so this scenario is BLOCKED until
		// GREEN implements it — that failure is the missing feature.
		store, path, key := newUnlockedGuardStore(t)
		receipt := CardReceipt{
			RequestID: guardReqID, SessionID: "sess-1", References: []string{guardRef},
			SubmittedAt: time.Now(),
		}
		gen, err := store.CommitProgramCredential(guardRef, "tok_valid_12345", &receipt)
		if gen == 0 && err == nil {
			_, found := store.ProgramEntryGeneration(guardRef)
			if !found {
				v, getErr := store.Get(guardRef)
				if getErr != nil || v == "" {
					t.Fatalf("BLOCKED: CommitProgramCredential not implemented — required by FR-048/S-127 setup (spec: program-owned entries are created only by the program-scoped methods)")
				}
			}
		}
		require.NoError(t, err, "setup: publish program credential")

		before := snapshotStore(t, store)

		// Every guarded mutator called on the program-owned reference R.
		err = store.Set(guardRef, "tok_evil_999999")
		require.ErrorIs(t, err, ErrProgramOwned, "Set on program-owned reference")
		err = store.Delete(guardRef)
		require.ErrorIs(t, err, ErrProgramOwned, "Delete on program-owned reference")
		_, err = store.RemoveReference(guardRef)
		require.ErrorIs(t, err, ErrProgramOwned, "RemoveReference on program-owned reference")
		// The two private-only methods already refuse non-private secrets;
		// the spec accepts their existing ErrNotPrivateSecret or the new
		// ErrProgramOwned — either is fail-closed (FR-048).
		_, err = store.SelectPrivateVersion(guardRef)
		requireProgramOrNotPrivate(t, err, "SelectPrivateVersion")
		_, err = store.SetPrivateChannelAvailability(guardRef, true)
		requireProgramOrNotPrivate(t, err, "SetPrivateChannelAvailability")

		after := snapshotStore(t, store)
		requireSnapshotsEqual(t, before, after)

		// The store file must be untouched: reopening gives the same state.
		reopened := reopenUnlockedGuardStore(t, path, key)
		requireSnapshotsEqual(t, before, snapshotStore(t, reopened))
	})

	t.Run("rotate_reencrypts_without_changing_program_value", func(t *testing.T) {
		// Oracle: FR-048 exceptions — Rotate/RotateWithPassphrase may
		// re-encrypt without changing values; the program entry's decrypted
		// value and generation metadata survive.
		store, _, key := newUnlockedGuardStore(t)
		publishForGuardSetup(t, store, guardRef, "tok_valid_12345")
		newKey := make([]byte, testKeyLen)
		for i := range newKey {
			newKey[i] = byte(0x61 + i)
		}
		require.NoError(t, store.Rotate(newKey), "Rotate with a new key")
		v, err := store.Get(guardRef)
		require.NoError(t, err)
		require.Equal(t, "tok_valid_12345", v, "Rotate must not change the program value")
		gen, ok := store.ProgramEntryGeneration(guardRef)
		require.True(t, ok, "program generation metadata must survive rotation")
		require.Positive(t, gen, "generation metadata must be set")
		_ = key
	})

	t.Run("receipt_pruning_leaves_program_value_unchanged", func(t *testing.T) {
		// Oracle: FR-048 exceptions — receipt pruning may remove receipts
		// only; the program credential and metadata stay.
		store, _, _ := newUnlockedGuardStore(t)
		publishForGuardSetup(t, store, guardRef, "tok_valid_12345")
		require.NoError(t, store.PruneCardReceipts([]string{guardReqID}),
			"receipt pruning is allowed (receipt-only class)")
		v, err := store.Get(guardRef)
		require.NoError(t, err)
		require.Equal(t, "tok_valid_12345", v, "receipt pruning must not touch values")
		_, ok := store.ProgramEntryGeneration(guardRef)
		require.True(t, ok, "program generation metadata must survive receipt pruning")
	})

	t.Run("program_scoped_methods_called_only_from_pkg_programs", func(t *testing.T) {
		// Oracle: FR-048 — "The program-scoped methods MUST be called only
		// from pkg/programs". The rule guards the SHIPPED call graph: the
		// sweep covers pkg/ and cmd/ and the _test.go exclusion is part of
		// the guard's own definition — test fixtures in any package drive
		// the real methods by design (the gateway sweep fixture, the
		// sysagent-tools S129 fixture) and ship nowhere. (CHECK F09; chief
		// rerun C1 proved the unscoped guard flagged its own fixtures.)
		root := repoRootFromCaller()
		var offenders []string
		walkGoFiles(t, root, func(path, src string) {
			rel, relErr := filepath.Rel(root, path)
			require.NoError(t, relErr, "a walked file must lie under the repository root: %s", path)
			if !strings.HasPrefix(rel, "pkg/") && !strings.HasPrefix(rel, "cmd/") {
				return
			}
			if callerGuardOffender(rel, src) {
				offenders = append(offenders, rel)
			}
		})
		require.Empty(t, offenders,
			"program-scoped store methods must be called only from pkg/programs (FR-048, production files): %v", offenders)

		// Instrument proof (F09): the checker fires on a planted production
		// caller outside pkg/programs and spares the identical _test.go
		// fixture caller and the sanctioned pkg/programs caller itself.
		const plantedSrc = `func f(s *credentials.Store) { s.CommitProgramCredential("R", "v", nil) }`
		require.True(t, callerGuardOffender("pkg/gateway/rest_settings.go", plantedSrc),
			"instrument: a production caller outside pkg/programs is flagged")
		require.False(t, callerGuardOffender("pkg/gateway/rest_settings_test.go", plantedSrc),
			"instrument: the identical _test.go fixture caller is exempt")
		require.False(t, callerGuardOffender("pkg/programs/service.go", plantedSrc),
			"instrument: pkg/programs is the sanctioned caller")
	})

	t.Run("every_exported_mutator_writes_only_inside_transact", func(t *testing.T) {
		// Oracle: FR-049 / S-127(d) — every exported mutating method changes
		// the file only inside s.transact; no direct or helper-reached
		// saveFileNoLock outside it (CHECK F11: writes reachable through
		// same-package helpers are part of the rule).
		dir := filepath.Join(repoRootFromCaller(), "pkg", "credentials")

		// Instrument proof first (F11, R08): planted controls — the checker
		// fires on a direct outside write and on a helper-reached outside
		// write; it stays silent on the transact-wrapped write AND on the
		// sanctioned helper-in-transact call, but flags the same helper when
		// it is also reached outside the literal. A verdict from a checker
		// that cannot fire proves nothing; one that flags sanctioned helpers
		// rejects correct code.
		planted := t.TempDir()
		writePlantedSource(t, planted, "planted_direct.go", plantedDirectWrite)
		writePlantedSource(t, planted, "planted_helper.go", plantedHelperWrite)
		writePlantedSource(t, planted, "planted_clean.go", plantedCleanWrite)
		writePlantedSource(t, planted, "planted_clean_helper.go", plantedCleanHelperWrite)
		writePlantedSource(t, planted, "planted_mixed.go", plantedMixedCallWrite)
		plantedOffenders := transactWriteOffenders(t, planted)
		require.Contains(t, plantedOffenders, "planted_direct.go::PlantedDirectWrite writes outside s.transact",
			"instrument: a direct outside write is flagged: %v", plantedOffenders)
		require.Contains(t, plantedOffenders, "planted_helper.go::PlantedIndirectWrite writes outside s.transact",
			"instrument: a helper-reached outside write is flagged: %v", plantedOffenders)
		require.Contains(t, plantedOffenders, "planted_mixed.go::PlantedMixedCallWrite writes outside s.transact",
			"instrument: a helper also reached OUTSIDE the literal keeps unsanctioned reachability: %v", plantedOffenders)
		for _, o := range plantedOffenders {
			require.NotContains(t, o, "planted_clean.go",
				"instrument: the transact-wrapped write is not flagged: %v", plantedOffenders)
			require.NotContains(t, o, "planted_clean_helper.go",
				"instrument: the sanctioned helper-in-transact call is not flagged (CHECK R08): %v", plantedOffenders)
		}

		// The verdict on the real package (at RED: every mutator writes
		// outside s.transact — FR-049's transaction machinery does not exist).
		offenders := transactWriteOffenders(t, dir)
		require.Empty(t, offenders,
			"exported Store methods must write the store file only inside s.transact (FR-049): %v", offenders)
	})
}

// This test-only type promotes the real Store method set and adds one exported
// mutator. The SAME classifier must flag it without changing the table.
type addedStoreMutator struct{ *Store }

func (*addedStoreMutator) PlantedUnclassifiedMutation() {}

func unclassifiedStoreMethods(typ reflect.Type) []string {
	var missing []string
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if guardMethodClass[name] == "" {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func TestCredentialStore_AddedExportedMutatorInstrument(t *testing.T) {
	require.Empty(t, unclassifiedStoreMethods(reflect.TypeFor[*Store]()), "positive: current classified set")
	require.Equal(t, []string{"PlantedUnclassifiedMutation"}, unclassifiedStoreMethods(reflect.TypeFor[*addedStoreMutator]()), "negative: added exported mutator cannot disappear")
}

// --- helpers ----------------------------------------------------------------

func requireProgramOrNotPrivate(t *testing.T, err error, method string) {
	t.Helper()
	require.Error(t, err, "%s on a program-owned reference must fail closed", method)
	if !errors.Is(err, ErrProgramOwned) && !errors.Is(err, ErrNotPrivateSecret) {
		t.Fatalf("%s returned %v; want ErrProgramOwned or the existing ErrNotPrivateSecret", method, err)
	}
}

// publishForGuardSetup creates the program-owned entry the guarded-refusal
// scenarios need. Only CommitProgramCredential may do this (I-3).
func publishForGuardSetup(t *testing.T, store *Store, ref, value string) {
	t.Helper()
	receipt := CardReceipt{
		RequestID: guardReqID, SessionID: "sess-guard", References: []string{ref},
		SubmittedAt: time.Now(),
	}
	gen, err := store.CommitProgramCredential(ref, value, &receipt)
	if err == nil && gen == 0 {
		if _, ok := store.ProgramEntryGeneration(ref); !ok {
			t.Fatalf("BLOCKED: CommitProgramCredential not implemented — required by FR-048/S-127 setup")
		}
	}
	require.NoError(t, err, "setup: publish program credential %s", ref)
}

func repoRootFromCaller() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed - cannot resolve repo root")
	}
	// file = <repo>/pkg/credentials/program_owned_guard_test.go → three up.
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func walkGoFiles(t *testing.T, root string, fn func(path, src string)) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" || base == "dist" || base == "spa" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn(path, string(b))
		return nil
	}))
}

// callerGuardOffender reports whether one file calls the program-scoped store
// methods from PRODUCTION code outside pkg/programs (FR-048). The _test.go
// exclusion is part of the guard's definition: test fixtures in any package
// drive the real methods by design and ship nowhere; the rule guards the
// shipped call graph.
func callerGuardOffender(relPath, src string) bool {
	if strings.HasSuffix(relPath, "_test.go") {
		return false
	}
	if strings.HasPrefix(relPath, "pkg/programs/") {
		return false
	}
	// The leading dot distinguishes a receiver CALL from the method
	// definition (`func (s *Store) CommitProgramCredential(`).
	for _, m := range []string{".CommitProgramCredential(", ".RemoveProgramCredential("} {
		if strings.Contains(src, m) {
			return true
		}
	}
	return false
}

// Planted sources for the transact analyzer's instrument proof (CHECK F11).
const (
	plantedDirectWrite = `package credentials

func (s *Store) PlantedDirectWrite() error {
	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}
	return s.saveFileNoLock(sf)
}
`
	plantedHelperWrite = `package credentials

func (s *Store) writeThroughPlanted(sf *storeFile) error {
	return s.saveFileNoLock(sf)
}

func (s *Store) PlantedIndirectWrite() error {
	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}
	return s.writeThroughPlanted(sf)
}
`
	plantedCleanWrite = `package credentials

func (s *Store) PlantedCleanWrite() error {
	return s.transact(func(sf *storeFile) error {
		return s.saveFileNoLock(sf)
	})
}
`
	// plantedCleanHelperWrite is the CHECK R08 positive control: the helper
	// saves outside any transact of its own, but its only caller reaches it
	// INSIDE a s.transact literal — the sanctioned write path. The scanner
	// must keep the call-site context on the helper edge and not flag the
	// exported caller.
	plantedCleanHelperWrite = `package credentials

func (s *Store) cleanWriteThroughPlanted(sf *storeFile) error {
	return s.saveFileNoLock(sf)
}

func (s *Store) PlantedCleanHelperWrite() error {
	return s.transact(func(sf *storeFile) error {
		return s.cleanWriteThroughPlanted(sf)
	})
}
`
	// plantedMixedCallWrite is the CHECK R08 boundary twin: the same helper
	// reached BOTH outside and inside the literal — the outside call keeps
	// unsanctioned reachability, so the caller is flagged.
	plantedMixedCallWrite = `package credentials

func (s *Store) mixedPlantedHelper(sf *storeFile) error {
	return s.saveFileNoLock(sf)
}

func (s *Store) PlantedMixedCallWrite() error {
	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}
	_ = s.mixedPlantedHelper(sf)
	return s.transact(func(x *storeFile) error {
		return s.mixedPlantedHelper(x)
	})
}
`
)

func writePlantedSource(t *testing.T, dir, name, src string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600), "write planted source %s", name)
}

// callSite is one reached callee with its transaction context: a call inside
// a s.transact function literal is the sanctioned write path — the helper's
// save runs inside the caller's transaction (CHECK R08: the earlier scanner
// kept no context on helper edges, so a sanctioned helper-in-transact call
// tainted its exported caller through the global fixpoint).
type callSite struct {
	name       string
	sanctioned bool
}

// transactWriteOffenders parses every non-test .go file of one package
// directory and returns offender descriptions for every EXPORTED Store method
// that can reach a saveFileNoLock call outside a s.transact function literal —
// directly or through any chain of same-package helpers reached OUTSIDE a
// sanctioned literal (CHECK F11 reachability; CHECK R08 context). A call to
// s.transact never taints its caller, and a dirty helper reached only inside
// its literal is sanctioned: the save happens inside the caller's
// transaction. Name-based call matching is the documented heuristic, same as
// the caller guard's dot-call match.
func transactWriteOffenders(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	type fnInfo struct {
		file     string
		exported bool
		isMethod bool // receiver *Store
		direct   bool // unsanctioned saveFileNoLock in own body
		calls    []callSite
	}
	fns := map[string]*fnInfo{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, perr, "parse %s", name)
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name == nil || fd.Body == nil {
				continue
			}
			info := &fnInfo{file: name, exported: fd.Name.IsExported(), isMethod: storePtrReceiver(fd)}
			info.direct, info.calls = scanFnBody(fd.Body)
			fns[fd.Name.Name] = info
		}
	}
	dirty := map[string]bool{}
	for {
		changed := false
		for name, info := range fns {
			if dirty[name] {
				continue
			}
			d := info.direct
			for _, c := range info.calls {
				if !c.sanctioned && dirty[c.name] {
					d = true
					break
				}
			}
			if d {
				dirty[name] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	var offenders []string
	for name, info := range fns {
		if dirty[name] && info.isMethod && info.exported {
			offenders = append(offenders, fmt.Sprintf("%s::%s writes outside s.transact", info.file, name))
		}
	}
	sort.Strings(offenders)
	return offenders
}

// scanFnBody reports whether the body calls saveFileNoLock outside a
// s.transact function literal (the sanctioned write path), and collects every
// reached callee WITH its call-site context for the reachability fixpoint.
// Calls into s.transact are excluded — its literal is where writes are
// allowed.
func scanFnBody(body ast.Node) (directWrite bool, calls []callSite) {
	var litRanges []token.Pos // [start, end] pairs flattened
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "transact" {
			for _, arg := range call.Args {
				if lit, ok := arg.(*ast.FuncLit); ok {
					litRanges = append(litRanges, lit.Pos(), lit.End())
				}
			}
		}
		return true
	})
	insideSanctioned := func(pos token.Pos) bool {
		for i := 0; i+1 < len(litRanges); i += 2 {
			if pos >= litRanges[i] && pos <= litRanges[i+1] {
				return true
			}
		}
		return false
	}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			switch sel.Sel.Name {
			case "transact":
				return true // sanctioned write path
			case "saveFileNoLock":
				if !insideSanctioned(call.Pos()) {
					directWrite = true
				}
				return true
			default:
				calls = append(calls, callSite{name: sel.Sel.Name, sanctioned: insideSanctioned(call.Pos())})
			}
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok {
			calls = append(calls, callSite{name: id.Name, sanctioned: insideSanctioned(call.Pos())})
		}
		return true
	})
	return directWrite, calls
}

func storePtrReceiver(fd *ast.FuncDecl) bool {
	if fd.Recv == nil || len(fd.Recv.List) != 1 {
		return false
	}
	star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "Store"
}
