// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_sessions_effort_wire_exclusion_test.go — RED test for WP-H's backend
// half (thinking-reasoning-spec.md §1 C5 non-web row: "unifiedMetaToGenSession
// deliberately does not map it, so it is not a new gateway/SPA wire field";
// §2.2 SessionMeta row, D30): the conversation's stored effort stays
// server-internal. The gen.Session wire type carries NO reasoning_effort
// field at all, and the REST serializer never hand-adds one.
//
// RED status at time of writing: SessionMeta.ReasoningEffort does not exist
// yet, so this file does not compile — that compile failure naming the field
// is the RED evidence. Once the field lands, the assertions below are the
// standing guard that it never leaks to the wire.
package gateway

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestUnifiedMetaToGenSession_NeverEmitsReasoningEffort (C5 non-web row +
// D30) — a meta carrying a stored effort serializes with no
// reasoning_effort key anywhere in the wire output.
func TestUnifiedMetaToGenSession_NeverEmitsReasoningEffort(t *testing.T) {
	meta := &session.UnifiedMeta{
		SessionMeta: session.SessionMeta{
			ID:              "effort-wire-probe",
			AgentID:         "agent-1",
			Channel:         "telegram",
			Model:           "gpt-4.1",
			ReasoningEffort: "high", // set deliberately: the guard's whole point
			Partitions:      []string{"2026-09-28.jsonl"},
		},
		Type: session.SessionTypeChannel,
	}

	out := unifiedMetaToGenSession(meta)
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal gen.Session: %v", err)
	}
	wire := string(data)

	// Instrument check: the marshal must have produced real output carrying
	// other known keys, or the absence below would prove nothing.
	for _, mustExist := range []string{`"id":"effort-wire-probe"`, `"model":"gpt-4.1"`} {
		if !strings.Contains(wire, mustExist) {
			t.Fatalf("wire output missing %s — the probe serialization is broken: %s", mustExist, wire)
		}
	}

	if strings.Contains(wire, "reasoning_effort") {
		t.Fatalf("gen.Session wire output carries a reasoning_effort key: %s — "+
			"the stored effort must stay server-internal (C5/D30: not a gateway/SPA wire field)", wire)
	}
}

// TestGenSessionWireType_HasNoReasoningEffortField — the type-level
// absence: no field of gen.Session (its flat fields and the nested inline
// Stats struct) carries a reasoning_effort json tag. This is the proof the
// dispatch asks for: the wire type does not even have the field to map.
func TestGenSessionWireType_HasNoReasoningEffortField(t *testing.T) {
	rt := reflect.TypeOf(gen.Session{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag := f.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "reasoning_effort" {
			t.Fatalf("gen.Session.%s carries json tag %q — the wire type must not have this field (C5/D30)", f.Name, tag)
		}
	}

	// The one nested anonymous struct (Stats) gets the same walk.
	if statsField, ok := rt.FieldByName("Stats"); ok {
		st := statsField.Type
		if st.Kind() == reflect.Struct {
			for i := 0; i < st.NumField(); i++ {
				f := st.Field(i)
				tag := f.Tag.Get("json")
				name := strings.Split(tag, ",")[0]
				if name == "reasoning_effort" {
					t.Fatalf("gen.Session.Stats.%s carries json tag %q — the wire type must not have this field (C5/D30)", f.Name, tag)
				}
			}
		}
	}
}
