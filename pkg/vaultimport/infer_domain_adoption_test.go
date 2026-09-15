// Omnipus — AdoptObservedDomains: a declaration nothing was observed for
// yields to one the data made.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// adoptionFixture builds a vault in which ONE record type observes a property
// and another carries the same property name with no value for it anywhere.
//
// The unobserved side is given a real note that omits the property rather than
// no notes at all, because those are different states and only this one
// exercises the rule: a type with no notes is FR-018d provisioned, and a type
// whose notes are silent about a property is the case the founder's
// `bank-account` is actually in.
func adoptionFixture(t *testing.T, observedValues []string) (map[string][]InferredProperty, []NoteRecord) {
	t.Helper()
	dir := t.TempDir()
	notes := make([]NoteRecord, 0, 2+len(observedValues))
	notes = append(notes,
		noteOnDisk(t, dir, "b1.md", "---\ntype: beta\nname: B1\n---\n\nbody\n"),
		noteOnDisk(t, dir, "b2.md", "---\ntype: beta\nname: B2\n---\n\nbody\n"),
	)
	for i, v := range observedValues {
		notes = append(notes, noteOnDisk(t, dir, fmt.Sprintf("a%d.md", i),
			fmt.Sprintf("---\ntype: alpha\nstage: %s\n---\n\nbody\n", v)))
	}
	groups := CollectTypeGroups(notes)
	names := BuildNameIndex(notes)
	inferred := map[string][]InferredProperty{}
	for typeName, g := range groups {
		inferred[typeName] = InferSchema(g, names)
	}
	// `beta` does not carry `stage` in any note; a template donated the name,
	// which is what leaves it standing on the text fallback.
	inferred["beta"] = append(inferred["beta"], InferredProperty{
		Name: "stage", Type: records.TypeText,
	})
	return inferred, notes
}
