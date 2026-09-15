// Omnipus — ADR-067 FR-090: the knowledge audit vocabulary must be a KNOWN one.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestKnowledgeAuditEventNames_AreKnownToPkgAudit closes a gap this package's
// own audit.go flagged and left open.
//
// pkg/audit.IsValidEventName is the recognised-event vocabulary. An unknown
// name is not rejected — audit deliberately never drops a row over its name,
// because losing audit data is worse than logging a strange one — but it does
// trip the warn-once "unknown Event value (warn-once); please add to
// IsValidEventName or fix typo" path. Every knowledge.note.* row did exactly
// that, so the feature shipped with a permanently broken vocabulary and a
// misleading warning on every install that turned audit logging on.
//
// The names cannot be shared as constants in the other direction:
// pkg/knowledge imports pkg/audit, so pkg/audit referencing
// knowledge.EventKnowledgeNote* would be an import cycle. This test is the
// bridge — a rename here now fails a test instead of quietly reintroducing the
// warn-once.
//
// DIES ON: removing any "knowledge.note.*" entry from IsValidEventName's
// switch (pkg/audit/audit.go); renaming an EventKnowledgeNote* constant
// without updating that switch.
func TestKnowledgeAuditEventNames_AreKnownToPkgAudit(t *testing.T) {
	// Every name this package can put on the wire. AuthorOpEdit is included
	// deliberately: it has no EventKnowledgeNote* constant, and it is the one
	// most likely to be missed by an author reading only the constant block.
	for _, name := range []string{
		EventKnowledgeNoteCreate,
		EventKnowledgeNoteWrite,
		EventKnowledgeNoteRename,
		EventKnowledgeNoteDelete,
		string(AuthorOpCreate),
		string(AuthorOpEdit),
		string(knowledgeRenameOp),
	} {
		assert.Truef(t, audit.IsValidEventName(audit.EventName(name)),
			"%q is not in pkg/audit's IsValidEventName. Every knowledge audit row written "+
				"under this name trips audit's warn-once 'unknown Event value' path, so an "+
				"operator who enables audit logging sees a warning telling them their own "+
				"knowledge-base records look like a typo (ADR-067 FR-090, D19)", name)
	}

	// Anti-vacuity: a name that is genuinely not in the vocabulary must be
	// rejected, or the loop above would pass against an IsValidEventName that
	// returns true for everything.
	assert.False(t, audit.IsValidEventName(audit.EventName("knowledge.note.not_a_real_event")),
		"IsValidEventName must still reject an unknown name — otherwise this test proves nothing")
}
