//go:build darwin || linux

package preview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/stretchr/testify/require"
)

// The cache test's converter and renderer are this test binary, launched
// under those file names. A normal `go test` argv0 is neither name.
func init() {
	switch filepath.Base(os.Args[0]) {
	case "gatefix-soffice":
		os.Exit(gatefixSofficeChild())
	case "gatefix-pdfium":
		os.Exit(gatefixPdfiumChild())
	}
}

// G8 / CR F2, SF W-02. AD-9: the page cache key is the converted PDF's
// SHA-256, not the Office source bytes. Two conversions of the same source
// that produce different PDFs must not reuse each other's page images.
func TestOfficePreview_PageCacheKeysTheConvertedPDF(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	home := t.TempDir()
	officeRoot := gatefixInstallProgram(t, "gatefix-soffice")
	rendererRoot := gatefixInstallProgram(t, "gatefix-pdfium")
	gatefixRegisterComponent(t, home, officeRoot, "gatefix-soffice", component.KindOffice, "", 0)
	gatefixRegisterComponent(t, home, rendererRoot, "gatefix-pdfium", component.KindPDFRenderer, "gatefix-renderer", 1)
	svc, err := New(Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)
	source := []byte("same-office-source-for-both-previews")
	first := gatefixOfficePreview(t, svc, source)
	second := gatefixOfficePreview(t, svc, source)
	require.NotEmpty(t, first.Pages)
	require.NotEmpty(t, second.Pages)
	require.NotEqual(t, first.Pages[0].PNG, second.Pages[0].PNG, "AD-9: the page cache must key the converted PDF; a different conversion of the same Office source must not reuse the first page image")
}

func gatefixOfficePreview(t *testing.T, svc *Service, source []byte) *Result {
	t.Helper()
	adm, err := svc.Admit(context.Background(), true)
	require.NoError(t, err)
	defer adm.Release()
	res, err := svc.Preview(context.Background(), adm, Request{
		Name: "notes.docx", Ext: ".docx", Data: source, First: 1, Last: 1,
	})
	require.NoError(t, err)
	return res
}

func gatefixSofficeChild() int {
	// A fresh PDF on every start, so two conversions of one source differ.
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	pdf := gatefixChildPDF("converted-" + stamp)
	if os.MkdirAll("out", 0o700) != nil {
		return 78
	}
	if os.WriteFile(filepath.Join("out", "document.pdf"), pdf, 0o600) != nil {
		return 78
	}
	return 0
}

func gatefixPdfiumChild() int {
	req, n, err := protocol.ReadRequest(os.Stdin)
	if err != nil {
		return 2
	}
	pdf := make([]byte, n)
	if _, err := io.ReadFull(os.Stdin, pdf); err != nil {
		return 3
	}
	sum := sha256.Sum256(pdf)
	meta := protocol.Meta{ProtocolVersion: protocol.Version, BuildID: "gatefix-renderer", PageCount: 1}
	switch req.Op {
	case protocol.OpInfo:
		return gatefixProtocolReply(meta, nil, "")
	case protocol.OpRender:
		return gatefixProtocolReply(meta, gatefixPixelPNG(sum[0], sum[1], sum[2]), "")
	case protocol.OpText:
		return gatefixProtocolReply(meta, nil, "gatefix")
	default:
		return 4
	}
}

func gatefixProtocolReply(meta protocol.Meta, pagePNG []byte, text string) int {
	frames := 0
	write := func(typ byte, payload []byte) {
		var h [5]byte
		h[0] = typ
		binary.LittleEndian.PutUint32(h[1:], uint32(len(payload)))
		_, _ = os.Stdout.Write(h[:])
		_, _ = os.Stdout.Write(payload)
		frames++
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return 5
	}
	write(protocol.FrameMeta, metaBytes)
	if pagePNG != nil {
		payload := make([]byte, 12+len(pagePNG))
		binary.LittleEndian.PutUint32(payload[0:4], 1)
		binary.LittleEndian.PutUint32(payload[4:8], 1)
		binary.LittleEndian.PutUint32(payload[8:12], 1)
		copy(payload[12:], pagePNG)
		write(protocol.FramePage, payload)
	}
	if text != "" {
		payload := make([]byte, 4+len(text))
		binary.LittleEndian.PutUint32(payload[0:4], 1)
		copy(payload[4:], text)
		write(protocol.FrameText, payload)
	}
	var done [4]byte
	binary.LittleEndian.PutUint32(done[:], uint32(frames+1))
	write(protocol.FrameDone, done[:])
	return 0
}

func gatefixPixelPNG(r, g, b byte) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: r, G: g, B: b, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func gatefixChildPDF(mark string) []byte {
	stream := "BT /F1 12 Tf (" + mark + ") Tj ET\n"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream),
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = out.Len()
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
