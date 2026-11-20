// packages_b1_effort_red_test.go — CHECK F04 / sign-off C-2.
//
// Import must run effortBodyRefusal on the package agent.json bytes at upload
// and again at apply (ARCH-SIGNOFF-T3-T4 C-2). The value rule is the REST
// raw-body rule, not a catalogue enum: a string of at most 32 Unicode
// characters, and JSON null is refused with the REST sentence. The package
// answer is 422 element_not_creatable (not REST's HTTP 400) and the field is
// the path effortBodyRefusal reports. A refused upload releases its slot
// (FR-020) and writes exactly one safety-refusal audit entry (FR-040).
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// Sentences are the REST raw-body refusals (effort-levels spec S-56 / S-94),
// copied here so the oracle does not move with a renamed constant.
const (
	effortLengthSentence = "reasoning_effort must be at most 32 characters"
	effortNullSentence   = `reasoning_effort must be a string; send "" to clear it`
	effortPrimaryField   = "reasoning_effort"
	effortFallbackField  = "fallback_models[0].reasoning_effort"
)

// effortRunes is n Unicode characters (U+00E9). Thirty-two of them are 64
// bytes, so a byte-length check of 32 would refuse the accepted boundary.
func effortRunes(n int) string {
	s := strings.Repeat("é", n)
	if utf8.RuneCountInString(s) != n {
		panic("effortRunes: rune count drifted")
	}
	return s
}

func recordWithPrimaryEffort(v any) map[string]any {
	rec := mainRecord(specSourceID, "Writer", "Drafts")
	rec["reasoning_effort"] = v
	return rec
}

func recordWithFallbackEffort(v any) map[string]any {
	rec := mainRecord(specSourceID, "Writer", "Drafts")
	rec["fallback_models"] = []any{
		map[string]any{"model": "m-fb", "provider": "openrouter", "reasoning_effort": v},
	}
	return rec
}

func effortPackage(t *testing.T, rec map[string]any) []byte {
	t.Helper()
	return buildAgentPackage(t, specSourceID, "Writer", rec, []byte("Answer in rhyme.\n"), nil, 0)
}

func packageStagingIDs(t *testing.T, home string) []string {
	t.Helper()
	dir := filepath.Join(home, "backups", ".staging")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// uploadEffortRefusal is the C-2 upload oracle: 422, the REST sentence, the
// stored-record field path, no slot, no agent or config write, one refusal audit.
func uploadEffortRefusal(t *testing.T, rec map[string]any, field, sentence string) {
	t.Helper()
	api := packageAPI(t)
	before := snapshotHome(t, api.homePath)
	cfgBefore := append([]byte(nil), configBytes(t, api.homePath)...)
	slotsBefore := packageStagingIDs(t, api.homePath)
	agentsBefore := agentEntityNames(t, api.homePath)
	auditBefore := len(auditJSONLLines(t, api.homePath))

	got := callPackageImport(t, api, effortPackage(t, rec))
	er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeElementNotCreatable)
	require.Equal(t, field, packageErrField(er), "C-2 field is the effortBodyRefusal path; body=%s", got.Body)
	require.Contains(t, er.Error, sentence, "the reason is the REST raw-body sentence; body=%s", got.Body)
	require.NotContains(t, er.Error, "rolled back", "an upload refusal is not a rollback")

	require.Equal(t, slotsBefore, packageStagingIDs(t, api.homePath), "FR-020: a refused upload leaves no staging slot")
	require.Equal(t, agentsBefore, agentEntityNames(t, api.homePath), "no agent file")
	require.Equal(t, cfgBefore, configBytes(t, api.homePath), "config.json bytes unchanged")
	requireHomeUnchangedExcept(t, before, snapshotHome(t, api.homePath))
	requireUploadRefusalAudit(t, api.homePath, auditBefore)
}

func requireUploadRefusalAudit(t *testing.T, home string, before int) {
	t.Helper()
	lines := auditJSONLLines(t, home)
	require.Equal(t, before+1, len(lines), "FR-040: a safety refusal writes exactly one audit entry")
	var row struct {
		Details map[string]any `json:"details"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &row), "line=%s", lines[len(lines)-1])
	require.Equal(t, "upload", row.Details["operation"], "line=%s", lines[len(lines)-1])
	require.Equal(t, "refused", row.Details["outcome"], "line=%s", lines[len(lines)-1])
	require.Equal(t, omnpkg.CodeElementNotCreatable, row.Details["code"], "line=%s", lines[len(lines)-1])
}

func uploadEffortAccepted(t *testing.T, rec map[string]any) {
	t.Helper()
	api := packageAPI(t)
	got := callPackageImport(t, api, effortPackage(t, rec))
	require.Equal(t, http.StatusCreated, got.Code, "32 Unicode characters are inside the REST limit; body=%s", got.Body)
	prev := decodePreview(t, got.Body)
	require.NotEmpty(t, prev.StagingId)
	require.Equal(t, "Writer", prev.WillAdd[0].ProposedName)
}

func replaceStagedPackage(t *testing.T, api *restAPI, stagingID string, pkg []byte) {
	t.Helper()
	dir, err := api.packageRegistry().Dir(stagingID)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, packageFileInStaging), pkg, 0o600))
}

// applyEffortRefusal plants a valid staging, then swaps in the bad package.
// Apply re-reads package.omnpkg, so the check is the apply check, not upload's.
func applyEffortRefusal(t *testing.T, rec map[string]any, field, sentence string) {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	prev := stageUpload(t, api, validMainPackage(t))
	replaceStagedPackage(t, api, prev.StagingId, effortPackage(t, rec))

	agentsBefore := agentEntityNames(t, api.homePath)
	cfgBefore := append([]byte(nil), configBytes(t, api.homePath)...)
	auditBefore := len(auditJSONLLines(t, api.homePath))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeElementNotCreatable)
	require.Equal(t, field, packageErrField(er), "C-2 field at apply; body=%s", got.Body)
	require.Contains(t, er.Error, sentence, "body=%s", got.Body)
	require.NotContains(t, er.Error, "rolled back")
	require.Equal(t, agentsBefore, agentEntityNames(t, api.homePath), "no partial agent")
	require.Equal(t, cfgBefore, configBytes(t, api.homePath), "config.json unchanged")
	require.Equal(t, auditBefore, len(auditJSONLLines(t, api.homePath)), "a pre-commit apply refusal writes no audit entry")
	state, err := api.packageRegistry().State(prev.StagingId)
	require.NoError(t, err)
	require.Equal(t, omnpkg.StagingReady, state, "staging stays ready")
	_, jerr := os.Stat(filepath.Join(api.packageStagingBase(), prev.StagingId, omnpkg.JournalName))
	require.ErrorIs(t, jerr, os.ErrNotExist, "no journal after a refusal before any write")
}

func applyEffortAccepted(t *testing.T, rec map[string]any, assertStored func(t *testing.T, stored map[string]any)) {
	t.Helper()
	api := packageAPI(t)
	usePackageSeams(t)
	packageT5.Reload = func() (bool, error) { return true, nil }
	ws := createWorkspaceViaAPI(t, api, "Research", "notes")
	prev := stageUpload(t, api, effortPackage(t, rec))
	got := callPackageApply(t, api, prev.StagingId, applyBody("new", ws))
	require.Equal(t, http.StatusOK, got.Code, "32 Unicode characters are kept; body=%s", got.Body)
	res := decodeResult(t, got.Body)
	require.Len(t, res.Created, 1)
	raw, err := os.ReadFile(filepath.Join(api.homePath, "entities", "agents", res.Created[0].NewId+".json"))
	require.NoError(t, err)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored), "record=%s", raw)
	assertStored(t, stored)
}
