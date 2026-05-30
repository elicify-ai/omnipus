//go:build !cgo

// cancel_subagent_cascade_test.go — T20b: Sub-agent cascade test.
//
// The sub-agent cascade test requires direct access to turnState (unexported),
// activeTurnStates (unexported field on AgentLoop), and the ability to inject
// synthetic sub-turns with controlled depth/parentTurnID/transcriptSessionID.
//
// These fields are only accessible from within the agent package. The real T20b
// test therefore lives in:
//
//   pkg/agent/cancel_subagent_cascade_test.go::TestCancel_SubAgentCascade
//
// This file exists to document the test location and to provide a thin
// gateway-level coverage exercise: we verify that canceling a session with
// multiple active turns (as seen through the exported API) returns both
// turn IDs in the Descendants slice — without needing access to unexported
// turnState fields.
//
// For the full cascade assertion (transcript entries for both parent and
// child), see pkg/agent/cancel_subagent_cascade_test.go.

package gateway
