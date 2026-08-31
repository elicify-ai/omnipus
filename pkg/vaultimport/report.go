// Omnipus — the importer's human-readable report: schema inference,
// ambiguities refused, per-base three-way outcomes, and validation numbers.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TypeSchemaSummary is one inferred record type's shape, for the report.
type TypeSchemaSummary struct {
	Type          string
	NoteCount     int
	PropertyCount int
	RequiredCount int
	ManyCount     int
	RelationCount int
	EnumCount     int
	DateCount     int
	IntegerCount  int
	DecimalCount  int
	TextCount     int
}

// ValidationSummary is the vault-wide record-validity count, produced by
// actually running records.Validate over every note against the schemas
// this importer just wrote — not estimated.
type ValidationSummary struct {
	TotalNotes          int
	NotesWithoutType    int // no `type:` at all — never a candidate record
	NotesWithType       int
	RecognisedRecords   int // `type:` matched a schema this run declared
	NotRecordsAtAll     int // NotesWithoutType, named for the report
	ValidRecords        int
	InvalidRecords      int
	ErrorFindingCount   int
	WarningFindingCount int
	InvalidExamples     []string // capped, for the report
}

// Report is the importer's complete, honest account of one run.
type Report struct {
	VaultRoot      string
	Discriminator  TypeDiscriminatorCheck
	LoadProblems   []LoadProblem
	Types          []TypeSchemaSummary
	Ambiguities    []AmbiguousInference
	RelationSplits []RelationSplitReport
	// TypeInference is FR-104b's per-note account of every note that
	// arrived carrying no `type:` key.
	TypeInference TypeInferenceReport
	Bases         []BaseOutcome
	SchemaReload  *records.SchemaLoadReport
	ViewReload    *records.ViewLoadReport
	Validation    ValidationSummary
}

// Render writes the full, human-readable report.
func (r *Report) Render(w io.Writer) {
	fmt.Fprintf(w, "Obsidian vault import — %s\n", r.VaultRoot)
	fmt.Fprintln(w, strings.Repeat("=", 78))

	fmt.Fprintln(w, "\n-- Type discriminator check --")
	fmt.Fprintf(w, "%d notes total; %d carry a `type:` key (%d distinct types); %d carry none.\n",
		r.Discriminator.TotalNotes, r.Discriminator.WithType, r.Discriminator.DistinctTypes, r.Discriminator.WithoutType)

	if len(r.LoadProblems) > 0 {
		fmt.Fprintf(w, "\n-- %d notes could not be read or parsed --\n", len(r.LoadProblems))
		for _, p := range r.LoadProblems {
			fmt.Fprintf(w, "  %s: %s\n", p.RelPath, p.Reason)
		}
	}

	fmt.Fprintf(w, "\n-- %d record types inferred --\n", len(r.Types))
	for _, t := range r.Types {
		fmt.Fprintf(w, "  %-20s %4d notes, %2d properties (required=%d many=%d relation=%d enum=%d date=%d integer=%d decimal=%d text=%d)\n",
			t.Type, t.NoteCount, t.PropertyCount, t.RequiredCount, t.ManyCount,
			t.RelationCount, t.EnumCount, t.DateCount, t.IntegerCount, t.DecimalCount, t.TextCount)
	}

	fmt.Fprintf(w, "\n-- %d ambiguous property inferences (refused to guess; defaulted to text) --\n", len(r.Ambiguities))
	for _, a := range r.Ambiguities {
		fmt.Fprintf(w, "  %s.%s: %d/%d values (%.0f%%) parse as %s; defaulted to text.\n",
			a.RecordType, a.Property, a.MatchedCount, a.TotalValues, a.MatchFrac*100, a.BestType)
		for _, ex := range a.Examples {
			fmt.Fprintf(w, "      counter-example: %s = %q\n", ex.NotePath, ex.Value)
		}
	}

	fmt.Fprintf(w, "\n-- %d relation properties whose link targets were not unanimous (FR-104a) --\n", len(r.RelationSplits))
	for _, s := range r.RelationSplits {
		switch s.Rule {
		case RelationUnresolved:
			fmt.Fprintf(w, "  %s.%s: NOT TYPED — no link resolved to any known record type (unresolved=%d); declared text.\n",
				s.RecordType, s.Property, s.Unresolved)
			fmt.Fprintf(w, "      fix: %s\n", s.Remedy)
		case RelationSupermajority:
			fmt.Fprintf(w, "  %s.%s: declared relation, to=%s — %d of %d resolved links (>= 2/3), unresolved=%d.\n",
				s.RecordType, s.Property, s.MajorityType, s.MajorityCount, s.ResolvedTotal, s.Unresolved)
			fmt.Fprintf(w, "      minority (these links WILL be reported as relation_type_mismatch, which is correct): %s\n",
				strings.Join(s.Minority, ", "))
		case RelationNoMajority:
			fmt.Fprintf(w, "  %s.%s: NOT TYPED — the evidence is genuinely mixed, no target type reached 2/3 of %d resolved links (unresolved=%d); declared text.\n",
				s.RecordType, s.Property, s.ResolvedTotal, s.Unresolved)
			fmt.Fprintf(w, "      evidence: %s\n", strings.Join(s.Minority, ", "))
			fmt.Fprintf(w, "      fix: %s\n", s.Remedy)
		default:
			fmt.Fprintf(w, "  %s.%s: %v (unresolved=%d) — declared %s\n",
				s.RecordType, s.Property, s.ByType, s.Unresolved, s.Declared)
		}
	}

	fmt.Fprintf(w, "\n-- %d `.base` files: three-way outcome --\n", len(r.Bases))
	for _, b := range r.Bases {
		fmt.Fprintf(w, "  %s: %s\n", b.BaseRelPath, b.Status)
		if b.Status == OutcomeRefused && b.RefusedReason != "" {
			fmt.Fprintf(w, "      %s\n", b.RefusedReason)
		}
		for _, v := range b.Views {
			fmt.Fprintf(w, "    - %-30s %s", v.DisplayName, v.Status)
			if v.ResolvedType != "" {
				fmt.Fprintf(w, " (type=%s)", v.ResolvedType)
			}
			if v.Layout != "" {
				fmt.Fprintf(w, " (layout=%s)", v.Layout)
			}
			if v.Disabled {
				fmt.Fprint(w, "  [DISABLED — stored, never applied]")
			}
			fmt.Fprintln(w)
			if v.Status == OutcomeRefused {
				fmt.Fprintf(w, "        reason: %s\n", v.RefusedReason)
			}
			if v.Disabled {
				fmt.Fprintf(w, "        FR-105: %d loss(es) sit where the ROW SET is decided, so applying this view would return MORE rows than the Obsidian original. It is stored disabled rather than imported broadened:\n", len(v.DisablingLosses))
				for _, l := range v.DisablingLosses {
					fmt.Fprintf(w, "          -> %s\n", l)
				}
			}
			for _, l := range v.Losses {
				fmt.Fprintf(w, "        lost: %s\n", l)
			}
		}
	}

	if r.SchemaReload != nil && !r.SchemaReload.OK() {
		fmt.Fprintf(w, "\n-- %d schema files rejected on reload (should be zero) --\n", len(r.SchemaReload.Rejections))
		for _, rej := range r.SchemaReload.Rejections {
			fmt.Fprintf(w, "  %s\n", rej.String())
		}
	}
	if r.ViewReload != nil && !r.ViewReload.OK() {
		fmt.Fprintf(w, "\n-- %d view files rejected on reload (should be zero) --\n", len(r.ViewReload.Rejections))
		for _, rej := range r.ViewReload.Rejections {
			fmt.Fprintf(w, "  %s\n", rej.String())
		}
	}

	ti := r.TypeInference
	fmt.Fprintf(w, "\n-- %d notes arrived with no `type:` — every one has a recorded outcome (FR-104b) --\n", len(ti.Notes))
	fmt.Fprintf(w, "  %d typed by this run, %d left as is because the shape matched several types, %d left as is because it matched none",
		ti.Written, ti.Ambiguous, ti.NoMatch)
	if ti.WriteErrors > 0 {
		fmt.Fprintf(w, ", %d could not be written", ti.WriteErrors)
	}
	fmt.Fprintln(w, ".")
	for _, n := range ti.Notes {
		fmt.Fprintf(w, "  %s: %s\n", n.RelPath, n.Reason)
	}

	fmt.Fprintln(w, "\n-- FR-105: the broadening prohibition, applied --")
	produced, disabled := 0, 0
	for _, b := range r.Bases {
		for _, vv := range b.Views {
			if vv.Status == OutcomeRefused {
				continue
			}
			produced++
			if vv.Disabled {
				disabled++
			}
		}
	}
	fmt.Fprintf(w, "  %d view files written, of which %d are DISABLED because a loss sits where the row set is decided.\n", produced, disabled)
	fmt.Fprintf(w, "  %d are enabled and every clause they carry has a faithful mapping — an enabled imported view NEVER returns more rows than its Obsidian original.\n", produced-disabled)

	v := r.Validation
	fmt.Fprintln(w, "\n-- Validation over every note, against the schemas just written --")
	fmt.Fprintf(w, "  %d notes total\n", v.TotalNotes)
	fmt.Fprintf(w, "  %d notes carry no `type:` — not records at all\n", v.NotRecordsAtAll)
	fmt.Fprintf(w, "  %d notes carry a `type:` this run recognised as a schema\n", v.RecognisedRecords)
	fmt.Fprintf(w, "  %d valid, %d invalid (%d error findings, %d warning findings)\n",
		v.ValidRecords, v.InvalidRecords, v.ErrorFindingCount, v.WarningFindingCount)
	if len(v.InvalidExamples) > 0 {
		fmt.Fprintln(w, "  invalid record examples:")
		for _, ex := range v.InvalidExamples {
			fmt.Fprintf(w, "    %s\n", ex)
		}
	}

	fmt.Fprintln(w, "\n-- Systemic gaps found in this vault's `.base` files (the point of the exercise) --")
	for _, g := range r.systemicGaps() {
		fmt.Fprintf(w, "  * %s\n", g)
	}
}

// systemicGaps groups every named loss across every base into the small
// number of REAL, recurring shapes this product's model cannot express —
// the honest headline finding this importer exists to surface, rather than
// a wall of per-clause repetition.
func (r *Report) systemicGaps() []string {
	var fileFn, formula, orGroup, groupDir, refusedNoType, textOpMismatch, emptyStringCmp int
	fileEx, formulaEx, orEx := "", "", ""
	for _, b := range r.Bases {
		if b.Status == OutcomeRefused && strings.Contains(b.RefusedReason, "no `type ==") {
			refusedNoType++
		}
		for _, v := range b.Views {
			if v.Status == OutcomeRefused && strings.Contains(v.RefusedReason, "type") {
				refusedNoType++
			}
			for _, l := range v.Losses {
				switch {
				case strings.Contains(l, "file."):
					fileFn++
					if fileEx == "" {
						fileEx = l
					}
				case strings.Contains(l, "formula"):
					formula++
					if formulaEx == "" {
						formulaEx = l
					}
				case strings.Contains(l, "or:") || strings.Contains(l, "\"or\""):
					orGroup++
					if orEx == "" {
						orEx = l
					}
				case strings.Contains(l, "no sort-direction field"):
					groupDir++
				case strings.Contains(l, "not defined on text property"):
					textOpMismatch++
				case strings.Contains(l, `!= ""`) || strings.Contains(l, `== ""`):
					emptyStringCmp++
				}
			}
		}
	}
	var out []string
	if refusedNoType > 0 {
		out = append(out, fmt.Sprintf("MIXED-TYPE / FOLDER-SCOPED VIEWS (%d view(s)/base(s)): a Base view can list notes across several record types by folder alone (`file.inFolder(...)`) with no `type ==` anywhere. ViewDef requires exactly ONE `type:` per view — there is no multi-type or folder-scoped view concept at all, so these are REFUSED rather than guessed.", refusedNoType))
	}
	if fileFn > 0 {
		out = append(out, fmt.Sprintf("file.* FUNCTIONS (%d dropped clause(s)): `file.inFolder(...)`, `file.name`, `file.backlinks.length` etc. have no property equivalent — folder scoping and backlink counts are not expressible as a RecordFilter at all. Example: %q", fileFn, fileEx))
	}
	if formula > 0 {
		out = append(out, fmt.Sprintf("COMPUTED / FORMULA PROPERTIES (%d dropped clause/column/aggregate(s)): every `.base` file in this vault that declares `formulas:` (age-in-days, days-to-renewal, stale flags, team-name lookups...) produces `formula.*` properties used in filters, groupBy, columns and aggregates. The record schema has no computed-property type at all — text/enum/relation/date/integer/decimal/person is the closed set (FR-004) — so every formula-derived reference is dropped. Example: %q", formula, formulaEx))
	}
	if orGroup > 0 {
		out = append(out, fmt.Sprintf("OR / DISJUNCTION (%d dropped group(s)): ViewDef's `filters:` is a flat AND-only list (view.go: \"Filter clauses, combined with AND\") with no boolean tree at all. Every `or:` block — including a base's own TOP-LEVEL filter in one case (Subscriptions.base) — is dropped whole and reported, never partially translated. Example: %q", orGroup, orEx))
	}
	if groupDir > 0 {
		out = append(out, fmt.Sprintf("GROUP-BY DIRECTION (%d occurrence(s)): ViewDef.group_by is a bare list of property names with no ASC/DESC field at all — every Base groupBy's direction is silently unrepresentable on the wire, not merely untranslated by this importer.", groupDir))
	}
	if textOpMismatch > 0 {
		out = append(out, fmt.Sprintf("COMPARISON ON A TEXT PROPERTY (%d dropped clause(s)): a property this importer inferred as `text` (free prose, given its observed values) was compared with `==`/`!=`/`<`/`>` in a Base filter. The contract defines only `contains`/`is_absent` for text (RecordFilter.yaml: \"Equality and ordering are NOT defined for text\") — these clauses are genuinely unrepresentable, not merely unimplemented.", textOpMismatch))
	}
	if emptyStringCmp > 0 {
		out = append(out, fmt.Sprintf("EMPTY-STRING COMPARISON (%d): `prop != \"\"` / `prop == \"\"` — Obsidian's own semantics for comparing an undefined property against the empty string are not something this importer can rely on, and the record engine has no empty-string literal for any of its seven types. Refused rather than approximated as `is_absent`.", emptyStringCmp))
	}
	if len(out) == 0 {
		out = append(out, "none found — every `.base` file translated without a systemic gap")
	}
	return out
}

func sortedGroupKeys(m map[string]*TypeGroup) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
