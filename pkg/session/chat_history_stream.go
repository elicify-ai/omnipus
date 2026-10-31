// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Streamed display reads (chat-history paging spec, A2.8, founder decision
// 2026-10-30): only what the user sees enters memory for chat display. A
// canonical record is streamed from disk with a running SHA-256; every JSON
// string whose canonical encoding exceeds the inline threshold is replaced by
// a bounded stand-in before the record is decoded; the private model_message
// member is skipped without being kept (it reaches the reduced document as
// null). The scanner mirrors encoding/json (v1) decoding exactly: escapes,
// surrogate pairs, invalid UTF-8 coerced to U+FFFD, and its syntax checks. A
// record it cannot read fails closed (errHistoryStreamUnreadable), including
// after any non-EOF read error from the source (sticky, never retried).

var (
	// historyStandInThreshold: a string whose canonical encoding, quotes
	// included, is larger becomes a stand-in. Equal to the inline result
	// threshold, so any result holding one is shown as a preview.
	historyStandInThreshold = ChatHistoryInlineResultMaxBytes
	// historyStandInDecodedKeep covers a 64 KiB text prefix plus the byte
	// that decides its UTF-8 cut.
	historyStandInDecodedKeep = ChatHistoryTextDisplayMaxBytes + utf8.UTFMax
	// historyStandInEncodedKeep covers a 4 KiB result preview and its cut.
	historyStandInEncodedKeep = ChatHistoryResultPreviewBytes + utf8.UTFMax
)

// historyStreamMaxDepth matches encoding/json's nesting limit.
const historyStreamMaxDepth = 10000

var (
	errHistoryStreamUnreadable  = errors.New("history display stream: unreadable record")
	errHistoryStandInUnresolved = errors.New("history display stream: value shown whole")
	// errHistoryStreamPrivateShape: the skipped model_message member does
	// not decode as a ModelPayload, which the full read also refuses.
	errHistoryStreamPrivateShape = errors.New("history display stream: invalid private member")
)

// historyStickyReader remembers the first non-EOF read error of its source
// and returns it for every later read, so a transient storage failure can
// never be skipped over by a lookahead that resumes reading afterwards
// (#1452 SEC-R5-2). bufio.Reader reports a source error only once.
type historyStickyReader struct {
	r   io.Reader
	err error
}

func (s *historyStickyReader) Read(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n, err := s.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		s.err = err
	}
	return n, err
}

// historyLargeString is the bounded stand-in of one large JSON string.
type historyLargeString struct {
	decodedPrefix  []byte // first historyStandInDecodedKeep decoded bytes
	encodedPrefix  []byte // first historyStandInEncodedKeep bytes of the encoding, after the opening quote
	decodedLen     int
	encodedLen     int // canonical encoding size, quotes included
	namesSessionID bool
}

// whole reports that the stand-in still holds the complete decoded value.
func (v *historyLargeString) whole() bool { return len(v.decodedPrefix) == v.decodedLen }

// historyStandIns is the stand-in table of one projected entry and its
// dependencies. A token is the per-table random nonce plus the SHA-256 of the
// decoded value, so equal values get equal tokens and the reducer's string
// comparisons (call ids, targets) keep their exact outcome; saved data cannot
// impersonate a token because it does not know the nonce.
type historyStandIns struct {
	prefix string
	values map[string]*historyLargeString
}

func newHistoryStandIns() *historyStandIns {
	var n [12]byte
	_, _ = rand.Read(n[:]) // crypto/rand.Read never returns an error
	return &historyStandIns{prefix: "\x00hsi:" + hex.EncodeToString(n[:]) + ":", values: map[string]*historyLargeString{}}
}

func (s *historyStandIns) token(sum []byte) string {
	return s.prefix + hex.EncodeToString(sum) + "\x00"
}

func (s *historyStandIns) add(sum []byte, v historyLargeString) string {
	t := s.token(sum)
	if _, ok := s.values[t]; !ok {
		s.values[t] = &v
	}
	return t
}

func (s *historyStandIns) lookup(v string) (*historyLargeString, bool) {
	if s == nil || len(s.values) == 0 || !strings.HasPrefix(v, s.prefix) {
		return nil, false
	}
	l, ok := s.values[v]
	return l, ok
}

// same reports whether a decoded value (possibly a token) equals the real string want.
func (s *historyStandIns) same(decoded, want string) bool {
	if decoded == want {
		return true
	}
	if _, ok := s.lookup(decoded); !ok {
		return false
	}
	sum := sha256.Sum256([]byte(want))
	return decoded == s.token(sum[:])
}

// inValue reports whether any stand-in appears anywhere inside v.
func (s *historyStandIns) inValue(v any) bool {
	if s == nil || len(s.values) == 0 {
		return false
	}
	switch x := v.(type) {
	case string:
		_, ok := s.lookup(x)
		return ok
	case map[string]any:
		for _, e := range x {
			if s.inValue(e) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if s.inValue(e) {
				return true
			}
		}
	}
	return false
}

// unresolvedIn reports a stand-in left anywhere in an encoded value.
func (s *historyStandIns) unresolvedIn(encoded []byte) bool {
	if s == nil || len(s.values) == 0 {
		return false
	}
	// The NUL is escaped as \u0000 in JSON; the nonce part is literal.
	return bytes.Contains(encoded, []byte(s.prefix[1:]))
}

type historyStreamScanner struct {
	r      *bufio.Reader
	out    *bytes.Buffer
	ins    *historyStandIns
	last   byte
	failed bool // a lookahead met a non-EOF read error
	// privateShapeBad: a skipped model_message member does not decode as
	// a ModelPayload. Syntax is still the scanner's only verdict.
	privateShapeBad bool
}

// peek is bufio Peek whose short result is only acceptable at end of input:
// any other read error fails the record closed.
func (s *historyStreamScanner) peek(n int) ([]byte, error) {
	b, err := s.r.Peek(n)
	if err != nil && !errors.Is(err, io.EOF) {
		s.failed = true
		return nil, errHistoryStreamUnreadable
	}
	return b, nil
}

func (s *historyStreamScanner) next() (byte, error) {
	c, err := s.r.ReadByte()
	if err != nil {
		return 0, errHistoryStreamUnreadable
	}
	s.last = c
	return c, nil
}

func historySpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func (s *historyStreamScanner) skipSpace() (byte, error) {
	for {
		c, err := s.next()
		if err != nil {
			return 0, err
		}
		if !historySpace(c) {
			return c, nil
		}
	}
}

// historyStreamRecord reduces one framed canonical record (JSON object plus
// newline) to a small JSON document with stand-ins.
func historyStreamRecord(r io.Reader, ins *historyStandIns) ([]byte, error) {
	reduced, _, err := historyStreamRecordChecked(r, ins)
	return reduced, err
}

// historyStreamRecordChecked is historyStreamRecord that also reports
// whether every skipped model_message member decodes as a ModelPayload, the
// type check the full read's decode applies to it.
func historyStreamRecordChecked(r io.Reader, ins *historyStandIns) (reduced []byte, privateOK bool, err error) {
	src := &historyStickyReader{r: r}
	s := &historyStreamScanner{r: bufio.NewReaderSize(src, 64<<10), out: &bytes.Buffer{}, ins: ins}
	reduced, err = s.record()
	if err != nil || s.failed || src.err != nil {
		return nil, false, errHistoryStreamUnreadable
	}
	return reduced, !s.privateShapeBad, nil
}

func (s *historyStreamScanner) record() ([]byte, error) {
	c, err := s.skipSpace()
	if err != nil {
		return nil, err
	}
	if err = s.value(c, 0, true, false); err != nil {
		return nil, err
	}
	for {
		c, err := s.r.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil || !historySpace(c) {
			return nil, errHistoryStreamUnreadable
		}
		s.last = c
	}
	if s.last != '\n' {
		return nil, errHistoryStreamUnreadable // unframed record
	}
	return s.out.Bytes(), nil
}

// value scans one JSON value. private marks the model_message subtree, which
// display never reads: it is checked, but nothing of it is kept.
func (s *historyStreamScanner) value(c byte, depth int, top, private bool) error {
	switch c {
	case '{':
		return s.object(depth+1, top, private)
	case '[':
		return s.array(depth+1, private)
	case '"':
		return s.str(false, private, nil)
	default:
		return s.literal(c)
	}
}

func (s *historyStreamScanner) object(depth int, top, private bool) error {
	if depth > historyStreamMaxDepth {
		return errHistoryStreamUnreadable
	}
	s.out.WriteByte('{')
	c, err := s.skipSpace()
	if err != nil {
		return err
	}
	if c == '}' {
		s.out.WriteByte('}')
		return nil
	}
	for first := true; ; first = false {
		if !first {
			s.out.WriteByte(',')
		}
		if c != '"' {
			return errHistoryStreamUnreadable
		}
		var key []byte
		if err = s.str(true, false, &key); err != nil {
			return err
		}
		if c, err = s.skipSpace(); err != nil {
			return err
		}
		if c != ':' {
			return errHistoryStreamUnreadable
		}
		s.out.WriteByte(':')
		if c, err = s.skipSpace(); err != nil {
			return err
		}
		// encoding/json matches struct fields case-insensitively.
		if top && strings.EqualFold(string(key), "model_message") {
			err = s.skipPrivate(c, depth)
		} else {
			err = s.value(c, depth, false, private)
		}
		if err != nil {
			return err
		}
		if c, err = s.skipSpace(); err != nil {
			return err
		}
		switch c {
		case '}':
			s.out.WriteByte('}')
			return nil
		case ',':
			if c, err = s.skipSpace(); err != nil {
				return err
			}
		default:
			return errHistoryStreamUnreadable
		}
	}
}

// skipPrivate checks the private model_message value with the same syntax
// rules and the same ModelPayload type decoding as the full read, then
// writes null in its place: no string of it is kept, it creates no stand-in,
// and its checked skeleton (structure, keys and literals, every string empty)
// is dropped before the next member (A2.8; #1452 SEC-R5-1).
func (s *historyStreamScanner) skipPrivate(c byte, depth int) error {
	out := s.out
	s.out = &bytes.Buffer{}
	err := s.value(c, depth, false, true)
	skeleton := s.out.Bytes()
	s.out = out
	if err != nil {
		return err
	}
	var payload *ModelPayload
	if json.Unmarshal(skeleton, &payload) != nil {
		s.privateShapeBad = true
	}
	s.out.WriteString("null")
	return nil
}

func (s *historyStreamScanner) array(depth int, private bool) error {
	if depth > historyStreamMaxDepth {
		return errHistoryStreamUnreadable
	}
	s.out.WriteByte('[')
	c, err := s.skipSpace()
	if err != nil {
		return err
	}
	if c == ']' {
		s.out.WriteByte(']')
		return nil
	}
	for first := true; ; first = false {
		if !first {
			s.out.WriteByte(',')
		}
		if err = s.value(c, depth, false, private); err != nil {
			return err
		}
		if c, err = s.skipSpace(); err != nil {
			return err
		}
		switch c {
		case ']':
			s.out.WriteByte(']')
			return nil
		case ',':
			if c, err = s.skipSpace(); err != nil {
				return err
			}
		default:
			return errHistoryStreamUnreadable
		}
	}
}

// literal copies true/false/null or a number, checked with JSON's grammar.
// A number is copied verbatim, so it decodes exactly as in the full read.
func (s *historyStreamScanner) literal(c byte) error {
	for _, word := range []string{"true", "false", "null"} {
		if c != word[0] {
			continue
		}
		for i := 1; i < len(word); i++ {
			if b, err := s.next(); err != nil || b != word[i] {
				return errHistoryStreamUnreadable
			}
		}
		s.out.WriteString(word)
		return s.literalEnd()
	}
	num := []byte{}
	eof := false
	take := func() {
		num = append(num, c)
		b, err := s.r.ReadByte()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.failed = true // a read error never ends a number
			}
			eof = true
			c = 0
			return
		}
		c = b
	}
	digits := func() int {
		n := 0
		for !eof && c >= '0' && c <= '9' {
			take()
			n++
		}
		return n
	}
	if c == '-' {
		take()
	}
	switch {
	case !eof && c == '0':
		take()
	case !eof && c >= '1' && c <= '9':
		digits()
	default:
		return errHistoryStreamUnreadable
	}
	if !eof && c == '.' {
		take()
		if digits() == 0 {
			return errHistoryStreamUnreadable
		}
	}
	if !eof && (c == 'e' || c == 'E') {
		take()
		if !eof && (c == '+' || c == '-') {
			take()
		}
		if digits() == 0 {
			return errHistoryStreamUnreadable
		}
	}
	if s.failed {
		return errHistoryStreamUnreadable
	}
	if !eof {
		if err := s.r.UnreadByte(); err != nil {
			return errHistoryStreamUnreadable
		}
	}
	s.last = num[len(num)-1]
	s.out.Write(num)
	return s.literalEnd()
}

// literalEnd rejects a literal glued to another token (e.g. "truex", "01").
func (s *historyStreamScanner) literalEnd() error {
	b, err := s.peek(1)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil // end of input is checked by the caller
	}
	switch b[0] {
	case ' ', '\t', '\n', '\r', ',', '}', ']':
		return nil
	}
	return errHistoryStreamUnreadable
}

// historyEncodedRune appends one decoded rune's canonical encoding
// (encoding/json appendString with HTML escaping). Decoded text is always
// valid UTF-8: invalid input bytes were already replaced by U+FFFD.
func historyEncodedRune(dst []byte, r rune, raw []byte) []byte {
	const hexDigits = "0123456789abcdef"
	if r < utf8.RuneSelf {
		b := byte(r)
		switch {
		case b == '\\' || b == '"':
			return append(dst, '\\', b)
		case b == '\b':
			return append(dst, '\\', 'b')
		case b == '\f':
			return append(dst, '\\', 'f')
		case b == '\n':
			return append(dst, '\\', 'n')
		case b == '\r':
			return append(dst, '\\', 'r')
		case b == '\t':
			return append(dst, '\\', 't')
		case b < 0x20 || b == '<' || b == '>' || b == '&':
			return append(dst, '\\', 'u', '0', '0', hexDigits[b>>4], hexDigits[b&0xF])
		}
		return append(dst, b)
	}
	if r == '\u2028' || r == '\u2029' {
		return append(dst, '\\', 'u', '2', '0', '2', hexDigits[r&0xF])
	}
	return append(dst, raw...)
}

// plainRun returns the buffered bytes, up to the first byte that needs
// per-character handling inside a JSON string.
func (s *historyStreamScanner) plainRun() []byte {
	buf, _ := s.r.Peek(s.r.Buffered())
	for i, b := range buf {
		if b < 0x20 || b >= utf8.RuneSelf || b == '"' || b == '\\' || b == '<' || b == '>' || b == '&' {
			return buf[:i]
		}
	}
	return buf
}

// historyHex4 is encoding/json getu4: a \uXXXX escape's value, or -1.
func historyHex4(b []byte) rune {
	if len(b) < 6 || b[0] != '\\' || b[1] != 'u' {
		return -1
	}
	var r rune
	for _, c := range b[2:6] {
		switch {
		case '0' <= c && c <= '9':
			c -= '0'
		case 'a' <= c && c <= 'f':
			c = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			c = c - 'A' + 10
		default:
			return -1
		}
		r = r*16 + rune(c)
	}
	return r
}

// str decodes one string after its opening quote, with encoding/json
// unquote semantics. A key is returned whole in *key (bounded); a value is
// emitted whole or, above the threshold, as a stand-in token.
func (s *historyStreamScanner) str(isKey, private bool, key *[]byte) error {
	const pattern = `"session_id"`
	var (
		decoded, encoded []byte
		decodedLen       int
		encodedLen       = 2
		match            int // progress through pattern in the decoded text
		runeBuf          [utf8.UTFMax]byte
		seqBuf           [utf8.UTFMax]byte
		encBuf           []byte
		sum              hash.Hash
	)
	keep, encKeep := historyStandInDecodedKeep, historyStandInEncodedKeep
	if private {
		// A private string is checked, but nothing of it is kept.
		keep, encKeep = 0, 0
	}
	for {
		// Fast path: a buffered run of plain printable ASCII decodes and
		// encodes to itself byte for byte (no quote, backslash, control,
		// HTML-escaped or non-ASCII byte), so it is taken in one step.
		if run := s.plainRun(); len(run) > 0 {
			before := decodedLen
			decodedLen += len(run)
			if len(decoded) < keep {
				decoded = append(decoded, run[:min(len(run), keep-len(decoded))]...)
			}
			if isKey && len(decoded) < decodedLen {
				return errHistoryStreamUnreadable // keys stay small
			}
			encodedLen += len(run)
			if len(encoded) < encKeep {
				encoded = append(encoded, run[:min(len(run), encKeep-len(encoded))]...)
			}
			switch {
			case sum != nil:
				sum.Write(run)
			case encodedLen > historyStandInThreshold && !isKey && !private:
				// Every earlier decoded byte is held: decoded <= encoded <= threshold.
				sum = sha256.New()
				sum.Write(decoded[:before])
				sum.Write(run)
			}
			// The run holds no '"', so a match can only continue, never start.
			for i := 0; i < len(run) && match > 0 && match < len(pattern); i++ {
				if run[i] == pattern[match] {
					match++
				} else {
					match = 0
				}
			}
			s.last = run[len(run)-1]
			if _, err := s.r.Discard(len(run)); err != nil {
				return errHistoryStreamUnreadable
			}
			continue
		}
		c, err := s.next()
		if err != nil {
			return err
		}
		var r rune
		switch {
		case c == '"':
			goto done
		case c == '\\':
			e, err := s.next()
			if err != nil {
				return err
			}
			switch e {
			case '"', '\\', '/':
				r = rune(e)
			case 'b':
				r = '\b'
			case 'f':
				r = '\f'
			case 'n':
				r = '\n'
			case 'r':
				r = '\r'
			case 't':
				r = '\t'
			case 'u':
				esc := [6]byte{'\\', 'u'}
				for i := 2; i < 6; i++ {
					if esc[i], err = s.next(); err != nil {
						return err
					}
				}
				if r = historyHex4(esc[:]); r < 0 {
					return errHistoryStreamUnreadable
				}
				if utf16.IsSurrogate(r) {
					peek, err := s.peek(6)
					if err != nil {
						return err
					}
					if dec := utf16.DecodeRune(r, historyHex4(peek)); dec != utf8.RuneError {
						last := peek[5]
						if _, err := s.r.Discard(6); err != nil {
							return errHistoryStreamUnreadable
						}
						s.last = last
						r = dec
					} else {
						r = utf8.RuneError
					}
				}
			default:
				return errHistoryStreamUnreadable
			}
		case c < 0x20:
			return errHistoryStreamUnreadable
		case c < utf8.RuneSelf:
			r = rune(c)
		default:
			seq := append(seqBuf[:0], c)
			more, err := s.peek(utf8.UTFMax - 1)
			if err != nil {
				return err
			}
			seq = append(seq, more...)
			var size int
			r, size = utf8.DecodeRune(seq)
			if size > 1 {
				last := seq[size-1]
				if _, err := s.r.Discard(size - 1); err != nil {
					return errHistoryStreamUnreadable
				}
				s.last = last
			} // size 1: an invalid byte becomes U+FFFD, as in encoding/json
		}
		raw := runeBuf[:utf8.EncodeRune(runeBuf[:], r)]
		decodedLen += len(raw)
		if len(decoded) < keep {
			decoded = append(decoded, raw...)
		} else if isKey {
			return errHistoryStreamUnreadable // keys stay small
		}
		encBuf = historyEncodedRune(encBuf[:0], r, raw)
		encodedLen += len(encBuf)
		if len(encoded) < encKeep {
			encoded = append(encoded, encBuf...)
			if len(encoded) > encKeep {
				encoded = encoded[:encKeep]
			}
		}
		switch {
		case sum != nil:
			sum.Write(raw)
		case encodedLen > historyStandInThreshold && !isKey && !private:
			// From here on the value is a stand-in; hash it whole. Every
			// decoded byte so far is still held: decoded <= encoded <= threshold.
			sum = sha256.New()
			sum.Write(decoded[:decodedLen])
		}
		for _, b := range raw {
			if match == len(pattern) {
				break
			}
			switch b {
			case pattern[match]:
				match++
			case '"':
				match = 1
			default:
				match = 0
			}
		}
	}
done:
	if isKey {
		*key = decoded
	}
	if private {
		s.out.WriteString(`""`) // the checked skeleton keeps the type, not the text
		return nil
	}
	if sum == nil {
		// The whole decoded value is held: decoded <= encoded <= threshold.
		b, _ := json.Marshal(string(decoded))
		s.out.Write(b)
		return nil
	}
	token := s.ins.add(sum.Sum(nil), historyLargeString{decodedPrefix: decoded, encodedPrefix: encoded, decodedLen: decodedLen, encodedLen: encodedLen, namesSessionID: match == len(pattern)})
	b, _ := json.Marshal(token)
	s.out.Write(b)
	return nil
}

// historyStreamArchive is historyReadArchive for the display reader: the
// record streams through the digest and the scanner and is decoded from the
// reduced document. Read events and metrics are identical to the full read.
func historyStreamArchive(ctx context.Context, dir string, r historyRecord, cut ChatHistoryReadCut, scope, purpose string, ins *historyStandIns) (rec ArchiveRecord, err error) {
	if err = ctx.Err(); err != nil {
		return rec, err
	}
	path, err := historyArchivePath(dir, r.Address.PartitionKey)
	if err != nil {
		return rec, err
	}
	off, length := r.Address.ByteOffset, r.Length
	if off < 0 || length < 0 || length > historyFrameBound {
		return rec, errors.New("invalid history read bounds")
	}
	e := chatHistoryReadEvent{SessionID: cut.sessionID, Scope: scope, Purpose: purpose, Cut: cut, Stream: "archive", Address: &r.Address}
	f, err := historyStreamOpenArchive(path)
	if err != nil {
		return rec, err
	}
	e.Phase = "record_opened"
	e.Offset = off
	defer func() {
		closeErr := f.Close()
		err = errors.Join(err, closeErr)
		e.Phase = "record_closed"
		e.Bytes = 0
		if chatHistoryReadHook != nil {
			err = errors.Join(err, chatHistoryReadHook(ctx, e))
		}
	}()
	if err = historyReadEvent(ctx, e); err != nil {
		return rec, err
	}
	h := sha256.New()
	counted := &historyCountingReader{r: io.TeeReader(io.NewSectionReader(f, off, length), h)}
	reduced, privateOK, scanErr := historyStreamRecordChecked(counted, ins)
	if _, drainErr := io.Copy(io.Discard, counted); drainErr != nil {
		return rec, drainErr
	}
	n := counted.n
	e.Phase = "record_read"
	e.Bytes = n
	historyCountRead(ctx, e, n)
	if err = historyReadEvent(ctx, e); err != nil {
		return rec, err
	}
	if n != length {
		return rec, io.ErrUnexpectedEOF
	}
	if hex.EncodeToString(h.Sum(nil)) != r.Digest {
		return rec, errors.New("history canonical record changed")
	}
	if scanErr != nil {
		return rec, scanErr
	}
	if !privateOK {
		// The full read's decode refuses the same record the same way.
		return rec, historyBuildFailure("corrupt_archive", errHistoryStreamPrivateShape)
	}
	if err = json.Unmarshal(reduced, &rec); err != nil {
		return rec, historyBuildFailure("corrupt_archive", err)
	}
	if err = historyValidateArchive(rec); err != nil {
		return rec, err
	}
	if !ins.same(rec.ID, r.Address.EntryID) {
		return rec, errors.New("history checked address mismatch")
	}
	return rec, nil
}

// historyStreamFile is the opened canonical partition the display read
// streams one record from.
type historyStreamFile interface {
	io.ReaderAt
	io.Closer
}

// historyStreamOpenArchive opens a canonical partition for the streamed
// display read. Production always uses os.Open; a test may replace it to
// inject read faults at the real archive boundary.
var historyStreamOpenArchive = func(path string) (historyStreamFile, error) {
	return os.Open(path)
}

type historyCountingReader struct {
	r io.Reader
	n int64
}

func (c *historyCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
