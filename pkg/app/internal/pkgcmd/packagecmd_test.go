//go:build goolm && stdjson

// RED tests for the T-6 offline package commands (`omnipus package inspect`
// and `omnipus package verify`) and the FR-054 auto-start seam.
//
// Oracles: export-import-s1-spec.md revision 7 — BDD "Inspect and verify
// offline" (all five scenarios), the inspect/verify rows of "UI Screens and
// States", FR-008 (decoder window), FR-052 (offline, reads only the manifest,
// writes nothing), FR-053 (exit codes), FR-054 (auto-start), SC-003/SC-006.
// Offline is proven positively: the fake gateway records every request and the
// tests require zero of them.

package pkgcmd

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireOffline fails unless the fake gateway saw no request at all and the
// data folder holds only the cli.token the caller may have created.
func requireOffline(t *testing.T, f *fakeGateway, home string) {
	t.Helper()
	counts := f.counts()
	total := counts.agents + counts.workspaces + counts.exports + counts.uploads + counts.applies
	require.Equal(t, 0, total, "FR-052: inspect/verify need no gateway — the fake saw %d requests", total)
	requireDirEmpty(t, home)
}

// TestPkgCmd_PackageInspectOffline covers the BDD inspect scenarios and the
// inspect CLI row: manifest details without an integrity claim, nothing
// written, refusal of newer packages and oversized decoder windows, and the
// usage errors of D10 rows 10 and FR-053.
func TestPkgCmd_PackageInspectOffline(t *testing.T) {
	dir := t.TempDir()

	t.Run("BDD Inspect reads only the manifest: details, no claim, no writes", func(t *testing.T) {
		// The fixture declares one needs_secrets entry so the "secrets to
		// re-enter" section of the inspect row has something to show (a
		// hand-made manifest may carry it; inspect reads the manifest only).
		secrets := []map[string]any{{
			"name": "OPENAI_API_KEY", "used_by": fixtureAgentID, "kind": "executor_env",
		}}
		pkg := buildPackage(t, pkgFixture{NeedsSecrets: secrets})
		path := writeFixtureFile(t, dir, "good.omnpkg", pkg)

		f := newFakeGateway(t)
		home := emptyHome(t)
		stdout, _, err := runCommand(t, packageCmd(f, Options{Home: home}), "",
			"package", "inspect", path)

		require.Equal(t, 0, exitCodeOf(t, err))
		assert.Contains(t, stdout, "omnpkg", "the format is shown (manifest format field)")
		assert.Contains(t, stdout, "v0.1.1-test", "the source build is shown")
		assert.Contains(t, stdout, "2026-11-19", "the creation time is shown")
		assert.Contains(t, stdout, "Writer", "the element name is shown")
		assert.Contains(t, stdout, filepath.Base(path), "…and the file under inspection")
		agentFile := "omnipus/agents/" + fixtureAgentID + "/SOUL.md"
		assert.Contains(t, stdout, agentFile, "files are listed by path")
		assert.Contains(t, stdout, soulSHA(t), "each file's SHA-256 checksum is shown")
		assert.Contains(t, stdout, "OPENAI_API_KEY", "secrets to re-enter are listed")
		assert.Contains(t, stdout, "Checksums not verified", "inspect makes no integrity claim (CLI row)")
		assert.Contains(t, stdout, "omnipus package verify", "…and points at verify (CLI row)")
		requireOffline(t, f, home)
	})

	t.Run("BDD Inspect reads only the manifest: corrupted payload still inspects", func(t *testing.T) {
		pkg := buildPackage(t, pkgFixture{TamperAfterHash: func(files map[string][]byte) {
			for path, data := range files {
				if filepath.Ext(path) == ".md" {
					data[0] ^= 0xFF
					break
				}
			}
		}})
		path := writeFixtureFile(t, dir, "corrupt-payload.omnpkg", pkg)

		f := newFakeGateway(t)
		home := emptyHome(t)
		stdout, _, err := runCommand(t, packageCmd(f, Options{Home: home}), "",
			"package", "inspect", path)

		require.Equal(t, 0, exitCodeOf(t, err),
			"inspect reads only the manifest — payload damage is not its business")
		assert.Contains(t, stdout, "Checksums not verified")
		requireOffline(t, f, home)
	})

	t.Run("BDD Newer package refused offline: format_version 2 exits 1", func(t *testing.T) {
		pkg := buildPackage(t, pkgFixture{FormatVersion: 2})
		path := writeFixtureFile(t, dir, "newer.omnpkg", pkg)

		f := newFakeGateway(t)
		home := emptyHome(t)
		_, stderr, err := runCommand(t, packageCmd(f, Options{Home: home}), "",
			"package", "inspect", path)

		require.Equal(t, 1, exitCodeOf(t, err), "SC-003: a newer package is refused by inspect")
		assert.Contains(t, stderr, "newer Omnipus", "the BDD wording: made by a newer Omnipus …")
		assert.Contains(t, stderr, "update", "…update first")
		requireOffline(t, f, home)
	})

	t.Run("BDD Oversized decoder window refused offline: 512 MiB window exits 1", func(t *testing.T) {
		pkg := buildPackage(t, pkgFixture{LargeZstdWindow: true})
		path := writeFixtureFile(t, dir, "big-window.omnpkg", pkg)

		f := newFakeGateway(t)
		home := emptyHome(t)
		_, stderr, err := runCommand(t, packageCmd(f, Options{Home: home}), "",
			"package", "inspect", path)

		require.Equal(t, 1, exitCodeOf(t, err),
			"FR-008: the decoder window is capped at 8 MiB, the frame asks for 512 MiB")
		assert.Contains(t, stderr, "too large", "refused as too large (window wording is engine-side)")
		requireOffline(t, f, home)
		// The allocation-boundedness of the refusal (no 512 MiB allocation) is
		// T-2's TestArchive_DecoderLimits ground; here only the CLI refusal.
	})

	t.Run("FR-008 declared window at the 8 MiB cap is accepted offline", func(t *testing.T) {
		// At the cap: the decoder refuses WindowSize > max, so 8 MiB must still inspect.
		pkg := buildPackage(t, pkgFixture{DeclaredWindow: 8 << 20})
		path := writeFixtureFile(t, dir, "window-8mib.omnpkg", pkg)

		f := newFakeGateway(t)
		home := emptyHome(t)
		stdout, stderr, err := runCommand(t, packageCmd(f, Options{Home: home}), "",
			"package", "inspect", path)

		require.Equal(t, 0, exitCodeOf(t, err),
			"FR-008: an 8 MiB declared window is the maximum allowed, not over it; stderr: %s", stderr)
		assert.Contains(t, stdout, "Writer", "the manifest was actually read")
		requireOffline(t, f, home)
	})

	t.Run("FR-008 next representable window above the cap (9 MiB) is refused offline", func(t *testing.T) {
		// Next zstd window after 8 MiB is 8 MiB + 1 MiB (exponent 13, mantissa 1).
		// Removing WithDecoderMaxWindow(8 MiB) leaves the clamp at max memory
		// (256 MiB), so a 9 MiB frame would be accepted and this case goes green.
		pkg := buildPackage(t, pkgFixture{DeclaredWindow: 9 << 20})
		path := writeFixtureFile(t, dir, "window-9mib.omnpkg", pkg)

		f := newFakeGateway(t)
		home := emptyHome(t)
		_, stderr, err := runCommand(t, packageCmd(f, Options{Home: home}), "",
			"package", "inspect", path)

		require.Equal(t, 1, exitCodeOf(t, err),
			"FR-008: 9 MiB is the first representable window above the 8 MiB cap")
		assert.Contains(t, stderr, "too large", "refused as too large, same wording as the 512 MiB case")
		requireOffline(t, f, home)
	})

	t.Run("D10 row 10: inspect on a folder exits 2", func(t *testing.T) {
		f := newFakeGateway(t)
		_, _, err := runCommand(t, packageCmd(f, Options{}), "",
			"package", "inspect", dir+string(os.PathSeparator))
		require.Equal(t, 2, exitCodeOf(t, err))
	})

	t.Run("FR-053: inspect on a missing file exits 2", func(t *testing.T) {
		f := newFakeGateway(t)
		_, stderr, err := runCommand(t, packageCmd(f, Options{}), "",
			"package", "inspect", filepath.Join(dir, "nope.omnpkg"))
		require.Equal(t, 2, exitCodeOf(t, err))
		assert.Contains(t, stderr, "nope.omnpkg", "the usage error names the file")
	})
}

// TestPkgCmd_PackageVerifyOffline covers the BDD verify scenarios: OK line,
// tampered file named, newer package refused — all offline and write-free
// (FR-052, SC-002, SC-003).
func TestPkgCmd_PackageVerifyOffline(t *testing.T) {
	dir := t.TempDir()

	t.Run("BDD Verify an unchanged package: OK: 3 files verified, exit 0", func(t *testing.T) {
		path := writeFixtureFile(t, dir, "intact.omnpkg", buildPackage(t, pkgFixture{}))

		f := newFakeGateway(t)
		home := emptyHome(t)
		stdout, _, err := runCommand(t, packageCmd(f, Options{Home: home}), "",
			"package", "verify", path)

		require.Equal(t, 0, exitCodeOf(t, err))
		assert.Contains(t, stdout, "OK: 3 files verified", "the CLI row's exact success line")
		requireOffline(t, f, home)
	})

	t.Run("BDD Verify a changed package: FAILED naming the file, exit 1", func(t *testing.T) {
		pkg := buildPackage(t, pkgFixture{TamperAfterHash: func(files map[string][]byte) {
			for path, data := range files {
				if filepath.Base(path) == "SOUL.md" {
					data[len(data)-1] ^= 0x01 // one byte of SOUL.md changed
					break
				}
			}
		}})
		path := writeFixtureFile(t, dir, "tampered.omnpkg", pkg)

		f := newFakeGateway(t)
		home := emptyHome(t)
		stdout, stderr, err := runCommand(t, packageCmd(f, Options{Home: home}), "",
			"package", "verify", path)

		require.Equal(t, 1, exitCodeOf(t, err), "SC-002: a one-byte change fails verify")
		combined := stdout + stderr
		assert.Contains(t, combined, "FAILED:", "the CLI row's failure shape")
		assert.Contains(t, combined, "omnipus/agents/"+fixtureAgentID+"/SOUL.md",
			"the failure names the changed file (SC-002)")
		requireOffline(t, f, home)
	})

	t.Run("SC-003: verify refuses format_version 2", func(t *testing.T) {
		path := writeFixtureFile(t, dir, "verify-newer.omnpkg", buildPackage(t, pkgFixture{FormatVersion: 2}))

		f := newFakeGateway(t)
		home := emptyHome(t)
		_, stderr, err := runCommand(t, packageCmd(f, Options{Home: home}), "",
			"package", "verify", path)

		require.Equal(t, 1, exitCodeOf(t, err))
		assert.Contains(t, stderr, "newer Omnipus")
		requireOffline(t, f, home)
	})

	t.Run("FR-053: verify on a missing file exits 2", func(t *testing.T) {
		f := newFakeGateway(t)
		_, _, err := runCommand(t, packageCmd(f, Options{}), "",
			"package", "verify", filepath.Join(dir, "gone.omnpkg"))
		require.Equal(t, 2, exitCodeOf(t, err))
	})
}

// TestPkgCmd_AutoStartSeam covers FR-054 at unit level: with the gateway
// unreachable, the command calls the shared auto-start step exactly once and,
// when the gateway still does not answer, exits 1 (gateway unreachable). The
// full "gateway not running" flow is E2E test #34 (built binary), not this
// unit round.
func TestPkgCmd_AutoStartSeam(t *testing.T) {
	dir := t.TempDir()
	file := defaultImportSetup(t, dir)

	f := newFakeGateway(t) // never reached: its URL is replaced below
	opts := Options{Token: "tok", GatewayURL: unreachableGatewayURL(t)}

	autoStartCalls := 0
	opts.AutoStart = func() error {
		autoStartCalls++
		return nil
	}

	_, _, err := runCommand(t, testRoot(f, opts), "", "import", file, "--yes")

	require.Equal(t, 1, exitCodeOf(t, err),
		"FR-053: a gateway that stays unreachable is refused with exit 1")
	require.Equal(t, 1, autoStartCalls,
		"FR-054: the shared auto-start step runs exactly once, then one retry")
	assert.Equal(t, 0, f.counts().uploads, "no upload reached the fake (the URL pointed nowhere)")
}

// TestPkgCmd_AutoStartRetryCompletesImport is the FR-054 case the seam test
// does not cover: the import can succeed only because send retries once
// after AutoStart binds the address that just refused the connection.
// Dropping that retry (and keeping the one AutoStart call) still satisfies
// TestPkgCmd_AutoStartSeam and fails here.
func TestPkgCmd_AutoStartRetryCompletesImport(t *testing.T) {
	dir := t.TempDir()
	file := defaultImportSetup(t, dir)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	f := newFakeGateway(t).
		withWorkspaces(map[string]any{"id": fixtureResearchWorkspaceID, "name": "Research", "status": "active"}).
		servePreview(previewBody(t, nil)).
		scriptApply([]applyReply{{status: 200, body: resultBody(t, "Writer", fixtureResearchWorkspaceID, "active", nil)}}, false)

	autoStartCalls := 0
	opts := Options{
		Token:      "tok",
		GatewayURL: "http://" + addr,
		AutoStart: func() error {
			autoStartCalls++
			srvLn, listenErr := net.Listen("tcp", addr)
			if listenErr != nil {
				return listenErr
			}
			srv := &http.Server{Handler: f.handler}
			go func() { _ = srv.Serve(srvLn) }()
			t.Cleanup(func() { _ = srv.Close() })
			return nil
		},
	}

	stdout, stderr, runErr := runCommand(t, testRoot(f, opts), "",
		"import", file, "--workspace", "Research", "--yes")

	require.Equal(t, 0, exitCodeOf(t, runErr),
		"FR-054 / BDD gateway-not-running: the import completes through the retry; stderr: %s", stderr)
	require.Equal(t, 1, autoStartCalls, "the shared auto-start step runs once, then one retry")
	wantLine := fmt.Sprintf("Imported %q as %s into workspace %q", "Writer", fixtureNewID, "Research")
	assert.Contains(t, stdout, wantLine, "success is the retried conversation, not a local fake URL")
	require.Equal(t, 1, f.counts().uploads, "the upload reached the gateway AutoStart started")
	require.Equal(t, 1, f.counts().applies)
	assert.Equal(t, fixtureResearchWorkspaceID, applyBody(t, f.last(f.applies).body)["workspace_id"])
}
