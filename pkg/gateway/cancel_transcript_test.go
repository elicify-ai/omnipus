//go:build !cgo

// cancel_transcript_test.go — T3 gateway placeholder.
//
// The authoritative T3 transcript test lives in:
//
//   pkg/agent/cancel_transcript_test.go::TestCancel_TranscriptTurnCancelledEntry
//
// The original gateway-level test used a blocking LLM provider and a WebSocket
// cancel frame to drive the cancel state machine. It was flaky: when run in
// sequence with other gateway cancel tests (or with -count>1), the blocking
// provider's ctx was occasionally pre-canceled, causing the turn to finish
// before the cancel claim. The result was a non-deterministic test that passed
// in isolation but failed in CI.
//
// Root cause: the provider blocked on <-ctx.Done(), but the providerCtx could
// be already-canceled by a previous test's deferred cleanup (or by a leaked
// timer callback). The turn then exited with status=completed before
// ClaimCancel() fired, so the onCancelFinish callback was never registered and
// no turn_canceled entry was written.
//
// The real test therefore lives in pkg/agent, where turnState (unexported) can
// be directly injected into activeTurnStates. RequestCancel is called directly
// (no WebSocket needed), and Finish(false) is called synchronously to trigger
// the onCancelFinish callback. This is deterministic regardless of test ordering.
//
// This file exists solely to satisfy any tooling that expects a
// cancel_transcript_test.go in pkg/gateway/. It carries no test functions.
//
// Spec ref: FR-10, FR-12, FR-13a.
// Traces to: pkg/agent/cancel.go:181-213

package gateway
