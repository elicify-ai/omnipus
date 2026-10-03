package session

// RED pack E — boot epoch store (prerequisite E; ADR-20260928 D8.3/D8.5).
//
// COMPILE-BLOCKED RED, stated honestly: BootEpochStore (pkg/session/
// boot_epoch.go, new) does not exist at this pin (a2eb202dedfb3…). Until it
// lands, this file breaks the pkg/session test build with
// "undefined: NewBootEpochStore" — that compile failure is this pack's first
// RED signal (a-boot-implementation-contract-20261002T1100 REPORT §5). It is
// NOT a behavioral red and must never be reported as one.
//
// Oracles below derive from the specification, never from an implementation:
//   - ADR-20260928-sub-agent-control-plane @cd20cf8b D8.3: boot_seq is "a
//     monotonic boot counter persisted in the data dir"; D8.5: boot order
//     begins "persist a monotonic boot epoch".
//   - a-boot-implementation-contract-20261002T1100 REPORT §2 (store shape:
//     NewBootEpochStore(dir) *BootEpochStore; Mint() (uint64, error);
//     Current() uint64; file <dir>/boot_epoch.json, JSON exactly
//     {"boot_epoch": <int>} — one key, permission 0600) and §5 (E1–E6).
//   - a-boot-durability-correction-20261002T1130 REPORT §4: Mint writes via
//     the strict dir-sync variant; second Mint on one instance = visible
//     error; file value == MaxInt64 = visible startup error; corrupt file =
//     visible error naming the file, never reset to 1.
//   - omnipus/coordination/recovery-20261002/runtime-decisions-20261002.md
//     D1 (Q1=B): Windows WithFlock is a documented no-op — the concurrent
//     case is therefore Unix-guarded and asserts nothing on Windows.
//
// Mutation targets for CHECK (not run in RED): value+1→value (E2 dies),
// missing-file→mint-1 removed (E1 dies), corrupt→reset-to-1 (E4/E5 die),
// double-mint guard removed (E3 dies), overflow < vs <= (E6 dies),
// WithFlock removed (E-conc must eventually die; small race window).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
)

// bootEpochFileJSON reads <dir>/boot_epoch.json and returns its decoded
// payload, failing the test unless the file exists and carries exactly one
// key named boot_epoch (1100 §2: JSON exactly {"boot_epoch": <int>} — one
// key, no version field, no envelope).
func bootEpochFileJSON(t *testing.T, dir string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "boot_epoch.json"))
	if err != nil {
		t.Fatalf("read boot_epoch.json: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("boot_epoch.json is not a JSON object: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("boot_epoch.json must carry exactly one key, got %d: %s", len(payload), data)
	}
	if _, ok := payload["boot_epoch"]; !ok {
		t.Fatalf("boot_epoch.json must carry the key boot_epoch, got: %s", data)
	}
	return payload
}

// bootEpochFileValue returns the decoded boot_epoch value from
// <dir>/boot_epoch.json.
func bootEpochFileValue(t *testing.T, dir string) uint64 {
	t.Helper()
	var v uint64
	if err := json.Unmarshal(bootEpochFileJSON(t, dir)["boot_epoch"], &v); err != nil {
		t.Fatalf("decode boot_epoch value: %v", err)
	}
	return v
}

// TestBootEpochStore_FirstMintOnFreshDirIsOne (E1): a fresh install mints 1,
// persists {"boot_epoch": 1} as the one-key shape, and Current reflects it.
// Current before any mint is 0 (1100 §2).
func TestBootEpochStore_FirstMintOnFreshDirIsOne(t *testing.T) {
	dir := t.TempDir()
	store := NewBootEpochStore(dir)
	if got := store.Current(); got != 0 {
		t.Fatalf("Current() before any Mint = %d, want 0", got)
	}
	epoch, err := store.Mint()
	if err != nil {
		t.Fatalf("first Mint on a fresh dir must succeed, got error: %v", err)
	}
	if epoch != 1 {
		t.Fatalf("first Mint = %d, want 1 (D8.3: monotonic counter, fresh dir starts at 1)", epoch)
	}
	if got := bootEpochFileValue(t, dir); got != 1 {
		t.Fatalf("boot_epoch.json value = %d, want 1", got)
	}
	if got := store.Current(); got != 1 {
		t.Fatalf("Current() after Mint = %d, want 1 (Current reflects the minted value)", got)
	}
}

// TestBootEpochStore_ReopenedStoreMintsNextEpoch (E2): a new boot is a new
// store instance over the same dir; monotonicity lives in the file, so the
// reopen mints 2 and persists it (D8.3; 1100 §5 E2).
func TestBootEpochStore_ReopenedStoreMintsNextEpoch(t *testing.T) {
	dir := t.TempDir()
	first := NewBootEpochStore(dir)
	if epoch, err := first.Mint(); err != nil || epoch != 1 {
		t.Fatalf("first store Mint = (%d, %v), want (1, nil)", epoch, err)
	}
	second := NewBootEpochStore(dir)
	epoch, err := second.Mint()
	if err != nil {
		t.Fatalf("reopened store Mint must succeed, got error: %v", err)
	}
	if epoch != 2 {
		t.Fatalf("reopened store Mint = %d, want 2 (persistent +1 monotonicity)", epoch)
	}
	if got := bootEpochFileValue(t, dir); got != 2 {
		t.Fatalf("boot_epoch.json value after reopen-mint = %d, want 2", got)
	}
	if got := second.Current(); got != 2 {
		t.Fatalf("reopened store Current() = %d, want 2", got)
	}
}

// TestBootEpochStore_SecondMintOnSameInstanceRefused (E3): double-mint is a
// wiring bug and must be refused visibly, not silently reused; the stored
// epoch is unchanged (correction §4; 1100 §5 E3).
func TestBootEpochStore_SecondMintOnSameInstanceRefused(t *testing.T) {
	dir := t.TempDir()
	store := NewBootEpochStore(dir)
	if epoch, err := store.Mint(); err != nil || epoch != 1 {
		t.Fatalf("first Mint = (%d, %v), want (1, nil)", epoch, err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "boot_epoch.json"))
	if err != nil {
		t.Fatalf("read boot_epoch.json after first mint: %v", err)
	}
	_, err = store.Mint()
	if err == nil {
		t.Fatal("second Mint on the same instance must be refused visibly, got nil error")
	}
	after, err := os.ReadFile(filepath.Join(dir, "boot_epoch.json"))
	if err != nil {
		t.Fatalf("re-read boot_epoch.json after refused mint: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("refused second Mint must not touch the file: before=%s after=%s", before, after)
	}
}

// TestBootEpochStore_CorruptFileRefusedNotReset (E4): an unparseable epoch
// file is a visible error naming the file — never silently treated as a
// fresh install, because resetting to 1 collides D8.4's
// (parent_id, boot_seq) wake-dedup keys (correction §4; 1100 §5 E4).
func TestBootEpochStore_CorruptFileRefusedNotReset(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "boot_epoch.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt boot_epoch.json: %v", err)
	}
	store := NewBootEpochStore(dir)
	_, err := store.Mint()
	if err == nil {
		t.Fatal("Mint over a corrupt boot_epoch.json must fail visibly, got nil error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("boot_epoch.json")) {
		t.Fatalf("corrupt-file error must name boot_epoch.json, got: %v", err)
	}
	after, readErr := os.ReadFile(filepath.Join(dir, "boot_epoch.json"))
	if readErr != nil {
		t.Fatalf("re-read boot_epoch.json after refused mint: %v", readErr)
	}
	if string(after) != "not json" {
		t.Fatalf("corrupt file must not be reset/rewritten, got: %s", after)
	}
	if got := store.Current(); got != 0 {
		t.Fatalf("Current() after a refused mint = %d, want 0 (nothing was minted)", got)
	}
}

// TestBootEpochStore_MissingBootEpochKeyRefused (E5): a parseable JSON object
// without the boot_epoch key is corrupt for this one-key contract (1100 §2),
// so Mint refuses visibly and leaves the file untouched (correction §4).
func TestBootEpochStore_MissingBootEpochKeyRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "boot_epoch.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed keyless boot_epoch.json: %v", err)
	}
	store := NewBootEpochStore(dir)
	_, err := store.Mint()
	if err == nil {
		t.Fatal("Mint over a boot_epoch.json without the boot_epoch key must fail visibly, got nil error")
	}
	after, readErr := os.ReadFile(filepath.Join(dir, "boot_epoch.json"))
	if readErr != nil {
		t.Fatalf("re-read boot_epoch.json after refused mint: %v", readErr)
	}
	if string(after) != "{}" {
		t.Fatalf("refused mint must not rewrite the file, got: %s", after)
	}
}

// TestBootEpochStore_MaxInt64OverflowRefused (E6): a stored MaxInt64 epoch
// cannot mint a successor — the next value is unrepresentable in the int64
// wire (StopNote.yaml boot_seq format int64; correction §4/§6 R1-range) — so
// Mint fails visibly and the file is untouched (1100 §5 E6).
func TestBootEpochStore_MaxInt64OverflowRefused(t *testing.T) {
	dir := t.TempDir()
	overflowFile := []byte(`{"boot_epoch":9223372036854775807}`)
	if err := os.WriteFile(filepath.Join(dir, "boot_epoch.json"), overflowFile, 0o600); err != nil {
		t.Fatalf("seed MaxInt64 boot_epoch.json: %v", err)
	}
	store := NewBootEpochStore(dir)
	_, err := store.Mint()
	if err == nil {
		t.Fatal("Mint at MaxInt64 must fail visibly (overflow), got nil error")
	}
	after, readErr := os.ReadFile(filepath.Join(dir, "boot_epoch.json"))
	if readErr != nil {
		t.Fatalf("re-read boot_epoch.json after refused mint: %v", readErr)
	}
	if !bytes.Equal(after, overflowFile) {
		t.Fatalf("refused overflow mint must not rewrite the file: before=%s after=%s", overflowFile, after)
	}
}

// TestBootEpochStore_ConcurrentMintsGetDistinctSequentialEpochs (E-conc):
// two store handles over one dir mint concurrently; under the production
// WithFlock serialization around the read-increment-write both succeed with
// the distinct values {1, 2} in either order. Narrow and outcome-
// deterministic under a correct lock; a removed lock is CHECK's mutation
// target (1100 §5b: must eventually fail). Unix-guarded: founder Q1=B
// accepts the Windows concurrent-mint limitation (WithFlock is a documented
// no-op there), so this case asserts nothing on Windows.
func TestBootEpochStore_ConcurrentMintsGetDistinctSequentialEpochs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("founder Q1=B (runtime-decisions-20261002.md D1): Windows WithFlock is a documented no-op; concurrent mint limitation accepted")
	}
	dir := t.TempDir()
	const writers = 2
	type mintResult struct {
		epoch uint64
		err   error
	}
	start := make(chan struct{})
	results := make(chan mintResult, writers)
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store := NewBootEpochStore(dir)
			<-start
			epoch, err := store.Mint()
			results <- mintResult{epoch: epoch, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	epochs := make([]uint64, 0, writers)
	for res := range results {
		if res.err != nil {
			t.Fatalf("concurrent Mint must succeed under WithFlock, got error: %v", res.err)
		}
		epochs = append(epochs, res.epoch)
	}
	sort.Slice(epochs, func(i, j int) bool { return epochs[i] < epochs[j] })
	if epochs[0] != 1 || epochs[1] != 2 {
		t.Fatalf("concurrent mints = %v, want exactly {1, 2} (serialized monotonic increments)", epochs)
	}
	if got := bootEpochFileValue(t, dir); got != 2 {
		t.Fatalf("boot_epoch.json value after concurrent mints = %d, want 2", got)
	}
}
