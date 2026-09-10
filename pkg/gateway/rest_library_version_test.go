// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Tests for ADR-083 Step 0 — closing the unguarded save door on
// PUT /api/v1/library/{workspace_id}/content and .../content-binary
// (EMB-001…EMB-007c). Test numbers in comments refer to
// docs/internal/specs/adr-083-embedded-content-spec.md's Test Implementation
// Order / Dataset G.

package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
)

// --- helpers -----------------------------------------------------------

// libraryBareETag extracts the bare (unquoted) token from a response's ETag
// header, failing the test if there is none — every 200 on the version
// doors must carry one (EMB-007).
func libraryBareETag(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	etag := w.Header().Get("ETag")
	require.NotEmpty(t, etag, "response must carry an ETag header")
	require.True(t, len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"',
		"ETag must be RFC-quoted-strong: %q", etag)
	return strings.Trim(etag, `"`)
}

// Real-audit-logger scaffolding (attachTestAuditor / readAuditEntries) lives
// in rest_authexpo_fix_test.go and is reused here rather than duplicated:
// attachTestAuditor wires a REAL *audit.Logger — the same type and Log()
// method production uses, not an injected recorder/stub — and
// readAuditEntries(t, dir, event) reads the actual JSONL it wrote back.

// --- Test 1: TestLibraryContentPut_MissingVersionReturns400 ------------

func TestLibraryContentPut_MissingVersionReturns400(t *testing.T) {
	t.Run("field-absent", func(t *testing.T) {
		api, id := buildLibraryTestAPI(t)
		w := libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"new.txt","content":"x"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		_, statErr := os.Stat(filepath.Join(workDir(api, id), "new.txt"))
		assert.True(t, os.IsNotExist(statErr), "a refused save must not touch disk")
	})

	t.Run("field-empty", func(t *testing.T) {
		api, id := buildLibraryTestAPI(t)
		w := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
			`{"path":"new2.txt","content":"x","expect_version":""}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		_, statErr := os.Stat(filepath.Join(workDir(api, id), "new2.txt"))
		assert.True(t, os.IsNotExist(statErr), "a refused save must not touch disk")
	})
}

// TestLibraryContentBinaryPut_MissingVersionReturns400 is the binary door's
// half of EMB-001 ("no caller is exempt") — not itself numbered in the
// order table, which names only the text-door case, but EMB-001 states the
// requirement for BOTH doors in one sentence.
func TestLibraryContentBinaryPut_MissingVersionReturns400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	enc := base64.StdEncoding.EncodeToString([]byte{0x01})
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"new.bin","content_base64":"`+enc+`"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	_, statErr := os.Stat(filepath.Join(workDir(api, id), "new.bin"))
	assert.True(t, os.IsNotExist(statErr), "a refused save must not touch disk")
}

// --- Test 2: TestLibraryContentPut_StaleVersionReturns409WithCurrentToken ---
//
// THE LOAD-BEARING TEST. Mutation-check: delete the version comparison
// (checkLibraryVersion's call site) and confirm this test fails.

func TestLibraryContentPut_StaleVersionReturns409WithCurrentToken(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	seed := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
		`{"path":"stale.txt","content":"v0","expect_version":"v1:absent"}`)
	require.Equal(t, http.StatusOK, seed.Code, "body: %s", seed.Body.String())
	currentToken := libraryBareETag(t, seed)

	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
		`{"path":"stale.txt","content":"v1","expect_version":"a-token-nobody-issued"}`)
	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())

	var conflict gen.LibraryConflictError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &conflict))
	assert.Equal(t, "stale.txt", conflict.Path)
	require.NotNil(t, conflict.ExpectedVersion)
	assert.Equal(t, "a-token-nobody-issued", *conflict.ExpectedVersion)
	require.NotNil(t, conflict.ActualVersion)
	assert.Equal(t, currentToken, *conflict.ActualVersion)
	assert.Equal(t, gen.LibraryVersionConflict, conflict.Code)

	// The refused write must leave the file untouched.
	g := libGet(t, api, "/api/v1/library/"+id+"/content?path=stale.txt")
	require.Equal(t, http.StatusOK, g.Code)
	var resp gen.LibraryContentResponse
	require.NoError(t, json.Unmarshal(g.Body.Bytes(), &resp))
	require.NotNil(t, resp.Content)
	assert.Equal(t, "v0", *resp.Content, "a 409 must not have changed the file")
}

// --- Test 3: TestLibraryContentPut_FreshVersionSucceedsAndReturnsNewToken --
//
// Deletable-subject warning (spec, revision 3): this test's "succeeds" half
// alone would still pass with the whole feature reverted. It is included
// only as the companion to the stale-token test above and must never be
// cited alone as evidence step 0 landed — the response-header assertion is
// the half that actually distinguishes it.

func TestLibraryContentPut_FreshVersionSucceedsAndReturnsNewToken(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
		`{"path":"fresh.txt","content":"v0","expect_version":"v1:absent"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	etag := libraryBareETag(t, w)
	assert.Equal(t, string(knowledge.ComputeVersionToken([]byte("v0"))), etag,
		"the write response must carry the NEW content's token")
}

// --- Test 4: TestLibraryContentBinaryPut_StaleVersionReturns409 --------

func TestLibraryContentBinaryPut_StaleVersionReturns409(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	enc0 := base64.StdEncoding.EncodeToString([]byte{0x01})
	seed := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"stale.bin","content_base64":"`+enc0+`","expect_version":"v1:absent"}`)
	require.Equal(t, http.StatusOK, seed.Code, "body: %s", seed.Body.String())

	enc1 := base64.StdEncoding.EncodeToString([]byte{0x02})
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"stale.bin","content_base64":"`+enc1+`","expect_version":"a-token-nobody-issued"}`)
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())

	got, err := os.ReadFile(filepath.Join(workDir(api, id), "stale.bin"))
	require.NoError(t, err)
	assert.Equal(t, []byte{0x01}, got, "a 409 must not have changed the file")
}

// --- Tests 5, 6: audit records, on the REAL sink -----------------------

// TestLibraryContentPut_WritesAuditRecordOnDefaultInstall boots a REAL
// *audit.Logger — the same type and Log() method production wires in
// (agentLoop.AuditLogger()) — rather than an injected recorder, and reads
// the record back from the file it wrote. Precedent:
// pkg/knowledge/authoring_audit_default_install_test.go,
// pkg/gateway/knowledge_realboot_wiring_test.go.
func TestLibraryContentPut_WritesAuditRecordOnDefaultInstall(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	auditDir := attachTestAuditor(t, api)

	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
		`{"path":"audited.txt","content":"hello","expect_version":"v1:absent"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	entries := readAuditEntries(t, auditDir, "library.write")
	require.Len(t, entries, 1, "expected exactly one library.write record in the real audit sink")
	entry := entries[0]
	assert.Equal(t, audit.DecisionAllow, entry["decision"])
	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "entry: %+v", entry)
	assert.Equal(t, "audited.txt", details["path"])
	assert.Equal(t, false, details["binary"])
	assert.Equal(t, id, details["workspace_id"])
}

// TestLibraryContentBinaryPut_WritesAuditRecordOnDefaultInstall is the
// binary door's half of the same guarantee.
func TestLibraryContentBinaryPut_WritesAuditRecordOnDefaultInstall(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	auditDir := attachTestAuditor(t, api)

	enc := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content-binary",
		`{"path":"audited.bin","content_base64":"`+enc+`","expect_version":"v1:absent"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	entries := readAuditEntries(t, auditDir, "library.write")
	require.Len(t, entries, 1, "expected exactly one library.write record in the real audit sink")
	entry := entries[0]
	assert.Equal(t, audit.DecisionAllow, entry["decision"])
	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "entry: %+v", entry)
	assert.Equal(t, "audited.bin", details["path"])
	assert.Equal(t, true, details["binary"])
}

// --- Test: v1:absent is NOT a bypass ------------------------------------

// TestLibraryContentPut_AbsentTokenAgainstExistingFileReturns409 pins the
// property a reviewer flagged as the way this whole guard could be hollow
// while looking green: knowledge.TokenAbsent asserts "I believe this file
// does not exist" (its own doc comment). Sending it against a file that DOES
// exist must be a genuine 409, never treated as "skip the check" — otherwise
// every "first write" fixture in this package that legitimately sends
// v1:absent for a brand-new path would ALSO silently pass against an
// unguarded overwrite of someone else's existing file.
func TestLibraryContentPut_AbsentTokenAgainstExistingFileReturns409(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	seed := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
		`{"path":"exists.txt","content":"v0","expect_version":"v1:absent"}`)
	require.Equal(t, http.StatusOK, seed.Code, "body: %s", seed.Body.String())

	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
		`{"path":"exists.txt","content":"v1","expect_version":"v1:absent"}`)
	assert.Equal(t, http.StatusConflict, w.Code,
		"v1:absent against an EXISTING file must conflict, never bypass the check: body=%s", w.Body.String())

	g := libGet(t, api, "/api/v1/library/"+id+"/content?path=exists.txt")
	var resp gen.LibraryContentResponse
	require.NoError(t, json.Unmarshal(g.Body.Bytes(), &resp))
	require.NotNil(t, resp.Content)
	assert.Equal(t, "v0", *resp.Content, "the existing file must be untouched")
}

// --- Tests 97, 98, 121: ETag on both read doors -------------------------

func TestLibraryContentGet_ReturnsVersionHeader(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	work := workDir(api, id)
	require.NoError(t, os.MkdirAll(work, 0o700))

	t.Run("text", func(t *testing.T) {
		content := []byte("hello version")
		require.NoError(t, os.WriteFile(filepath.Join(work, "text.md"), content, 0o600))
		w := libGet(t, api, "/api/v1/library/"+id+"/content?path=text.md")
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		assert.Equal(t, string(knowledge.ComputeVersionToken(content)), libraryBareETag(t, w))
	})

	t.Run("binary", func(t *testing.T) {
		content := []byte{0x00, 0x01, 0xFF, 0xFE}
		require.NoError(t, os.WriteFile(filepath.Join(work, "blob.bin"), content, 0o600))
		w := libGet(t, api, "/api/v1/library/"+id+"/content?path=blob.bin")
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp gen.LibraryContentResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Nil(t, resp.Content, "a binary response must carry no content field")
		assert.Equal(t, string(knowledge.ComputeVersionToken(content)), libraryBareETag(t, w),
			"the token must come from the file's raw bytes, not the hash of an omitted content field")
	})

	t.Run("too_large", func(t *testing.T) {
		content := bytes.Repeat([]byte("A"), library.MaxContentBytes+1)
		require.NoError(t, os.WriteFile(filepath.Join(work, "huge.txt"), content, 0o600))
		w := libGet(t, api, "/api/v1/library/"+id+"/content?path=huge.txt")
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp gen.LibraryContentResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.True(t, resp.TooLarge)
		require.Nil(t, resp.Content)
		assert.Equal(t, string(knowledge.ComputeVersionToken(content)), libraryBareETag(t, w))
	})
}

func TestLibraryDownload_ReturnsVersionHeader(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	content := []byte("downloadable bytes")
	require.NoError(t, os.MkdirAll(workDir(api, id), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(workDir(api, id), "file.bin"), content, 0o600))

	w := libGet(t, api, "/api/v1/library/"+id+"/download?path=file.bin")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, string(knowledge.ComputeVersionToken(content)), libraryBareETag(t, w))
}

// --- Test: M11 — content and token must come from the SAME read ---------

// TestLibraryContentGet_TokenMatchesReturnedContentUnderConcurrentWrite
// pins the M11 fix: the ETag GET .../content returns must always be derived
// from the SAME bytes as the Content field in that same response, never from
// a second, independent read of the file. Before the fix, the handler read
// Content via root.ReadContent and THEN re-read the file from scratch via
// knowledge.ReadFileVersion for the token — a window in which a concurrent
// writer's change could land, so the response would pair one version's
// content with a DIFFERENT version's token. That is exactly the shape of
// bug B1 (useLibraryFileEditor.ts) turns into a silent lost update: a save
// built on Content, carrying this ETag as expect_version, would pass the
// server's compare-and-swap (the token really is current) while silently
// overwriting the concurrent writer's change.
//
// libraryContentGetRaceHook (test-only seam, mirrors libraryWriteRaceHook)
// fires right after root.ReadContent returns and before the token is
// derived, so this test can deterministically widen that window rather than
// relying on a timing accident.
//
// THIS TEST DIES ON: reverting the M11 fix back to an unconditional
// `knowledge.ReadFileVersion(root.HostPath(rel))` call for the token (as the
// pre-fix code did) — the hook's concurrent write would then be visible to
// that second read, and the assertions below would fail.
func TestLibraryContentGet_TokenMatchesReturnedContentUnderConcurrentWrite(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	work := workDir(api, id)
	require.NoError(t, os.MkdirAll(work, 0o700))
	target := filepath.Join(work, "race.txt")
	original := []byte("version A")
	require.NoError(t, os.WriteFile(target, original, 0o600))

	libraryContentGetRaceHook = func() {
		// A concurrent writer (e.g. an agent) lands AFTER ReadContent has
		// already captured "version A" into the response, but BEFORE the
		// (pre-fix) second read would derive the token.
		require.NoError(t, os.WriteFile(target, []byte("version B - concurrent write"), 0o600))
	}
	t.Cleanup(func() { libraryContentGetRaceHook = nil })

	w := libGet(t, api, "/api/v1/library/"+id+"/content?path=race.txt")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp gen.LibraryContentResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Content)
	require.Equal(t, "version A", *resp.Content,
		"fixture sanity: ReadContent must have captured the PRE-race bytes")

	gotToken := libraryBareETag(t, w)
	wantToken := string(knowledge.ComputeVersionToken([]byte("version A")))
	assert.Equal(t, wantToken, gotToken,
		"the ETag must be derived from the SAME bytes as Content, not a second independent "+
			"read of the file")

	// Pin the exact failure mode — the pre-fix token, not just "some other
	// token" — so this test cannot be satisfied by an unrelated regression.
	concurrentWriterToken := string(knowledge.ComputeVersionToken([]byte("version B - concurrent write")))
	assert.NotEqual(t, concurrentWriterToken, gotToken,
		"the ETag must not be the CONCURRENT writer's token — that pairing is the B1 lost update")
}

// TestLibraryVersionToken_IdenticalAcrossBothReadDoors is test 121: for the
// SAME file, GET .../content's ETag and GET .../download's ETag must be
// byte-identical, and both must equal knowledge.ReadNoteVersion's Token for
// that note — over a markdown note inside a knowledge base, a "PDF" inside
// one, and a file outside every knowledge base.
func TestLibraryVersionToken_IdenticalAcrossBothReadDoors(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	work := workDir(api, id)
	vaultDir := filepath.Join(work, "KB")
	makeKnowledgeBase(t, vaultDir, "KB")

	cases := []struct {
		name     string
		relPath  string
		inVault  bool
		content  []byte
		vaultRel string // relative to vaultDir, only when inVault
	}{
		{name: "markdown-in-kb", relPath: "KB/note.md", inVault: true, vaultRel: "note.md",
			content: []byte("# hello\n")},
		{name: "pdf-in-kb", relPath: "KB/doc.pdf", inVault: true, vaultRel: "doc.pdf",
			content: append([]byte("%PDF-1.4\n"), 0x00, 0xFF, 0xFE)},
		{name: "outside-kb", relPath: "loose.md", inVault: false,
			content: []byte("outside content")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full := filepath.Join(work, filepath.FromSlash(tc.relPath))
			require.NoError(t, os.WriteFile(full, tc.content, 0o600))

			getW := libGet(t, api, "/api/v1/library/"+id+"/content?path="+tc.relPath)
			require.Equal(t, http.StatusOK, getW.Code, "body: %s", getW.Body.String())
			getETag := libraryBareETag(t, getW)

			dlW := libGet(t, api, "/api/v1/library/"+id+"/download?path="+tc.relPath)
			require.Equal(t, http.StatusOK, dlW.Code)
			dlETag := libraryBareETag(t, dlW)

			assert.Equal(t, getETag, dlETag,
				"GET .../content and GET .../download must return byte-identical tokens")

			var wantToken knowledge.VersionToken
			if tc.inVault {
				col, err := knowledge.OpenCollection(vaultDir)
				require.NoError(t, err)
				nv, err := knowledge.ReadNoteVersion(col, tc.vaultRel)
				require.NoError(t, err)
				wantToken = nv.Token
			} else {
				wantToken = knowledge.ComputeVersionToken(tc.content)
			}
			assert.Equal(t, string(wantToken), getETag,
				"both doors' token must equal knowledge.ReadNoteVersion's for the same note")
		})
	}
}

// --- Test 123: quoted-vs-bare shape error --------------------------------

func TestLibraryContentPut_QuotedTokenInBodyIsRejectedWith400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	seed := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
		`{"path":"quoted.txt","content":"v0","expect_version":"v1:absent"}`)
	require.Equal(t, http.StatusOK, seed.Code, "body: %s", seed.Body.String())
	bareToken := libraryBareETag(t, seed)

	quotedBody := `{"path":"quoted.txt","content":"v1","expect_version":"\"` + bareToken + `\""}`
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content", quotedBody)
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"the RFC-quoted wire form sent in the body must be a 400 shape error, never a 409 "+
			"conflict a client can never clear: body=%s", w.Body.String())

	// Paired: the bare form on the identical file succeeds.
	bareBody := `{"path":"quoted.txt","content":"v1","expect_version":"` + bareToken + `"}`
	w2 := libPutJSON(t, api, "/api/v1/library/"+id+"/content", bareBody)
	assert.Equal(t, http.StatusOK, w2.Code, "body: %s", w2.Body.String())
}

// --- Tests 100, 124: the lock key -----------------------------------------

// TestLibraryContentPut_TakesTheSameLockKeyAsTheAgentPath is test 100 (G7b):
// the lock a Library write takes must be the SAME collection root, lock
// directory and collection-relative path the agent write path
// (knowledge.Writer / AuthoringDeps.begin, pkg/knowledge/authoring_tools.go)
// computes for the identical file — otherwise two writers on two different
// locks pass a sequential race test (test 99) by luck on a fast machine.
//
// The oracle is an INDEPENDENT call to knowledge.OpenCollection and
// knowledge.LockDirFor — the exact two calls AuthoringDeps.begin makes —
// over the vault's real host path, not a second invocation of
// resolveLibraryLock's own internals.
func TestLibraryContentPut_TakesTheSameLockKeyAsTheAgentPath(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	work := workDir(api, id)
	vaultDir := filepath.Join(work, "KB")
	makeKnowledgeBase(t, vaultDir, "KB")
	require.NoError(t, os.WriteFile(filepath.Join(vaultDir, "note.md"), []byte("hi"), 0o600))

	root, err := library.OpenRoot(api.homePath, id)
	require.NoError(t, err)
	defer root.Close()

	lockCfg, lockRel, err := resolveLibraryLock(root, api.homePath, id, "KB/note.md")
	require.NoError(t, err)

	wantCollection, err := knowledge.OpenCollection(vaultDir)
	require.NoError(t, err)
	wantLockDir, err := knowledge.LockDirFor(api.homePath, wantCollection.Root())
	require.NoError(t, err)

	assert.Equal(t, wantCollection.Root(), lockCfg.CollectionRoot,
		"collection root must be byte-identical to the agent path's")
	assert.Equal(t, wantLockDir, lockCfg.LockDir,
		"lock directory must be byte-identical to the agent path's")
	assert.Equal(t, "note.md", lockRel,
		"the lock's relative path must be COLLECTION-relative, matching what the agent path resolves")
}

// TestLibraryContentPut_LockKeyUsesTheInnermostEnclosingCollection is test
// 124 (G7d/M7): a knowledge base nested inside another knowledge base — the
// lock key must name the INNERMOST enclosing collection, not the outer one.
// Paired: a file with NO enclosing collection takes the degraded
// in-process-only path, asserted explicitly (G7c) rather than by the
// absence of a failure.
func TestLibraryContentPut_LockKeyUsesTheInnermostEnclosingCollection(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	work := workDir(api, id)
	outerDir := filepath.Join(work, "Outer")
	innerDir := filepath.Join(outerDir, "Inner")
	makeKnowledgeBase(t, outerDir, "Outer")
	makeKnowledgeBase(t, innerDir, "Inner")
	require.NoError(t, os.WriteFile(filepath.Join(innerDir, "note.md"), []byte("hi"), 0o600))

	root, err := library.OpenRoot(api.homePath, id)
	require.NoError(t, err)
	defer root.Close()

	lockCfg, lockRel, err := resolveLibraryLock(root, api.homePath, id, "Outer/Inner/note.md")
	require.NoError(t, err)

	wantInner, err := knowledge.OpenCollection(innerDir)
	require.NoError(t, err)
	wantOuter, err := knowledge.OpenCollection(outerDir)
	require.NoError(t, err)
	require.NotEqual(t, wantOuter.Root(), wantInner.Root(),
		"fixture sanity: the two vaults must be distinct roots")

	assert.Equal(t, wantInner.Root(), lockCfg.CollectionRoot,
		"the lock must name the INNERMOST enclosing collection, not Outer")
	assert.Equal(t, "note.md", lockRel)

	t.Run("no-enclosing-collection-is-degraded-not-disabled", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(work, "loose.md"), []byte("hi"), 0o600))
		cfg, rel, err := resolveLibraryLock(root, api.homePath, id, "loose.md")
		require.NoError(t, err)
		assert.Empty(t, cfg.CollectionRoot,
			"no enclosing collection: CollectionRoot must be empty (degraded, not disabled — G7c)")
		assert.Empty(t, cfg.LockDir)
		assert.Equal(t, id+"/loose.md", rel)
	})
}

// --- Test 99: the compare-and-write is atomic under a racing writer -----

// TestLibraryContentPut_CompareAndWriteAreAtomicUnderAgentWrite is test 99
// (the C7 test / G7a). Sequential stale-token tests (2, 4) pass against a
// lock-free implementation too — this one does not, because it uses
// libraryWriteRaceHook to deterministically pause writer 1 AFTER its
// compare succeeds and BEFORE its write lands, then attempts a second write
// over the identical file while writer 1 still holds the lock.
//
// The second writer stands in for an agent's EditNote over the same file:
// TestLibraryContentPut_TakesTheSameLockKeyAsTheAgentPath proves
// resolveLibraryLock computes the byte-identical lock key the agent write
// path would for this exact file, so racing this handler against itself
// races the SAME mutual-exclusion boundary an agent write would collide
// with.
//
// PROPERTY, NOT CLOCK (false-green-patterns.md §3): a prior version of this
// test inferred "writer 2 is blocked" from 300ms of silence on secondDone —
// on a loaded runner, a LOCK-FREE implementation whose second goroutine
// simply had not been SCHEDULED yet within that window passes exactly the
// same way a genuinely-blocked one does, so the check proved nothing under
// load. There is no production seam exposing WithNoteWriteLock's internal
// mutex acquire/release to this package (pkg/gateway is external to
// pkg/knowledge) to instrument directly — mirroring pkg/session's
// sessionLockAcquireFn/sessionLockReleaseFn (FR-101) pattern here would need
// a new exported hook in pkg/knowledge/version.go, which is backend-lead's
// call, not qa-lead's to add unilaterally.
//
// Instead this pins a real PROPERTY using the ALREADY-EXISTING
// libraryWriteRaceHook seam this test already relies on: hookCalls counts
// every entry into the shared instrumented critical section (the point
// past checkLibraryVersion, reached only once WithNoteWriteLock's lock is
// actually HELD). While writer 1 is paused inside the hook, that lock —
// real or not — is the only thing standing between writer 2 and a second
// hookCalls increment: writer 2 CANNOT increment it without first getting
// past acquireMutexByDeadline. So instead of guessing a wall-clock bound is
// "long enough" to prove absence, the loop below polls hookCalls
// continuously over a generous budget and fails THE INSTANT it changes —
// catching re-entry into the critical section itself, which happens well
// before a broken write's HTTP round trip would ever complete, so it is not
// vulnerable to secondDone being slow to fire on a loaded runner either.
// secondStarted additionally rules out "writer 2's goroutine was never
// scheduled at all" as a reason for observing no change.
func TestLibraryContentPut_CompareAndWriteAreAtomicUnderAgentWrite(t *testing.T) {
	api, id := buildLibraryTestAPI(t)

	seed := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
		`{"path":"race.txt","content":"v0","expect_version":"v1:absent"}`)
	require.Equal(t, http.StatusOK, seed.Code, "body: %s", seed.Body.String())
	t0 := libraryBareETag(t, seed)

	entered := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	var hookCalls atomic.Int32
	libraryWriteRaceHook = func() {
		hookCalls.Add(1)
		once.Do(func() { close(entered) })
		<-proceed
	}
	t.Cleanup(func() { libraryWriteRaceHook = nil })

	var (
		wg     sync.WaitGroup
		w1Code int
		w1Body string
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
			`{"path":"race.txt","content":"from-writer-1","expect_version":"`+t0+`"}`)
		w1Code, w1Body = w.Code, w.Body.String()
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("writer 1 never reached the race hook — checkLibraryVersion or the lock wiring changed shape")
	}
	// Writer 1 is now past its compare and holding the lock (hookCalls==1),
	// paused before its write.
	require.EqualValues(t, 1, hookCalls.Load(), "fixture sanity: only writer 1 has reached the critical section so far")

	secondStarted := make(chan struct{})
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		close(secondStarted) // proves the goroutine was actually scheduled and is running
		secondDone <- libPutJSON(t, api, "/api/v1/library/"+id+"/content",
			`{"path":"race.txt","content":"from-writer-2","expect_version":"`+t0+`"}`)
	}()

	select {
	case <-secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("writer 2's goroutine was never scheduled — cannot judge blocking from a goroutine that never ran")
	}

	// Poll the PROPERTY (hookCalls, and secondDone as a belt-and-suspenders
	// check) over a generous budget, failing the instant either changes —
	// not after a fixed sleep elapses.
	pollDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(pollDeadline) {
		if n := hookCalls.Load(); n > 1 {
			t.Fatalf("writer 2 entered the write critical section (hookCalls=%d) while writer 1 "+
				"still held the lock — the compare-and-swap is not exclusive", n)
		}
		select {
		case <-secondDone:
			t.Fatal("the second writer completed while writer 1 still held the lock — " +
				"the compare-and-swap is not exclusive")
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.EqualValues(t, 1, hookCalls.Load(),
		"writer 2 must still be blocked acquiring the lock (never having reached the critical "+
			"section), not merely slow to respond, at the moment writer 1 is about to release it")

	close(proceed) // let writer 1's write land and release the lock
	wg.Wait()
	require.Equal(t, http.StatusOK, w1Code, "body: %s", w1Body)

	var w2 *httptest.ResponseRecorder
	select {
	case w2 = <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("writer 2 never completed after the lock was released")
	}
	assert.Equal(t, http.StatusConflict, w2.Code,
		"writer 2 must see writer 1's NEW token once it finally acquires the lock, and be "+
			"refused rather than silently overwriting: body=%s", w2.Body.String())

	g := libGet(t, api, "/api/v1/library/"+id+"/content?path=race.txt")
	require.Equal(t, http.StatusOK, g.Code)
	var resp gen.LibraryContentResponse
	require.NoError(t, json.Unmarshal(g.Body.Bytes(), &resp))
	require.NotNil(t, resp.Content)
	assert.Equal(t, "from-writer-1", *resp.Content, "exactly one write must survive")
}
