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
func TestPreviewOffice_RealStaleFormulaCacheRecalculatesToTwo(t *testing.T) {
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
	adm, err := svc.Admit(context.Background(), true)
	require.NoError(t, err)
	defer adm.Release()
	result, err := svc.Preview(context.Background(), adm, preview.Request{Name: "stale.xlsx", Ext: ".xlsx", Data: source, Default: true})
	require.NoError(t, err)
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
