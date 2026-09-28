// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package protocoltypes

import (
	"reflect"
	"testing"
)

// WP-C guard test for the live-progress carrier (spec section 16 item 24
// and section 5: reasoning text never travels on ToolCallProgress - "A count
// only: the reasoning text itself never travels through this type"; the D2
// ban on thinking reaching ToolCallProgress frames).
//
// Guard: expected GREEN in RED - the type is already counts-only. The test
// pins the shape so a future field that could carry reasoning text fails
// here first.

func TestToolCallProgress_CarriesCountsNeverText(t *testing.T) {
	tp := reflect.TypeOf(ToolCallProgress{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.Name == "Name" {
			continue // the tool name is the one string field
		}
		if f.Type.Kind() == reflect.String {
			t.Errorf("ToolCallProgress.%s is a string - the progress frame must never gain a text "+
				"field that could carry reasoning content (item 24: counts, never text)", f.Name)
		}
	}

	// The reasoning channel is a byte count, positively stated.
	rf, found := tp.FieldByName("ReasoningBytes")
	requireTrue(t, found, "ReasoningBytes must exist - the reasoning liveness signal")
	if rf.Type.Kind() != reflect.Int {
		t.Errorf("ReasoningBytes is %s, want int (a count, not text)", rf.Type.Kind())
	}
}

func requireTrue(t *testing.T, ok bool, msg string) {
	t.Helper()
	if !ok {
		t.Fatal(msg)
	}
}
