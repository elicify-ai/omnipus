// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// audit_test.go — FR-090 ("every mutation and every refusal"), US-15,
// ADR-067 D19, spec tests 50 and 51.
//
// The expected shape of a record comes from US-15 AS-1, which names exactly
// five things it must carry: the agent, the collection, the paths touched, the
// operation, and the outcome. Every assertion below traces to one of those, to
// AS-2 (a multi-file rewrite records the WHOLE set), or to AS-3 (a refusal is
// audited as a refusal, not omitted).

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// ---------------------------------------------------------------------------
// Recording sink
// ---------------------------------------------------------------------------

// recordingSink captures entries so a test can assert on their CONTENT.
//
// A sink that only counted calls, or only returned nil, would let every test in
// this file pass against an implementation that wrote an empty row — which is
// exactly the "green gate that verified nothing" this project has been bitten
// by before.
type recordingSink struct {
	mu       sync.Mutex
	entries  []audit.Entry
	failWith error
}

// Log implements AuditSink.
func (s *recordingSink) Log(entry *audit.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWith != nil {
		return s.failWith
	}
	s.entries = append(s.entries, *entry)
	return nil
}

// all returns a copy of everything recorded so far.
func (s *recordingSink) all() []audit.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]audit.Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// setFailure makes every subsequent Log return err.
func (s *recordingSink) setFailure(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failWith = err
}

// countWithReason counts entries whose recorded reason equals reason.
func (s *recordingSink) countWithReason(reason string) int {
	n := 0
	for _, e := range s.all() {
		if got, _ := e.Details[detailKeyReason].(string); got == reason {
			n++
		}
	}
	return n
}

// mustFind returns the single entry carrying reason, failing the test otherwise.
func (s *recordingSink) mustFind(t *testing.T, reason string) audit.Entry {
	t.Helper()
	var found []audit.Entry
	for _, e := range s.all() {
		if got, _ := e.Details[detailKeyReason].(string); got == reason {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("audit entries with reason %q = %d, want exactly 1. Recorded: %s", reason, len(found), summarize(s.all()))
	}
	return found[0]
}

// mustFindOperation returns the single entry for an operation and outcome.
func (s *recordingSink) mustFindOperation(t *testing.T, operation string, outcome MutationOutcome) audit.Entry {
	t.Helper()
	var found []audit.Entry
	for _, e := range s.all() {
		got, _ := e.Details[detailKeyOutcome].(string)
		if e.Event == operation && got == string(outcome) {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("audit entries for %s/%s = %d, want exactly 1. Recorded: %s", operation, outcome, len(found), summarize(s.all()))
	}
	return found[0]
}

// summarize renders recorded entries compactly for a failure message.
func summarize(entries []audit.Entry) string {
	if len(entries) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		outcome, _ := e.Details[detailKeyOutcome].(string)
		reason, _ := e.Details[detailKeyReason].(string)
		parts = append(parts, fmt.Sprintf("{event=%s decision=%s outcome=%s reason=%s paths=%v}",
			e.Event, e.Decision, outcome, reason, e.Details[detailKeyPaths]))
	}
	return strings.Join(parts, ", ")
}

// entryPaths returns the recorded path list.
func entryPaths(t *testing.T, e audit.Entry) []string {
	t.Helper()
	paths, ok := e.Details[detailKeyPaths].([]string)
	if !ok {
		t.Fatalf("audit entry has no %q detail (US-15 AS-1 requires the paths touched); details = %v", detailKeyPaths, e.Details)
	}
	return paths
}

// ---------------------------------------------------------------------------
// Spec test 50 — TestAudit_MutationAndRefusalRecorded (US-15 AS-1, AS-3)
// ---------------------------------------------------------------------------

func TestAudit_MutationAndRefusalRecorded(t *testing.T) {
	t.Parallel()

	const rel = "architecture/sandboxing.md"
	f := newWriteFixture(t, map[string]string{rel: "original\n"})
	start := f.version(rel)

	// --- an applied mutation ------------------------------------------------
	if _, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("the agent's edit\n"),
		ExpectedVersion: start.Token,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	applied := f.sink.mustFindOperation(t, EventKnowledgeNoteWrite, MutationApplied)
	// US-15 AS-1: the agent …
	if applied.AgentID != fixtureAgentID {
		t.Errorf("applied entry agent_id = %q, want %q", applied.AgentID, fixtureAgentID)
	}
	if applied.User != fixtureUser {
		t.Errorf("applied entry user = %q, want %q", applied.User, fixtureUser)
	}
	// … the collection …
	if got, _ := applied.Details[detailKeyCollection].(string); got != f.col.Root() {
		t.Errorf("applied entry collection = %q, want %q", got, f.col.Root())
	}
	// … the paths touched …
	if got := entryPaths(t, applied); len(got) != 1 || got[0] != rel {
		t.Errorf("applied entry paths = %v, want [%q]", got, rel)
	}
	// … the operation …
	if applied.Event != EventKnowledgeNoteWrite {
		t.Errorf("applied entry event = %q, want %q", applied.Event, EventKnowledgeNoteWrite)
	}
	// … and the outcome.
	if applied.Decision != audit.DecisionAllow {
		t.Errorf("applied entry decision = %q, want %q", applied.Decision, audit.DecisionAllow)
	}
	if got, _ := applied.Details[detailKeyWorkspace].(string); got != fixtureWorkspace {
		t.Errorf("applied entry workspace_id = %q, want %q", got, fixtureWorkspace)
	}

	// --- a refusal ----------------------------------------------------------
	// The agent still holds the pre-write token, so this write is stale.
	_, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("a second edit from a stale read\n"),
		ExpectedVersion: start.Token,
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("premise broken: the second write was not refused (%v); the refusal-audit assertions below would prove nothing", err)
	}

	refused := f.sink.mustFind(t, "version_conflict")
	if refused.Decision != audit.DecisionDeny {
		t.Errorf("refusal decision = %q, want %q — a refusal recorded as an allow is worse than no record", refused.Decision, audit.DecisionDeny)
	}
	if got, _ := refused.Details[detailKeyOutcome].(string); got != string(MutationRefused) {
		t.Errorf("refusal outcome = %q, want %q", got, MutationRefused)
	}
	if refused.AgentID != fixtureAgentID {
		t.Errorf("refusal entry agent_id = %q, want %q", refused.AgentID, fixtureAgentID)
	}
	if got := entryPaths(t, refused); len(got) != 1 || got[0] != rel {
		t.Errorf("refusal entry paths = %v, want [%q]", got, rel)
	}
	if got, _ := refused.Details["expected_version"].(string); got != string(start.Token) {
		t.Errorf("refusal expected_version = %q, want %q", got, start.Token)
	}
	if got, _ := refused.Details["actual_version"].(string); got == "" || got == string(start.Token) {
		t.Errorf("refusal actual_version = %q, want the token now on disk", got)
	}
}

// TestAudit_RefusalIsNeverOmitted is US-15 AS-3 stated as a counting property
// rather than a lookup: after N refusals and zero successful writes, there must
// be N records. A test that only asserts "at least one record exists" cannot
// tell a complete audit trail from a partial one.
func TestAudit_RefusalIsNeverOmitted(t *testing.T) {
	t.Parallel()

	const rel = "note.md"
	f := newWriteFixture(t, map[string]string{rel: "original\n"})
	start := f.version(rel)

	// Make every subsequent write stale.
	f.externalEdit(rel, "changed by the operator\n")

	const attempts = 5
	for i := range attempts {
		_, err := f.writer.WriteNote(WriteRequest{
			Path:            rel,
			Content:         []byte(fmt.Sprintf("attempt %d\n", i)),
			ExpectedVersion: start.Token,
		})
		if !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("attempt %d was not refused: %v", i, err)
		}
	}

	if got := f.sink.countWithReason("version_conflict"); got != attempts {
		t.Errorf("audited refusals = %d, want %d — FR-090 says EVERY refusal, and a refusal that leaves no trace is how a silent failure hides",
			got, attempts)
	}
}

// ---------------------------------------------------------------------------
// Spec test 51 — TestAudit_MultiFileRewriteRecordsAllPaths (US-15 AS-2)
// ---------------------------------------------------------------------------

func TestAudit_MultiFileRewriteRecordsAllPaths(t *testing.T) {
	t.Parallel()

	f := newWriteFixture(t, nil)

	// A rename drags every inbound link with it. US-15 AS-2 is explicit that
	// the record carries the full set of touched paths, "not just the renamed
	// note" — which is the shape a naive implementation gets wrong, because the
	// renamed note is the only path the caller thought about.
	touched := make([]string, 0, 22)
	touched = append(touched, "notes/new-name.md", "notes/old-name.md")
	for i := range 20 {
		touched = append(touched, fmt.Sprintf("linkers/note-%02d.md", i))
	}
	// Deliberately unsorted and containing a duplicate: the record must be
	// stable and deduplicated so two runs can be diffed after an incident.
	shuffled := append([]string{}, touched...)
	shuffled = append(shuffled, touched[0])
	for i, j := 0, len(shuffled)-1; i < j; i, j = i+1, j-1 {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	if err := f.writer.Audit(Mutation{
		Operation: EventKnowledgeNoteRename,
		Outcome:   MutationApplied,
		Paths:     shuffled,
		Reason:    "rename",
	}); err != nil {
		t.Fatalf("Audit: %v", err)
	}

	entry := f.sink.mustFindOperation(t, EventKnowledgeNoteRename, MutationApplied)
	got := entryPaths(t, entry)

	if len(got) != len(touched) {
		t.Fatalf("recorded %d paths, want %d (US-15 AS-2: the FULL set, deduplicated).\n got: %v\nwant: %v",
			len(got), len(touched), got, touched)
	}
	recorded := make(map[string]bool, len(got))
	for _, p := range got {
		recorded[p] = true
	}
	for _, want := range touched {
		if !recorded[want] {
			t.Errorf("path %q was touched but not recorded", want)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("recorded paths are not sorted at index %d (%q > %q); an unstable list cannot be diffed between runs",
				i, got[i-1], got[i])
			break
		}
	}
	if n, _ := entry.Details[detailKeyPathCount].(int); n != len(touched) {
		t.Errorf("path_count = %d, want %d", n, len(touched))
	}
}

// ---------------------------------------------------------------------------
// The record must be complete, or refused
// ---------------------------------------------------------------------------

func TestAudit_IncompleteRecordIsRefused(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	auditor, err := NewAuditor(sink)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}

	complete := Mutation{
		Operation:      EventKnowledgeNoteWrite,
		Outcome:        MutationApplied,
		Actor:          Actor{AgentID: "mia"},
		CollectionRoot: "/vault",
		Paths:          []string{"note.md"},
	}
	// Control: the complete record must be accepted, otherwise every negative
	// case below passes for the wrong reason.
	if err := auditor.Record(complete); err != nil {
		t.Fatalf("the complete control record was refused: %v", err)
	}

	cases := map[string]func(m *Mutation){
		"no operation": func(m *Mutation) { m.Operation = "" },
		"no outcome":   func(m *Mutation) { m.Outcome = "" },
		"bogus outcome": func(m *Mutation) {
			m.Outcome = MutationOutcome("succeeded-probably")
		},
		"no actor":      func(m *Mutation) { m.Actor = Actor{} },
		"no collection": func(m *Mutation) { m.CollectionRoot = "" },
		"no paths":      func(m *Mutation) { m.Paths = nil },
		"blank paths":   func(m *Mutation) { m.Paths = []string{"", "   "} },
	}
	for name, mutate := range cases {
		m := complete
		mutate(&m)
		if err := auditor.Record(m); !errors.Is(err, ErrAuditIncomplete) {
			t.Errorf("Record(%s) error = %v, want ErrAuditIncomplete — a record that does not answer US-15 AS-1's questions must not be written as if it did",
				name, err)
		}
	}

	if got := len(sink.all()); got != 1 {
		t.Errorf("sink received %d entries, want 1 (only the control); an incomplete record must be refused, not written in a degraded form", got)
	}
}

func TestAudit_NilSinkIsRefusedAtConstruction(t *testing.T) {
	t.Parallel()

	if _, err := NewAuditor(nil); !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("NewAuditor(nil) error = %v, want ErrAuditUnavailable. "+
			"A no-op auditor is the shape that lets FR-090 be violated for months with no symptom", err)
	}
}

// ---------------------------------------------------------------------------
// An audit failure is surfaced, never swallowed
// ---------------------------------------------------------------------------

func TestAudit_SinkFailureIsSurfacedOnBothOutcomes(t *testing.T) {
	t.Parallel()

	const rel = "note.md"
	f := newWriteFixture(t, map[string]string{rel: "original\n"})
	start := f.version(rel)

	sinkErr := errors.New("audit disk is full")
	f.sink.setFailure(sinkErr)

	// A SUCCESSFUL write whose audit failed must still tell the caller. An
	// unaudited mutation of the operator's real files is a fact the caller has
	// to be told about (FR-090); silently returning success is how the audit
	// trail acquires holes nobody notices.
	_, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("the agent's edit\n"),
		ExpectedVersion: start.Token,
	})
	if !errors.Is(err, ErrAuditWriteFailed) {
		t.Errorf("applied write with a failing audit sink returned %v, want an error wrapping ErrAuditWriteFailed", err)
	}

	// A REFUSED write whose audit failed must report both facts, and the
	// conflict must still be recognisable through the join — otherwise a
	// conflict-aware caller would stop recognising conflicts the moment the
	// audit log broke.
	_, err = f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte("stale edit\n"),
		ExpectedVersion: start.Token,
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Errorf("refused write error = %v, want it to still satisfy errors.Is(err, ErrVersionConflict)", err)
	}
	if !errors.Is(err, ErrAuditWriteFailed) {
		t.Errorf("refused write error = %v, want it to also satisfy errors.Is(err, ErrAuditWriteFailed)", err)
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Errorf("the typed conflict is not reachable through the joined error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The audit log must not become a copy of the vault
// ---------------------------------------------------------------------------

func TestAudit_CallerDetailsCannotOverwriteTheRecordsOwnFields(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	auditor, err := NewAuditor(sink)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}

	if err := auditor.Record(Mutation{
		Operation:      EventKnowledgeNoteWrite,
		Outcome:        MutationRefused,
		Actor:          Actor{AgentID: "mia"},
		CollectionRoot: "/vault",
		Paths:          []string{"note.md"},
		Reason:         "version_conflict",
		Details: map[string]any{
			detailKeyCollection: "/somewhere/else",
			detailKeyPaths:      []string{"nothing-happened.md"},
			detailKeyOutcome:    string(MutationApplied),
			detailKeyActor:      "somebody-else",
			"harmless":          "kept",
		},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	entries := sink.all()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if got, _ := e.Details[detailKeyCollection].(string); got != "/vault" {
		t.Errorf("collection = %q, want /vault — a caller must not be able to rewrite the record", got)
	}
	if got := entryPaths(t, e); len(got) != 1 || got[0] != "note.md" {
		t.Errorf("paths = %v, want [note.md]", got)
	}
	if got, _ := e.Details[detailKeyOutcome].(string); got != string(MutationRefused) {
		t.Errorf("outcome = %q, want %q — a refusal must not be recordable as an applied write", got, MutationRefused)
	}
	if got, _ := e.Details[detailKeyActor].(string); got != "mia" {
		t.Errorf("actor = %q, want mia", got)
	}
	if got, _ := e.Details["harmless"].(string); got != "kept" {
		t.Errorf("non-reserved detail was dropped: %v", e.Details)
	}
}

// TestAudit_CallerCannotForgeTheReasonOrTheWorkspace covers the two reserved
// keys the test above cannot reach — and it is the reason that test could be
// deleted from the guard's point of view.
//
// Record() reassigns collection, paths, path_count, outcome and actor
// UNCONDITIONALLY, two lines after sanitizeDetails runs. Those five are masked
// by the overwrite whether or not the reserved-key filter does anything, so a
// test that probes only them stays green with the filter neutralised — mutation
// confirmed: 435 pass, that test among them, with the filter switched off.
//
// reason and workspace_id are different: Record assigns them only inside
// `if reason != ""` / `if ws != ""`. When the caller supplies neither, the
// filter is the ONLY thing standing between an agent and a forged refusal
// reason or a forged workspace attribution in the operator's audit trail —
// which is the record US-15 exists to make trustworthy.
func TestAudit_CallerCannotForgeTheReasonOrTheWorkspace(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	auditor, err := NewAuditor(sink)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}

	// Reason and WorkspaceID deliberately EMPTY on the mutation, so Record's
	// own conditional assignment never fires and only the filter can refuse.
	if err := auditor.Record(Mutation{
		Operation:      EventKnowledgeNoteWrite,
		Outcome:        MutationRefused,
		Actor:          Actor{AgentID: "mia"},
		CollectionRoot: "/vault",
		Paths:          []string{"note.md"},
		Details: map[string]any{
			detailKeyReason:    "the operator approved this",
			detailKeyWorkspace: "some-other-workspace",
			"harmless":         "kept",
		},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	entries := sink.all()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if got, present := e.Details[detailKeyReason]; present {
		t.Errorf("reason = %v, want absent — a caller must not be able to write the recorded reason for its own refusal", got)
	}
	if got, present := e.Details[detailKeyWorkspace]; present {
		t.Errorf("workspace_id = %v, want absent — a caller must not be able to attribute its write to another workspace", got)
	}
	if got, _ := e.Details["harmless"].(string); got != "kept" {
		t.Errorf("non-reserved detail was dropped: %v", e.Details)
	}

	// Positive control: the record DOES carry a reason and a workspace when
	// the mutation names them, so the two absences above are the filter at
	// work rather than fields this Auditor never writes.
	if err := auditor.Record(Mutation{
		Operation:      EventKnowledgeNoteWrite,
		Outcome:        MutationRefused,
		Actor:          Actor{AgentID: "mia"},
		CollectionRoot: "/vault",
		Paths:          []string{"note.md"},
		Reason:         "version_conflict",
		WorkspaceID:    "ws-1",
	}); err != nil {
		t.Fatalf("Record (control): %v", err)
	}
	ctl := sink.all()[1]
	if got, _ := ctl.Details[detailKeyReason].(string); got != "version_conflict" {
		t.Fatalf("control reason = %q, want version_conflict — the assertions above prove nothing", got)
	}
	if got, _ := ctl.Details[detailKeyWorkspace].(string); got != "ws-1" {
		t.Fatalf("control workspace_id = %q, want ws-1 — the assertions above prove nothing", got)
	}
}

func TestAudit_LongDetailValuesAreTruncated(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	auditor, err := NewAuditor(sink)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}

	// Stand-in for a careless caller putting a paragraph of the operator's
	// private note into a detail field. The audit log is retained, rotated and
	// read by whoever administers the machine; it must not become a second copy
	// of the vault.
	secret := strings.Repeat("private note content. ", 200)
	if err := auditor.Record(Mutation{
		Operation:      EventKnowledgeNoteWrite,
		Outcome:        MutationApplied,
		Actor:          Actor{AgentID: "mia"},
		CollectionRoot: "/vault",
		Paths:          []string{"note.md"},
		Reason:         secret,
		Details:        map[string]any{"note": secret},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	e := sink.all()[0]
	got, _ := e.Details["note"].(string)
	if len(got) > auditDetailValueMax {
		t.Errorf("detail value length = %d, want <= %d", len(got), auditDetailValueMax)
	}
	if !strings.HasSuffix(got, "[truncated]") {
		t.Errorf("truncated value %q does not say it was truncated; a clipped value that looks whole misleads the reader", got)
	}
	reason, _ := e.Details[detailKeyReason].(string)
	if len(reason) > auditReasonMax {
		t.Errorf("reason length = %d, want <= %d", len(reason), auditReasonMax)
	}
}

func TestAudit_OutcomeMapsOntoAuditDecisions(t *testing.T) {
	t.Parallel()

	// The three outcomes must land on the three audit decisions, so existing
	// audit queries that filter on decision keep working. Mapping "refused"
	// onto allow, or "failed" onto allow, would make the log actively wrong.
	cases := []struct {
		outcome MutationOutcome
		want    string
	}{
		{MutationApplied, audit.DecisionAllow},
		{MutationRefused, audit.DecisionDeny},
		{MutationFailed, audit.DecisionError},
	}
	for _, tc := range cases {
		if got := tc.outcome.decision(); got != tc.want {
			t.Errorf("MutationOutcome(%q).decision() = %q, want %q", tc.outcome, got, tc.want)
		}
	}
}
