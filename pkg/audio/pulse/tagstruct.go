// Package pulse is a minimal, pure-Go client for the PulseAudio native
// protocol — no libpulse, no CGo, no `pactl` subprocess. It implements just
// enough of the wire format to authenticate against a PulseAudio server over
// its Unix control socket and issue LoadModule requests.
//
// This is the "NoiseTorch pattern" cited by ADR-044-spike-results.md §S-3:
// github.com/noisetorch/pulseaudio (itself a fork of github.com/mafik/pulse)
// proves a pure-Go client can drive LoadModule against a real PulseAudio
// server in production. The wire format implemented here (packet framing,
// tagstruct encoding, command numbers) was verified against that reference
// implementation's source (client.go, command.go, format.go, module.go) —
// there is no other authoritative public spec for the native protocol; the
// PulseAudio project deliberately does not document it and advises against
// reimplementing a *server*. A client is a much narrower, safe target.
//
// Only the tag types Omnipus's audio sidecar (pkg/tools/browser/audiosink_*)
// needs are implemented: uint32, string, arbitrary (length-prefixed bytes),
// and proplist (needed for SET_CLIENT_NAME). This is intentionally not a
// general-purpose PulseAudio client.
package pulse

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// tagType is the single-byte type discriminator prefixing every field in a
// PulseAudio tagstruct (the tagged binary encoding used for every command and
// reply payload in the native protocol).
type tagType byte

const (
	tagInvalid    tagType = 0
	tagString     tagType = 't'
	tagStringNull tagType = 'N'
	tagUint32     tagType = 'L'
	tagArbitrary  tagType = 'x'
	tagPropList   tagType = 'P'
)

// tagWriter builds a PulseAudio tagstruct payload.
type tagWriter struct {
	buf bytes.Buffer
}

func (w *tagWriter) putUint32(v uint32) {
	w.buf.WriteByte(byte(tagUint32))
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	w.buf.Write(b[:])
}

// putString writes a PA_TAG_STRING: 't' + raw bytes + NUL terminator. An
// empty string is still encoded as 't' + NUL (a zero-length C string) — PA
// distinguishes "empty string" (tagString) from "absent/null string"
// (tagStringNull, produced only by putNullString), matching how PulseAudio's
// own tagstruct_put_string treats "" vs NULL in the C API.
func (w *tagWriter) putString(s string) {
	w.buf.WriteByte(byte(tagString))
	w.buf.WriteString(s)
	w.buf.WriteByte(0)
}

// putArbitrary writes a PA_TAG_ARBITRARY: 'x' + uint32 length + raw bytes.
func (w *tagWriter) putArbitrary(b []byte) {
	w.buf.WriteByte(byte(tagArbitrary))
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(b)))
	w.buf.Write(lb[:])
	w.buf.Write(b)
}

// putPropList writes a PA_TAG_PROPLIST: 'P', then per non-empty entry
// (string key, uint32 length-of-value-with-NUL, arbitrary value+NUL),
// terminated by 'N' (PA_TAG_STRING_NULL, PulseAudio's proplist end marker).
// Verified byte-for-byte against noisetorch/pulseaudio's bwrite propList
// branch (client.go / format.go).
func (w *tagWriter) putPropList(props map[string]string) {
	w.buf.WriteByte(byte(tagPropList))
	for k, v := range props {
		if v == "" {
			continue
		}
		w.putString(k)
		val := append([]byte(v), 0)
		w.putUint32(uint32(len(val)))
		w.putArbitrary(val)
	}
	w.buf.WriteByte(byte(tagStringNull))
}

func (w *tagWriter) bytes() []byte { return w.buf.Bytes() }

// tagReader decodes a PulseAudio tagstruct payload sequentially.
type tagReader struct {
	r *bytes.Reader
}

func newTagReader(payload []byte) *tagReader {
	return &tagReader{r: bytes.NewReader(payload)}
}

func (r *tagReader) getUint32(out *uint32) error {
	t, err := r.r.ReadByte()
	if err != nil {
		return fmt.Errorf("tagstruct: read uint32 tag: %w", err)
	}
	if tagType(t) != tagUint32 {
		return fmt.Errorf("tagstruct: expected uint32 tag 'L', got %q", t)
	}
	var b [4]byte
	if _, err := io.ReadFull(r.r, b[:]); err != nil {
		return fmt.Errorf("tagstruct: read uint32 value: %w", err)
	}
	*out = binary.BigEndian.Uint32(b[:])
	return nil
}

func (r *tagReader) getString(out *string) error {
	t, err := r.r.ReadByte()
	if err != nil {
		return fmt.Errorf("tagstruct: read string tag: %w", err)
	}
	switch tagType(t) {
	case tagStringNull:
		*out = ""
		return nil
	case tagString:
		var sb bytes.Buffer
		for {
			c, err := r.r.ReadByte()
			if err != nil {
				return fmt.Errorf("tagstruct: read string bytes: %w", err)
			}
			if c == 0 {
				break
			}
			sb.WriteByte(c)
		}
		*out = sb.String()
		return nil
	default:
		return fmt.Errorf("tagstruct: expected string tag 't'/'N', got %q", t)
	}
}

func (r *tagReader) getArbitrary(out *[]byte) error {
	t, err := r.r.ReadByte()
	if err != nil {
		return fmt.Errorf("tagstruct: read arbitrary tag: %w", err)
	}
	if tagType(t) != tagArbitrary {
		return fmt.Errorf("tagstruct: expected arbitrary tag 'x', got %q", t)
	}
	var lb [4]byte
	if _, err := io.ReadFull(r.r, lb[:]); err != nil {
		return fmt.Errorf("tagstruct: read arbitrary length: %w", err)
	}
	n := binary.BigEndian.Uint32(lb[:])
	b := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(r.r, b); err != nil {
			return fmt.Errorf("tagstruct: read arbitrary payload: %w", err)
		}
	}
	*out = b
	return nil
}

// rest returns every remaining unread byte (used once the caller has decoded
// the fixed prefix of a reply it cares about — e.g. LoadModule's uint32
// index — and doesn't need the rest of the payload).
func (r *tagReader) rest() []byte {
	b, _ := io.ReadAll(r.r)
	return b
}
