//go:build darwin || linux

package preview_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// T-10 / B-05: real Office conversion, real pdfium/extractor, stale source
// cache independently checked before conversion. No workbook writeback.
//
// The confinement state is made explicit and deterministic: the kernel
// sandbox is primed HERE, in this order-independent first statement, so the
// converter always runs confined where the platform supports it — running
// this test alone used to leave the process unprimed and the converter
// unconfined, and the test passed while checking nothing about confinement.
// result.Unconfined must stay empty (an unconfined run is not a pass).
// The context carries the spec's per-call deadline (preview.CallTimeout, the
// 120 s the tools use) so a LibreOffice stuck on a startup dialog fails the
// test with the deadline message instead of hanging to the go test timeout.
// RED on macOS at 8e86c0ea3 (LibreOffice terminates with
// com::sun::star::uno::DeploymentException once confined — the parent-folder
// look-up issue D8d is ruling on); that is the open spec issue, not this
// test's defect.
func TestPreviewOffice_RealStaleFormulaCacheRecalculatesToTwo(t *testing.T) {
	officepreview.PrimeKernelSandbox(t)
	source := officepreview.StaleWorkbook(t)
	zr, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	require.NoError(t, err)
	cached, err := zr.Open("xl/worksheets/sheet1.xml")
	require.NoError(t, err)
	xmlBytes, err := io.ReadAll(cached)
	require.NoError(t, err)
	require.NoError(t, cached.Close())
	require.Contains(t, string(xmlBytes), "<f>1+1</f><v>1</v>", "positive control: source really contains the wrong cached answer")
	svc := officepreview.Service(t, true, true)
	ctx, cancel := context.WithTimeout(context.Background(), preview.CallTimeout)
	defer cancel()
	adm, err := svc.Admit(ctx, true)
	require.NoError(t, err)
	defer adm.Release()
	result, err := svc.Preview(ctx, adm, preview.Request{Name: "stale.xlsx", Ext: ".xlsx", Data: source, Default: true})
	require.NoError(t, err)
	require.Equal(t, "", result.Unconfined, "T-10: the preview must run confined where the platform supports it — an unconfined run is not a pass")
	require.Equal(t, "rendered", result.Validity)
	require.NotEmpty(t, result.Pages)
	var text strings.Builder
	for _, page := range result.Text {
		text.WriteString(page.Text)
		text.WriteByte('\n')
	}
	require.Contains(t, strings.Fields(text.String()), "2", "the rendered PDF must contain the recalculated answer, not just reuse cached 1")
	// Conversion receives a copy; source package bytes remain unchanged.
	require.Equal(t, officepreview.StaleWorkbook(t), source)
}
