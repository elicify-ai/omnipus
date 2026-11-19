//go:build goolm && stdjson

// RED tests for the T-6 CLI: omnipus export / omnipus import (epic #1094 S1a).
//
// Oracles: export-import-s1-spec.md revision 7 — the CLI rows of "UI Screens
// and States" (exact output lines), "CLI exit codes" (0/1/2), Dataset D10
// (usage and lookups), D4/D11 refusal surfacing, the BDD "Command line"
// scenarios, FR-011/017/018/019/030/035/036/036b/037/044/050/051/053/054/055.
// The gateway is an httptest fake; every expected line below is quoted or
// derived from the spec text, never from a running implementation.

package pkgcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── command constructors ─────────────────────────────────────────────────────

// exportCmd builds an `omnipus` root carrying `export`, pointed at the fake
// gateway; args are the real CLI argv (e.g. "export --agent Writer -o …").
func exportCmd(f *fakeGateway, opts Options) *cobra.Command {
	return testRoot(f, opts)
}

// importCmd builds an `omnipus` root carrying `import`.
func importCmd(f *fakeGateway, opts Options) *cobra.Command {
	return testRoot(f, opts)
}

// packageCmd builds an `omnipus` root carrying `package` (offline; the fake
// proves it is never used).
func packageCmd(f *fakeGateway, opts Options) *cobra.Command {
	return testRoot(f, opts)
}

// defaultImportSetup writes a fixture package to dir and returns its path.
func defaultImportSetup(t *testing.T, dir string) string {
	t.Helper()
	return writeFixtureFile(t, dir, "writer.omnpkg", buildPackage(t, pkgFixture{}))
}

// applyBody decodes an apply request body into a map.
func applyBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m), "apply request body must be JSON")
	return m
}

// rosterWriter is the single-agent roster the lookup tests resolve against.
func rosterWriter() []map[string]any {
	return []map[string]any{{"id": fixtureAgentID, "name": "Writer", "type": "Main"}}
}

// ─── D10: usage, lookups, exit codes ──────────────────────────────────────────

// TestPkgCmd_UsageLookupsExitCodes walks Dataset D10 and the FR-053 exit-code
// contract, plus the CLI-side surfacing of the gateway's coded refusals
// (FR-011 [r7], FR-018, FR-019, FR-030, FR-035, FR-040).
func TestPkgCmd_UsageLookupsExitCodes(t *testing.T) {
	dir := t.TempDir()

	t.Run("D10 row 1: export without --agent exits 2, no output file", func(t *testing.T) {
		f := newFakeGateway(t)
		out := filepath.Join(dir, "w1.omnpkg")
		_, stderr, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "", "export", "-o", out)
		require.Equal(t, 2, exitCodeOf(t, err), "missing --agent is a usage error (FR-053)")
		assert.Contains(t, stderr, "--agent")
		require.NoFileExists(t, out)
	})

	t.Run("D10 row 2: export without -o exits 2", func(t *testing.T) {
		f := newFakeGateway(t)
		_, stderr, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "", "export", "--agent", "Writer")
		require.Equal(t, 2, exitCodeOf(t, err), "missing -o is a usage error (FR-053)")
		assert.Contains(t, stderr, "-o")
	})

	t.Run("D10 row 3: unknown agent exits 1, names it, sends no export, leaves no file", func(t *testing.T) {
		f := newFakeGateway(t).withRoster(rosterWriter()...)
		out := filepath.Join(dir, "w3.omnpkg")
		_, stderr, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "",
			"export", "--agent", "nope", "-o", out)
		require.Equal(t, 1, exitCodeOf(t, err), "unknown agent is refused, not a usage error (D10 row 3)")
		assert.Contains(t, stderr, "nope", "the message names the problem")
		require.NoFileExists(t, out, "BDD: Export refusals leave no file")
		assert.Equal(t, 0, f.counts().exports, "refusal must happen before the export request")
	})

	t.Run("D10 row 4: ambiguous name exits 1 listing both IDs, sends no export", func(t *testing.T) {
		f := newFakeGateway(t).withRoster(
			map[string]any{"id": "id-aaa", "name": "Writer", "type": "Main"},
			map[string]any{"id": "id-bbb", "name": "Writer", "type": "Main"},
		)
		out := filepath.Join(dir, "w4.omnpkg")
		_, stderr, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "",
			"export", "--agent", "Writer", "-o", out)
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "id-aaa", "ambiguous refusal lists the IDs (FR-050)")
		assert.Contains(t, stderr, "id-bbb")
		require.NoFileExists(t, out)
		assert.Equal(t, 0, f.counts().exports)
	})

	t.Run("D10 row 5: existing output refused without --force, old bytes kept", func(t *testing.T) {
		f := newFakeGateway(t).withRoster(rosterWriter()...).serveExportPackage(buildPackage(t, pkgFixture{}))
		out := writeFixtureFile(t, dir, "w5.omnpkg", []byte("OLD-BYTES"))
		_, _, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "",
			"export", "--agent", "Writer", "-o", out)
		require.Equal(t, 1, exitCodeOf(t, err), "an existing output is refused without --force (FR-050)")
		kept, readErr := os.ReadFile(out)
		require.NoError(t, readErr)
		assert.Equal(t, "OLD-BYTES", string(kept), "the existing file is unchanged (D10 row 5)")
	})

	t.Run("US-1 AS-6: --force replaces the existing file with the served bytes", func(t *testing.T) {
		pkg := buildPackage(t, pkgFixture{})
		f := newFakeGateway(t).withRoster(rosterWriter()...).serveExportPackage(pkg)
		out := writeFixtureFile(t, dir, "w6.omnpkg", []byte("OLD-BYTES"))
		_, _, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "",
			"export", "--agent", "Writer", "-o", out, "--force")
		require.Equal(t, 0, exitCodeOf(t, err))
		written, readErr := os.ReadFile(out)
		require.NoError(t, readErr)
		assert.Equal(t, pkg, written, "--force writes the new package (old bytes replaced)")
	})

	t.Run("D10 row 6: unwritable output path exits 1, file absent", func(t *testing.T) {
		f := newFakeGateway(t).withRoster(rosterWriter()...).serveExportPackage(buildPackage(t, pkgFixture{}))
		// Root-proof equivalent of the read-only directory: the parent folder
		// does not exist, so creating the file fails for any user.
		out := filepath.Join(dir, "no-such-dir", "w6b.omnpkg")
		_, _, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "",
			"export", "--agent", "Writer", "-o", out)
		require.Equal(t, 1, exitCodeOf(t, err))
		require.NoFileExists(t, out)
	})

	t.Run("D10 row 7: blank-prompt agent surfaces agent_prompt_missing, no file", func(t *testing.T) {
		f := newFakeGateway(t).withRoster(rosterWriter()...).
			serveExportError(422, "agent_prompt_missing", "a native agent needs a non-blank prompt")
		out := filepath.Join(dir, "w7.omnpkg")
		_, stderr, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "",
			"export", "--agent", "Writer", "-o", out)
		require.Equal(t, 1, exitCodeOf(t, err), "FR-019: export refuses what import would refuse")
		assert.Contains(t, stderr, "agent_prompt_missing", "the refusal names the code")
		require.NoFileExists(t, out)
	})

	t.Run("D4 row 1 via CLI: built-in agent surfaces core_agent_not_exportable, no file", func(t *testing.T) {
		f := newFakeGateway(t).
			withRoster(map[string]any{"id": "mia", "name": "Mia", "type": "core"}).
			serveExportError(422, "core_agent_not_exportable", "built-in agents cannot be exported")
		out := filepath.Join(dir, "mia.omnpkg")
		_, stderr, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "",
			"export", "--agent", "mia", "-o", out)
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "cannot be exported",
			"[r7] P-Q1 B: built-in agents cannot be exported — the message must not soften to 'not yet'")
		require.NoFileExists(t, out)
	})

	t.Run("D11 row 2 via CLI: agent_file_unreadable surfaces, no file", func(t *testing.T) {
		f := newFakeGateway(t).withRoster(rosterWriter()...).
			serveExportError(422, "agent_file_unreadable", "AGENT.md is a link")
		out := filepath.Join(dir, "w11.omnpkg")
		_, stderr, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "",
			"export", "--agent", "Writer", "-o", out)
		require.Equal(t, 1, exitCodeOf(t, err), "FR-018 refusal is surfaced, never omitted")
		assert.Contains(t, stderr, "agent_file_unreadable")
		require.NoFileExists(t, out)
	})

	t.Run("D10 row 8: import without a file exits 2", func(t *testing.T) {
		f := newFakeGateway(t)
		_, _, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "", "import")
		require.Equal(t, 2, exitCodeOf(t, err), "missing argument is a usage error (FR-053)")
	})

	t.Run("D10 row 9: import of a missing file exits 2, uploads nothing", func(t *testing.T) {
		f := newFakeGateway(t)
		_, stderr, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "",
			"import", filepath.Join(dir, "missing.omnpkg"), "--yes")
		require.Equal(t, 2, exitCodeOf(t, err), "input file missing is a usage error (FR-053)")
		assert.Contains(t, stderr, "missing.omnpkg")
		assert.Equal(t, 0, f.counts().uploads, "nothing is uploaded for a missing file")
	})

	t.Run("D10 row 10: package inspect on a directory exits 2", func(t *testing.T) {
		f := newFakeGateway(t)
		_, _, err := runCommand(t, packageCmd(f, Options{}), "", "package", "inspect", dir+string(os.PathSeparator))
		require.Equal(t, 2, exitCodeOf(t, err), "an input that is a folder is a usage error (FR-053)")
	})

	t.Run("D10 row 15: unknown package subcommand exits 2", func(t *testing.T) {
		f := newFakeGateway(t)
		_, stderr, err := runCommand(t, packageCmd(f, Options{}), "", "package", "unknown", "x")
		require.Equal(t, 2, exitCodeOf(t, err), "FR-053: unknown subcommand is a usage error")
		assert.Contains(t, stderr, "unknown")
	})

	t.Run("FR-053: unknown flag exits 2", func(t *testing.T) {
		f := newFakeGateway(t)
		_, _, err := runCommand(t, exportCmd(f, Options{Token: "tok"}), "",
			"export", "--bogus", "x", "--agent", "Writer", "-o", filepath.Join(dir, "wflag.omnpkg"))
		require.Equal(t, 2, exitCodeOf(t, err), "an unknown flag is a usage error (FR-053)")
	})

	t.Run("D10 row 12: ambiguous workspace name exits 1 listing both IDs, uploads nothing", func(t *testing.T) {
		f := newFakeGateway(t).withWorkspaces(
			map[string]any{"id": "ws-team-a", "name": "Team", "status": "active"},
			map[string]any{"id": "ws-team-b", "name": "Team", "status": "active"},
		).servePreview(previewBody(t, nil))
		file := defaultImportSetup(t, dir)
		_, stderr, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "",
			"import", file, "--workspace", "Team", "--yes")
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "ws-team-a", "ambiguous refusal lists the IDs (FR-051)")
		assert.Contains(t, stderr, "ws-team-b")
		assert.Equal(t, 0, f.counts().uploads, "FR-051 order: workspace resolution happens before the upload")
	})

	t.Run("D10 row 13: unknown workspace name exits 1 naming it, uploads nothing", func(t *testing.T) {
		f := newFakeGateway(t).withWorkspaces(
			map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"},
		)
		file := defaultImportSetup(t, dir)
		_, stderr, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "",
			"import", file, "--workspace", "Nope", "--yes")
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "Nope", "the message names the problem")
		assert.Equal(t, 0, f.counts().uploads, "resolution precedes the upload (FR-051)")
	})

	t.Run("unknown workspace ID surfaces the gateway 404 workspace_not_found", func(t *testing.T) {
		f := newFakeGateway(t).withWorkspaces(
			map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"},
		).servePreview(previewBody(t, nil)).
			scriptApply([]applyReply{{status: 404, body: `{"code":"workspace_not_found","message":"unknown workspace"}`}}, false)
		file := defaultImportSetup(t, dir)
		_, stderr, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "",
			"import", file, "--workspace", "ws-unknown", "--yes")
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "workspace_not_found", "the coded refusal surfaces to the CLI user")
	})

	t.Run("D10 row 14: --mode replace is refused with import_mode_not_available", func(t *testing.T) {
		f := newFakeGateway(t).withWorkspaces(
			map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"},
		).servePreview(previewBody(t, nil)).
			scriptApply([]applyReply{{status: 422, body: `{"code":"import_mode_not_available","message":"only new mode is available"}`}}, false)
		file := defaultImportSetup(t, dir)
		_, stderr, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "",
			"import", file, "--workspace", "Research", "--yes", "--mode", "replace")
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "import_mode_not_available", "FR-030: only new is available")
		require.NotEmpty(t, f.applies, "the CLI must send the user's mode, not silently rewrite it to new")
		body := applyBody(t, f.last(f.applies).body)
		assert.Equal(t, "replace", body["mode"], "the apply request carries the requested mode")
	})

	t.Run("damaged package surfaces checksum_mismatch at upload, nothing applied", func(t *testing.T) {
		f := newFakeGateway(t).servePreviewError(422, "checksum_mismatch", "omnipus/agents/x/SOUL.md changed")
		file := defaultImportSetup(t, dir)
		_, stderr, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "",
			"import", file, "--yes")
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "checksum_mismatch")
		assert.Equal(t, 0, f.counts().applies, "a refused upload is never applied")
	})

	t.Run("FR-035: no default workspace surfaces 409 default_workspace_missing", func(t *testing.T) {
		f := newFakeGateway(t).servePreview(previewBody(t, nil)).
			scriptApply([]applyReply{{status: 409, body: `{"code":"default_workspace_missing","message":"name a workspace"}`}}, false)
		file := defaultImportSetup(t, dir)
		_, stderr, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "",
			"import", file, "--yes")
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "default_workspace_missing")
	})
}

// ─── prompt and lost connection (BDD "Command line") ──────────────────────────

// TestPkgCmd_PromptAndLostConnection covers the interactive confirm, the
// declined and non-interactive cancels, and the lost-apply-connection message
// (BDD scenarios, FR-051; D10 row 11 rides in the confirm case).
func TestPkgCmd_PromptAndLostConnection(t *testing.T) {
	dir := t.TempDir()

	t.Run("BDD Interactive confirm: preview before prompt, apply sent, result line, exit 0", func(t *testing.T) {
		f := newFakeGateway(t).
			withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
			servePreview(previewBody(t, nil)).
			scriptApply([]applyReply{{status: 200, body: resultBody(t, "Main", "Writer", fixtureResearchWorkspaceID, "active", nil)}}, false)
		file := defaultImportSetup(t, dir)

		stdout, _, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "y\n",
			"import", file, "--workspace", "Research")

		require.Equal(t, 0, exitCodeOf(t, err))
		assert.Contains(t, stdout, "Uploading", "the CLI row promises an in-progress line")
		assert.Contains(t, stdout, "Writer", "the preview names the agent to add")
		previewAt := strings.Index(stdout, "Writer")
		promptAt := strings.Index(stdout, "Import 1 agent as new? [y/N]")
		require.NotEqual(t, -1, promptAt, "the confirmation prompt line is shown (CLI row)")
		require.Less(t, previewAt, promptAt, "BDD: the preview is printed before the prompt")

		// The result line, exactly as the CLI row spells it.
		wantLine := fmt.Sprintf("Imported %q as %s into workspace %q", "Writer", fixtureNewID, "Research")
		assert.Contains(t, stdout, wantLine)

		// Wire contract: the file is uploaded verbatim; the apply targets the
		// preview's staging id and carries mode new + the resolved workspace.
		assert.Equal(t, 1, f.counts().uploads)
		upload := f.last(f.uploads)
		fixtureBytes, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		assert.Equal(t, fixtureBytes, upload.body, "FR-051: the CLI uploads the file bytes unchanged")
		require.Equal(t, 1, f.counts().applies)
		apply := f.last(f.applies)
		assert.Equal(t, "/api/v1/packages/import/"+fixtureStagingID+"/apply", apply.path,
			"the apply addresses the preview's staging id")
		body := applyBody(t, apply.body)
		assert.Equal(t, "new", body["mode"], "S1's CLI sends mode new (ambiguity warning 4)")
		assert.Equal(t, fixtureResearchWorkspaceID, body["workspace_id"],
			"D10 row 11: the resolved workspace ID travels to apply (FR-035)")
	})

	t.Run("BDD Declined: typing n cancels, exit 1, nothing applied", func(t *testing.T) {
		f := newFakeGateway(t).
			withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
			servePreview(previewBody(t, nil))
		file := defaultImportSetup(t, dir)

		stdout, _, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "n\n",
			"import", file, "--workspace", "Research")

		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stdout, "Import cancelled")
		assert.Equal(t, 0, f.counts().applies, "a declined import sends no apply request")
	})

	t.Run("BDD Declined: bare Enter cancels, exit 1, nothing applied", func(t *testing.T) {
		f := newFakeGateway(t).
			withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
			servePreview(previewBody(t, nil))
		file := defaultImportSetup(t, dir)

		stdout, _, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "\n",
			"import", file, "--workspace", "Research")

		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stdout, "Import cancelled", "Enter alone cancels (accessibility row)")
		assert.Equal(t, 0, f.counts().applies)
	})

	t.Run("BDD Declined: non-interactive stdin without --yes cancels, exit 1", func(t *testing.T) {
		f := newFakeGateway(t).
			withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
			servePreview(previewBody(t, nil))
		file := defaultImportSetup(t, dir)

		_, _, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "",
			"import", file, "--workspace", "Research")

		require.Equal(t, 1, exitCodeOf(t, err), "no terminal and no --yes must not import silently")
		assert.Equal(t, 0, f.counts().applies)
	})

	t.Run("FR-051: --yes skips the prompt and applies", func(t *testing.T) {
		f := newFakeGateway(t).
			withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
			servePreview(previewBody(t, nil)).
			scriptApply([]applyReply{{status: 200, body: resultBody(t, "Main", "Writer", fixtureResearchWorkspaceID, "active", nil)}}, false)
		file := defaultImportSetup(t, dir)

		_, _, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "",
			"import", file, "--workspace", "Research", "--yes")

		require.Equal(t, 0, exitCodeOf(t, err))
		assert.Equal(t, 1, f.counts().applies, "--yes applies without a prompt")
	})

	t.Run("BDD Lost connection during apply: may-or-may-not-have-completed message, exit 1", func(t *testing.T) {
		f := newFakeGateway(t).
			withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
			servePreview(previewBody(t, nil)).
			scriptApply(nil, true)
		file := defaultImportSetup(t, dir)

		_, stderr, err := runCommand(t, importCmd(f, Options{Token: "tok"}), "y\n",
			"import", file, "--workspace", "Research")

		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "may or may not have completed", "the CLI row's lost-connection text")
		assert.Contains(t, stderr, fixtureStagingID, "the message names the staging id")
		assert.Contains(t, stderr, "Check the agent list", "…and tells the owner what to check")
		assert.Contains(t, stderr, "Writer", "…for the agent by name")
	})
}

// ─── export success line, notice, atomic write, cli.token ─────────────────────

// TestPkgCmd_ExportSuccessNoticeAtomic covers the export success row of
// "UI Screens and States": the success line, the free-text notice (FR-050,
// FR-013's CLI notice), the atomic write of the served bytes (FR-017/FR-050),
// cli.token authentication (FR-054), and that the CLI leaves the data folder
// alone (FR-017 export leaves no file in the data folder; FR-055).
func TestPkgCmd_ExportSuccessNoticeAtomic(t *testing.T) {
	pkg := buildPackage(t, pkgFixture{})
	f := newFakeGateway(t).withRoster(rosterWriter()...).serveExportPackage(pkg)

	home := emptyHome(t)
	// No Options.Token: FR-054 says export authenticates with cli.token, so the
	// command must load the bearer from Home itself.
	require.NoError(t, os.WriteFile(filepath.Join(home, "cli.token"), []byte("tok-cli-export"), 0o600))
	out := filepath.Join(t.TempDir(), "writer.omnpkg")

	stdout, _, err := runCommand(t, exportCmd(f, Options{Home: home}), "",
		"export", "--agent", "Writer", "-o", out)

	require.Equal(t, 0, exitCodeOf(t, err))

	written, readErr := os.ReadFile(out)
	require.NoError(t, readErr, "the output file exists after success")
	assert.Equal(t, pkg, written, "FR-017/FR-050: the CLI writes the served package bytes")

	// Success line (CLI row): Exported "Writer" (1 agent, 3 files, <size>) to <out>.
	// The size's KB rendering is not specified, so the line is asserted
	// structurally around it; counts and names are exact.
	assert.Contains(t, stdout, `Exported "Writer" (1 agent, 3 files,`, "success line: name and counts")
	assert.Contains(t, stdout, fmt.Sprintf("to %s", out), "success line ends with the -o path as typed")

	// The free-text notice on every export (Security and User Promises; L6).
	assert.Contains(t, stdout, "copied as they are", "the notice says prompt files are copied as they are")
	assert.Contains(t, stdout, "pasted secrets", "…and warns about pasted secrets")

	// Auth: the export went out with the cli.token bearer (FR-054).
	require.Equal(t, 1, f.counts().exports)
	exportReq := f.last(f.exports)
	assert.Equal(t, "Bearer tok-cli-export", exportReq.header.Get("Authorization"),
		"FR-054: export authenticates with the cli.token bearer")

	// FR-010: exactly one agent id travels.
	var reqBody struct {
		AgentIDs []string `json:"agent_ids"`
	}
	require.NoError(t, json.Unmarshal(exportReq.body, &reqBody))
	assert.Equal(t, []string{fixtureAgentID}, reqBody.AgentIDs,
		"FR-010: S1 exports exactly one agent (the resolved ID)")

	// FR-017/FR-055: nothing was written into the data folder — the only entry
	// is the cli.token the test itself created.
	entries, readDirErr := os.ReadDir(home)
	require.NoError(t, readDirErr)
	require.Len(t, entries, 1, "export must leave no file in the data folder (FR-017)")
	assert.Equal(t, "cli.token", entries[0].Name())
}

// ─── import outcome lines (CLI rows) ──────────────────────────────────────────

// TestPkgCmd_ImportOutcomeLines covers the result-side CLI rows: the worker
// delegation line (FR-036b), the activation-pending restart guidance
// (FR-036, D7 rows 7/17 CLI line), the rollback and incomplete-rollback
// messages (FR-037 CLI lines, revision 7 wording), and the
// unfinished_import_found preview warning (FR-044).
func TestPkgCmd_ImportOutcomeLines(t *testing.T) {
	dir := t.TempDir()
	baseFake := func() *fakeGateway {
		return newFakeGateway(t).
			withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
			servePreview(previewBody(t, nil))
	}
	runImport := func(t *testing.T, f *fakeGateway, file string) (string, string, error) {
		t.Helper()
		return runCommand(t, importCmd(f, Options{Token: "tok"}), "y\n",
			"import", file, "--workspace", "Research")
	}

	t.Run("FR-036b: a worker result adds the reached-by-delegation line", func(t *testing.T) {
		f := baseFake().scriptApply([]applyReply{{status: 200,
			body: resultBody(t, "Subagent", "Writer", fixtureResearchWorkspaceID, "active", nil)}}, false)
		file := defaultImportSetup(t, dir)
		stdout, _, err := runImport(t, f, file)
		require.Equal(t, 0, exitCodeOf(t, err))
		assert.Contains(t, stdout, "reached by delegation",
			"CLI row: for a worker, plus the reached-by-delegation line (US-3 AS-11)")
	})

	t.Run("FR-036: activation pending prints the restart guidance and exits 0", func(t *testing.T) {
		f := baseFake().scriptApply([]applyReply{{status: 200,
			body: resultBody(t, "Main", "Writer", fixtureResearchWorkspaceID, "pending", nil)}}, false)
		file := defaultImportSetup(t, dir)
		stdout, _, err := runImport(t, f, file)
		require.Equal(t, 0, exitCodeOf(t, err), "pending activation is a success with guidance (CLI row)")
		assert.Contains(t, stdout, "did not confirm that it is live yet", "D7 row 7 CLI line")
		assert.Contains(t, stdout, "Restart the gateway, or wait and check the agent list")
		assert.Contains(t, stdout, fixtureNewID, "the line names the stored agent's new ID")
	})

	t.Run("FR-037: import_rolled_back prints that nothing was imported", func(t *testing.T) {
		f := baseFake().scriptApply([]applyReply{{status: 500,
			body: `{"code":"import_rolled_back","message":"the pending record write failed"}`}}, false)
		file := defaultImportSetup(t, dir)
		_, stderr, err := runImport(t, f, file)
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "nothing was imported", "CLI row: rollback message")
		assert.Contains(t, stderr, "the pending record write failed", "the reason is included")
	})

	t.Run("[r7] FR-037: incomplete rollback prints the never-visible guidance with IDs", func(t *testing.T) {
		f := baseFake().scriptApply([]applyReply{{status: 500, body: `{"code":"import_rollback_incomplete",
			"message":"rollback failed",
			"details":{"failed_step":"C2","agent_id":"55555555-4444-3333-2222-111111111111","staging_id":"staging-7","leftovers":["agents/55555555-4444-3333-2222-111111111111/"]}}`}}, false)
		file := defaultImportSetup(t, dir)
		_, stderr, err := runImport(t, f, file)
		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "could not be fully undone", "CLI row [r7]: incomplete-rollback message")
		assert.Contains(t, stderr, "never visible", "[r7]: the agent was never visible")
		assert.Contains(t, stderr, "55555555-4444-3333-2222-111111111111", "…names the agent ID")
		assert.Contains(t, stderr, "staging-7", "…and the staging ID")
		assert.Contains(t, stderr, "Restart the gateway", "…with the recovery guidance")
		assert.Contains(t, stderr, "Recover from a failed import", "…pointing at the CLI guide section (DOC-001)")
	})

	t.Run("FR-044: unfinished_import_found is shown in the preview with IDs", func(t *testing.T) {
		warnings := []map[string]any{{
			"code":    "unfinished_import_found",
			"message": "an unfinished import for agent orphan-id-5 remains; staging folder staging-orphan",
		}}
		f := newFakeGateway(t).
			withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
			servePreview(previewBody(t, warnings)).
			scriptApply([]applyReply{{status: 200,
				body: resultBody(t, "Main", "Writer", fixtureResearchWorkspaceID, "active", nil)}}, false)
		file := defaultImportSetup(t, dir)
		stdout, _, err := runImport(t, f, file)
		require.Equal(t, 0, exitCodeOf(t, err))
		assert.Contains(t, stdout, "orphan-id-5", "FR-044: the warning names the agent ID")
		assert.Contains(t, stdout, "staging-orphan", "FR-044: …and the staging folder")
	})
}
