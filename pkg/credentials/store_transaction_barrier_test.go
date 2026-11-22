// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenarios S-135 (a second Store object cannot write back stale
// program state), S-136 (the CLI writer and the gateway do not lose each
// other's writes) and S-146 (a busy store fails ordinary writes truthfully),
// invariant I-3 / FR-049.
//
// Oracles come from the SPEC scenario text: the pause is INSIDE the writer's
// transaction after its in-lock read (R5-N1); ordinary admission is bounded at
// 5 s ± 0.5 s and ends with ErrStoreBusy and no file change from the failed
// writer; after release, every surviving write is present and the program
// credential, its receipt and its generation survive an unrelated write.
//
// The S-135 "read outside the lock" mutant and other planted build-tag mutants
// are CHECK's mutation probes (FR-045), not run in RED. The real
// `omnipus credentials set` binary stays hands-on (H-17); its automated
// stand-in here is a second process writing the same store path.

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const (
	s136ChildEnv  = "OMNIPUS_S136_CHILD"
	s136PathEnv   = "OMNIPUS_S136_PATH"
	s136KeyEnv    = "OMNIPUS_S136_KEY"
	s136RoleEnv   = "OMNIPUS_S136_ROLE"
	s136DirEnv    = "OMNIPUS_S136_DIR" // scratch dir for markers/results
	s136ResultEnv = "OMNIPUS_S136_RESULT"

	s136RoleHold      = "hold-sidecar" // child holds the sidecar lock for 6 s
	s136RolePausedSet = "paused-set"   // child pauses inside Set until the signal file
	s136RolePlainSet  = "plain-set"    // child runs Set("OTHER","y") and records the error
)

func TestCredentialStore_Transactions(t *testing.T) {
	if os.Getenv(s136ChildEnv) == "1" {
		runS136ChildBody(t)
		return
	}

	t.Run("second_store_cannot_write_back_stale_state", s135SecondStoreStaleWriter)
	t.Run("cross_process_writer_barrier", s136CrossProcessBarrier)
	t.Run("busy_store_fails_ordinary_writes_within_five_seconds", s146BusyStoreBound)
	t.Run("cross_process_sidecar_holder_busy_and_cli_exit", s146CrossProcessHolder)
}

// txTestKey is the fixed 32-byte test key for every store this file opens.
func txTestKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(0x20 + i)
	}
	return key
}

func openUnlockedTxStore(t *testing.T, path string) *Store {
	t.Helper()
	store := NewStore(path)
	require.NoError(t, store.UnlockWithKey(txTestKey()), "unlock %s", path)
	t.Cleanup(store.Close)
	return store
}

func reopenStoreForRead(t *testing.T, path string, key []byte) *Store {
	t.Helper()
	store := NewStore(path)
	require.NoError(t, store.UnlockWithKey(key), "reopen %s", path)
	return store
}

// publishProgramForTx is the S-135/S-146 setup: one program credential under
// reference R at generation 1 with a card receipt. Only CommitProgramCredential
// may create it (I-3); the RED stub is a no-op, so setup reports BLOCKED.
func publishProgramForTx(t *testing.T, store *Store, ref string) {
	t.Helper()
	receipt := CardReceipt{
		RequestID:   "req-tx-" + ref,
		SessionID:   "sess-tx",
		References:  []string{ref},
		SubmittedAt: time.Now(),
	}
	gen, err := store.CommitProgramCredential(ref, "tok_valid_12345", &receipt)
	if err == nil && gen == 0 {
		if _, ok := store.ProgramEntryGeneration(ref); !ok {
			t.Fatalf("BLOCKED: CommitProgramCredential not implemented — required by FR-049 setup " +
				"(spec I-3: program-owned entries are created only by the program-scoped methods)")
		}
	}
	require.NoError(t, err, "setup: publish program credential %s", ref)
}

// waitHookEntered fails BLOCKED when the pause seam cannot engage: that is the
// FR-049 gap itself, reported honestly rather than skipped.
func waitHookEntered(t *testing.T, entered chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-entered:
		return
	case <-time.After(timeout):
		t.Fatalf("BLOCKED: store transactions not implemented — TransactHook never fired " +
			"(s.transact, FR-049; scenarios S-135/S-136/S-146)")
	}
}

// pausedWriteHook builds a TransactHook that acts ONLY on its first
// engagement (so a later writer's transaction is never delayed by it), sends
// one signal on `engaged`, and then runs `enter` (which normally blocks).
func pausedWriteHook(enter func()) (hook func(stage string), engaged chan struct{}) {
	engaged = make(chan struct{}, 1)
	var mu sync.Mutex
	armed := true
	hook = func(stage string) {
		if stage != "after-read" {
			return
		}
		mu.Lock()
		if !armed {
			mu.Unlock()
			return
		}
		armed = false
		mu.Unlock()
		select {
		case engaged <- struct{}{}:
		default:
		}
		enter()
	}
	return hook, engaged
}

// startPausedWriter starts store.Set(name, value) paused INSIDE its
// transaction: the hook reports entry on `entered` and blocks until `hold` is
// closed (R5-N1: the pause is after the in-lock read).
func startPausedWriter(store *Store, name, value string) (entered, hold chan struct{}, done chan error) {
	hold = make(chan struct{})
	var hook func(stage string)
	hook, entered = pausedWriteHook(func() { <-hold })
	TransactHook = hook
	done = make(chan error, 1)
	go func() { done <- store.Set(name, value) }()
	return entered, hold, done
}

// s135SecondStoreStaleWriter: while B is paused inside its transaction (after
// its in-lock read), A cannot commit; after release both writes exist and the
// program credential, receipt and generation survive the unrelated write.
func s135SecondStoreStaleWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	key := txTestKey()
	storeA := openUnlockedTxStore(t, path)
	storeB := openUnlockedTxStore(t, path)
	t.Cleanup(func() { TransactHook = nil })

	entered, hold, bDone := startPausedWriter(storeB, "UNRELATED", "x")
	waitHookEntered(t, entered, 2*time.Second) // BLOCKED in RED: no s.transact yet

	publishProgramForTx(t, storeA, "prog_github_token")

	aDone := make(chan error, 1)
	go func() {
		_, err := storeA.CommitProgramCredential("prog_github_token", "tok_new_67890", nil)
		aDone <- err
	}()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-aDone:
		t.Fatalf("A committed while B held the transaction (err=%v) — stale-writer barrier FR-049 missing", err)
	default:
	}
	reader := reopenStoreForRead(t, path, key)
	v, err := reader.Get("prog_github_token")
	require.NoError(t, err)
	require.Equal(t, "tok_valid_12345", v,
		"the file must still hold the old token while B holds the transaction")
	reader.Close()

	close(hold)
	require.NoError(t, <-bDone, "B completes after release")
	require.NoError(t, <-aDone, "A completes after release")

	final := reopenStoreForRead(t, path, key)
	defer final.Close()
	gotNew, err := final.Get("prog_github_token")
	require.NoError(t, err)
	require.Equal(t, "tok_new_67890", gotNew, "A's program token replace is present")
	gotUnrelated, err := final.Get("UNRELATED")
	require.NoError(t, err)
	require.Equal(t, "x", gotUnrelated, "B's unrelated write survives")
	gen, ok := final.ProgramEntryGeneration("prog_github_token")
	require.True(t, ok, "generation metadata present after A's commit")
	require.EqualValues(t, 2, gen, "the replace is generation 2")
	rec, ok, err := final.CardReceipt("req-tx-prog_github_token")
	require.NoError(t, err)
	require.True(t, ok, "card receipt CR survives the unrelated write")
	require.Equal(t, []string{"prog_github_token"}, rec.References)

	// Disconnect variant: A removes the program credential while B is paused.
	entered2, hold2, bDone2 := startPausedWriter(storeB, "UNRELATED2", "z")
	waitHookEntered(t, entered2, 2*time.Second)
	rmDone := make(chan error, 1)
	go func() { rmDone <- storeA.RemoveProgramCredential("prog_github_token") }()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-rmDone:
		t.Fatalf("RemoveProgramCredential committed while B held the transaction (err=%v)", err)
	default:
	}
	close(hold2)
	require.NoError(t, <-bDone2)
	require.NoError(t, <-rmDone)

	final2 := reopenStoreForRead(t, path, key)
	defer final2.Close()
	_, err = final2.Get("prog_github_token")
	require.Error(t, err, "the disconnect removed the program credential")
	gotUnrelated2, err := final2.Get("UNRELATED2")
	require.NoError(t, err)
	require.Equal(t, "z", gotUnrelated2, "B's unrelated write survives the disconnect")
}

// s136CrossProcessBarrier: a separate process is paused inside its
// transaction after the in-lock read; the in-process paste must wait, and
// after release both values are in the file (S-136). The automated suite
// pauses a re-exec'd test binary as the second process; the real
// `omnipus credentials set` binary path is hands-on H-17.
func s136CrossProcessBarrier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	scratch := t.TempDir()
	pausedMarker := filepath.Join(scratch, "child-paused")
	signalFile := filepath.Join(scratch, "release-signal")
	resultFile := filepath.Join(scratch, "child-result.json")

	cmd := exec.Command(os.Args[0], "-test.run", "^TestCredentialStore_Transactions$")
	cmd.Env = append(os.Environ(),
		s136ChildEnv+"=1",
		s136RoleEnv+"="+s136RolePausedSet,
		s136PathEnv+"="+path,
		s136KeyEnv+"="+hex.EncodeToString(txTestKey()),
		s136DirEnv+"="+scratch,
	)
	require.NoError(t, cmd.Start(), "start child writer process")
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	waitForFile(t, pausedMarker, 5*time.Second,
		"BLOCKED: the child writer never paused inside its transaction — "+
			"s.transact does not invoke TransactHook (FR-049)")

	store := openUnlockedTxStore(t, path)
	pasteDone := make(chan error, 1)
	go func() {
		_, err := store.CommitProgramCredential("prog_github_token", "tok_valid_12345", nil)
		pasteDone <- err
	}()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-pasteDone:
		t.Fatalf("the paste returned while the other process held the transaction (err=%v) — "+
			"cross-process barrier FR-049 missing", err)
	default:
	}

	require.NoError(t, os.WriteFile(signalFile, []byte("go"), 0o600), "write release signal")
	select {
	case err := <-pasteDone:
		require.NoError(t, err, "the paste completes after release")
	case <-time.After(3 * time.Second):
		t.Fatal("the paste did not complete within 3 s of release")
	}

	waitForFile(t, resultFile, 5*time.Second, "the child writer did not finish after release")
	var child s136ChildResult
	b, err := os.ReadFile(resultFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &child), "parse child result %s", b)
	require.Empty(t, child.Err, "the child's unrelated Set succeeds after release")

	final := reopenStoreForRead(t, path, txTestKey())
	defer final.Close()
	got, err := final.Get("prog_github_token")
	require.NoError(t, err)
	require.Equal(t, "tok_valid_12345", got, "the program token survives the concurrent write")
	other, err := final.Get("OTHER")
	require.NoError(t, err)
	require.Equal(t, "y", other, "the child's unrelated write survives the paste")
	gen, ok := final.ProgramEntryGeneration("prog_github_token")
	require.True(t, ok, "generation metadata present after the paste")
	require.Positive(t, gen)
}

// s146BusyStoreBound: a writer held inside its transaction for 6 s; the paste
// fails with ErrStoreBusy after 5 s (± 0.5 s), the file is unchanged after the
// holder releases (2 s later), and a 1 s holder lets the paste succeed as
// generation 2.
func s146BusyStoreBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := openUnlockedTxStore(t, path)
	holder := openUnlockedTxStore(t, path)
	t.Cleanup(func() { TransactHook = nil })
	publishProgramForTx(t, store, "prog_github_token")

	// 6 s holder in the same process (second Store object). The hook acts
	// only on its first engagement — the holder's — so the paste's own
	// transaction is never delayed by the seam.
	holderHook, engaged := pausedWriteHook(func() { time.Sleep(6 * time.Second) })
	TransactHook = holderHook
	holderDone := make(chan error, 1)
	go func() { holderDone <- holder.Set("OTHER", "x") }()
	waitHookEntered(t, engaged, 2*time.Second)

	// The paste runs with an inert hook (the holder is already inside its
	// sleeping engagement).
	TransactHook = func(stage string) {}
	pasteStart := time.Now()
	_, pasteErr := store.CommitProgramCredential("prog_github_token", "tok_new_67890", nil)
	elapsed := time.Since(pasteStart)
	require.ErrorIs(t, pasteErr, ErrStoreBusy, "the paste fails busy against a held store")
	require.GreaterOrEqual(t, elapsed, 4500*time.Millisecond, "admission waits the full 5 s bound")
	require.LessOrEqual(t, elapsed, 5500*time.Millisecond, "the admission bound is 5 s ± 0.5 s")

	require.NoError(t, <-holderDone, "the holder's own write completes when it releases")
	TransactHook = nil
	time.Sleep(2 * time.Second) // spec: file unchanged 2 s after the holder releases
	after := reopenStoreForRead(t, path, txTestKey())
	v, err := after.Get("prog_github_token")
	require.NoError(t, err)
	require.Equal(t, "tok_valid_12345", v, "the failed paste keeps the previous token")
	gen, ok := after.ProgramEntryGeneration("prog_github_token")
	require.True(t, ok)
	require.EqualValues(t, 1, gen, "generation unchanged after the failed paste")
	other, err := after.Get("OTHER")
	require.NoError(t, err)
	require.Equal(t, "x", other, "the holder's own write is present after it releases")

	// 1 s holder: the paste succeeds as generation 2.
	holderHook2, engaged2 := pausedWriteHook(func() { time.Sleep(1 * time.Second) })
	TransactHook = holderHook2
	holder2Done := make(chan error, 1)
	go func() { holder2Done <- holder.Set("OTHER2", "y") }()
	waitHookEntered(t, engaged2, 2*time.Second)
	TransactHook = func(stage string) {}
	pasteStart2 := time.Now()
	gen2, pasteErr2 := store.CommitProgramCredential("prog_github_token", "tok_new_67890", nil)
	elapsed2 := time.Since(pasteStart2)
	require.NoError(t, pasteErr2, "the paste succeeds when the holder releases within the bound")
	require.Greater(t, elapsed2, time.Second, "the paste waited for the 1 s holder")
	require.EqualValues(t, 2, gen2, "the successful paste is generation 2")
	require.NoError(t, <-holder2Done)
	TransactHook = nil
}

// s146CrossProcessHolder: another PROCESS holds the sidecar lock for 6 s (no
// seam needed — the raw lock exists today); the in-process write must end
// with ErrStoreBusy after the 5 s bound, and a second process attempting the
// same ordinary write records the busy outcome (the automated stand-in for the
// CLI `credentials set`; the real CLI binary is hands-on H-17).
func s146CrossProcessHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	scratch := t.TempDir()
	store := openUnlockedTxStore(t, path)

	holder := exec.Command(os.Args[0], "-test.run", "^TestCredentialStore_Transactions$")
	holder.Env = append(os.Environ(),
		s136ChildEnv+"=1", s136RoleEnv+"="+s136RoleHold, s136PathEnv+"="+path,
		s136KeyEnv+"="+hex.EncodeToString(txTestKey()), s136DirEnv+"="+scratch)
	require.NoError(t, holder.Start(), "start holder process")
	t.Cleanup(func() { _ = holder.Process.Kill(); _, _ = holder.Process.Wait() })

	// Wait until the child actually holds the sidecar lock.
	waitForFile(t, filepath.Join(scratch, "holder-locked"), 5*time.Second,
		"holder process did not take the sidecar lock within 5 s (harness failure, not a product result)")

	cliResult := filepath.Join(scratch, "cli-result.json")
	cli := exec.Command(os.Args[0], "-test.run", "^TestCredentialStore_Transactions$")
	cli.Env = append(os.Environ(),
		s136ChildEnv+"=1", s136RoleEnv+"="+s136RolePlainSet, s136PathEnv+"="+path,
		s136KeyEnv+"="+hex.EncodeToString(txTestKey()), s136DirEnv+"="+scratch,
		s136ResultEnv+"="+cliResult)
	require.NoError(t, cli.Start(), "start CLI stand-in process")
	t.Cleanup(func() { _ = cli.Process.Kill(); _, _ = cli.Process.Wait() })

	start := time.Now()
	pasteErr := store.Set("UNRELATED", "x")
	elapsed := time.Since(start)
	require.ErrorIs(t, pasteErr, ErrStoreBusy,
		"an ordinary write against a 6 s cross-process holder fails busy (FR-049)")
	require.GreaterOrEqual(t, elapsed, 4500*time.Millisecond,
		"admission waits the 5 s bound")
	require.LessOrEqual(t, elapsed, 6500*time.Millisecond,
		"the 5 s bound still ends the write (slack for process scheduling)")

	waitForFile(t, cliResult, 8*time.Second, "the CLI stand-in did not record its outcome")
	var res s136ChildResult
	b, err := os.ReadFile(cliResult)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &res), "parse CLI stand-in result %s", b)
	require.Contains(t, res.Err, "busy",
		"the CLI stand-in reports the busy refusal (production: non-zero exit, H-17)")
}

// --- child-process helpers --------------------------------------------------

type s136ChildResult struct {
	Err string `json:"err"`
}

// runS136ChildBody runs inside the re-exec'd test binary.
func runS136ChildBody(t *testing.T) {
	t.Helper()
	path := os.Getenv(s136PathEnv)
	key, err := hex.DecodeString(os.Getenv(s136KeyEnv))
	if err != nil || len(key) != 32 {
		t.Fatalf("child: bad key hex: %v (len %d)", err, len(key))
	}
	dir := os.Getenv(s136DirEnv)
	store := NewStore(path)
	if err := store.UnlockWithKey(key); err != nil {
		t.Fatalf("child: unlock: %v", err)
	}
	defer store.Close()

	switch os.Getenv(s136RoleEnv) {
	case s136RoleHold:
		// Hold the sidecar lock for 6 s — the "another process" holder of
		// S-146, needing no seam (the raw lock exists today).
		_ = fileutil.WithFlock(fileutil.SidecarLockPath(path), func() error {
			_ = os.WriteFile(filepath.Join(dir, "holder-locked"), []byte("1"), 0o600)
			time.Sleep(6 * time.Second)
			return nil
		})
	case s136RolePausedSet:
		// Pause inside Set after the in-lock read until the signal file.
		pausedMarker := filepath.Join(dir, "child-paused")
		signalFile := filepath.Join(dir, "release-signal")
		resultFile := filepath.Join(dir, "child-result.json")
		TransactHook = func(stage string) {
			if stage != "after-read" {
				return
			}
			_ = os.WriteFile(pausedMarker, []byte("1"), 0o600)
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(signalFile); err == nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
		setErr := store.Set("OTHER", "y")
		TransactHook = nil
		rec := s136ChildResult{}
		if setErr != nil {
			rec.Err = setErr.Error()
		}
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(resultFile, b, 0o600)
	case s136RolePlainSet:
		resultFile := os.Getenv(s136ResultEnv)
		setErr := store.Set("OTHER", "y")
		rec := s136ChildResult{}
		if setErr != nil {
			rec.Err = setErr.Error()
		}
		b, _ := json.Marshal(rec)
		if werr := os.WriteFile(resultFile, b, 0o600); werr != nil {
			t.Fatalf("child: write result: %v", werr)
		}
	default:
		t.Fatalf("child: unknown role %q", os.Getenv(s136RoleEnv))
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration, blockedMsg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(blockedMsg)
}
