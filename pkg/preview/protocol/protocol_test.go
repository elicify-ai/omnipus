package protocol

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

// Encode the peer's wire independently, not using the production writer.
func peerFrame(kind byte, payload []byte) []byte {
	frame := make([]byte, 5+len(payload))
	frame[0] = kind
	binary.LittleEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame
}
func peerDone(n uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, n)
	return peerFrame(5, b)
}
func peerMeta() []byte {
	return peerFrame(1, []byte(`{"protocol_version":1,"build_id":"peer-v1","page_count":3,"encrypted":false}`))
}
func peerPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 2, 3))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, im))
	return b.Bytes()
}
func peerPage(n, w, h uint32, pngBytes []byte) []byte {
	b := make([]byte, 12+len(pngBytes))
	binary.LittleEndian.PutUint32(b, n)
	binary.LittleEndian.PutUint32(b[4:], w)
	binary.LittleEndian.PutUint32(b[8:], h)
	copy(b[12:], pngBytes)
	return peerFrame(2, b)
}
func peerStream(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestPreviewProtocol_RequestWireAndLimits(t *testing.T) {
	req := Request{Op: OpRender, FirstPage: 1, LastPage: 20, MaxLongEdge: 2000, Flags: 1, MaxInput: 50 << 20}
	var out bytes.Buffer
	require.NoError(t, WriteRequest(&out, req, bytes.NewReader([]byte("pdf")), 3))
	// AD-2: 32-byte little-endian header followed by the exact input.
	want := []byte{'O', 'P', 'D', 'F', 1, 0, 2, 0, 1, 0, 0, 0, 20, 0, 0, 0, 208, 7, 0, 0, 1, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 'p', 'd', 'f'}
	require.Equal(t, want, out.Bytes())
	for _, n := range []int64{(50 << 20) - 1, 50 << 20} {
		require.NoError(t, req.Validate(n))
	}
	require.ErrorContains(t, req.Validate((50<<20)+1), "input length")
	require.ErrorContains(t, req.Validate(0), "input length")
	req.MaxInput = 200 << 20
	require.NoError(t, req.Validate(200<<20))
	require.ErrorContains(t, req.Validate((200<<20)+1), "input length")
	req.LastPage = 21
	require.ErrorContains(t, req.Validate(1), "page range")
	req.LastPage = 19
	require.NoError(t, req.Validate(1))
	req.FirstPage = 0
	require.ErrorContains(t, req.Validate(1), "page range")
}

func TestPreviewProtocol_StreamsValidatedPagesWithoutRetainingCopies(t *testing.T) {
	p := peerPNG(t)
	req := Request{Op: OpRender, FirstPage: 1, LastPage: 2, MaxLongEdge: 2000}
	wire := peerStream(peerMeta(), peerPage(1, 2, 3, p), peerPage(2, 2, 3, p), peerDone(4))
	var pages []Page
	response, err := ReadResponse(bytes.NewReader(wire), req, func(page Page) error { pages = append(pages, page); return nil })
	require.NoError(t, err)
	require.Equal(t, Meta{ProtocolVersion: 1, BuildID: "peer-v1", PageCount: 3}, response.Meta)
	require.Empty(t, response.Pages, "AD-6: sink path must not also retain PAGE buffers in the response")
	require.Equal(t, []Page{{Number: 1, Width: 2, Height: 3, PNG: p}, {Number: 2, Width: 2, Height: 3, PNG: p}}, pages)
}

func TestPreviewProtocol_RejectsMalformedPeerFrames(t *testing.T) {
	p := peerPNG(t)
	page := peerPage(1, 2, 3, p)
	meta := peerMeta()
	oversized := []byte{2, 1, 0, 64, 0} // AD-2: 4 MiB + 1 declaration, no allocation/payload.
	cases := []struct {
		name string
		wire []byte
	}{
		{"missing_meta", peerStream(page, peerDone(2))},
		{"duplicate_meta", peerStream(meta, meta, peerDone(3))},
		{"wrong_version", peerStream(peerFrame(1, []byte(`{"protocol_version":2,"build_id":"peer-v2","page_count":3,"encrypted":false}`)), peerDone(2))},
		{"unknown_type", peerStream(meta, peerFrame(99, nil))},
		{"oversized_page_declaration", peerStream(meta, oversized)},
		{"duplicate_page", peerStream(meta, page, page, peerDone(4))},
		{"out_of_range", peerStream(meta, peerPage(3, 2, 3, p), peerDone(3))},
		{"descending_pages", peerStream(meta, peerPage(2, 2, 3, p), page, peerDone(4))},
		{"dimensions_mismatch", peerStream(meta, peerPage(1, 3, 2, p), peerDone(3))},
		{"missing_done", peerStream(meta, page)},
		{"wrong_done_count", peerStream(meta, page, peerDone(2))},
		{"output_after_done", peerStream(meta, page, peerDone(3), []byte("extra"))},
		{"truncated_header", peerStream(meta, []byte{2, 1})},
		{"truncated_payload", peerStream(meta, page[:len(page)-1])},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, err := ReadResponse(bytes.NewReader(tc.wire), Request{Op: OpRender, FirstPage: 1, LastPage: 2, MaxLongEdge: 2000}, nil)
			require.Nil(t, response)
			require.IsType(t, &ProtocolError{}, err)
			require.ErrorContains(t, err, "protocol error:")
		})
	}
}

func TestPreviewProtocol_RejectsPNGThatCannotFullyDecode(t *testing.T) {
	// Valid IHDR but missing image data/IEND: DecodeConfig alone is not a
	// successful PNG decode (AD-2 PAGE rule). The negative control proves this.
	broken := peerPNG(t)[:33]
	_, err := png.DecodeConfig(bytes.NewReader(broken))
	require.NoError(t, err)
	_, err = png.Decode(bytes.NewReader(broken))
	require.Error(t, err)
	wire := peerStream(peerMeta(), peerPage(1, 2, 3, broken), peerDone(3))
	response, err := ReadResponse(bytes.NewReader(wire), Request{Op: OpRender, FirstPage: 1, LastPage: 1, MaxLongEdge: 2000}, nil)
	require.Nil(t, response, "undecodable page bytes must not be admitted as a rendered page")
	require.IsType(t, &ProtocolError{}, err)
}
