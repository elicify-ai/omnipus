// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

// stamp.go — writing an ADR-068 D7 identifier onto every note in a vault that
// is a record and does not have one.
//
// # Why this exists
//
// Inline record editing needs a row to carry BOTH a version token and an `id`.
// The token comes from the bytes and is always present; the `id` is a
// frontmatter key something has to WRITE. Importing a real Obsidian vault
// wrote none: the importer infers schemas and translates `.base` files, and
// stamped no identifiers at all. On the founder's own 766-note vault that is 0
// of 766 notes carrying an `id:`, so every row arrives with `id: null`,
// resolveEditTarget returns undefined, and the whole inline-editing feature is
// invisible with nothing on screen to say why.
//
// This file is the vault-wide pass that fixes that, and it serves BOTH of the
// founder's remaining asks from one implementation:
//
//   - "mint during import" — StampIdentities runs as a phase of Run.
//   - "the already imported vault needs the update" — the same function is
//     the whole body of `omnipus records stamp-ids`.
//
// They are the same code because the operation is the same operation, and
// because it is IDEMPOTENT BY CONSTRUCTION: the only notes it touches are
// records with no identifier, so a second run finds nothing to do. That is not
// a property bolted on for the repair command — it is what makes stamping
// safe to run as part of an import that an operator may repeat.
//
// # What it must never do
//
// Never stamp a note whose record type it could not determine. The importer's
// existing discipline is to REPORT what it cannot classify rather than guess,
// and an identifier written under a type that was a guess is worse than no
// identifier: it is a wrong answer that looks authoritative. So the gate is
// narrow and explicit — the note declares a `type:`, and that type resolves to
// a schema this vault actually declares. Everything else is counted, named
// where it is useful, and left alone.

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// StampVault adds a missing `id:` to every record note in an
// ALREADY-IMPORTED vault, and changes nothing else.
//
// This is the founder's third ask — "the already imported vault needs the
// update" — and it is deliberately NOT a re-run of the importer. A full
// re-import would re-infer every schema and re-translate every `.base` file
// against a vault whose notes have since been edited, which can legitimately
// change a schema an operator has since corrected by hand. Repairing a missing
// identifier must not be able to do that. So this reads the schemas the
// earlier import already wrote, and the ONLY thing it can change on disk is
// the addition of an `id:` line to a note that has none.
//
// IDEMPOTENT: running it twice changes nothing the second time, because the
// only notes it touches are records with no identifier and the first run left
// none. That is a property of the pass itself, not a check bolted on top.
//
// It requires the vault to have been imported already — without
// `.omnipus-vault/records/*.yaml` there are no schemas, so no note's `type:`
// resolves, so nothing is a record and nothing would be stamped. That is
// reported as a refusal rather than as a cheerful "0 notes stamped", because
// the two states look identical in a count and need completely different
// actions from the operator.
func StampVault(vaultRoot string, opts Options) (IdentityStampReport, error) {
	inv, err := ScanVault(vaultRoot)
	if err != nil {
		return IdentityStampReport{}, err
	}
	notes, _, err := LoadNotes(inv)
	if err != nil {
		return IdentityStampReport{}, err
	}
	set, _, err := records.LoadSchemas(inv.Root)
	if err != nil {
		return IdentityStampReport{}, fmt.Errorf("vaultimport: loading this vault's record schemas: %w", err)
	}
	if set == nil || len(set.Types()) == 0 {
		return IdentityStampReport{}, fmt.Errorf(
			"this vault declares no record types in %s/%s — it has not been imported yet, so no note is a record and stamping would do nothing; run `omnipus records import-obsidian --vault %s` first",
			records.VaultMarkerDirName, records.RecordsDirName, vaultRoot)
	}
	return StampIdentities(inv.Root, notes, set, opts.LockDir, opts.Write), nil
}

// IdentityStamp is one note's stamping outcome.
type IdentityStamp struct {
	// RelPath is the note, vault-relative.
	RelPath string
	// RecordType is the type whose allocator minted the identifier.
	RecordType string
	// ID is the identifier written, or — on a dry run — the identifier a real
	// run would write. Never empty on an entry in Stamped.
	ID string
	// Written reports whether the note's file was actually modified. It is
	// false on a dry run even though ID is set, so a dry run's report says
	// what it WOULD write without claiming it did. This mirrors
	// TypeInferenceOutcome.Written exactly.
	Written bool
}

// IdentityStampFailure is one note that should have been stamped and was not,
// named with the reason.
//
// A failure is its own outcome rather than being folded into "not a record",
// which would misattribute a filesystem problem to the classification and
// leave the operator looking for a schema that is not the problem.
type IdentityStampFailure struct {
	RelPath    string
	RecordType string
	ID         string
	Reason     string
}

// IdentityStampReport is the complete account of one stamping pass.
//
// The four populations below PARTITION every note the pass considered, and
// renderIdentityStamps re-derives that sum and prints a CONTRADICTION line if
// it does not hold. A report whose numbers do not add up is how a silent skip
// hides inside a healthy-looking summary.
type IdentityStampReport struct {
	// DryRun is true when nothing was written to any note.
	DryRun bool
	// Stamped is every note that received (or would receive) an identifier.
	Stamped []IdentityStamp
	// AlreadyIdentified counts notes that are records and already carry an
	// `id:` (or `omni_id:`). They are NOT touched — that is the idempotence
	// guarantee, and re-stamping one would break a link an operator or the
	// SPA already holds.
	AlreadyIdentified int
	// NotRecords counts notes that declare no `type:` at all. The majority of
	// every real vault. Not an error, not a gap.
	NotRecords int
	// UndeclaredType counts notes that DO declare a `type:` for which this
	// vault declares no schema, mapped from type name to how many notes.
	//
	// This is the population that matters most in the report: these notes look
	// like records to their author and are not records to Omnipus, so they get
	// no identifier and no inline editing, and the operator cannot discover
	// why unless they are named. Guessing a type for them is exactly what this
	// pass refuses to do.
	UndeclaredType map[string]int
	// Failures is every note that qualified for an identifier and did not get
	// one, named with the reason.
	Failures []IdentityStampFailure
	// TypeFailures is every record type whose allocator could not run at all
	// (a corrupt `.seq` counter, a held lock) — so NONE of its notes were
	// stamped. Kept separate from Failures because the cause is the type's,
	// not any one note's.
	TypeFailures map[string]string
	// Collisions is every identifier the allocator skipped because a note
	// already carries it. Surfaced because a counter that jumps is alarming
	// and this is the only thing that explains it.
	Collisions []knowledge.RecordIdentityCollision
}

// Considered returns how many notes the pass looked at. It is the partition
// sum the renderer checks itself against.
func (r IdentityStampReport) Considered() int {
	n := len(r.Stamped) + r.AlreadyIdentified + r.NotRecords + len(r.Failures)
	for _, c := range r.UndeclaredType {
		n += c
	}
	return n
}

// StampIdentities writes an `id:` into every note in notes that is a record of
// a type set declares and carries no identifier yet.
//
// It MUTATES notes in place — each stamped note's Rec is re-parsed from the
// bytes just written — for the same reason adoptTypeInMemory does: the run's
// own validation pass and its post-write discriminator check read this slice,
// and a report built from a stale in-memory view contradicts the files the run
// just changed.
//
// write=false is `--dry-run`: identifiers are computed through the real
// allocator (so the report names the identifiers a real run produces) but the
// counter is not advanced and not one note byte is written.
func StampIdentities(vaultRoot string, notes []NoteRecord, set *records.SchemaSet, lockDir string, write bool) IdentityStampReport {
	rep := IdentityStampReport{
		DryRun:         !write,
		UndeclaredType: map[string]int{},
		TypeFailures:   map[string]string{},
	}
	if set == nil {
		// Every note is then an undeclared type or an ordinary note. Counting
		// them is still the honest answer; refusing to run is not.
		set = &records.SchemaSet{}
	}

	// Partition first, mint second. Grouping by type is what lets each type's
	// identifiers be allocated in ONE locked pass instead of one walk of the
	// whole vault per note.
	byType := map[string][]stampCandidate{}
	for i := range notes {
		rec := notes[i].Rec
		typeName := rec.TypeName()
		if typeName == "" {
			rep.NotRecords++
			continue
		}
		sc, ok := set.Get(typeName)
		if !ok {
			rep.UndeclaredType[typeName]++
			continue
		}
		if rec.ID() != "" {
			rep.AlreadyIdentified++
			continue
		}
		byType[typeName] = append(byType[typeName], stampCandidate{idx: i, schema: sc})
	}

	lock := knowledge.NoteLockConfig{CollectionRoot: vaultRoot, LockDir: lockDir}
	for _, typeName := range sortedStampTypes(byType) {
		group := byType[typeName]
		mint := knowledge.MintRecordIDs
		if !write {
			mint = knowledge.PreviewRecordIDs
		}
		ids, collisions, err := mint(lock, vaultRoot, group[0].schema, len(group))
		rep.Collisions = append(rep.Collisions, collisions...)
		if err != nil {
			// The whole type fails together: without identifiers there is
			// nothing to write for any of its notes. Naming the type once
			// beats naming the same cause on every one of its notes.
			rep.TypeFailures[typeName] = err.Error()
			continue
		}

		for i, c := range group {
			note := &notes[c.idx]
			id := ids[i]
			if !write {
				rep.Stamped = append(rep.Stamped, IdentityStamp{
					RelPath: note.RelPath, RecordType: typeName, ID: id, Written: false,
				})
				continue
			}
			if werr := writeRecordIdentity(note.AbsPath, id); werr != nil {
				rep.Failures = append(rep.Failures, IdentityStampFailure{
					RelPath: note.RelPath, RecordType: typeName, ID: id, Reason: werr.Error(),
				})
				continue
			}
			// Re-read rather than trusting the in-memory splice result: the
			// bytes that matter are the bytes ON DISK, and re-parsing them is
			// the only way the run's later validation reflects the file an
			// operator would open.
			if data, rerr := os.ReadFile(note.AbsPath); rerr == nil {
				note.Rec = records.ParseRecord(note.RelPath, data)
			}
			rep.Stamped = append(rep.Stamped, IdentityStamp{
				RelPath: note.RelPath, RecordType: typeName, ID: id, Written: true,
			})
		}
	}

	sort.Slice(rep.Stamped, func(i, j int) bool { return rep.Stamped[i].RelPath < rep.Stamped[j].RelPath })
	sort.Slice(rep.Failures, func(i, j int) bool { return rep.Failures[i].RelPath < rep.Failures[j].RelPath })
	sort.Slice(rep.Collisions, func(i, j int) bool { return rep.Collisions[i].Candidate < rep.Collisions[j].Candidate })
	return rep
}

// stampCandidate is one note queued for an identifier, with the schema whose
// allocator will mint it. The index refers back into the caller's notes slice,
// which StampIdentities mutates in place.
type stampCandidate struct {
	idx    int
	schema *records.Schema
}

// sortedStampTypes orders the types so a run is deterministic and its report
// is diffable against the previous one. Map iteration order would otherwise
// make two runs over an identical vault hand the same notes different
// identifiers, which turns "nothing changed" into an unreadable diff.
func sortedStampTypes(byType map[string][]stampCandidate) []string {
	out := make([]string, 0, len(byType))
	for t := range byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// renderIdentityStamps prints the identifier pass.
//
// ALWAYS PRINTED, even when nothing was stamped — unlike the optional sections
// above it, which stay silent when empty. The reason is the defect this whole
// feature exists to remove: a vault where no note has an identifier looks
// EXACTLY like a healthy vault from the outside, and the operator's only
// symptom is that inline editing does not appear, with nothing to explain it.
// A section that renders "0 notes were given an identifier, and here is why"
// is the difference between a diagnosable state and a mystery.
func (r *Report) renderIdentityStamps(w io.Writer) {
	r.IdentityStamps.Render(w, r.Validation.TotalNotes)
}

// Render writes the identifier pass's account.
//
// totalNotes is how many notes the run loaded, used only for the
// self-contradiction check at the end; pass 0 to skip it.
func (s IdentityStampReport) Render(w io.Writer, totalNotes int) {
	verb, tail := "were given", ""
	if s.DryRun {
		verb, tail = "would be given", " (dry run: nothing was written)"
	}
	fmt.Fprintf(w, "\n-- %d note(s) %s a record identifier (ADR-068 D7)%s --\n", len(s.Stamped), verb, tail)
	fmt.Fprintf(w, "  Inline record editing needs BOTH a version token and an `id:`. A record with no identifier cannot be edited in place and the UI cannot say why, so every record gets one.\n")
	fmt.Fprintf(w, "  %d already carried one and were NOT touched, %d notes carry no `type:` at all.\n",
		s.AlreadyIdentified, s.NotRecords)

	byType := map[string][]IdentityStamp{}
	for _, st := range s.Stamped {
		byType[st.RecordType] = append(byType[st.RecordType], st)
	}
	for _, t := range sortedStampReportTypes(byType) {
		group := byType[t]
		fmt.Fprintf(w, "  %s: %d note(s), %s … %s\n", t, len(group), group[0].ID, group[len(group)-1].ID)
	}

	// The population that the operator can ACT on, and the one a silent run
	// would hide: these notes declare a type, so their author plainly means
	// them as records, but this vault declares no schema for that type. They
	// get no identifier and no inline editing. Stamping them anyway would mean
	// guessing a type, which is the one thing this pass refuses to do.
	if len(s.UndeclaredType) > 0 {
		total := 0
		for _, c := range s.UndeclaredType {
			total += c
		}
		fmt.Fprintf(w, "  %d note(s) declare a `type:` this vault has no schema for, so they were deliberately NOT stamped — an identifier written under a guessed type is a wrong answer that looks authoritative:\n", total)
		for _, t := range sortedCountKeys(s.UndeclaredType) {
			fmt.Fprintf(w, "    type %q: %d note(s)\n", t, s.UndeclaredType[t])
		}
	}

	if len(s.Collisions) > 0 {
		fmt.Fprintf(w, "  %d identifier(s) were already in use, so the counter advanced past them (this is why a sequence may jump):\n", len(s.Collisions))
		for _, c := range s.Collisions {
			fmt.Fprintf(w, "    %s is held by %s\n", c.Candidate, c.HeldBy)
		}
	}
	for _, t := range sortedCountKeys(mapLen(s.TypeFailures)) {
		fmt.Fprintf(w, "  type %q: NO note was stamped — %s\n", t, s.TypeFailures[t])
	}
	for _, f := range s.Failures {
		fmt.Fprintf(w, "  %s: could not be stamped with %s — %s\n", f.RelPath, f.ID, f.Reason)
	}

	// The self-contradiction check every other section in this report makes.
	// The four populations must account for every note the run loaded; if they
	// do not, a note went missing from an answer that claims to be complete,
	// and saying so is better than a tidy total that is wrong.
	if totalNotes > 0 && s.Considered() != totalNotes {
		fmt.Fprintf(w, "  CONTRADICTION — this section accounts for %d notes but the run loaded %d. A note is missing from this account; treat the counts above as incomplete.\n",
			s.Considered(), totalNotes)
	}
}

// sortedStampReportTypes orders the per-type lines deterministically.
func sortedStampReportTypes(byType map[string][]IdentityStamp) []string {
	out := make([]string, 0, len(byType))
	for t := range byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// sortedCountKeys orders a name->count map's keys.
func sortedCountKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mapLen adapts a name->reason map to sortedCountKeys' shape, so the two
// failure listings sort identically instead of one of them riding Go's
// randomised map order.
func mapLen(m map[string]string) map[string]int {
	out := make(map[string]int, len(m))
	for k := range m {
		out[k] = 1
	}
	return out
}

// writeRecordIdentity splices `id: <value>` into one note on disk.
//
// THE WRITE IS A SPLICE, AND THE PROOF OBLIGATION IS BYTE-IDENTITY OUTSIDE THE
// INSERTED LINE. knowledge.SpliceRecordIdentity does the edit — the SAME
// primitive the record-create door uses — so comments, key order, blank lines
// and quoting style all survive. This function's own contribution is the two
// things a file, as opposed to a byte slice, needs:
//
//  1. The note's existing MODE is preserved rather than replaced with the
//     control plane's 0600. This is the operator's own document, not a
//     generated file — writeTypeKey established the same rule.
//  2. The `id:` is written only if the note still has no `id:` at the moment
//     it is read here. The pass already checked, but it checked against a
//     snapshot loaded earlier in the run; re-asking against the bytes on disk
//     is what stops a concurrent writer's identifier being overwritten by
//     this one.
func writeRecordIdentity(absPath, id string) error {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read the note: %w", err)
	}
	current := records.ParseRecord(absPath, src)
	if existing := current.ID(); existing != "" {
		return fmt.Errorf("the note already declares `%s: %s`; refusing to overwrite an identifier",
			records.RecordIDKey, existing)
	}
	out, err := knowledge.SpliceRecordIdentity(src, id)
	if err != nil {
		return err
	}

	mode := os.FileMode(0o644)
	if fi, statErr := os.Stat(absPath); statErr == nil {
		mode = fi.Mode().Perm()
	}
	if werr := os.WriteFile(absPath, out, mode); werr != nil {
		return fmt.Errorf("write the note: %w", werr)
	}
	return nil
}
