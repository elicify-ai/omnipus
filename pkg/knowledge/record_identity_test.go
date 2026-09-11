// Omnipus — tests for ADR-068 D7 identifier minting at note creation.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// riCompanySchema declares an identity prefix; riGadgetSchema declares none.
const riCompanySchema = `schema_version: 1
type: company
label: Company
identity:
  prefix: CO
properties:
  name:   { type: text, required: true }
  status: { type: enum, values: [prospect, active] }
`

const riGadgetSchema = `schema_version: 1
type: gadget
label: Gadget
properties:
  name: { type: text }
`

// riVaultWithSchemas builds a collection whose records control plane declares
// `company` (prefixed) and `gadget` (prefix-less).
func riVaultWithSchemas(t *testing.T) (*Collection, string) {
	t.Helper()
	root := a1Vault(t, "Records", nil)
	dir := records.SchemaDir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "company.yaml"), []byte(riCompanySchema), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gadget.yaml"), []byte(riGadgetSchema), 0o644))
	return a1Collection(t, root), root
}

// riCreate creates a note with the given body and returns the result.
func riCreate(t *testing.T, c *Collection, rel, body string) CreateNoteResult {
	t.Helper()
	res, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{
		RelPath:   rel,
		Body:      []byte(body),
		Now:       a1Clock(t),
		NameShape: OperatorNameShape,
	})
	require.NoError(t, err)
	return res
}

// ---------------------------------------------------------------------------
// The founder's second ask: a record created from scratch carries an id,
// without the caller having to ask for one.
// ---------------------------------------------------------------------------

// TestCreateNote_MintsAnIdentifierForARecord is the core of ask 2. The caller
// passes no identifier and knows nothing about the allocator; the note that
// lands on disk carries one anyway. CreateNote is the choke point all three
// in-process creation doors funnel through, so proving it here proves it for
// knowledge_edit's create op and knowledge_create_note alike.
func TestCreateNote_MintsAnIdentifierForARecord(t *testing.T) {
	c, _ := riVaultWithSchemas(t)

	res := riCreate(t, c, "companies/acme.md", "---\ntype: company\nname: Acme\n---\n\nBody.\n")

	require.Equal(t, "CO-0001", res.RecordID, "the first company minted must be CO-0001")

	data, err := os.ReadFile(res.AbsPath)
	require.NoError(t, err)
	rec := records.ParseRecord(res.RelPath, data)
	require.Equal(t, "CO-0001", rec.ID(), "the identifier must be on DISK, not merely in the result")
	require.Equal(t, "company", rec.TypeName())
	require.Contains(t, string(data), "name: Acme", "the caller's own frontmatter must survive")
	require.Contains(t, string(data), "Body.", "the caller's body must survive")
}

// TestCreateNote_MintsMonotonicallyAndNeverRepeats covers the invariant D7
// actually states — unique within its type — across several creates, and
// across two types at once so the two counters cannot be sharing state.
func TestCreateNote_MintsMonotonicallyAndNeverRepeats(t *testing.T) {
	c, _ := riVaultWithSchemas(t)

	seen := map[string]string{}
	for _, rel := range []string{"a.md", "b.md", "c.md"} {
		res := riCreate(t, c, rel, "---\ntype: company\nname: X\n---\n")
		require.NotEmpty(t, res.RecordID)
		if prev, dup := seen[res.RecordID]; dup {
			t.Fatalf("%s was minted %s, already held by %s", rel, res.RecordID, prev)
		}
		seen[res.RecordID] = rel
	}
	require.ElementsMatch(t, []string{"CO-0001", "CO-0002", "CO-0003"}, keysOf(seen))

	// A DIFFERENT type restarts at 1 — uniqueness is per type, so `gadget`
	// numbering is not affected by how many companies exist.
	g := riCreate(t, c, "g.md", "---\ntype: gadget\nname: Sprocket\n---\n")
	require.Equal(t, "0001", g.RecordID)
}

// TestCreateNote_PrefixLessSchemaMintsABareZeroPaddedNumber pins the
// documented decision for a schema with no `identity:` block, at the create
// door. The alternative — refusing to mint — would make "no prefix declared" a
// second route back to the unidentifiable records this change removes.
func TestCreateNote_PrefixLessSchemaMintsABareZeroPaddedNumber(t *testing.T) {
	c, _ := riVaultWithSchemas(t)

	res := riCreate(t, c, "gadget.md", "---\ntype: gadget\nname: Sprocket\n---\n")
	require.Equal(t, "0001", res.RecordID)
	require.False(t, strings.HasPrefix(res.RecordID, "-"), "the prefix-less form must not leave a dangling separator")

	// It must read back as the STRING "0001". An identifier YAML re-reads as
	// the integer 1 stops matching the id the SPA holds.
	data, err := os.ReadFile(res.AbsPath)
	require.NoError(t, err)
	require.Equal(t, "0001", records.ParseRecord(res.RelPath, data).ID())
}

// TestCreateNote_LeavesAnOrdinaryNoteAlone is the FR-005 half: the majority of
// every real vault is notes that are not records, and none of them may acquire
// an identifier or a frontmatter block they did not have.
func TestCreateNote_LeavesAnOrdinaryNoteAlone(t *testing.T) {
	c, _ := riVaultWithSchemas(t)

	for name, body := range map[string]string{
		"no-frontmatter.md":  "# Just prose\n\nNothing structured here.\n",
		"no-type.md":         "---\ndate: 2026-03-01\n---\n\nA note.\n",
		"undeclared-type.md": "---\ntype: spaceship\nname: Voyager\n---\n\nNo schema declares this.\n",
	} {
		res := riCreate(t, c, name, body)
		require.Empty(t, res.RecordID, "%s is not a record of a declared type and must not be stamped", name)

		data, err := os.ReadFile(res.AbsPath)
		require.NoError(t, err)
		require.Equal(t, body, string(data), "%s must land on disk byte-identical to what the caller passed", name)
	}
}

// TestCreateNote_DoesNotOverwriteAnIdentifierTheCallerSupplied is what keeps
// the REST record-create door correct. It mints BEFORE calling CreateNote and
// puts the identifier in its own response, so re-stamping here would hand the
// caller an id the note does not carry.
func TestCreateNote_DoesNotOverwriteAnIdentifierTheCallerSupplied(t *testing.T) {
	c, _ := riVaultWithSchemas(t)

	res := riCreate(t, c, "supplied.md", "---\ntype: company\nid: CO-0042\nname: Globex\n---\n")
	require.Empty(t, res.RecordID, "nothing was minted, because the caller supplied one")

	data, err := os.ReadFile(res.AbsPath)
	require.NoError(t, err)
	require.Equal(t, "CO-0042", records.ParseRecord(res.RelPath, data).ID())
	require.Equal(t, 1, strings.Count(string(data), "id:"), "a second `id:` key would be a duplicate-key corruption")
}

// TestCreateNote_AdvancesPastAnIdentifierAlreadyOnDisk covers the collision
// scan. An operator can hand-write an id the counter has not reached (imported
// data, a restored copy), and minting a duplicate would break D7's uniqueness.
func TestCreateNote_AdvancesPastAnIdentifierAlreadyOnDisk(t *testing.T) {
	c, _ := riVaultWithSchemas(t)

	// CO-0001 is the identifier the counter would hand out first.
	riCreate(t, c, "hand-written.md", "---\ntype: company\nid: CO-0001\nname: Prior\n---\n")

	res := riCreate(t, c, "next.md", "---\ntype: company\nname: Next\n---\n")
	require.NotEqual(t, "CO-0001", res.RecordID, "the allocator handed out an identifier a note already holds")
	require.Equal(t, "CO-0002", res.RecordID)
}

// TestKnowledgeEditCreate_MintsAnIdentifier drives the founder's named door —
// `knowledge_edit`'s create op — rather than CreateNote directly.
//
// Worth its own test even though CreateNote is the choke point: this is the
// path an AGENT takes, it assembles frontmatter through a different route
// (the `frontmatter` argument, spliced property by property), and "the choke
// point works" and "the door that reaches it works" are two claims.
func TestKnowledgeEditCreate_MintsAnIdentifier(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Deals/acme.md",
		"body":        "# Acme\n",
		"frontmatter": map[string]any{"type": "deal", "status": "prospect"},
	})
	require.False(t, res.IsError, "create refused: %s", res.ForLLM)

	got := a4Read(t, root, "Deals/acme.md")
	rec := records.ParseRecord("Deals/acme.md", []byte(got))
	require.NotEmpty(t, rec.ID(), "a record created through knowledge_edit must carry an identifier:\n%s", got)

	// veSchema's `deal` declares no `identity:` block, so this also exercises
	// the prefix-less fallback through the agent-facing door.
	require.Equal(t, "0001", rec.ID())
	require.Contains(t, got, "status: prospect", "the caller's own properties must survive")
	require.Contains(t, got, "# Acme", "the caller's body must survive")
}

// ---------------------------------------------------------------------------
// The splice primitive
// ---------------------------------------------------------------------------

// TestSpliceRecordIdentity_IsByteIdenticalOutsideTheInsertedLine is the
// primitive-level statement of the rule RecordWriteRequest's schema gives:
// the write must be a splice, never a re-serialisation.
//
// The oracle comes from the rule: remove the one added line and the bytes must
// equal the input exactly.
func TestSpliceRecordIdentity_IsByteIdenticalOutsideTheInsertedLine(t *testing.T) {
	src := "---\n# a comment nobody may delete\ntype: company\nzebra: last\n\nname: 'Quoted Value'\n---\n\nBody --- with a false fence.\n"

	out, err := SpliceRecordIdentity([]byte(src), "CO-0007")
	require.NoError(t, err)

	removed, rest := riRemoveLine(string(out), "id: CO-0007")
	require.Equal(t, 1, removed, "exactly one line must have been added")
	require.Equal(t, src, rest, "every other byte must be unchanged")
}

// TestSpliceRecordIdentity_DoesNotPrependASecondBlockOnABOMNote is the
// messy-vault case that would corrupt a real file.
//
// records.ParseRecord SKIPS a UTF-8 BOM and therefore sees a record, while
// fmParse does NOT skip it and reports "no frontmatter present". Left
// unreconciled, SetProperty helpfully prepends a SECOND frontmatter block
// above the real one and the note stops parsing altogether.
func TestSpliceRecordIdentity_DoesNotPrependASecondBlockOnABOMNote(t *testing.T) {
	src := "\xef\xbb\xbf---\ntype: company\nname: Umbrella\n---\n\nBody.\n"

	out, err := SpliceRecordIdentity([]byte(src), "CO-0003")
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(string(out), "\xef\xbb\xbf"), "the BOM must survive, unchanged and still first")
	require.Equal(t, 2, strings.Count(string(out), "---"), "a second frontmatter block was prepended")
	require.Equal(t, "CO-0003", records.ParseRecord("bom.md", out).ID())

	removed, rest := riRemoveLine(string(out), "id: CO-0003")
	require.Equal(t, 1, removed)
	require.Equal(t, src, rest)
}

// TestRenderRecordIdentity_MatchesD7sWorkedExample pins the rendering against
// the ADR's own example rather than against this implementation.
//
// This matters beyond formatting: pkg/gateway carries a SECOND allocator, and
// the two interoperate only because they agree on the counter file, the lock
// key and this rendering. A change here silently lets two records be given the
// same identifier.
func TestRenderRecordIdentity_MatchesD7sWorkedExample(t *testing.T) {
	withPrefix := &records.Schema{Type: "company", Identity: records.Identity{Prefix: "CO"}}
	require.Equal(t, "CO-0142", RenderRecordIdentity(withPrefix, 142), "D7's own worked example")
	require.Equal(t, "CO-0001", RenderRecordIdentity(withPrefix, 1))
	require.Equal(t, "CO-99999", RenderRecordIdentity(withPrefix, 99999), "past 9999 it widens, never wraps")

	noPrefix := &records.Schema{Type: "gadget"}
	require.Equal(t, "0001", RenderRecordIdentity(noPrefix, 1), "a schema with no identity prefix mints a bare zero-padded number")
	require.Equal(t, "0142", RenderRecordIdentity(noPrefix, 142))
}

// TestPreviewRecordIDs_DoesNotAdvanceTheCounter is the dry-run guarantee at
// the allocator: a preview that burned identifiers would make every --dry-run
// leave a gap in the sequence.
func TestPreviewRecordIDs_DoesNotAdvanceTheCounter(t *testing.T) {
	c, root := riVaultWithSchemas(t)
	set, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	sc, ok := set.Get("company")
	require.True(t, ok)

	lock := NoteLockConfig{CollectionRoot: c.Root()}
	preview, _, err := PreviewRecordIDs(lock, c.Root(), sc, 3)
	require.NoError(t, err)
	require.Equal(t, []string{"CO-0001", "CO-0002", "CO-0003"}, preview)

	seq := filepath.Join(c.Root(), records.VaultMarkerDirName, records.RecordsDirName, "company.seq")
	_, statErr := os.Stat(seq)
	require.True(t, os.IsNotExist(statErr), "a preview must not create the counter file")

	minted, _, err := MintRecordIDs(lock, c.Root(), sc, 3)
	require.NoError(t, err)
	require.Equal(t, preview, minted, "a real run must produce exactly what the preview promised")

	data, err := os.ReadFile(seq)
	require.NoError(t, err)
	require.Equal(t, "3", strings.TrimSpace(string(data)), "the counter must record the last value handed out")
}

// TestMintRecordIDs_RefusesACorruptCounter covers the fault that silently
// re-issues every identifier already given out. Resetting to zero is the
// tempting repair and it is the wrong one.
func TestMintRecordIDs_RefusesACorruptCounter(t *testing.T) {
	c, root := riVaultWithSchemas(t)
	set, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	sc, ok := set.Get("company")
	require.True(t, ok)

	seqDir := filepath.Join(c.Root(), records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(seqDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(seqDir, "company.seq"), []byte("not-a-number\n"), 0o600))

	_, _, err = MintRecordIDs(NoteLockConfig{CollectionRoot: c.Root()}, c.Root(), sc, 1)
	require.Error(t, err, "a corrupt counter must be reported, never silently reset")
	require.Contains(t, err.Error(), "not a non-negative integer")
}

// riRemoveLine deletes every line whose trimmed form equals want.
func riRemoveLine(src, want string) (int, string) {
	lines := strings.SplitAfter(src, "\n")
	var kept []string
	removed := 0
	for _, l := range lines {
		if strings.TrimRight(l, "\r\n") == want {
			removed++
			continue
		}
		kept = append(kept, l)
	}
	return removed, strings.Join(kept, "")
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
