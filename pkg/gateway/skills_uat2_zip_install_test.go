package gateway

// UAT run 2 R2-2 and R2-4 (skills spec r15 §5.10, §7.1):
//   - R2-2: a zip whose only top-level entry is a skill folder (SKILL.md not at
//     the zip root) is the uploader's mistake: 400 with text saying SKILL.md
//     must be at the zip root, nothing written. Run 2 saw 502 "server storage
//     or configuration failed". Only this case is in scope; other staging
//     failures staying 502 is parked #1573.
//   - R2-4: installing a zip keeps the owner exec bit of files under scripts/
//     (0755 stays executable) and adds no exec bit anywhere else.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

type uat2ZipEntry struct {
	name string
	mode fs.FileMode
	body string
}

func uat2BuildZip(t *testing.T, entries []uat2ZipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		require.NoError(t, err)
		_, err = w.Write([]byte(e.body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// uat2InstallZip uploads data as <slug>.zip and POSTs the install request.
func uat2InstallZip(t *testing.T, api *restAPI, slug string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	return uat2InstallNamed(t, api, slug+".zip", data, nil)
}

// uat2InstallNamed uploads data under fileName (the on-disk and stored name,
// as the upload handler leaves it) and POSTs the install request with extra
// fields merged in.
func uat2InstallNamed(t *testing.T, api *restAPI, fileName string, data []byte, extra map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	uploadDir := filepath.Join(api.homePath, "uploads", "owner-session")
	require.NoError(t, os.MkdirAll(uploadDir, 0o755))
	uploadPath := filepath.Join(uploadDir, fileName)
	require.NoError(t, os.WriteFile(uploadPath, data, 0o644))
	store := newTestFileMediaStore(t)
	api.mediaStore = store
	ref, err := store.Store(uploadPath, media.MediaMeta{Filename: fileName, Source: "upload:webchat", CleanupPolicy: media.CleanupPolicyForgetOnly}, "upload:owner-session")
	require.NoError(t, err)
	req := map[string]any{"upload_id": ref}
	for k, v := range extra {
		req[k] = v
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleSkills(w, r)
	return w
}

const uat2SkillMD = "---\nname: demo-a\ndescription: Use when a zip install test needs a skill.\n---\n\nBody.\n"

func TestInstallSkillZipWrappedInFolder_Returns400SkillMdAtRoot(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	data := uat2BuildZip(t, []uat2ZipEntry{
		{name: "demo-a/SKILL.md", mode: 0o644, body: uat2SkillMD},
		{name: "demo-a/reference/a.md", mode: 0o644, body: "ref\n"},
	})
	w := uat2InstallZip(t, api, "demo-a", data)

	assert.Equal(t, http.StatusBadRequest, w.Code, "a wrapped zip is the uploader's mistake, not a server fault; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "SKILL.md", "the text names the file")
	assert.Contains(t, w.Body.String(), "root", "the text says it must be at the zip root")
	assert.NotContains(t, w.Body.String(), "server storage or configuration failed")

	_, statErr := os.Stat(filepath.Join(api.homePath, "skills", "demo-a"))
	assert.ErrorIs(t, statErr, os.ErrNotExist, "nothing was installed")
	staged, readErr := os.ReadDir(filepath.Join(api.homePath, "skills", ".staging"))
	if readErr != nil {
		assert.ErrorIs(t, readErr, os.ErrNotExist)
	} else {
		assert.Empty(t, staged, "staging is left empty")
	}
}

func TestInstallSkillZipFlatRoot_InstallsControl(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	data := uat2BuildZip(t, []uat2ZipEntry{
		{name: "SKILL.md", mode: 0o644, body: uat2SkillMD},
		{name: "reference/a.md", mode: 0o644, body: "ref\n"},
	})
	w := uat2InstallZip(t, api, "demo-a", data)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	_, err := os.Stat(filepath.Join(api.homePath, "skills", "demo-a", "SKILL.md"))
	require.NoError(t, err)
}

func TestInstallSkillZip_KeepsOwnerExecBitUnderScriptsOnly(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	data := uat2BuildZip(t, []uat2ZipEntry{
		{name: "SKILL.md", mode: 0o644, body: uat2SkillMD},
		{name: "scripts/run.sh", mode: 0o755, body: "#!/bin/sh\necho hi\n"},
		{name: "reference/a.md", mode: 0o644, body: "ref\n"},
	})
	w := uat2InstallZip(t, api, "demo-a", data)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	root := filepath.Join(api.homePath, "skills", "demo-a")
	run, err := os.Stat(filepath.Join(root, "scripts", "run.sh"))
	require.NoError(t, err)
	assert.NotZero(t, run.Mode().Perm()&0o100, "scripts/run.sh keeps the owner exec bit; mode %v", run.Mode().Perm())
	for _, rel := range []string{"SKILL.md", filepath.Join("reference", "a.md")} {
		st, statErr := os.Stat(filepath.Join(root, rel))
		require.NoError(t, statErr)
		assert.Zero(t, st.Mode().Perm()&0o111, "%s gets no exec bit; mode %v", rel, st.Mode().Perm())
	}
}

// R2-3 (chief ruling, option B): the upload handler renames a repeated
// same-named file to <base>_<unixnano>.zip (pkg/gateway/rest_uploads.go); the
// install must strip that `_<19 digits>` suffix before the slug check, so a
// repeat upload of good-skill.zip installs as good-skill (run 2: 502).
func uat2GoodZip(t *testing.T, slug string) []byte {
	t.Helper()
	md := "---\nname: " + slug + "\ndescription: Use when a repeat-upload test needs a skill.\n---\n\nBody.\n"
	return uat2BuildZip(t, []uat2ZipEntry{{name: "SKILL.md", mode: 0o644, body: md}})
}

func TestInstallSkillRepeatUploadRenamedWithTimestamp_InstallsAsOriginalSlug(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	data := uat2GoodZip(t, "good-skill")
	first := uat2InstallZip(t, api, "good-skill", data)
	require.Equal(t, http.StatusOK, first.Code, "first upload installs: %s", first.Body.String())
	var skill gen.Skill
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &skill))
	require.NotEmpty(t, skill.Revision)

	// The repeat upload sits in the same upload context under the renamed name.
	second := uat2InstallNamed(t, api, "good-skill_1730000000000000000.zip", data, map[string]any{"revision": skill.Revision})
	require.Equal(t, http.StatusOK, second.Code, "the repeat upload installs, not 502; body: %s", second.Body.String())
	var again gen.Skill
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &again))
	assert.Equal(t, "good-skill", again.Id)
	_, statErr := os.Stat(filepath.Join(api.homePath, "skills", "good-skill_1730000000000000000"))
	assert.ErrorIs(t, statErr, os.ErrNotExist, "no skill folder carries the suffix")
}

func TestInstallSkillRepeatUploadReplaceAfterLocalChangesWarning(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	data := uat2GoodZip(t, "good-skill")
	first := uat2InstallZip(t, api, "good-skill", data)
	require.Equal(t, http.StatusOK, first.Code, "body: %s", first.Body.String())

	// An edit made outside Omnipus makes the next replacement need confirmation.
	skillDir := filepath.Join(api.homePath, "skills", "good-skill")
	edited := "---\nname: good-skill\ndescription: edited locally\n---\nLOCAL EDIT\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(edited), 0o644))
	rev, err := skills.ContentRevision(skillDir)
	require.NoError(t, err)

	warn := uat2InstallNamed(t, api, "good-skill_1730000000000000001.zip", data, map[string]any{"revision": rev})
	require.Equal(t, http.StatusConflict, warn.Code, "local changes need confirmation, not 502; body: %s", warn.Body.String())
	assert.Contains(t, warn.Body.String(), "LOCAL_CHANGES_CONFIRMATION_REQUIRED")
	raw, readErr := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	require.NoError(t, readErr)
	require.Equal(t, edited, string(raw), "the warning changed nothing")

	// Replace from the same upload context (same renamed copy confirmed).
	replace := uat2InstallNamed(t, api, "good-skill_1730000000000000001.zip", data, map[string]any{"revision": rev, "replace_local_changes": true})
	require.Equal(t, http.StatusOK, replace.Code, "Replace succeeds; body: %s", replace.Body.String())
	raw, readErr = os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	require.NoError(t, readErr)
	assert.Contains(t, string(raw), "Use when a repeat-upload test needs a skill.")
}
