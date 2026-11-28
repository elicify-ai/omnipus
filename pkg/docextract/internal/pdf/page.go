// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pdf

import (
	"bytes"
	"container/heap"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"sort"
	"strings"
	"unicode/utf8"
)

// A Page represent a single page in a PDF file.
// The methods interpret a Page dictionary stored in V.
type Page struct {
	V Value
}

// GetPlainText returns all the text in the PDF file, page by page in
// page-tree order. Upstream looked every page up from the root by number
// (r.Page(i) for i up to the file's own /Count), which is quadratic and runs
// 2^31 lookups for a hostile /Count; this walks the tree once, with its depth
// bounded and every node charged as a resolution. As upstream, at most
// /Count pages are read (none when /Count is absent). When the text budget
// ends the walk, the text gathered so far is returned with ErrTextLimit.
func (r *Reader) GetPlainText() (reader io.Reader, err error) {
	var buf bytes.Buffer
	fonts := NewFontCache()
	root := r.Trailer().Key("Root").Key("Pages")
	remaining := root.Key("Count").Int64()
	err = r.plainTextWalk(root, 0, &remaining, fonts, &buf)
	if err != nil && !errors.Is(err, ErrTextLimit) {
		return &bytes.Buffer{}, err
	}
	return &buf, err
}

func (r *Reader) plainTextWalk(node Value, depth int, remaining *int64, fonts *FontCache, buf *bytes.Buffer) error {
	if *remaining <= 0 {
		return nil
	}
	if depth > maxInheritDepth {
		structuralPanicf("page tree deeper than %d", maxInheritDepth)
	}
	r.charge(resolveWork)
	switch node.Key("Type").Name() {
	case "Pages":
		kids := node.Key("Kids")
		for i := 0; i < kids.Len(); i++ {
			if err := r.plainTextWalk(kids.Index(i), depth+1, remaining, fonts, buf); err != nil {
				return err
			}
		}
	case "Page":
		*remaining--
		p := Page{node}
		text, err := p.GetPlainText(fonts.PageFonts(p))
		buf.WriteString(text)
		return err
	}
	return nil
}

// FontCache shares parsed fonts, and so their cached encoders, between the
// pages of one document. A font name such as /F1 belongs to one page's
// resource dictionary, so two pages can bind the same name to different
// fonts; upstream cached fonts by name for the whole document, and a later
// page then decoded its text with an earlier page's font. The cache is
// keyed by the font's indirect object instead, and each page gets its own
// name map. A font written directly in a resource dictionary has no object
// identity and is parsed again for each page that uses it.
type FontCache struct {
	byRef map[objptr]*Font
}

// NewFontCache returns an empty cache for one document.
func NewFontCache() *FontCache {
	return &FontCache{byRef: make(map[objptr]*Font)}
}

// PageFonts returns the fonts of page p by the names p's own resources give
// them. A font that is the same indirect object as one an earlier page used
// is shared with that page.
func (c *FontCache) PageFonts(p Page) map[string]*Font {
	fontDict := p.Resources().Key("Font")
	names := fontDict.Keys()
	raw, _ := fontDict.data.(dict)
	fonts := make(map[string]*Font, len(names))
	for _, n := range names {
		if ref, ok := raw[name(n)].(objptr); ok {
			if f, ok := c.byRef[ref]; ok {
				fonts[n] = f
				continue
			}
			f := &Font{fontDict.Key(n), nil}
			c.byRef[ref] = f
			fonts[n] = f
			continue
		}
		fonts[n] = &Font{fontDict.Key(n), nil}
	}
	return fonts
}

func (p Page) findInherited(key string) Value {
	// Bounded: a /Parent that points back at the page (or any ancestor)
	// would otherwise loop forever.
	depth := 0
	for v := p.V; !v.IsNull(); v = v.Key("Parent") {
		if depth >= maxInheritDepth {
			structuralPanicf("page /Parent chain longer than %d", maxInheritDepth)
		}
		depth++
		// A direct /Parent is not resolved, so the step costs work here
		// (each page and font lookup walks the chain again).
		v.r.charge(1)
		if r := v.Key(key); !r.IsNull() {
			return r
		}
	}
	return Value{}
}

// Resources returns the resources dictionary associated with the page.
func (p Page) Resources() Value {
	return p.findInherited("Resources")
}

// Fonts returns a list of the fonts associated with the page.
func (p Page) Fonts() []string {
	return p.Resources().Key("Font").Keys()
}

// Font returns the font with the given name associated with the page.
func (p Page) Font(name string) Font {
	return Font{p.Resources().Key("Font").Key(name), nil}
}

// A Font represent a font in a PDF file.
// The methods interpret a Font dictionary stored in V.
type Font struct {
	V   Value
	enc TextEncoding
}

// BaseFont returns the font's name (BaseFont property).
func (f Font) BaseFont() string {
	return f.V.Key("BaseFont").Name()
}

// FirstChar returns the code point of the first character in the font.
func (f Font) FirstChar() int {
	return int32Value(f.V.Key("FirstChar"))
}

// LastChar returns the code point of the last character in the font.
func (f Font) LastChar() int {
	return int32Value(f.V.Key("LastChar"))
}

// int32Value returns v's integer as an int, or 0 (what Int64 gives a
// non-integer) when it lies outside int32: a character code is a few bytes
// wide, so a larger one is malformed and must not wrap on a 32-bit build.
func int32Value(v Value) int {
	x := v.Int64()
	if x < math.MinInt32 || x > math.MaxInt32 {
		return 0
	}
	return int(x)
}

// Widths returns the widths of the glyphs in the font.
// In a well-formed PDF, len(f.Widths()) == f.LastChar()+1 - f.FirstChar().
func (f Font) Widths() []float64 {
	x := f.V.Key("Widths")
	var out []float64
	for i := 0; i < x.Len(); i++ {
		out = append(out, x.Index(i).Float64())
	}
	return out
}

// Width returns the width of the given code point.
func (f Font) Width(code int) float64 {
	first := f.FirstChar()
	last := f.LastChar()
	if code < first || last < code {
		return 0
	}
	return f.V.Key("Widths").Index(code - first).Float64()
}

// Encoder returns the encoding between font code point sequences and UTF-8.
// It has a pointer receiver so the cache actually holds: upstream's value
// receiver re-parsed the font's cmap at every Tf operator.
func (f *Font) Encoder() TextEncoding {
	if f.enc == nil { // caching the Encoder so we don't have to continually parse charmap
		f.enc = f.getEncoder()
	}
	return f.enc
}

func (f Font) getEncoder() TextEncoding {
	enc := f.V.Key("Encoding")
	switch enc.Kind() {
	case Name:
		switch enc.Name() {
		case "WinAnsiEncoding":
			return &byteEncoder{&winAnsiEncoding}
		case "MacRomanEncoding":
			return &byteEncoder{&macRomanEncoding}
		case "Identity-H":
			return f.charmapEncoding()
		default:
			return &nopEncoder{}
		}
	case Dict:
		return newDictEncoder(enc.Key("Differences"))
	case Null:
		return f.charmapEncoding()
	default:
		return &nopEncoder{}
	}
}

func (f *Font) charmapEncoding() TextEncoding {
	toUnicode := f.V.Key("ToUnicode")
	if toUnicode.Kind() == Stream {
		m := readCmap(toUnicode)
		if m == nil {
			return &nopEncoder{}
		}
		return m
	}

	return &byteEncoder{&pdfDocEncoding}
}

// dictEncoder decodes with an /Encoding dictionary's /Differences array.
// Upstream scanned the whole array, hashing glyph names, for every byte it
// decoded, so one long name was hashed again for every glyph. The table is
// built once instead, with the same result: byte c becomes the first glyph
// name coded c that nameToRune knows, else rune(c).
type dictEncoder struct {
	table [256]rune
}

func newDictEncoder(differences Value) *dictEncoder {
	e := &dictEncoder{}
	var known [256]bool
	for c := range e.table {
		e.table[c] = rune(c)
	}
	// int64, as lexed: a code outside 0..255 names no byte, and narrowing
	// it to int could wrap it into that range on a 32-bit build.
	n := int64(-1)
	for j := 0; j < differences.Len(); j++ {
		differences.r.charge(1)
		x := differences.Index(j)
		switch x.Kind() {
		case Integer:
			n = x.Int64()
		case Name:
			// Only a byte's first known name is ever used, so only those
			// names are hashed, each once, and the hash costs its bytes.
			if n >= 0 && n < int64(len(e.table)) && !known[n] {
				glyph := x.Name()
				differences.r.charge(int64(len(glyph)))
				if r := nameToRune[glyph]; r != 0 {
					e.table[n], known[n] = r, true
				}
			}
			n++
		}
	}
	return e
}

func (e *dictEncoder) Decode(raw string) (text string) {
	r := make([]rune, len(raw))
	for i := 0; i < len(raw); i++ {
		r[i] = e.table[raw[i]]
	}
	return string(r)
}

// A TextEncoding represents a mapping between
// font code points and UTF-8 text.
type TextEncoding interface {
	// Decode returns the UTF-8 text corresponding to
	// the sequence of code points in raw.
	Decode(raw string) (text string)
}

type nopEncoder struct {
}

func (e *nopEncoder) Decode(raw string) (text string) {
	return raw
}

type byteEncoder struct {
	table *[256]rune
}

func (e *byteEncoder) Decode(raw string) (text string) {
	r := make([]rune, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		r = append(r, e.table[raw[i]])
	}
	return string(r)
}

type byteRange struct {
	low  string
	high string
}

type bfrange struct {
	lo  string
	hi  string
	dst Value
}

type cmap struct {
	owner   *Reader        // work budget for Decode
	space   [4][]byteRange // codespace range
	bfrange []bfrange
	idx     cmapIndex // lookup structures built by readCmap
	entries int       // entries kept, at most maxCmapEntries
}

// admitEntry charges for one more kept cmap entry before it is stored.
func (m *cmap) admitEntry() {
	if m.entries >= maxCmapEntries {
		structuralPanicf("cmap over %d entries", maxCmapEntries)
	}
	m.entries++
	m.owner.charge(cmapEntryWork)
}

// usableCode reports whether a source code of this length can ever be
// looked up: Decode tries codes of 1 to 4 bytes only, so upstream's scan
// never matched an entry of another length.
func usableCode(code string) bool { return len(code) >= 1 && len(code) <= 4 }

// Decode maps raw character codes to text. Upstream scanned every codespace
// range, bfchar and bfrange linearly for every code; the lookups here use
// the index readCmap built (same first-match results), and each charges the
// comparisons it actually makes, so a code that matches nothing is cheap
// and a large table no longer multiplies the cost of every glyph.
func (m *cmap) Decode(raw string) (text string) {
	d := cmapDecode{m: m}
Parse:
	for len(raw) > 0 {
		for n := 1; n <= 4 && n <= len(raw); n++ { // number of digits in character replacement (1-4 possible)
			if !m.idx.inCodespace(m.owner, raw[:n]) {
				continue
			}
			code := raw[:n]
			raw = raw[n:]
			if !d.emit(m.mapCode(code)) {
				break Parse
			}
			continue Parse
		}
		if !d.emit(string(noRune)) {
			break Parse
		}
		raw = raw[1:]
	}
	m.owner.noteTextStop(d.stopped)
	return d.out.String()
}

// mapCode returns the text of one code that lies in a codespace range: the
// first bfchar with that code, else the first bfrange holding it, else
// noRune (upstream's order and results).
func (m *cmap) mapCode(code string) string {
	m.owner.charge(1)
	if repl, ok := m.idx.bfchar[code]; ok {
		// Decoding reads every byte of the replacement (which a shared
		// dictionary value can make long), so the bytes cost work first.
		m.owner.charge(int64(len(repl)))
		return utf16Decode(repl)
	}
	i := m.idx.bfrangeFor(m.owner, code)
	if i < 0 {
		return string(noRune)
	}
	br := m.bfrange[i]
	if br.dst.Kind() == String {
		s := br.dst.RawString()
		m.owner.charge(int64(len(s)))
		if br.lo != code { // value isn't at the beginning of the range so scale result
			b := []byte(s)
			b[len(b)-1] += code[len(code)-1] - br.lo[len(br.lo)-1] // increment last byte by difference
			s = string(b)
		}
		return utf16Decode(s)
	}
	if br.dst.Kind() == Array {
		n := code[len(code)-1] - br.lo[len(br.lo)-1]
		v := br.dst.Index(int(n))
		if v.Kind() == String {
			s := v.RawString()
			m.owner.charge(int64(len(s)))
			return utf16Decode(s)
		}
	}
	return string(noRune)
}

// cmapIndex holds a cmap's lookup structures. Codes of 1 to 4 bytes are
// compared as big-endian integers, which orders them exactly as the
// upstream string comparisons did.
type cmapIndex struct {
	space1 [256 / 64]uint64 // one-byte codespace, a bitmap
	space2 []uint64         // two-byte codespace, a 65,536-bit bitmap (nil: none)
	space  [2][]codeSpan    // three- and four-byte codespace, sorted and merged
	bfchar map[string]string
	ranges [4][]rangeSpan // per code length: disjoint spans, first bfrange wins
}

// codeSpan is an inclusive span of codes.
type codeSpan struct{ lo, hi uint32 }

// rangeSpan is a span of codes whose first matching bfrange is idx.
type rangeSpan struct {
	lo, hi uint32
	idx    int
}

func codeValue(s string) uint32 {
	var v uint32
	for i := 0; i < len(s); i++ {
		v = v<<8 | uint32(s[i])
	}
	return v
}

// searchCost is the comparisons a binary search over n spans makes.
func searchCost(n int) int64 { return int64(bits.Len(uint(n))) + 1 }

// inCodespace reports whether code (1 to 4 bytes) lies in a codespace
// range of its length, charging the comparisons made.
func (x *cmapIndex) inCodespace(owner *Reader, code string) bool {
	v := codeValue(code)
	switch len(code) {
	case 1:
		owner.charge(1)
		return x.space1[v/64]&(1<<(v%64)) != 0
	case 2:
		owner.charge(1)
		return x.space2 != nil && x.space2[v/64]&(1<<(v%64)) != 0
	}
	spans := x.space[len(code)-3]
	if len(spans) == 0 {
		return false
	}
	owner.charge(searchCost(len(spans)))
	i := sort.Search(len(spans), func(i int) bool { return spans[i].hi >= v })
	return i < len(spans) && spans[i].lo <= v
}

// bfrangeFor returns the index of the first bfrange holding code, or -1.
func (x *cmapIndex) bfrangeFor(owner *Reader, code string) int {
	spans := x.ranges[len(code)-1]
	if len(spans) == 0 {
		return -1
	}
	owner.charge(searchCost(len(spans)))
	v := codeValue(code)
	i := sort.Search(len(spans), func(i int) bool { return spans[i].hi >= v })
	if i < len(spans) && spans[i].lo <= v {
		return spans[i].idx
	}
	return -1
}

// buildIndex builds the codespace and bfrange parts of m.idx (readCmap
// fills the bfchar map as it parses). Every entry was admitted and charged
// when it was stored; the sorts charge about n log n before they run.
func (m *cmap) buildIndex() {
	x := &m.idx
	for n := 1; n <= 4; n++ {
		var spans []codeSpan
		for _, r := range m.space[n-1] {
			lo, hi := codeValue(r.low), codeValue(r.high)
			if lo <= hi {
				spans = append(spans, codeSpan{lo, hi})
			}
		}
		m.owner.charge(int64(len(spans)) * searchCost(len(spans)))
		spans = mergeSpans(spans)
		switch n {
		case 1:
			for _, sp := range spans {
				for c := sp.lo; c <= sp.hi; c++ {
					x.space1[c/64] |= 1 << (c % 64)
				}
			}
		case 2:
			if len(spans) > 0 {
				x.space2 = make([]uint64, 65536/64)
				m.owner.charge(65536 / 64)
			}
			for _, sp := range spans {
				m.owner.charge(int64(sp.hi-sp.lo)/64 + 1)
				for c := sp.lo; c <= sp.hi; c++ {
					x.space2[c/64] |= 1 << (c % 64)
				}
			}
		default:
			x.space[n-3] = spans
		}
	}
	for n := 1; n <= 4; n++ {
		x.ranges[n-1] = m.rangeSpans(n)
	}
}

// mergeSpans sorts spans and merges overlapping or adjacent ones.
func mergeSpans(spans []codeSpan) []codeSpan {
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })
	out := spans[:0]
	for _, sp := range spans {
		if k := len(out) - 1; k >= 0 && uint64(sp.lo) <= uint64(out[k].hi)+1 {
			if sp.hi > out[k].hi {
				out[k].hi = sp.hi
			}
			continue
		}
		out = append(out, sp)
	}
	return out
}

// rangeSpans turns the bfranges for codes of n bytes into disjoint sorted
// spans, each naming the first bfrange (lowest index) that holds it, as
// upstream's linear scan chose. A range holds code c when lo has n bytes
// and lo <= c <= hi as strings; hi of another length is turned into the
// equal integer bound for n-byte codes.
func (m *cmap) rangeSpans(n int) []rangeSpan {
	type item struct {
		lo, hi uint64
		idx    int
	}
	var items []item
	for i, r := range m.bfrange {
		if len(r.lo) != n {
			continue
		}
		lo := uint64(codeValue(r.lo))
		var hi uint64
		switch {
		case len(r.hi) >= n:
			// c <= hi exactly when c <= hi's first n bytes.
			hi = uint64(codeValue(r.hi[:n]))
		default:
			// A shorter hi is below every code that starts with it.
			hi = uint64(codeValue(r.hi)) << (8 * uint(n-len(r.hi)))
			if hi == 0 {
				continue
			}
			hi--
		}
		if lo <= hi {
			items = append(items, item{lo, hi, i})
		}
	}
	if len(items) == 0 {
		return nil
	}
	// The two sorts are charged before they run; the sweep charges per step.
	m.owner.charge(3 * int64(len(items)) * searchCost(len(items)))
	sort.Slice(items, func(i, j int) bool { return items[i].lo < items[j].lo })
	// Boundaries: every lo and every hi+1. Between two consecutive ones
	// the set of holding ranges is fixed; its lowest index wins.
	points := make([]uint64, 0, 2*len(items))
	for _, it := range items {
		points = append(points, it.lo, it.hi+1)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	var out []rangeSpan
	active := &idxHeap{}
	next := 0
	for p := 0; p+1 < len(points); p++ {
		m.owner.charge(searchCost(len(items)))
		from, to := points[p], points[p+1]
		if from == to {
			continue
		}
		for next < len(items) && items[next].lo <= from {
			heap.Push(active, idxEntry{items[next].idx, items[next].hi})
			next++
		}
		for active.Len() > 0 && (*active)[0].hi < from {
			heap.Pop(active)
		}
		if active.Len() == 0 {
			continue
		}
		win := (*active)[0].idx
		if k := len(out) - 1; k >= 0 && out[k].idx == win && uint64(out[k].hi)+1 == from {
			out[k].hi = uint32(to - 1)
			continue
		}
		out = append(out, rangeSpan{uint32(from), uint32(to - 1), win})
	}
	return out
}

// idxEntry is an active bfrange in rangeSpans' sweep.
type idxEntry struct {
	idx int
	hi  uint64
}

// idxHeap is a min-heap of active bfranges by index.
type idxHeap []idxEntry

func (h idxHeap) Len() int           { return len(h) }
func (h idxHeap) Less(i, j int) bool { return h[i].idx < h[j].idx }
func (h idxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *idxHeap) Push(x any) {
	e, ok := x.(idxEntry)
	if !ok {
		panic(fmt.Sprintf("idxHeap: pushed %T", x))
	}
	*h = append(*h, e)
}
func (h *idxHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

// cmapDecode accumulates one Decode call's output. A replacement can be far
// longer than its code, so the output is held to the text budget still
// left, counted in the UTF-8 bytes it will actually take; the decoded prefix
// that fits is kept, and stopped records that the budget ended the decode.
type cmapDecode struct {
	m       *cmap
	out     strings.Builder
	stopped bool
}

// emit appends s if its UTF-8 bytes still fit the text budget, charging
// its runes as work; otherwise it appends the runes of s that fit, records
// the stop, and reports false.
func (d *cmapDecode) emit(s string) bool {
	d.m.owner.charge(int64(utf8.RuneCountInString(s)))
	room := d.m.owner.textRoom() - int64(d.out.Len())
	if int64(len(s)) <= room {
		d.out.WriteString(s)
		return true
	}
	for _, ch := range s {
		size := int64(utf8.RuneLen(ch))
		if size > room {
			break
		}
		d.out.WriteRune(ch)
		room -= size
	}
	d.stopped = true
	return false
}

func readCmap(toUnicode Value) *cmap {
	n := -1
	m := cmap{owner: toUnicode.r}
	ok := true
	Interpret(toUnicode, func(stk *Stack, op string) {
		if !ok {
			return
		}
		switch op {
		case "findresource":
			stk.Pop() // category
			stk.Pop() // key
			stk.Push(newDict())
		case "begincmap":
			stk.Push(newDict())
		case "endcmap":
			stk.Pop()
		case "begincodespacerange":
			n = int(stk.Pop().Int64())
		case "endcodespacerange":
			// n > Len/2, not 2*n > Len: the product can wrap negative. A
			// count the operands cannot honour makes the cmap unusable, an
			// error rather than a silent fall back to the raw codes.
			if n < 0 || n > stk.Len()/2 {
				structuralPanicf("cmap codespace count %d with %d operands", n, stk.Len())
			}
			for i := 0; i < n; i++ {
				hi, lo := stk.popRequired().RawString(), stk.popRequired().RawString()
				if len(lo) == 0 || len(lo) != len(hi) {
					ok = false
					return
				}
				m.admitEntry()
				m.space[len(lo)-1] = append(m.space[len(lo)-1], byteRange{lo, hi})
			}
			n = -1
		case "beginbfchar":
			n = int(stk.Pop().Int64())
		case "endbfchar":
			if n < 0 {
				panic("missing beginbfchar")
			}
			// n > Len/2, not 2*n > Len: the product can wrap negative.
			if n > stk.Len()/2 {
				structuralPanicf("cmap bfchar count %d with %d operands", n, stk.Len())
			}
			// The map is filled here, one admitted entry per distinct
			// code: a duplicate costs one lookup of at most four bytes and
			// keeps nothing, and a code Decode can never look up (not 1 to
			// 4 bytes) is skipped unhashed, as upstream's scan skipped it.
			for i := 0; i < n; i++ {
				m.owner.charge(1)
				repl, orig := stk.popRequired().RawString(), stk.popRequired().RawString()
				if !usableCode(orig) {
					continue
				}
				if _, dup := m.idx.bfchar[orig]; dup { // upstream used the first
					continue
				}
				m.admitEntry()
				if m.idx.bfchar == nil {
					m.idx.bfchar = make(map[string]string)
				}
				m.idx.bfchar[orig] = repl
			}
		case "beginbfrange":
			n = int(stk.Pop().Int64())
		case "endbfrange":
			if n < 0 {
				panic("missing beginbfrange")
			}
			// n > Len/3, not 3*n > Len: the product can wrap negative.
			if n > stk.Len()/3 {
				structuralPanicf("cmap bfrange count %d with %d operands", n, stk.Len())
			}
			for i := 0; i < n; i++ {
				m.owner.charge(1)
				dst, srcHi, srcLo := stk.popRequired(), stk.popRequired().RawString(), stk.popRequired().RawString()
				if !usableCode(srcLo) { // never matched, as upstream
					continue
				}
				m.admitEntry()
				m.bfrange = append(m.bfrange, bfrange{srcLo, srcHi, dst})
			}
		case "defineresource":
			stk.Pop().Name() // category
			value := stk.Pop()
			stk.Pop().Name() // key
			// The value goes back on the stack without being lexed again.
			m.owner.chargeReuse(value.data)
			stk.Push(value)
		default:
		}
	})
	if !ok {
		return nil
	}
	m.buildIndex()
	return &m
}

// isNumber reports whether v is a PDF number (integer or real).
func isNumber(v Value) bool {
	k := v.Kind()
	return k == Integer || k == Real
}

// GetPlainText returns the page's all text without format.
// fonts can be passed in (to improve parsing performance) or left nil
func (p Page) GetPlainText(fonts map[string]*Font) (result string, err error) {
	var textBuilder bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			result = ""
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = errors.New(fmt.Sprint(r))
			}
			if errors.Is(err, ErrTextLimit) {
				// The text up to the budget is kept; the caller marks it
				// truncated.
				result = textBuilder.String()
			}
		}
	}()

	// Handle in case the content page is empty
	if p.V.IsNull() || p.V.Key("Contents").Kind() == Null {
		return "", nil
	}
	strm := p.V.Key("Contents")
	var enc TextEncoding = &nopEncoder{}

	if fonts == nil {
		fonts = make(map[string]*Font)
		for _, font := range p.Fonts() {
			f := p.Font(font)
			fonts[font] = &f
		}
	}

	owner := p.V.r
	showText := func(s string) {
		owner.chargeText(len(s))
		textBuilder.WriteString(s)
	}
	showEncodedText := func(s string) {
		// Every encoder reads every byte of s (a shared string can be
		// shown repeatedly), so the bytes cost work before decoding.
		owner.charge(int64(len(s)))
		decoded := enc.Decode(s)
		stopped := owner.takeTextStop()
		for _, ch := range decoded {
			// Charge the bytes WriteRune writes (an invalid rune is
			// written as U+FFFD, three bytes).
			size := utf8.RuneLen(ch)
			if size < 0 {
				size = utf8.RuneLen(utf8.RuneError)
			}
			owner.chargeText(size)
			if _, err := textBuilder.WriteRune(ch); err != nil {
				panic(err)
			}
		}
		if stopped {
			// The decoder kept the prefix that fits; the rest of this
			// string, and of the document, is over the text budget.
			panic(ErrTextLimit)
		}
	}

	Interpret(strm, func(stk *Stack, op string) {
		n := stk.Len()
		args := make([]Value, n)
		for i := n - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}

		switch op {
		default:
			return
		case "BT": // add a space between text objects
			showText("\n")
		case "T*": // move to start of next line
			showEncodedText("\n")
		case "Tf": // set text font and size
			if len(args) != 2 {
				panic("bad TL")
			}
			if font, ok := fonts[args[0].Name()]; ok {
				enc = font.Encoder()
			} else {
				enc = &nopEncoder{}
			}
		case "\"": // aw ac string ": set spacing, move to next line, show text
			// PDF 1.7 §9.4.3: word spacing aw and character spacing ac are
			// numbers, then the string; the operator is aw Tw ac Tc string '.
			// Spacing does not change the extracted text, so the operands
			// are checked and the string is shown like '. (Upstream fell
			// through into the one-operand ' check, so every valid " failed;
			// gate-8 finding G8-C1.)
			if len(args) != 3 || !isNumber(args[0]) || !isNumber(args[1]) {
				panic("bad \" operator")
			}
			showText("\n")
			showEncodedText(args[2].RawString())
		case "'": // move to next line and show text: T* string Tj
			if len(args) != 1 {
				panic("bad ' operator")
			}
			showText("\n")
			showEncodedText(args[0].RawString())
		case "Tj": // show text
			if len(args) != 1 {
				panic("bad Tj operator")
			}
			showEncodedText(args[0].RawString())
		case "TJ": // show text, allowing individual glyph positioning
			v := args[0]
			for i := 0; i < v.Len(); i++ {
				// Every element costs work, numbers too: an array bound
				// with def can be shown again and again without being
				// lexed again, and the charge also samples the clock.
				owner.charge(1)
				x := v.Index(i)
				if x.Kind() == String {
					showEncodedText(x.RawString())
				}
			}
		}
	})
	return textBuilder.String(), nil
}
