// Omnipus — FR-105's structural partition: WHERE a named loss came from,
// and therefore whether it can change which rows a view returns.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import "fmt"

// ---------------------------------------------------------------------------
// THE BROADENING PROHIBITION, MADE STRUCTURAL
//
// FR-105 is the one import rule that admits no exception: an imported view
// MUST NEVER return MORE rows than its original while looking correct. The
// standing example is Decisions.base — `type == "decision"` AND NOT
// inFolder("99-Temp") AND NOT inFolder("00-Inbox"). Drop the two folder
// clauses (which is exactly what this importer did before this file existed)
// and the view still runs, still looks right, and now silently includes
// every scratch note in the vault.
//
// The ORACLE is structural, not arithmetic. Nothing counts rows at import
// time. Instead every loss is emitted with its POSITION, positions are
// partitioned once — here — into row-set-affecting and annotation, and a
// view carrying any row-set-affecting loss is DISABLED. "Never more rows"
// then follows by induction over clauses rather than by measurement.
//
// WHY THIS IS A PARTITION AND NOT A LIST OF STRINGS TO GREP FOR. A flat
// "these substrings mean a filter" list cannot detect its own incompleteness:
// add a new loss position tomorrow, forget to list it, and it silently
// classifies as an annotation — the view stays ENABLED and broadens, which
// is the precise failure this requirement exists to stop, reintroduced by an
// omission nothing tests. So positions are a closed enum, the classification
// is a map over that enum, and TestLossPositions_ArePartitioned (loss_test.go)
// fails by name if a constant is added without classifying it, or classified
// without being a constant. That test is the guard; this comment is not.
// ---------------------------------------------------------------------------

// LossPosition names where in a `.base` file a named loss originated. It is
// rendered as the `[position]` prefix of every loss line, so the report a
// human reads and the classification the code makes are the same fact.
type LossPosition string

const (
	// LossBaseOuterFilter — the base file's own top-level `filters:` tree,
	// which applies to every view in the file.
	LossBaseOuterFilter LossPosition = "base outer filter"
	// LossViewFilter — a subtree of one view's own `filters:` that could not
	// be translated as a unit (an `or:` group, a multi-clause `not:`).
	LossViewFilter LossPosition = "view filter"
	// LossFilterLeaf — a single filter clause that parsed but could not be
	// built against the resolved schema (an undeclared property, an enum
	// literal the inferred schema does not declare, an operator not defined
	// for the property's type).
	LossFilterLeaf LossPosition = "filter"
	// LossGroupBy — a `groupBy` property or direction.
	LossGroupBy LossPosition = "group_by"
	// LossProperties — a display column from `order:`.
	LossProperties LossPosition = "properties"
	// LossSort — a sort key.
	LossSort LossPosition = "sort"
	// LossAggregates — a `summaries` entry.
	LossAggregates LossPosition = "aggregates"
	// LossLayout — the view's requested rendering (FR-109).
	LossLayout LossPosition = "layout"
	// LossLimit — the view's own row-count bound (`limit:`). A limit DECIDES
	// the row set: dropping one lets the view return more rows than the base
	// asked for, which is the FR-105 direction, so it is classified with the
	// filters and not with the annotations.
	LossLimit LossPosition = "limit"
)

// allLossPositions is every position this importer can emit. It exists so a
// test can assert the classification below covers exactly it — see this
// file's header on why a list of substrings would be vacuous.
var allLossPositions = []LossPosition{
	LossBaseOuterFilter,
	LossViewFilter,
	LossFilterLeaf,
	LossGroupBy,
	LossProperties,
	LossSort,
	LossAggregates,
	LossLayout,
	LossLimit,
}

// lossAffectsRowSet is FR-105's partition. TRUE means a loss at this
// position can change which records the view returns, so a view carrying one
// is DISABLED. FALSE means the loss is an ANNOTATION — display config, a
// summary, a rendering — which cannot change the row set at all, so the view
// imports ENABLED with the loss declared in `untranslated` (FR-106).
//
// Every position MUST appear. A position added to allLossPositions and not
// to this map fails TestLossPositions_ArePartitioned by name.
var lossAffectsRowSet = map[LossPosition]bool{
	LossBaseOuterFilter: true,
	LossViewFilter:      true,
	LossFilterLeaf:      true,
	LossLimit:           true,

	LossGroupBy:    false,
	LossProperties: false,
	LossSort:       false,
	LossAggregates: false,
	LossLayout:     false,
}

// lossf renders one named loss as `[position] detail`. It is the ONLY way a
// loss line is built, so no loss can reach a report without a position the
// partition above has an opinion about.
func lossf(pos LossPosition, format string, args ...any) string {
	return fmt.Sprintf("[%s] ", pos) + fmt.Sprintf(format, args...)
}

// parseLossPosition reads back the position a loss line was rendered with.
// The second return is FALSE for a line whose prefix is not a known
// position — which a test treats as a defect rather than as an annotation,
// because "unclassified" defaulting to "harmless" is how the prohibition
// would erode.
func parseLossPosition(line string) (LossPosition, bool) {
	if len(line) == 0 || line[0] != '[' {
		return "", false
	}
	end := -1
	for i := 1; i < len(line); i++ {
		if line[i] == ']' {
			end = i
			break
		}
	}
	if end < 0 {
		return "", false
	}
	pos := LossPosition(line[1:end])
	if _, known := lossAffectsRowSet[pos]; !known {
		return "", false
	}
	return pos, true
}

// lossPositionAffectsRowSet reports whether one rendered loss line sits in a
// row-set-affecting position. An UNKNOWN prefix answers true: an
// unclassifiable loss is treated as the dangerous kind, so the failure mode
// of forgetting to classify something is a view that is disabled when it did
// not need to be — visible, arguable, and safe — rather than a view that
// silently returns more rows than its original.
func lossPositionAffectsRowSet(line string) bool {
	pos, ok := parseLossPosition(line)
	if !ok {
		return true
	}
	return lossAffectsRowSet[pos]
}
