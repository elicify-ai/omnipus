// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-83 (TestProgramProvenance_DriftTable — every origin
// kind and session type has exactly one provenance-mode row), FR-050
// (root versus lineage), FR-045 (the guard fails when a kind has no row).
//
// The oracle is the SPEC's classification table (rows 1–7 of "Classification
// (R5-M1)"): root ONLY for the identified standing conversations (origin
// kinds chat, channel, main); every work kind, every parentless work session
// and every unknown kind is lineage. The test enumerates the declared enums
// by parsing the const blocks (the same technique as
// origin_kind_session_type_test.go::declaredSessionTypes), so a kind added
// later without a row fails the guard (the instrument check).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// specOriginMode is the SPEC's mode per origin kind (FR-050 classification
// table, rows 2–7): root only for chat, channel and main; every work kind and
// every other kind (for example human) is lineage.
var specOriginMode = map[OriginKind]ProvenanceMode{
	OriginKindDelegate:  ProvenanceModeLineage, // row 2: ParentSessionID set, or origin kind delegate
	OriginKindTask:      ProvenanceModeLineage, // row 5: work kind — the task record at creation
	OriginKindChat:      ProvenanceModeRoot,    // row 6: identified standing conversation
	OriginKindChannel:   ProvenanceModeRoot,    // row 6: per-person channel conversation
	OriginKindMain:      ProvenanceModeRoot,    // row 6: an agent's main chat
	OriginKindScheduled: ProvenanceModeLineage, // row 5: the schedule record at creation
	OriginKindHeartbeat: ProvenanceModeLineage, // row 5: {independent} at trigger
	OriginKindVerifier:  ProvenanceModeLineage, // row 5: the verified work's provenance
	OriginKindPlan:      ProvenanceModeLineage, // row 4: the plan engine owns it
	OriginKindHuman:     ProvenanceModeLineage, // row 7: any other kind is lineage (fail closed)
}

// specTypeMode is the SPEC's mode per UnifiedSessionType (the same table —
// plan and human have no session type of their own).
var specTypeMode = map[UnifiedSessionType]ProvenanceMode{
	SessionTypeChat:      ProvenanceModeRoot,
	SessionTypeChannel:   ProvenanceModeRoot,
	SessionTypeMain:      ProvenanceModeRoot,
	SessionTypeTask:      ProvenanceModeLineage,
	SessionTypeScheduled: ProvenanceModeLineage,
	SessionTypeHeartbeat: ProvenanceModeLineage,
	SessionTypeVerifier:  ProvenanceModeLineage,
	SessionTypeDelegate:  ProvenanceModeLineage,
}

func TestProgramProvenance_DriftTable(t *testing.T) {
	rows := ProvenanceModeRows()

	t.Run("every_origin_kind_has_exactly_one_row", func(t *testing.T) {
		kinds := declaredOriginKindsForDrift(t)
		require.GreaterOrEqual(t, len(kinds), 10, "instrument check: the ten known origin kinds must be found")
		byOrigin := map[OriginKind][]ProvenanceModeRow{}
		for _, r := range rows {
			byOrigin[r.Origin] = append(byOrigin[r.Origin], r)
		}
		for _, k := range kinds {
			rowsForKind, ok := byOrigin[k]
			if !ok {
				t.Fatalf("BLOCKED: no provenance-mode row for origin kind %q — the FR-050/S-83 "+
					"classification table is not implemented (ProvenanceModeRows returned %d rows)",
					k, len(rows))
			}
			require.Len(t, rowsForKind, 1, "origin kind %q must have exactly one row (S-83)", k)
			want, specOK := specOriginMode[k]
			require.True(t, specOK, "instrument: %q missing from the spec oracle table itself", k)
			require.Equal(t, want, rowsForKind[0].Mode,
				"origin kind %q: mode must match the FR-050 classification table", k)
			if rowsForKind[0].Mode == ProvenanceModeLineage {
				require.NotEmpty(t, rowsForKind[0].Producer,
					"origin kind %q is lineage: its stored provenance must name a producer", k)
			}
		}
	})

	t.Run("every_session_type_has_exactly_one_row", func(t *testing.T) {
		types := declaredSessionTypes(t)
		require.GreaterOrEqual(t, len(types), 8, "instrument check: the eight known session types must be found")
		byType := map[UnifiedSessionType][]ProvenanceModeRow{}
		for _, r := range rows {
			if r.SessionType == "" {
				continue
			}
			byType[r.SessionType] = append(byType[r.SessionType], r)
		}
		for _, st := range types {
			rowsForType, ok := byType[st]
			if !ok {
				t.Fatalf("BLOCKED: no provenance-mode row for session type %q — the FR-050/S-83 "+
					"classification table is not implemented (ProvenanceModeRows returned %d rows)",
					st, len(rows))
			}
			require.Len(t, rowsForType, 1, "session type %q must have exactly one row (S-83)", st)
			want, specOK := specTypeMode[st]
			require.True(t, specOK, "instrument: %q missing from the spec oracle table itself", st)
			require.Equal(t, want, rowsForType[0].Mode,
				"session type %q: mode must match the FR-050 classification table", st)
		}
	})

	t.Run("root_is_only_for_standing_conversations", func(t *testing.T) {
		// Spec FR-050: "root only for chat, channel and main". Any other
		// root row fails the guard. An empty table never passes trivially
		// (CHECK F11): it BLOCKs before the loop.
		require.NotEmpty(t, rows,
			"BLOCKED: ProvenanceModeRows returned no rows — the FR-050/S-83 "+
				"classification table is not implemented")
		for _, r := range rows {
			if r.Mode == ProvenanceModeRoot {
				require.Contains(t, []OriginKind{OriginKindChat, OriginKindChannel, OriginKindMain},
					r.Origin, "root must be limited to chat, channel and main; got %q", r.Origin)
			}
		}
	})

	t.Run("every_registered_turn_start_path_has_exactly_one_row", func(t *testing.T) {
		// Spec S-83: the registered turn-start paths each have exactly one
		// row too (the launch, steering and background-callback paths of the
		// producer table). The path names are GREEN's registration (recorded
		// in test-disputes.md section G); what the guard pins is the
		// structure: one row per path, every path row lineage with its
		// producer named, and no path row colliding with a kind row.
		require.NotEmpty(t, rows,
			"BLOCKED: ProvenanceModeRows returned no rows — the FR-050/S-83 "+
				"classification table is not implemented (no turn-start path can have a row)")
		seen := map[string]string{}
		for _, r := range rows {
			if r.TurnStartPath == "" {
				continue
			}
			if prev, dup := seen[r.TurnStartPath]; dup {
				t.Errorf("turn-start path %q has two rows (%s and %s) — exactly one row per path (S-83)",
					r.TurnStartPath, prev, r.Origin)
				continue
			}
			seen[r.TurnStartPath] = string(r.Origin)
			require.Equal(t, ProvenanceModeLineage, r.Mode,
				"turn-start path %q is fed work: it is lineage (FR-050)", r.TurnStartPath)
			require.NotEmpty(t, r.Producer,
				"turn-start path %q must name where its stored provenance comes from", r.TurnStartPath)
		}
	})

	t.Run("instrument_added_kind_without_row_is_caught", func(t *testing.T) {
		// Instrument check (spec S-83): a test-only added kind with no row
		// makes the detection fire — the same lookup the enumeration above
		// fatals on.
		rows := ProvenanceModeRows()
		const probeKind = OriginKind("probe-added-kind")
		_, ok := driftRowForOrigin(rows, probeKind)
		require.False(t, ok, "instrument: an added kind without a row is detected")
		// Positive control: the enumeration's detector fires for a DECLARED
		// kind too when the table lacks it (at RED: all of them).
		kinds := declaredOriginKindsForDrift(t)
		require.NotEmpty(t, kinds, "instrument: the origin-kind enumeration found the declared kinds")
		detected := 0
		for _, k := range kinds {
			if _, ok := driftRowForOrigin(rows, k); !ok {
				detected++
			}
		}
		require.Positive(t, detected,
			"instrument: the detector fires on declared kinds missing from the table (RED: all)")
	})
}

// driftRowForOrigin is the single row lookup the enumeration fatals on and
// the instrument probes.
func driftRowForOrigin(rows []ProvenanceModeRow, k OriginKind) (ProvenanceModeRow, bool) {
	for _, r := range rows {
		if r.Origin == k {
			return r, true
		}
	}
	return ProvenanceModeRow{}, false
}

// declaredOriginKindsForDrift enumerates the OriginKind constants by parsing
// lifecycle_edge.go (the same source-of-truth technique the existing
// TestEverySessionTypeMapsToAValidOriginKind guard uses for session types).
func declaredOriginKindsForDrift(t *testing.T) []OriginKind {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "lifecycle_edge.go", nil, 0)
	require.NoError(t, err, "parse lifecycle_edge.go")
	var out []OriginKind
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "OriginKind" || len(vs.Values) != 1 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok {
				continue
			}
			v, err := strconv.Unquote(lit.Value)
			require.NoError(t, err, "unquote %s", lit.Value)
			out = append(out, OriginKind(v))
		}
	}
	return out
}
