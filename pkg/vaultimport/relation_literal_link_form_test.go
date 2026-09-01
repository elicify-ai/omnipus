// Omnipus — WHY A BARE `.base` LITERAL IS NOT QUIETLY WRAPPED IN BRACKETS FOR
// A RELATION, settled with evidence rather than with an argument.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// THE QUESTION, AND WHY IT WAS WORTH ANSWERING PROPERLY
//
// The founder's Tasks.base carries `owner == "Daniel Piatkowski"` and 89 of his
// task notes carry `owner: "[[Daniel Piatkowski]]"`. The importer types `owner`
// as a relation, a relation literal must be a wikilink, so the clause is
// refused and the "Needs Daniel" view is stored DISABLED.
//
// The obvious repair is to wrap the literal — write `[[Daniel Piatkowski]]` and
// enable the view. Whether that is legitimate turns on ONE fact that no amount
// of reasoning inside this repository can settle: what does Obsidian actually
// compare when a link-valued property is tested against a bare string? Three
// answers were plausible (the raw frontmatter text, the link's target, the
// link's display text) and they give three different row sets.
//
// So it was read out of Obsidian rather than guessed. See obsidianLinkEqualsString
// below for the transcription and the citation. The answer is a FOURTH thing
// nobody proposed, and it is the reason this file exists.
//
// WHAT THIS FILE ESTABLISHES, in the two claims the distinction actually turns on:
//
//	NOT FAITHFUL BY CONSTRUCTION.  TestRelationLiteral_BareLiteralIsNotFaithfulByConstruction
//	    finds link spellings where Obsidian says NO and this engine says YES.
//	    That is the FR-105-forbidden direction — MORE rows than the original —
//	    and it needs no exotic vault: Obsidian's own "New link format: Absolute
//	    path in vault" setting writes every link in the shape that diverges.
//
//	FAITHFUL ON THIS DATA.  TestRelationLiteral_FounderVaultHappensToCoincide
//	    walks the founder's own notes and finds the two sides agreeing on every
//	    one of them. That is a fact about 131 files, not a property of the
//	    translation, and it justifies nothing on its own.
//
// Only the first kind of claim would justify enabling the view silently. It is
// the claim that fails, so the clause stays refused — and
// TestRelationLiteral_LinkFormLiteralMakesBothSidesAgreeByConstruction shows why
// the refusal message points at the founder's own `.base` file: with the literal
// written in the link form, Obsidian switches to comparing by RESOLVED TARGET,
// which is this engine's rule exactly, and the two agree by construction from
// then on.
// ---------------------------------------------------------------------------

// obsidianLinkEqualsString is Obsidian's answer for `<link property> == "<literal>"`,
// transcribed from the shipping application rather than from its documentation.
//
// SOURCE, so the next reader can re-derive it instead of trusting this comment:
// /Applications/Obsidian.app/Contents/Resources/obsidian.asar, entry `app.js`,
// version 1.12.7 (the sibling `package.json` entry states the version). The
// Bases value model there is a class chain BW(Link) -> CW(String) -> kW(primitive),
// and three pieces decide the answer:
//
//  1. AW.fromFrontMatter's lazyEvaluator runs every STRING frontmatter value
//     through BW.parseFromString first, so `owner: "[[X]]"` is a Link, not a
//     string. parseFromString sets `.data` to the text between the brackets with
//     only the `|display` half removed — a `#heading` stays in, a folder path
//     stays in, and nothing is resolved or case-folded.
//
//  2. `==` calls the static gW.looseEquals(a,b), which tries BOTH directions:
//     e===t || (e && t && ((e.constructor===t.constructor && e.equals(t))
//     || e.looseEquals(t) || t.looseEquals(e)))
//
//  3. Link.looseEquals(String) returns FALSE for a bare literal — it needs a
//     `[[`-delimited string to parse. But the SECOND direction,
//     String.looseEquals(Link), inherits kW's
//     return e instanceof kW && this.data == e.data
//     and a Link IS an instanceof kW through the chain in (1). So the literal is
//     compared against the link's RAW TARGET TEXT.
//
// Hence: a bare literal matches the link target EXACTLY AS WRITTEN. Not the
// resolved note, not the display text, not the whole frontmatter string.
//
// The `many` case is deliberately out of scope — `owner` is single-valued in the
// vault this was settled for, and Obsidian's list handling is a separate
// question that would need its own evidence.
func obsidianLinkEqualsString(frontmatterValue, literal string) bool {
	v := strings.TrimSpace(frontmatterValue)
	if strings.HasPrefix(v, "[[") && strings.HasSuffix(v, "]]") {
		inner := v[2 : len(v)-2]
		if i := strings.LastIndex(inner, "|"); i >= 0 {
			inner = inner[:i] // parseFromString strips the display half and nothing else
		}
		return inner == literal
	}
	// Not a link: an ordinary String == String, which is kW.equals — exact.
	return v == literal
}

// ourRelationEquals is THIS ENGINE's answer for the same comparison, through the
// real comparator rather than a description of it.
//
// It is records.Comparator with a RelationResolver, which is precisely how
// knowledge_find wires it (`cmp := records.Comparator{ResolveRelation: d.Resolve}`
// in pkg/records/knowledgefind/find.go), and the resolver is built on
// pkg/knowledge's own NoteIndex — the same resolution
// pkg/vaultprops.RelationResolver.ResolveIdentity performs before it maps the
// resolved file to a record id. Nothing about the comparison is restated here.
//
// ok=false means the operand could not even be built (the literal is not a
// wikilink), which is the state the importer refuses on today.
func ourRelationEquals(t *testing.T, notes *knowledge.NoteIndex, frontmatterValue, literal string) (equal bool, ok bool) {
	t.Helper()
	prop := &records.Property{Name: "owner", Type: records.TypeRelation}
	build := func(raw string) (records.PropertyValue, bool) {
		tv, verr := records.ParseValue(prop, records.Node{Kind: records.KindScalar, Text: raw})
		if verr != nil {
			return records.PropertyValue{}, false
		}
		return records.PropertyValue{Property: prop, State: records.StatePresent, Values: []records.TypedValue{tv}}, true
	}
	left, lok := build(frontmatterValue)
	right, rok := build(literal)
	if !lok || !rok {
		return false, false
	}
	cmp := records.Comparator{ResolveRelation: func(link records.Wikilink) (string, bool) {
		rl := notes.Resolve("", knowledge.Link{Kind: knowledge.LinkWikilink, Target: link.Target})
		if rl.State != knowledge.ResolveResolved {
			return "", false
		}
		return rl.To, true
	}}
	verdict, _ := cmp.Evaluate(records.OpEqual, left, right)
	return verdict, true
}

// danielVaultPaths is the founder's own resolution situation: exactly one note
// bears the title, and it sits in a folder. Both facts are load-bearing —
// the folder is what makes a path-qualified spelling possible, and the
// uniqueness is what removes ambiguity as an alternative explanation for
// anything below.
var danielVaultPaths = []string{
	"05-Maps/Entities/Daniel Piatkowski.md",
	"05-Maps/Entities/Bruno-Backend-Developer.md",
	"01-Areas/Tasks/some-task.md",
}

const danielLiteral = "Daniel Piatkowski"

// relationSpellings are the ways a vault can write a link that a reader would
// call "the same person". They are the input to both sides; neither side's
// answer is written down here.
var relationSpellings = []string{
	`[[Daniel Piatkowski]]`,
	`[[Daniel Piatkowski|Danny]]`,
	`[[05-Maps/Entities/Daniel Piatkowski]]`,
	`[[05-Maps/Entities/Daniel Piatkowski|Danny]]`,
	`[[Daniel Piatkowski#Focus]]`,
	`[[daniel piatkowski]]`,
	`Daniel Piatkowski`,
	`[[Bruno-Backend-Developer]]`,
}

// wrappedLiteral is the TRANSLATION under consideration: the founder's bare
// literal, wrapped so that a relation operand can be built from it at all.
const wrappedLiteral = "[[" + danielLiteral + "]]"

// TestRelationLiteral_BareLiteralIsNotFaithfulByConstruction is the whole
// judgement, and the comparison it makes is the only one that answers the
// question honestly:
//
//	OBSIDIAN is asked the clause THE FOUNDER WROTE   — `owner == "Daniel Piatkowski"`
//	THIS ENGINE is asked the clause WE WOULD WRITE   — `owner = "[[Daniel Piatkowski]]"`
//
// Comparing our engine against the BARE literal instead would measure nothing:
// a bare literal cannot be built into a relation operand at all, so every row
// would come back false and the two sides would look like they disagree
// everywhere. That mistake was made once while writing this file and it is
// recorded here because the resulting table is superficially convincing.
//
// It passes by finding at least one spelling on which the translated clause
// matches a record Obsidian's original does not. One is enough: FR-105 is
// one-directional — fewer rows with the loss named is acceptable, more rows
// never is.
func TestRelationLiteral_BareLiteralIsNotFaithfulByConstruction(t *testing.T) {
	notes := knowledge.NewNoteIndex(danielVaultPaths)

	var broadened, narrowed []string
	for _, raw := range relationSpellings {
		obs := obsidianLinkEqualsString(raw, danielLiteral)
		ours, built := ourRelationEquals(t, notes, raw, wrappedLiteral)
		verdict := "agree"
		switch {
		case ours && !obs:
			verdict = "BROADENED — the translated clause matches, the founder's does not (FR-105 forbids)"
			broadened = append(broadened, raw)
		case obs && !ours:
			verdict = "narrowed — the founder's clause matches, the translated one does not (FR-105 permits)"
			narrowed = append(narrowed, raw)
		}
		note := ""
		if !built {
			// The note's own value is not a wikilink, so no relation operand
			// exists for it on our side. The verdict is still false, which is
			// what a row set cares about; the distinction is logged because it
			// is a DIFFERENT reason for the same answer.
			note = "  (our side: the note's value is not a wikilink, so no relation operand exists)"
		}
		t.Logf("  %-46s obsidian=%-5v ours=%-5v  %s%s", raw, obs, ours, verdict, note)
	}

	if len(broadened) == 0 {
		t.Fatalf("no spelling was found on which the translated clause matches a record the founder's own\n"+
			"clause does not. That would make the bare-literal-to-link-form translation faithful BY\n"+
			"CONSTRUCTION, and this test — and the refusal it justifies — would both need revisiting.\n"+
			"Spellings tried: %v", relationSpellings)
	}

	// Naming them, rather than only counting them, is what makes the finding
	// re-checkable: each is a concrete vault shape somebody can go and write.
	sort.Strings(broadened)
	t.Logf("NOT FAITHFUL BY CONSTRUCTION — %d spelling(s) return MORE rows here than in Obsidian: %v",
		len(broadened), broadened)
	t.Logf("  (%d spelling(s) return fewer, which FR-105 permits: %v)", len(narrowed), narrowed)

	// The path-qualified spelling is singled out because it is the one that
	// turns this from a curiosity into a real hazard: it is not a typo or an
	// exotic authoring style, it is what Obsidian writes for EVERY link when
	// "New link format" is set to "Absolute path in vault". In such a vault the
	// founder's own filter matches nothing at all, while the translated one
	// would match every note — the maximal FR-105 violation, reached by a
	// settings toggle.
	const pathQualified = `[[05-Maps/Entities/Daniel Piatkowski]]`
	obs := obsidianLinkEqualsString(pathQualified, danielLiteral)
	ours, built := ourRelationEquals(t, notes, pathQualified, wrappedLiteral)
	if !built || obs || !ours {
		t.Errorf("the path-qualified spelling %s is the load-bearing case and it did not behave as the\n"+
			"evidence says it must: want obsidian=false ours=true, got obsidian=%v ours=%v (operand built=%v)",
			pathQualified, obs, ours, built)
	}
}

// TestRelationLiteral_LinkFormLiteralMakesBothSidesAgreeByConstruction is why
// the refusal can name a fix instead of only naming a loss.
//
// Writing the literal in the LINK form changes what OBSIDIAN does, not only what
// this importer can accept. A `.base` filter literal is an ordinary quoted
// string — Obsidian does NOT run it through the frontmatter link parser — so
// `"[[Daniel Piatkowski]]"` arrives as a String. But Link.looseEquals(String)
// parses a `[[`-delimited string into a Link and re-enters itself on the
// Link==Link branch, which RESOLVES both sides and compares the resolved files.
// Resolved-target identity is §8 R-8 — this engine's rule. So after the edit the
// two are not merely agreeing on the founder's data, they are computing the same
// question.
//
// That is the difference between telling the founder "a clause was dropped" and
// telling him "here is a thirty-second edit to your own file that gets it back",
// and it is worth a test because if it were ever untrue the refusal message
// would be sending him to break his own view.
func TestRelationLiteral_LinkFormLiteralMakesBothSidesAgreeByConstruction(t *testing.T) {
	notes := knowledge.NewNoteIndex(danielVaultPaths)

	resolve := func(target string) (string, bool) {
		rl := notes.Resolve("", knowledge.Link{Kind: knowledge.LinkWikilink, Target: target})
		if rl.State != knowledge.ResolveResolved {
			return "", false
		}
		return rl.To, true
	}
	// Obsidian's answer when the LITERAL is bracketed. Transcribed from the same
	// source as obsidianLinkEqualsString — see its citation:
	//
	//	Link.looseEquals(e): if (e instanceof String) { const r = parseFromString(e.data);
	//	                                                if (r) return this.looseEquals(r) }
	//	Link.looseEquals(Link): const n=this.resolve(), i=e.resolve();
	//	                        return n && i ? n===i : this.data===e.data
	//
	// A frontmatter value that is NOT a link never reaches that branch: it is a
	// String on both sides, and String vs the literal `"[[X]]"` compares the two
	// texts, which differ.
	obsidianLinkEqualsLink := func(frontmatterValue string) bool {
		v := strings.TrimSpace(frontmatterValue)
		if !strings.HasPrefix(v, "[[") || !strings.HasSuffix(v, "]]") {
			return v == wrappedLiteral
		}
		lhs, lok := records.ParseWikilink(v)
		rhs, rok := records.ParseWikilink(wrappedLiteral)
		if !lok || !rok {
			return false
		}
		lp, lres := resolve(lhs.Target)
		rp, rres := resolve(rhs.Target)
		if lres && rres {
			return lp == rp
		}
		return lhs.Target == rhs.Target
	}

	var disagreements []string
	for _, raw := range relationSpellings {
		obs := obsidianLinkEqualsLink(raw)
		ours, _ := ourRelationEquals(t, notes, raw, wrappedLiteral)
		if obs != ours {
			disagreements = append(disagreements, raw)
		}
		t.Logf("  %-46s obsidian=%-5v ours=%-5v", raw, obs, ours)
	}
	if len(disagreements) != 0 {
		t.Fatalf("the LINK-form literal was supposed to make both sides compute the same question\n"+
			"(resolved-target identity on both sides), but they disagreed on: %v.\n"+
			"If this is real, the refusal message must STOP telling the founder to make that edit.",
			disagreements)
	}

	// THE FALSIFIABILITY CHECK FOR THIS TEST, and it is not optional: "the two
	// sides agree" is worth nothing if the two sides agree on EVERYTHING,
	// including the bare literal that this whole file exists to refuse. If that
	// were so, either the refusal or this test would be wrong.
	var bareDisagreements int
	for _, raw := range relationSpellings {
		if obsidianLinkEqualsString(raw, danielLiteral) != mustOurs(t, notes, raw, wrappedLiteral) {
			bareDisagreements++
		}
	}
	if bareDisagreements == 0 {
		t.Errorf("the BARE literal agreed with this engine on every spelling too, which contradicts\n" +
			"TestRelationLiteral_BareLiteralIsNotFaithfulByConstruction. One of the two is wrong, and\n" +
			"a green result from this file should not be trusted until that is settled")
	}
}

func mustOurs(t *testing.T, notes *knowledge.NoteIndex, value, literal string) bool {
	t.Helper()
	v, _ := ourRelationEquals(t, notes, value, literal)
	return v
}

// ---------------------------------------------------------------------------
// AND NOW THE VAULT ITSELF
// ---------------------------------------------------------------------------

// TestRelationLiteral_FounderVaultHappensToCoincide is the SECOND kind of claim,
// labelled as such: on the founder's actual notes the two rules agree, so the
// wrapped translation would have returned exactly the Obsidian rows — here,
// today, on these files.
//
// It is written to be USELESS as a justification and useful as a fact. A
// coincidence over a hundred-odd files is not a property of the translation, and
// this test says so in its own output as well as in this comment, so that an
// agent who reads only the green result cannot mistake it for permission.
//
// It carries two falsifiability checks, which the FR-105 harness's own history
// makes mandatory: a view over a record type with no notes, or a filter that
// matches nothing, agrees with anything at all. Both of those states FAIL here
// rather than passing quietly.
func TestRelationLiteral_FounderVaultHappensToCoincide(t *testing.T) {
	vault := os.Getenv("OMNIPUS_KB_FIXTURE")
	if vault == "" {
		t.Skip("OMNIPUS_KB_FIXTURE is unset; this claim is about the founder's own notes and has no committed stand-in")
	}

	type ownerValue struct{ note, raw string }
	var paths []string
	var owners []ownerValue
	reType := regexp.MustCompile(`(?m)^type:[ \t]*(.+)$`)
	reOwner := regexp.MustCompile(`(?m)^owner:[ \t]*(.+)$`)

	err := filepath.WalkDir(vault, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			// An entry the walk cannot read is SKIPPED rather than fatal. The
			// grader checks below refuse to draw a conclusion from an empty or
			// zero-match census, so a partial walk cannot turn into false
			// evidence — whereas aborting on one unreadable entry would lose a
			// census that is otherwise sound.
			return nil //nolint:nilerr // deliberate skip; see the comment above
		}
		rel, rerr := filepath.Rel(vault, p)
		if rerr != nil {
			return nil //nolint:nilerr // a path that will not relativise against the root is not a note of this vault
		}
		paths = append(paths, filepath.ToSlash(rel))
		b, rerr := os.ReadFile(p) //nolint:gosec // a fixture path supplied by the operator's own env var
		if rerr != nil {
			return nil //nolint:nilerr // an unreadable note is skipped; the grader checks below catch a census gone empty
		}
		text := string(b)
		if !strings.HasPrefix(text, "---") {
			return nil
		}
		parts := strings.SplitN(text, "---", 3)
		if len(parts) < 3 {
			return nil
		}
		fm := parts[1]
		mt := reType.FindStringSubmatch(fm)
		if mt == nil || strings.Trim(strings.TrimSpace(mt[1]), `"'`) != "task" {
			return nil
		}
		if mo := reOwner.FindStringSubmatch(fm); mo != nil {
			owners = append(owners, ownerValue{note: filepath.ToSlash(rel), raw: strings.Trim(strings.TrimSpace(mo[1]), `"'`)})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the fixture vault: %v", err)
	}

	// GRADER CHECK 1 — a comparison over zero values agrees with anything.
	if len(owners) == 0 {
		t.Fatalf("no note of type `task` in %s carries an `owner`, so every comparison below would agree\n"+
			"vacuously and this test would be UNFALSIFIABLE. Either the fixture is not the founder's vault\n"+
			"or the frontmatter shape changed; do not read a pass from this state.", vault)
	}

	notes := knowledge.NewNoteIndex(paths)
	var matched int
	var divergent []string
	for _, ov := range owners {
		obs := obsidianLinkEqualsString(ov.raw, danielLiteral)
		wrapped, built := ourRelationEquals(t, notes, ov.raw, wrappedLiteral)
		if !built && strings.HasPrefix(ov.raw, "[[") {
			t.Fatalf("note %s holds a wikilink %q that this engine could not build a relation operand from",
				ov.note, ov.raw)
		}
		if obs {
			matched++
		}
		if obs != wrapped {
			divergent = append(divergent, ov.note+" -> "+ov.raw)
		}
	}

	// GRADER CHECK 2, the sharper of the two: if the Obsidian side matched
	// NOTHING, "they agree" is the trivial agreement of two empty row sets and
	// says nothing about the translation either.
	if matched == 0 {
		t.Fatalf("Obsidian's rule matched none of the %d task notes carrying an `owner`, so the agreement\n"+
			"below is between two empty row sets and is UNFALSIFIABLE. Do not count this as evidence.", len(owners))
	}

	if len(divergent) != 0 {
		sort.Strings(divergent)
		show := divergent
		if len(show) > 10 {
			show = show[:10]
		}
		t.Fatalf("FAITHFUL ON THIS DATA is FALSE: %d of %d task notes with an `owner` disagree between\n"+
			"Obsidian's raw-link-text rule and this engine's resolved-identity rule. First few: %v",
			len(divergent), len(owners), show)
	}

	t.Logf("FAITHFUL ON THIS DATA, AND ON THIS DATA ONLY: %d task notes carry an `owner`; the founder's own\n"+
		"clause matches %d of them and the wrapped translation matches exactly the same %d. That is a fact\n"+
		"about %d files, NOT a property of the translation. The claim that decides whether the clause may be\n"+
		"enabled is TestRelationLiteral_BareLiteralIsNotFaithfulByConstruction, and it says no.",
		len(owners), matched, matched, len(owners))
}

// ---------------------------------------------------------------------------
// THE SCOPE LIMIT ON "BY CONSTRUCTION", FOUND BY ASKING THE VAULT INSTEAD OF
// THE FIXTURE
//
// TestRelationLiteral_LinkFormLiteralMakesBothSidesAgreeByConstruction above
// says the link-form literal makes Obsidian and this engine compute the same
// question. That is true, and it is also NARROWER than it sounds, in a way its
// own fixture could not reveal: danielVaultPaths gives every target it tries a
// real note, so `resolve()` succeeds on both sides of every comparison and the
// UNRESOLVED branch of Obsidian's rule is never reached.
//
//	Link.looseEquals(Link):  n=this.resolve(), i=e.resolve()
//	                         return n && i ? n===i : this.data===e.data
//	                                         ^^^^^^^^^^^^^^^^^^^^^^^^^^
//
// When either side fails to resolve, Obsidian falls back to comparing the RAW
// TARGET TEXT — so `[[Jarvis]] == [[Jarvis]]` is TRUE for it even though no
// note named Jarvis exists. This engine has no such fallback: relation identity
// is the resolved record, and an unresolvable link has none, so the comparison
// is simply false.
//
// THIS IS NOT HYPOTHETICAL AND IT IS NOT RARE. In the founder's own vault only
// TWO of the sixteen distinct `owner` targets resolve to a note; the other
// fourteen — `[[Bruno-Backend-Developer]]`, `[[Jarvis]]`, `[[Grace-People-Lead]]`
// and the rest — are links to people who have no entity note yet. His
// "Needs Daniel" view happens to name one of the two that DO resolve, which is
// the only reason the advice in the refusal message is sound for it.
//
// The direction is NARROWING (ours ⊆ Obsidian's), which FR-105 permits, so this
// is not a second reason to refuse. It is a reason the refusal message must not
// over-promise: "write it in the link form" fixes the view when the target has
// a note, and produces an ENABLED, SILENTLY EMPTY view when it does not — which
// is a worse failure than the disabled one, because it looks like it worked.
// ---------------------------------------------------------------------------

// TestRelationLiteral_LinkFormAgreementIsScopedToResolvedTargets bounds the
// claim its neighbour makes, by running the same two rules over a target that
// has NO note.
func TestRelationLiteral_LinkFormAgreementIsScopedToResolvedTargets(t *testing.T) {
	// Daniel has a note; Jarvis does not. This is the founder's real situation,
	// not a contrived one — see the census in the block comment above.
	notes := knowledge.NewNoteIndex([]string{"05-Maps/Entities/Daniel Piatkowski.md"})

	resolves := func(target string) bool {
		rl := notes.Resolve("", knowledge.Link{Kind: knowledge.LinkWikilink, Target: target})
		return rl.State == knowledge.ResolveResolved
	}

	const unresolved = "[[Jarvis]]"
	const resolved = "[[Daniel Piatkowski]]"

	if resolves("Jarvis") {
		t.Fatalf("%s was supposed to be the UNRESOLVED case and it resolved; this test proves nothing "+
			"in that state", unresolved)
	}
	if !resolves(danielLiteral) {
		t.Fatalf("%s was supposed to be the RESOLVED case and it did not resolve; this test proves "+
			"nothing in that state", resolved)
	}

	// Obsidian, for two identical unresolvable links, takes the `this.data ===
	// e.data` branch quoted above and says they are EQUAL.
	const obsidianSaysEqual = true

	ours, built := ourRelationEquals(t, notes, unresolved, unresolved)
	if !built {
		t.Fatalf("a relation operand could not be built from %s on either side, so the comparison "+
			"below would be measuring operand construction rather than identity", unresolved)
	}
	if ours != false {
		t.Fatalf("this engine was expected to answer FALSE for %s == %s (an unresolvable link has no "+
			"resolved record to be identical to), but it answered %v. If this engine has gained a "+
			"raw-target fallback, the scope limit this test documents is gone and the neighbouring "+
			"'by construction' claim can be widened.", unresolved, unresolved, ours)
	}
	t.Logf("UNRESOLVED target %s:  obsidian=%v  ours=%v  -> narrowed (FR-105 permits, but the view is "+
		"enabled and EMPTY)", unresolved, obsidianSaysEqual, ours)

	// THE FALSIFIABILITY CHECK. "The two sides differ" is worth nothing if they
	// differ on everything, including the resolved case the refusal message
	// actually sends the founder to. The SAME comparison on a target that DOES
	// resolve must agree — that is what makes the divergence above a property of
	// resolvability rather than of relations in general.
	oursResolved, builtResolved := ourRelationEquals(t, notes, resolved, resolved)
	if !builtResolved || !oursResolved {
		t.Fatalf("the RESOLVED control case failed: %s == %s should be true on both sides (Obsidian "+
			"by n===i, this engine by resolved-record identity), got ours=%v built=%v. Without this "+
			"control, the divergence above could be a relation bug rather than a resolvability limit.",
			resolved, resolved, oursResolved, builtResolved)
	}
	t.Logf("RESOLVED   target %s:  obsidian=true  ours=%v  -> agree, which is why the refusal "+
		"message's advice is sound for the founder's own view", resolved, oursResolved)
}

// TestRelationLiteral_FounderVaultHasUnresolvedOwners is the fact behind the
// scope limit, read off the founder's notes rather than asserted.
//
// It exists so that the limit cannot be dismissed as a contrived edge case: if
// the vault ever grows entity notes for all of these, this test FAILS and
// whoever sees it can widen the message's advice with evidence in hand.
func TestRelationLiteral_FounderVaultHasUnresolvedOwners(t *testing.T) {
	vault := os.Getenv("OMNIPUS_KB_FIXTURE")
	if vault == "" {
		t.Skip("OMNIPUS_KB_FIXTURE is unset; this claim is about the founder's own notes and has no committed stand-in")
	}

	var paths []string
	targets := map[string]int{}
	reOwner := regexp.MustCompile(`(?m)^owner:[ \t]*(.+)$`)

	err := filepath.WalkDir(vault, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			// An entry the walk cannot read is SKIPPED rather than fatal. The
			// grader checks below refuse to draw a conclusion from an empty or
			// zero-match census, so a partial walk cannot turn into false
			// evidence — whereas aborting on one unreadable entry would lose a
			// census that is otherwise sound.
			return nil //nolint:nilerr // deliberate skip; see the comment above
		}
		rel, rerr := filepath.Rel(vault, p)
		if rerr != nil {
			return nil //nolint:nilerr // a path that will not relativise against the root is not a note of this vault
		}
		paths = append(paths, filepath.ToSlash(rel))
		b, rerr := os.ReadFile(p) //nolint:gosec // a fixture path supplied by the operator's own env var
		if rerr != nil {
			return nil //nolint:nilerr // an unreadable note is skipped; the grader checks below catch a census gone empty
		}
		text := string(b)
		if !strings.HasPrefix(text, "---") {
			return nil
		}
		parts := strings.SplitN(text, "---", 3)
		if len(parts) < 3 {
			return nil
		}
		if mo := reOwner.FindStringSubmatch(parts[1]); mo != nil {
			raw := strings.Trim(strings.TrimSpace(mo[1]), `"'`)
			if link, ok := records.ParseWikilink(raw); ok {
				targets[link.Target]++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the fixture vault: %v", err)
	}

	// GRADER CHECK — a census over zero targets says nothing.
	if len(targets) == 0 {
		t.Fatalf("no note in %s carries an `owner` holding a wikilink, so the census below is empty and "+
			"UNFALSIFIABLE; do not read a pass from this state", vault)
	}

	notes := knowledge.NewNoteIndex(paths)
	var resolved, unresolved []string
	for target := range targets {
		rl := notes.Resolve("", knowledge.Link{Kind: knowledge.LinkWikilink, Target: target})
		if rl.State == knowledge.ResolveResolved {
			resolved = append(resolved, target)
		} else {
			unresolved = append(unresolved, target)
		}
	}
	sort.Strings(resolved)
	sort.Strings(unresolved)

	t.Logf("distinct `owner` targets: %d resolve %v", len(resolved), resolved)
	t.Logf("                          %d do NOT resolve %v", len(unresolved), unresolved)

	if len(unresolved) == 0 {
		t.Fatalf("every `owner` target now resolves to a note. The scope limit documented in\n" +
			"TestRelationLiteral_LinkFormAgreementIsScopedToResolvedTargets is then no longer\n" +
			"reachable through this vault's own data, and the refusal message's advice may be\n" +
			"stated without the caveat about silently-empty views. Re-check before widening it.")
	}

	// The founder's own view names a target that DOES resolve — which is the
	// whole reason the advice is safe to give him. If that ever stops being
	// true, the message is sending him to an empty view.
	if !slicesContains(resolved, danielLiteral) {
		t.Fatalf("%q does not resolve in this vault, so telling the founder to write the literal as\n"+
			"[[%s]] would enable his view and return NOTHING. The refusal message must stop giving\n"+
			"that advice until an entity note for him exists.", danielLiteral, danielLiteral)
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
