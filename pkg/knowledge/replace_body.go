// Body-replace: the second unbuilt primitive ADR-068 D14.1 names, and the one
// FR-047 gives its own ambiguity rule rather than leaving to whoever writes
// the function (ADR-068 D14.1, D15.3 "replace_body"; spec Draft 9 FR-047,
// §4.1.4, AC-E3).
//
// # Why this exists as its own file
//
// Every shipped NoteEdit constructor in author.go is ADDITIVE — SetProperty
// replaces one frontmatter value, AppendSectionAt only ever appends. Neither
// can express "replace part of what is already there", and that operation
// needed real design content, not a relabelling: an anchor that matches twice
// is a silent-corruption hazard with no additive analogue, because there is
// no "append" reading of "which occurrence did you mean?"
//
// # What an anchor IS, and why (FR-047 Deliverable 1)
//
// An anchor is an EXACT, LITERAL byte sequence — no trimming, no whitespace
// collapsing, no case folding, no line-ending normalisation. That is a
// deliberate, narrow choice, made from the position of the caller that will
// actually construct one: a language model that has just called knowledge_read.
// The model did not derive the anchor from a mental model of the file — it
// COPIED it from text knowledge_read just handed back. The one thing it can do
// reliably is reproduce that copy exactly; the one thing it cannot do
// reliably is guess which of several plausible normalisations (trim trailing
// space? fold "smart quotes"? treat CRLF and LF as the same?) the server
// applied before comparing. A fuzzy match trades a caller's occasional typo
// for a caller's occasional SILENT wrong match — trimming or folding can turn
// two lines that are visibly different in the file into the same anchor,
// which is a second, quieter route to exactly the ambiguity bug this file
// exists to refuse loudly. Exact-byte matching cannot do that: every match it
// reports is a match the file's own bytes actually contain, byte for byte.
//
// A literal anchor also makes disambiguation self-service. FR-047's remedy is
// "give a unique anchor" — that only means something if a caller CAN widen
// their anchor to include more surrounding, distinguishing text and expect it
// to behave predictably. A match rule with hidden normalisation makes that
// unpredictable; an exact substring match makes it mechanical: add a line of
// context, the match narrows.
//
// The anchor is matched only within the note's BODY — the bytes after any
// YAML frontmatter block, never inside it. replace_body is a body operation
// (FR-047's own framing: "an operation of knowledge_edit"); set_property already
// owns frontmatter (FR-040..FR-040b), and letting an anchor match text that
// happens to appear inside a frontmatter value would let a body edit silently
// corrupt metadata it was never meant to touch.
//
// # Addressing by line range
//
// The alternative FR-047 names is a 1-based, inclusive line range, counted
// over the WHOLE FILE — the same numbering a text editor or knowledge_read's
// rendered output shows, and the same numbering AmbiguousAnchorError reports
// its matches in, so a caller reading one refusal can use the other
// addressing mode without converting line numbers by hand. A range that
// reaches outside the file, or that overlaps the frontmatter block, is
// refused (FR-047's "a line range outside the file is likewise refused",
// extended here to frontmatter for the same reason anchor matching is scoped
// to the body: this is a body tool).
//
// # Whitespace and blank lines around the matched span (Deliverable 5)
//
// Stated plainly, because a whitespace-blind comparison hiding a real
// difference is a standing lesson on this project: NOTHING outside the
// matched span is touched, ever, by construction — both replacement paths
// are a literal three-way byte split (prefix + replacement + suffix), so
// there is no code path that could normalise, trim or re-flow a neighbouring
// blank line even by accident.
//
//   - Anchor replace: exactly the matched bytes are removed and body is
//     spliced in their place. Whatever line terminator, leading indent or
//     blank line sat immediately before or after the anchor in the file is
//     untouched — if the caller wants their replacement to end with a
//     newline, body must contain one; none is added for them.
//   - Line-range replace: the span handed to the splice runs from the START
//     of the first named line to the START of the line AFTER the last named
//     line — i.e. it consumes the LAST replaced line's own terminator (or
//     runs to EOF when the last line has none). A line range names WHOLE
//     LINES, so the replacement is kept on whole lines too: when body is
//     non-empty, does not itself end with a newline, and further lines
//     follow in the file, the terminator the range consumed is written
//     back after body (in the file's own LF/CRLF form) — the last line of
//     body never abuts the first surviving line (UAT 2026-09-13 D-05). An
//     empty body deletes the whole lines, terminator included.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Sentinel errors for the replace_body primitive, so a caller can distinguish
// classes with errors.Is without parsing message text.
var (
	// ErrReplaceBodySpec means the caller's request shape itself is invalid —
	// neither addressing mode given, both given, or an anchor with no
	// content. Distinct from AmbiguousAnchorError / AnchorNotFoundError /
	// LineRangeError, which are about a VALID request that could not be
	// resolved against THIS file.
	ErrReplaceBodySpec = errors.New("knowledge: replace_body requires exactly one of anchor or line_range")

	// ErrAmbiguousAnchor is the class behind *AmbiguousAnchorError.
	ErrAmbiguousAnchor = errors.New("knowledge: anchor matched more than once")

	// ErrAnchorNotFound is the class behind *AnchorNotFoundError.
	ErrAnchorNotFound = errors.New("knowledge: anchor not found in the note's body")

	// ErrLineRangeInvalid is the class behind *LineRangeError.
	ErrLineRangeInvalid = errors.New("knowledge: line_range does not address a replaceable span")
)

// AnchorMatch is one place an anchor was found, reported so a caller can pick
// among several rather than have one picked for them.
//
// Line is 1-based and counted over the WHOLE FILE (frontmatter included),
// matching what a text editor shows and what LineRangeError's Start/End
// already mean — so a caller reading "lines 14 and 88" can retry with
// line_range: {14, 14} without converting anything.
type AnchorMatch struct {
	Line int
}

// AmbiguousAnchorError is FR-047's central requirement made a typed value: an
// anchor that matched more than once is REFUSED, naming every match, rather
// than silently resolved by picking the first (or the last, or any other
// unstated rule). The file is left byte-identical (AC-E3) — this error is
// always returned instead of a replacement, never alongside one.
type AmbiguousAnchorError struct {
	Path    string
	Anchor  string
	Matches []AnchorMatch
}

// Error renders every match, never a truncated "first two" summary — FR-047
// says "naming every match", and a caller choosing between five occurrences
// needs to see all five, not be told there were "more".
func (e *AmbiguousAnchorError) Error() string {
	lines := make([]string, len(e.Matches))
	for i, m := range e.Matches {
		lines[i] = strconv.Itoa(m.Line)
	}
	var where string
	switch len(lines) {
	case 2:
		where = fmt.Sprintf("appears twice in %s (lines %s and %s)", e.Path, lines[0], lines[1])
	default:
		where = fmt.Sprintf("appears %d times in %s (lines %s)", len(lines), e.Path, strings.Join(lines, ", "))
	}
	return fmt.Sprintf("anchor %q %s — no change made; give a unique anchor or a line_range", e.Anchor, where)
}

// Unwrap exposes the sentinel so errors.Is(err, ErrAmbiguousAnchor) works
// regardless of which addressing mode a caller used.
func (e *AmbiguousAnchorError) Unwrap() error { return ErrAmbiguousAnchor }

// AnchorNotFoundError is the DISTINCT refusal for zero matches — deliberately
// a different type from AmbiguousAnchorError (Deliverable 3), because the
// caller's remedy is the opposite of the ambiguous case: an ambiguous anchor
// needs to be made MORE specific, a not-found anchor needs to be re-copied
// (or loosened) — collapsing both into one message would mean showing a
// caller the wrong instruction half the time.
type AnchorNotFoundError struct {
	Path   string
	Anchor string
}

// Error names the remedy: re-read the note and copy the anchor exactly,
// because the leading cause of a spurious not-found is a caller who typed the
// anchor from memory rather than copying it out of a knowledge_read response
// (whitespace and line endings are matched exactly — see the anchor-addressing
// note above).
func (e *AnchorNotFoundError) Error() string {
	return fmt.Sprintf(
		"anchor %q not found in %s — no change made; knowledge_read the note again and copy the anchor "+
			"text exactly (matching is byte-exact: whitespace, case and line endings all matter), "+
			"or address the span with a line_range instead",
		e.Anchor, e.Path,
	)
}

// Unwrap exposes the sentinel.
func (e *AnchorNotFoundError) Unwrap() error { return ErrAnchorNotFound }

// LineRangeError is the line_range-addressing refusal: the range reaches
// outside the file, or overlaps the frontmatter block that replace_body does
// not own.
type LineRangeError struct {
	Path             string
	Start, End       int
	Reason           string // "frontmatter" or "outside"
	TotalLines       int
	FrontmatterLines int // valid only when Reason == "frontmatter"
}

// Error names the reason and the file's actual bounds, so a caller can pick a
// valid range without a second round trip to discover them by trial.
func (e *LineRangeError) Error() string {
	switch e.Reason {
	case "frontmatter":
		return fmt.Sprintf(
			"line_range %d-%d overlaps %s's frontmatter block (lines 1-%d) — no change made; "+
				"replace_body only addresses the body, use knowledge_edit set_property for frontmatter",
			e.Start, e.End, e.Path, e.FrontmatterLines,
		)
	default:
		return fmt.Sprintf(
			"line_range %d-%d is outside %s, which has %d line(s) — no change made",
			e.Start, e.End, e.Path, e.TotalLines,
		)
	}
}

// Unwrap exposes the sentinel.
func (e *LineRangeError) Unwrap() error { return ErrLineRangeInvalid }

// LineRange is a 1-based, inclusive line span, counted over the whole file.
type LineRange struct {
	Start, End int
}

// ReplaceBody returns a NoteEdit for knowledge_edit's "replace_body" operation
// (FR-047). Exactly one of anchor (non-empty) or lineRange (non-nil) must be
// given; path is used only to render refusal messages that name the file, as
// FR-047's normative wording does.
//
// path is a constructor argument rather than something read off the src bytes
// EditNote hands the returned NoteEdit, because a NoteEdit's signature —
// func(src []byte) ([]byte, error) — carries no path, and FR-047's messages
// must name one. This mirrors how ConflictError gets its path: from the layer
// that already knows it, not invented inside the closure.
func ReplaceBody(path string, anchor string, lineRange *LineRange, body string) NoteEdit {
	switch {
	case anchor != "" && lineRange != nil:
		return failingEdit(fmt.Errorf("%w: %s: send anchor or line_range, not both", ErrReplaceBodySpec, path))
	case anchor != "":
		return ReplaceBodyByAnchor(path, anchor, body)
	case lineRange != nil:
		return ReplaceBodyByLineRange(path, lineRange.Start, lineRange.End, body)
	default:
		return failingEdit(fmt.Errorf("%w: %s: send anchor or line_range", ErrReplaceBodySpec, path))
	}
}

// ReplaceBodyReport is what a successful replace_body actually did (UAT
// 2026-09-13 D-18): an anchor replaces ONLY its own bytes, and a caller who
// sent `anchor: "Budget:"` with a whole-sentence body got the sentence
// spliced after the label with the old sentence still in place, reported as
// a plain "(changed)". The report says which span went and what stayed.
type ReplaceBodyReport struct {
	Mode          string // "anchor" or "line_range"
	Line          int    // anchor: the 1-based line the match starts on
	AnchorBytes   int    // anchor: the matched byte count
	KeptBefore    string // anchor: text on the same line before the match, if any
	KeptAfter     string // anchor: text on the same line after the match, if any
	Start, End    int    // line_range: the lines replaced
	BodyLineCount int    // lines the replacement text spans
}

// Describe renders the report for the tool reply. Empty for a line_range
// (the caller named the lines; nothing was kept on them).
func (r ReplaceBodyReport) Describe() string {
	if r.Mode != "anchor" {
		return ""
	}
	s := fmt.Sprintf("replaced only the %d-byte anchor on line %d", r.AnchorBytes, r.Line)
	var kept []string
	if r.KeptBefore != "" {
		kept = append(kept, fmt.Sprintf("before it: %q", r.KeptBefore))
	}
	if r.KeptAfter != "" {
		kept = append(kept, fmt.Sprintf("after it: %q", r.KeptAfter))
	}
	if len(kept) > 0 {
		s += "; kept on that line " + strings.Join(kept, ", ") + " — to replace the whole line, make the anchor the whole line or use line_range"
	}
	return s
}

// ReplaceBodyReporting is ReplaceBody plus the D-18 report, filled in when
// the edit runs successfully.
func ReplaceBodyReporting(path string, anchor string, lineRange *LineRange, body string, report *ReplaceBodyReport) NoteEdit {
	inner := ReplaceBody(path, anchor, lineRange, body)
	return func(src []byte) ([]byte, error) {
		out, err := inner(src)
		if err != nil || report == nil {
			return out, err
		}
		switch {
		case anchor != "":
			bodyStart, _ := replaceBodyOffset(src)
			matches := findAnchorMatches(src, bodyStart, []byte(anchor))
			if len(matches) == 1 {
				off := matches[0]
				lineStart := bytes.LastIndexByte(src[:off], '\n') + 1
				lineEnd := off + len(anchor)
				if i := bytes.IndexByte(src[lineEnd:], '\n'); i >= 0 {
					lineEnd += i
				} else {
					lineEnd = len(src)
				}
				*report = ReplaceBodyReport{
					Mode: "anchor", Line: replaceBodyLineAt(src, off), AnchorBytes: len(anchor),
					KeptBefore: string(src[lineStart:off]),
					KeptAfter:  strings.TrimRight(string(src[off+len(anchor):lineEnd]), "\r"),
				}
				// A multi-line anchor keeps nothing "on that line" after it in
				// any useful sense; only report the single-line kept tail.
				if strings.Contains(anchor, "\n") {
					report.KeptBefore, report.KeptAfter = "", ""
				}
			}
		case lineRange != nil:
			*report = ReplaceBodyReport{Mode: "line_range", Start: lineRange.Start, End: lineRange.End}
		}
		report.BodyLineCount = strings.Count(body, "\n") + 1
		return out, nil
	}
}

// failingEdit returns a NoteEdit that always refuses with err, without
// touching src — used for request-shape errors that are wrong independent of
// any file's content, so there is no reason to read src at all.
func failingEdit(err error) NoteEdit {
	return func([]byte) ([]byte, error) { return nil, err }
}

// ReplaceBodyByAnchor returns a NoteEdit that replaces the SOLE occurrence of
// anchor within the note's body with body. Zero occurrences and two-or-more
// occurrences are both refused, as two DISTINCT typed errors — see
// AnchorNotFoundError and AmbiguousAnchorError.
func ReplaceBodyByAnchor(path, anchor, body string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if anchor == "" {
			return nil, fmt.Errorf("%w: %s: anchor must not be empty", ErrReplaceBodySpec, path)
		}
		bodyStart, err := replaceBodyOffset(src)
		if err != nil {
			return nil, fmt.Errorf("knowledge: %s: %w", path, err)
		}
		matches := findAnchorMatches(src, bodyStart, []byte(anchor))
		switch len(matches) {
		case 0:
			return nil, &AnchorNotFoundError{Path: path, Anchor: anchor}
		case 1:
			off := matches[0]
			out := make([]byte, 0, len(src)-len(anchor)+len(body))
			out = append(out, src[:off]...)
			out = append(out, body...)
			out = append(out, src[off+len(anchor):]...)
			return out, nil
		default:
			ms := make([]AnchorMatch, len(matches))
			for i, off := range matches {
				ms[i] = AnchorMatch{Line: replaceBodyLineAt(src, off)}
			}
			return nil, &AmbiguousAnchorError{Path: path, Anchor: anchor, Matches: ms}
		}
	}
}

// ReplaceBodyByLineRange returns a NoteEdit that replaces file lines
// [start, end] (1-based, inclusive, whole-file numbering) with body. The
// range must lie entirely within the body — not the frontmatter block — and
// entirely within the file.
func ReplaceBodyByLineRange(path string, start, end int, body string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if start < 1 || end < start {
			return nil, fmt.Errorf(
				"%w: %s: line_range %d-%d is not a valid ascending 1-based range (start must be >= 1 and <= end)",
				ErrLineRangeInvalid, path, start, end,
			)
		}
		bodyStart, err := replaceBodyOffset(src)
		if err != nil {
			return nil, fmt.Errorf("knowledge: %s: %w", path, err)
		}
		firstBodyLine := replaceBodyLineAt(src, bodyStart)
		offsets := replaceBodyLineOffsets(src)
		totalLines := len(offsets) - 1

		if start < firstBodyLine {
			return nil, &LineRangeError{
				Path: path, Start: start, End: end,
				Reason: "frontmatter", TotalLines: totalLines, FrontmatterLines: firstBodyLine - 1,
			}
		}
		if end > totalLines {
			return nil, &LineRangeError{
				Path: path, Start: start, End: end,
				Reason: "outside", TotalLines: totalLines,
			}
		}

		spanStart := offsets[start-1]
		spanEnd := offsets[end]
		// The span consumes the LAST replaced line's own terminator, so a
		// replacement that does not end with one would weld its final line
		// onto the first surviving line after the range ("YYY" + "DDD4" ->
		// "YYYDDD4" — UAT 2026-09-13 D-05). A line-range replacement is a
		// replacement of whole LINES: the terminator the range took is put
		// back, in the file's own form (LF or CRLF), whenever body is
		// non-empty, does not already end with one, and lines follow. An
		// empty body is a deletion of the whole lines, terminator included,
		// which is what a caller deleting lines means.
		terminator := lineTerminatorBefore(src, spanEnd)
		out := make([]byte, 0, spanStart+len(body)+len(terminator)+(len(src)-spanEnd))
		out = append(out, src[:spanStart]...)
		out = append(out, body...)
		if body != "" && terminator != "" && !strings.HasSuffix(body, "\n") && spanEnd < len(src) {
			out = append(out, terminator...)
		}
		out = append(out, src[spanEnd:]...)
		return out, nil
	}
}

// lineTerminatorBefore returns the line terminator that ends at byte offset
// end — "\r\n", "\n", or "" when the byte before end is not a newline (the
// span ran to EOF on a file whose last line has no terminator).
func lineTerminatorBefore(src []byte, end int) string {
	if end <= 0 || end > len(src) || src[end-1] != '\n' {
		return ""
	}
	if end >= 2 && src[end-2] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// replaceBodyOffset returns the byte offset where the note's BODY starts —
// the first byte after a frontmatter block's closing fence line, or 0 when
// there is no frontmatter block at all. Reuses fmParse (author.go) rather
// than re-deriving frontmatter detection, so replace_body and set_property
// agree on where one ends and the other begins.
//
// A frontmatter block whose opening fence has no closing fence is refused
// outright — fmParse's own ErrFrontmatterUnterminated — rather than guessed
// at, because there is no principled place to say the body starts inside a
// file whose frontmatter is already malformed.
func replaceBodyOffset(src []byte) (int, error) {
	block, err := fmParse(src)
	if err != nil {
		return 0, err
	}
	if !block.present {
		return 0, nil
	}
	_, next, ok := authorLineAt(src, block.innerEnd)
	if !ok {
		// The closing fence is the file's last line with no trailing
		// newline: there is no body at all, and the body "starts" at EOF.
		return len(src), nil
	}
	return next, nil
}

// replaceBodyLineAt reports the 1-based line number containing byte offset,
// counted over the whole file. offset is clamped to len(src) so a caller may
// pass an offset that sits exactly at EOF (an empty body after the closing
// fence) without going out of range.
func replaceBodyLineAt(src []byte, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}
	return 1 + bytes.Count(src[:offset], []byte("\n"))
}

// replaceBodyLineOffsets returns the byte offset at which each line starts,
// plus a trailing sentinel equal to len(src). Line i (1-based) therefore
// spans [offsets[i-1], offsets[i]) for i < len(offsets), and
// len(offsets)-1 is the file's total line count. An empty file has zero
// lines (offsets == []int{0}).
func replaceBodyLineOffsets(src []byte) []int {
	offsets := []int{0}
	pos := 0
	for {
		_, next, ok := authorLineAt(src, pos)
		if !ok {
			break
		}
		pos = next
		offsets = append(offsets, pos)
	}
	return offsets
}

// findAnchorMatches returns the byte offset of every NON-OVERLAPPING
// occurrence of anchor within src[bodyStart:], left to right, as absolute
// offsets into src. "Non-overlapping" matches strings.Count's own definition
// (and Go's bytes.Index family): after a match, the search resumes just past
// it, so "aa" against "aaa" reports one match, not two. That is the ordinary,
// unsurprising reading of "how many times does X appear" and is documented
// here because a caller constructing an adversarial fixture needs to know
// which convention to expect.
func findAnchorMatches(src []byte, bodyStart int, anchor []byte) []int {
	if len(anchor) == 0 {
		return nil
	}
	var offsets []int
	body := src[bodyStart:]
	pos := 0
	for pos <= len(body) {
		idx := bytes.Index(body[pos:], anchor)
		if idx < 0 {
			return offsets
		}
		offsets = append(offsets, bodyStart+pos+idx)
		pos += idx + len(anchor)
	}
	return offsets
}
