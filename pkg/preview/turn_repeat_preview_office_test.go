//go:build darwin || linux

package preview

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// T-102 / §3.4 D8, FR-054 (B-67): a real Office preview, TWICE in a row, from
// an agent turn, on a home where tmp already exists, through the real
// confinement hooks with the turn's real read model. Both previews must
// produce pages, and the policy may open only <id>/work — never tmp,
// tmp/preview-calls or <id>. This settles D8's Unknown: the turn's deny list
// (home entries that are not branches, so <home>/tmp once it exists) may
// defeat the second preview's write to its own scratch on macOS.
//
// Real components are required: registration BLOCKS (never skips) when the
// real LibreOffice or renderer fixture is unavailable.
func TestPreviewRepeatFromAgentTurnKeepsItsOwnScratch(t *testing.T) {
	primeKernelSandbox(t)
	home := t.TempDir()
	// tmp must exist BEFORE the turn policy is derived — that is exactly the
	// state that puts <home>/tmp on the deny list (D8).
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

	// The turn's REAL read model: a registered boot base + a real authored
	// FSPolicy, derived the way preview_document.go derives it.
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelConfined})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	require.True(t, sandbox.TurnPolicyBaseInstalled(), "instrument: a policy base is registered (otherwise the policy is nil and the test is vacuous)")
	workspace := t.TempDir()
	authored := fspolicy.FSPolicy{WorkDir: workspace}
	readModel, err := sandbox.KernelPolicyForTurn(authored)
	require.NoError(t, err)
	require.NotNil(t, readModel, "T-102: ReadModel must not be nil — every existing test passes nil, which is exactly what this row exists to close")
	require.Contains(t, readModel.DeniedPaths, filepath.Join(home, "tmp"),
		"instrument: the turn's read model really denies <home>/tmp — the D8 trigger is in force")

	docx := minimalDocx(t)
	for i := 1; i <= 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		adm, err := svc.Admit(ctx, true)
		require.NoError(t, err)
		res, previewErr := svc.Preview(ctx, adm, Request{
			Name: "repeat.docx", Ext: ".docx", Data: docx, First: 1, Last: 1,
			ReadModel: readModel,
		})
		cancel()
		require.NoError(t, previewErr, "T-102: preview %d of two must succeed on a home where tmp exists", i)
		require.NotEmpty(t, res.Pages, "T-102: preview %d of two produces pages", i)
		require.Equal(t, "", res.Unconfined, "T-102: the converter runs confined on preview %d", i)
	}

	// Second check: the policy that reaches the converter opens only the
	// call's work folder — nothing wider. Built from the same inputs the
	// real call used; a wider allow would undo D1.
	constrained := confinement{readModel: readModel, readWrite: []string{filepath.Join(home, "tmp", "preview-calls", "someid", "work")}}
	policy := constrained.policy()
	var allowedWrite []string
	for _, rule := range policy.FilesystemRules {
		if rule.Access&sandbox.AccessWrite != 0 {
			allowedWrite = append(allowedWrite, rule.Path)
		}
	}
	require.Equal(t, []string{filepath.Join(home, "tmp", "preview-calls", "someid", "work")}, allowedWrite,
		"T-102 §3.4 D8: the policy opens only <id>/work — never tmp, tmp/preview-calls or <id>")
	require.Contains(t, policy.DeniedPaths, filepath.Join(home, "tmp"),
		"the turn's deny on <home>/tmp is kept in the converter's policy")
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
