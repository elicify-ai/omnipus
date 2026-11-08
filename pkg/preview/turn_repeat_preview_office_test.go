//go:build darwin || linux

package preview

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// T-102 / §3.4 D8, D8c; FR-054, FR-055 (B-67, B-69; revised again 2026-11-08
// for D8c). Two real Office previews in a row through Service.Preview with
// exactly the arguments the tools pass — the turn's policy is no longer an
// input (Request.ReadModel goes; its removal is compiler-enforced for all
// three callers) — on a home where tmp already exists, with
// operator-registered real LibreOffice and the real renderer; each admission
// is released before the next is requested (maxOfficeRuns = 1: a held
// admission would deadlock the second preview). Both previews produce pages,
// confined.
//
// Second check, on the policy each real start used, plus the read probes —
// HELD on the pre-D8c code: the spec's test seam does not exist yet. The
// BLOCKED fatal below names it; the held checks, per the T-102 row:
//
//   - a test seam capturing the converter's SandboxPolicy per start, never a
//     policy built by hand;
//   - the file rules are exactly the §3.4 D8c list for the platform
//     (component root read+execute; launcher folder read+execute; this
//     call's <id>/work read+write; this call's socket folder read+write with
//     socket bind/connect on macOS; system libraries per the platform
//     preamble; on macOS Component.Registry read-only; the platform's font
//     folders read-only);
//   - write rules are exactly {<id>/work, socket folder} on macOS and
//     {<id>/work} on Linux; ReadsOpen and ExecOpen are false;
//   - no rule covers any part of the data folder other than that work and,
//     on macOS, the registry folder; URE_MORE_SERVICES names
//     Component.Registry;
//   - probes: a harmless probe program started through the same confinement
//     hooks with the captured policy, at a barrier inside the call (a
//     barrier file, not a timer), tries to read a file in another call
//     folder created after this call started, this call's owner.json, a file
//     in the agent's workspace and a file in <home>/system outside the
//     registry — every read fails; it writes in work, which succeeds; it
//     reads the registry, which succeeds, and writes there, which fails.
//
// Not a pass if: the policy is built by hand; the "later" folder is created
// before the start; an unconfined run is counted as a pass; a missing
// component skips instead of failing; an admission is never released; the
// component is registered so its registry lies inside its root; a probe
// expected to fail fails for a reason other than the sandbox (every planted
// target below is verified to exist and be readable by the test first).
func TestPreviewRepeatFromAgentTurnKeepsItsOwnScratch(t *testing.T) {
	primeKernelSandbox(t)
	home := t.TempDir()
	// tmp exists before the previews: the D8c guarantee is timing-independent
	// (no deny walk, nothing to snapshot), and a real entry must be there for
	// the no-other-data-folder-rule check to mean something.
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

	// The probe targets, planted before the previews and each verified to be
	// readable by the test — so a probe that fails on one fails because of
	// the sandbox, never because the target is missing (T-102 false-pass
	// note). This call's owner.json needs no planting: it exists inside the
	// live call, where the barrier probe reads it.
	workspaceFile := filepath.Join(t.TempDir(), "turn-note.txt")
	require.NoError(t, os.WriteFile(workspaceFile, []byte("workspace payload"), 0o600))
	_, readErr := os.ReadFile(workspaceFile)
	require.NoError(t, readErr, "instrument: the workspace target exists and is readable outside the sandbox")
	outsideSystem := filepath.Join(home, "system", "t102-not-registry.txt")
	require.NoError(t, os.WriteFile(outsideSystem, []byte("not the registry"), 0o600))
	_, readErr = os.ReadFile(outsideSystem)
	require.NoError(t, readErr, "instrument: the <home>/system target outside the registry exists and is readable outside the sandbox")

	docx := minimalDocx(t)
	for i := 1; i <= 2; i++ {
		// The spec's per-call deadline (CallTimeout, the 120 s the tools
		// pass): a LibreOffice stuck on its startup dialog fails this test
		// with the deadline outcome instead of hanging to the go test
		// timeout. Each preview is one call, so each iteration carries its
		// own deadline.
		ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
		adm, admErr := svc.Admit(ctx, true)
		require.NoError(t, admErr, "instrument: the office slot is free — each admission is released before the next is requested")
		res, previewErr := svc.Preview(ctx, adm, Request{
			Name: fmt.Sprintf("repeat%d.docx", i), Ext: ".docx", Data: docx, First: 1, Last: 1,
		})
		adm.Release() // released before the next admission, including on failure
		cancel()
		require.NoError(t, previewErr,
			"T-102 §3.4 D8c: preview %d of two must succeed on the converter's own read list — a failure here is the converter failing to start confined (pre-D8c: the registry under <home>/system was not granted; at the D8c state on macOS: LibreOffice's parent-folder look-up denials, the open issue the D8d ruling decides)", i)
		require.NotEmpty(t, res.Pages, "T-102: preview %d of two produces pages", i)
		require.Equal(t, "", res.Unconfined, "T-102: the converter runs confined on preview %d — an unconfined run is not a pass", i)
	}

	t.Fatal("BLOCKED: the policy each real start used cannot be captured on this code — the T-102 test seam (a hook in pkg/preview capturing the converter's SandboxPolicy per launcher start, called from the office conversion's start path) does not exist, and the row forbids a policy built by hand. Required by T-102 (§13) for the policy-shape checks and for the probe program (started through the same confinement hooks with the captured policy, at a barrier inside the call); the held checks are listed in this test's doc comment")
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
