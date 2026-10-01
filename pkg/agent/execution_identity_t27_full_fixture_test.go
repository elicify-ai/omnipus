// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// execution_identity_t27_full_fixture_test.go adds the small amount of extra
// harness this file's sibling (execution_identity_t27_full_test.go) needs on
// top of execution_identity_t27_fixture_test.go's shared T27 harness (the
// original partial pack, fc6519908): a bounded poll for a durable lifecycle
// state transition. Every test in the sibling file reuses
// newExecutionIdentityT27Harness, cancelExecutionIdentityT27Child,
// loadExecutionIdentityT27Record, executionIdentityT27JournalTail,
// requireExecutionIdentityT27Identity, nextExecutionIdentityT27ProviderCall
// and waitExecutionIdentityT27Signal unchanged — no second fixture is forked.
package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// waitExecutionIdentityT27RecordState polls the REAL durable lifecycle
// record for f's selected child until its State matches want, or fails after
// a bounded budget. A deadlock safeguard, like this package's other
// waitExecutionIdentityT27* helpers — not a timing oracle: the production
// completion path (steer_completion.go::completionDisposition) that lands a
// hard-aborted turn's record at LifecycleStopped runs on its own detached
// goroutine, so the test must observe the real write rather than assume a
// particular latency.
func waitExecutionIdentityT27RecordState(t *testing.T, f *executionIdentityT27Harness, want session.LifecycleState) *session.LifecycleRecord {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last *session.LifecycleRecord
	for {
		rec, err := f.al.GetSessionLifecycleStore().Load(f.childID)
		if err != nil {
			t.Fatalf("real LifecycleStore.Load(selected child) while waiting for state %q: %v", want, err)
		}
		last = rec
		if rec.State == want {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatalf("production record never reached state %q within budget; last observed state=%q terminal=%v",
				want, last.State, last.Terminal())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
