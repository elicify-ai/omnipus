// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// ADR-067 Stage 0 at the REST layer: name-shape validation is wired into the
// five create/rename handlers and nowhere else, and the download handler
// builds an RFC 6266 Content-Disposition.
//
// Every test here names the mutation it exists to kill. The design constraint
// that shaped all of them: on a POSIX build the ONLY active name-shape rule is
// a 255-BYTE per-component budget, so any assertion phrased in terms of
// Windows-illegal characters ("Meeting: notes.md is refused") proves nothing
// on the Linux and macOS runners that are the only ones CI has — it passes
// whether the wiring exists or not. The probe used throughout is therefore a
// 300-byte component, which is over budget under BOTH rule sets and so gives
// the same verdict on every platform.

package gateway

import (
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// overlongName is 300 ASCII bytes in 300 runes. Refused by POSIXRules (over
// the 255-byte component budget) AND by WindowsRules (over the 100-rune
// budget), which is what makes every assertion below platform-independent.
//
// It is also longer than any filesystem Omnipus targets will accept, which is
// the second half of the trick: with the wiring in place the caller gets 400
// from ValidateCreateName; with it removed the create reaches the kernel,
// ENAMETOOLONG comes back, translateErr does not recognise it, and the caller
// gets 500. The two outcomes are distinguishable, so "assert 400" is a real
// assertion and not a synonym for "assert it failed somehow".
var overlongName = strings.Repeat("a", 300)

// stage0Workspace builds the standard Library test API plus a second
// workspace, for the cross-workspace copy/move cases.
func stage0Workspace(t *testing.T) (*restAPI, string) {
	t.Helper()
	api, id := buildLibraryTestAPI(t)
	require.NoError(t, os.MkdirAll(workDir(api, id), 0o700))
	return api, id
}

// stage0Mount materialises a real mounted folder named "vault" on workspaceID,
// pointing at a directory outside $OMNIPUS_HOME, and returns that host path.
// Goes through workspace.CreateMount — the same call the REST mount endpoint
// makes — so the mount store, the symlink and library.Root's mount table all
// agree, rather than a hand-built fixture that only resembles one.
func stage0Mount(t *testing.T, api *restAPI, workspaceID string) string {
	t.Helper()
	host := t.TempDir()
	_, _, err := workspace.CreateMount(api.homePath, workspaceID, "vault", host)
	require.NoError(t, err, "seeding the mount is setup, not the thing under test")
	return host
}

// TestLibraryCreate_NameShapeRefusedInWorkspaceStorage is the wiring test for
// FR-0001a: all five create/rename handlers must consult
// (*Root).ValidateCreateName on their DESTINATION path.
//
// MUTATION IT DIES ON: delete the checkCreateName call from any one of
// handleLibraryContentPut, handleLibraryMkdir, handleLibraryRename or
// handleLibraryTransfer. That handler's subtest then returns 500 (the kernel's
// ENAMETOOLONG, unrecognised by translateErr) instead of 400.
func TestLibraryCreate_NameShapeRefusedInWorkspaceStorage(t *testing.T) {
	t.Run("content-put", func(t *testing.T) {
		api, id := stage0Workspace(t)
		w := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
			`{"path":"`+overlongName+`","content":"x"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code,
			"a 300-byte name Omnipus is CREATING must be refused by name-shape validation, "+
				"not by the kernel; body=%s", w.Body.String())

		entries, err := os.ReadDir(workDir(api, id))
		require.NoError(t, err)
		assert.Empty(t, entries, "the refusal must happen before anything is written")
	})

	t.Run("mkdir", func(t *testing.T) {
		api, id := stage0Workspace(t)
		w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"`+overlongName+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	})

	t.Run("mkdir-intermediate-component", func(t *testing.T) {
		// The leaf is harmless; the INTERMEDIATE directory is not. A per-leaf
		// check would create the unportable parent and never look at it again.
		api, id := stage0Workspace(t)
		w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"`+overlongName+`/notes"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	})

	t.Run("rename", func(t *testing.T) {
		api, id := stage0Workspace(t)
		require.Equal(t, http.StatusOK,
			libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"src.txt","content":"x"}`).Code)

		w := libPostJSON(t, api, "/api/v1/library/"+id+"/rename",
			`{"from":"src.txt","to":"`+overlongName+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	})

	t.Run("move", func(t *testing.T) {
		api, id := stage0Workspace(t)
		require.Equal(t, http.StatusOK,
			libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"src.txt","content":"x"}`).Code)

		body := `{"from_workspace_id":"` + id + `","from_path":"src.txt",` +
			`"to_workspace_id":"` + id + `","to_path":"` + overlongName + `"}`
		w := libPostJSON(t, api, "/api/v1/library/move", body)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	})

	t.Run("copy-cross-workspace-validates-the-destination-workspace", func(t *testing.T) {
		// The destination is a DIFFERENT root from the source. Validating
		// fromRoot would consult the wrong workspace's mount table.
		api, from := stage0Workspace(t)
		to := seedLibraryWorkspace(t, api, "Destination WS")
		require.Equal(t, http.StatusOK,
			libPutJSON(t, api, "/api/v1/library/"+from+"/content", `{"path":"src.txt","content":"x"}`).Code)

		body := `{"from_workspace_id":"` + from + `","from_path":"src.txt",` +
			`"to_workspace_id":"` + to + `","to_path":"` + overlongName + `"}`
		w := libPostJSON(t, api, "/api/v1/library/copy", body)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	})
}

// TestLibraryCreate_NameShapeSkippedInsideAMount is FR-0001b at the REST
// layer: a mounted folder is the operator's own disk, so Omnipus applies none
// of its portability rules to a create inside one. The host filesystem stays
// the authority on what it will accept, and it says so in its own error.
//
// The assertion is deliberately "not 400, and specifically 500". 500 here is
// the kernel's ENAMETOOLONG arriving unrecognised — i.e. proof that the create
// was actually ATTEMPTED rather than pre-refused on naming grounds. Asserting
// only "not 2xx" would pass under the very mutation this test exists to catch.
//
// MUTATION IT DIES ON: change checkCreateName to validate rel directly against
// pkg/pathsafe instead of through the destination root — the plausible
// "simplification" that drops the *Root receiver and with it the mount
// predicate. Every subtest here then returns 400.
func TestLibraryCreate_NameShapeSkippedInsideAMount(t *testing.T) {
	t.Run("mkdir", func(t *testing.T) {
		api, id := stage0Workspace(t)
		stage0Mount(t, api, id)

		w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"vault/`+overlongName+`"}`)
		assert.NotEqual(t, http.StatusBadRequest, w.Code,
			"inside a mount Omnipus must not refuse a name on shape grounds — the operator's "+
				"filesystem decides; body=%s", w.Body.String())
		assert.Equal(t, http.StatusInternalServerError, w.Code,
			"the create must reach the host filesystem and fail there (ENAMETOOLONG), which is "+
				"how we know it was attempted rather than pre-refused; body=%s", w.Body.String())
	})

	t.Run("content-put", func(t *testing.T) {
		api, id := stage0Workspace(t)
		stage0Mount(t, api, id)

		w := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
			`{"path":"vault/`+overlongName+`","content":"x"}`)
		assert.NotEqual(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
	})
}

// TestLibraryMountedCreate_WindowsIllegalNameAccepted is the positive half of
// FR-0001b, and the one that matches the user story: the operator writes a
// note into their own mounted vault, named the way their notes are named.
//
// Skipped on Windows, where the host filesystem itself refuses the colon —
// which is the correct outcome there and is the host's call, not Omnipus's.
//
// It carries no mutation of its own, and deliberately so: measured, it
// survives dropping the mount predicate entirely, because POSIXRules accepts a
// colon anyway. Its job is the end-to-end statement — the bytes land in the
// operator's real folder under the name they gave — not the rule-set proof,
// which TestLibraryCreate_NameShapeSkippedInsideAMount owns.
func TestLibraryMountedCreate_WindowsIllegalNameAccepted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the host filesystem refuses ':' here; FR-0001b defers to it")
	}
	api, id := stage0Workspace(t)
	host := stage0Mount(t, api, id)

	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content",
		`{"path":"vault/Meeting: 2026-01-01.md","content":"notes"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	written, err := os.ReadFile(filepath.Join(host, "Meeting: 2026-01-01.md"))
	require.NoError(t, err, "the file must land in the operator's real folder under the name they gave")
	assert.Equal(t, "notes", string(written))
}

// TestLibraryRead_NameShapeNeverAppliedToExistingFiles is FR-0001 end to end
// through this layer: the read/list/download/delete handlers show the operator
// what is on their disk, whatever it is called. These three names are the
// measured failures from the reference vault — a colon, a question mark, and a
// 106-rune basename (2 of 748 notes exceed 100 runes).
//
// MUTATION IT DIES ON: apply pathsafe.WindowsRules to rel in
// handleLibraryContentGet — i.e. the pre-Stage-0 world, Windows filename rules
// enforced on every platform. The three "open" subtests then return 400. That
// is the regression this test exists for, and it is what a Stage 0 revert
// looks like from here.
//
// MUTATION IT DOES *NOT* DIE ON, measured rather than assumed: adding a plain
// checkCreateName call to this same handler. It survives, and it must be
// understood why before anyone strengthens the test — the fix is not a better
// assertion, because none exists. On a POSIX build the only active shape rule
// is a 255-BYTE per-component budget, and NAME_MAX makes a component that
// violates it impossible to put on disk in the first place. There is
// therefore no file whose read this over-application could refuse, on this
// platform, by construction. FR-0004's "a name already on disk is by
// construction inside its own filesystem's limit" is exactly that fact. The
// same call added to a create handler IS observable, because a create can
// propose a name the filesystem would never have accepted — which is why
// TestLibraryCreate_NameShapeRefusedInWorkspaceStorage bites and this cannot.
func TestLibraryRead_NameShapeNeverAppliedToExistingFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("these names cannot be planted on an NTFS volume; the read path is POSIX-observable only")
	}
	long := strings.Repeat("k", 102) + ".md" // 105 runes, 105 bytes: over the Windows rune cap, inside POSIX's byte cap
	names := []string{"Meeting: 2026-01-01.md", "Why?.md", long}

	api, id := stage0Workspace(t)
	for _, n := range names {
		require.NoError(t, os.WriteFile(filepath.Join(workDir(api, id), n), []byte("body"), 0o600))
	}

	t.Run("list", func(t *testing.T) {
		w := libGet(t, api, "/api/v1/library/"+id+"/entries")
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		listed := map[string]bool{}
		for _, e := range decodeEntries(t, w.Body.Bytes()) {
			listed[e.Name] = true
		}
		for _, n := range names {
			assert.True(t, listed[n], "%q is on disk and must appear in the listing", n)
		}
	})

	for _, n := range names {
		t.Run("open/"+n, func(t *testing.T) {
			w := libGet(t, api, "/api/v1/library/"+id+"/content?path="+url.QueryEscape(n))
			assert.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		})
		t.Run("download/"+n, func(t *testing.T) {
			w := libGet(t, api, "/api/v1/library/"+id+"/download?path="+url.QueryEscape(n))
			assert.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		})
	}

	t.Run("delete", func(t *testing.T) {
		// Delete is not one of FR-0001a's five. An operator must be able to
		// remove a file whose name Omnipus would decline to create.
		w := libDelete(t, api, "/api/v1/library/"+id+"/entries?path="+url.QueryEscape(names[0]))
		assert.Equal(t, http.StatusNoContent, w.Code, "body=%s", w.Body.String())
	})
}

// TestLibraryDownload_ContentDispositionRFC6266 covers FR-0003.
//
// The two injection cases are REGRESSION CONTROLS, not new coverage: measured
// against the previous fmt.Sprintf("attachment; filename=%q", …), a double
// quote was already escaped and control characters could not reach here at
// all. They are here to prove the rewrite did not lose a property that already
// held. The non-ASCII case is the actual defect.
//
// MUTATION IT DIES ON: restore fmt.Sprintf("attachment; filename=%q",
// filename). The "non-ASCII" subtest fails on both of its load-bearing
// assertions — the header stops being pure ASCII, and filename* disappears —
// while the ASCII and quote subtests keep passing, which is itself the
// evidence that ordinary downloads' headers are unchanged.
func TestLibraryDownload_ContentDispositionRFC6266(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip(`a filename containing '"' cannot be planted on an NTFS volume`)
	}
	api, id := stage0Workspace(t)

	header := func(t *testing.T, name string) string {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(workDir(api, id), name), []byte("payload"), 0o600))
		w := libGet(t, api, "/api/v1/library/"+id+"/download?path="+url.QueryEscape(name))
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		return w.Header().Get("Content-Disposition")
	}

	t.Run("plain-ASCII-is-byte-identical-to-the-old-header", func(t *testing.T) {
		assert.Equal(t, `attachment; filename="report.txt"`, header(t, "report.txt"),
			"the overwhelming majority of downloads must not have their headers churned by this change")
	})

	t.Run("regression-control/embedded-quote-still-escaped", func(t *testing.T) {
		got := header(t, `he"llo.txt`)
		assert.Equal(t, `attachment; filename="he\"llo.txt"`, got)

		_, params, err := mime.ParseMediaType(got)
		require.NoError(t, err, "a header a parser rejects is a header that failed")
		assert.Equal(t, `he"llo.txt`, params["filename"])
	})

	t.Run("regression-control/no-CR-or-LF-can-reach-the-header", func(t *testing.T) {
		// CleanRelPath refuses control characters upstream, so the download
		// never even runs. Asserted here because THIS is the property a reader
		// worries about when they see raw string concatenation into a header.
		w := libGet(t, api, "/api/v1/library/"+id+"/download?path="+url.QueryEscape("bad\r\nX-Evil: 1.txt"))
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	})

	t.Run("non-ASCII", func(t *testing.T) {
		const name = "Ünïcödé — Näme.md"
		got := header(t, name)

		for i := 0; i < len(got); i++ {
			require.Less(t, got[i], byte(0x80),
				"an HTTP header field value must be ASCII; a raw UTF-8 byte in a quoted-string "+
					"carries no charset and is read as Latin-1 by some clients. header=%q", got)
		}
		assert.Contains(t, got, "filename*=UTF-8''",
			"the exact name must travel in an RFC 5987 ext-value")
		assert.Contains(t, got, `filename="`,
			"FR-0003 requires an ASCII fallback ALONGSIDE filename*, which mime.FormatMediaType does not emit")

		_, params, err := mime.ParseMediaType(got)
		require.NoError(t, err, "header=%q", got)
		assert.Equal(t, name, params["filename"],
			"a conforming client must recover the name exactly, accents and em dash intact")
	})

	t.Run("all-non-ASCII-name-still-has-a-usable-fallback", func(t *testing.T) {
		got := header(t, "文档.md")
		assert.Contains(t, got, `filename="__.md"`,
			"the fallback is allowed to be lossy but must never be empty — an empty or absent "+
				"filename parameter is how a download silently becomes an inline render")
		_, params, err := mime.ParseMediaType(got)
		require.NoError(t, err)
		assert.Equal(t, "文档.md", params["filename"])
	})
}

// TestLibraryUpload_DestinationValidatedNotJustLeaf covers the fifth handler.
//
// HONEST LIMITATION, stated rather than papered over and MEASURED, not
// assumed: deleting the ValidateCreateName call from handleLibraryUpload
// leaves this whole file green. On a POSIX build that call site is provably
// redundant and no mutation of it can be made to fail here. agent.SanitizeUploadFilename already validates the leaf against the
// same active rule set, and the only POSIX shape rule is a 255-byte
// per-component budget — which no directory on disk can violate, because the
// filesystem refused to create it. The two rules ValidateCreateName adds over
// the leaf check (targetDir's own segments, and the whole-path MAX_PATH
// budget) are both inert under POSIXRules. The wiring is required by FR-0001a
// and matters on a Windows build; it is unobservable on the runners CI has.
//
// What these assertions DO buy: proof that adding the call introduced no false
// refusal on the paths operators actually use — a nested workspace directory,
// and a mounted folder.
func TestLibraryUpload_DestinationValidatedNotJustLeaf(t *testing.T) {
	t.Run("nested-workspace-directory-still-accepts-uploads", func(t *testing.T) {
		api, id := stage0Workspace(t)
		require.Equal(t, http.StatusCreated,
			libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"notes/2026"}`).Code)

		w := libUpload(t, api, "/api/v1/library/"+id+"/upload?path=notes%2F2026",
			map[string]string{"doc.txt": "hello"})
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	})

	t.Run("mounted-folder-still-accepts-uploads", func(t *testing.T) {
		api, id := stage0Workspace(t)
		host := stage0Mount(t, api, id)

		w := libUpload(t, api, "/api/v1/library/"+id+"/upload?path=vault",
			map[string]string{"doc.txt": "hello"})
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

		got, err := os.ReadFile(filepath.Join(host, "doc.txt"))
		require.NoError(t, err, "the upload must land in the operator's real folder")
		assert.Equal(t, "hello", string(got))
	})
}
