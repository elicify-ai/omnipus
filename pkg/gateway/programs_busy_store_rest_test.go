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
// methods and the CLI stand-in — live in the credentials pack,
// store_transaction_barrier_test.go; the CLI's real-binary exit code is
// hands-on H-17.)
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
	"github.com/stretchr/testify/require"
)

// pgBusyHolder pauses a second Store object's writer INSIDE its transaction,
// after its in-lock read (the FR-049 seam, spec R5-N1). The hook acts only on
// its first engagement — the holder's — so the paste's own transaction is
// never delayed by the seam once the holder is inside. enter receives the
// hold channel; closing it releases the holder. Returns the hold channel and
// the holder's completion channel.
func pgBusyHolder(t *testing.T, holder *credentials.Store, enter func(hold chan struct{})) (chan struct{}, chan error) {
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
	t.Cleanup(func() { credentials.TransactHook = nil })
	done := make(chan error, 1)
	go func() { done <- holder.Set("BUSY_PROBE", "x") }()
	select {
	case <-engaged:
	case <-time.After(2 * time.Second):
		t.Fatalf("BLOCKED: store transactions not implemented — TransactHook never fired (s.transact, FR-049; required by S-146's held writer)")
	}
	credentials.TransactHook = func(string) {} // inert: the paste's own transaction must not pause
	return hold, done
}

func TestProgramSave_BusyStore(t *testing.T) {
	f := pgPersonAPI(t)
	pgConnectGithub(t, f) // BLOCKED (routeMissing) at RED; connected gen 1 octocat at GREEN
	baselineStarts := f.ghStarted()

	holder := credentials.NewStore(f.api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(holder),
		"instrument: the holder store unlocks under the fixture master key")
	t.Cleanup(holder.Close)

	t.Run("busy_6s_holder_answers_503_after_the_bound", func(t *testing.T) {
		var releasedOnce bool
		hold, holderDone := pgBusyHolder(t, holder, func(h chan struct{}) { <-h })
		t.Cleanup(func() {
			if !releasedOnce {
				close(hold)
				<-holderDone
			}
		})

		// The person pastes while the holder is inside its transaction.
		start := time.Now()
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/github/credential",
			`{"value":"tok_new_67890"}`)
		elapsed := time.Since(start)
		require.Equal(t, http.StatusServiceUnavailable, w.Code,
			"a busy store answers 503: got %d %s after %s", w.Code, w.Body.String(), elapsed)
		require.Contains(t, w.Body.String(), "The credential store is busy. Try again.",
			"the busy message (S-146)")
		require.GreaterOrEqual(t, elapsed, 4500*time.Millisecond, "the paste waited the 5 s bound")
		require.LessOrEqual(t, elapsed, 5500*time.Millisecond, "the bound is 5 s ± 0.5 s (FR-049)")

		row := pgProgramEntryByID(t, f, "github")
		require.Equal(t, "connected", row["state"], "GitHub is still connected")
		require.Equal(t, "octocat", row["account_label"], "the old token is still in use")
		require.Equal(t, float64(1), row["generation"], "generation is still 1")
		require.Equal(t, baselineStarts, f.ghStarted(), "no check started for the failed paste")

		// Release the holder; 2 s later nothing of the paste is written (the
		// entry is still connected gen 1; the holder's own unrelated write is
		// the only new state).
		close(hold)
		releasedOnce = true
		require.NoError(t, <-holderDone, "the holder completes after release")
		time.Sleep(2 * time.Second)
		after := pgProgramEntryByID(t, f, "github")
		require.Equal(t, "connected", after["state"], "still connected 2 s after release")
		require.Equal(t, float64(1), after["generation"], "the paste never landed, even late")
		require.Equal(t, baselineStarts, f.ghStarted(), "no late check started either")
	})

	t.Run("holder_released_after_1s_paste_succeeds_generation_2", func(t *testing.T) {
		_, holderDone := pgBusyHolder(t, holder, func(chan struct{}) { time.Sleep(1 * time.Second) })
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/programs/github/credential",
			`{"value":"tok_new_67890"}`)
		require.Equal(t, http.StatusOK, w.Code,
			"the paste succeeds when the holder releases within the bound: %d %s", w.Code, w.Body.String())
		entry := pgPollEntryState(t, f, "github", "connected", 15*time.Second)
		require.Equal(t, float64(2), entry["generation"], "the successful paste is generation 2")
		require.NoError(t, <-holderDone, "the holder's own write completed")
	})
}
