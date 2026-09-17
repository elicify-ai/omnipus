// Regression tests for UAT re-test row U-58 (2026-09-14, build dd25339bf):
// deleting a knowledge base's own `.omnipus-vault` folder from the Library
// answered 400 "reserved location" and deleted nothing.
//
// Round 4 (f32977b42) sent every folder inside a knowledge base through the
// knowledge layer, and the knowledge layer refuses tool-state directories by
// name (`.omnipus-vault`, `.obsidian`, `.git`, `.trash`). Those directories
// are not part of the knowledge base the layer models — every walker skips
// them, nothing in them is indexed or linked — so the knowledge layer has
// nothing to do for them except refuse. Before round 4 the Library door
// handled them as plain files and folders; these tests pin that it does again.
//
// Every request goes through HandleLibraryTree, the handler the gateway mux
// registers for "/api/v1/library/" (rest.go), not a handler reached directly.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// libTree sends one request through the Library subtree router.
func libTree(t *testing.T, api *restAPI, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rdr)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	api.HandleLibraryTree(w, r)
	return w
}

// detectKnowledgeBase asks the product's own detection endpoint — the call the
// KnowledgePanel makes — whether folder is a knowledge base.
func detectKnowledgeBase(t *testing.T, api *restAPI, ws, folder string) gen.KnowledgeBaseInfo {
	t.Helper()
	w := libTree(t, api, http.MethodGet, "/api/v1/library/"+ws+"/knowledge?path="+folder, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var info gen.KnowledgeBaseInfo
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &info))
	return info
}

// snapshotOutsideToolState reads every file under dir except those inside a
// tool-state directory, keyed by slash path. Comparing two snapshots proves no
// note or attachment was rewritten, trashed or added.
func snapshotOutsideToolState(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".omnipus-vault", ".obsidian", ".git", ".trash":
				return filepath.SkipDir
			}
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", p, rerr)
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	}))
	return out
}

// buildReservedVault is buildFolderVault (notes that link and embed each
// other) plus content in every tool-state directory the knowledge layer
// refuses.
func buildReservedVault(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	api, ws, vault := buildFolderVault(t)
	writeNote(t, vault, ".omnipus-vault/records/project.yaml", "schema_version: 1\ntype: project\n")
	writeNote(t, vault, ".omnipus-vault/templates/meeting.md", "---\ntype: meeting\n---\n# Meeting\n")
	writeNote(t, vault, ".omnipus-vault/trash/2026-09-01T10-00-00Z/Old.md", "# Old\n")
	writeNote(t, vault, ".obsidian/app.json", "{}\n")
	writeNote(t, vault, ".trash/Discarded.md", "# Discarded\n")
	writeNote(t, vault, ".git/HEAD", "ref: refs/heads/main\n")
	return api, ws, vault
}

// TestLibraryDelete_VaultMarkerDemotesKnowledgeBase is U-58 as the plan states
// it: delete `.omnipus-vault` from the Library, and the folder stops being a
// knowledge base, its notes stay as plain files, and nothing breaks.
func TestLibraryDelete_VaultMarkerDemotesKnowledgeBase(t *testing.T) {
	api, ws, vault := buildFolderVault(t)
	writeNote(t, vault, ".omnipus-vault/records/project.yaml", "schema_version: 1\ntype: project\n")
	notesBefore := snapshotOutsideToolState(t, vault)
	require.Contains(t, notesBefore, "Projects/Brief.md", "precondition: the fixture has linked notes")
	require.True(t, detectKnowledgeBase(t, api, ws, "vault").IsKnowledgeBase, "precondition: vault is a knowledge base")

	w := libTree(t, api, http.MethodDelete, "/api/v1/library/"+ws+"/entries?path=vault/.omnipus-vault", "")
	require.Equal(t, http.StatusNoContent, w.Code,
		"U-58: deleting the knowledge base's own marker folder must succeed, not be refused as a reserved location: %s", w.Body.String())

	_, err := os.Stat(filepath.Join(vault, ".omnipus-vault"))
	require.True(t, os.IsNotExist(err), "the marker folder is gone, with everything in it")

	info := detectKnowledgeBase(t, api, ws, "vault")
	require.False(t, info.IsKnowledgeBase, "the folder is no longer a knowledge base")
	require.Equal(t, gen.KnowledgeBaseInfoMarkerNone, info.Marker)

	require.Equal(t, notesBefore, snapshotOutsideToolState(t, vault),
		"every note and attachment stays exactly as it was: nothing rewritten, trashed or added")
	_, err = os.Stat(filepath.Join(workDir(api, ws), ".omnipus-vault"))
	require.True(t, os.IsNotExist(err), "no marker is conjured anywhere else")

	// No crash, and the demoted folder behaves as plain files from here on: a
	// note delete is an ordinary delete, which would have recreated
	// .omnipus-vault/trash/ if the knowledge layer still governed the folder.
	w = libTree(t, api, http.MethodGet, "/api/v1/library/"+ws+"/entries?path=vault", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = libTree(t, api, http.MethodDelete, "/api/v1/library/"+ws+"/entries?path=vault/Projects/Brief.md", "")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	_, err = os.Stat(filepath.Join(vault, "Projects", "Brief.md"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(vault, ".omnipus-vault"))
	require.True(t, os.IsNotExist(err), "a delete in the demoted folder goes to no knowledge-base trash")
}

// TestLibraryDoors_ToolStateLocationsKeepPlainSemantics covers every Library
// action on a tool-state directory, or on something inside one, that round 4
// turned into a 400. Each gets the plain filesystem outcome it had before.
func TestLibraryDoors_ToolStateLocationsKeepPlainSemantics(t *testing.T) {
	type check func(t *testing.T, vault string)
	gone := func(rel string) check {
		return func(t *testing.T, vault string) {
			_, err := os.Stat(filepath.Join(vault, filepath.FromSlash(rel)))
			require.Truef(t, os.IsNotExist(err), "%s must be gone", rel)
		}
	}
	present := func(rel string) check {
		return func(t *testing.T, vault string) {
			_, err := os.Stat(filepath.Join(vault, filepath.FromSlash(rel)))
			require.NoErrorf(t, err, "%s must exist", rel)
		}
	}
	sameBytes := func(rel, want string) check {
		return func(t *testing.T, vault string) {
			raw, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(rel)))
			require.NoErrorf(t, err, "%s must exist", rel)
			require.Equal(t, want, string(raw), rel)
		}
	}
	transfer := func(from, to string) string {
		return `{"from_workspace_id":"WS","from_path":"` + from + `","to_workspace_id":"WS","to_path":"` + to + `"}`
	}

	cases := []struct {
		name   string
		method string
		target string // WS is replaced by the workspace id
		body   string
		status int
		checks []check
	}{
		{
			name: "delete a record type file inside the marker", method: http.MethodDelete,
			target: "/api/v1/library/WS/entries?path=vault/.omnipus-vault/records/project.yaml",
			status: http.StatusNoContent,
			checks: []check{gone(".omnipus-vault/records/project.yaml"), present(".omnipus-vault/vault.json")},
		},
		{
			name: "delete a note-shaped template inside the marker", method: http.MethodDelete,
			target: "/api/v1/library/WS/entries?path=vault/.omnipus-vault/templates/meeting.md",
			status: http.StatusNoContent,
			checks: []check{gone(".omnipus-vault/templates/meeting.md")},
		},
		{
			name: "delete the knowledge base trash folder", method: http.MethodDelete,
			target: "/api/v1/library/WS/entries?path=vault/.omnipus-vault/trash",
			status: http.StatusNoContent,
			checks: []check{gone(".omnipus-vault/trash"), present(".omnipus-vault/vault.json")},
		},
		{
			name: "delete the Obsidian config folder", method: http.MethodDelete,
			target: "/api/v1/library/WS/entries?path=vault/.obsidian",
			status: http.StatusNoContent,
			checks: []check{gone(".obsidian")},
		},
		{
			name: "delete Obsidian's own trash folder", method: http.MethodDelete,
			target: "/api/v1/library/WS/entries?path=vault/.trash",
			status: http.StatusNoContent,
			checks: []check{gone(".trash")},
		},
		{
			name: "delete a note inside Obsidian's trash", method: http.MethodDelete,
			target: "/api/v1/library/WS/entries?path=vault/.trash/Discarded.md",
			status: http.StatusNoContent,
			checks: []check{gone(".trash/Discarded.md")},
		},
		{
			name: "delete a git folder", method: http.MethodDelete,
			target: "/api/v1/library/WS/entries?path=vault/.git",
			status: http.StatusNoContent,
			checks: []check{gone(".git")},
		},
		{
			name: "rename the marker folder", method: http.MethodPost,
			target: "/api/v1/library/WS/rename",
			body:   `{"from":"vault/.omnipus-vault","to":"vault/omnipus-vault-backup"}`,
			status: http.StatusOK,
			checks: []check{gone(".omnipus-vault"), present("omnipus-vault-backup/vault.json")},
		},
		{
			name: "rename a template inside the marker", method: http.MethodPost,
			target: "/api/v1/library/WS/rename",
			body:   `{"from":"vault/.omnipus-vault/templates/meeting.md","to":"vault/.omnipus-vault/templates/standup.md"}`,
			status: http.StatusOK,
			checks: []check{
				gone(".omnipus-vault/templates/meeting.md"),
				sameBytes(".omnipus-vault/templates/standup.md", "---\ntype: meeting\n---\n# Meeting\n"),
			},
		},
		{
			name: "rename a file inside the Obsidian config", method: http.MethodPost,
			target: "/api/v1/library/WS/rename",
			body:   `{"from":"vault/.obsidian/app.json","to":"vault/.obsidian/app.backup.json"}`,
			status: http.StatusOK,
			checks: []check{gone(".obsidian/app.json"), sameBytes(".obsidian/app.backup.json", "{}\n")},
		},
		{
			name: "move the marker folder to another folder", method: http.MethodPost,
			target: "/api/v1/library/move",
			body:   transfer("vault/.omnipus-vault", "vault/Projects/.omnipus-vault"),
			status: http.StatusOK,
			checks: []check{gone(".omnipus-vault"), present("Projects/.omnipus-vault/vault.json")},
		},
		{
			name: "move the templates folder out of the marker", method: http.MethodPost,
			target: "/api/v1/library/move",
			body:   transfer("vault/.omnipus-vault/templates", "vault/Templates"),
			status: http.StatusOK,
			checks: []check{gone(".omnipus-vault/templates"), present("Templates/meeting.md")},
		},
		{
			name: "copy the marker folder (never routed; control)", method: http.MethodPost,
			target: "/api/v1/library/copy",
			body:   transfer("vault/.omnipus-vault", "vault/marker-copy"),
			status: http.StatusCreated,
			checks: []check{present(".omnipus-vault/vault.json"), present("marker-copy/vault.json")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, ws, vault := buildReservedVault(t)
			notesBefore := snapshotOutsideToolState(t, vault)

			w := libTree(t, api, tc.method,
				strings.ReplaceAll(tc.target, "WS", ws), strings.ReplaceAll(tc.body, `"WS"`, `"`+ws+`"`))
			require.Equalf(t, tc.status, w.Code, "body: %s", w.Body.String())
			for _, c := range tc.checks {
				c(t, vault)
			}

			after := snapshotOutsideToolState(t, vault)
			// A move or copy out of a tool-state directory adds files outside it
			// by design; everything that was already outside must be untouched.
			for rel, body := range notesBefore {
				require.Equalf(t, body, after[rel], "%s must not be rewritten, trashed or removed", rel)
			}
		})
	}
}

// TestLibraryMove_IntoToolStateLocationIsStillRefused pins the other direction,
// which this fix deliberately leaves as rounds 3 and 4 made it. Moving an
// ordinary note or folder of the knowledge base INTO a tool-state directory
// hides it from search, links and backlinks at once, with no trash and no
// restore — the knowledge layer refuses that on purpose (rename.go), and the
// Library door, which routes ordinary entries through that layer, refuses it
// too, with the reason, and changes nothing.
func TestLibraryMove_IntoToolStateLocationIsStillRefused(t *testing.T) {
	api, ws, vault := buildReservedVault(t)
	notesBefore := snapshotOutsideToolState(t, vault)

	w := libTree(t, api, http.MethodPost, "/api/v1/library/move",
		`{"from_workspace_id":"`+ws+`","from_path":"vault/People/Tobias Brandt.md",`+
			`"to_workspace_id":"`+ws+`","to_path":"vault/.omnipus-vault/templates/Tobias Brandt.md"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "reserved location")

	w = libTree(t, api, http.MethodPost, "/api/v1/library/"+ws+"/rename",
		`{"from":"vault/assets","to":"vault/.trash/assets"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "reserved location")

	require.Equal(t, notesBefore, snapshotOutsideToolState(t, vault), "a refused move changes nothing")
}
