// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// audit.go — the record of every change Omnipus makes to an operator's own
// notes, and of every change it refused to make (FR-090, ADR-067 D19, US-15).
//
// # Why this file exists at all
//
// Everything else Omnipus writes goes into $OMNIPUS_HOME, a directory Omnipus
// owns. A knowledge base does not: it is the operator's real folder, opened in
// Obsidian, synchronised by Syncthing, tracked in git, and edited by hand. An
// agent writing there is writing into someone else's workspace, unattended.
//
// The Library's own mutations already route through the gateway's
// logLibraryAudit. The knowledge tools bypass that path entirely, and D19 is
// explicit that they "must not be exempt". So: no mutation of a knowledge base
// happens without a record, and — this is the half that is usually forgotten —
// no REFUSAL happens without one either.
//
// # A refusal that leaves no trace is how a silent failure hides
//
// The security-relevant event is not "an agent wrote a note". It is "an agent
// tried to write a note over a change it had not seen". That is the event that
// tells an operator their collection has two writers who disagree, and it is
// precisely the event a naive implementation drops, because nothing was
// written and there is apparently nothing to report. FR-090 says every
// mutation AND every refusal; MutationOutcome below has three values for that
// reason, and Record treats all three identically.
//
// # You cannot mutate a knowledge base anonymously
//
// US-15 AS-1 requires the record to name the agent. That is enforced
// structurally rather than by hoping callers fill the field in: an Actor with
// neither an agent nor a user is rejected by NewWriter, before any Writer
// exists, so there is no code path that reaches a file with no one to name.
// Validation here is the second line of that same rule, not the first.
//
// # What is deliberately NOT in a record
//
// Note CONTENT. A Mutation has no field for it, which is the only reliable
// way to keep an operator's private notes out of an audit log that is
// retained, rotated and read by whoever administers the machine. Details
// exists for small scalars — a reason, a token, a count — and every string
// value in it is truncated (auditDetailValueMax) so that a careless caller
// stuffing a paragraph in cannot turn the audit log into a copy of the vault.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// Audit event names for knowledge-base mutations.
//
// They are dotted and namespaced to match the "library.*" events the gateway
// already emits for Library mutations, so an operator reading an audit file
// sees one consistent vocabulary rather than two.
//
// NOTE for whoever wires these into the gateway: pkg/audit's IsValidEventName
// does not yet know them, so today each one triggers audit's warn-once
// "unknown event name" log. The entry is still written in full — audit
// deliberately never rejects a row over its name — but the names belong in
// pkg/audit/events.go's switch. That file is outside this package's ownership;
// see this unit's report.
const (
	// EventKnowledgeNoteWrite is a write to an existing note.
	EventKnowledgeNoteWrite = "knowledge.note.write"
	// EventKnowledgeNoteCreate is the creation of a note that did not exist.
	EventKnowledgeNoteCreate = "knowledge.note.create"
	// EventKnowledgeNoteRename is a rename or move, including the inbound-link
	// rewrites it drags along (US-15 AS-2 — every touched path, not just the
	// renamed note).
	EventKnowledgeNoteRename = "knowledge.note.rename"
	// EventKnowledgeNoteDelete is the removal of a note.
	EventKnowledgeNoteDelete = "knowledge.note.delete"
)

// MutationOutcome is what actually happened to the operator's files.
//
// Three values, not two, because "refused" and "failed" are different events
// for the reader of an audit log: a refusal means Omnipus declined on purpose
// and the collection is intact; a failure means something broke and the state
// on disk needs looking at.
type MutationOutcome string

const (
	// MutationApplied — the mutation reached the operator's disk.
	MutationApplied MutationOutcome = "applied"
	// MutationRefused — Omnipus declined deliberately (a stale version token, a
	// path outside the collection, a lock it could not take in time). Nothing
	// was written. This is the FR-090 half that is easy to forget.
	MutationRefused MutationOutcome = "refused"
	// MutationFailed — the mutation was attempted and errored. Whether anything
	// reached disk is exactly what the reader needs to check.
	MutationFailed MutationOutcome = "failed"
)

// valid reports whether o is one of the three defined outcomes. An unknown
// outcome is a programming error and is refused rather than mapped to a
// default, because "unknown outcome" silently recorded as "allow" is worse
// than no record.
func (o MutationOutcome) valid() bool {
	switch o {
	case MutationApplied, MutationRefused, MutationFailed:
		return true
	}
	return false
}

// decision maps a MutationOutcome onto pkg/audit's three Decision values, so
// existing audit queries and dashboards that filter on decision keep working
// unchanged.
func (o MutationOutcome) decision() string {
	switch o {
	case MutationApplied:
		return audit.DecisionAllow
	case MutationRefused:
		return audit.DecisionDeny
	default:
		return audit.DecisionError
	}
}

// auditDetailValueMax bounds any string value a caller puts in Mutation.Details.
// It is a leak bound, not a formatting nicety: the audit log must not become a
// second copy of the operator's notes.
const auditDetailValueMax = 256

// auditReasonMax bounds Mutation.Reason for the same reason.
const auditReasonMax = 256

// Detail keys Record writes itself. A caller cannot supply them — see
// sanitizeDetails — so no caller can overwrite the operation, the actor, or
// the outcome after the fact.
const (
	detailKeyCollection = "collection"
	detailKeyPaths      = "paths"
	detailKeyPathCount  = "path_count"
	detailKeyOutcome    = "outcome"
	detailKeyReason     = "reason"
	detailKeyActor      = "actor"
	detailKeyWorkspace  = "workspace_id"
)

// reservedDetailKeys are the keys Record owns. Anything a caller supplies under
// one of these names is dropped.
//
//nolint:gochecknoglobals // a fixed lookup table, never mutated after init.
var reservedDetailKeys = map[string]struct{}{
	detailKeyCollection: {},
	detailKeyPaths:      {},
	detailKeyPathCount:  {},
	detailKeyOutcome:    {},
	detailKeyReason:     {},
	detailKeyActor:      {},
	detailKeyWorkspace:  {},
}

// Errors from the audit layer.
var (
	// ErrAuditUnavailable means a mutating knowledge-base component was
	// constructed without an audit sink. It is returned at CONSTRUCTION, not at
	// write time, so there is no way to end up holding a writer that cannot
	// satisfy FR-090.
	ErrAuditUnavailable = errors.New("knowledge: an audit sink is required for knowledge-base mutations")

	// ErrAuditIncomplete means a record was missing something US-15 AS-1
	// requires it to name — the operation, the collection, the paths, the
	// actor, or a recognised outcome.
	ErrAuditIncomplete = errors.New("knowledge: audit record is missing a required field")

	// ErrAuditWriteFailed wraps a sink failure. Callers join it onto the
	// operation's own error rather than discarding it: an unaudited mutation is
	// a fact the caller has to be told about, even when the mutation itself
	// succeeded.
	ErrAuditWriteFailed = errors.New("knowledge: audit write failed")
)

// AuditSink is the narrow slice of *audit.Logger this package needs.
//
// An interface rather than the concrete logger for one reason that matters:
// the tests for FR-090 have to assert on what was recorded, and a test that
// can only assert "the logger did not return an error" proves nothing about
// whether the agent, the collection or the paths were actually in the row.
type AuditSink interface {
	Log(entry *audit.Entry) error
}

// Actor is who is making the change. At least one of AgentID and User must be
// set — see NewWriter, which refuses an empty Actor outright.
//
// Both may be set at once and often are: an agent acting inside a turn the
// operator started has an agent id AND the gateway principal that started it.
type Actor struct {
	// AgentID is the agent making the change ("mia", a custom agent's id).
	AgentID string
	// User is the authenticated gateway principal, matching audit.Entry.User.
	User string
	// SessionID is the session the change belongs to, when there is one.
	SessionID string
}

// named reports whether this actor can be named in an audit record at all.
func (a Actor) named() bool {
	return strings.TrimSpace(a.AgentID) != "" || strings.TrimSpace(a.User) != ""
}

// name is the single best display string for the actor, preferring the agent.
func (a Actor) name() string {
	if s := strings.TrimSpace(a.AgentID); s != "" {
		return s
	}
	return strings.TrimSpace(a.User)
}

// Mutation is one knowledge-base change — applied, refused or failed.
//
// There is no Content field, and there must never be one: see this file's
// header.
type Mutation struct {
	// Operation is one of the EventKnowledge* names.
	Operation string
	// Outcome is what happened. All three are recorded (FR-090).
	Outcome MutationOutcome
	// Actor is who did it (US-15 AS-1).
	Actor Actor
	// CollectionRoot is the knowledge base's real path (US-15 AS-1).
	CollectionRoot string
	// WorkspaceID is the workspace the change was made from, when known.
	WorkspaceID string
	// Paths is EVERY collection-relative path the operation touched or would
	// have touched — US-15 AS-2 is explicit that a rename records the whole
	// rewrite set, not just the renamed note. Recorded deduplicated and sorted
	// so the record is stable and comparable between runs.
	Paths []string
	// Reason is a short machine-ish token explaining a refusal or failure
	// ("version_conflict", "lock_timeout", "outside_collection").
	Reason string
	// Details carries small scalars. Reserved keys are dropped and string
	// values are truncated; note content does not belong here.
	Details map[string]any
}

// Auditor writes knowledge-base mutation records.
type Auditor struct {
	sink AuditSink
}

// NewAuditor returns an Auditor over sink.
//
// A nil sink is an error rather than a silent no-op auditor. The no-op version
// is the tempting one — it keeps a half-configured gateway booting — and it is
// exactly the shape that lets FR-090 be violated for months without a symptom.
func NewAuditor(sink AuditSink) (*Auditor, error) {
	if sink == nil {
		return nil, ErrAuditUnavailable
	}
	return &Auditor{sink: sink}, nil
}

// Record writes one mutation record.
//
// It validates first, and an incomplete record is refused rather than written
// in a degraded form: a row that does not name the agent or the paths does not
// satisfy US-15 AS-1, and writing it anyway would make the audit log look
// complete while answering none of the questions it exists to answer.
//
// A sink failure is returned wrapped in ErrAuditWriteFailed. Callers must not
// discard it.
func (a *Auditor) Record(m Mutation) error {
	if a == nil || a.sink == nil {
		return ErrAuditUnavailable
	}
	if err := m.validate(); err != nil {
		return err
	}

	paths := normalizeAuditPaths(m.Paths)
	details := sanitizeDetails(m.Details)
	details[detailKeyCollection] = m.CollectionRoot
	details[detailKeyPaths] = paths
	details[detailKeyPathCount] = len(paths)
	details[detailKeyOutcome] = string(m.Outcome)
	details[detailKeyActor] = m.Actor.name()
	if reason := truncateAuditValue(strings.TrimSpace(m.Reason), auditReasonMax); reason != "" {
		details[detailKeyReason] = reason
	}
	if ws := strings.TrimSpace(m.WorkspaceID); ws != "" {
		details[detailKeyWorkspace] = ws
	}

	entry := &audit.Entry{
		Event:     m.Operation,
		Decision:  m.Outcome.decision(),
		AgentID:   strings.TrimSpace(m.Actor.AgentID),
		SessionID: strings.TrimSpace(m.Actor.SessionID),
		User:      strings.TrimSpace(m.Actor.User),
		Details:   details,
	}
	if err := a.sink.Log(entry); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrAuditWriteFailed, m.Operation, err)
	}
	return nil
}

// validate enforces exactly the fields US-15 AS-1 names, plus a recognised
// outcome.
func (m Mutation) validate() error {
	switch {
	case strings.TrimSpace(m.Operation) == "":
		return fmt.Errorf("%w: operation", ErrAuditIncomplete)
	case !m.Outcome.valid():
		return fmt.Errorf("%w: outcome %q", ErrAuditIncomplete, string(m.Outcome))
	case !m.Actor.named():
		return fmt.Errorf("%w: actor", ErrAuditIncomplete)
	case strings.TrimSpace(m.CollectionRoot) == "":
		return fmt.Errorf("%w: collection", ErrAuditIncomplete)
	case len(normalizeAuditPaths(m.Paths)) == 0:
		return fmt.Errorf("%w: paths", ErrAuditIncomplete)
	}
	return nil
}

// normalizeAuditPaths trims, drops empties, deduplicates and sorts.
//
// Sorted and deduplicated because a rename's rewrite set arrives in whatever
// order the link graph produced it, and an audit record whose path list
// reorders between runs cannot be diffed against another run — which is the
// main thing anyone does with these records after an incident.
func normalizeAuditPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = truncateAuditValue(p, auditDetailValueMax)
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// sanitizeDetails copies caller details, dropping reserved keys and truncating
// string values.
func sanitizeDetails(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+len(reservedDetailKeys))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if _, reserved := reservedDetailKeys[key]; reserved {
			continue
		}
		if s, ok := v.(string); ok {
			out[key] = truncateAuditValue(s, auditDetailValueMax)
			continue
		}
		out[key] = v
	}
	return out
}

// truncateAuditValue clips s to max BYTES and marks that it was clipped, so a
// reader is never shown a truncated value that looks whole.
func truncateAuditValue(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	const marker = "…[truncated]"
	if maxLen <= len(marker) {
		return s[:maxLen]
	}
	// Trim back to a rune boundary so the result stays valid UTF-8.
	cut := maxLen - len(marker)
	for cut > 0 && !isUTF8Start(s[cut]) {
		cut--
	}
	return s[:cut] + marker
}

// isUTF8Start reports whether b begins a UTF-8 rune (i.e. is not a
// continuation byte 10xxxxxx).
func isUTF8Start(b byte) bool { return b&0xC0 != 0x80 }
