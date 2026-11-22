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
//
// CHECK R05 repair: the detector is proven on synthetic populations — a
// complete-table positive control (the earlier instrument demanded a positive
// count of REAL missing kinds, so a correct table failed it forever), a
// dropped-kind negative control naming the kind, and a dropped-path control;
// the turn-start path population is enumerated from an independent
// TurnStartPath registration (declaredTurnStartPaths), and its absence at RED
// is a loud BLOCKED — rows read back from ProvenanceModeRows are
// self-consistency, never completeness.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
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
		// Exact coverage (CHECK R05): a kind row for a kind outside the
		// declared population is extra state the enumeration cannot see.
		declared := map[OriginKind]bool{}
		for _, k := range kinds {
			declared[k] = true
		}
		for _, r := range rows {
			if r.TurnStartPath != "" {
				continue
			}
			require.True(t, declared[r.Origin],
				"exact coverage: kind row for %q is not a declared OriginKind", r.Origin)
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
		// Exact coverage (CHECK R05): a session-type row outside the declared
		// population is extra state the enumeration cannot see.
		declaredTypes := map[UnifiedSessionType]bool{}
		for _, st := range types {
			declaredTypes[st] = true
		}
		for _, r := range rows {
			if r.SessionType == "" {
				continue
			}
			require.True(t, declaredTypes[r.SessionType],
				"exact coverage: session-type row for %q is not a declared UnifiedSessionType", r.SessionType)
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
		// producer table). Structural checks run on whatever path rows exist;
		// COMPLETENESS needs a population that does not come from the table
		// itself (CHECK R05: rows derived from ProvenanceModeRows are
		// self-consistency, never coverage).
		seen := map[string]string{}
		pathRows := 0
		for _, r := range rows {
			if r.TurnStartPath == "" {
				continue
			}
			pathRows++
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
		registered, found := declaredTurnStartPaths(t)
		if !found {
			t.Fatalf("BLOCKED: the registered turn-start paths are not enumerable — no TurnStartPath "+
				"registration exists in pkg/session (S-83 enumerates the registered turn-start paths; "+
				"the classification table itself has %d rows and %d of them are path rows). GREEN exposes "+
				"the registration — a TurnStartPath named type with one constant per registered turn-start "+
				"path, mirroring OriginKind — and the same test author reconciles", len(rows), pathRows)
		}
		require.NotEmpty(t, registered, "instrument: the registration enumeration found the registered paths")
		for _, p := range missingPathRows(rows, registered) {
			t.Fatalf("BLOCKED: registered turn-start path %q has no provenance-mode row (S-83)", p)
		}
	})

	t.Run("instrument_detector_passes_a_complete_table", func(t *testing.T) {
		// CHECK R05: the detector must PASS a complete table — the earlier
		// instrument required the real table to stay broken (it counted real
		// declared kinds missing from ProvenanceModeRows and demanded a
		// positive count, which a correct implementation fails). The positive
		// control runs the shared detector over a complete synthetic table.
		rows, kinds, paths := syntheticDriftTable()
		require.Empty(t, missingKindRows(rows, kinds),
			"instrument: the kind detector passes a complete table")
		require.Empty(t, missingPathRows(rows, paths),
			"instrument: the path detector passes a complete path population")
	})

	t.Run("instrument_added_kind_without_row_is_caught", func(t *testing.T) {
		// Instrument check (spec S-83): the test-only added kind with no row
		// fires the detector. The negative control drops one synthetic row
		// and the detector names exactly that kind — proven on the detector,
		// never by scoring the real table's own missing rows (CHECK R05).
		rows, kinds, _ := syntheticDriftTable()
		var cut []ProvenanceModeRow
		for _, r := range rows {
			if r.Origin == "syn-task" && r.TurnStartPath == "" {
				continue
			}
			cut = append(cut, r)
		}
		require.Equal(t, []OriginKind{"syn-task"}, missingKindRows(cut, kinds),
			"instrument: a dropped kind row is detected, naming the kind")
		const probeKind = OriginKind("probe-added-kind")
		require.Equal(t, []OriginKind{probeKind},
			missingKindRows(syntheticDriftTableFullRows(), append([]OriginKind{}, probeKind)),
			"instrument: an added kind without a row is detected")
	})

	t.Run("instrument_missing_path_row_is_caught", func(t *testing.T) {
		// CHECK R05: a path population row dropped from the table fires the
		// path detector — the earlier path leaf only looped the table's own
		// rows and could pass a table with no path rows at all.
		rows, _, paths := syntheticDriftTable()
		var cut []ProvenanceModeRow
		for _, r := range rows {
			if r.TurnStartPath == "syn-steer" {
				continue
			}
			cut = append(cut, r)
		}
		require.Equal(t, []string{"syn-steer"}, missingPathRows(cut, paths),
			"instrument: a dropped path row is detected, naming the path")
	})
}

// syntheticDriftTable builds a COMPLETE synthetic classification table over a
// small synthetic population — the instrument's positive control (CHECK R05).
func syntheticDriftTable() (rows []ProvenanceModeRow, kinds []OriginKind, paths []string) {
	kinds = []OriginKind{"syn-chat", "syn-task", "syn-channel"}
	for _, k := range kinds {
		mode := ProvenanceModeLineage
		if k == "syn-chat" || k == "syn-channel" {
			mode = ProvenanceModeRoot
		}
		rows = append(rows, ProvenanceModeRow{Origin: k, Mode: mode, Producer: "syn-producer"})
	}
	paths = []string{"syn-launch", "syn-steer", "syn-background"}
	for _, p := range paths {
		rows = append(rows, ProvenanceModeRow{Origin: "syn-task", Mode: ProvenanceModeLineage,
			Producer: "syn-producer", TurnStartPath: p})
	}
	return rows, kinds, paths
}

func syntheticDriftTableFullRows() []ProvenanceModeRow {
	rows, _, _ := syntheticDriftTable()
	return rows
}

// missingKindRows is the shared kind detector: the population kinds with no
// exactly-one kind row (path rows excluded) in the table. The enumeration
// leaves and the instruments both go through it (CHECK R05: one detector,
// proven on synthetic populations, never scored on the real table alone).
func missingKindRows(rows []ProvenanceModeRow, kinds []OriginKind) []OriginKind {
	count := map[OriginKind]int{}
	for _, r := range rows {
		if r.TurnStartPath == "" {
			count[r.Origin]++
		}
	}
	var out []OriginKind
	for _, k := range kinds {
		if count[k] != 1 {
			out = append(out, k)
		}
	}
	return out
}

// missingPathRows is the shared path detector: the population paths without
// exactly one path row in the table.
func missingPathRows(rows []ProvenanceModeRow, paths []string) []string {
	count := map[string]int{}
	for _, r := range rows {
		if r.TurnStartPath != "" {
			count[r.TurnStartPath]++
		}
	}
	var out []string
	for _, p := range paths {
		if count[p] != 1 {
			out = append(out, p)
		}
	}
	return out
}

// declaredTurnStartPaths enumerates the registered turn-start paths
// INDEPENDENTLY of the classification table (CHECK R05): a `TurnStartPath`
// named string type with one constant per registered path — the same
// source-of-truth technique the origin kinds use on lifecycle_edge.go.
// found=false means no such registration exists (RED): the table's own rows
// are then self-consistency only, never completeness.
func declaredTurnStartPaths(t *testing.T) (paths []string, found bool) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err, "parse the session package")
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
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
					if !ok || id.Name != "TurnStartPath" || len(vs.Values) != 1 {
						continue
					}
					lit, ok := vs.Values[0].(*ast.BasicLit)
					if !ok {
						continue
					}
					v, err := strconv.Unquote(lit.Value)
					require.NoError(t, err, "unquote %s", lit.Value)
					paths = append(paths, v)
				}
			}
		}
	}
	return paths, len(paths) > 0
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
