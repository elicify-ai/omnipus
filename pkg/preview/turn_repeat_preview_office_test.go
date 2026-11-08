//go:build darwin || linux

package preview

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// T-102 / §3.4 D8, D8c, D8d; FR-054, FR-055, FR-056; B-67, B-69, B-70; MV-3
// (revised 2026-11-08 for D8c + the D8d additions). Two real Office previews
// in a row through Service.Preview with exactly the arguments the tools pass
// (the turn's policy is no longer an input: Request.ReadModel is removed,
// compiler-enforced for all three callers), on a home under a folder reached
// through a symlink, with tmp already present, operator-registered real
// LibreOffice and the real renderer; each admission is released before the
// next is requested (maxOfficeRuns = 1). Both previews produce pages,
// confined.
//
// On the policy each real start used — captured by the accepted seam
// testHookConverterStart (§3.4 D8d ruling), never a policy built by hand:
//
//   - the file rules are exactly the §3.4 D8c list for the platform; on
//     macOS the write rules are exactly {that call's <id>/work, its socket
//     folder}; ReadsOpen and ExecOpen are false; the policy carries no deny
//     list (D8c); no rule covers any part of the data folder other than
//     that work and, on macOS, the registry folder;
//   - D8d: the look-up lines of the rendered profile (`-p` argument of the
//     captured start), in the policy section (after the filesystem-allows
//     marker, never the platform preamble), are exactly one
//     `(allow file-read-metadata (literal "<folder>"))` per folder above
//     every listed folder, computed independently by this test from the
//     captured policy's real paths, `/` excluded — and no file-read*,
//     file-write* or process-exec line covers any of those folders;
//   - the probe program (below) looks up each of those folders, cannot list
//     any of them, cannot look up or read a file planted directly in the
//     data folder, and cannot look up another call folder (macOS).
//
// The probe is a harmless program started through the SAME confinement hooks
// with the captured policy (converterStart.hooks.prepare), spawned from
// inside the seam hook — synchronously, on this test's own call path while
// the start has not even spawned yet, so the call cannot finish under it:
// the barrier is an event, never a timer. It tries to read a file in another
// call folder created after this call started (created inside the hook),
// this call's owner.json, a file in the agent's workspace and a file in
// <home>/system outside the registry — every read fails; it writes in work,
// which succeeds; it reads the registry, which succeeds, and writes there,
// which fails. On Linux the probe and the look-up checks are not run:
// Landlock cannot restrict look-up, and the Linux listing/reading variant is
// Unknown until run (T-102 row).
//
// Not a pass if: the policy is built by hand; the "later" folder is created
// before the start (it is created inside the hook, after the start began);
// an unconfined run is counted as a pass; a missing component skips instead
// of failing; an admission is never released, so the second preview never
// runs; the component is registered so its registry lies inside its root;
// the expected look-up list is copied from the code under test (it is
// recomputed here from the captured policy's paths); a probe expected to
// fail fails for a reason other than the sandbox (every planted target is
// verified to exist and be readable by the test first); the hook captured
// zero converter starts (that fails the test, never skips).
func TestPreviewRepeatFromAgentTurnKeepsItsOwnScratch(t *testing.T) {
	primeKernelSandbox(t)

	// The home lies under a symlink so folders computed from unresolved paths
	// fail visibly (§3.4 D8d risk row): the policy's paths are resolved, and
	// this test computes its expected look-up folders from those.
	symRoot := t.TempDir()
	twisted := filepath.Join(t.TempDir(), "through-link")
	require.NoError(t, os.Symlink(symRoot, twisted), "instrument: the symlinked home root is created")
	home := filepath.Join(twisted, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	// tmp exists before the previews: the D8c guarantee is
	// timing-independent, and a real entry must be there for the
	// no-other-data-folder-rule check to mean something.
	require.NoError(t, os.MkdirAll(filepath.Join(home, "tmp"), 0o700))

	officeRoot := os.Getenv("OMNIPUS_TEST_OFFICE_ROOT")
	if officeRoot == "" {
		t.Fatal("BLOCKED: real LibreOffice fixture unavailable; set OMNIPUS_TEST_OFFICE_ROOT — required by T-102 (real LibreOffice is missing (fail, never skip))")
	}
	rendererRoot := os.Getenv("OMNIPUS_TEST_RENDERER_ROOT")
	if rendererRoot == "" {
		t.Fatal("BLOCKED: real renderer fixture unavailable; set OMNIPUS_TEST_RENDERER_ROOT (a built omnipus-pdfium) — required by T-102 (both previews must produce pages, and no built omnipus-pdfium renderer exists on this Mac)")
	}
	opts := component.RegisterOptions{Home: home, Operator: "office-preview-test", Folder: officeRoot}
	_, err := component.RegisterOffice(opts)
	require.NoError(t, err)
	opts.Folder = rendererRoot
	_, err = component.RegisterRenderer(opts)
	require.NoError(t, err)
	self, err := os.Executable()
	require.NoError(t, err)
	svc, err := New(Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)

	// The real registry folder of the registered LibreOffice, and a file in
	// it the probe must be able to read (D8c grants the registry read-only;
	// macOS only — a Linux registration carries no services registry).
	registryFile := ""
	if runtime.GOOS == "darwin" {
		regDir := filepath.Join(component.RegisteredDir(home, component.KindOffice), "services")
		registryFile = filepath.Join(regDir, "services.rdb")
		_, statErr := os.Stat(registryFile)
		require.NoError(t, statErr, "instrument: the registered LibreOffice's filtered registry holds services.rdb")
	}

	// Planted targets, each verified to exist and be readable by the test —
	// so a probe that fails on one fails because of the sandbox, never
	// because the target is missing (T-102 false-pass note).
	workspaceFile := filepath.Join(t.TempDir(), "turn-note.txt")
	require.NoError(t, os.WriteFile(workspaceFile, []byte("workspace payload"), 0o600))
	_, readErr := os.ReadFile(workspaceFile)
	require.NoError(t, readErr, "instrument: the workspace target exists and is readable outside the sandbox")
	plantedFile := filepath.Join(home, "planted-in-data.txt")
	require.NoError(t, os.WriteFile(plantedFile, []byte("planted in the data folder"), 0o600))
	_, readErr = os.ReadFile(plantedFile)
	require.NoError(t, readErr, "instrument: the planted data-folder target exists and is readable outside the sandbox")
	outsideSystem := filepath.Join(home, "system", "t102-not-registry.txt")
	require.NoError(t, os.WriteFile(outsideSystem, []byte("not the registry"), 0o600))
	_, readErr = os.ReadFile(outsideSystem)
	require.NoError(t, readErr, "instrument: the <home>/system target outside the registry exists and is readable outside the sandbox")

	// The accepted seam (§3.4 D8d ruling): set only here, reset with
	// t.Cleanup, this test never runs in parallel, and zero captured starts
	// fails the test below. For every converter start the hook runs the probe
	// through the captured hooks — inside the start path, on this test's own
	// goroutine, so the call cannot finish under it — and records what the
	// probe found.
	type startProbe struct {
		start   converterStart
		parents []string
		receipt previewProbeReceipt
	}
	var (
		starts []converterStart
		probes []startProbe
	)
	testHookConverterStart = func(cs converterStart) {
		if isPreviewProbeStart(cs.args) {
			return // the probe's own start through the same hooks: record nothing, recurse no further
		}
		starts = append(starts, cs)
		if runtime.GOOS != "darwin" {
			return // Landlock cannot restrict look-up; the Linux probe variant is Unknown until run (T-102 row)
		}
		workDir := previewWorkDirOf(t, cs.args)
		parents := previewLookupParentFolders(t, cs.policy)
		// The "later" call folder is created HERE — after this call started —
		// with a planted file inside, and never before (T-102 false-pass note).
		later := filepath.Join(filepath.Dir(filepath.Dir(workDir)), fmt.Sprintf("t102-later-%d", len(starts)))
		require.NoError(t, os.MkdirAll(later, 0o700), "instrument: the later call folder is created inside the hook")
		laterFile := filepath.Join(later, "later-note.txt")
		require.NoError(t, os.WriteFile(laterFile, []byte("created after the call started"), 0o600))
		spec := previewProbeSpec{
			Receipt:      previewProbeReceiptPath,
			Lookup:       parents,
			Planted:      plantedFile,
			OtherCall:    later,
			OtherFile:    laterFile,
			Owner:        filepath.Join(filepath.Dir(workDir), "owner.json"),
			Workspace:    workspaceFile,
			Outside:      outsideSystem,
			RegistryFile: registryFile,
		}
		specData, specErr := json.Marshal(spec)
		if specErr != nil {
			t.Errorf("T-102: the probe spec does not encode: %v", specErr)
			return
		}
		if writeErr := os.WriteFile(filepath.Join(workDir, "probe-spec.json"), specData, 0o600); writeErr != nil {
			t.Errorf("T-102: the probe spec cannot be written into the live work folder %s: %v", workDir, writeErr)
			return
		}
		probe := exec.Command(self, "--office-preview-probe", "probe-spec.json")
		probe.Dir = workDir
		if prepareErr := cs.hooks.prepare(probe); prepareErr != nil {
			t.Errorf("T-102: the captured hooks refuse to prepare the probe with the same policy: %v", prepareErr)
			return
		}
		raw, runErr := probe.CombinedOutput()
		if runErr != nil {
			t.Errorf("T-102: the probe did not run cleanly under the captured policy: %v\n%s", runErr, raw)
			return
		}
		receipt, _, receiptErr := previewReadProbeReceipt(workDir)
		if receiptErr != nil {
			t.Errorf("T-102: the probe receipt cannot be read from the live work folder: %v\n%s", receiptErr, raw)
			return
		}
		probes = append(probes, startProbe{start: cs, parents: parents, receipt: receipt})
	}
	t.Cleanup(func() { testHookConverterStart = nil })

	docx := minimalDocx(t)
	type previewOutcome struct {
		pages      int
		unconfined string
		err        error
	}
	outcomes := make([]previewOutcome, 0, 2)
	for i := 1; i <= 2; i++ {
		// The spec's per-call deadline (CallTimeout, the 120 s the tools
		// pass): a LibreOffice stuck on its startup dialog fails this test
		// with the deadline outcome instead of hanging to the go test
		// timeout. Each preview is one call, so each iteration carries its
		// own deadline.
		ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
		adm, admErr := svc.Admit(ctx, true)
		require.NoError(t, admErr, "instrument: the office slot is free — each admission is released before the next is requested")
		var outcome previewOutcome
		func() {
			defer func() { adm.Release() }() // released before the next admission, including on failure
			res, previewErr := svc.Preview(ctx, adm, Request{
				Name: fmt.Sprintf("repeat%d.docx", i), Ext: ".docx", Data: docx, First: 1, Last: 1,
			})
			if res != nil {
				outcome.pages = len(res.Pages)
				outcome.unconfined = res.Unconfined
			}
			outcome.err = previewErr
		}()
		cancel()
		outcomes = append(outcomes, outcome)
	}

	// Zero captured starts fails, never skips (seam ruling, §3.4 D8d).
	require.NotEmpty(t, starts, "T-102: the hook captured zero converter starts — a test expecting a confined start fails, never skips")
	if runtime.GOOS == "darwin" {
		require.Len(t, probes, len(starts), "T-102: every converter start ran the probe at its in-call barrier")
	}

	for _, probeRun := range probes {
		receipt := probeRun.receipt
		require.ElementsMatch(t, probeRun.parents, receipt.LookupOK,
			"T-102 §3.4 D8d: the probe can look up (stat) every folder above the converter's own folders — computed here from the captured policy's real paths; failures: %v", receipt.LookupFail)
		require.Empty(t, receipt.ListOK,
			"T-102 §3.4 D8d: the probe cannot list any folder above the converter's own folders; unexpectedly listed: %v", receipt.ListOK)
		require.ElementsMatch(t, probeRun.parents, receipt.ListFail,
			"T-102 §3.4 D8d: listing is refused on every look-up folder")
		require.NotEmpty(t, receipt.PlantedStatErr, "T-102 §3.4 D8d/B-70: the probe cannot look up a file planted directly in the data folder")
		require.NotEmpty(t, receipt.PlantedReadErr, "T-102 §3.4 D8d/B-70: the probe cannot read a file planted directly in the data folder")
		require.NotEmpty(t, receipt.OtherCallStatErr, "T-102 §3.4 D8d/B-70: the probe cannot look up another call folder (created after this call started)")
		require.NotEmpty(t, receipt.OtherFileReadErr, "T-102: the probe cannot read a file in another call folder")
		require.NotEmpty(t, receipt.OwnerReadErr, "T-102 §3.4 D8c: the probe cannot read this call's owner record (§3.4 D1 keeps it out of the converter's reach)")
		require.NotEmpty(t, receipt.WorkspaceReadErr, "T-102 §3.4 D8c: the probe cannot read a file in the agent's workspace")
		require.NotEmpty(t, receipt.OutsideReadErr, "T-102 §3.4 D8c: the probe cannot read <home>/system outside the registry")
		require.Equal(t, "", receipt.RegistryReadErr, "T-102 §3.4 D8c: the probe reads the registry folder")
		require.NotEmpty(t, receipt.RegistryWriteErr, "T-102 §3.4 D8c: the probe cannot change the registry folder")
		require.Equal(t, "", receipt.WorkWriteErr, "T-102 §3.4 D8c: the probe writes in this call's work folder")
	}

	for _, cs := range starts {
		require.False(t, cs.policy.ReadsOpen, "T-102 §3.4 D8c: ReadsOpen is false")
		require.False(t, cs.policy.ExecOpen, "T-102 §3.4 D8c: ExecOpen is false")
		require.Empty(t, cs.policy.DeniedPaths, "T-102 §3.4 D8c: the converter policy carries no deny list")
		require.Empty(t, cs.policy.DeniedNodes, "T-102 §3.4 D8c: the converter policy carries no deny list")
		require.Empty(t, cs.policy.DeniedPathPrefixes, "T-102 §3.4 D8c: the converter policy carries no deny list")
		require.Empty(t, cs.policy.WriteDeniedPaths, "T-102 §3.4 D8c: the converter policy carries no deny list")
		workDir := previewWorkDirOf(t, cs.args)
		for _, rule := range cs.policy.FilesystemRules {
			resolved, linkErr := filepath.EvalSymlinks(rule.Path)
			require.NoError(t, linkErr, "instrument: a captured rule path resolves: %s", rule.Path)
			if !previewWithinDir(home, resolved) {
				continue // outside the data folder: not this check's subject
			}
			inWork := previewWithinDir(workDir, resolved)
			inRegistry := registryFile != "" && previewWithinDir(filepath.Dir(filepath.Dir(registryFile)), resolved)
			require.True(t, inWork || inRegistry,
				"T-102 §3.4 D8c: no rule covers any part of the data folder other than that call's work and, on macOS, the registry folder — found rule on %s", resolved)
		}
		if runtime.GOOS != "darwin" {
			continue // the profile look-up lines and posture are macOS rows; Linux is Unknown until run
		}
		// D8d: the profile's look-up lines, in the policy section (after the
		// filesystem-allows marker, never the platform preamble), are exactly
		// the folders above the listed folders, recomputed here from the
		// captured policy's real paths — and no broader grant covers any of
		// them.
		profile := seatbeltProfileOf(t, cs)
		marker := strings.Index(profile, ";; --- Filesystem allows")
		require.NotEqual(t, -1, marker, "T-102: the profile marks its policy section")
		section := profile[marker:]
		parents := previewLookupParentFolders(t, cs.policy)
		for _, parent := range parents {
			require.Contains(t, section, previewAllowLineExact(parent),
				"T-102 §3.4 D8d: the profile carries exactly one look-up line for %s", parent)
		}
		metadataCount := 0
		for _, line := range previewParseAllows(section) {
			if line.verb == "file-read-metadata" && line.path != "" {
				metadataCount++
				require.Contains(t, parents, line.path,
					"T-102 §3.4 D8d: every look-up line names a folder above a listed folder (computed independently), never anything else — found %s", line.path)
				continue
			}
			for _, parent := range parents {
				require.NotEqual(t, parent, line.path,
					"T-102 §3.4 D8d/MV-3: no %s line covers the look-up folder %s (look-up only)", line.verb, parent)
			}
		}
		require.Equal(t, len(parents), metadataCount,
			"T-102 §3.4 D8d: the profile carries exactly one look-up line per folder above the listed folders, %d expected", len(parents))
		assertConverterProfilePosture(t, cs, profile)
	}

	for i, outcome := range outcomes {
		require.NoError(t, outcome.err,
			"T-102 §3.4 D8c: preview %d of two must succeed on the converter's own read list — a failure here is the converter failing to start confined (pre-D8c: the registry under <home>/system was not granted; at the D8c state on macOS: LibreOffice's parent-folder look-up denials, the open issue the D8d implementation closes)", i+1)
		require.NotEmpty(t, outcome.pages, "T-102: preview %d of two produces pages", i+1)
		require.Equal(t, "", outcome.unconfined, "T-102: the converter runs confined on preview %d — an unconfined run is not a pass", i+1)
	}
}

// assertConverterProfilePosture checks the MV-3 posture facts that do not
// depend on which folders are listed: the 3 socket rules (the socket
// folder's read, write and bind/connect) rendered for the captured socket
// folder, the explicit network deny, and exactly 2 Mach lookups.
func assertConverterProfilePosture(t *testing.T, cs converterStart, profile string) {
	t.Helper()
	require.Len(t, cs.policy.UnixSocketRules, 1, "T-102/MV-3: the converter carries exactly the socket folder rule")
	socket := cs.policy.UnixSocketRules[0].Path
	socketReal, linkErr := filepath.EvalSymlinks(socket)
	require.NoError(t, linkErr, "instrument: the captured socket folder resolves")
	require.Contains(t, profile, fmt.Sprintf("(allow network-bind (subpath %q))", socketReal), "T-102/MV-3: the socket bind rule is in the profile")
	require.Contains(t, profile, fmt.Sprintf("(allow network-outbound (subpath %q))", socketReal), "T-102/MV-3: the socket connect rule is in the profile")
	require.Contains(t, profile, fmt.Sprintf("(allow file-write* (subpath %q))", socketReal), "T-102/MV-3: the socket folder write rule is in the profile")
	require.Contains(t, profile, "(deny network*)", "T-102/MV-3: the profile denies network")
	mach := 0
	for _, line := range strings.Split(profile, "\n") {
		if strings.Contains(line, "(allow mach-lookup") {
			mach++
		}
	}
	require.Equal(t, 2, mach, "T-102/MV-3: exactly 2 Mach lookups (the macOS preamble's two)")
}

// previewWorkDirOf returns this start's work folder: the parent of the
// --outdir argument on the captured, post-wrap command line (a path the
// production code built, never one the test guesses).
func previewWorkDirOf(t *testing.T, args []string) string {
	t.Helper()
	for i := 1; i < len(args); i++ {
		if args[i] == "--outdir" && i+1 < len(args) {
			return filepath.Dir(args[i+1])
		}
	}
	require.Fail(t, "T-102: the captured start carries no --outdir argument", "args: %v", args)
	return ""
}

// isPreviewProbeStart reports whether a captured command line is the probe
// program started through the same hooks (so the hook does not probe the
// probe).
func isPreviewProbeStart(args []string) bool {
	for _, arg := range args {
		if arg == "--office-preview-probe" {
			return true
		}
	}
	return false
}

// minimalDocx builds the smallest docx LibreOffice opens: a zip with the
// content types and one paragraph.
func minimalDocx(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)
	files := []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>repeat preview fixture</w:t></w:r></w:p></w:body></w:document>`},
	}
	for _, file := range files {
		writer, err := zipWriter.Create(file.name)
		require.NoError(t, err)
		_, err = writer.Write([]byte(file.body))
		require.NoError(t, err)
	}
	require.NoError(t, zipWriter.Close())
	return buf.Bytes()
}
