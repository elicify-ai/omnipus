package pulse

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestPulse_Tagstruct_RoundTrip exercises the tagstruct write/read pair for
// every field type this client uses (uint32, string, arbitrary bytes),
// including boundary values (0, max uint32, empty string, empty arbitrary
// blob) — the framing-level correctness the audio-sidecar epic component
// (Wave 1 / C) is explicitly required to prove hermetically.
func TestPulse_Tagstruct_RoundTrip(t *testing.T) {
	var w tagWriter
	w.putUint32(0)
	w.putUint32(0xDEADBEEF)
	w.putUint32(0xFFFFFFFF)
	w.putString("module-null-sink")
	w.putString("") // empty (but present) string
	w.putArbitrary([]byte{1, 2, 3, 4})
	w.putArbitrary(nil) // zero-length arbitrary blob must round-trip too

	r := newTagReader(w.bytes())

	var u1, u2, u3 uint32
	if err := r.getUint32(&u1); err != nil {
		t.Fatalf("getUint32(1): %v", err)
	}
	if err := r.getUint32(&u2); err != nil {
		t.Fatalf("getUint32(2): %v", err)
	}
	if err := r.getUint32(&u3); err != nil {
		t.Fatalf("getUint32(3): %v", err)
	}
	if u1 != 0 || u2 != 0xDEADBEEF || u3 != 0xFFFFFFFF {
		t.Fatalf("uint32 round-trip mismatch: got %d, %#x, %#x", u1, u2, u3)
	}

	var s1, s2 string
	if err := r.getString(&s1); err != nil {
		t.Fatalf("getString(1): %v", err)
	}
	if err := r.getString(&s2); err != nil {
		t.Fatalf("getString(2): %v", err)
	}
	if s1 != "module-null-sink" || s2 != "" {
		t.Fatalf("string round-trip mismatch: got %q, %q", s1, s2)
	}

	var a1, a2 []byte
	if err := r.getArbitrary(&a1); err != nil {
		t.Fatalf("getArbitrary(1): %v", err)
	}
	if err := r.getArbitrary(&a2); err != nil {
		t.Fatalf("getArbitrary(2): %v", err)
	}
	if !bytes.Equal(a1, []byte{1, 2, 3, 4}) {
		t.Fatalf("arbitrary round-trip mismatch: got %v", a1)
	}
	if len(a2) != 0 {
		t.Fatalf("expected zero-length arbitrary blob, got %v", a2)
	}
}

// TestPulse_Tagstruct_StringNull confirms a null string (as PulseAudio's
// proplist terminator uses) decodes to "" via the dedicated stringNullTag,
// distinct from an explicit empty string — both are legal on the wire but
// use different tag bytes ('N' vs 't').
func TestPulse_Tagstruct_StringNull(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(byte(tagStringNull))
	r := newTagReader(buf.Bytes())
	var s string
	if err := r.getString(&s); err != nil {
		t.Fatalf("getString: %v", err)
	}
	if s != "" {
		t.Fatalf("expected empty string from stringNullTag, got %q", s)
	}
}

// TestPulse_Tagstruct_WrongTag confirms a type mismatch is a decode error,
// not a silent misparse — protects every caller (auth/load-module reply
// decoding) from quietly accepting a malformed/foreign payload.
func TestPulse_Tagstruct_WrongTag(t *testing.T) {
	var w tagWriter
	w.putString("not-a-uint32")
	r := newTagReader(w.bytes())
	var u uint32
	if err := r.getUint32(&u); err == nil {
		t.Fatal("expected a tag-mismatch error reading a string as uint32, got nil")
	}
}

// TestPulse_Tagstruct_PropList verifies putPropList's exact wire layout
// (key string, uint32 length, arbitrary value+NUL, terminated by
// stringNullTag) against a hand-computed expected byte sequence — verified
// against noisetorch/pulseaudio's bwrite propList branch (client.go /
// format.go), since there is no independent decoder for it in this package
// (Omnipus's client only ever WRITES a proplist, for SET_CLIENT_NAME).
func TestPulse_Tagstruct_PropList(t *testing.T) {
	var w tagWriter
	w.putPropList(map[string]string{"k": "v"})

	var want bytes.Buffer
	want.WriteByte(byte(tagPropList)) // 'P'
	want.WriteByte(byte(tagString))   // 't'
	want.WriteString("k")             // key bytes
	want.WriteByte(0)                 // key NUL
	want.WriteByte(byte(tagUint32))   // 'L'
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], 2) // len("v\x00") == 2
	want.Write(lb[:])
	want.WriteByte(byte(tagArbitrary)) // 'x'
	want.Write(lb[:])                  // arbitrary length, same value
	want.WriteString("v")
	want.WriteByte(0)                   // value NUL
	want.WriteByte(byte(tagStringNull)) // 'N' proplist terminator

	if !bytes.Equal(w.bytes(), want.Bytes()) {
		t.Fatalf("propList encoding mismatch:\n got  %v\n want %v", w.bytes(), want.Bytes())
	}
}

// TestPulse_Tagstruct_PropList_SkipsEmptyValues mirrors the reference
// client's behavior of silently omitting proplist entries with an empty
// value (PulseAudio proplist values must be non-empty).
func TestPulse_Tagstruct_PropList_SkipsEmptyValues(t *testing.T) {
	var w tagWriter
	w.putPropList(map[string]string{"empty": ""})

	var want bytes.Buffer
	want.WriteByte(byte(tagPropList))
	want.WriteByte(byte(tagStringNull))

	if !bytes.Equal(w.bytes(), want.Bytes()) {
		t.Fatalf("expected empty-value entries to be skipped: got %v want %v", w.bytes(), want.Bytes())
	}
}
