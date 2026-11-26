// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8, S-146 — the
// paste face of a busy store. With a writer held inside its transaction (a
// second credentials.Store object on the gateway's own store path), the
// person's paste answers 503 "The credential store is busy. Try again."
// after the 5 s bound (± 0.5 s), GitHub is still generation 1 with the old
// token in use, no check started, and nothing of the paste is written even
// 2 s after the holder releases; with a 1 s holder the paste succeeds as
// generation 2. (The busy store's other faces — ErrStoreBusy from the store
// methods and a lower-level re-exec store child — live in the credentials
// pack. Actual CLI + running-gateway acceptance remains separately BLOCKED
// in tests/cli-credentials-s1/real_cli_gateway_test.go; H-17 is additional,
// never an automated S-136/S-146 substitute.)
//
// RED: the save route is not registered, so pgConnectGithub fatal-BLOCKs via
// routeMissing before the busy face can be exercised.

package gateway

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/tests/testutil/programstore"
	"github.com/stretchr/testify/require"
)

// pgAsyncResult joins a launched operation and caches its error, so the body
// and cleanup can both join it (the cached-result pattern of CHECK R03: a
// one-shot channel received by the body and again by cleanup hangs).
type pgAsyncResult struct {
	done   chan struct{}
	result error
}

func pgLaunchAsync(op func() error) *pgAsyncResult {
	r := &pgAsyncResult{done: make(chan struct{})}
	go func() {
		r.result = op()
		close(r.done)
	}()
	return r
}

func (r *pgAsyncResult) wait() error { <-r.done; return r.result }

// pgBusyHolder pauses a second Store object's writer INSIDE its transaction,
// after its in-lock read (the FR-049 seam, spec R5-N1). The hook acts only on
// its first engagement — the holder's — so the paste's own transaction is
// never delayed by the seam once the holder is inside. enter receives the
// hold channel; closing it releases the holder. Returns the hold channel and
// the holder's cached-result handle.
func pgBusyHolder(t *testing.T, holder *credentials.Store, enter func(hold chan struct{})) (func(), *pgAsyncResult) {
	t.Helper()
	hold := make(chan struct{})
	engaged := make(chan struct{}, 1)
	var mu sync.Mutex
	armed := true
	credentials.TransactHook = func(stage string) {
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
		enter(hold)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(hold) }) }
	done := pgLaunchAsync(func() error { return holder.Set("BUSY_PROBE", "x") })
	// Register before waiting for the hook: even a failed engagement must join.
	t.Cleanup(func() {
		release()
		pgJoinHolder(t, done, 10*time.Second, "busy holder")
		credentials.TransactHook = nil
	})
	select {
	case <-engaged:
	case <-time.After(2 * time.Second):
		t.Fatalf("BLOCKED: store transactions not implemented — TransactHook never fired (s.transact, FR-049; required by S-146's held writer)")
	}
	// The same hook is now disarmed, so no concurrent global-hook replacement.
	return release, done
}

// pgJoinHolder joins the holder's cached-result handle within the grace
// period (bounded — never an unbounded cleanup hang).
func pgJoinHolder(t *testing.T, holder *pgAsyncResult, grace time.Duration, what string) {
	t.Helper()
	select {
	case <-holder.done:
	case <-time.After(grace):
		t.Errorf("%s did not finish within %s (leaked goroutine)", what, grace)
	}
}

func TestProgramSave_BusyStore(t *testing.T) {
	t.Run("busy_6s_holder_answers_503_after_the_bound", func(t *testing.T) {
		f := pgPersonAPI(t)
		pgConnectGithub(t, f)
		baselineStarts := f.ghStarted()
		holder := credentials.NewStore(f.api.credentialsStorePath())
		require.NoError(t, credentials.Unlock(holder))
		t.Cleanup(holder.Close)
		canary := programstore.SeedCanary(t, f.store)
		beforeStore := programstore.Snapshot(t, f.store, canary)

		release, holderDone := pgBusyHolder(t, holder, func(h chan struct{}) { <-h })

		// The 5 s bound of FR-049 is the store wait: it starts when the paste's
		// own transaction asks for admission (the "admission" stage), not when
		// the request arrives. The whole-request time also holds the session
		// cookie check (bcrypt), about 68 ms normally and about 0.9 s under
		// -race (measured: /workspace/logs/1960/ci-fix-be/busy-timing-race*.log),
		// so the whole-request time is only a LOWER bound here.
		var admissionMu sync.Mutex
		var admissions []time.Time
		holderHook := credentials.TransactHook // the holder's, already disarmed
		credentials.TransactHook = func(stage string) {
			if stage == "admission" {
				admissionMu.Lock()
				admissions = append(admissions, time.Now())
				admissionMu.Unlock()
			}
			if holderHook != nil {
				holderHook(stage)
			}
		}

		// The person pastes while the holder is inside its transaction.
		start := time.Now()
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/github/credential",
			`{"value":"tok_new_67890"}`)
		end := time.Now()
		elapsed := end.Sub(start)
		require.Equal(t, http.StatusServiceUnavailable, w.Code,
			"a busy store answers 503: got %d %s after %s", w.Code, w.Body.String(), elapsed)
		require.Contains(t, w.Body.String(), "The credential store is busy. Try again.",
			"the busy message (S-146)")
		admissionMu.Lock()
		require.NotEmpty(t, admissions, "instrument: the paste's own transaction asked for admission")
		storeWaitStart := admissions[len(admissions)-1]
		admissionMu.Unlock()
		storeWait := end.Sub(storeWaitStart)
		require.GreaterOrEqual(t, elapsed, 4500*time.Millisecond, "the paste waited the 5 s bound")
		require.GreaterOrEqual(t, storeWait, 4500*time.Millisecond, "the store wait lasted the 5 s bound")
		require.LessOrEqual(t, storeWait, 5500*time.Millisecond, "the store wait is bounded at 5 s ± 0.5 s (FR-049), measured from admission to the response")

		row := pgConnection(t, pgProgramEntryByID(t, f, "github"))
		require.Equal(t, "connected", row["state"], "GitHub is still connected")
		require.Equal(t, "octocat", row["account_label"], "the old token is still in use")
		require.Equal(t, float64(1), row["generation"], "generation is still 1")
		require.Equal(t, baselineStarts, f.ghStarted(), "no check started for the failed paste")

		// Release the holder; 2 s later nothing of the paste is written (the
		// entry is still connected gen 1; the holder's own unrelated write is
		// the only new state).
		release()
		require.NoError(t, holderDone.wait(), "the holder completes after release")
		time.Sleep(2 * time.Second)
		after := pgConnection(t, pgProgramEntryByID(t, f, "github"))
		require.Equal(t, "connected", after["state"], "still connected 2 s after release")
		require.Equal(t, float64(1), after["generation"], "the paste never landed, even late")
		require.Equal(t, baselineStarts, f.ghStarted(), "no late check started either")
		afterStore := programstore.Snapshot(t, f.store, canary)
		require.Equal(t, "x", afterStore.Values["BUSY_PROBE"], "holder's unrelated write survives")
		delete(afterStore.Values, "BUSY_PROBE")
		delete(afterStore.Metadata, "BUSY_PROBE")
		kept := afterStore.Inventory[:0]
		for _, item := range afterStore.Inventory {
			if item.Reference != "BUSY_PROBE" {
				kept = append(kept, item)
			}
		}
		afterStore.Inventory = kept
		require.Equal(t, beforeStore, afterStore, "failed paste preserves all program/private state and known receipts, not only connection text")
	})

	t.Run("holder_released_after_1s_paste_succeeds_generation_2", func(t *testing.T) {
		f := pgPersonAPI(t)
		pgConnectGithub(t, f)
		baselineStarts := f.ghStarted()
		holder := credentials.NewStore(f.api.credentialsStorePath())
		require.NoError(t, credentials.Unlock(holder))
		t.Cleanup(holder.Close)
		canary := programstore.SeedCanary(t, f.store)
		beforeStore := programstore.Snapshot(t, f.store, canary)

		// CHECK R14: the fake accepts BOTH tokens — the Given's
		// tok_valid_12345 and the pasted tok_new_67890 (the row reused the
		// octocat-only fake, so a correct paste check ended check_failed,
		// never connected).
		shBody, cmdBody := pgFakeGhAccounts(map[string]string{
			"tok_valid_12345": "octocat", "tok_new_67890": "hubot"})
		pgInstallFakeGh(t, f, shBody, cmdBody)
		_, holderDone2 := pgBusyHolder(t, holder, func(chan struct{}) { time.Sleep(1 * time.Second) })
		require.NotEmpty(t, beforeStore.Values)
		require.Positive(t, baselineStarts)
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/github/credential",
			`{"value":"tok_new_67890"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"the paste succeeds when the holder releases within the bound: %d %s", w.Code, w.Body.String())
		entry := pgPollEntryState(t, f, "github", "connected", 15*time.Second)
		require.Equal(t, float64(2), pgConnection(t, entry)["generation"], "the successful paste is generation 2")
		require.Equal(t, "hubot", pgConnection(t, entry)["account_label"],
			"the pasted token's check really ran (the fake maps it to hubot)")
		require.NoError(t, holderDone2.wait(), "the holder's own write completed")
	})
}
