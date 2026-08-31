// Omnipus — FR-104b: inferring and WRITING `type:` into notes that carry
// none, so an untyped note is a decision on the record rather than a note
// nobody ever looked at.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// THE FOUNDER'S RULING, AND WHAT IT ACTUALLY DEMANDS
//
// Verbatim: "can you not just fix them, use common sense." The founder's
// vault holds 27 notes with no `type:` key. Before this file they were
// counted in the report ("27 carry none") and then dropped on the floor:
// they got no schema, no record identity, and no line saying why.
//
// FR-104b's requirement is NOT "guess a type for every note". It is that
// EVERY untyped note leaves the import with a RECORDED OUTCOME — a written
// `type:`, or a named reason it could not be inferred. "Left as is" is a
// decision, and a decision has to be written down. That is the difference
// between this and a silent skip, and it is the only thing that makes the
// count in the report mean anything.
//
// THE MATCH RULE, STATED SO IT CAN BE ARGUED WITH:
//
//	A note matches inferred type T when
//	  (1) it carries at least one non-reserved frontmatter key, AND
//	  (2) EVERY key it carries is a property T declares, AND
//	  (3) EVERY property T declares as REQUIRED is present and non-empty
//	      on the note, AND
//	  (4) EVERY value the note carries is one T would ACCEPT — right arity,
//	      right type, and for an enum, one of T's declared values.
//	When EXACTLY ONE inferred type matches, it is WRITTEN.
//	When SEVERAL match, the BEST-FIT rule below picks between them, and
//	the type is written with the ranking that chose it recorded.
//	When several match and best fit cannot separate them, nothing is
//	written and the report says which types tied and by what numbers.
//
// (2) is what stops a note being adopted by a type that never heard of half
// its keys. (3) is what stops the loosest schema in the vault swallowing
// everything: a type whose notes all carry `status` and `owner` will not
// take a note that carries neither. (1) stops a note with no frontmatter at
// all — which matches every type vacuously under (2) — from matching
// anything. (4) is what stops a note being typed into a schema it
// demonstrably violates: shape alone let `status: blocked` be written as a
// `project` whose status values are `active` and `done`, producing a note
// that failed the very schema the same run generated. Its rule and its
// evidence live in typeinfer_conformance.go.
//
// THE BEST-FIT RULE, AND WHY "EXACTLY ONE" WAS NOT ENOUGH ON ITS OWN.
//
// The first version of this file stopped at "exactly one candidate or
// nothing". Measured against the founder's own 757-note vault it typed ZERO
// notes: of the 27 that arrived untyped, 14 matched SEVERAL types and 13
// matched none. An importer that helps nobody is not safe, it is useless,
// and the founder's ruling was explicit in both directions — "can you not
// just fix them, use common sense" and "Importer guesses, you can correct".
// A REPORTED guess is wanted. A SILENT WRONG guess is not.
//
// So when several types accept everything the note carries, they are
// ranked, in one sentence a human can check:
//
//	BEST FIT = the share of the frontmatter that a type's notes ACTUALLY
//	FILL IN which this note carries.
//
// Concretely: for each candidate type T, add up — across every note of
// type T in the vault — how many times each of T's declared properties was
// filled in with a real value. That total is T's evidence mass. The note's
// score against T is the part of that mass belonging to the properties the
// note itself carries, over the whole of it.
//
// Two properties of that rule are the point of it:
//
//   - It rewards carrying a type's COMMON properties, not merely being
//     compatible with its rare ones. A generic org note carrying
//     {created, updated, up, related} scores 75% against `note` (whose
//     notes fill in little else) and 8% against `reference` (whose 48
//     declared properties are mostly one-offs from a handful of notes),
//     even though `reference` accepts it just as happily.
//   - The vault's own note count CANCELS OUT of the ratio, so the score is
//     an exact ratio of two integers. No floating point decides anything,
//     the comparison is a cross-multiplication, and two identical runs
//     produce byte-identical rankings.
//
// A WINNER MUST WIN CLEARLY. The top-ranked type is only written when it
// beats the runner-up by at least bestFitMargin (5 percentage points). This
// is not decoration: on the founder's vault, `note` scores 67.2% and `moc`
// 66.7% on the same note, and a rule that let half a point decide would be
// a coin toss dressed up with a number. Inside the margin the outcome is
// AMBIGUOUS and the report prints the whole ranking, so the founder sees
// the two contenders and the half-point between them and settles it in one
// edit.
//
// A guess made this way is auditable in a single line: the report names the
// type that won, every runner-up, and each one's score.
//
// WHAT THIS RULE DOES NOT DO. It never overrides conditions (1)-(4). Only
// types that already accept every value the note carries are ranked at all,
// so a best-fit winner is still, always, a type the note conforms to — the
// acceptance bar (a typed note is never reported invalid by the same run)
// is untouched by it, because ranking a set cannot add a member to it.
//
// A CONSEQUENCE OF (4) WORTH KNOWING. A record type evidenced by only a
// handful of notes infers tight enums on fields that are really free text —
// two people with two addresses makes `email` an enum of those two
// addresses — and (4) then refuses to adopt any newcomer into it. That is
// the correct conservative answer (the alternative writes a type onto a
// note that then fails validation), but it means a 2-note type adopts
// almost nothing, and the report says so per note rather than leaving the
// operator to wonder.
//
// THE WRITE IS THE MINIMUM POSSIBLE EDIT. One `type: <value>` line is
// inserted immediately after the opening `---` fence. Nothing else in the
// file is touched — not the other keys, not their order, not their
// formatting, not the body. A note is the operator's own document; an
// importer that re-serialises YAML to add one key will reformat quotes,
// collapse blank lines and reorder nothing anybody asked it to reorder.
// ---------------------------------------------------------------------------

// TypeInferenceOutcome is one untyped note's recorded decision.
type TypeInferenceOutcome struct {
	// RelPath is the note, vault-relative.
	RelPath string
	// Inferred is the type written into the note, empty when none was.
	Inferred string
	// Written reports whether the note's file was actually modified. It is
	// false on a dry run even when Inferred is set — so a dry run's report
	// says what it WOULD write without claiming it did.
	Written bool
	// Candidates is every inferred type whose shape the note matched,
	// sorted. Length 1 with Inferred set is the confident case; length > 1
	// means the best-fit rule was asked to choose; length 0 is no match.
	Candidates []string
	// Fit is every candidate scored by the best-fit rule, best first. It is
	// populated only when there was more than one candidate — a sole
	// candidate needs no ranking and gets none, so an empty Fit on a
	// written note means "there was nothing to choose between".
	Fit []TypeFit
	// TieBroken reports that Inferred was CHOSEN from several candidates by
	// the best-fit rule rather than being the only type that fit. It is the
	// field that separates a certainty from a reported guess, and the
	// report says so in those words.
	TieBroken bool
	// Reason states the outcome in words. NEVER empty — a recorded outcome
	// with no reason is a silent skip wearing a report entry's clothes.
	Reason string
}

// bestFitMarginNum/Den express the margin the top-ranked type must beat the
// runner-up by, as an exact fraction rather than a float: 5 percentage
// points. Stated as a ratio so the whole comparison stays in integers.
//
// WHY THERE IS A MARGIN AT ALL, with the case that forced it. On the
// founder's vault, `02-Projects/content-production/content-production
// MOC.md` scores 67.2% against `note` and 66.7% against `moc`. Both print
// as "67%". A rule that wrote `note` there would be making a coin toss look
// like a measurement — the exact dishonesty this package exists to avoid —
// and the founder would have no way to see it was one. Under the margin it
// is reported AMBIGUOUS with both numbers, which is the truth: the shape of
// that note does not tell the two types apart.
//
// Five points is a judgement, and it is stated here rather than buried in a
// condition so it can be argued with. It was chosen against the measured
// vault: it lets through the 11 notes whose winner leads by 6 to 19 points,
// and stops the 3 whose lead is half a point or less.
const (
	bestFitMarginNum = 5
	bestFitMarginDen = 100
)

// TypeFit is one candidate type's best-fit score for one note.
//
// The score is Carried/Total and it is deliberately kept as the two
// integers rather than a computed float: the comparison between two fits is
// an exact cross-multiplication, and the report shows the working.
type TypeFit struct {
	// Type is the candidate.
	Type string
	// Carried is the summed evidence weight of the properties THIS NOTE
	// carries — for each such property, how many notes of Type filled it
	// in with a real value.
	Carried int
	// Total is the same sum over EVERY property Type declares.
	Total int
}

// Percent renders the score for the report, to one decimal place.
//
// One decimal, not zero, and the reason is the `note` 67.2% / `moc` 66.7%
// case above: rounded to whole percent both read "67%", and a report that
// prints two identical numbers and then picks one of them teaches the
// reader that the numbers are decoration.
func (f TypeFit) Percent() float64 {
	if f.Total <= 0 {
		return 0
	}
	return 100 * float64(f.Carried) / float64(f.Total)
}

// String renders one ranked candidate, e.g. `note 67.2%`.
func (f TypeFit) String() string {
	return fmt.Sprintf("%s %.1f%%", f.Type, f.Percent())
}

// leads reports whether f's score exceeds other's by at least the best-fit
// margin, in exact integer arithmetic:
//
//	f.Carried/f.Total - o.Carried/o.Total >= num/den
//
// multiplied out by the (positive) product of the denominators. int64 is
// far more headroom than needed — the largest possible operand is bounded
// by (notes x properties)^2, and the founder's vault gives ~10^10 against
// int64's ~9.2x10^18 — but a silent overflow here would corrupt a decision
// rather than fail one, so the widening is not left to chance.
func (f TypeFit) leads(o TypeFit, num, den int) bool {
	ft, ot := int64(f.Total), int64(o.Total)
	if ft <= 0 || ot <= 0 {
		// An unscorable candidate (no property of it was ever filled in by
		// any note) cannot be shown to lead anything.
		return false
	}
	lhs := (int64(f.Carried)*ot - int64(o.Carried)*ft) * int64(den)
	return lhs >= int64(num)*ft*ot
}

// scoreFit computes one candidate's best-fit score against a note's keys.
//
// Only integers are added, so the result does not depend on the order the
// declared properties are visited — which is what lets this walk a map
// without sorting it first and still be bit-identical between runs.
func scoreFit(keys []string, sh typeShape) TypeFit {
	f := TypeFit{Type: sh.name}
	for _, decl := range sh.declared {
		f.Total += decl.ObservedCount
	}
	for _, k := range keys {
		if decl, ok := sh.declared[k]; ok {
			f.Carried += decl.ObservedCount
		}
	}
	return f
}

// rankFit scores every candidate and sorts them best first, breaking equal
// scores by name so a report never reshuffles between two identical runs.
func rankFit(keys []string, candidates []string, shapes map[string]typeShape) []TypeFit {
	fits := make([]TypeFit, 0, len(candidates))
	for _, c := range candidates {
		fits = append(fits, scoreFit(keys, shapes[c]))
	}
	sort.Slice(fits, func(i, j int) bool {
		a, b := fits[i], fits[j]
		// a > b  <=>  a.Carried/a.Total > b.Carried/b.Total, cross-multiplied.
		l := int64(a.Carried) * int64(b.Total)
		r := int64(b.Carried) * int64(a.Total)
		if l != r {
			return l > r
		}
		return a.Type < b.Type
	})
	return fits
}

// bestFitWinner returns the top-ranked type when it leads the runner-up by
// at least the margin, and false when it does not.
//
// It is deliberately blind to everything except the ranking it is handed:
// no preference for a type with more notes, no preference for a shorter
// name, no fallback to "the first one alphabetically". A rule that reaches
// for a second criterion the moment the first one is inconclusive is a rule
// that always produces an answer, and always producing an answer is exactly
// how an importer ends up writing coin tosses into somebody's files.
func bestFitWinner(fits []TypeFit) (string, bool) {
	if len(fits) < 2 {
		return "", false
	}
	if !fits[0].leads(fits[1], bestFitMarginNum, bestFitMarginDen) {
		return "", false
	}
	return fits[0].Type, true
}

// joinFits renders a whole ranking for the report.
func joinFits(fits []TypeFit) string {
	parts := make([]string, 0, len(fits))
	for _, f := range fits {
		parts = append(parts, f.String())
	}
	return strings.Join(parts, ", ")
}

// chooseType picks the type to write from a non-empty candidate list, and
// returns the clause the report uses to justify it.
//
// It returns "" when the best-fit rule declines, and in that case it is
// chooseType that writes out.Reason — the refusal has to carry the whole
// ranking, which the caller does not have, and a refusal is a recorded
// outcome exactly like a write.
//
// It MUTATES out (Fit, TieBroken, and Reason on a refusal). That is
// deliberate and it is why it takes a pointer: the ranking is evidence for
// the decision, and evidence that is computed and thrown away leaves a
// report the founder cannot check.
func chooseType(keys []string, out *TypeInferenceOutcome, shapes map[string]typeShape) (string, string) {
	if len(out.Candidates) == 1 {
		return out.Candidates[0], "its frontmatter shape matches that type and no other"
	}

	out.Fit = rankFit(keys, out.Candidates, shapes)
	winner, ok := bestFitWinner(out.Fit)
	if !ok {
		out.Reason = fmt.Sprintf(
			"left as is: it carries [%s] — %d inferred types accept every one of those values (%s), and BEST FIT cannot separate the top of the field: %s. A type is only written when it leads the runner-up by at least %d percentage points, and these are inside that margin, so picking one would be a coin toss written into your own file. One edit settles it: add `type: <one of those>` to the note, or narrow the schemas with knowledge_configure.",
			strings.Join(keys, ", "), len(out.Candidates), strings.Join(out.Candidates, ", "),
			joinFits(out.Fit), bestFitMarginNum)
		return "", ""
	}

	out.TieBroken = true
	return winner, fmt.Sprintf(
		"BEST FIT, a reported guess rather than a certainty: %d inferred types accept everything this note carries, and it fills %.1f%% of the frontmatter that `%s` notes actually fill in, against %s. If that is wrong, one edit to `type:` corrects it",
		len(out.Candidates), out.Fit[0].Percent(), winner, joinFits(out.Fit[1:]))
}

// TypeInferenceReport is FR-104b's whole account.
type TypeInferenceReport struct {
	Notes []TypeInferenceOutcome
	// Written / Ambiguous / NoMatch partition Notes. Kept as counts because
	// the founder reads the counts and the reviewer reads the list.
	Written   int
	Ambiguous int
	NoMatch   int
	// WriteErrors is every note whose file could not be edited, named. A
	// failed write is reported as its own outcome rather than downgraded
	// into "no match", which would misattribute a filesystem problem to the
	// inference.
	WriteErrors int
}

// reservedFrontmatterKeys are the three keys a schema never declares:
// the record-type discriminator itself and D7/D8's two identifier keys.
// CollectTypeGroups excludes exactly these, so the key sets compared here
// and the property sets inferred there are drawn on the same terms.
func isReservedKey(key string) bool {
	return key == records.RecordTypeKey ||
		key == records.RecordIDKey ||
		key == records.RecordIDKeyNamespaced
}

// InferTypesForUntypedNotes decides every untyped note in notes against the
// inferred schemas, writing `type:` where exactly one shape matches.
//
// write=false performs every decision and records every outcome without
// touching a single file — the dry-run path, and the path every unit test
// that is not specifically about the write itself uses.
func InferTypesForUntypedNotes(notes []NoteRecord, inferred map[string][]InferredProperty, write bool) TypeInferenceReport {
	shapes := buildTypeShapes(inferred)
	var rep TypeInferenceReport

	for i := range notes {
		n := &notes[i]
		if n.Rec.TypeName() != "" {
			continue
		}
		out := TypeInferenceOutcome{RelPath: n.RelPath}
		keys := nonReservedKeys(n.Rec)

		switch {
		case len(keys) == 0:
			rep.NoMatch++
			out.Reason = "left as is: the note carries no frontmatter properties at all, so there is no shape to match. It is still fully indexed (FR-021e) and reachable through an untyped view."
		default:
			var blocked []shapeBlock
			out.Candidates, blocked = matchingTypes(keys, n.Rec, shapes)
			switch len(out.Candidates) {
			case 0:
				rep.NoMatch++
				if len(blocked) > 0 {
					// A NEAR MISS, and the most useful line in the whole
					// report: the note is one bad value away from being a
					// record, and this names the value. Typing it anyway
					// would write a note that fails the schema this very
					// run produced.
					out.Reason = fmt.Sprintf(
						"left as is: its shape fits %s, but the value stops it — %s. Typing it anyway would write a note that fails the schema this run just produced. Fix the value, or add `type:` yourself if the schema is what is wrong. It is still fully indexed (FR-021e) and reachable through an untyped view.",
						plural(len(blocked), "one type", "these types"), joinBlocks(blocked))
					break
				}
				out.Reason = fmt.Sprintf(
					"left as is: it carries [%s], and no inferred type declares all of those AND has every one of its own required properties present on this note. It is still fully indexed (FR-021e) and reachable through an untyped view.",
					strings.Join(keys, ", "))
			default:
				// One candidate is a certainty; several is a job for the
				// best-fit rule, which either names a clear winner or
				// declines and leaves `chosen` empty.
				chosen, why := chooseType(keys, &out, shapes)
				if chosen == "" {
					rep.Ambiguous++
					break
				}
				out.Inferred = chosen
				if !write {
					rep.Written++
					out.Reason = fmt.Sprintf("would write `type: %s` — %s (dry run: nothing was written).", out.Inferred, why)
					break
				}
				if err := writeTypeKey(n.AbsPath, out.Inferred); err != nil {
					rep.WriteErrors++
					out.Inferred = ""
					out.TieBroken = false
					out.Reason = fmt.Sprintf("matched type %q but the file could NOT be edited: %v", chosen, err)
					break
				}
				out.Written = true
				rep.Written++
				// The in-memory record is patched to match what is now on
				// disk. Without this the run's own validation pass would
				// still see the note as untyped and report it as "not a
				// record at all" — a report contradicting the file the same
				// run just wrote, which is the exact class of stale-read
				// dishonesty this package exists to avoid.
				adoptTypeInMemory(&n.Rec, out.Inferred)
				out.Reason = fmt.Sprintf("wrote `type: %s` — %s.", out.Inferred, why)
			}
		}
		rep.Notes = append(rep.Notes, out)
	}
	sort.Slice(rep.Notes, func(i, j int) bool { return rep.Notes[i].RelPath < rep.Notes[j].RelPath })
	return rep
}

// adoptTypeInMemory records, on an already-parsed Record, the `type:` key
// that was just written to its file — the same key, the same lexical value,
// so nothing downstream re-reads the file to learn what this run did.
func adoptTypeInMemory(rec *records.Record, typeName string) {
	if rec.Frontmatter.Values == nil {
		rec.Frontmatter.Values = map[string]records.Node{}
	}
	if _, exists := rec.Frontmatter.Values[records.RecordTypeKey]; !exists {
		rec.Frontmatter.Keys = append([]string{records.RecordTypeKey}, rec.Frontmatter.Keys...)
	}
	rec.Frontmatter.Present = true
	rec.Frontmatter.Values[records.RecordTypeKey] = records.Node{Kind: records.KindScalar, Text: typeName}
}

// typeShape is one inferred type reduced to what the match rule needs: its
// name, every property it declares (with the full declaration, so condition
// (4) can ask whether a value is acceptable), and the subset it requires.
type typeShape struct {
	name     string
	declared map[string]InferredProperty
	required []string
}

func buildTypeShapes(inferred map[string][]InferredProperty) map[string]typeShape {
	shapes := map[string]typeShape{}
	for t, props := range inferred {
		sh := typeShape{name: t, declared: map[string]InferredProperty{}}
		for _, p := range props {
			sh.declared[p.Name] = p
			if p.Required {
				sh.required = append(sh.required, p.Name)
			}
		}
		sort.Strings(sh.required)
		shapes[t] = sh
	}
	return shapes
}

func nonReservedKeys(rec records.Record) []string {
	var out []string
	for _, k := range rec.Frontmatter.Keys {
		if isReservedKey(k) {
			continue
		}
		out = append(out, k)
	}
	return out
}

// matchingTypes applies the four-condition rule stated in this file's header
// and returns every type that satisfies it, sorted.
//
// It also returns the types that satisfied (1)-(3) — the SHAPE — but were
// stopped by (4), each with the single value that stopped it. Those are the
// near-misses worth naming: "nothing matched" is not actionable, whereas
// "it is a project except `status: blocked` is not one of project's values"
// is a one-line fix the operator can make.
func matchingTypes(keys []string, rec records.Record, shapes map[string]typeShape) ([]string, []shapeBlock) {
	var out []string
	var blocked []shapeBlock
	for _, t := range sortedShapeNames(shapes) {
		sh := shapes[t]
		if !everyKeyDeclared(keys, sh) {
			continue
		}
		if !everyRequiredPresent(rec, sh) {
			continue
		}
		if block, ok := everyValueAccepted(rec, sh); !ok {
			blocked = append(blocked, block)
			continue
		}
		out = append(out, t)
	}
	sort.Strings(out)
	return out, blocked
}

// sortedShapeNames iterates shapes in a stable order, so a note blocked on
// several types names them in the same order on every run — Go's map
// iteration order is randomised and a report that reshuffles between two
// identical runs teaches an operator to distrust it.
func sortedShapeNames(shapes map[string]typeShape) []string {
	out := make([]string, 0, len(shapes))
	for t := range shapes {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func everyKeyDeclared(keys []string, sh typeShape) bool {
	for _, k := range keys {
		if _, ok := sh.declared[k]; !ok {
			return false
		}
	}
	return true
}

// everyRequiredPresent applies FR-007's own definition of presence, not a
// looser one: an explicit null and an empty scalar are ABSENCE, which is
// exactly how collectNodeValues counted presence when `required` was
// inferred in the first place. Using a different definition here would let
// a note satisfy a requirement it would then fail validation on.
func everyRequiredPresent(rec records.Record, sh typeShape) bool {
	for _, name := range sh.required {
		node, ok := rec.Frontmatter.Get(name)
		if !ok || !nodeHasValue(node) {
			return false
		}
	}
	return true
}

func nodeHasValue(node records.Node) bool {
	switch node.Kind {
	case records.KindScalar:
		return strings.TrimSpace(node.Text) != ""
	case records.KindSequence:
		for _, item := range node.Items {
			if item.Kind == records.KindScalar && strings.TrimSpace(item.Text) != "" {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// The write
// ---------------------------------------------------------------------------

// writeTypeKey inserts one `type: <value>` line immediately after a note's
// opening frontmatter fence, leaving every other byte of the file exactly
// where it was.
//
// It REFUSES rather than repairs in three cases, each returning a named
// error the report prints: a file with no frontmatter block at all, a file
// that already carries a `type:` key (which cannot happen through
// InferTypesForUntypedNotes, and is checked anyway because a function that
// silently double-writes a key is a landmine for the next caller), and a
// value that is not a plain scalar the YAML would round-trip unquoted.
//
// The fence-detection rules mirror records.extractFrontmatterBlock exactly —
// UTF-8 BOM, `---` as the very first line, CRLF tolerated — because a note
// this function edits and a note the parser reads must agree on where the
// frontmatter starts. They are re-stated here rather than shared because
// that function is unexported and returns the block's TEXT, not its offsets.
func writeTypeKey(absPath, typeName string) error {
	if !isPlainYAMLScalar(typeName) {
		return fmt.Errorf("refusing to write type %q: it is not a plain scalar and would need quoting rules this edit does not implement", typeName)
	}
	src, err := os.ReadFile(absPath) //nolint:gosec // the path came from this importer's own vault scan
	if err != nil {
		return err
	}

	bom := []byte{}
	body := src
	if bytes.HasPrefix(body, []byte("\xef\xbb\xbf")) {
		bom = body[:3]
		body = body[3:]
	}

	nl := bytes.IndexByte(body, '\n')
	if nl < 0 {
		return fmt.Errorf("the file has no frontmatter block (no line break at all)")
	}
	first := body[:nl]
	if strings.TrimRight(string(first), " \t\r") != "---" {
		return fmt.Errorf("the file has no frontmatter block (its first line is not `---`)")
	}

	// The already-typed guard. InferTypesForUntypedNotes never reaches here
	// with a typed note, so this cannot fire through the normal path — it is
	// here because a function that silently writes a SECOND `type:` key into
	// a file is a landmine for the next caller, and a duplicate key is the
	// kind of corruption an operator finds weeks later in someone else's
	// document. The question is asked of records.ParseRecord rather than a
	// substring scan, so "does this file declare a type" gets the same
	// answer here as everywhere else in the product.
	if existing := records.ParseRecord(absPath, src); existing.ParseError == "" {
		if _, has := existing.Frontmatter.Get(records.RecordTypeKey); has {
			return fmt.Errorf("the file already declares `%s: %s`; refusing to write a second one",
				records.RecordTypeKey, existing.TypeName())
		}
	}

	lineEnd := "\n"
	if bytes.HasSuffix(first, []byte("\r")) {
		lineEnd = "\r\n"
	}

	var out bytes.Buffer
	out.Grow(len(src) + len(typeName) + 8)
	out.Write(bom)
	out.Write(body[:nl+1])
	out.WriteString("type: " + typeName + lineEnd)
	out.Write(body[nl+1:])

	// The note is the operator's own file: preserve its mode rather than
	// imposing the control plane's 0600 on a document they wrote.
	mode := os.FileMode(0o644)
	if fi, statErr := os.Stat(absPath); statErr == nil {
		mode = fi.Mode().Perm()
	}
	return os.WriteFile(absPath, out.Bytes(), mode)
}

// isPlainYAMLScalar accepts the conservative subset of type names this edit
// will write unquoted. Every type name reaching it came from a `type:` value
// already written in this vault's own frontmatter, so the subset is not a
// restriction in practice — it is a guard against writing a line that would
// parse back as something else.
func isPlainYAMLScalar(s string) bool {
	if s == "" || s != strings.TrimSpace(s) {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
