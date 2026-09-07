// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// links.go — extraction and resolution of the links between notes
// (ADR-067 D6; FR-040, FR-041, FR-042, FR-045; FR-034a's streaming clause).
//
// Obsidian is not only search. What makes a collection navigable rather than a
// pile of files is that its notes point at each other, in four spellings that
// all mean the same thing:
//
//	[[Target]]           bare name
//	[[Target|alias]]     name with display text
//	[[Target#Heading]]   name with a heading anchor
//	[[folder/Target]]    collection-relative path
//
// plus ordinary relative markdown links, "[text](../folder/Target.md)", and
// embeds, "![[image.png]]".
//
// # Two properties this file exists to guarantee
//
// DETERMINISM (FR-045, FR-046, NB-5). Nothing here consults a model, a
// heuristic, or anything that could differ between two runs or two machines.
// Where the collection genuinely does not determine an answer — two notes
// sharing a basename — the tie is broken by a stated rule AND the ambiguity is
// reported, rather than resolved quietly. Silent resolution is the real
// failure mode: the reader believes the link went where they meant, and there
// is no way to discover afterwards that it did not.
//
// BOUNDED MEMORY (FR-034a). A note has no maximum size — none is refused,
// skipped or truncated — and link and heading extraction stream over the whole
// file rather than being segmented. Both halves of that sentence matter: the
// index may split a huge note into several documents, but the GRAPH must see
// it whole, so this scanner reads a fixed-size window and never holds the file.
// ScanStats.MaxBufferedBytes reports the high-water mark of that window, which
// is what makes "streaming" a measurable property rather than a claim.

import (
	"bytes"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// LinkKind distinguishes the two syntaxes a link can be written in. Both
// resolve through the same rules; the kind matters because a wikilink target
// is collection-relative while a markdown target is relative to the note that
// contains it.
type LinkKind string

const (
	// LinkWikilink is Obsidian's [[…]] form.
	LinkWikilink LinkKind = "wikilink"
	// LinkMarkdown is the ordinary [text](dest) form.
	LinkMarkdown LinkKind = "markdown"
)

// Link is one reference extracted from a note, before resolution.
type Link struct {
	// Kind is the syntax it was written in.
	Kind LinkKind
	// Embed is true for "![[x]]" and "![alt](x)" — the target is meant to be
	// displayed in place rather than linked to.
	Embed bool
	// Raw is the exact source text of the link, brackets included.
	Raw string
	// Target is the target part with any alias and anchor removed, trimmed.
	Target string
	// Alias is the display text: after "|" for a wikilink, the bracketed text
	// for a markdown link. Empty when none was given.
	Alias string
	// Heading is the "#…" anchor with the leading "#" removed. Empty when the
	// link named no heading.
	Heading string
	// BlockID is the "#^…" block reference with "#^" removed. Obsidian block
	// anchors are a distinct addressing form from headings and are kept
	// separate so a "#^abc123" is never matched against heading text.
	BlockID string
	// Line is the 1-based line the link starts on.
	Line int
	// Offset is the absolute byte offset of the link's first character within
	// the note. Absolute, so a re-read lands correctly regardless of how the
	// index chose to segment the file (FR-050a(c)).
	Offset int64
}

// Heading is one ATX heading found in a note, in document order.
type Heading struct {
	// Level is 1-6, the number of leading "#" characters.
	Level int
	// Text is the heading text with the markers and surrounding space removed.
	Text string
	// Line is the 1-based line the heading is on.
	Line int
	// Offset is the absolute byte offset of the heading's first character.
	Offset int64
}

// ScanStats reports what the scan actually did. MaxBufferedBytes is the point:
// it is the high-water mark of the scanner's working window, so a test can
// assert that scanning a very large note never buffers anything close to its
// size — a discrete count rather than a memory-profile guess.
type ScanStats struct {
	// BytesRead is the total number of bytes consumed from the reader.
	BytesRead int64
	// MaxBufferedBytes is the largest the scanner's working window ever grew.
	MaxBufferedBytes int
	// Lines is the number of lines seen.
	Lines int
}

// NoteScan is everything a single streaming pass over one note produces.
type NoteScan struct {
	Links    []Link
	Headings []Heading
	Stats    ScanStats
}

const (
	// scanReadChunk is how much is pulled from the reader at a time.
	scanReadChunk = 64 << 10
	// maxSegmentBytes bounds how much of a single unterminated line is held
	// before it is processed in pieces. A note that is one enormous line
	// (minified data, a base64 blob) must not become an allocation the size
	// of the file.
	maxSegmentBytes = 1 << 20
	// maxLinkBytes is the longest construct recognised as a link, and hence
	// the overlap carried between pieces of an over-long line so that a link
	// straddling the split is still seen exactly once.
	maxLinkBytes = 4096
)

// ScanNote reads r to the end and returns every link and heading in it.
//
// The reader is consumed in a fixed window; the note is never held in memory.
func ScanNote(r io.Reader) (NoteScan, error) {
	s := &noteScanner{
		line:        1,
		atLineStart: true,
	}
	// One fixed backing array for the whole scan. Nothing here ever grows
	// with the size of the note: that is the FR-034a property, and
	// ScanStats.MaxBufferedBytes is how a test observes it.
	backing := make([]byte, maxSegmentBytes+maxLinkBytes+scanReadChunk)
	buf := backing[:0]
	chunk := make([]byte, scanReadChunk)
	var bufBase int64 // absolute offset of buf[0] within the note

	for {
		n, err := r.Read(chunk)
		if n > 0 {
			s.stats.BytesRead += int64(n)
			buf = append(buf, chunk[:n]...)
			if len(buf) > s.stats.MaxBufferedBytes {
				s.stats.MaxBufferedBytes = len(buf)
			}
			// Every complete line currently in the window.
			for {
				nl := bytes.IndexByte(buf, '\n')
				if nl < 0 {
					break
				}
				s.feed(buf[:nl], bufBase, true)
				bufBase += int64(nl) + 1
				buf = buf[nl+1:]
			}
			// An unterminated line longer than the bound is processed in
			// pieces, keeping an overlap so a link straddling the seam is
			// still found — and found exactly once, because emit() refuses
			// anything starting at or before the previous link's offset.
			for len(buf) > maxSegmentBytes {
				cut := maxSegmentBytes
				s.feed(buf[:cut], bufBase, false)
				keep := maxLinkBytes
				bufBase += int64(cut - keep)
				buf = buf[cut-keep:]
			}
			// Compact back to the start of the backing array so the window
			// never walks off the end of it.
			if len(buf) > 0 {
				copy(backing, buf)
				buf = backing[:len(buf)]
			} else {
				buf = backing[:0]
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return NoteScan{Links: s.links, Headings: s.headings, Stats: s.stats}, err
		}
	}
	if len(buf) > 0 {
		s.feed(buf, bufBase, false)
	}
	s.stats.Lines = s.line
	return NoteScan{Links: s.links, Headings: s.headings, Stats: s.stats}, nil
}

// ScanNoteBytes is ScanNote over an in-memory note. It exists for callers that
// already hold the bytes; it is not the path a large note takes.
func ScanNoteBytes(src []byte) NoteScan {
	sc, _ := ScanNote(bytes.NewReader(src))
	return sc
}

// ExtractLinks returns every link in src, in document order.
func ExtractLinks(src []byte) []Link { return ScanNoteBytes(src).Links }

// ExtractHeadings returns every ATX heading in src, in document order.
//
// Headings inside fenced code blocks and inside YAML frontmatter are not
// headings — a "# comment" in a shell example is not a section of the note.
func ExtractHeadings(src []byte) []Heading { return ScanNoteBytes(src).Headings }

type noteScanner struct {
	links    []Link
	headings []Heading
	stats    ScanStats

	line        int
	atLineStart bool

	inFence   bool
	fenceChar byte
	fenceLen  int

	inFrontmatter bool
	seenFirstLine bool

	// codeRun is the length of the currently open inline-code backtick run,
	// or 0 when no span is open. Carried across the pieces of an over-long
	// line so a split cannot silently close a span.
	codeRun int

	// offsetFloor is the absolute offset past which a link must start to be
	// emitted. The overlap between pieces of an over-long line re-scans a
	// region; this is what stops the same link being emitted twice.
	offsetFloor int64
}

// feed processes one piece of the note. complete reports whether the piece
// ended at a newline (and hence whether the line counter advances). A piece is
// a true line start unless it is the continuation of an over-long line.
func (s *noteScanner) feed(seg []byte, base int64, complete bool) {
	if s.atLineStart {
		s.codeRun = 0
		s.handleLineStart(seg, base)
	}
	if !s.inFence {
		s.scanLinks(seg, base)
	}
	if complete {
		s.line++
		s.atLineStart = true
	} else {
		s.atLineStart = false
	}
}

func (s *noteScanner) handleLineStart(seg []byte, base int64) {
	trimmed := strings.TrimRight(string(seg), " \t\r")

	if !s.seenFirstLine {
		s.seenFirstLine = true
		if strings.TrimSpace(trimmed) == "---" {
			// YAML frontmatter. Its contents are metadata, not body: a "#tag"
			// line in it is not a heading (US-7 AS-4). Links in it ARE links —
			// a note can name another note in a frontmatter field, and a
			// rename has to rewrite it (US-13 AS-2).
			s.inFrontmatter = true
			return
		}
	}
	if s.inFrontmatter {
		t := strings.TrimSpace(trimmed)
		if t == "---" || t == "..." {
			s.inFrontmatter = false
		}
		return
	}

	if s.inFence {
		if char, n, ok := fenceMarker(trimmed); ok && char == s.fenceChar && n >= s.fenceLen {
			s.inFence = false
		}
		return
	}
	if char, n, ok := fenceMarker(trimmed); ok {
		s.inFence = true
		s.fenceChar = char
		s.fenceLen = n
		return
	}
	if level, text, off, ok := atxHeading(seg); ok {
		s.headings = append(s.headings, Heading{
			Level:  level,
			Text:   text,
			Line:   s.line,
			Offset: base + int64(off),
		})
	}
}

// fenceMarker reports an opening or closing code fence: up to three leading
// spaces then at least three backticks or tildes.
func fenceMarker(line string) (byte, int, bool) {
	i := 0
	for i < len(line) && i < 4 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || i > 3 {
		return 0, 0, false
	}
	c := line[i]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	if j-i < 3 {
		return 0, 0, false
	}
	return c, j - i, true
}

// atxHeading parses "### Title ###" into its level and text. The closing run
// of "#" is decoration and is removed; "#Tag" is NOT a heading, because the
// marker must be followed by whitespace or end of line.
func atxHeading(line []byte) (level int, text string, offset int, ok bool) {
	i := 0
	for i < len(line) && i < 4 && line[i] == ' ' {
		i++
	}
	if i > 3 || i >= len(line) || line[i] != '#' {
		return 0, "", 0, false
	}
	start := i
	for i < len(line) && line[i] == '#' {
		i++
	}
	level = i - start
	if level < 1 || level > 6 {
		return 0, "", 0, false
	}
	if i < len(line) && line[i] != ' ' && line[i] != '\t' {
		return 0, "", 0, false
	}
	body := strings.TrimSpace(strings.TrimRight(string(line[i:]), " \t\r"))
	// Strip a closing "###" run, which is decoration rather than text.
	trimmedRight := strings.TrimRight(body, "#")
	if trimmedRight != body {
		if trimmedRight == "" || strings.HasSuffix(trimmedRight, " ") || strings.HasSuffix(trimmedRight, "\t") {
			body = strings.TrimSpace(trimmedRight)
		}
	}
	return level, body, start, true
}

func (s *noteScanner) emit(l Link) {
	if l.Offset < s.offsetFloor {
		return
	}
	s.offsetFloor = l.Offset + 1
	s.links = append(s.links, l)
}

func (s *noteScanner) scanLinks(seg []byte, base int64) {
	i := 0
	for i < len(seg) {
		c := seg[i]
		if c == '`' {
			j := i
			for j < len(seg) && seg[j] == '`' {
				j++
			}
			run := j - i
			switch {
			case s.codeRun == 0:
				s.codeRun = run
			case s.codeRun == run:
				s.codeRun = 0
			}
			i = j
			continue
		}
		if s.codeRun != 0 {
			i++
			continue
		}
		if c == '!' && i+1 < len(seg) && seg[i+1] == '[' {
			if i+2 < len(seg) && seg[i+2] == '[' {
				if l, next, ok := parseWikilink(seg, i+1, base, true, s.line); ok {
					s.emit(l)
					i = next
					continue
				}
			} else if l, next, ok := parseMarkdownLink(seg, i+1, base, true, s.line); ok {
				s.emit(l)
				i = next
				continue
			}
			i++
			continue
		}
		if c == '[' {
			if i+1 < len(seg) && seg[i+1] == '[' {
				if l, next, ok := parseWikilink(seg, i, base, false, s.line); ok {
					s.emit(l)
					i = next
					continue
				}
				i += 2
				continue
			}
			if l, next, ok := parseMarkdownLink(seg, i, base, false, s.line); ok {
				s.emit(l)
				i = next
				continue
			}
			i++
			continue
		}
		i++
	}
}

// parseWikilink reads "[[target|alias]]" starting at the first "[".
// embed reports whether a "!" preceded it, which is one byte earlier.
func parseWikilink(seg []byte, start int, base int64, embed bool, line int) (Link, int, bool) {
	if start+1 >= len(seg) || seg[start] != '[' || seg[start+1] != '[' {
		return Link{}, 0, false
	}
	limit := start + 2 + maxLinkBytes
	if limit > len(seg) {
		limit = len(seg)
	}
	closeIdx := -1
	for i := start + 2; i+1 < limit; i++ {
		if seg[i] == ']' && seg[i+1] == ']' {
			closeIdx = i
			break
		}
		if seg[i] == '\n' {
			return Link{}, 0, false
		}
	}
	if closeIdx < 0 {
		return Link{}, 0, false
	}
	inner := string(seg[start+2 : closeIdx])
	rawStart := start
	if embed {
		rawStart = start - 1
	}
	l := Link{
		Kind:   LinkWikilink,
		Embed:  embed,
		Raw:    string(seg[rawStart : closeIdx+2]),
		Line:   line,
		Offset: base + int64(rawStart),
	}
	target := inner
	if bar := strings.Index(target, "|"); bar >= 0 {
		l.Alias = strings.TrimSpace(target[bar+1:])
		target = target[:bar]
	}
	if hash := strings.Index(target, "#"); hash >= 0 {
		anchor := target[hash+1:]
		target = target[:hash]
		if strings.HasPrefix(anchor, "^") {
			l.BlockID = strings.TrimSpace(anchor[1:])
		} else {
			l.Heading = strings.TrimSpace(anchor)
		}
	}
	l.Target = strings.TrimSpace(target)
	return l, closeIdx + 2, true
}

// parseMarkdownLink reads "[text](dest \"title\")" starting at "[".
func parseMarkdownLink(seg []byte, start int, base int64, embed bool, line int) (Link, int, bool) {
	if start >= len(seg) || seg[start] != '[' {
		return Link{}, 0, false
	}
	limit := start + maxLinkBytes
	if limit > len(seg) {
		limit = len(seg)
	}
	depth := 0
	textEnd := -1
	for i := start; i < limit; i++ {
		switch seg[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				textEnd = i
			}
		case '\n':
			return Link{}, 0, false
		}
		if textEnd >= 0 {
			break
		}
	}
	if textEnd < 0 || textEnd+1 >= len(seg) || seg[textEnd+1] != '(' {
		return Link{}, 0, false
	}
	depth = 0
	destEnd := -1
	for i := textEnd + 1; i < limit; i++ {
		switch seg[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				destEnd = i
			}
		case '\n':
			return Link{}, 0, false
		}
		if destEnd >= 0 {
			break
		}
	}
	if destEnd < 0 {
		return Link{}, 0, false
	}

	dest := destination(string(seg[textEnd+2 : destEnd]))
	if dest == "" || isExternalTarget(dest) || strings.HasPrefix(dest, "#") {
		// A scheme URL leaves the collection and a bare "#anchor" stays in
		// the same note; neither is an edge in the note graph.
		return Link{}, 0, false
	}
	rawStart := start
	if embed {
		rawStart = start - 1
	}
	l := Link{
		Kind:   LinkMarkdown,
		Embed:  embed,
		Raw:    string(seg[rawStart : destEnd+1]),
		Alias:  strings.TrimSpace(string(seg[start+1 : textEnd])),
		Line:   line,
		Offset: base + int64(rawStart),
	}
	if hash := strings.Index(dest, "#"); hash >= 0 {
		anchor := dest[hash+1:]
		dest = dest[:hash]
		if strings.HasPrefix(anchor, "^") {
			l.BlockID = strings.TrimSpace(anchor[1:])
		} else {
			l.Heading = strings.TrimSpace(anchor)
		}
	}
	// Percent-decoding happens BEFORE containment is ever considered, so an
	// encoded traversal ("..%2F..%2Fetc") is judged as the path it actually
	// denotes rather than as an innocent-looking literal.
	if decoded, err := url.PathUnescape(dest); err == nil {
		dest = decoded
	}
	l.Target = strings.TrimSpace(dest)
	if l.Target == "" {
		return Link{}, 0, false
	}
	return l, destEnd + 1, true
}

// destination extracts the target from the parenthesised part of a markdown
// link, dropping an optional title and unwrapping the <…> form.
func destination(inner string) string {
	d := strings.TrimSpace(inner)
	if strings.HasPrefix(d, "<") {
		if end := strings.Index(d, ">"); end > 0 {
			return unescapeMarkdown(d[1:end])
		}
		return ""
	}
	if sp := strings.IndexAny(d, " \t"); sp >= 0 {
		d = d[:sp]
	}
	return unescapeMarkdown(d)
}

func unescapeMarkdown(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// isExternalTarget reports whether a markdown destination names something
// outside the filesystem entirely. The test is deliberately narrow — it
// requires "//" after the scheme, or one of the small set of schemes that omit
// it — so that a note named "Meeting: 2026-01-01.md" is never mistaken for a
// URL because of its colon.
func isExternalTarget(dest string) bool {
	lower := strings.ToLower(dest)
	if strings.HasPrefix(lower, "//") {
		return true
	}
	for _, s := range []string{"mailto:", "tel:", "data:", "javascript:", "obsidian:"} {
		if strings.HasPrefix(lower, s) {
			return true
		}
	}
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		if c == ':' {
			return i > 0 && strings.HasPrefix(lower[i:], "://")
		}
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '+' && c != '.' && c != '-' {
			return false
		}
	}
	return false
}

// ResolveState is whether a link found its target.
type ResolveState string

const (
	// ResolveResolved — the link names a file in the collection.
	ResolveResolved ResolveState = "resolved"
	// ResolveUnresolved — it does not, for the stated Reason (FR-042).
	ResolveUnresolved ResolveState = "unresolved"
)

// UnresolvedReason says why a link did not resolve. It is machine-readable
// because the three cases are not equivalent: "no_match" is a broken link the
// operator can fix, while "outside_collection" is a link that tried to leave
// the collection and is worth surfacing on its own terms (US-10).
type UnresolvedReason string

const (
	// ReasonNone — the link resolved.
	ReasonNone UnresolvedReason = ""
	// ReasonNoMatch — nothing in the collection carries that path or name.
	ReasonNoMatch UnresolvedReason = "no_match"
	// ReasonEmptyTarget — the link named nothing at all ("[[]]").
	ReasonEmptyTarget UnresolvedReason = "empty_target"
	// ReasonAbsoluteTarget — the link named an absolute filesystem path.
	ReasonAbsoluteTarget UnresolvedReason = "absolute_target"
	// ReasonOutsideRoot — the link traversed out of the collection root.
	ReasonOutsideRoot UnresolvedReason = "outside_collection"
)

// ResolvedLink is a link together with what it resolved to.
type ResolvedLink struct {
	Link
	// From is the collection-relative path of the note containing the link.
	From string
	// State is whether it resolved.
	State ResolveState
	// To is the collection-relative path of the target, empty when unresolved.
	To string
	// Reason is why it did not resolve, empty when it did.
	Reason UnresolvedReason
	// Ambiguous reports that the target name matched more than one note and the
	// tie-break chose one (FR-041). The link still resolves; the ambiguity is
	// reported IN ADDITION, so determinism never hides it.
	Ambiguous bool
	// Candidates lists every note the name matched, in tie-break order, when
	// Ambiguous. Nil otherwise.
	Candidates []string
	// HeadingFound reports whether the "#Heading" anchor matched a heading in
	// the target. Meaningless when the link named no heading.
	HeadingFound bool
	// HeadingLine is the 1-based line of the matched heading, 0 when none.
	HeadingLine int
}

// NoteIndex is the set of paths a link may resolve to, prepared for the
// resolution order of FR-040. It holds collection-relative paths only, so it
// cannot be used to address anything outside the collection at all.
type NoteIndex struct {
	paths  map[string]struct{}
	byBase map[string][]string
	all    []string
}

// NewNoteIndex builds the resolution index from collection-relative paths.
//
// Every file is registered under two names: its full basename
// ("diagram-v3.png") and its basename without extension ("diagram-v3"), so
// that both "[[diagram-v3.png]]" and "[[Target]]" find their file by the same
// mechanism. Duplicate paths collapse; the candidate lists are stored already
// sorted into the FR-040 tie-break order, which is what makes resolution
// independent of the order the filesystem happened to list things in.
func NewNoteIndex(relPaths []string) *NoteIndex {
	ni := &NoteIndex{
		paths:  make(map[string]struct{}, len(relPaths)),
		byBase: make(map[string][]string),
	}
	seenBase := make(map[string]map[string]struct{})
	for _, raw := range relPaths {
		p := normalizeRel(raw)
		if p == "" || p == "." {
			continue
		}
		if _, dup := ni.paths[p]; dup {
			continue
		}
		ni.paths[p] = struct{}{}
		ni.all = append(ni.all, p)

		base := path.Base(p)
		keys := []string{base}
		if stem := trimMarkdownExt(base); stem != base && stem != "" {
			keys = append(keys, stem)
		} else if ext := path.Ext(base); ext != "" {
			if stem := strings.TrimSuffix(base, ext); stem != "" {
				keys = append(keys, stem)
			}
		}
		for _, k := range keys {
			set, ok := seenBase[k]
			if !ok {
				set = make(map[string]struct{})
				seenBase[k] = set
			}
			if _, dup := set[p]; dup {
				continue
			}
			set[p] = struct{}{}
			ni.byBase[k] = append(ni.byBase[k], p)
		}
	}
	sort.Strings(ni.all)
	for k := range ni.byBase {
		sortByTieBreak(ni.byBase[k])
	}
	return ni
}

// Paths returns every indexed path, sorted.
func (ni *NoteIndex) Paths() []string {
	out := make([]string, len(ni.all))
	copy(out, ni.all)
	return out
}

// Has reports whether an exact collection-relative path is indexed.
func (ni *NoteIndex) Has(relPath string) bool {
	_, ok := ni.paths[normalizeRel(relPath)]
	return ok
}

// sortByTieBreak orders candidate paths by FR-040's stated tie-break: shortest
// collection-relative path first, then lexicographically. "Shortest" is
// counted in runes rather than bytes, so a folder named in a non-Latin script
// is not penalised for its UTF-8 encoding — and either measure is identical on
// every machine, which is the property that actually matters (US-11).
func sortByTieBreak(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		li := utf8.RuneCountInString(paths[i])
		lj := utf8.RuneCountInString(paths[j])
		if li != lj {
			return li < lj
		}
		return paths[i] < paths[j]
	})
}

func normalizeRel(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(p, "./")
	if p == "" {
		return ""
	}
	return path.Clean(p)
}

func trimMarkdownExt(name string) string {
	ext := path.Ext(name)
	if strings.EqualFold(ext, ".md") || strings.EqualFold(ext, ".markdown") {
		return strings.TrimSuffix(name, ext)
	}
	return name
}

// IsMarkdownPath reports whether a collection-relative path names a note.
func IsMarkdownPath(relPath string) bool {
	ext := path.Ext(relPath)
	return strings.EqualFold(ext, ".md") || strings.EqualFold(ext, ".markdown")
}

// Resolve applies FR-040's resolution order to one link found in note `from`.
//
// The order, exactly:
//
//  1. Exact collection-relative path, with and without ".md".
//  2. Unique basename across the collection.
//  3. Ambiguous basename: shortest collection-relative path wins.
//  4. Still tied: lexicographically first wins.
//
// and, before any of them, three refusals that are not "no match" and must not
// be reported as one — an empty target, an absolute path, and a target that
// traverses out of the collection (FR-043). Refusing before the lookup is what
// guarantees no syscall is issued for an escaping target: the refusal is
// decided from the text alone.
//
// The basename stages apply to wikilinks only. A relative markdown link is a
// path and is resolved as one; searching the collection for a file that merely
// shares its basename would be a guess, and step 4 of the ADR's algorithm is
// explicit that no match means unresolved, never a guess.
func (ni *NoteIndex) Resolve(from string, l Link) ResolvedLink {
	res := ResolvedLink{Link: l, From: normalizeRel(from), State: ResolveUnresolved, Reason: ReasonNoMatch}

	target := strings.TrimSpace(l.Target)
	if target == "" {
		res.Reason = ReasonEmptyTarget
		return res
	}
	if IsAbsoluteTarget(target) {
		res.Reason = ReasonAbsoluteTarget
		return res
	}

	prefix := ""
	if l.Kind == LinkMarkdown {
		if d := path.Dir(res.From); d != "." && d != "" {
			prefix = d + "/"
		}
	}
	cleaned := path.Clean(prefix + strings.ReplaceAll(target, "\\", "/"))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		res.Reason = ReasonOutsideRoot
		return res
	}
	if cleaned == "." || cleaned == "" {
		res.Reason = ReasonEmptyTarget
		return res
	}

	// A bare wikilink — no slash — is a BASENAME link, not a path. Obsidian
	// treats it as one, and so must the ambiguity report: "[[Index]]" in a
	// collection holding `Index.md`, `a/Index.md` and `b/deep/Index.md` is
	// ambiguous whether or not one of the duplicates happens to sit at the
	// collection root.
	//
	// This is computed BEFORE the exact-path stage and applied to whatever
	// that stage resolves, because a root-level duplicate makes stage 1 match
	// and return, and the ambiguity report would then never be reached. The
	// resolved target is unaffected — FR-040 still gives the exact path
	// precedence — but FR-041's report is not optional, and its loss is the
	// worse failure of the two: the reader believes the link went where they
	// meant and has no way to discover afterwards that it did not.
	basenameMatches := []string(nil)
	if l.Kind == LinkWikilink && !strings.Contains(target, "/") {
		basenameMatches = ni.byBase[target]
		if len(basenameMatches) == 0 {
			if stem := trimMarkdownExt(target); stem != target {
				basenameMatches = ni.byBase[stem]
			}
		}
	}
	markAmbiguous := func(r *ResolvedLink) {
		if len(basenameMatches) > 1 {
			r.Ambiguous = true
			r.Candidates = append([]string(nil), basenameMatches...)
		}
	}

	// 1. Exact path, with and without .md.
	candidates := []string{cleaned}
	if !IsMarkdownPath(cleaned) {
		candidates = append(candidates, cleaned+".md")
	}
	for _, c := range candidates {
		if _, ok := ni.paths[c]; ok {
			res.State = ResolveResolved
			res.To = c
			res.Reason = ReasonNone
			markAmbiguous(&res)
			return res
		}
	}

	// 2-4. Basename, then the tie-break.
	if len(basenameMatches) > 0 {
		res.State = ResolveResolved
		res.To = basenameMatches[0]
		res.Reason = ReasonNone
		markAmbiguous(&res)
		return res
	}

	return res
}
