// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// record_identity.go — minting ADR-068 D7 record identifiers, for every door
// that brings a record note into existence.
//
// # Why this file exists
//
// Inline record editing needs a row to carry BOTH a version token and an `id`.
// The token is computed from the bytes, so it is always there. The `id` is a
// frontmatter key (records.RecordIDKey) that something has to WRITE, and until
// this file existed only ONE door did: the REST record-create handler. Every
// other way a record note comes into being — the `knowledge_edit` create op,
// `knowledge_create_note`, and above all `omnipus records import-obsidian`
// pointed at a real Obsidian vault — produced records with no identifier at
// all. On the founder's own 766-note vault that was 0 of 766 notes carrying an
// `id:`, which makes the entire inline-editing feature invisible with nothing
// on screen to explain why.
//
// So identity minting is moved to a CHOKE POINT rather than being added to
// each caller in turn. CreateNote (author.go) is the single function all three
// in-process creation doors already funnel through, and stampNewRecordIdentity
// below runs there — so a record note created from scratch acquires an `id`
// without the caller asking, and a door added later inherits the behaviour
// instead of re-introducing the gap.
//
// # The on-disk protocol is the contract, not this code
//
// pkg/gateway's own allocateRecordID predates this file and still carries its
// own copy of the same algorithm (that file is owned elsewhere and is not
// edited here). The two implementations interoperate CORRECTLY — and must
// keep doing so — because they agree on the three things that actually matter,
// none of which is a shared Go symbol:
//
//  1. The counter file: <collection>/.omnipus-vault/records/<type>.seq, holding
//     the last value handed out as a decimal integer.
//  2. The lock key: ".omnipus-vault/records/<type>.seq" passed to
//     WithNoteWriteLock, so two minters of the same type contend for the
//     identical striped mutex and advisory file lock.
//  3. The rendering: "<prefix>-%04d", or a bare "%04d" when the schema
//     declares no identity prefix.
//
// Change any of those three here and the two allocators can hand the same
// identifier to two records. RenderRecordIdentity's test pins the rendering
// against D7's own worked example ("CO-0142") for exactly that reason.
//
// # Lock ordering — mint BEFORE the note lock, never inside it
//
// WithNoteWriteLock keys a 64-shard striped pool, so two DIFFERENT keys can
// land on the SAME shard. Taking the `.seq` lock while already holding a
// note's write lock would therefore self-contend whenever the two keys collide
// (1 in 64), and a plain sync.Mutex is not reentrant — it surfaces as an
// inexplicable lock timeout on 1.6% of creates. Every minting call site here
// runs to completion BEFORE the note's own lock is taken.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// maxRecordIdentityMintAttempts bounds how far the allocator scans forward
// past live collisions before giving up. Reached only when a vault holds that
// many consecutive hand-written identifiers above the counter.
const maxRecordIdentityMintAttempts = 10000

// mintedInThisPass marks an identifier reserved earlier in the SAME batch, so
// a batch cannot hand the same value out twice. It is a sentinel "holder
// path", never a real one — the map it lives in is otherwise id -> the note
// that already carries it.
const mintedInThisPass = "(minted in this pass)"

// RenderRecordIdentity renders D7's "CO-0142" shape: four digits minimum,
// widening rather than wrapping past 9999.
//
// A SCHEMA THAT DECLARES NO `identity: {prefix: …}` MINTS A BARE ZERO-PADDED
// NUMBER — "0001", not "-0001" and not a refusal. That is a deliberate,
// documented decision, not an accident of formatting:
//
//   - identity_prefix is OPTIONAL on the wire and in the schema file, so a
//     vault that never declares one is a legal vault, and the majority of
//     inferred schemas from a real Obsidian import declare none.
//   - An identifier is what makes a record findable and editable at all. The
//     alternative — refusing to mint, or leaving the note unstamped — is the
//     exact defect this file exists to remove, so "no prefix" must not be a
//     second way to end up with no id.
//   - Uniqueness is scoped PER TYPE (D7: "unique within its type"), and the
//     collision scan filters by the note's own `type:`. Two prefix-less types
//     both holding a record numbered "0001" is therefore correct and not a
//     collision — they are different records of different types, exactly as
//     "CO-0001" and "WI-0001" would be.
func RenderRecordIdentity(sc *records.Schema, n int64) string {
	if sc == nil || sc.Identity.Prefix == "" {
		return fmt.Sprintf("%04d", n)
	}
	return fmt.Sprintf("%s-%04d", sc.Identity.Prefix, n)
}

// RecordIdentityCollision reports one identifier the allocator skipped because
// a note already carries it.
//
// Returned rather than logged. A counter that jumps from 12 to 431 is
// alarming, and the ONLY thing that explains it is the path of the note
// holding the identifier in the way — so that path is carried out to whoever
// can actually show it to the operator (the importer's report, a command's
// output), instead of being dropped into a log line nobody correlates.
type RecordIdentityCollision struct {
	// Candidate is the identifier that was already taken.
	Candidate string
	// HeldBy is the collection-relative path of the note holding it.
	HeldBy string
}

// MintRecordIDs allocates n identifiers for sc's record type, in one pass
// under the type's `.seq` lock, and ADVANCES the persisted counter.
//
// Batch rather than one-at-a-time because the collision scan costs a walk of
// the whole collection: minting 766 identifiers one call at a time is 766
// walks, which on the founder's vault is ~590,000 file reads for a single
// import. The set of live identifiers cannot change while this lock is held,
// so reading it once and probing memory is the same answer, faster.
//
// The counter is advanced to the last value actually handed out, so a crash
// mid-import never re-issues an identifier a note on disk already carries.
func MintRecordIDs(lock NoteLockConfig, collectionRoot string, sc *records.Schema, n int) ([]string, []RecordIdentityCollision, error) {
	return mintRecordIDs(lock, collectionRoot, sc, n, true)
}

// PreviewRecordIDs returns the identifiers MintRecordIDs WOULD hand out,
// without advancing the persisted counter and without writing anything.
//
// It exists for `--dry-run`. The alternative — reporting "766 notes would be
// stamped" without saying with what — is the weaker answer, and the only way
// to name the actual identifiers is to run the real allocator. So this runs
// exactly the same code over exactly the same inputs, under the same lock, and
// skips ONE step: persisting the counter. A dry run therefore reports the
// identifiers a real run produces, and leaves the vault byte-identical.
func PreviewRecordIDs(lock NoteLockConfig, collectionRoot string, sc *records.Schema, n int) ([]string, []RecordIdentityCollision, error) {
	return mintRecordIDs(lock, collectionRoot, sc, n, false)
}

func mintRecordIDs(lock NoteLockConfig, collectionRoot string, sc *records.Schema, n int, persist bool) ([]string, []RecordIdentityCollision, error) {
	if strings.TrimSpace(collectionRoot) == "" {
		return nil, nil, fmt.Errorf("knowledge: mint record identifiers: empty collection root")
	}
	if sc == nil {
		return nil, nil, fmt.Errorf("knowledge: mint record identifiers: nil schema")
	}
	if n <= 0 {
		return nil, nil, nil
	}

	cfg := lock
	cfg.CollectionRoot = collectionRoot
	seqPath := filepath.Join(collectionRoot, records.VaultMarkerDirName, records.RecordsDirName, sc.Type+".seq")
	lockKey := records.VaultMarkerDirName + "/" + records.RecordsDirName + "/" + sc.Type + ".seq"

	var (
		out        []string
		collisions []RecordIdentityCollision
	)
	lockErr := WithNoteWriteLock(cfg, lockKey, func() error {
		next, err := nextRecordSequenceValue(seqPath)
		if err != nil {
			return err
		}
		// An unreadable note is an ERROR here, never a skip: a note whose
		// bytes cannot be read might hold the very identifier about to be
		// minted, and treating "I could not look" as "it is free" hands out a
		// duplicate and quietly breaks the D7 uniqueness invariant this scan
		// exists to hold.
		live, err := liveRecordIdentifiers(collectionRoot, sc)
		if err != nil {
			return err
		}

		out = make([]string, 0, n)
		last := int64(0)
		for attempts := 0; len(out) < n; {
			candidate := RenderRecordIdentity(sc, next)
			if holder, taken := live[candidate]; taken {
				collisions = append(collisions, RecordIdentityCollision{Candidate: candidate, HeldBy: holder})
				next++
				attempts++
				if attempts >= maxRecordIdentityMintAttempts {
					return fmt.Errorf("could not mint %d unique identifier(s) for record type %q: "+
						"%d consecutive candidates are already in use", n, sc.Type, attempts)
				}
				continue
			}
			out = append(out, candidate)
			live[candidate] = mintedInThisPass
			last = next
			next++
		}
		if !persist {
			return nil
		}
		return writeRecordSequenceValue(seqPath, last)
	})
	if lockErr != nil {
		return nil, nil, lockErr
	}
	return out, collisions, nil
}

// nextRecordSequenceValue reads the persisted counter and returns the NEXT
// value to try.
//
// A missing or empty file starts the sequence at 1. A file whose content does
// not parse as a non-negative integer is a GENUINE FAULT, reported rather than
// silently reset: resetting a corrupted counter to zero re-issues every
// identifier it already handed out.
func nextRecordSequenceValue(seqPath string) (int64, error) {
	data, err := os.ReadFile(seqPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, fmt.Errorf("read identity sequence %q: %w", seqPath, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return 1, nil
	}
	n, perr := strconv.ParseInt(text, 10, 64)
	if perr != nil || n < 0 {
		return 0, fmt.Errorf("identity sequence %q holds %q, which is not a non-negative integer", seqPath, text)
	}
	return n + 1, nil
}

// writeRecordSequenceValue persists the counter atomically.
func writeRecordSequenceValue(seqPath string, n int64) error {
	if err := os.MkdirAll(filepath.Dir(seqPath), 0o700); err != nil {
		return fmt.Errorf("create identity sequence directory for %q: %w", seqPath, err)
	}
	if err := fileutil.WriteFileAtomic(seqPath, []byte(strconv.FormatInt(n, 10)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write identity sequence %q: %w", seqPath, err)
	}
	return nil
}

// liveRecordIdentifiers collects every identifier the collection currently
// holds for sc's type, mapped to the path holding it.
//
// WalkContained never descends into .omnipus-vault/ (FR-038a), so a trashed
// copy of a note can never block an identifier the live vault has released.
func liveRecordIdentifiers(collectionRoot string, sc *records.Schema) (map[string]string, error) {
	fsys := OSLinkFS()
	root, err := NewCollectionRoot(fsys, collectionRoot)
	if err != nil {
		return nil, err
	}
	wr, err := WalkContained(fsys, root)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(wr.Files))
	for _, rel := range wr.Files {
		if !IsMarkdownPath(rel) {
			continue
		}
		abs := filepath.Join(root.Path(), filepath.FromSlash(rel))
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			return nil, fmt.Errorf("read %q while checking identifier collisions: %w", rel, rerr)
		}
		rec := records.ParseRecord(rel, data)
		if rec.TypeName() != sc.Type {
			continue
		}
		if id := rec.ID(); id != "" {
			if _, dup := out[id]; !dup {
				out[id] = rel
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// The create-time hook
// ---------------------------------------------------------------------------

// stampNewRecordIdentity splices an `id:` into freshly assembled note content
// when — and only when — that content is a record of a type the collection
// declares and carries no identifier yet.
//
// It returns the content unchanged, with an empty id and a nil error, in every
// case where stamping is not the right answer. Those cases are the MAJORITY of
// creates and none of them is an error:
//
//   - the note declares no `type:` at all — an ordinary note (FR-005);
//   - its `type:` matches no schema in this collection — also an ordinary
//     note, which is most of every real vault;
//   - it already carries an `id:` (or `omni_id:`) — the REST record-create
//     door mints before it calls CreateNote, so this is its normal path, and
//     re-stamping would overwrite an identifier the caller already committed
//     to in its response;
//   - the collection has no records control plane at all, so LoadSchemas
//     finds nothing.
//
// THE FRONTMATTER IS PARSED BEFORE THE SCHEMAS ARE LOADED, deliberately. A
// note with no `type:` is decided in one cheap in-memory parse, so creating an
// ordinary note never pays for reading and parsing every schema file in the
// vault.
//
// A FAILURE TO MINT IS RETURNED, NOT SWALLOWED. Writing a record note with no
// identifier is precisely the defect this file exists to remove, so "the
// counter file is corrupt" or "the lock is held" refuses the create rather
// than quietly producing another unidentifiable record.
func stampNewRecordIdentity(lock NoteLockConfig, collectionRoot string, content []byte) ([]byte, string, error) {
	rec := records.ParseRecord("", content)
	if rec.TypeName() == "" || rec.ID() != "" {
		return content, "", nil
	}

	// LoadSchemas' rejection report is deliberately dropped here: a malformed
	// schema file is reported loudly by the doors that VALIDATE against it
	// (and by the importer), and this hook must not turn one broken schema
	// into a failure to create an unrelated note. A type whose schema failed
	// to load simply does not resolve below, and the note is created as an
	// ordinary note — the same outcome as a type nobody declared.
	set, _, err := records.LoadSchemas(collectionRoot)
	if err != nil {
		return nil, "", fmt.Errorf("knowledge: load record schemas while minting an identifier: %w", err)
	}
	sc, ok := set.Get(rec.TypeName())
	if !ok {
		return content, "", nil
	}

	ids, _, err := MintRecordIDs(lock, collectionRoot, sc, 1)
	if err != nil {
		return nil, "", err
	}
	if len(ids) != 1 {
		return nil, "", fmt.Errorf("knowledge: minting an identifier for record type %q produced %d identifiers, wanted 1",
			sc.Type, len(ids))
	}

	stamped, err := SpliceRecordIdentity(content, ids[0])
	if err != nil {
		return nil, "", err
	}
	return stamped, ids[0], nil
}

// utf8BOM is the byte-order mark a Windows editor leaves on a Markdown file.
var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// SpliceRecordIdentity writes `id: <value>` into src's frontmatter, leaving
// every other byte of the note exactly where it was.
//
// IT IS A SPLICE, NEVER A RE-SERIALISATION, and that is the whole point.
// RecordWriteRequest's own schema states the rule and the reason: the vault is
// simultaneously a human's working notes, and a writer that re-serialises YAML
// degrades it a little on every touch — comments vanish, key order is sorted,
// quoting style is normalised, blank lines collapse. So this delegates to
// SetProperty, the package's existing property setter, which replaces or
// appends ONE line and copies the rest through unchanged.
//
// THE BOM IS HANDLED HERE, AND IT IS NOT COSMETIC (the messy-vault case).
// fmParse tests whether a note's FIRST LINE is exactly "---", and it does not
// skip a UTF-8 byte-order mark — so on a BOM-prefixed note it reports "no
// frontmatter present" and SetProperty helpfully PREPENDS A SECOND
// frontmatter block above the real one. records.ParseRecord, by contrast, DOES
// skip the BOM, so such a note is correctly seen as a record and reaches this
// function in the ordinary way. The two disagreeing is how a real note in a
// real vault gets two `---` blocks and stops parsing at all. Stripping the
// mark before the splice and putting the identical bytes back afterwards makes
// them agree, and keeps the mark itself byte-identical.
func SpliceRecordIdentity(src []byte, id string) ([]byte, error) {
	bom, body := []byte(nil), src
	if bytes.HasPrefix(body, utf8BOM) {
		bom, body = body[:len(utf8BOM)], body[len(utf8BOM):]
	}
	spliced, err := SetProperty(records.RecordIDKey, id)(body)
	if err != nil {
		return nil, fmt.Errorf("knowledge: write identifier %q: %w", id, err)
	}
	if len(bom) == 0 {
		return spliced, nil
	}
	out := make([]byte, 0, len(bom)+len(spliced))
	out = append(out, bom...)
	return append(out, spliced...), nil
}
