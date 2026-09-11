// Regression tests for the ADR-083 implementation review findings on the
// record doors: the audit actor (§4.2b / N4), the list-arity write guard
// (§4.6), the malformed-token refusal (§4.2a(c)), schema-load reporting,
// path-collision status, and the per-row version tokens the whole inline
// editor depends on.
//
// Every test here asserts behaviour a green suite previously reported as
// working, so each one is written to FAIL against the code as it was.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// §4.2b / founder ruling N4 — the audit actor
// ---------------------------------------------------------------------------

// TestKnowledgeRecordWrite_AuditActor is the test ADR-083 §11.2 names verbatim
// as the step-5 exit criterion, and it is deliberately ONE test covering all
// three cases rather than three tests covering one each.
//
// The reason is the same one §4.6's editor-gating table gives for its own
// paired assertion: a test that only checks the refusal passes against a
// handler that refuses everything, and a test that only checks `user:` passes
// against one that writes that string unconditionally. Only asserting all
// three against the same fixture pins the actual DISCRIMINATION — that the
// door tells the three populations apart — which is the property N4's
// greppability obligation actually rests on.
//
// What this catches, concretely: before this fix the door recorded
// `a.callerIdentity(r).Username`, a bare display name with no `user:` prefix,
// which is "" under dev_mode_bypass. So an authenticated write and a bypassed
// write were indistinguishable from each other by any mechanical rule, a
// bypassed write recorded an empty actor that EMB-093 forbids by name, and an
// unattributable request was not refused at all — it wrote the file.
func TestKnowledgeRecordWrite_AuditActor(t *testing.T) {
	t.Run("an authenticated write records user:<id>", func(t *testing.T) {
		api, ws, _, auditDir := buildRecordTestVaultWithAuditor(t)
		token := widgetVersionToken(t, api, ws)

		w := knowledgePostWithUser(t, api, "/api/v1/library/"+ws+"/knowledge/records",
			recordNameWrite(token, "Renamed By Person"),
			&config.UserConfig{Username: "daniela"})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		assert.Equal(t, []string{"user:daniela"}, recordAuditActors(t, auditDir),
			"an attributable write must carry the user: prefix — N4's greppability obligation "+
				"is that EVERY identified actor carries it, which is what makes `anonymous` separable as a class")
	})

	t.Run("a bypassed write is ALLOWED and records the literal anonymous", func(t *testing.T) {
		api, ws, vault, auditDir := buildRecordTestVaultWithAuditor(t)
		setDevModeBypass(t, api, true)
		token := widgetVersionToken(t, api, ws)

		w := knowledgePostWithUser(t, api, "/api/v1/library/"+ws+"/knowledge/records",
			recordNameWrite(token, "Renamed Anonymously"), nil)
		// N4 OVERRULED the architect's recommendation of a 503 here: the write
		// is allowed and recorded, with the accepted cost that the entry
		// cannot answer "who".
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		after, err := os.ReadFile(filepath.Join(vault, "w1.md"))
		require.NoError(t, err)
		assert.Contains(t, string(after), "Renamed Anonymously", "the bypassed write must LAND")

		actors := recordAuditActors(t, auditDir)
		assert.Equal(t, []string{"anonymous"}, actors)
		for _, a := range actors {
			assert.NotEmpty(t, a, "EMB-093: never an empty string")
			assert.False(t, strings.HasPrefix(a, "user:"),
				"the anonymous token carries NO prefix — `user:anonymous` would put it back in the attributable population")
		}
	})

	t.Run("neither authenticated nor bypassed is refused BEFORE the file is touched", func(t *testing.T) {
		api, ws, vault, auditDir := buildRecordTestVaultWithAuditor(t)
		setDevModeBypass(t, api, false)
		token := widgetVersionToken(t, api, ws)
		before, err := os.ReadFile(filepath.Join(vault, "w1.md"))
		require.NoError(t, err)

		w := knowledgePostWithUser(t, api, "/api/v1/library/"+ws+"/knowledge/records",
			recordNameWrite(token, "Should Not Land"), nil)
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())

		after, err := os.ReadFile(filepath.Join(vault, "w1.md"))
		require.NoError(t, err)
		assert.Equal(t, before, after,
			"N4 permits an UNATTRIBUTED write, not an UNATTRIBUTABLE one — the file must be byte-identical")
		assert.Contains(t, auditReasonsFor(t, auditDir), recordRefusalUnattributable,
			"the refusal itself must be recorded: the refused population is what answers "+
				"'was anything attempted against this vault'")
	})
}

// TestKnowledgeRecordWrite_AuditEventNameIsRegistered pins ADR §4.2
// precondition 3's actual decision.
//
// The implementation invented `knowledge.record.write`, a SIXTH event name
// that pkg/audit's IsValidEventName does not know, so every record write
// tripped the warn-once "unknown Event value" path the project deliberately
// closed. Neither existing audit guard test caught it: both walk audit's
// Event* CONSTANTS, and an emitter passing a bare string literal from another
// package is invisible to them. This test closes that shape for this door by
// asserting the emitted name against the allowlist itself.
func TestKnowledgeRecordWrite_AuditEventNameIsRegistered(t *testing.T) {
	api, ws, _, auditDir := buildRecordTestVaultWithAuditor(t)
	token := widgetVersionToken(t, api, ws)

	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records",
		recordNameWrite(token, "Any Name"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	entries := recordAuditEntries(t, auditDir)
	require.Len(t, entries, 1, "one write, one audit entry")
	assert.Equal(t, "knowledge.note.write", entries[0]["event"],
		"§4.2 precondition 3 chose this name precisely because it is already registered")
	assert.True(t, audit.IsValidEventName(recordWriteAuditEvent),
		"the emitted name must be in pkg/audit's allowlist, or every write logs an unknown-event warning")
}

// ---------------------------------------------------------------------------
// §4.6 / review C1 — the list-arity write guard
// ---------------------------------------------------------------------------

// TestKnowledgeRecordWrite_ListPropertyShrinkRefused is the server half of the
// data-loss defect, and it is the half that matters: §4.2c's whole argument is
// that this door must not trust the client.
//
// The failure it prevents, end to end: `tags: [alpha, beta]` renders on the
// wire as ONE string, "alpha, beta", because VaultFindCell.value is a single
// rendered string whatever the arity. An editor offered on that cell sends
// back one value, SetPropertyList replaces the whole list span, and the note
// now holds a ONE-element list whose single member is the literal text
// "alpha, beta". HTTP 200, audit decision=allow, two tags silently gone.
func TestKnowledgeRecordWrite_ListPropertyShrinkRefused(t *testing.T) {
	api, ws, vault, auditDir := buildListRecordTestVault(t)
	before, err := os.ReadFile(filepath.Join(vault, "L1.md"))
	require.NoError(t, err)
	token := listRecordVersionToken(t, api, ws)

	body := map[string]any{
		"type": "widget", "id": "WD-0001", "version_token": token,
		"properties": []map[string]any{
			{"property": "tags", "values": []map[string]any{
				// Exactly what an inline editor built from the joined cell
				// sends: the rendering, re-submitted as one value.
				{"type": "text", "text": "alpha, beta"},
			}},
		},
	}
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "would discard the rest")

	after, err := os.ReadFile(filepath.Join(vault, "L1.md"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused write must leave the file byte-identical")
	assert.Contains(t, auditReasonsFor(t, auditDir), recordRefusalListProperty)
}

// TestKnowledgeRecordWrite_ListPropertyWholeListAndClearStillWork is the
// positive control the guard needs, and it is not optional.
//
// Without it, TestKnowledgeRecordWrite_ListPropertyShrinkRefused above passes
// just as happily against a server that refuses EVERY write to a list
// property — which would build the wall §4.2c explicitly declined to build
// ("a scope decision, not a platform limit ... with a way forward, rather
// than a wall") and would break a future multi-value control. The rule is
// "must not SHRINK", so the two non-shrinking shapes must both still land.
func TestKnowledgeRecordWrite_ListPropertyWholeListAndClearStillWork(t *testing.T) {
	t.Run("sending at least as many values as are held is accepted", func(t *testing.T) {
		api, ws, vault, _ := buildListRecordTestVault(t)
		token := listRecordVersionToken(t, api, ws)

		body := map[string]any{
			"type": "widget", "id": "WD-0001", "version_token": token,
			"properties": []map[string]any{
				{"property": "tags", "values": []map[string]any{
					{"type": "text", "text": "alpha"},
					{"type": "text", "text": "beta"},
					{"type": "text", "text": "gamma"},
				}},
			},
		}
		w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		onDisk, err := os.ReadFile(filepath.Join(vault, "L1.md"))
		require.NoError(t, err)
		for _, want := range []string{"alpha", "beta", "gamma"} {
			assert.Contains(t, string(onDisk), want)
		}
	})

	t.Run("an empty values array still CLEARS the list", func(t *testing.T) {
		api, ws, vault, _ := buildListRecordTestVault(t)
		token := listRecordVersionToken(t, api, ws)

		body := map[string]any{
			"type": "widget", "id": "WD-0001", "version_token": token,
			// D3.2's explicit clear. The caller plainly asked for it, so it is
			// not the silent collapse the guard exists to stop.
			"properties": []map[string]any{{"property": "tags", "values": []map[string]any{}}},
		}
		w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		onDisk, err := os.ReadFile(filepath.Join(vault, "L1.md"))
		require.NoError(t, err)
		assert.NotContains(t, string(onDisk), "alpha")
		assert.NotContains(t, string(onDisk), "tags:")
		assert.Contains(t, string(onDisk), "name: Sprocket", "clearing one property must not disturb another")
	})
}

// ---------------------------------------------------------------------------
// §4.2a(c) — a malformed token is 400, never 409
// ---------------------------------------------------------------------------

// TestKnowledgeRecordWrite_QuotedTokenIs400NotConflict pins the rule ADR-083
// §4.2a(c) states with its own reasoning: "never 409, because otherwise a
// client that forgot to strip the quotes gets a conflict indistinguishable
// from a real one, forever."
//
// A JSON-quoted token simply mismatches under an opaque comparison, so the
// natural answer is 409 — and that is what shipped. The client then re-reads
// to get "the current version", forgets to strip the quotes again, conflicts
// again, and the loop never terminates, with every attempt recorded in the
// audit log as a genuine version_conflict against a record no second writer
// ever touched.
func TestKnowledgeRecordWrite_QuotedTokenIs400NotConflict(t *testing.T) {
	api, ws, vault, auditDir := buildRecordTestVaultWithAuditor(t)
	before, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	token := widgetVersionToken(t, api, ws)

	// The exact mistake: the token's JSON ENCODING rather than its characters.
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records",
		recordNameWrite(`"`+token+`"`, "Should Not Land"))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.NotEqual(t, http.StatusConflict, w.Code,
		"§4.2a(c): a quoted token is a malformed request, not a stale one")
	assert.Contains(t, w.Body.String(), "JSON quotes",
		"the refusal must name the actual mistake, or the client cannot act on it")

	after, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	assert.Equal(t, before, after)

	reasons := auditReasonsFor(t, auditDir)
	assert.Contains(t, reasons, recordRefusalVersionMalformed)
	assert.NotContains(t, reasons, recordRefusalVersionConflict,
		"recording this as a version conflict tells an operator a second writer exists when none does")
}

// TestKnowledgeRecordWrite_StaleButWellFormedTokenIsStill409 is the paired
// positive control for the test above: tightening the shape check must not
// turn a REAL conflict into a 400, or the compare-and-swap has been disabled
// rather than sharpened.
func TestKnowledgeRecordWrite_StaleButWellFormedTokenIsStill409(t *testing.T) {
	api, ws, _, auditDir := buildRecordTestVaultWithAuditor(t)

	// A well-formed token this server could have minted, but did not: v1: plus
	// 32 hex. Built through the real minter so the shape can never drift from
	// what IsWellFormedVersionToken accepts.
	stale := string(knowledge.ComputeVersionToken([]byte("some other content entirely")))
	require.True(t, knowledge.IsWellFormedVersionToken(stale))

	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records",
		recordNameWrite(stale, "Should Not Land"))
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Contains(t, auditReasonsFor(t, auditDir), recordRefusalVersionConflict)
}

// ---------------------------------------------------------------------------
// Review M8 — the record identity keys
// ---------------------------------------------------------------------------

// TestKnowledgeRecordWrite_IdentityPropertyRefused covers the case where a
// schema declares a property literally named `id` — plausible, since an
// external system's identifier is a normal thing to model, and nothing in the
// schema loader forbids it.
//
// Clearing it (an empty values array) used to succeed with a 200 and DELETE
// the record's identifier. From that moment the record is unreachable through
// every record door, AND invisible to the allocator's collision check — which
// then mints the same identifier to a SECOND record, breaking the "unique
// within its type" invariant the allocator's own comment claims to hold.
func TestKnowledgeRecordWrite_IdentityPropertyRefused(t *testing.T) {
	api, ws, vault, auditDir := buildIdentityPropertyTestVault(t)
	before, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	token := widgetVersionToken(t, api, ws)

	for _, key := range []string{"id", "type"} {
		body := map[string]any{
			"type": "widget", "id": "WD-0001", "version_token": token,
			// The empty values array is D3.2's CLEAR — the shape that reaches
			// RemoveProperty and deletes the key line outright.
			"properties": []map[string]any{{"property": key, "values": []map[string]any{}}},
		}
		w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
		require.Equal(t, http.StatusBadRequest, w.Code, "clearing %q must be refused: %s", key, w.Body.String())
	}

	after, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "the record's identity must survive byte-identically")
	assert.Contains(t, auditReasonsFor(t, auditDir), recordRefusalIdentityProperty)
}

// ---------------------------------------------------------------------------
// Review H2/F9 — a broken schema file is visible, not silent
// ---------------------------------------------------------------------------

// TestKnowledgeRecordSchema_ReportsABrokenSchemaFile asserts the distinction
// the endpoint could not previously express.
//
// A vault whose schema file has a YAML mistake answered `{types: [], problems:
// []}` — byte for byte what a HEALTHY vault declaring no record types returns.
// The operator saw a Library with no record types, no editors anywhere, and
// nothing on screen suggesting a file was broken; their only evidence was a
// WARN in a server log they cannot reach from a browser.
//
// The existing TestKnowledgeRecordSchema_NoRecordTypesIsEmptyNotError asserts
// the healthy shape, so the two together are what make this a DISCRIMINATION
// test rather than a shape test.
func TestKnowledgeRecordSchema_ReportsABrokenSchemaFile(t *testing.T) {
	api, ws, vault := buildRecordTestVault(t)
	// A property declaring a `group` outside D4's closed set — the exact
	// one-word mistake that used to blank a whole dashboard at the SPA's Zod
	// edge, and which the loader now refuses with a named reason.
	writeNote(t, vault, ".omnipus-vault/records/broken.yaml",
		"schema_version: 1\ntype: broken\nproperties:\n  stage: { type: enum, values: [{name: waiting, group: blocked}] }\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/record-schema")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.RecordSchema](t, w)

	require.NotEmpty(t, out.Problems,
		"a schema file that failed to load must be REPORTED; an empty problems array is what a healthy vault returns")
	var found bool
	for _, p := range out.Problems {
		if p.Code == gen.SchemaBadProperty {
			found = true
			assert.Contains(t, p.Reason, "blocked", "the reason must name the offending value")
			require.NotNil(t, p.Paths)
			assert.NotEmpty(t, *p.Paths, "the operator has to be able to FIND the file")
		}
	}
	assert.True(t, found, "expected a schema_bad_property problem, got %#v", out.Problems)

	// The healthy type in the same vault must still load: a reporting change
	// must not cost the caller the types that were fine.
	names := make([]string, 0, len(out.Types))
	for _, ty := range out.Types {
		names = append(names, ty.Type)
	}
	assert.Contains(t, names, "widget")
}

// TestSchemaRejectionProblem_MapsEveryRejectionCode pins the mapping against
// its named source of truth, which is what turns "seven of nine silently
// dropped" from a thing a reviewer has to notice into a thing a test fails on.
func TestSchemaRejectionProblem_MapsEveryRejectionCode(t *testing.T) {
	seen := map[gen.RecordProblemCode]records.SchemaRejectionCode{}
	for _, code := range records.SchemaRejectionCodes {
		p := schemaRejectionProblem(records.SchemaRejection{Code: code, Reason: "because"})
		assert.NotEqual(t, gen.SchemaLoadFailed, p.Code,
			"%q fell through to the generic code — it needs its own arm, or a caller cannot tell it apart", code)
		assert.Equal(t, "because", p.Reason, "the loader's own words must survive the mapping")
		if prev, dup := seen[p.Code]; dup {
			t.Fatalf("%q and %q both map to %q — a collapse is what H2/F9 was", code, prev, p.Code)
		}
		seen[p.Code] = code
	}
	assert.Len(t, seen, len(records.SchemaRejectionCodes), "the mapping must be one-to-one")
}

// ---------------------------------------------------------------------------
// Review M7/F8 — a path collision is the caller's fault, not the server's
// ---------------------------------------------------------------------------

// TestKnowledgeRecordCreate_ExistingPathIsNotAServerError covers the single
// most ordinary way a create fails: two people name a record the same thing.
//
// knowledge.CreateNote returns ErrNoteExists from its O_EXCL create — the
// error that check exists to produce — and finishRecordWriteError recognised
// only two error shapes, so it fell through to 500 "internal server error"
// with audit decision=error, reason=write_failed. That misleads the caller
// (whose request was merely wrong) and pollutes the error population an
// operator greps for real faults.
func TestKnowledgeRecordCreate_ExistingPathIsNotAServerError(t *testing.T) {
	api, ws, _, auditDir := buildRecordTestVaultWithAuditor(t)

	body := map[string]any{
		"type": "widget",
		// w1.md already exists in the fixture vault.
		"path": "w1.md",
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "Collides"}}},
		},
	}
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.NotEqual(t, http.StatusInternalServerError, w.Code,
		"a path the caller chose and that is already taken is the caller's to fix")
	assert.Contains(t, w.Body.String(), "already exists")

	assert.Contains(t, auditReasonsFor(t, auditDir), recordRefusalPathExists)
	for _, e := range recordAuditEntries(t, auditDir) {
		assert.NotEqual(t, audit.DecisionError, e["decision"],
			"an ordinary client mistake must not be audited as a server fault")
	}
}

// TestKnowledgeRecordCreate_MintsAndReturns201 is the create door's happy path,
// which had no test at all — the whole create path was dead to the suite
// because every POST in it carried an id.
func TestKnowledgeRecordCreate_MintsAndReturns201(t *testing.T) {
	api, ws, vault := buildRecordTestVault(t)

	body := map[string]any{
		"type": "widget",
		"path": "new-widget.md",
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "Freshly Minted"}}},
		},
	}
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	rec := decodeJSON[gen.VaultRecord](t, w)
	// WD-0001 is taken by the fixture note, so the allocator must ADVANCE past
	// it rather than mint a duplicate — the FR-038a collision scan, exercised
	// for real.
	assert.Equal(t, "WD-0002", rec.Id,
		"the counter starts at 1 and WD-0001 is live, so the first free identifier is WD-0002")
	assert.Equal(t, "new-widget.md", rec.Path)
	require.NotNil(t, rec.VersionToken)
	assert.True(t, knowledge.IsWellFormedVersionToken(*rec.VersionToken))

	onDisk, err := os.ReadFile(filepath.Join(vault, "new-widget.md"))
	require.NoError(t, err)
	assert.Contains(t, string(onDisk), "id: WD-0002")
	assert.Contains(t, string(onDisk), "type: widget")
	assert.Contains(t, string(onDisk), "Freshly Minted")

	// Round trip: the minted record must be readable back through the GET
	// door, which is the only proof the id it reported is the id it stored.
	g := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/records/WD-0002")
	require.Equal(t, http.StatusOK, g.Code, g.Body.String())
}

// ---------------------------------------------------------------------------
// Review C5 — the per-row version tokens every editor depends on
// ---------------------------------------------------------------------------

// TestAttachRowVersionTokens_UsesTheRealTokenAndDegradesVisibly covers the
// function whose silent regression would make EVERY EDITOR IN THE PRODUCT
// DISAPPEAR with no error, no console warning, and a green suite on both
// sides of the wire: a row with no version_token makes the SPA's
// resolveEditTarget return undefined, and the cell renders as ordinary text.
//
// The oracle is knowledge.ComputeVersionToken over the note's own bytes —
// the real minter, not a value read off this function's output.
func TestAttachRowVersionTokens_UsesTheRealTokenAndDegradesVisibly(t *testing.T) {
	_, _, vault := buildRecordTestVault(t)

	rows := []gen.VaultFindRow{
		{Path: "w1.md"},
		// A row for a note that is not there: the documented best-effort case.
		{Path: "does-not-exist.md"},
	}
	attachRowVersionTokens(rows, vault)

	raw, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	require.NotNil(t, rows[0].VersionToken, "a live row MUST carry a token, or its cells lose every editor")
	assert.Equal(t, string(knowledge.ComputeVersionToken(raw)), *rows[0].VersionToken,
		"the row token must be the same scheme the write door compares against, not a second one")

	assert.Nil(t, rows[1].VersionToken,
		"a row whose note is gone legitimately has no token — the field is documented OPTIONAL for this case")
}

// TestAttachRowVersionTokens_UnopenableCollectionLeavesEveryRowTokenless
// pins the whole-collection failure as a real, observable outcome rather than
// an accident: the pass drops every token at once, which is the case that
// silently turns a fully editable board into a read-only one.
func TestAttachRowVersionTokens_UnopenableCollectionLeavesEveryRowTokenless(t *testing.T) {
	rows := []gen.VaultFindRow{{Path: "a.md"}, {Path: "b.md"}}
	attachRowVersionTokens(rows, filepath.Join(t.TempDir(), "no-such-knowledge-base"))
	for i := range rows {
		assert.Nil(t, rows[i].VersionToken)
	}
}

// ---------------------------------------------------------------------------
// Review H4 — refusals are audited
// ---------------------------------------------------------------------------

// TestKnowledgeRecordWrite_EveryRefusalIsAudited walks the early-exit refusals
// that previously answered the client and recorded NOTHING.
//
// The scenario that makes it matter: a misbehaving client (or a compromised
// token) hammers the write door with malformed bodies. Every attempt is
// refused, and the audit log — the one artefact an operator consults to
// answer "was anything attempted against this vault" — contained zero
// entries. The refusals existed only as status codes in whatever access log
// happened to be enabled.
func TestKnowledgeRecordWrite_EveryRefusalIsAudited(t *testing.T) {
	cases := []struct {
		name   string
		body   map[string]any
		status int
		reason string
	}{
		{
			name:   "no type",
			body:   map[string]any{"properties": []map[string]any{{"property": "name", "values": []map[string]any{{"type": "text", "text": "x"}}}}},
			status: http.StatusBadRequest,
			reason: recordRefusalInvalidRequest,
		},
		{
			name:   "empty properties",
			body:   map[string]any{"type": "widget", "properties": []map[string]any{}},
			status: http.StatusBadRequest,
			reason: recordRefusalInvalidRequest,
		},
		{
			name:   "create with no path",
			body:   map[string]any{"type": "widget", "properties": []map[string]any{{"property": "name", "values": []map[string]any{{"type": "text", "text": "x"}}}}},
			status: http.StatusBadRequest,
			reason: recordRefusalInvalidRequest,
		},
		{
			name: "unknown record type",
			body: map[string]any{"type": "sprocket", "path": "x.md",
				"properties": []map[string]any{{"property": "name", "values": []map[string]any{{"type": "text", "text": "x"}}}}},
			status: http.StatusBadRequest,
			reason: recordRefusalTypeUnknown,
		},
		{
			name: "unknown property",
			body: map[string]any{"type": "widget", "path": "x.md",
				"properties": []map[string]any{{"property": "nope", "values": []map[string]any{{"type": "text", "text": "x"}}}}},
			status: http.StatusBadRequest,
			reason: recordRefusalPropertyUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, ws, _, auditDir := buildRecordTestVaultWithAuditor(t)
			w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", tc.body)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			assert.Contains(t, auditReasonsFor(t, auditDir), tc.reason,
				"a refusal that records nothing leaves the audit log claiming nothing was attempted")
		})
	}
}

// ---------------------------------------------------------------------------
// Seam 1 — the gateway door and the agent door take the SAME lock
// ---------------------------------------------------------------------------

// TestRecordWriteAndAgentWriteShareOneLockKey settles a point on which two
// careful reviewers disagreed, by measurement rather than by reading.
//
// §4.2a(iii) is explicit that the striped lock key is
// `collectionRoot + "\x00" + path.Clean(rel)` and that "all three of those
// must match or the two writers take different locks and the guard is
// decorative". The gateway handler passes
// knowledge.NoteLockConfig{LockDir: lockDir} with CollectionRoot left EMPTY,
// which looks exactly like that failure. The competing reading is that
// EditNote/CreateNote set lock.CollectionRoot from the collection THEMSELVES,
// so both doors key the same lock despite the zero value.
//
// If the pessimistic reading were right, the existing concurrency test would
// prove only that two GATEWAY writers serialise — not that a gateway writer
// and an agent writer do, which is the property that actually protects a
// vault. So this asserts the lock key directly, from the same expression
// pkg/knowledge uses, rather than racing two goroutines and hoping.
// ⚠️ THE OBVIOUS VERSION OF THIS TEST PASSES FOR THE WRONG REASON, and that
// trap is worth naming because I fell into it first. Holding
// `WithNoteWriteLock(gatewayCfg, …)` and showing that
// `WithNoteWriteLock(agentCfg, …)` blocks proves nothing about the striped
// MUTEX: both configs carry the same LockDir, so the advisory FILE lock
// (which excludes two opens even inside one process) serialises them whatever
// their mutex keys are. A green there is compatible with the keys differing —
// exactly the state the test exists to rule out.
//
// So the holder below takes the mutex with NO LockDir at all, under the key an
// agent's call site produces. The only resource the two can share is then the
// striped mutex itself, and EditNote's outcome discriminates cleanly:
//
//	keys MATCH    EditNote cannot acquire and times out → *LockTimeoutError.
//	keys DIFFER   EditNote acquires immediately and the edit SUCCEEDS.
func TestRecordWriteAndAgentWriteShareOneLockKey(t *testing.T) {
	api, _, vault := buildRecordTestVault(t)
	col, err := knowledge.OpenCollection(vault)
	require.NoError(t, err)
	lockDir, err := knowledge.LockDirFor(api.homePath, col.Root())
	require.NoError(t, err)

	before, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	version := string(knowledge.ComputeVersionToken(before))

	// An agent-shaped holder: mutex only, keyed on (collectionRoot, rel).
	held := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- knowledge.WithNoteWriteLock(
			knowledge.NoteLockConfig{CollectionRoot: col.Root()},
			"w1.md",
			func() error {
				close(held)
				<-release
				return nil
			})
	}()
	<-held

	// The gateway door's config, verbatim: CollectionRoot deliberately unset,
	// exactly as handleRecordUpdate constructs it. A short bound so a correctly
	// blocked acquire reports quickly instead of waiting out DefaultLockBound.
	_, editErr := knowledge.EditNote(knowledge.OSLinkFS(), col, knowledge.EditNoteRequest{
		RelPath:       "w1.md",
		Edits:         []knowledge.NoteEdit{knowledge.SetProperty("name", "Written Through The Lock")},
		ExpectVersion: version,
		Now:           time.Now(),
		Lock:          knowledge.NoteLockConfig{LockDir: lockDir, Bound: 300 * time.Millisecond},
	})

	var timeout *knowledge.LockTimeoutError
	require.ErrorAs(t, editErr, &timeout,
		"the gateway door's zero-CollectionRoot config must key the SAME striped mutex an agent's "+
			"call site keys — EditNote normalises it (author.go sets lock.CollectionRoot from the "+
			"collection itself). If this acquires instead, §4.2a(iii)'s decorative-guard failure is "+
			"REAL and TestKnowledgeRecordWrite_ConcurrentUpdatesOnlyOneWins proves only that two "+
			"GATEWAY writers serialise, never that a gateway writer and an agent do")

	after, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "a write refused at the lock must not have touched the file")

	close(release)
	require.NoError(t, <-holderDone)

	// Positive control: once the agent-shaped holder releases, the very same
	// gateway-shaped write succeeds. Without this the test above would pass
	// against an EditNote that can never acquire at all.
	res, err := knowledge.EditNote(knowledge.OSLinkFS(), col, knowledge.EditNoteRequest{
		RelPath:       "w1.md",
		Edits:         []knowledge.NoteEdit{knowledge.SetProperty("name", "Written Through The Lock")},
		ExpectVersion: version,
		Now:           time.Now(),
		Lock:          knowledge.NoteLockConfig{LockDir: lockDir, Bound: 2 * time.Second},
	})
	require.NoError(t, err)
	assert.True(t, res.Changed)
	assert.Contains(t, string(res.Content), "Written Through The Lock",
		"EditNoteResult.Content must carry the post-edit bytes it was captured with, inside the lock")
}

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

// recordNameWrite is the ordinary one-field update body every actor/token test
// above sends, so the only thing that varies between them is the thing under
// test.
func recordNameWrite(token, name string) map[string]any {
	return map[string]any{
		"type": "widget", "id": "WD-0001", "version_token": token,
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": name}}},
		},
	}
}

// setDevModeBypass flips the live gateway flag the actor gate reads.
//
// AgentLoop.GetConfig hands back the live *config.Config rather than a copy,
// and there is no setter, so this mutates it in place. Safe here and only
// here: each test builds its own AgentLoop via buildLibraryTestAPI and drives
// the handler on the test's own goroutine, so no reader races this write.
func setDevModeBypass(t *testing.T, api *restAPI, on bool) {
	t.Helper()
	cfg := api.agentLoop.GetConfig()
	require.NotNil(t, cfg, "the test API must carry a config for the actor gate to read")
	cfg.Gateway.DevModeBypass = on
}

const listRecordSchema = "schema_version: 1\n" +
	"type: widget\n" +
	"identity:\n" +
	"  prefix: WD\n" +
	"properties:\n" +
	"  name: { type: text }\n" +
	"  tags: { type: text, many: true }\n"

// buildListRecordTestVault seeds a vault whose widget type declares a
// LIST-valued property holding two values — the exact shape whose joined
// rendering ("alpha, beta") an inline editor would collapse.
func buildListRecordTestVault(t *testing.T) (*restAPI, string, string, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Widgets vault")
	writeNote(t, vault, ".omnipus-vault/records/widget.yaml", listRecordSchema)
	writeNote(t, vault, "L1.md", "---\ntype: widget\nid: WD-0001\nname: Sprocket\ntags:\n  - alpha\n  - beta\n---\n# Sprocket\n")
	auditDir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = lg.Close() })
	api.auditor = lg
	return api, ws, vault, auditDir
}

func listRecordVersionToken(t *testing.T, api *restAPI, ws string) string {
	t.Helper()
	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/records/WD-0001")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	rec := decodeJSON[gen.VaultRecord](t, w)
	require.NotNil(t, rec.VersionToken)
	return *rec.VersionToken
}

const identityPropertySchema = "schema_version: 1\n" +
	"type: widget\n" +
	"identity:\n" +
	"  prefix: WD\n" +
	"properties:\n" +
	"  name: { type: text }\n" +
	// A schema declaring a property literally named `id` and one named `type`.
	// Nothing in the loader forbids either, which is exactly why the write
	// door has to.
	"  id:   { type: text }\n" +
	"  type: { type: text }\n"

func buildIdentityPropertyTestVault(t *testing.T) (*restAPI, string, string, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Widgets vault")
	writeNote(t, vault, ".omnipus-vault/records/widget.yaml", identityPropertySchema)
	writeNote(t, vault, "w1.md", recordTestWidgetNote("WD-0001", "Sprocket", "open"))
	auditDir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = lg.Close() })
	api.auditor = lg
	return api, ws, vault, auditDir
}
