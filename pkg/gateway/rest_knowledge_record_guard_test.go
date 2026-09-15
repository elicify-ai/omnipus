// Omnipus — the gateway's half of the shared record-write guard contract.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestGuardRefusalReasonsCoverEveryCode is the drift pin for the move of the
// write guards into pkg/records.
//
// WHAT IT PREVENTS. The guards now return a records.WriteRefusalCode, and this
// door translates that into the audit reason token an operator greps. A code
// added in pkg/records with no entry here would fall through
// recordWriteRefusalFromGuard's fallback and be audited as `invalid_value` —
// a truthful HTTP answer to the caller, but a silent lie in the audit log,
// where a whole population of refusals would file itself under the wrong
// reason with nothing anywhere reporting a problem. Walking the exported
// WriteRefusalCodes list rather than a transcription of it is what makes this
// test notice; a hand-copied list here would drift exactly as the map does.
func TestGuardRefusalReasonsCoverEveryCode(t *testing.T) {
	if len(records.WriteRefusalCodes) == 0 {
		t.Fatal("records.WriteRefusalCodes is empty — this test would pass vacuously")
	}
	for _, code := range records.WriteRefusalCodes {
		reason, ok := guardRefusalReasons[code]
		if !ok {
			t.Errorf("records.WriteRefusalCode %q has no audit reason in guardRefusalReasons; "+
				"add it there, or refusals carrying it are audited under the wrong reason", code)
			continue
		}
		if reason == "" {
			t.Errorf("code %q maps to an empty audit reason", code)
		}
	}
	if len(guardRefusalReasons) != len(records.WriteRefusalCodes) {
		t.Errorf("guardRefusalReasons has %d entries for %d codes — a stale entry names a code "+
			"pkg/records no longer returns", len(guardRefusalReasons), len(records.WriteRefusalCodes))
	}
}

// TestRecordWriteRefusalFromGuard_PreservesReasonAndMessage proves the
// adapter carries the guard's own words through rather than re-deriving them.
//
// The MESSAGE matters as much as the status: it is the only thing that tells
// a caller — an agent especially, which has no form in front of it — what to
// send instead. An adapter that answered a generic "bad request" would keep
// every status code correct and make every refusal useless.
func TestRecordWriteRefusalFromGuard_PreservesReasonAndMessage(t *testing.T) {
	for _, code := range records.WriteRefusalCodes {
		refusal := recordWriteRefusalFromGuard(&records.WriteRefusal{
			Code:     code,
			Property: "status",
			Message:  "the guard's own sentence",
		})
		if refusal.status != http.StatusBadRequest {
			t.Errorf("code %q: every guard refusal is a 400 (only the caller can fix it); got %d",
				code, refusal.status)
		}
		if refusal.reason != guardRefusalReasons[code] {
			t.Errorf("code %q: audit reason must be the mapped token; got %q", code, refusal.reason)
		}
		if refusal.message != "the guard's own sentence" {
			t.Errorf("code %q: the guard's message must reach the caller unaltered; got %q",
				code, refusal.message)
		}
	}
}

// TestBuildRecordPropertyEdits_MapsEachWriteKindToItsPrimitive pins the half
// of the old function that STAYED in the gateway: turning a validated verdict
// into the right knowledge.NoteEdit.
//
// It asserts against the BYTES the edits produce, not against which
// constructor was called, because that is where the distinction is real: a
// clear must leave the key ABSENT, not present holding an empty string. Those
// two are different records — one has no value for the property, the other
// has the empty value — and only the first is what D3.2's explicit clear
// means.
func TestBuildRecordPropertyEdits_MapsEachWriteKindToItsPrimitive(t *testing.T) {
	sc := &records.Schema{
		SchemaVersion: 1,
		Type:          "widget",
		Properties: map[string]*records.Property{
			"name": {Name: "name", Type: records.TypeText, RecordType: "widget"},
			"tags": {Name: "tags", Type: records.TypeText, RecordType: "widget", Many: true},
			"note": {Name: "note", Type: records.TypeText, RecordType: "widget"},
		},
		PropertyOrder: []string{"name", "tags", "note"},
	}
	text := func(s string) gen.RecordValue { return gen.RecordValue{Text: &s} }

	edits, refusal := buildRecordPropertyEdits(sc, []gen.RecordPropertyValue{
		{Property: "name", Values: []gen.RecordValue{text("Widget One")}},
		{Property: "tags", Values: []gen.RecordValue{text("alpha"), text("beta")}},
		{Property: "note", Values: []gen.RecordValue{}},
	}, nil)
	if refusal != nil {
		t.Fatalf("a conforming write must be allowed; got %q", refusal.message)
	}
	if len(edits) != 3 {
		t.Fatalf("want one edit per property, got %d", len(edits))
	}

	// Seed the note with the property the third entry CLEARS, so "absent
	// afterwards" is a real removal rather than a key that was never written.
	content := []byte("---\nnote: something\n---\n")
	var err error
	for i, edit := range edits {
		content, err = edit(content)
		if err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
	}
	got := string(content)
	for _, want := range []string{"name: Widget One", "tags:", "- alpha", "- beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("frontmatter must contain %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "note:") {
		t.Errorf("a cleared property must be ABSENT, not present-and-empty; got:\n%s", got)
	}
}
