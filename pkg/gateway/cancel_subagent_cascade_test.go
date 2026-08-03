// cancel_subagent_cascade_test.go — T20b: Sub-agent cascade test.
//
// The sub-agent cascade test requires direct access to turnState (unexported),
// activeTurnStates (unexported field on AgentLoop), and the ability to inject
// synthetic sub-turns with controlled depth/parentTurnID/routingSessionID.
//
// ADR-057 update (W22 comment-accuracy pass): pre-ADR-057, injecting a shared
// transcriptSessionID onto both turnStates was itself sufficient to make the
// cascade discover both turns (transcriptSessionID doubled as the whole-session
// routing key). Post-ADR-057 D1/D2, transcriptSessionID is the child's OWN
// distinct session id (its "own identity" role, D1) and is no longer what the
// cascade predicates key on; the field that must be shared verbatim across
// parent and child for InterruptSession's activeTurnStates.Range walk to find
// both turns is routingSessionID (D2's role-B routing key — see
// pkg/agent/steering.go's collectDescendantTurnIDs/InterruptSession). The real
// T20b test was updated accordingly (ADR-057 U30) and still lives in:
//
//   pkg/agent/cancel_subagent_cascade_test.go::TestCancel_SubAgentCascade
//
// This file exists solely to document that test's location and to satisfy any
// tooling that expects a cancel_subagent_cascade_test.go in pkg/gateway/. It
// carries no test functions of its own — there is no gateway-exported API that
// lets an external caller inject a synthetic multi-turn session with
// controlled depth/parentTurnID/routingSessionID, so a "thin gateway-level
// coverage exercise" of this specific scenario is not achievable from this
// package without reaching into agent-package internals (which would just
// duplicate, not independently verify, the real test). For the full cascade
// assertion (transcript entries for both parent and child, DescendantsCancelled
// listing both turn IDs), see pkg/agent/cancel_subagent_cascade_test.go.

package gateway
