//go:build !cgo

// cancel_session_isolation_test.go — T18 gateway placeholder.
//
// The authoritative T18 isolation test lives in:
//
//   pkg/agent/cancel_session_isolation_test.go
//
// The original gateway-level test attempted to start two WebSocket sessions
// concurrently and cancel one while the other kept running. It could not work
// because the agent loop serializes turns that share the same scope key
// ("agent:main:main"): the second turn message is redirected to the steering
// queue rather than starting a concurrent Chat call.
//
// The real test therefore lives in pkg/agent where turnState (unexported) can
// be directly injected, allowing two independent active turns to be registered
// against two distinct transcriptSessionIDs. RequestCancel is then called on
// session A only, and the test asserts session B's cancelFired remains false.
//
// This file exists solely to satisfy any tooling that expects a
// cancel_session_isolation_test.go in pkg/gateway/. It carries no test
// functions; all assertions are in the agent package file above.
//
// Spec ref: FR-12, FR-13a.
// Traces to: pkg/agent/cancel.go:96-165

package gateway
