package pdf

// #1968 FIX8 (gate 4: SEC4-1, SF4-1, SF4-2, SEC4-2/T1, G4-T1..T3). Tests that
// do not read the parser's own work counter as their only evidence, and that
// go through the document (a ToUnicode program and a page), so they do not
// depend on how the cmap tables are stored:
//   - a reference linear-scan decoder written here from the legacy rules (the
//     first codespace range of each code length that holds the code, then the
//     first bfchar, then the first bfrange) is compared with the parser on fixed
//     edge cases and seeded random tables;
//   - a codespace miss is a genuine miss (proved against the reference) and the
//     lookup cost is judged by real elapsed time on a fixture whose cost grows
//     with the number of ranges examined, so an uncharged linear scan cannot
//     hide behind the limiter's own remainder;
//   - long bfchar origins and duplicate-heavy bfchar tables are bounded in
//     time and allocation.

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- reference decoder ------------------------------------------------------

type fix8Range struct{ lo, hi string }

type fix8BfRange struct {
	lo, hi string
	arr    []string // destination array (nil: use str)
	str    string   // destination string
}

type fix8Char struct{ orig, repl string }

// fix8Table is a cmap in the reference's own terms.
type fix8Table struct {
	space  [4][]fix8Range
	chars  []fix8Char
	ranges []fix8BfRange
}

func fix8Hex(s string) string { return fmt.Sprintf("<%X>", []byte(s)) }

// program renders the table as a ToUnicode program. The parser pops the
// operands of an endbf... operator from the end, so the entries are written in
// reverse: the table's first entry is the last one in the program text, and
// "first match wins" refers to the table order used by decode.
func (tb fix8Table) program() string {
	var b strings.Builder
	for n := 0; n < 4; n++ {
		if len(tb.space[n]) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%d begincodespacerange\n", len(tb.space[n]))
		for i := len(tb.space[n]) - 1; i >= 0; i-- {
			r := tb.space[n][i]
			fmt.Fprintf(&b, "%s %s\n", fix8Hex(r.lo), fix8Hex(r.hi))
		}
		b.WriteString("endcodespacerange\n")
	}
	if len(tb.chars) > 0 {
		fmt.Fprintf(&b, "%d beginbfchar\n", len(tb.chars))
		for i := len(tb.chars) - 1; i >= 0; i-- {
			c := tb.chars[i]
			fmt.Fprintf(&b, "%s %s\n", fix8Hex(c.orig), fix8Hex(c.repl))
		}
		b.WriteString("endbfchar\n")
	}
	if len(tb.ranges) > 0 {
		fmt.Fprintf(&b, "%d beginbfrange\n", len(tb.ranges))
		for i := len(tb.ranges) - 1; i >= 0; i-- {
			r := tb.ranges[i]
			if r.arr != nil {
				parts := make([]string, len(r.arr))
				for i, a := range r.arr {
					parts[i] = fix8Hex(a)
				}
				fmt.Fprintf(&b, "%s %s [%s]\n", fix8Hex(r.lo), fix8Hex(r.hi), strings.Join(parts, " "))
			} else {
				fmt.Fprintf(&b, "%s %s %s\n", fix8Hex(r.lo), fix8Hex(r.hi), fix8Hex(r.str))
			}
		}
		b.WriteString("endbfrange\n")
	}
	return b.String()
}

// fix8Utf16 is the UTF-16BE decode every replacement goes through.
func fix8Utf16(s string) string {
	var u []rune
	for i := 0; i+1 < len(s); i += 2 {
		u = append(u, rune(uint16(s[i])<<8|uint16(s[i+1])))
	}
	return string(u) // BMP only in these fixtures: surrogates are not generated
}

// decode is the legacy linear scan: for each position try code lengths 1 to 4;
// the first codespace range of that length holding the code (string comparison)
// matches it; the code maps through the first bfchar of that exact length and
// value, else the first bfrange whose low bound has that length and
// lo <= code <= hi (string comparison), else U+FFFD. A code in no codespace
// range emits U+FFFD and consumes one byte.
func (tb fix8Table) decode(raw string) string {
	var out strings.Builder
	for len(raw) > 0 {
		matched := false
		for n := 1; n <= 4 && n <= len(raw) && !matched; n++ {
			code := raw[:n]
			for _, sp := range tb.space[n-1] {
				if sp.lo <= code && code <= sp.hi {
					matched = true
					break
				}
			}
			if matched {
				raw = raw[n:]
				out.WriteString(tb.mapCode(code))
			}
		}
		if !matched {
			out.WriteString("�")
			raw = raw[1:]
		}
	}
	return out.String()
}

func (tb fix8Table) mapCode(code string) string {
	n := len(code)
	for _, c := range tb.chars {
		if len(c.orig) == n && c.orig == code {
			return fix8Utf16(c.repl)
		}
	}
	for _, r := range tb.ranges {
		if len(r.lo) == n && r.lo <= code && code <= r.hi {
			if r.arr == nil {
				s := []byte(r.str)
				if r.lo != code {
					s[len(s)-1] += code[n-1] - r.lo[n-1]
				}
				return fix8Utf16(string(s))
			}
			if i := int(code[n-1] - r.lo[n-1]); i < len(r.arr) {
				return fix8Utf16(r.arr[i])
			}
			return "�"
		}
	}
	return "�"
}

// fix8Page decodes raws (one Tj each) through the parser with table tb as the
// font's ToUnicode, returning the page text without its leading line break.
func fix8Page(t *testing.T, tb fix8Table, raws []string) string {
	t.Helper()
	var c strings.Builder
	c.WriteString("BT /F1 12 Tf ")
	for _, r := range raws {
		fmt.Fprintf(&c, "%s Tj ", fix8Hex(r))
	}
	c.WriteString("ET")
	text, err := fix6Read(t, fix6OnePage(tb.program(), []byte(c.String()), ""), Limits{Work: 1 << 29, Text: 1 << 24})
	if err != nil {
		t.Fatalf("page read: %v", err)
	}
	return strings.TrimPrefix(text, "\n")
}

func fix8Check(t *testing.T, label string, tb fix8Table, raws []string) {
	t.Helper()
	var want strings.Builder
	for _, r := range raws {
		want.WriteString(tb.decode(r))
	}
	if got := fix8Page(t, tb, raws); got != want.String() {
		t.Fatalf("%s: parser %q, reference %q (raws %q, program:\n%s)", label, got, want.String(), raws, tb.program())
	}
}

func TestFix8_IndexEqualsLinearScan_FixedEdges(t *testing.T) {
	t.Run("control: the reference reads one known table as hand-derived", func(t *testing.T) {
		tb := fix8Table{
			space:  [4][]fix8Range{{{"\x00", "\xFF"}}},
			chars:  []fix8Char{{"A", "\x00B"}},
			ranges: []fix8BfRange{{"a", "z", nil, "\x00\x61"}},
		}
		if got := tb.decode("Aac"); got != "Bac" { // A->B (bfchar), a->U+0061, c->U+0061+2
			t.Fatalf("reference decode %q, want Bac", got)
		}
		if got := fix8Page(t, tb, []string{"Aac"}); got != "Bac" {
			t.Fatalf("parser decode %q, want Bac", got)
		}
	})
	t.Run("one byte: overlapping spans, duplicate bfchar, bfrange priority", func(t *testing.T) {
		tb := fix8Table{
			space: [4][]fix8Range{{{"\x10", "\x30"}, {"\x20", "\x50"}, {"\xFF", "\xFF"}, {"\x70", "\x60"}}},
			chars: []fix8Char{{"\x20", "\x00A"}, {"\x20", "\x00B"}},
			ranges: []fix8BfRange{
				{"\x10", "\x28", nil, "\x00a"},
				{"\x20", "\x40", nil, "\x00z"},
				{"\x41", "\x50", []string{"\x00x", "\x00y"}, ""},
			},
		}
		raws := make([]string, 0, 18)
		for _, b := range []byte{0x0F, 0x10, 0x20, 0x21, 0x28, 0x29, 0x30, 0x31, 0x40, 0x41, 0x42, 0x43, 0x50, 0x51, 0x60, 0x70, 0xFE, 0xFF} {
			raws = append(raws, string([]byte{b}))
		}
		fix8Check(t, "1-byte", tb, raws)
	})
	t.Run("two bytes: endpoints 0000 and FFFF, hi shorter or longer than lo", func(t *testing.T) {
		tb := fix8Table{
			space: [4][]fix8Range{{{"A", "A"}}, {{"\x00\x00", "\x00\x10"}, {"\xFF\xF0", "\xFF\xFF"}, {"\x01\x00", "\x01\x00"}}},
			chars: []fix8Char{{"\x00\x05", "\x00Q"}, {"\xFF\xFF", "\x00R"}},
			ranges: []fix8BfRange{
				{"\x00\x00", "\x00\x0A", nil, "\x00A"},
				{"\x00\x08", "\x00", nil, "\x00B"},
				{"\x00\x09", "\x01\x00\x00", nil, "\x00C"},
				{"\xFF\xF0", "\xFF\xFF", []string{"\x00m", "\x00n"}, ""},
				{"\x01\x00", "\x01\x00", nil, "\x00D"},
			},
		}
		raws := make([]string, 0, 90)
		for _, a := range []byte{0x00, 0x01, 0x05, 0x08, 0x09, 0x0A, 0x10, 0x41, 0xF0, 0xFF} {
			raws = append(raws, string([]byte{a}))
			for _, b := range []byte{0x00, 0x05, 0x08, 0x09, 0x0A, 0x10, 0xF1, 0xFF} {
				raws = append(raws, string([]byte{a, b}))
			}
		}
		fix8Check(t, "2-byte", tb, raws)
	})
	t.Run("three and four bytes: merged spans, upper endpoint FFFFFFFF", func(t *testing.T) {
		tb := fix8Table{
			space: [4][]fix8Range{nil, {{"\x00\x00", "\x00\x00"}},
				{{"\x01\x00\x00", "\x01\x00\x10"}, {"\x01\x00\x08", "\x01\x00\x20"}, {"\x01\x00\x21", "\x01\x00\x30"}, {"\x05\x00\x00", "\x04\x00\x00"}},
				{{"\xFF\xFF\xFF\xF0", "\xFF\xFF\xFF\xFF"}, {"\x00\x00\x00\x00", "\x00\x00\x00\x02"}}},
			chars: []fix8Char{{"\x01\x00\x09", "\x00A"}, {"\xFF\xFF\xFF\xFF", "\x00B"}, {"\x00\x00\x00\x01", "\x00C"}},
			ranges: []fix8BfRange{
				{"\x01\x00\x00", "\x01\x00\x0F", nil, "\x00a"},
				{"\x01\x00\x08", "\x01\x00\x28", nil, "\x00b"},
				{"\xFF\xFF\xFF\xF0", "\xFF\xFF\xFF\xFF", nil, "\x00c"},
				{"\x00\x00\x00\x00", "\x00\x00\x00\x02", []string{"\x00p", "\x00q"}, ""},
			},
		}
		raws := []string{
			"\x01\x00\x00", "\x01\x00\x07", "\x01\x00\x08", "\x01\x00\x09", "\x01\x00\x0F", "\x01\x00\x10", "\x01\x00\x20", "\x01\x00\x21", "\x01\x00\x30", "\x01\x00\x31",
			"\x05\x00\x00", "\xFF\xFF\xFF\xF0", "\xFF\xFF\xFF\xFF", "\xFF\xFF\xFF\xEF", "\x00\x00\x00\x00", "\x00\x00\x00\x01", "\x00\x00\x00\x02", "\x00\x00\x00\x03",
			"\x00\x00\x01\x00\x00\xFF", "\xFF\xFF\xFF", "\x01\x00",
		}
		fix8Check(t, "3/4-byte", tb, raws)
	})
	t.Run("the shortest matching code length wins", func(t *testing.T) {
		tb := fix8Table{
			space: [4][]fix8Range{{{"A", "A"}}, {{"AB", "AB"}}},
			chars: []fix8Char{{"A", "\x00X"}, {"AB", "\x00Y"}},
		}
		if got := fix8Page(t, tb, []string{"AB"}); got != "X�" {
			t.Fatalf("code AB decodes as A (shortest) then the unmatched B: got %q", got)
		}
		fix8Check(t, "shortest", tb, []string{"AB", "A", "BA", "AAB"})
	})
}

// seeded random tables: overlapping codespace and bfrange spans, duplicate
// bfchar, lo and hi of different lengths, inverted ranges, string and array
// destinations, drawn from a small alphabet so that hits are common.
func TestFix8_IndexEqualsLinearScan_Random(t *testing.T) {
	alphabet := []byte{0x00, 0x01, 0x02, 0x7F, 0x80, 0xFE, 0xFF}
	code := func(rng *rand.Rand, n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(b)
	}
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var tb fix8Table
		for i, k := 0, 1+rng.Intn(6); i < k; i++ {
			n := 1 + rng.Intn(4)
			tb.space[n-1] = append(tb.space[n-1], fix8Range{code(rng, n), code(rng, n)})
		}
		for i, k := 0, rng.Intn(8); i < k; i++ {
			tb.chars = append(tb.chars, fix8Char{code(rng, 1+rng.Intn(5)), string([]byte{0x00, byte('0' + rng.Intn(10))})})
		}
		for i, k := 0, rng.Intn(8); i < k; i++ {
			r := fix8BfRange{lo: code(rng, 1+rng.Intn(4)), hi: code(rng, 1+rng.Intn(5))}
			if rng.Intn(3) == 0 {
				r.arr = []string{"\x00a", "\x00b", "\x00c"}
			} else {
				r.str = string([]byte{0x00, byte('A' + rng.Intn(20))})
			}
			tb.ranges = append(tb.ranges, r)
		}
		var raws []string
		for j := 0; j < 12; j++ {
			raws = append(raws, code(rng, 1+rng.Intn(10)))
		}
		fix8Check(t, fmt.Sprintf("seed %d", seed), tb, raws)
	}
}

// ---- genuine codespace miss, real-cost oracle -----------------------------

// fix8EvenSpaceTable declares 30,000 distinct two-byte singleton codespace
// ranges 0x0100, 0x0102, ..., 0xEB5E (all even, never adjacent, so they cannot
// merge) with the mapping 0x0100 -> 'A'.
func fix8EvenSpaceTable() fix8Table {
	var tb fix8Table
	for i := 0; i < 30000; i++ {
		v := 0x0100 + 2*i
		c := string([]byte{byte(v >> 8), byte(v)})
		tb.space[1] = append(tb.space[1], fix8Range{c, c})
	}
	tb.chars = []fix8Char{{"\x01\x00", "\x00A"}}
	return tb
}

// fix8ReadWithin runs the page read in a disposable child process and fails
// the test when it does not finish within d; the child is killed then, so the
// parser does not keep running inside the test binary.
func fix8ReadWithin(t *testing.T, data []byte, lim Limits, d time.Duration) (string, error) {
	t.Helper()
	res := fix10Read(t, data, lim, d)
	return res.text, res.err
}

// Genuine misses: the content is (0x01 0x01)*. Its two-byte code 0x0101 (odd,
// between the even ranges) lies in no codespace
// range. 1,000,000 bytes against 30,000 ranges is 3e10 comparisons for a linear
// scan: no uncharged one finishes in the time allowed, an indexed lookup takes
// a fraction of a second, and a charged linear scan ends with ErrWorkLimit.
func TestFix8_CodespaceMissLookupCostDoesNotScaleWithRanges(t *testing.T) {
	tb := fix8EvenSpaceTable()
	pair := "\x01\x01"
	lim := Limits{Work: 1 << 29, Text: 1 << 25, Time: time.Hour}

	// Precondition, from the linear reference and not from the parser: every
	// byte of the miss input decodes to U+FFFD.
	probe := strings.Repeat(pair, 50)
	if got, want := tb.decode(probe), strings.Repeat("�", len(probe)); got != want {
		t.Fatalf("fixture is not a pure miss: reference decodes %d runes (want %d), %.40q", len([]rune(got)), len(probe), got)
	}

	build := func(raw string) []byte {
		return fix6OnePage(tb.program(), []byte("BT /F1 12 Tf "+fix8Hex(raw)+" Tj ET"), "")
	}
	t.Run("control: hits decode through the same table", func(t *testing.T) {
		text, err := fix8ReadWithin(t, build(strings.Repeat("\x01\x00", 200_000)), lim, 15*time.Second)
		if err != nil || strings.TrimSpace(text) != strings.Repeat("A", 200_000) {
			t.Fatalf("hit control: err=%v, %d bytes of text", err, len(text))
		}
	})
	t.Run("1,000,000 misses end quickly with one U+FFFD per byte, or by the work budget", func(t *testing.T) {
		raw := strings.Repeat(pair, 500_000)
		text, err := fix8ReadWithin(t, build(raw), lim, 6*time.Second)
		switch {
		case errors.Is(err, ErrWorkLimit):
			// a metered scan: its comparisons were charged
		case err != nil:
			t.Fatalf("unexpected error %v", err)
		default:
			if got, want := strings.TrimPrefix(text, "\n"), strings.Repeat("�", len(raw)); got != want {
				t.Fatalf("completed with %d bytes of text, want one U+FFFD per consumed input byte (%d runes)", len(got), len(raw))
			}
		}
	})
}

// ---- SEC4-1 / SF4-1: long bfchar origins -----------------------------------

// fix8LongKeyTU is a ToUnicode program whose dictionary alias K is one long
// string, used as the origin of pairs bfchar entries (blocks of 100). The
// ordinary mapping A -> B comes first.
func fix8LongKeyTU(keyBytes, pairs int) string {
	var b strings.Builder
	b.WriteString("1 dict begin\n/K (")
	b.WriteString(strings.Repeat("x", keyBytes))
	b.WriteString(") def\n1 begincodespacerange <00> <FF> endcodespacerange\n1 beginbfchar <41> <0042> endbfchar\n")
	for i := 0; i < pairs/100; i++ {
		b.WriteString("100 beginbfchar\n")
		for j := 0; j < 100; j++ {
			b.WriteString("K <0041>\n")
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString("end\n")
	return b.String()
}

// 400,000 pairs sharing one 1 MiB origin: hashing each occurrence is 400 GiB of
// key bytes for one 400,000-unit charge. A fixed parser skips origins no code
// can equal (1 to 4 bytes) or charges the bytes it hashes; either ends quickly
// with the ordinary mapping intact or with a work-limit error.
func TestFix8_LongAliasedBfcharOriginIsBounded(t *testing.T) {
	content := []byte("BT /F1 12 Tf (AAAA) Tj ET")
	lim := Limits{Work: 1 << 25, Text: 1 << 20}

	t.Run("control: ordinary origins map A to B", func(t *testing.T) {
		text, err := fix6Read(t, fix6OnePage(fix8LongKeyTU(8, 100), content, ""), lim)
		if err != nil || strings.TrimSpace(text) != "BBBB" {
			t.Fatalf("control text=%q err=%v", text, err)
		}
	})
	t.Run("1 MiB origin used by 400,000 pairs", func(t *testing.T) {
		data := fix6OnePage(fix8LongKeyTU(1<<20, 400_000), content, "")
		start := time.Now()
		text, err := fix6Read(t, data, lim)
		elapsed := time.Since(start)
		if elapsed > 2*time.Second {
			t.Fatalf("SEC4-1: indexing took %v for 400,000 pairs sharing a 1 MiB origin (about 400 GiB of hashing); the loop must be charged or the impossible origins skipped (err=%v text=%q)", elapsed, err, text)
		}
		if err == nil && strings.TrimSpace(text) != "BBBB" {
			t.Fatalf("the ordinary mapping was lost: text=%q", text)
		}
		if err != nil && !errors.Is(err, ErrWorkLimit) && !errors.Is(err, ErrStructureLimit) {
			t.Fatalf("unexpected error %v", err)
		}
	})
}

// ---- SF4-2: duplicate bfchar capacity ---------------------------------------

// fix8DuplicateTU repeats the pair "a b" (aliases for <41> and <0042>) in
// blocks of 32,768 pairs, each within the 65,536-operand stack bound.
func fix8DuplicateTU(pairs int) string {
	var b strings.Builder
	b.WriteString("1 dict begin\n/a <41> def\n/b <0042> def\n1 begincodespacerange <00> <FF> endcodespacerange\n")
	for left := pairs; left > 0; {
		n := min(left, 32768)
		fmt.Fprintf(&b, "%d beginbfchar\n", n)
		for i := 0; i < n; i++ {
			b.WriteString("a b\n")
		}
		b.WriteString("endbfchar\n")
		left -= n
	}
	b.WriteString("end\n")
	return b.String()
}

// 4,000,000 bfchar pairs that all repeat one origin hold one distinct mapping:
// the parser must not reserve index storage for every copy. Reserving map
// capacity for 4,000,000 entries alone is over 250 MiB.
func TestFix8_DuplicateBfcharCapacityIsBounded(t *testing.T) {
	content := []byte("BT /F1 12 Tf (AAAA) Tj ET")
	lim := Limits{Work: 1 << 29, Text: 1 << 20}

	t.Run("control: a few duplicates map A to B", func(t *testing.T) {
		text, err := fix6Read(t, fix6OnePage(fix8DuplicateTU(1000), content, ""), lim)
		if err != nil || strings.TrimSpace(text) != "BBBB" {
			t.Fatalf("control text=%q err=%v", text, err)
		}
	})
	t.Run("4,000,000 duplicates of one origin", func(t *testing.T) {
		data := fix6OnePage(fix8DuplicateTU(4_000_000), content, "")
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		text, err := fix6Read(t, data, lim)
		runtime.ReadMemStats(&after)
		alloc := after.TotalAlloc - before.TotalAlloc
		if err != nil && !errors.Is(err, ErrWorkLimit) && !errors.Is(err, ErrStructureLimit) {
			t.Fatalf("unexpected error %v", err)
		}
		if err == nil && strings.TrimSpace(text) != "BBBB" {
			t.Fatalf("first entry must win: text=%q", text)
		}
		// The allowance covers 16 MB of program text and the operand stack (about 139 MB measured with admission before allocation;
		// 1,136 MB measured on 734bf8d3f0, where duplicate storage appeared before admission); storage for 4,000,000 entries (a 128 MiB slice, a 270 MiB
		// map) must not appear.
		if alloc > 192<<20 {
			t.Fatalf("SF4-2: %d bytes allocated for 4,000,000 duplicates of one origin (err=%v); limit 192 MiB", alloc, err)
		}
	})
}
