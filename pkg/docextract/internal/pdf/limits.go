// Parser safety bounds for the vendored PDF reader (issue #1968, security
// finding F1). Upstream github.com/ledongthuc/pdf trusts the file's own
// structure: a self-referencing /Parent loops forever, an xref stream's
// /Size sizes an allocation, compressed streams expand without limit, and
// the extracted text grows without limit. Every bound below is a property
// of the parser, never a provider quota: provider document limits come from
// catalogue data only.
//
// This package is a vendored copy of github.com/ledongthuc/pdf
// v0.0.0-20250511090121-5959a4027728 (BSD-3-Clause, see LICENSE), itself a
// fork of rsc.io/pdf. Changes from upstream: every Reader carries Limits
// (work and text budgets) and the bounds below; the layout APIs (styled
// text, columns, rows, content, outline), the path-based Open and the
// stdout debug output are removed; Reader.GetPlainText walks the page tree
// once; Font.Encoder caches its encoder.

package pdf

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Structural bounds. Real documents stay far below each of them; a file
// past one is treated as malformed.
const (
	// maxXrefEntries bounds the cross-reference table (object count).
	maxXrefEntries = 1 << 20
	// maxXrefSections bounds the /Prev chain of cross-reference sections.
	maxXrefSections = 1 << 10
	// maxXrefFieldWidth bounds one /W field of an xref stream, in bytes.
	maxXrefFieldWidth = 8
	// maxResolveDepth bounds nested indirect-object resolution (an object
	// stream whose /Length lives in another object stream, and so on).
	maxResolveDepth = 64
	// maxObjectNesting bounds nested arrays and dictionaries.
	maxObjectNesting = 256
	// maxObjectElements bounds the entries of one array or dictionary.
	maxObjectElements = 1 << 20
	// maxTokenBytes bounds one string, name or keyword token.
	maxTokenBytes = 1 << 24
	// maxOperandStack bounds the content-stream operand stack.
	maxOperandStack = 1 << 16
	// maxDictStack bounds the begin/end dictionary stack of a cmap program.
	maxDictStack = 64
	// maxPredictorColumns bounds the PNG predictor row width.
	maxPredictorColumns = 1 << 16
	// maxInheritDepth bounds the /Parent walk of an inherited page attribute.
	maxInheritDepth = 256
	// resolveWork is the work charged for one indirect-object resolution,
	// on top of the bytes its lexing consumes. A resolution costs about as
	// much CPU as lexing a kilobyte.
	resolveWork = 1024
	// maxFilterStages bounds a stream's /Filter chain. Every stage is a
	// decoder built (and retained) before the first byte is read, so the
	// chain length is checked before any is constructed; real documents use
	// one or two stages.
	maxFilterStages = 16
	// filterStageWork is the work charged for building one filter stage.
	filterStageWork = resolveWork
	// defaultParseTime is the wall-clock ceiling of one Reader when
	// Limits.Time is zero: defence in depth behind the work budget, for a
	// loop whose meter is too cheap. It is sampled, not preemptive: the
	// clock is read every clockCheckEvery charges (every xref row, cmap
	// entry, interpreter reuse and TJ element charges), so a loop that
	// charges nothing is not covered.
	// Ordinary documents parse in well under a second; ten seconds leaves
	// room for the largest accepted files on a loaded host.
	defaultParseTime = 10 * time.Second
	// clockCheckEvery is how many charges pass between two clock reads.
	clockCheckEvery = 1024
	// maxCmapEntries bounds the codespace ranges, distinct bfchar codes and
	// bfranges one cmap keeps (real ToUnicode maps hold tens of thousands).
	maxCmapEntries = 1 << 20
	// cmapEntryWork is the work charged for keeping one cmap entry, about
	// the bytes it occupies with its share of the index, so the memory a
	// cmap retains stays within the work budget.
	cmapEntryWork = 64
)

var (
	// ErrWorkLimit: the document needs more parsing work (bytes decoded,
	// bytes lexed, objects resolved, glyph lookups) than the reader's
	// Limits.Work allows, or more wall-clock time than Limits.Time. Parsing
	// stops at the moment the budget runs out.
	ErrWorkLimit = errors.New("pdf: parser work limit exceeded")
	// ErrTextLimit: the extracted text reached the reader's Limits.Text.
	ErrTextLimit = errors.New("pdf: extracted text limit reached")
	// ErrStructureLimit: a structural bound above was exceeded.
	ErrStructureLimit = errors.New("pdf: structure exceeds parser safety bounds")
)

// Limits are the per-reader budgets the caller chooses. Work and Text are
// required and must be positive: a reader without budgets does not exist.
type Limits struct {
	// Work bounds the total parsing work, in roughly byte-sized units.
	Work int64
	// Text bounds the total bytes of extracted text across all pages.
	Text int64
	// Time bounds the wall-clock time from NewReader to the end of the
	// last parse; zero means defaultParseTime, a negative value is refused.
	Time time.Duration
}

// limiter is the mutable remainder of a Reader's Limits. A Reader is used
// by one goroutine at a time.
type limiter struct {
	work     int64
	text     int64
	depth    int
	textStop bool // a decoder stopped at the text budget (takeTextStop)
	deadline time.Time
	charges  int // charges since the last clock read
}

func newLimiter(lim Limits) *limiter {
	d := lim.Time
	if d == 0 {
		d = defaultParseTime
	}
	return &limiter{work: lim.Work, text: lim.Text, deadline: time.Now().Add(d)}
}

// charge spends units of work; when the budget is gone, or the wall-clock
// ceiling has passed, it panics with ErrWorkLimit, which unwinds the parse
// at once.
func (r *Reader) charge(units int64) {
	if r == nil || r.lim == nil {
		return
	}
	r.lim.work -= units
	if r.lim.work < 0 {
		panic(ErrWorkLimit)
	}
	r.lim.charges++
	if r.lim.charges >= clockCheckEvery {
		r.checkClock()
	}
}

// chargeReuse charges for handing out an already-parsed value again without
// lexing it again: an interpreter name bound with def and pushed by a short
// reference, or an operand an operator pushes back. The lexer charged the
// value once, but every consumer walks or hashes it again (TJ visits each
// array element, def and the dictionary lookups hash a name, a decoder reads
// a string), so each reuse costs its size: one unit per string or name byte
// and per array or dictionary entry, and at least one unit.
func (r *Reader) chargeReuse(x object) {
	units := int64(1)
	switch x := x.(type) {
	case string:
		units += int64(len(x))
	case name:
		units += int64(len(x))
	case array:
		units += int64(len(x))
	case dict:
		units += int64(len(x))
	}
	r.charge(units)
}

// checkClock ends the parse with ErrWorkLimit once the reader's wall-clock
// ceiling has passed.
func (r *Reader) checkClock() {
	if r == nil || r.lim == nil {
		return
	}
	r.lim.charges = 0
	if time.Now().After(r.lim.deadline) {
		panic(fmt.Errorf("%w: parse ran past its time ceiling", ErrWorkLimit))
	}
}

// chargeText spends n bytes of the text budget; when the budget is gone it
// panics with ErrTextLimit.
func (r *Reader) chargeText(n int) {
	if r == nil || r.lim == nil {
		return
	}
	r.lim.text -= int64(n)
	if r.lim.text < 0 {
		panic(ErrTextLimit)
	}
}

// textRoom is the text budget still left (unlimited without a budget).
func (r *Reader) textRoom() int64 {
	if r == nil || r.lim == nil {
		return math.MaxInt64
	}
	return r.lim.text
}

// noteTextStop latches that a decoder stopped at the text budget; the text
// emitter reads it with takeTextStop after writing the decoded prefix.
func (r *Reader) noteTextStop(stopped bool) {
	if r == nil || r.lim == nil || !stopped {
		return
	}
	r.lim.textStop = true
}

// takeTextStop reports and clears the decoder's text-budget stop.
func (r *Reader) takeTextStop() bool {
	if r == nil || r.lim == nil {
		return false
	}
	stopped := r.lim.textStop
	r.lim.textStop = false
	return stopped
}

// growXref grows table's capacity past x exactly as the upstream loop did
// (each step extends the length to the capacity). x must lie below
// maxXrefEntries, and every new slot costs one unit of work, so a forged
// object number can neither wrap past the admission checks nor allocate
// without bound.
func (r *Reader) growXref(table []xref, x int) []xref {
	if x < 0 || x >= maxXrefEntries {
		structuralPanicf("xref entry %d outside 0..%d", x, maxXrefEntries-1)
	}
	if cap(table) <= x {
		r.charge(int64(x + 1 - cap(table)))
	}
	for cap(table) <= x {
		table = append(table[:cap(table)], xref{})
	}
	return table
}

// structuralPanicf aborts the parse with an ErrStructureLimit error.
func structuralPanicf(format string, args ...any) {
	panic(fmt.Errorf("%w: %s", ErrStructureLimit, fmt.Sprintf(format, args...)))
}

// chargedReader charges every byte it yields to its Reader's work budget.
type chargedReader struct {
	owner *Reader
	rd    interface{ Read(p []byte) (int, error) }
}

func (c *chargedReader) Read(p []byte) (int, error) {
	n, err := c.rd.Read(p)
	c.owner.charge(int64(n))
	return n, err
}
