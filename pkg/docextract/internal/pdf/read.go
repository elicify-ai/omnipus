// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pdf implements reading of PDF files.
//
// # Overview
//
// PDF is Adobe's Portable Document Format, ubiquitous on the internet.
// A PDF document is a complex data format built on a fairly simple structure.
// This package exposes the simple structure along with some wrappers to
// extract basic information. If more complex information is needed, it is
// possible to extract that information by interpreting the structure exposed
// by this package.
//
// Specifically, a PDF is a data structure built from Values, each of which has
// one of the following Kinds:
//
//	Null, for the null object.
//	Integer, for an integer.
//	Real, for a floating-point number.
//	Bool, for a boolean value.
//	Name, for a name constant (as in /Helvetica).
//	String, for a string constant.
//	Dict, for a dictionary of name-value pairs.
//	Array, for an array of values.
//	Stream, for an opaque data stream and associated header dictionary.
//
// The accessors on Value—Int64, Float64, Bool, Name, and so on—return
// a view of the data as the given type. When there is no appropriate view,
// the accessor returns a zero result. For example, the Name accessor returns
// the empty string if called on a Value v for which v.Kind() != Name.
// Returning zero values this way, especially from the Dict and Array accessors,
// which themselves return Values, makes it possible to traverse a PDF quickly
// without writing any error checking. On the other hand, it means that mistakes
// can go unreported.
//
// The basic structure of the PDF file is exposed as the graph of Values.
//
// Most richer data structures in a PDF file are dictionaries with specific interpretations
// of the name-value pairs. The Font and Page wrappers make the interpretation
// of a specific Value as the corresponding type easier. They are only helpers, though:
// they are implemented only in terms of the Value API and could be moved outside
// the package. Equally important, traversal of other PDF data structures can be implemented
// in other packages as needed.
package pdf

// BUG(rsc): The package is incomplete, although it has been used successfully on some
// large real-world PDF files.

// BUG(rsc): There is no support for closing open PDF files. If you drop all references to a Reader,
// the underlying reader will eventually be garbage collected.

// BUG(rsc): The library makes no attempt at efficiency. A value cache maintained in the Reader
// would probably help significantly.

// BUG(rsc): The support for reading encrypted files is weak.

// BUG(rsc): The Value API does not support error reporting. The intent is to allow users to
// set an error reporting callback in Reader, but that code has not been implemented.

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"encoding/ascii85"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
)

// A Reader is a single PDF file open for reading.
type Reader struct {
	f          io.ReaderAt
	end        int64
	xref       []xref
	trailer    dict
	trailerptr objptr
	key        []byte
	useAES     bool
	lim        *limiter
}

type xref struct {
	ptr      objptr
	inStream bool
	stream   objptr
	offset   int64
}

// NewReader opens a file for reading, using the data in f with the given
// total size. lim bounds every later parse of the document; both budgets must
// be positive. Parsing panics with ErrWorkLimit, ErrTextLimit or an
// ErrStructureLimit error when a bound is reached; callers recover.
func NewReader(f io.ReaderAt, size int64, lim Limits) (*Reader, error) {
	return NewReaderEncrypted(f, size, nil, lim)
}

// NewReaderEncrypted opens a file for reading, using the data in f with the given total size.
// If the PDF is encrypted, NewReaderEncrypted calls pw repeatedly to obtain passwords
// to try. If pw returns the empty string, NewReaderEncrypted stops trying to decrypt
// the file and returns an error.
func NewReaderEncrypted(f io.ReaderAt, size int64, pw func() string, lim Limits) (*Reader, error) {
	if lim.Work <= 0 || lim.Text <= 0 || lim.Time < 0 {
		return nil, fmt.Errorf("pdf: reader limits must be positive (work %d, text %d, time %s)", lim.Work, lim.Text, lim.Time)
	}
	if size < 10 {
		return nil, fmt.Errorf("not a PDF file: too short")
	}
	buf := make([]byte, 10)
	f.ReadAt(buf, 0)
	if !bytes.HasPrefix(buf, []byte("%PDF-1.")) || buf[7] < '0' || buf[7] > '7' || buf[8] != '\r' && buf[8] != '\n' {
		return nil, fmt.Errorf("not a PDF file: invalid header")
	}
	end := size
	const endChunk = 100
	buf = make([]byte, endChunk)
	f.ReadAt(buf, end-endChunk)
	for len(buf) > 0 && buf[len(buf)-1] == '\n' || buf[len(buf)-1] == '\r' {
		buf = buf[:len(buf)-1]
	}
	buf = bytes.TrimRight(buf, "\r\n\t ")
	if !bytes.HasSuffix(buf, []byte("%%EOF")) {
		return nil, fmt.Errorf("not a PDF file: missing %%%%EOF")
	}
	i := findLastLine(buf, "startxref")
	if i < 0 {
		return nil, fmt.Errorf("malformed PDF file: missing final startxref")
	}

	r := &Reader{
		f:   f,
		end: end,
		lim: newLimiter(lim),
	}
	pos := end - endChunk + int64(i)
	b := newBuffer(io.NewSectionReader(f, pos, end-pos), pos, nil)
	if b.readToken() != keyword("startxref") {
		return nil, fmt.Errorf("malformed PDF file: missing startxref")
	}
	startxref, ok := b.readToken().(int64)
	if !ok {
		return nil, fmt.Errorf("malformed PDF file: startxref not followed by integer")
	}
	b = newBuffer(r.section(startxref, r.end-startxref), startxref, r)
	xref, trailerptr, trailer, err := readXref(r, b)
	if err != nil {
		return nil, err
	}
	r.xref = xref
	r.trailer = trailer
	r.trailerptr = trailerptr
	if trailer["Encrypt"] == nil {
		return r, nil
	}
	err = r.initEncrypt("")
	if err == nil {
		return r, nil
	}
	if pw == nil || !errors.Is(err, ErrInvalidPassword) {
		return nil, err
	}
	for {
		next := pw()
		if next == "" {
			break
		}
		if r.initEncrypt(next) == nil {
			return r, nil
		}
	}
	return nil, err
}

// Trailer returns the file's Trailer value.
func (r *Reader) Trailer() Value {
	return Value{r, r.trailerptr, r.trailer}
}

func readXref(r *Reader, b *buffer) ([]xref, objptr, dict, error) {
	tok := b.readToken()
	if tok == keyword("xref") {
		return readXrefTable(r, b)
	}
	if _, ok := tok.(int64); ok {
		b.unreadToken(tok)
		return readXrefStream(r, b)
	}
	return nil, objptr{}, nil, fmt.Errorf("malformed PDF: cross-reference table not found: %v", objfmt(tok))
}

func readXrefStream(r *Reader, b *buffer) ([]xref, objptr, dict, error) {
	obj1 := b.readObject()
	obj, ok := obj1.(objdef)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: cross-reference table not found: %v", objfmt(obj1))
	}
	strmptr := obj.ptr
	strm, ok := obj.obj.(stream)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: cross-reference table not found: %v", objfmt(obj))
	}
	if strm.hdr["Type"] != name("XRef") {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref stream does not have type XRef")
	}
	size, ok := strm.hdr["Size"].(int64)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref stream missing Size")
	}
	if size <= 0 || size > maxXrefEntries {
		return nil, objptr{}, nil, fmt.Errorf("%w: xref stream Size %d outside 1..%d", ErrStructureLimit, size, maxXrefEntries)
	}
	table := make([]xref, size)

	table, err := readXrefStreamData(r, strm, table, size)
	if err != nil {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: %w", err)
	}

	seen := map[int64]bool{}
	for prevoff := strm.hdr["Prev"]; prevoff != nil; {
		off, ok := prevoff.(int64)
		if !ok {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref Prev is not integer: %v", objfmt(prevoff))
		}
		if prevErr := checkPrevSection(seen, off, r.end); prevErr != nil {
			return nil, objptr{}, nil, prevErr
		}
		b := newBuffer(r.section(off, r.end-off), off, r)
		obj1 := b.readObject()
		obj, ok := obj1.(objdef)
		if !ok {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref prev stream not found: %v", objfmt(obj1))
		}
		prevstrm, ok := obj.obj.(stream)
		if !ok {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref prev stream not found: %v", objfmt(obj))
		}
		prevoff = prevstrm.hdr["Prev"]
		prev := Value{r, objptr{}, prevstrm}
		if prev.Kind() != Stream {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref prev stream is not stream: %v", prev)
		}
		if prev.Key("Type").Name() != "XRef" {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref prev stream does not have type XRef")
		}
		psize := prev.Key("Size").Int64()
		if psize > size {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref prev stream larger than last stream")
		}
		prevStream, isStream := prev.data.(stream)
		if !isStream {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref prev stream is not stream: %v", prev)
		}
		if table, err = readXrefStreamData(r, prevStream, table, psize); err != nil {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: reading xref prev stream: %w", err)
		}
	}

	return table, strmptr, strm.hdr, nil
}

func readXrefStreamData(r *Reader, strm stream, table []xref, size int64) ([]xref, error) {
	index, _ := strm.hdr["Index"].(array)
	if index == nil {
		index = array{int64(0), size}
	}
	if len(index)%2 != 0 {
		return nil, fmt.Errorf("invalid Index array %v", objfmt(index))
	}
	ww, ok := strm.hdr["W"].(array)
	if !ok {
		return nil, fmt.Errorf("xref stream missing W array")
	}

	var w []int
	for _, x := range ww {
		i, ok := x.(int64)
		if !ok || i < 0 || i > maxXrefFieldWidth {
			return nil, fmt.Errorf("invalid W array %v", objfmt(ww))
		}
		w = append(w, int(i))
	}
	if len(w) < 3 {
		return nil, fmt.Errorf("invalid W array %v", objfmt(ww))
	}

	v := Value{r, objptr{}, strm}
	wtotal := 0
	for _, wid := range w {
		wtotal += wid
	}
	if wtotal == 0 {
		// A row of zero bytes reads nothing, so no byte meter would see it.
		return nil, fmt.Errorf("%w: xref stream W fields are all zero", ErrStructureLimit)
	}
	// The sections of one stream cover distinct entries, so their counts
	// add up to at most /Size; repeated sections would multiply the rows.
	var rows int64
	for i := 1; i < len(index); i += 2 {
		n, ok := index[i].(int64)
		if !ok || n < 0 || n > size-rows {
			return nil, fmt.Errorf("%w: xref Index counts exceed Size %d", ErrStructureLimit, size)
		}
		rows += n
	}
	buf := make([]byte, wtotal)
	data := v.Reader()
	for len(index) >= 2 { // even length, checked above
		start, ok1 := index[0].(int64)
		n, ok2 := index[1].(int64)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("malformed Index pair %v %v %T %T", objfmt(index[0]), objfmt(index[1]), index[0], index[1])
		}
		index = index[2:]
		// Compared without adding: start+n can wrap past MaxInt64. size is at
		// most maxXrefEntries; the constant bound states it for start and n.
		if start < 0 || n < 0 || start > size || n > size-start || start > maxXrefEntries || n > maxXrefEntries {
			return nil, fmt.Errorf("%w: xref Index section %d+%d outside Size %d", ErrStructureLimit, start, n, size)
		}
		for i := 0; i < int(n); i++ {
			r.charge(1) // every row costs work, whatever it reads
			_, err := io.ReadFull(data, buf)
			if err != nil {
				return nil, fmt.Errorf("error reading xref stream: %w", err)
			}
			v1 := decodeInt(buf[0:w[0]])
			if w[0] == 0 {
				v1 = 1
			}
			v2 := decodeInt(buf[w[0] : w[0]+w[1]])
			v3 := decodeInt(buf[w[0]+w[1] : w[0]+w[1]+w[2]])
			x := int(start) + i
			table = r.growXref(table, x)
			if table[x].ptr != (objptr{}) {
				continue
			}
			switch v1 {
			case 0:
				table[x] = xref{ptr: objptr{0, 65535}}
			case 1:
				// A generation outside uint16 is malformed and the row is
				// skipped like one of unknown type, instead of wrapping.
				if v3 <= math.MaxUint16 {
					table[x] = xref{ptr: objptr{uint32(x), uint16(v3)}, offset: int64(v2)}
				}
			case 2:
				// Likewise an object stream number outside uint32.
				if v2 <= math.MaxUint32 {
					table[x] = xref{ptr: objptr{uint32(x), 0}, inStream: true, stream: objptr{uint32(v2), 0}, offset: int64(v3)}
				}
			default:
			}
		}
	}
	return table, nil
}

// decodeInt decodes a big-endian xref stream field of at most
// maxXrefFieldWidth (8) bytes; uint64 holds it exactly on every platform.
func decodeInt(b []byte) uint64 {
	var x uint64
	for _, c := range b {
		x = x<<8 | uint64(c)
	}
	return x
}

func readXrefTable(r *Reader, b *buffer) ([]xref, objptr, dict, error) {
	var table []xref

	table, err := readXrefTableData(b, table)
	if err != nil {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: %w", err)
	}

	trailer, ok := b.readObject().(dict)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref table not followed by trailer dictionary")
	}

	seen := map[int64]bool{}
	for prevoff := trailer["Prev"]; prevoff != nil; {
		off, isInt := prevoff.(int64)
		if !isInt {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref Prev is not integer: %v", objfmt(prevoff))
		}
		if prevErr := checkPrevSection(seen, off, r.end); prevErr != nil {
			return nil, objptr{}, nil, prevErr
		}
		b := newBuffer(r.section(off, r.end-off), off, r)
		tok := b.readToken()
		if tok != keyword("xref") {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref Prev does not point to xref")
		}
		table, err = readXrefTableData(b, table)
		if err != nil {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: %w", err)
		}

		prevTrailer, isDict := b.readObject().(dict)
		if !isDict {
			return nil, objptr{}, nil, fmt.Errorf("malformed PDF: xref Prev table not followed by trailer dictionary")
		}
		prevoff = prevTrailer["Prev"]
	}

	size, ok := trailer[name("Size")].(int64)
	if !ok {
		return nil, objptr{}, nil, fmt.Errorf("malformed PDF: trailer missing /Size entry")
	}

	if size < int64(len(table)) {
		table = table[:size]
	}

	return table, objptr{}, trailer, nil
}

func readXrefTableData(b *buffer, table []xref) ([]xref, error) {
	for {
		tok := b.readToken()
		if tok == keyword("trailer") {
			break
		}
		start, ok1 := tok.(int64)
		n, ok2 := b.readToken().(int64)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("malformed xref table")
		}
		// Compared without adding: start+n can wrap past MaxInt64.
		if start < 0 || n < 0 || start > maxXrefEntries || n > maxXrefEntries-start || n > maxXrefEntries {
			return nil, fmt.Errorf("%w: xref table section %d+%d over %d entries", ErrStructureLimit, start, n, maxXrefEntries)
		}
		for i := 0; i < int(n); i++ {
			b.owner.charge(1) // every row costs work, on top of its lexing
			off, ok1 := b.readToken().(int64)
			gen, ok2 := b.readToken().(int64)
			alloc, ok3 := b.readToken().(keyword)
			// A generation outside uint16 is malformed like a non-integer one,
			// instead of wrapping into another object's generation.
			if !ok1 || !ok2 || !ok3 || alloc != keyword("f") && alloc != keyword("n") || gen < 0 || gen > math.MaxUint16 {
				return nil, fmt.Errorf("malformed xref table")
			}
			x := int(start) + i
			table = b.owner.growXref(table, x)
			if len(table) <= x {
				table = table[:x+1]
			}
			if alloc == "n" && table[x].offset == 0 {
				table[x] = xref{ptr: objptr{uint32(x), uint16(gen)}, offset: off}
			}
		}
	}
	return table, nil
}

func findLastLine(buf []byte, s string) int {
	bs := []byte(s)
	limit := len(buf)
	for {
		i := bytes.LastIndex(buf[:limit], bs)
		if i <= 0 || i+len(bs) >= len(buf) {
			return -1
		}
		if (buf[i-1] == '\n' || buf[i-1] == '\r') && (buf[i+len(bs)] == '\n' || buf[i+len(bs)] == '\r') {
			return i
		}
		limit = i
	}
}

// A Value is a single PDF value, such as an integer, dictionary, or array.
// The zero Value is a PDF null (Kind() == Null, IsNull() = true).
type Value struct {
	r    *Reader
	ptr  objptr
	data any
}

// IsNull reports whether the value is a null. It is equivalent to Kind() == Null.
func (v Value) IsNull() bool {
	return v.data == nil
}

// A ValueKind specifies the kind of data underlying a Value.
type ValueKind int

// The PDF value kinds.
const (
	Null ValueKind = iota
	Bool
	Integer
	Real
	String
	Name
	Dict
	Array
	Stream
)

// Kind reports the kind of value underlying v.
func (v Value) Kind() ValueKind {
	switch v.data.(type) {
	default:
		return Null
	case bool:
		return Bool
	case int64:
		return Integer
	case float64:
		return Real
	case string:
		return String
	case name:
		return Name
	case dict:
		return Dict
	case array:
		return Array
	case stream:
		return Stream
	}
}

// String returns a textual representation of the value v.
// Note that String is not the accessor for values with Kind() == String.
// To access such values, see RawString, Text, and TextFromUTF16.
func (v Value) String() string {
	return objfmt(v.data)
}

// Bounds of objfmt, which renders objects into error messages. Upstream
// rendered a whole object and copied each nested level into its parent, so
// a deeply nested object holding a long string was copied once per level;
// the rendering now writes into one buffer and stops at maxObjfmtBytes.
const (
	maxObjfmtBytes = 1024
	// maxObjfmtKeys: a larger dictionary is shown as its entry count, so
	// its keys are not collected and sorted for a message.
	maxObjfmtKeys = 64
)

func objfmt(x any) string {
	var buf bytes.Buffer
	writeObjfmt(&buf, x)
	return clipText(buf.String())
}

// clipText cuts s to maxObjfmtBytes, on a rune boundary, marking the cut.
func clipText(s string) string {
	if len(s) <= maxObjfmtBytes {
		return s
	}
	cut := maxObjfmtBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// writeObjfmt renders x into buf. Whatever it leaves out is marked with
// "…": a string cut at maxObjfmtBytes source bytes (its quoted rendering can
// be shorter than the final bound, so that bound alone would not show the
// cut) and an element skipped because buf is already full.
func writeObjfmt(buf *bytes.Buffer, x any) {
	if buf.Len() >= maxObjfmtBytes {
		buf.WriteString("…")
		return
	}
	switch x := x.(type) {
	default:
		buf.WriteString(clipText(fmt.Sprint(x)))
	case keyword:
		buf.WriteString(clipText(string(x)))
	case string:
		sourceCut := len(x) > maxObjfmtBytes
		if sourceCut {
			x = x[:maxObjfmtBytes] // even: a UTF-16 string keeps whole units
		}
		switch {
		case isPDFDocEncoded(x):
			buf.WriteString(strconv.Quote(pdfDocDecode(x)))
		case isUTF16(x):
			buf.WriteString(strconv.Quote(utf16Decode(x[2:])))
		default:
			buf.WriteString(strconv.Quote(x))
		}
		if sourceCut {
			buf.WriteString("…")
		}
	case name:
		buf.WriteString("/")
		buf.WriteString(clipText(string(x)))
	case dict:
		if len(x) > maxObjfmtKeys {
			fmt.Fprintf(buf, "<<%d entries>>", len(x))
			return
		}
		var keys []string
		for k := range x {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		buf.WriteString("<<")
		for i, k := range keys {
			if buf.Len() >= maxObjfmtBytes {
				buf.WriteString("…")
				break
			}
			if i > 0 {
				buf.WriteString(" ")
			}
			buf.WriteString("/")
			buf.WriteString(clipText(k))
			buf.WriteString(" ")
			writeObjfmt(buf, x[name(k)])
		}
		buf.WriteString(">>")

	case array:
		buf.WriteString("[")
		for i, elem := range x {
			if buf.Len() >= maxObjfmtBytes {
				buf.WriteString("…")
				break
			}
			if i > 0 {
				buf.WriteString(" ")
			}
			writeObjfmt(buf, elem)
		}
		buf.WriteString("]")

	case stream:
		writeObjfmt(buf, x.hdr)
		fmt.Fprintf(buf, "@%d", x.offset)

	case objptr:
		fmt.Fprintf(buf, "%d %d R", x.id, x.gen)

	case objdef:
		fmt.Fprintf(buf, "{%d %d obj}", x.ptr.id, x.ptr.gen)
		writeObjfmt(buf, x.obj)
	}
}

// Bool returns v's boolean value.
// If v.Kind() != Bool, Bool returns false.
func (v Value) Bool() bool {
	x, ok := v.data.(bool)
	if !ok {
		return false
	}
	return x
}

// Int64 returns v's int64 value.
// If v.Kind() != Int64, Int64 returns 0.
func (v Value) Int64() int64 {
	x, ok := v.data.(int64)
	if !ok {
		return 0
	}
	return x
}

// Float64 returns v's float64 value, converting from integer if necessary.
// If v.Kind() != Float64 and v.Kind() != Int64, Float64 returns 0.
func (v Value) Float64() float64 {
	x, ok := v.data.(float64)
	if !ok {
		x, ok := v.data.(int64)
		if ok {
			return float64(x)
		}
		return 0
	}
	return x
}

// RawString returns v's string value.
// If v.Kind() != String, RawString returns the empty string.
func (v Value) RawString() string {
	x, ok := v.data.(string)
	if !ok {
		return ""
	}
	return x
}

// Text returns v's string value interpreted as a “text string” (defined in the PDF spec)
// and converted to UTF-8.
// If v.Kind() != String, Text returns the empty string.
func (v Value) Text() string {
	x, ok := v.data.(string)
	if !ok {
		return ""
	}
	if isPDFDocEncoded(x) {
		return pdfDocDecode(x)
	}
	if isUTF16(x) {
		return utf16Decode(x[2:])
	}
	return x
}

// TextFromUTF16 returns v's string value interpreted as big-endian UTF-16
// and then converted to UTF-8.
// If v.Kind() != String or if the data is not valid UTF-16, TextFromUTF16 returns
// the empty string.
func (v Value) TextFromUTF16() string {
	x, ok := v.data.(string)
	if !ok {
		return ""
	}
	if len(x)%2 == 1 {
		return ""
	}
	if x == "" {
		return ""
	}
	return utf16Decode(x)
}

// Name returns v's name value.
// If v.Kind() != Name, Name returns the empty string.
// The returned name does not include the leading slash:
// if v corresponds to the name written using the syntax /Helvetica,
// Name() == "Helvetica".
func (v Value) Name() string {
	x, ok := v.data.(name)
	if !ok {
		return ""
	}
	return string(x)
}

// Key returns the value associated with the given name key in the dictionary v.
// Like the result of the Name method, the key should not include a leading slash.
// If v is a stream, Key applies to the stream's header dictionary.
// If v.Kind() != Dict and v.Kind() != Stream, Key returns a null Value.
func (v Value) Key(key string) Value {
	x, ok := v.data.(dict)
	if !ok {
		strm, ok := v.data.(stream)
		if !ok {
			return Value{}
		}
		x = strm.hdr
	}
	return v.r.resolve(v.ptr, x[name(key)])
}

// Keys returns a sorted list of the keys in the dictionary v.
// If v is a stream, Keys applies to the stream's header dictionary.
// If v.Kind() != Dict and v.Kind() != Stream, Keys returns nil.
func (v Value) Keys() []string {
	x, ok := v.data.(dict)
	if !ok {
		strm, ok := v.data.(stream)
		if !ok {
			return nil
		}
		x = strm.hdr
	}
	keys := []string{} // not nil
	var keyBytes int64
	for k := range x {
		keys = append(keys, string(k))
		keyBytes += int64(len(k)) + 1
	}
	// A sort compares each key about log n times, each comparison reading
	// up to the key's bytes; the dictionary can be re-read for every page.
	v.r.charge(keyBytes * searchCost(len(keys)))
	sort.Strings(keys)
	return keys
}

// Index returns the i'th element in the array v.
// If v.Kind() != Array or if i is outside the array bounds,
// Index returns a null Value.
func (v Value) Index(i int) Value {
	x, ok := v.data.(array)
	if !ok || i < 0 || i >= len(x) {
		return Value{}
	}
	return v.r.resolve(v.ptr, x[i])
}

// Len returns the length of the array v.
// If v.Kind() != Array, Len returns 0.
func (v Value) Len() int {
	x, ok := v.data.(array)
	if !ok {
		return 0
	}
	return len(x)
}

func (r *Reader) resolve(parent objptr, x any) Value {
	if ptr, ok := x.(objptr); ok {
		// Every indirect resolution costs work, and nesting is bounded so a
		// cycle of object streams (or of their /Length objects) ends as an
		// error instead of a goroutine stack overflow.
		r.charge(resolveWork)
		if r.lim != nil {
			if r.lim.depth >= maxResolveDepth {
				structuralPanicf("indirect objects nested deeper than %d", maxResolveDepth)
			}
			r.lim.depth++
			defer func() { r.lim.depth-- }()
		}
		if ptr.id >= uint32(len(r.xref)) {
			return Value{}
		}
		xref := r.xref[ptr.id]
		if xref.ptr != ptr || !xref.inStream && xref.offset == 0 {
			return Value{}
		}
		var obj object
		if xref.inStream {
			strm := r.resolve(parent, xref.stream)
		Search:
			for {
				if strm.Kind() != Stream {
					panic("not a stream")
				}
				if strm.Key("Type").Name() != "ObjStm" {
					panic("not an object stream")
				}
				n := strm.Key("N").Int64() // int64: an int could wrap on 32-bit builds
				first := strm.Key("First").Int64()
				if first == 0 {
					panic("missing First")
				}
				b := newBuffer(strm.Reader(), 0, r)
				b.allowEOF = true
				for i := int64(0); i < n; i++ {
					id, ok1 := b.readToken().(int64)
					off, ok2 := b.readToken().(int64)
					if !ok1 || !ok2 {
						break // the header ended before N pairs
					}
					if id == int64(ptr.id) { // as lexed, not modulo 2^32
						b.seekForward(first + off)
						x = b.readObject()
						break Search
					}
				}
				ext := strm.Key("Extends")
				if ext.Kind() != Stream {
					panic("cannot find object in stream")
				}
				strm = ext
			}
		} else {
			b := newBuffer(r.section(xref.offset, r.end-xref.offset), xref.offset, r)
			b.key = r.key
			b.useAES = r.useAES
			obj = b.readObject()
			def, ok := obj.(objdef)
			if !ok {
				panic(fmt.Errorf("loading %v: found %T instead of objdef", ptr, obj))
				//return Value{}
			}
			if def.ptr != ptr {
				panic(fmt.Errorf("loading %v: found %v", ptr, def.ptr))
			}
			x = def.obj
		}
		parent = ptr
	}

	switch x := x.(type) {
	case nil, bool, int64, float64, name, dict, array, stream:
		return Value{r, parent, x}
	case string:
		return Value{r, parent, x}
	default:
		panic(fmt.Errorf("unexpected value type %T in resolve", x))
	}
}

type errorReadCloser struct {
	err error
}

func (e *errorReadCloser) Read([]byte) (int, error) {
	return 0, e.err
}

func (e *errorReadCloser) Close() error {
	return e.err
}

// Reader returns the data contained in the stream v.
// If v.Kind() != Stream, Reader returns a ReadCloser that
// responds to all reads with a “stream not present” error.
func (v Value) Reader() io.ReadCloser {
	x, ok := v.data.(stream)
	if !ok {
		return &errorReadCloser{fmt.Errorf("stream not present")}
	}
	var rd io.Reader
	length := v.Key("Length").Int64()
	if length < 0 {
		length = 0
	}
	rd = io.NewSectionReader(v.r.f, x.offset, length)
	if v.r.key != nil {
		rd = decryptStream(v.r.key, v.r.useAES, x.ptr, rd)
	}
	filter := v.Key("Filter")
	param := v.Key("DecodeParms")
	// The raw stream and every stage's output cost work as the next stage
	// (or the consumer) reads them, so neither decompression of a small
	// stream into a huge one nor an intermediate expansion a later filter
	// discards (ASCII85 skips whitespace) escapes the budget. Each stage is
	// a decoder built before the first read, so the chain length is bounded
	// and every stage is charged before it is built.
	rd = &chargedReader{owner: v.r, rd: rd}
	switch filter.Kind() {
	default:
		panic(fmt.Errorf("unsupported filter %v", filter))
	case Null:
	case Name:
		v.r.charge(filterStageWork)
		rd = &chargedReader{owner: v.r, rd: applyFilter(rd, filter.Name(), param)}
	case Array:
		stages := filter.Len()
		if stages > maxFilterStages {
			structuralPanicf("filter chain of %d stages over %d", stages, maxFilterStages)
		}
		for i := 0; i < stages; i++ {
			v.r.charge(filterStageWork)
			rd = &chargedReader{owner: v.r, rd: applyFilter(rd, filter.Index(i).Name(), param.Index(i))}
		}
	}
	return io.NopCloser(rd)
}

func applyFilter(rd io.Reader, name string, param Value) io.Reader {
	switch name {
	default:
		panic("unknown filter " + clipText(name))
	case "FlateDecode":
		zr, err := zlib.NewReader(rd)
		if err != nil {
			panic(err)
		}
		pred := param.Key("Predictor")
		if pred.Kind() == Null {
			return zr
		}
		columns := param.Key("Columns").Int64()
		if columns <= 0 || columns > maxPredictorColumns {
			structuralPanicf("predictor Columns %d outside 1..%d", columns, maxPredictorColumns)
		}
		switch pred.Int64() {
		default:
			panic("pred")
		case 12:
			return &pngUpReader{r: zr, hist: make([]byte, 1+columns), tmp: make([]byte, 1+columns)}
		}
	case "ASCII85Decode":
		cleanASCII85 := newAlphaReader(rd)
		decoder := ascii85.NewDecoder(cleanASCII85)

		switch param.Keys() {
		default:
			panic("not expected DecodeParms for ascii85")
		case nil:
			return decoder
		}
	}
}

type pngUpReader struct {
	r    io.Reader
	hist []byte
	tmp  []byte
	pend []byte
}

func (r *pngUpReader) Read(b []byte) (int, error) {
	n := 0
	for len(b) > 0 {
		if len(r.pend) > 0 {
			m := copy(b, r.pend)
			n += m
			b = b[m:]
			r.pend = r.pend[m:]
			continue
		}
		_, err := io.ReadFull(r.r, r.tmp)
		if err != nil {
			return n, err
		}
		if r.tmp[0] != 2 {
			return n, fmt.Errorf("malformed PNG-Up encoding")
		}
		for i, b := range r.tmp {
			r.hist[i] += b
		}
		r.pend = r.hist[1:]
	}
	return n, nil
}

var passwordPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

func (r *Reader) initEncrypt(password string) error {
	// See PDF 32000-1:2008, §7.6.
	encrypt, _ := r.resolve(objptr{}, r.trailer["Encrypt"]).data.(dict)
	if encrypt["Filter"] != name("Standard") {
		return fmt.Errorf("unsupported PDF: encryption filter %v", objfmt(encrypt["Filter"]))
	}
	n, _ := encrypt["Length"].(int64)
	if n == 0 {
		n = 40
	}
	if n%8 != 0 || n > 128 || n < 40 {
		return fmt.Errorf("malformed PDF: %d-bit encryption key", n)
	}
	V, _ := encrypt["V"].(int64)
	if V != 1 && V != 2 && (V != 4 || !okayV4(encrypt)) {
		return fmt.Errorf("unsupported PDF: encryption version V=%d; %v", V, objfmt(encrypt))
	}

	ids, ok := r.trailer["ID"].(array)
	if !ok || len(ids) < 1 {
		return fmt.Errorf("malformed PDF: missing ID in trailer")
	}
	idstr, ok := ids[0].(string)
	if !ok {
		return fmt.Errorf("malformed PDF: missing ID in trailer")
	}
	ID := []byte(idstr)

	R, _ := encrypt["R"].(int64)
	if R < 2 {
		return fmt.Errorf("malformed PDF: encryption revision R=%d", R)
	}
	if R > 4 {
		return fmt.Errorf("unsupported PDF: encryption revision R=%d", R)
	}
	O, _ := encrypt["O"].(string)
	U, _ := encrypt["U"].(string)
	if len(O) != 32 || len(U) != 32 {
		return fmt.Errorf("malformed PDF: missing O= or U= encryption parameters")
	}
	p, _ := encrypt["P"].(int64)
	P := uint32(p)

	// TODO: Password should be converted to Latin-1.
	pw := []byte(password)
	h := md5.New()
	if len(pw) >= 32 {
		h.Write(pw[:32])
	} else {
		h.Write(pw)
		h.Write(passwordPad[:32-len(pw)])
	}
	h.Write([]byte(O))
	h.Write([]byte{byte(P), byte(P >> 8), byte(P >> 16), byte(P >> 24)})
	h.Write(ID)
	key := h.Sum(nil)

	if R >= 3 {
		for i := 0; i < 50; i++ {
			h.Reset()
			h.Write(key[:n/8])
			key = h.Sum(key[:0])
		}
		key = key[:n/8]
	} else {
		key = key[:40/8]
	}

	c, err := rc4.NewCipher(key)
	if err != nil {
		return fmt.Errorf("malformed PDF: invalid RC4 key: %w", err)
	}

	var u []byte
	if R == 2 {
		u = make([]byte, 32)
		copy(u, passwordPad)
		c.XORKeyStream(u, u)
	} else {
		h.Reset()
		h.Write(passwordPad)
		h.Write(ID)
		u = h.Sum(nil)
		c.XORKeyStream(u, u)

		for i := 1; i <= 19; i++ {
			key1 := make([]byte, len(key))
			copy(key1, key)
			for j := range key1 {
				key1[j] ^= byte(i)
			}
			c, _ = rc4.NewCipher(key1)
			c.XORKeyStream(u, u)
		}
	}

	if !bytes.HasPrefix([]byte(U), u) {
		return ErrInvalidPassword
	}

	r.key = key
	r.useAES = V == 4

	return nil
}

var ErrInvalidPassword = fmt.Errorf("encrypted PDF: invalid password")

func okayV4(encrypt dict) bool {
	cf, ok := encrypt["CF"].(dict)
	if !ok {
		return false
	}
	stmf, ok := encrypt["StmF"].(name)
	if !ok {
		return false
	}
	strf, ok := encrypt["StrF"].(name)
	if !ok {
		return false
	}
	if stmf != strf {
		return false
	}
	cfparam, _ := cf[stmf].(dict)
	if cfparam["AuthEvent"] != nil && cfparam["AuthEvent"] != name("DocOpen") {
		return false
	}
	if cfparam["Length"] != nil && cfparam["Length"] != int64(16) {
		return false
	}
	if cfparam["CFM"] != name("AESV2") {
		return false
	}
	return true
}

func cryptKey(key []byte, useAES bool, ptr objptr) []byte {
	h := md5.New()
	h.Write(key)
	h.Write([]byte{byte(ptr.id), byte(ptr.id >> 8), byte(ptr.id >> 16), byte(ptr.gen), byte(ptr.gen >> 8)})
	if useAES {
		h.Write([]byte("sAlT"))
	}
	return h.Sum(nil)
}

func decryptString(key []byte, useAES bool, ptr objptr, x string) string {
	key = cryptKey(key, useAES, ptr)
	if useAES {
		s := []byte(x)
		if len(s) < aes.BlockSize {
			panic("Encrypted text shorter that AES block size")
		}

		block, _ := aes.NewCipher(key)
		iv := s[:aes.BlockSize]
		s = s[aes.BlockSize:]

		stream := cipher.NewCBCDecrypter(block, iv)
		stream.CryptBlocks(s, s)
		x = string(s)
	} else {
		c, _ := rc4.NewCipher(key)
		data := []byte(x)
		c.XORKeyStream(data, data)
		x = string(data)
	}
	return x
}

func decryptStream(key []byte, useAES bool, ptr objptr, rd io.Reader) io.Reader {
	key = cryptKey(key, useAES, ptr)
	if useAES {
		cb, err := aes.NewCipher(key)
		if err != nil {
			panic("AES: " + err.Error())
		}
		iv := make([]byte, 16)
		io.ReadFull(rd, iv)
		cbc := cipher.NewCBCDecrypter(cb, iv)
		rd = &cbcReader{cbc: cbc, rd: rd, buf: make([]byte, 16)}
	} else {
		c, _ := rc4.NewCipher(key)
		rd = &cipher.StreamReader{S: c, R: rd}
	}
	return rd
}

type cbcReader struct {
	cbc  cipher.BlockMode
	rd   io.Reader
	buf  []byte
	pend []byte
}

func (r *cbcReader) Read(b []byte) (n int, err error) {
	if len(r.pend) == 0 {
		_, err = io.ReadFull(r.rd, r.buf)
		if err != nil {
			return 0, err
		}
		r.cbc.CryptBlocks(r.buf, r.buf)
		r.pend = r.buf
	}
	n = copy(b, r.pend)
	r.pend = r.pend[n:]
	return n, nil
}

// checkPrevSection bounds the /Prev chain of cross-reference sections: each
// section offset must lie inside the file, appear once, and the chain must
// stay under maxXrefSections.
func checkPrevSection(seen map[int64]bool, off, end int64) error {
	if off < 0 || off >= end {
		return fmt.Errorf("malformed PDF: xref Prev offset %d outside the file", off)
	}
	if seen[off] {
		return fmt.Errorf("malformed PDF: xref Prev chain loops at offset %d", off)
	}
	if len(seen) >= maxXrefSections {
		return fmt.Errorf("%w: more than %d xref sections", ErrStructureLimit, maxXrefSections)
	}
	seen[off] = true
	return nil
}

// section returns a reader over [off, off+n) of the file. Its bytes are
// charged as the lexer consumes them (buffer.reload), not as they are read:
// pulling a 4 KiB chunk to parse a 20-byte object costs a copy, not a parse.
func (r *Reader) section(off, n int64) io.Reader {
	if n < 0 {
		n = 0
	}
	return io.NewSectionReader(r.f, off, n)
}
