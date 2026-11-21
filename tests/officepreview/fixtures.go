// Package officepreview supplies test-only document fixtures and a launcher
// host. Nothing under this package is linked by the product.
package officepreview

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/preview/extracttext"
	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/elicify-ai/omnipus/tests/officepreview/realprograms"
	"github.com/stretchr/testify/require"
)

// Host the real hidden modes in the test executable, so tests exercise the
// checked-out launcher/extractor instead of a stale prebuilt Omnipus binary.
func init() {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case launcher.ModeArg:
		os.Exit(launcher.Main(os.Args[2:]))
	case extracttext.ModeArg:
		os.Exit(extracttext.Main())
	}
}

// Service uses test-owned protected registration and real installed components.
// Test-only environment variables name fixture locations; the production
// resolver never receives those variables as component-root configuration.
func Service(t *testing.T, office, renderer bool) *preview.Service {
	t.Helper()
	officeRoot, rendererRoot := realprograms.Roots(t, office, renderer)
	self, err := os.Executable()
	require.NoError(t, err)
	home := t.TempDir()
	opts := component.RegisterOptions{Home: home, Operator: "office-preview-test"}
	if office {
		opts.Folder = officeRoot
		_, err = component.RegisterOffice(opts)
		require.NoError(t, err)
	}
	if renderer {
		opts.Folder = rendererRoot
		_, err = component.RegisterRenderer(opts)
		require.NoError(t, err)
	}
	svc, err := preview.New(preview.Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)
	return svc
}

// PDF emits an independently specified PDF: 200 x 100 points, alternating
// red and blue full-page fills and page-number text. At 2x the expected PNG
// is 400 x 200, with red page 1 / blue page 2. No renderer output is an oracle.
func PDF(pages int) []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	var kids bytes.Buffer
	for i := 0; i < pages; i++ {
		page := 4 + 2*i
		fmt.Fprintf(&kids, "%d 0 R ", page)
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", page+1))
		color := "1 0 0"
		if i%2 == 1 {
			color = "0 0 1"
		}
		stream := fmt.Sprintf("q %s rg 0 0 200 100 re f Q BT /F1 12 Tf 10 10 Td (page %d) Tj ET\n", color, i+1)
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", pages, kids.String())
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}
