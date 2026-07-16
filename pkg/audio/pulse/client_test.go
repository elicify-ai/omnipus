package pulse

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPulse_LoadModule_Request verifies the exact byte layout of a
// LOAD_MODULE request packet — the 20-byte pstream descriptor plus the
// tagstruct command+tag+name+argument body — against a hand-computed
// expected sequence. This is the framing-level proof the epic asks for: get
// this wrong and every real PulseAudio server rejects (or silently
// misinterprets) the request.
func TestPulse_LoadModule_Request(t *testing.T) {
	var args tagWriter
	args.putString("module-null-sink")
	args.putString("sink_name=vspk")

	const tag = uint32(7)
	payload := buildRequest(commandLoadModule, tag, &args)

	var wantPayload bytes.Buffer
	wantPayload.WriteByte(byte(tagUint32))
	var cmdBytes [4]byte
	binary.BigEndian.PutUint32(cmdBytes[:], uint32(commandLoadModule))
	wantPayload.Write(cmdBytes[:])

	wantPayload.WriteByte(byte(tagUint32))
	var tagBytes [4]byte
	binary.BigEndian.PutUint32(tagBytes[:], tag)
	wantPayload.Write(tagBytes[:])

	wantPayload.WriteByte(byte(tagString))
	wantPayload.WriteString("module-null-sink")
	wantPayload.WriteByte(0)

	wantPayload.WriteByte(byte(tagString))
	wantPayload.WriteString("sink_name=vspk")
	wantPayload.WriteByte(0)

	if !bytes.Equal(payload, wantPayload.Bytes()) {
		t.Fatalf("LOAD_MODULE tagstruct payload mismatch:\n got  %v\n want %v", payload, wantPayload.Bytes())
	}

	// Now confirm writePacket prefixes the correct 20-byte descriptor:
	// length=len(payload), channel=0xffffffff (control), offset/flags=0.
	var wire bytes.Buffer
	if err := writePacket(&wire, payload); err != nil {
		t.Fatalf("writePacket: %v", err)
	}
	wireBytes := wire.Bytes()
	if len(wireBytes) != 20+len(payload) {
		t.Fatalf("wire length = %d, want %d", len(wireBytes), 20+len(payload))
	}
	gotLen := binary.BigEndian.Uint32(wireBytes[0:4])
	if gotLen != uint32(len(payload)) {
		t.Fatalf("descriptor length = %d, want %d", gotLen, len(payload))
	}
	gotChannel := binary.BigEndian.Uint32(wireBytes[4:8])
	if gotChannel != controlChannel {
		t.Fatalf("descriptor channel = %#x, want %#x", gotChannel, controlChannel)
	}
	for i := 8; i < 20; i++ {
		if wireBytes[i] != 0 {
			t.Fatalf("descriptor offset/flags byte %d = %d, want 0", i, wireBytes[i])
		}
	}
	if !bytes.Equal(wireBytes[20:], payload) {
		t.Fatalf("wire payload does not match the tagstruct body")
	}

	// readPacket must be the exact inverse of writePacket.
	gotPayload, err := readPacket(bytes.NewReader(wireBytes))
	if err != nil {
		t.Fatalf("readPacket: %v", err)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatalf("readPacket did not invert writePacket: got %v want %v", gotPayload, payload)
	}
}

// --- Dial/LoadModule against a minimal, independent mock server ---
//
// This mock deliberately does NOT reuse tagWriter/tagReader/buildRequest —
// it hand-decodes the wire bytes with encoding/binary directly, so a bug in
// this package's own encoder/decoder can't hide from the test by being
// mirrored on both sides.

type fakeServerModuleCall struct {
	name string
	args string
}

type fakePAServer struct {
	ln    net.Listener
	calls []fakeServerModuleCall
}

func startFakePAServer(t *testing.T, socketPath string, cookie []byte) *fakePAServer {
	t.Helper()
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen %s: %v", socketPath, err)
	}
	s := &fakePAServer{ln: ln}
	go s.acceptLoop(t, cookie)
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakePAServer) acceptLoop(t *testing.T, cookie []byte) {
	conn, err := s.ln.Accept()
	if err != nil {
		return // listener closed by test cleanup
	}
	defer conn.Close()
	moduleIdx := uint32(0)
	for {
		hdr := make([]byte, 20)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr[0:4])
		payload := make([]byte, n)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		br := bytes.NewReader(payload)
		var cmdTag, cmdVal, seqTag, seqVal uint32
		mustReadTaggedU32(br, &cmdTag, &cmdVal)
		mustReadTaggedU32(br, &seqTag, &seqVal)

		switch commandForTest(cmdVal) {
		case 8: // AUTH
			var versionTag, version uint32
			mustReadTaggedU32(br, &versionTag, &version)
			gotCookie := readArbitraryForTest(br)
			if cookie != nil && !bytes.Equal(gotCookie, cookie) {
				t.Errorf("fake server: cookie mismatch: got %v want %v", gotCookie, cookie)
			}
			reply := replyHeader(cmdReplyForTest(), seqVal)
			reply = appendTaggedU32(reply, 32) // server version
			writeFramedForTest(conn, reply)
		case 9: // SET_CLIENT_NAME: don't bother decoding the proplist body
			reply := replyHeader(cmdReplyForTest(), seqVal)
			reply = appendTaggedU32(reply, 0) // client index
			writeFramedForTest(conn, reply)
		case 51: // LOAD_MODULE
			name := readStringForTest(br)
			args := readStringForTest(br)
			s.calls = append(s.calls, fakeServerModuleCall{name: name, args: args})
			moduleIdx++
			reply := replyHeader(cmdReplyForTest(), seqVal)
			reply = appendTaggedU32(reply, moduleIdx)
			writeFramedForTest(conn, reply)
		default:
			t.Errorf("fake server: unexpected command %d", cmdVal)
			return
		}
	}
}

func cmdReplyForTest() uint32        { return 2 }
func commandForTest(v uint32) uint32 { return v }

func mustReadTaggedU32(r *bytes.Reader, tag *uint32, val *uint32) {
	tb, _ := r.ReadByte()
	*tag = uint32(tb)
	var b [4]byte
	io.ReadFull(r, b[:])
	*val = binary.BigEndian.Uint32(b[:])
}

func readStringForTest(r *bytes.Reader) string {
	r.ReadByte() // tag byte ('t' or 'N') -- tests only ever send non-null strings
	var sb bytes.Buffer
	for {
		c, err := r.ReadByte()
		if err != nil || c == 0 {
			break
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

func readArbitraryForTest(r *bytes.Reader) []byte {
	r.ReadByte() // 'x' tag
	var lb [4]byte
	io.ReadFull(r, lb[:])
	n := binary.BigEndian.Uint32(lb[:])
	b := make([]byte, n)
	io.ReadFull(r, b)
	return b
}

func replyHeader(cmd uint32, tag uint32) []byte {
	var b []byte
	b = appendTaggedU32(b, cmd)
	b = appendTaggedU32(b, tag)
	return b
}

func appendTaggedU32(b []byte, v uint32) []byte {
	b = append(b, 'L')
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], v)
	return append(b, lb[:]...)
}

func writeFramedForTest(w io.Writer, payload []byte) {
	var hdr [20]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[4:8], 0xffffffff)
	w.Write(hdr[:])
	w.Write(payload)
}

// TestPulse_Dial_Auth_LoadModule_AgainstFakeServer proves the full client
// stack (Dial -> auth -> set-client-name -> LoadModule) against an
// independent mock implementation of the wire format, hermetically (no real
// pulseaudio binary).
func TestPulse_Dial_Auth_LoadModule_AgainstFakeServer(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "native")
	cookiePath := filepath.Join(dir, "cookie")
	cookie := []byte("test-cookie-contents")
	if err := os.WriteFile(cookiePath, cookie, 0o600); err != nil {
		t.Fatalf("write cookie: %v", err)
	}

	srv := startFakePAServer(t, sockPath, cookie)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, sockPath, cookiePath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	idx1, err := client.LoadModule(ctx, "module-null-sink", "sink_name=vspk")
	if err != nil {
		t.Fatalf("LoadModule(null-sink): %v", err)
	}
	idx2, err := client.LoadModule(ctx, "module-remap-source", "master=vspk.monitor source_name=vmic")
	if err != nil {
		t.Fatalf("LoadModule(remap-source): %v", err)
	}
	if idx1 == idx2 {
		t.Fatalf("expected distinct module indices, got %d and %d", idx1, idx2)
	}

	if len(srv.calls) != 2 {
		t.Fatalf("expected 2 recorded LoadModule calls, got %d: %+v", len(srv.calls), srv.calls)
	}
	if srv.calls[0].name != "module-null-sink" || srv.calls[0].args != "sink_name=vspk" {
		t.Fatalf("unexpected first call: %+v", srv.calls[0])
	}
	if srv.calls[1].name != "module-remap-source" || srv.calls[1].args != "master=vspk.monitor source_name=vmic" {
		t.Fatalf("unexpected second call: %+v", srv.calls[1])
	}
}

// TestPulse_Dial_AnonymousAuth_NoCookieFile proves the anonymous-auth
// fallback path: an empty/absent cookiePath sends a zero-length cookie, and
// the fake server (configured to accept any cookie, including empty) still
// completes the handshake.
func TestPulse_Dial_AnonymousAuth_NoCookieFile(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "native")

	srv := startFakePAServer(t, sockPath, nil) // nil == accept any cookie
	_ = srv

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, sockPath, filepath.Join(dir, "does-not-exist-cookie"))
	if err != nil {
		t.Fatalf("Dial (anonymous): %v", err)
	}
	defer client.Close()
}

// TestPulse_ProtocolError_Decoded proves a COMMAND_ERROR reply surfaces as a
// typed *ProtocolError rather than a generic decode failure.
func TestPulse_ProtocolError_Decoded(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "native")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		for {
			hdr := make([]byte, 20)
			if _, readErr := io.ReadFull(conn, hdr); readErr != nil {
				return
			}
			n := binary.BigEndian.Uint32(hdr[0:4])
			payload := make([]byte, n)
			if _, readErr := io.ReadFull(conn, payload); readErr != nil {
				return
			}
			br := bytes.NewReader(payload)
			var cmdTag, cmdVal, seqTag, seqVal uint32
			mustReadTaggedU32(br, &cmdTag, &cmdVal)
			mustReadTaggedU32(br, &seqTag, &seqVal)
			// Reject every request with COMMAND_ERROR code 42.
			reply := replyHeader(0 /* commandError */, seqVal)
			reply = appendTaggedU32(reply, 42)
			writeFramedForTest(conn, reply)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = Dial(ctx, sockPath, "")
	var protoErr *ProtocolError
	if !errors.As(err, &protoErr) {
		t.Fatalf("expected *ProtocolError from Dial, got %v (%T)", err, err)
	}
	if protoErr.Code != 42 {
		t.Fatalf("expected error code 42, got %d", protoErr.Code)
	}
}
