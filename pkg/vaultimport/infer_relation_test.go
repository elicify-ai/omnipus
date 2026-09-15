// Omnipus — FR-104a's unit suite: a relation's `to:` is inferred from what
// its links actually resolve to. Unanimity or a >=2/3 supermajority declares
// it; below that the importer says so and names the fix instead of guessing.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// nameIndexOf builds a link-target index directly: stem -> record type. An
// empty type means "a real note that is not a record", which is a distinct
// case from "no note with that title", and the two must not be conflated.
func nameIndexOf(byStem map[string]string) *NameIndex {
	idx := &NameIndex{byStem: map[string][]string{}}
	for stem, typ := range byStem {
		idx.byStem[records.FoldKey(stem)] = append(idx.byStem[records.FoldKey(stem)], typ)
	}
	return idx
}

// linksTo builds one property observation whose values are wikilinks to the
// named targets.
func linksTo(prop string, targets ...string) *PropertyObservation {
	po := &PropertyObservation{Name: prop}
	for i, tgt := range targets {
		po.Values = append(po.Values, observedValue{
			Text:     "[[" + tgt + "]]",
			NotePath: fmt.Sprintf("note-%d.md", i),
		})
	}
	return po
}

// mixedIndex names `contactN` notes as contacts and `taskN` notes as tasks.
func mixedIndex(contacts, tasks int) (*NameIndex, []string) {
	stems := map[string]string{}
	var targets []string
	for i := 0; i < contacts; i++ {
		s := fmt.Sprintf("contact%d", i)
		stems[s] = "contact"
		targets = append(targets, s)
	}
	for i := 0; i < tasks; i++ {
		s := fmt.Sprintf("task%d", i)
		stems[s] = "task"
		targets = append(targets, s)
	}
	return nameIndexOf(stems), targets
}
