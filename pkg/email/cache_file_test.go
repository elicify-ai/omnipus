package email

// RED pack — W2 encrypted folder-metadata file (oracle: spec only, never the
// implementation).
//
// Every expected value derives from the specification, written down before any
// implementation existed (none compiles yet — the expected RED state for a
// Wave-C RED pack):
//
//   - w2 spec §3.6 (payload fields, 64 KiB budget, four refresh triggers),
//     §3.7 (envelope rules R-3.7-1..8), §3.8 (location/permissions/exclusion),
//     §3.10 (publication revision at Save), §3.11 (invalidation rows),
//     §5 US-5/US-6/US-7, §6 F-1..F-11/G-1..G-3, §7.1 test rows, §7.2 DT-2,
//     §11 CX-4/CX-6/CX-7/CX-8/CX-14.
//   - ADR-20261001 "What must be built" (AES-256-GCM, fresh nonce,
//     domain-separated keys, AAD = purpose+schema+pair+generation+transport,
//     atomic ciphertext-only replacement, reject before allocation, never
//     salvage) and the filesystem table (0700/0600, no plaintext temp,
//     tracked files are not untracked by ignore rules).
//
// The frozen §4.1 surfaces under test: FolderSnapshotStore.Load(Scope)
// (Snapshot, Found, error), .Save(Scope, Snapshot, capturedRevision,
// derivedKey) error, .Delete(Scope) error. Constructors are the proposed
// construction point (the freeze binds shapes and semantics): keys are
// SUPPLIED per the ADR's W2 row ("encrypted folder envelopes with supplied
// derived keys"), the current-revision source is the store's consumption of
// register row 10 (W1 publishes RevisionSource; W2 compares the captured
// value), and the purpose string is R-3.7-3's namespaced derivation input.
// writeCacheFileFn is the package-local write indirection grill M-5 ordered
// (the pkg/credentials writeFileAtomicFn precedent) so the crash-consistency
// test can park a write without production test hooks.
//
// ROUND-2 IMP-1 CORRECTION (2026-10-03, qa-lead). The self-evaluating gate
// was withdrawn (register row 18's settlement, round-2 IMP-1: ONE evaluator
// — the publisher's, w5-integration's EvaluateStagingExclusion; ONE enforcer
// — the write path, which consumes the published GateDecision and never
// re-evaluates; w2 spec §3.8 E-1 cell and §4.1 Save row). That changed the
// ARRANGEMENT of this pack, not its subjects: a store built without a
// published decision refuses every write (fail closed on absence, §3.8 gate
// paragraph), so a store exercising an envelope property is built WITH the
// published ALLOWED decision (§4.2 gate-decision row; §7.1 gate rows:
// allowed=true → the write proceeds). Every assertion below is unchanged in
// kind and strength; the ABSENT-decision and NOT-ALLOWED-decision refusal
// rows are the gate pack's subjects (mail_cache_gate_test.go), per §7.1's
// three-injection split.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/fileutil/fileutiltest"
)

// Folder cache envelope purpose strings — R-3.7-3 names the folder-metadata
// purpose and explicitly reserves a distinct one for Phase 2 headers.
const (
	testPurposeFolder  = "omnipus-mail-folder-cache-v1"
	testPurposeHeaders = "omnipus-mail-header-cache-v1"
)

func testKey(fill byte) func(Scope) ([]byte, error) {
	return func(Scope) ([]byte, error) {
		return bytes.Repeat([]byte{fill}, 32), nil // 32 bytes: DeriveSubkey's output size (§3.7)
	}
}

func revisionAlways(n uint64) func(Scope) Revision {
	return func(Scope) Revision { return Revision(n) }
}

// newTestStore builds a store on a real temporary directory (§3.8: the cache
// lives under the resolved data root; the temp dir stands in for it — the
// path shape base/mail-cache/<pair>/folders.enc is asserted in
// TestCacheFile_RoundTripPreservesAllFields).
//
// The store carries the PUBLISHED, ALLOWED gate decision (round-2 IMP-1;
// w2 spec §4.2 gate-decision row, §7.1 gate rows): under the settled design
// the write path consumes the decision the publisher published and never
// evaluates the exclusion itself, and a decision-less store refuses every
// write (fail closed on absence, §3.8 gate paragraph). An envelope property
// is a property of a cache-writing store — one whose publisher has published
// allowed=true — so that is the arrangement under test here; the absent and
// not-allowed refusal rows are the gate pack's (mail_cache_gate_test.go).
func newTestStore(t *testing.T, base string, keys func(Scope) ([]byte, error), purpose string, rev uint64) *FolderSnapshotStore {
	t.Helper()
	return NewFolderSnapshotStore(base, keys, purpose, revisionAlways(rev),
		WithGateDecision(GateDecision{Allowed: true})) // §4.2: allowed=true → the write path proceeds (§7.1)
}

func testSnapshot() Snapshot {
	uv100 := uint32(100)
	uv55 := uint32(55)
	stamp := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	return Snapshot{
		SchemaVersion:    1,
		PairIdentity:     "pair-1",
		ConfigGeneration: "gen-1",
		Transport:        "imap", // §3.6 payload table: "imap" in Phase 1
		Roles: SnapshotRoles{
			Sent: SnapshotRole{
				Name:         "Sent Items",
				Source:       "saved", // register row 3: one of the five values
				UIDValidity:  &uv100,
				Availability: "present",
			},
			Drafts: SnapshotRole{
				Name:         "",
				Source:       "none",
				UIDValidity:  nil, // CX-4: an unvalidated epoch stays nil, never 0
				Availability: "unknown",
				Ambiguity:    nil,
			},
			Inbox: SnapshotRole{
				Name:         "INBOX",
				Source:       "override", // five-value enum exercised in the payload
				UIDValidity:  &uv55,
				Availability: "present",
			},
		},
		LastValidatedAt: stamp,
		SavedAt:         stamp,
	}
}

func snapshotPath(base, pair string) string {
	return filepath.Join(base, "mail-cache", pair, "folders.enc") // §3.8 path shape
}

// TestCacheFile_RoundTripPreservesAllFields — F-1, US-5.1, CX-4.
// Oracle: Save→Load preserves roles, sources, UIDVALIDITY (incl. nil = never
// validated — never fabricated as 0), schema/generation, transport and the
// timestamps; the file lands at the §3.8 path under the opaque pair ID; and
// the folder names never touch the disk in the clear (§3.6: the plaintext
// payload is "never on disk in this form").
func TestCacheFile_RoundTripPreservesAllFields(t *testing.T) {
	base := t.TempDir()
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	snap := testSnapshot()

	if err := store.Save(scope, snap, Revision(1), []byte(bytes.Repeat([]byte{0xAA}, 32))); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(snapshotPath(base, "pair-1"))
	if err != nil {
		t.Fatalf("the snapshot must exist at <base>/mail-cache/<opaque-pair-id>/folders.enc (§3.8): %v", err)
	}
	if bytes.Contains(raw, []byte("Sent Items")) {
		t.Fatal("the resolved folder name appears in the file in plaintext — the payload is sealed (§3.6/§3.7-1)")
	}

	got, found, err := store.Load(scope)
	if err != nil || !found {
		t.Fatalf("Load: found=%v err=%v, want found with no error", found, err)
	}
	if !reflect.DeepEqual(got, snap) {
		t.Fatalf("round-trip changed the snapshot:\n got: %+v\nwant: %+v", got, snap)
	}
	if got.Roles.Drafts.UIDValidity != nil {
		t.Fatalf("nil UIDVALIDITY must stay nil through the envelope (CX-4: never a fabricated 0), got %d", *got.Roles.Drafts.UIDValidity)
	}
}

// TestCacheFile_WrongDerivedKeyRefuses — ADR test-strategy "Ciphertext and
// permissions" row ("wrong derived key"); the dispatch brief's first envelope
// obligation: the file cannot be read without THE derived key.
// Oracle: AES-256-GCM under a different key fails authentication — the read
// refuses with the corrupt class and yields NO plaintext and NO fabricated
// folder list (R-3.7-1: never partial or unauthenticated plaintext).
func TestCacheFile_WrongDerivedKeyRefuses(t *testing.T) {
	base := t.TempDir()
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	writer := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	if err := writer.Save(scope, testSnapshot(), Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reader := newTestStore(t, base, testKey(0xBB), testPurposeFolder, 1) // same install shape, different key
	got, found, err := reader.Load(scope)
	if found {
		t.Fatal("a file sealed under another key must not be served")
	}
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("wrong-key read must refuse with the cache-corrupt class (R-3.7-1/R-3.7-7), got %v", err)
	}
	if !reflect.DeepEqual(got, Snapshot{}) {
		t.Fatalf("a refused read must yield no plaintext at all, got %+v", got)
	}
	if got.Roles.Sent.Name != "" {
		t.Fatalf("a refused read must not fabricate a folder list, got name %q", got.Roles.Sent.Name)
	}
}

// TestCacheFile_ForeignPairRefuses — F-7, CX-7.
// Oracle: pair B's file copied onto pair A's path fails the AAD comparison
// (R-3.7-4: the AAD binds the pair identity; the reader compares the
// authenticated identity against its independently resolved scope) — before
// any plaintext exists.
func TestCacheFile_ForeignPairRefuses(t *testing.T) {
	base := t.TempDir()
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	if err := store.Save(Scope{PairID: "pair-B", Generation: "gen-1"}, testSnapshot(), Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("Save (pair B): %v", err)
	}

	raw, err := os.ReadFile(snapshotPath(base, "pair-B"))
	if err != nil {
		t.Fatalf("read pair B's file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "mail-cache", "pair-A"), 0o700); err != nil {
		t.Fatalf("mkdir pair A: %v", err)
	}
	if err := os.WriteFile(snapshotPath(base, "pair-A"), raw, 0o600); err != nil {
		t.Fatalf("copy pair B's file onto pair A's path: %v", err)
	}

	got, found, err := store.Load(Scope{PairID: "pair-A", Generation: "gen-1"})
	if found {
		t.Fatal("a foreign pair's file must not be served as pair A's state (F-7)")
	}
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("cross-pair copy must refuse via the AAD comparison (CX-7), got %v", err)
	}
	if !reflect.DeepEqual(got, Snapshot{}) {
		t.Fatalf("a refused foreign file yields no plaintext, got %+v", got)
	}
}

// TestCacheFile_ForeignGenerationRefuses — F-7, CX-7, §3.11 (credential/host/
// override change row: "The old generation's file is unopenable anyway (AAD
// binding, R-3.7-4)").
// Oracle: a snapshot sealed under generation gen-1 must refuse to open when
// the requesting scope carries gen-2.
func TestCacheFile_ForeignGenerationRefuses(t *testing.T) {
	base := t.TempDir()
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	if err := store.Save(Scope{PairID: "pair-1", Generation: "gen-1"}, testSnapshot(), Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, found, err := store.Load(Scope{PairID: "pair-1", Generation: "gen-2"})
	if found {
		t.Fatal("a stale-generation file must not be served (V-4/R-3.7-4)")
	}
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("stale-generation read must refuse with the corrupt class, got %v", err)
	}
}

// TestCacheFile_ForeignPurposeRefuses — R-3.7-3/R-3.7-4.
// Oracle: a file sealed under the folder-metadata purpose must refuse to open
// through a store running the Phase-2 headers purpose — distinct purposes get
// cryptographically independent keys and distinct AAD purpose tags, so one
// compromised purpose does not cross into the other.
func TestCacheFile_ForeignPurposeRefuses(t *testing.T) {
	base := t.TempDir()
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	writer := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	if err := writer.Save(scope, testSnapshot(), Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	headers := newTestStore(t, base, testKey(0xAA), testPurposeHeaders, 1)
	_, found, err := headers.Load(scope)
	if found {
		t.Fatal("a folder-metadata file must not open under the headers purpose (R-3.7-3)")
	}
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("foreign-purpose read must refuse with the corrupt class, got %v", err)
	}
}

// TestCacheFile_BitFlipRefusesWithoutPlaintext — F-6, US-6.1, DT-2.
// Oracle: a single flipped bit anywhere (nonce, ciphertext head, ciphertext
// tail) fails authentication: nothing decrypts, the corrupt class surfaces,
// and the live path's data is untouched (zero plaintext returned).
func TestCacheFile_BitFlipRefusesWithoutPlaintext(t *testing.T) {
	base := t.TempDir()
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	if err := store.Save(scope, testSnapshot(), Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	good, err := os.ReadFile(snapshotPath(base, "pair-1"))
	if err != nil {
		t.Fatalf("read sealed file: %v", err)
	}
	if len(good) < 64 {
		t.Fatalf("sealed envelope implausibly small (%d bytes); the flip positions need a real envelope", len(good))
	}

	for _, pos := range []int{3, 40, len(good) - 1} { // DT-2: nonce area, ciphertext head, tail
		t.Run("flip-at-byte-"+strconv.Itoa(pos), func(t *testing.T) {
			mutated := append([]byte(nil), good...)
			mutated[pos] ^= 0x01
			if err := os.WriteFile(snapshotPath(base, "pair-1"), mutated, 0o600); err != nil {
				t.Fatalf("write mutated envelope: %v", err)
			}
			got, found, err := store.Load(scope)
			if found {
				t.Fatalf("bit flip at %d: the tampered envelope must not be served (US-6.1)", pos)
			}
			if !errors.Is(err, ErrCacheCorrupt) {
				t.Fatalf("bit flip at %d: want the cache-corrupt class (F-6), got %v", pos, err)
			}
			if !reflect.DeepEqual(got, Snapshot{}) {
				t.Fatalf("bit flip at %d: zero plaintext may survive authentication failure, got %+v", pos, got)
			}
		})
	}
}

// TestCacheFile_FreshNoncePerWrite — F-8, US-6.3, CX-6, R-3.7-2.
// Oracle: two writes of BYTE-IDENTICAL payloads must produce different
// ciphertexts on disk (fresh crypto/rand nonce per write). A deterministic or
// reused nonce passes every other test in this file while breaking the
// crypto — CX-6's silent killer.
func TestCacheFile_FreshNoncePerWrite(t *testing.T) {
	base := t.TempDir()
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	snap := testSnapshot() // constructed ONCE: byte-identical payloads

	if err := store.Save(scope, snap, Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	first, err := os.ReadFile(snapshotPath(base, "pair-1"))
	if err != nil {
		t.Fatalf("read first ciphertext: %v", err)
	}
	if err := store.Save(scope, snap, Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	second, err := os.ReadFile(snapshotPath(base, "pair-1"))
	if err != nil {
		t.Fatalf("read second ciphertext: %v", err)
	}

	if bytes.Equal(first, second) {
		t.Fatal("two writes of identical payloads produced identical ciphertext — the nonce is reused or deterministic (CX-6/R-3.7-2)")
	}
}

// TestCacheFile_RejectsBeforeAllocation — F-9, US-6.4, CX-8, DT-2.
// Oracle: malformed and oversized envelopes are rejected with the safe class
// BEFORE anything proportional to their claimed/actual size is materialized
// (R-3.7-6: "length caps are checked before reads that could materialize the
// bytes"). Layout-independent mutations only — the exact byte-layout probe
// (a hostile claimed-size header) is CHECK's mutation material, noted in the
// delivery report.
func TestCacheFile_RejectsBeforeAllocation(t *testing.T) {
	base := t.TempDir()
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	path := snapshotPath(base, "pair-1")

	// The malformed-file subtests below place an envelope directly at the
	// cache path — Save is what normally creates mail-cache/<pair>/, and these
	// subtests must not go through it (F-9 exercises the READ refusal path).
	// The fixture therefore creates the parent directory itself; without it
	// os.WriteFile dies on the missing directory before the store under test
	// is ever exercised.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}

	t.Run("empty file", func(t *testing.T) {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("write empty: %v", err)
		}
		assertRefused(t, store, scope)
	})
	t.Run("short garbage", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("not-an-envelope"), 0o600); err != nil {
			t.Fatalf("write garbage: %v", err)
		}
		assertRefused(t, store, scope)
	})
	t.Run("truncated envelope", func(t *testing.T) {
		if err := store.Save(scope, testSnapshot(), Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
			t.Fatalf("Save: %v", err)
		}
		good, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read sealed: %v", err)
		}
		if err := os.WriteFile(path, good[:len(good)/2], 0o600); err != nil {
			t.Fatalf("write truncated: %v", err)
		}
		assertRefused(t, store, scope)
	})
	t.Run("oversized on disk", func(t *testing.T) {
		big := make([]byte, 128<<10) // 128 KiB ≫ the 64 KiB payload budget (§3.6)
		if _, err := rand.Read(big); err != nil {
			t.Fatalf("rand: %v", err)
		}
		if err := os.WriteFile(path, big, 0o600); err != nil {
			t.Fatalf("write oversized: %v", err)
		}
		assertRefused(t, store, scope)
	})
	t.Run("unsupported schema version refuses at write", func(t *testing.T) {
		snap := testSnapshot()
		snap.SchemaVersion = 99 // an unknown version refuses the whole file (§3.6 payload table, R-3.7-6/7)
		if err := store.Save(scope, snap, Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err == nil {
			t.Fatal("Save must refuse an unsupported schema version, not seal it into an unreadable file")
		} else if !errors.Is(err, ErrCacheUnavailable) && !errors.Is(err, ErrCacheCorrupt) {
			t.Fatalf("unsupported version must refuse with a safe cache class, got %v", err)
		}
	})
	t.Run("payload over the 64 KiB budget is visible, never truncated", func(t *testing.T) {
		snap := testSnapshot()
		snap.Roles.Sent.Ambiguity = []string{strings.Repeat("A", 70<<10)} // pushes the payload past 64 KiB
		err := store.Save(scope, snap, Revision(1), bytes.Repeat([]byte{0xAA}, 32))
		if err == nil {
			t.Fatal("a payload past the 64 KiB budget must be refused (§3.6: a visible cache-unavailable outcome, never truncation)")
		}
		if !errors.Is(err, ErrCacheUnavailable) {
			t.Fatalf("over-budget Save must surface the cache-unavailable class, got %v", err)
		}
		if _, statErr := os.Stat(path); statErr == nil {
			t.Fatal("no file may exist for a refused over-budget payload")
		}
	})
}

func assertRefused(t *testing.T, store *FolderSnapshotStore, scope Scope) {
	t.Helper()
	got, found, err := store.Load(scope)
	if found {
		t.Fatal("a malformed envelope must not be served (R-3.7-7: never salvage)")
	}
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("malformed envelope must refuse with the cache-corrupt class (F-9), got %v", err)
	}
	if !reflect.DeepEqual(got, Snapshot{}) {
		t.Fatalf("refused envelope yielded data (%+v) — unauthenticated bytes must never render as folder state", got)
	}
}

// TestCacheFile_LockedStoreIsLiveOnlyNoMint — F-10, US-6.5, R-3.7-8.
// Oracle: a locked key source yields cache-unavailable and live-only: NO key
// is minted, NO plaintext fallback is written, and not a single file appears
// in the cache directory.
func TestCacheFile_LockedStoreIsLiveOnlyNoMint(t *testing.T) {
	base := t.TempDir()
	locked := func(Scope) ([]byte, error) { return nil, credentials.ErrStoreLocked }
	store := newTestStore(t, base, locked, testPurposeFolder, 1)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}

	if err := store.Save(scope, testSnapshot(), Revision(1), nil); !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("Save under a locked store must report cache-unavailable (R-3.7-8), got %v", err)
	}
	if _, found, err := store.Load(scope); found || !errors.Is(err, ErrCacheUnavailable) {
		t.Fatalf("Load under a locked store must report cache-unavailable, got found=%v err=%v", found, err)
	}

	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			t.Errorf("locked store wrote %s — no key minted, no plaintext fallback may exist (F-10)", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk cache dir: %v", err)
	}
}

// TestCacheFile_AtomicReplaceCrashConsistency — F-11, US-6.6, DT-2 crash
// points, grill M-5's mandated write indirection.
// Oracle: the atomic replace routes through the package-local write seam; a
// write parked mid-flight leaves either the old complete file or nothing —
// never a torn or empty one (the false-green-patterns #6 lesson: a reader
// outside the lock must never observe an incomplete file).
func TestCacheFile_AtomicReplaceCrashConsistency(t *testing.T) {
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	key := bytes.Repeat([]byte{0xAA}, 32)

	t.Run("first write parked means no file at all", func(t *testing.T) {
		base := t.TempDir()
		store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
		parked, release := fileutiltest.HoldFirstWrite(t, &writeCacheFileFn, "folders.enc")

		done := make(chan error, 1)
		go func() { done <- store.Save(scope, testSnapshot(), Revision(1), key) }()
		fileutiltest.WaitParked(t, parked)

		if _, statErr := os.Stat(snapshotPath(base, "pair-1")); statErr == nil {
			t.Fatal("folders.enc exists while its first write is still in flight — a crash now would leave a bogus file (F-11)")
		}
		if _, found, err := store.Load(scope); found || err != nil {
			t.Fatalf("mid-first-write Load must be a clean miss, got found=%v err=%v", found, err)
		}
		release()
		fileutiltest.WaitDone(t, done)
	})

	t.Run("second write parked leaves the old snapshot complete", func(t *testing.T) {
		base := t.TempDir()
		store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
		snap1 := testSnapshot()
		if err := store.Save(scope, snap1, Revision(1), key); err != nil {
			t.Fatalf("Save v1: %v", err)
		}
		v1, err := os.ReadFile(snapshotPath(base, "pair-1"))
		if err != nil {
			t.Fatalf("read v1: %v", err)
		}

		parked, release := fileutiltest.HoldFirstWrite(t, &writeCacheFileFn, "folders.enc")
		snap2 := snap1
		snap2.Roles.Sent.Name = "INBOX.Sent"
		done := make(chan error, 1)
		go func() { done <- store.Save(scope, snap2, Revision(1), key) }()
		fileutiltest.WaitParked(t, parked)

		got, found, err := store.Load(scope)
		if !found || err != nil {
			t.Fatalf("mid-replace Load must serve the old complete snapshot, got found=%v err=%v", found, err)
		}
		if !reflect.DeepEqual(got, snap1) {
			t.Fatalf("mid-replace Load must serve v1 intact, got %+v", got)
		}
		onDisk, err := os.ReadFile(snapshotPath(base, "pair-1"))
		if err != nil || !bytes.Equal(onDisk, v1) {
			t.Fatalf("the file on disk must still be the complete old ciphertext mid-replace, err=%v equal=%v", err, bytes.Equal(onDisk, v1))
		}
		release()
		fileutiltest.WaitDone(t, done)

		got2, found2, err2 := store.Load(scope)
		if !found2 || err2 != nil || !reflect.DeepEqual(got2, snap2) {
			t.Fatalf("after the write lands, v2 must load: found=%v err=%v got=%+v", found2, err2, got2)
		}
	})
}

// TestCacheFile_PermissionsUnix — §3.8 permissions, F-1's "(0600 file in a
// 0700 directory)".
// Oracle: the store creates mail-cache/ at 0700 and folders.enc at 0600.
// Unix-scoped per §7.1 ("Unix; the Windows restrictive-access evidence comes
// from the register row-23 instrument") — the skip names that instrument and
// is a platform scope, not a green-manufacturing skip.
func TestCacheFile_PermissionsUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-scoped by w2 §7.1; Windows restrictive-access evidence is the register row-23 instrument (w5-integration's Windows-leg workflow extension), never a chmod-only claim")
	}
	base := t.TempDir()
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := store.Save(scope, testSnapshot(), Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	for _, tc := range []struct {
		path string
		want fs.FileMode
	}{
		{filepath.Join(base, "mail-cache"), 0o700},
		{filepath.Join(base, "mail-cache", "pair-1"), 0o700},
		{snapshotPath(base, "pair-1"), 0o600},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.path, err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Errorf("%s permissions = %v, want %v (§3.8: 0700 dir / 0600 file on Unix)", tc.path, got, tc.want)
		}
	}
}

// TestCacheFile_SaveRefusesUnderStaleRevision — V-2's disk half, R-3.10-3,
// FR-W2-16, CX-9.
// Oracle: Save takes the revision CAPTURED at read start (register row 10);
// when it is no longer current at write time the snapshot publishes nothing —
// the previous complete snapshot stands, timestamps included.
func TestCacheFile_SaveRefusesUnderStaleRevision(t *testing.T) {
	base := t.TempDir()
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1) // current revision: 1
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	snap1 := testSnapshot()
	if err := store.Save(scope, snap1, Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("Save under current revision: %v", err)
	}

	current := revisionAlways(2) // the mutation + its refresh advanced the revision
	// Same settled arrangement as newTestStore: the stale-revision refusal is
	// a property of a cache-writing store, so this store also carries the
	// published allowed decision (§4.2; the revision check precedes the gate
	// in Save's refusal order either way).
	stale := NewFolderSnapshotStore(base, testKey(0xAA), testPurposeFolder, current,
		WithGateDecision(GateDecision{Allowed: true}))
	snap2 := snap1
	snap2.Roles.Sent.Name = "INBOX.Sent"
	if err := stale.Save(scope, snap2, Revision(1), bytes.Repeat([]byte{0xAA}, 32)); !errors.Is(err, ErrStalePublication) {
		t.Fatalf("a superseded write must refuse with the stale-publication class (CX-9/R-3.10-3), got %v", err)
	}

	got, found, err := store.Load(scope)
	if !found || err != nil {
		t.Fatalf("the previous snapshot must stand, got found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(got, snap1) {
		t.Fatalf("the superseded write must not have landed, got %+v want v1 %+v", got, snap1)
	}
}

// TestCacheFile_DeleteRemovesSnapshot — E-4's W2-supplied delete primitive
// (§3.8 rule E-4: "W2 supplies the delete primitive"), §3.11 removal row.
// Oracle: Delete removes the pair's file; a later Load is a clean miss (no
// error); deleting a never-written pair is orphan-tolerant (no error) — the
// handler's existing best-effort pattern (§3.8 E-4).
func TestCacheFile_DeleteRemovesSnapshot(t *testing.T) {
	base := t.TempDir()
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	if err := store.Save(scope, testSnapshot(), Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.Delete(scope); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, statErr := os.Stat(snapshotPath(base, "pair-1")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("mailbox removal must leave no orphan cache file (E-4), stat=%v", statErr)
	}
	if _, found, err := store.Load(scope); found || err != nil {
		t.Fatalf("Load after Delete must be a clean miss (found=false, no error), got found=%v err=%v", found, err)
	}
	if err := store.Delete(Scope{PairID: "never-written", Generation: "gen-1"}); err != nil {
		t.Fatalf("Delete of a never-written pair must be orphan-tolerant (E-4 best-effort), got %v", err)
	}
}

// envelopeTransportOffset is the byte offset of the transport marker inside
// the sealed-envelope framing (magic | envelopeVersion | schema | transport
// length | transport | nonce | ciphertext). It anchors the transport-tamper
// fixture; a precondition assertion fatals loudly if the framing drifts
// instead of letting the test pass by tampering the wrong bytes.
const envelopeTransportOffset = len(envelopeMagic) + 1 + 4 + 1

// TestCacheFile_ForeignPairAADRefusesAtAuthentication — R-3.7-4, CX-7's layer
// claim; closes the CHECK mutation-audit hole F-2 (survivor M5: the AAD's
// pair/generation/transport bindings enforced by no test — refusals came only
// from the post-decrypt payload comparison).
// Oracle: the AAD binds the pair identity, so an envelope SEALED under a
// foreign pair's identity must fail AUTHENTICATION even when its decrypted
// payload would have satisfied the payload comparison — the payload below
// carries the READER's own pair identity. A refusal is then possible only at
// the authentication layer; a mutant dropping the pair binding opens this
// envelope and dies on the found=false assertion.
func TestCacheFile_ForeignPairAADRefusesAtAuthentication(t *testing.T) {
	base := t.TempDir()
	key := bytes.Repeat([]byte{0xAA}, 32)
	readerScope := Scope{PairID: "pair-A", Generation: "gen-1"}

	// Payload identity fields AGREE with the reading scope: with the pair
	// binding dropped from the AAD this envelope would open cleanly.
	snap := testSnapshot()
	snap.PairIdentity = readerScope.PairID
	snap.ConfigGeneration = readerScope.Generation
	payload, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	// Seal under a FOREIGN pair identity: the AAD binds pair-B; the envelope
	// header and payload stay self-consistent for pair-A.
	envelope, err := sealSnapshot(payload, testPurposeFolder, snap, Scope{PairID: "pair-B", Generation: readerScope.Generation}, key)
	if err != nil {
		t.Fatalf("seal under foreign pair identity: %v", err)
	}

	path := snapshotPath(base, readerScope.PairID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(path, envelope, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	got, found, err := store.Load(readerScope)
	if found {
		t.Fatalf("an envelope whose AAD binds a foreign pair must not be served (R-3.7-4: it must fail AUTHENTICATION, not fall through to the payload comparison), got found=true with sent name %q", got.Roles.Sent.Name)
	}
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("foreign-pair AAD mismatch must refuse with the cache-corrupt class (CX-7), got %v", err)
	}
	if !reflect.DeepEqual(got, Snapshot{}) {
		t.Fatalf("a refused envelope yields no plaintext, got %+v", got)
	}
}

// TestCacheFile_ForeignGenerationAADRefusesAtAuthentication — R-3.7-4, CX-7,
// §3.11's "the old generation's file is unopenable anyway (AAD binding)". The
// generation twin of the pair test: sealed under generation gen-1, payload
// agreeing with the gen-2 reader — refusal can only come from authentication.
func TestCacheFile_ForeignGenerationAADRefusesAtAuthentication(t *testing.T) {
	base := t.TempDir()
	key := bytes.Repeat([]byte{0xAA}, 32)
	readerScope := Scope{PairID: "pair-1", Generation: "gen-2"}

	snap := testSnapshot()
	snap.PairIdentity = readerScope.PairID
	snap.ConfigGeneration = readerScope.Generation
	payload, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	envelope, err := sealSnapshot(payload, testPurposeFolder, snap, Scope{PairID: readerScope.PairID, Generation: "gen-1"}, key)
	if err != nil {
		t.Fatalf("seal under foreign generation: %v", err)
	}

	path := snapshotPath(base, readerScope.PairID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(path, envelope, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	got, found, err := store.Load(readerScope)
	if found {
		t.Fatalf("an envelope whose AAD binds a foreign generation must not be served (R-3.7-4), got found=true with sent name %q", got.Roles.Sent.Name)
	}
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("foreign-generation AAD mismatch must refuse with the cache-corrupt class (CX-7), got %v", err)
	}
	if !reflect.DeepEqual(got, Snapshot{}) {
		t.Fatalf("a refused envelope yields no plaintext, got %+v", got)
	}
}

// TestCacheFile_TransportHeaderTamperRefusesAtAuthentication — R-3.7-4 (the
// AAD binds the transport). Closes the CHECK audit's sharpest F-2 loss: the
// transport binding was checked by NOTHING outside the AAD, so dropping it
// (mutation M5) was invisible. Oracle: tampering the envelope header's
// transport marker (same length, so the framing survives) must fail
// authentication; the ciphertext — and therefore the payload, which still
// agrees with the scope — is untouched, so only the authentication layer can
// refuse.
func TestCacheFile_TransportHeaderTamperRefusesAtAuthentication(t *testing.T) {
	base := t.TempDir()
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}
	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	if err := store.Save(scope, testSnapshot(), Revision(1), bytes.Repeat([]byte{0xAA}, 32)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	good, err := os.ReadFile(snapshotPath(base, "pair-1"))
	if err != nil {
		t.Fatalf("read sealed file: %v", err)
	}

	// Fixture preconditions (loud, never silent): the header must carry the
	// expected transport marker at the framing's documented offset.
	if len(good) < envelopeTransportOffset+len(transportIMAP) {
		t.Fatalf("envelope implausibly short (%d bytes) — the transport header field is missing and this fixture must be revisited", len(good))
	}
	if gotLen := int(good[envelopeTransportOffset-1]); gotLen != len(transportIMAP) {
		t.Fatalf("envelope header transport length = %d, want %d — framing drifted, fixture must be revisited", gotLen, len(transportIMAP))
	}
	if gotMarker := string(good[envelopeTransportOffset : envelopeTransportOffset+len(transportIMAP)]); gotMarker != transportIMAP {
		t.Fatalf("envelope header transport marker = %q, want %q — fixture must be revisited", gotMarker, transportIMAP)
	}

	tampered := append([]byte(nil), good...)
	tampered[envelopeTransportOffset] ^= 0x01 // same length, different marker: a foreign-transport header
	if err := os.WriteFile(snapshotPath(base, "pair-1"), tampered, 0o600); err != nil {
		t.Fatalf("write tampered envelope: %v", err)
	}

	got, found, err := store.Load(scope)
	if found {
		t.Fatalf("a tampered transport header must not authenticate — the AAD binds the transport (R-3.7-4), got found=true with sent name %q", got.Roles.Sent.Name)
	}
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("transport-header tamper must refuse with the cache-corrupt class (R-3.7-4), got %v", err)
	}
	if !reflect.DeepEqual(got, Snapshot{}) {
		t.Fatalf("a refused envelope yields no plaintext, got %+v", got)
	}
}

// TestCacheFile_SealedWithoutAADRefusesNoFallback — R-3.7-1 ("there is no
// plaintext fallback path in either direction"). Closes the CHECK mutation
// audit's F-4 hole (survivor M4): a "compatibility" reader that retries
// without the AAD and accepts what opens was invisible, because nothing in
// the pack constructed an envelope that authenticates ONLY without the AAD.
// This test does exactly that — a test-side AES-256-GCM seal under the SAME
// key with a valid tag and NO AAD, payload agreeing with the reading scope —
// and requires Load to refuse it. A fallback mutant serves it and dies.
func TestCacheFile_SealedWithoutAADRefusesNoFallback(t *testing.T) {
	base := t.TempDir()
	key := bytes.Repeat([]byte{0xAA}, 32)
	scope := Scope{PairID: "pair-1", Generation: "gen-1"}

	// Payload identity agrees with the reading scope: a nil-AAD fallback
	// would open it, decrypt it and pass the payload comparison.
	snap := testSnapshot()
	payload, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("rand: %v", err)
	}
	ct := gcm.Seal(nil, nonce, payload, nil) // NO AAD — the fallback shape itself

	// Frame it exactly as an envelope the reader would parse.
	var out bytes.Buffer
	out.WriteString(envelopeMagic)
	out.WriteByte(envelopeVersion)
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], uint32(snap.SchemaVersion))
	out.Write(v[:])
	out.WriteByte(byte(len(snap.Transport)))
	out.WriteString(snap.Transport)
	out.Write(nonce)
	out.Write(ct)

	path := snapshotPath(base, scope.PairID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	store := newTestStore(t, base, testKey(0xAA), testPurposeFolder, 1)
	got, found, err := store.Load(scope)
	if found {
		t.Fatalf("an envelope that authenticates only WITHOUT its AAD must be refused — there is no plaintext fallback path (R-3.7-1), got found=true with sent name %q", got.Roles.Sent.Name)
	}
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("a no-AAD envelope must refuse with the cache-corrupt class (R-3.7-1/R-3.7-7), got %v", err)
	}
	if !reflect.DeepEqual(got, Snapshot{}) {
		t.Fatalf("a refused envelope yields no plaintext, got %+v", got)
	}
}
