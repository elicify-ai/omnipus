// Package session — pkg/session/ids.go
//
// ADR-057 (session/subagent parent-child parity), work item W20, FR-004:
// named identity types so the two identity namespaces this migration
// untangles can no longer be confused by the compiler.
//
// Before this file, both namespaces were passed around as bare `string`,
// which is exactly what let bugs #576 and #577 through: a delegate/routing
// session id was handed to code that expected the real, store-backed
// transcript session id, and vice versa. Both directions compiled cleanly
// and failed at runtime — a lost transcript write, or a cancel/interrupt
// that silently matched nothing. Two distinct named types with different
// underlying purposes turn that class of bug into a compile error: Go does
// not implicitly convert between two named types even when they share an
// underlying type, so assigning a RoutingSessionID where a SessionID is
// expected (or the reverse) requires an explicit conversion that a reviewer
// — or a static gate — can see.
//
// Scope of this file: the two type declarations only (FR-004). FR-090's
// conversion boundary — re-typing turnState, processOptions and the
// UnifiedStore public API to carry these instead of bare strings — is
// deliberately NOT done here; it is the job of the units that own those
// files (U2/U3/U4/U5/U6 and others), each converting its own call sites.
// This unit (U1) introduces the vocabulary; it does not speak it anywhere
// yet.
package session

// SessionID identifies a real, store-backed session: the id under which
// UnifiedStore persists a session's on-disk state (meta.json,
// transcript.jsonl, stats.json — see daypartition.go and unified.go).
// Every SessionID names a session directory that a store operation
// (AppendTranscript, SetMeta, ReadTranscript, …) can resolve against.
//
// For a delegated child, SessionID is the child's OWN session — the one
// FR-005/FR-009 require to exist as a real store-backed session distinct
// from its parent's.
type SessionID string

// RoutingSessionID identifies the routing identity a turn inherits verbatim
// from the root of its delegation subtree (FR-011). It is the value shared,
// unchanged, across an entire parent→child→grandchild chain for the narrow,
// closed set of concerns that must stay keyed to the root regardless of how
// deep delegation goes: interrupt/cancel scoping (the role-B predicates),
// cancel pre-arm latch keys, and the session_id stamped onto session-scoped
// WS frames (FR-012).
//
// For a root turn, RoutingSessionID MUST equal that turn's own SessionID.
// For a delegated child, RoutingSessionID equals the ROOT's SessionID, which
// is why it must never be used to resolve store-backed state (transcript
// writes, meta reads) for anything other than the root itself — that
// resolution belongs to SessionID.
type RoutingSessionID string
