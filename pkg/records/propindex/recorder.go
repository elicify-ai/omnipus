// Omnipus — ADR-068 D16.6 / spec AC-8.10: the query-boundary recorder.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package propindex

import "sync"

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// Ruling R-A — "SQLite narrows candidates, our own comparator decides" — is
// worth exactly as much as the thing that can detect a violation of it. Review
// round 6 found SEVEN surviving SQL-side evaluations in the revision whose
// headline was that ruling, and nothing in the document would have caught an
// eighth.
//
// So the recorder sits at the STORE BOUNDARY, not inside a query compiler. It
// survives the compiler's deletion and it cannot be satisfied by a comparator
// that is simply bypassed: every statement this package hands to the driver
// passes through sqlgate.go, and a source-level test fails the build if a second
// path to the driver ever appears.
// ---------------------------------------------------------------------------

// Phase says what the store was doing when it emitted a statement.
//
// The distinction is not decoration. AC-8.10's subject is the READ path — the
// place a comparison would decide an answer. Opening the database emits DDL and
// PRAGMAs, and writing a note emits row-identity predicates on a primary key;
// neither decides a filter, and neither is exempt from review, but conflating
// the three would make the control either unenforceable or meaningless.
type Phase string

const (
	// PhaseOpen is schema creation and connection setup.
	PhaseOpen Phase = "open"
	// PhaseWrite is the indexing path.
	PhaseWrite Phase = "write"
	// PhaseRead is the query path — AC-8.10's subject.
	PhaseRead Phase = "read"
)

// Statement is one SQL statement as the store handed it to the driver.
type Statement struct {
	Phase Phase
	SQL   string
}

// Recorder captures statements. It is safe for concurrent use because the store
// is, and a recorder that needed the caller to be careful would be a recorder
// that stops being attached in exactly the tests that matter.
type Recorder struct {
	mu    sync.Mutex
	stmts []Statement
}

// NewRecorder returns an empty recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// The write half — record — lives in sqlgate.go, beside the only code that may
// call it. The type is here, untagged, because Options carries it on BOTH halves
// of the platform gate; the method is not, because on a build with no store
// there is nothing to record and an unused writer would be exactly the dead code
// this package refuses to carry.

// Statements returns everything captured so far, in order.
func (r *Recorder) Statements() []Statement {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Statement, len(r.stmts))
	copy(out, r.stmts)
	return out
}

// InPhase returns the captured statements of one phase.
func (r *Recorder) InPhase(p Phase) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.stmts {
		if s.Phase == p {
			out = append(out, s.SQL)
		}
	}
	return out
}

// Reset drops everything captured.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stmts = nil
}
