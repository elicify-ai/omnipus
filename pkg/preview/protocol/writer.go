// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// FrameWriter emits protocol v1 frames on the program side (the extract-text
// mode of the omnipus binary). It enforces the same per-frame ceilings the
// reader checks and counts frames for DONE, so a program built on it cannot
// produce output the gateway would reject for size or count.
type FrameWriter struct {
	w      io.Writer
	frames uint32
	text   int
}

// NewFrameWriter wraps w.
func NewFrameWriter(w io.Writer) *FrameWriter { return &FrameWriter{w: w} }

func (fw *FrameWriter) frame(typ byte, payload []byte) error {
	limit, ok := maxPayload(typ)
	if !ok {
		return fmt.Errorf("protocol: unknown frame type %d", typ)
	}
	if uint64(len(payload)) > uint64(limit) {
		return fmt.Errorf("protocol: frame type %d payload %d exceeds %d", typ, len(payload), limit)
	}
	var h [frameHeaderBytes]byte
	h[0] = typ
	binary.LittleEndian.PutUint32(h[1:5], uint32(len(payload)))
	if _, err := fw.w.Write(h[:]); err != nil {
		return err
	}
	if _, err := fw.w.Write(payload); err != nil {
		return err
	}
	fw.frames++
	return nil
}

// Meta writes the META frame.
func (fw *FrameWriter) Meta(m Meta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return fw.frame(FrameMeta, b)
}

// Text writes one TEXT frame. Text that would exceed the per-frame or
// per-call ceiling is cut at a UTF-8 boundary; the returned bool reports a cut.
func (fw *FrameWriter) Text(page uint32, text string) (bool, error) {
	cut := false
	room := MaxTextTotalBytes - fw.text
	limit := MaxTextFrameBytes - textHeaderBytes
	if room < limit {
		limit = room
	}
	if limit < 0 {
		limit = 0
	}
	if len(text) > limit {
		text = truncateUTF8(text, limit)
		cut = true
	}
	payload := make([]byte, textHeaderBytes+len(text))
	binary.LittleEndian.PutUint32(payload[0:4], page)
	copy(payload[textHeaderBytes:], text)
	fw.text += len(text)
	return cut, fw.frame(FrameText, payload)
}

// Error writes the terminal ERROR frame.
func (fw *FrameWriter) Error(code, message string) error {
	if len(message) > MaxErrorBytes/2 {
		message = truncateUTF8(message, MaxErrorBytes/2)
	}
	b, err := json.Marshal(PeerError{Code: code, Message: message})
	if err != nil {
		return err
	}
	return fw.frame(FrameError, b)
}

// Done writes the terminal DONE frame; its count includes itself.
func (fw *FrameWriter) Done() error {
	var b [doneFrameBytes]byte
	binary.LittleEndian.PutUint32(b[:], fw.frames+1)
	return fw.frame(FrameDone, b[:])
}

func truncateUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
