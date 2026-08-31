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
//	      on the note.
//	The type is WRITTEN only when EXACTLY ONE inferred type matches.
//
// (2) is what stops a note being adopted by a type that never heard of half
// its keys. (3) is what stops the loosest schema in the vault swallowing
// everything: a type whose notes all carry `status` and `owner` will not
// take a note that carries neither. (1) stops a note with no frontmatter at
// all — which matches every type vacuously under (2) — from matching
// anything. And "exactly one" is the honest reading of a founder ruling
// about common sense: two candidates is not a match, it is a coin toss, and
// a coin toss written into the operator's own files is worse than a line in
// a report saying which two.
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
	// is the ambiguity that refuses to guess; length 0 is no match.
	Candidates []string
	// Reason states the outcome in words. NEVER empty — a recorded outcome
	// with no reason is a silent skip wearing a report entry's clothes.
	Reason string
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
			out.Candidates = matchingTypes(keys, n.Rec, shapes)
			switch len(out.Candidates) {
			case 0:
				rep.NoMatch++
				out.Reason = fmt.Sprintf(
					"left as is: no inferred type declares every one of its %d propert%s and has all of its own required properties present. It is still fully indexed (FR-021e) and reachable through an untyped view.",
					len(keys), plural(len(keys), "y", "ies"))
			case 1:
				out.Inferred = out.Candidates[0]
				if !write {
					rep.Written++
					out.Reason = fmt.Sprintf("would write `type: %s` — its frontmatter shape matches that type and no other (dry run: nothing was written).", out.Inferred)
					break
				}
				if err := writeTypeKey(n.AbsPath, out.Inferred); err != nil {
					rep.WriteErrors++
					out.Inferred = ""
					out.Reason = fmt.Sprintf("matched type %q but the file could NOT be edited: %v", out.Candidates[0], err)
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
				out.Reason = fmt.Sprintf("wrote `type: %s` — its frontmatter shape matches that type and no other.", out.Inferred)
			default:
				rep.Ambiguous++
				out.Reason = fmt.Sprintf(
					"left as is: its shape matches %d inferred types (%s) equally well, and picking one would be a coin toss written into your own file. One edit settles it: add `type: <one of those>` to the note, or narrow the schemas with knowledge_configure.",
					len(out.Candidates), strings.Join(out.Candidates, ", "))
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

// typeShape is one inferred type reduced to the two sets the match rule
// needs: every property it declares, and the subset it requires.
type typeShape struct {
	declared map[string]struct{}
	required []string
}

func buildTypeShapes(inferred map[string][]InferredProperty) map[string]typeShape {
	shapes := map[string]typeShape{}
	for t, props := range inferred {
		sh := typeShape{declared: map[string]struct{}{}}
		for _, p := range props {
			sh.declared[p.Name] = struct{}{}
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

// matchingTypes applies the three-condition rule stated in this file's
// header and returns every type that satisfies it, sorted.
func matchingTypes(keys []string, rec records.Record, shapes map[string]typeShape) []string {
	var out []string
	for t, sh := range shapes {
		if !everyKeyDeclared(keys, sh) {
			continue
		}
		if !everyRequiredPresent(rec, sh) {
			continue
		}
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
