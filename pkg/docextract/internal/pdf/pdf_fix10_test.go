package pdf

// #1968 FIX10 (fix round 5: SEC5-1, SEC5-2, G5-R1). Parser-level regression
// tests for value reuse. Every probe that can run long goes through a
// disposable child process (the test binary re-executed), so a timeout kills
// the parser instead of leaving it running inside the test binary. The wall
// clock ceiling Limits.Time is set to an hour wherever the work meter is the
// subject, so only a meter can stop the parse. Expected values come from
// fixture arithmetic and the Limits contract, never from the parser's own
// work counter.

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fix10ChildEnv = "PDF1968_FIX10_CHILD"

// fix10ChildResult carries a child's outcome back to the parent.
type fix10ChildResult struct {
	text    string
	err     error
	elapsed time.Duration
}

// fix10Sentinels are the typed stops a child can report.
var fix10Sentinels = []struct {
	kind string
	err  error
}{{"work", ErrWorkLimit}, {"text", ErrTextLimit}, {"structure", ErrStructureLimit}}

// TestFix10_ReadChild is the child half of fix10Read. Without the environment
// variable it does nothing, so a normal run is not affected.
func TestFix10_ReadChild(t *testing.T) {
	spec := os.Getenv(fix10ChildEnv)
	if spec == "" {
		return
	}
	parts := strings.Split(spec, "|")
	if len(parts) != 5 {
		t.Fatalf("bad child spec %q", spec)
	}
	data, err := os.ReadFile(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var n [3]int64
	for i := range n {
		if n[i], err = strconv.ParseInt(parts[1+i], 10, 64); err != nil {
			t.Fatal(err)
		}
	}
	lim := Limits{Work: n[0], Text: n[1], Time: time.Duration(n[2])}
	t.Log("FIX10_STARTED")
	start := time.Now()
	var text string
	r, err := fix7Open(data, lim)
	if err == nil {
		text, err = fix7PlainText(r)
	}
	elapsed := time.Since(start)
	kind := "ok"
	if err != nil {
		kind = "err:" + err.Error()
		for _, s := range fix10Sentinels {
			if errors.Is(err, s.err) {
				kind = s.kind + ":" + err.Error()
			}
		}
	}
	out := fmt.Sprintf("%s\n%d\n%s", kind, elapsed.Nanoseconds(), text)
	if err := os.WriteFile(parts[4], []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fix10Read opens and reads data in a child process. When the child does not
// finish within hard it is killed and the test fails: the parser cannot keep
// running behind a timed-out test.
func fix10Read(t *testing.T, data []byte, lim Limits, hard time.Duration) fix10ChildResult {
	t.Helper()
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.pdf"), filepath.Join(dir, "out.txt")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		t.Fatal(err)
	}
	spec := fmt.Sprintf("%s|%d|%d|%d|%s", in, lim.Work, lim.Text, int64(lim.Time), out)
	ctx, cancel := context.WithTimeout(context.Background(), hard)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFix10_ReadChild$", "-test.v")
	cmd.Env = append(os.Environ(), fix10ChildEnv+"="+spec, "GOTRACEBACK=none")
	output, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the read did not finish within %v (child killed): the work is not metered, or the meter is too cheap", hard)
	}
	if !bytes.Contains(output, []byte("FIX10_STARTED")) {
		t.Fatalf("the child never reached the parser: %v\n%.2000s", runErr, output)
	}
	if runErr != nil {
		t.Fatalf("child failed: %v\n%.2000s", runErr, output)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("child left no result: %v\n%.2000s", err, output)
	}
	head, rest, _ := strings.Cut(string(raw), "\n")
	ns, text, _ := strings.Cut(rest, "\n")
	elapsedNs, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		t.Fatalf("bad child result %.80q", raw)
	}
	res := fix10ChildResult{text: text, elapsed: time.Duration(elapsedNs)}
	if kind, msg, found := strings.Cut(head, ":"); found {
		res.err = errors.New(msg)
		for _, s := range fix10Sentinels {
			if s.kind == kind {
				res.err = fmt.Errorf("%w (%s)", s.err, msg)
			}
		}
	}
	return res
}

// fix10Doc is a one-page document with font F1 given as an object body.
func fix10Doc(font, content string) []byte {
	return fix6Assemble([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [4 0 R] >>",
		font,
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R >>",
		fix6Stream("", []byte(content)),
	})
}

const fix10PlainFont = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"

// SEC5-1: a TJ array bound with def is handed to TJ again and again without
// being lexed again; each reuse must cost the array's size. 20,000 numbers
// shown 20,000 times is 4e8 element visits for about 140 KB of file (40,000 bytes of
// array plus 20,000 lines of 5 bytes). Against the
// 16 Mi budget below a metered reuse stops after a few hundred shows; an
// unmetered one needs seconds and never reports the limit.
func TestFix10_ReusedTJArrayStopsWithTheWorkLimit(t *testing.T) {
	lim := Limits{Work: 1 << 24, Text: 1 << 20, Time: time.Hour}

	t.Run("control: a reused small array extracts in order within the same budget", func(t *testing.T) {
		content := "1 dict begin /h [(Hel) -20 (lo)] def BT /F1 12 Tf h TJ h TJ [(!)] TJ ET end\n"
		res := fix10Read(t, fix10Doc(fix10PlainFont, content), lim, 20*time.Second)
		if res.err != nil {
			t.Fatalf("control must extract: %v", res.err)
		}
		if got, want := strings.TrimSpace(res.text), "HelloHello!"; got != want {
			t.Fatalf("control text = %q, want %q", got, want)
		}
	})
	t.Run("a reused 20,000-number array shown 20,000 times ends with ErrWorkLimit, quickly", func(t *testing.T) {
		var c strings.Builder
		c.WriteString("1 dict begin /a [")
		c.WriteString(strings.Repeat("0 ", 20_000))
		c.WriteString("] def\n")
		c.WriteString(strings.Repeat("a TJ\n", 20_000))
		c.WriteString("end BT /F1 12 Tf (OK) Tj ET\n")
		res := fix10Read(t, fix10Doc(fix10PlainFont, c.String()), lim, 8*time.Second)
		if !errors.Is(res.err, ErrWorkLimit) {
			t.Fatalf("SEC5-1: 4e8 reused element visits against a 16 Mi work budget must end with ErrWorkLimit, got err=%v text=%.40q", res.err, res.text)
		}
		if res.elapsed > 4*time.Second {
			t.Fatalf("SEC5-1: the stop took %v, want well under the clock ceiling", res.elapsed)
		}
	})
}

// SEC5-2: a long name bound to a key and then used as the operand of def, over
// and over. Each use hashes the whole name, so each use must cost its length.
// A 1 MiB name used 20,000 times is 20 GiB of hashing for a file of about
// 1.2 MiB (the 1 MiB name plus 20,000 lines of 8 bytes, 160,000 bytes).
func TestFix10_ReusedLongDefKeyStopsWithTheWorkLimit(t *testing.T) {
	lim := Limits{Work: 1 << 24, Text: 1 << 20, Time: time.Hour}

	t.Run("control: short names bound and reused still extract", func(t *testing.T) {
		content := "1 dict begin /K /Short def K 0 def K 1 def end BT /F1 12 Tf (OK) Tj ET\n"
		res := fix10Read(t, fix10Doc(fix10PlainFont, content), lim, 20*time.Second)
		if res.err != nil || strings.TrimSpace(res.text) != "OK" {
			t.Fatalf("control text=%q err=%v", res.text, res.err)
		}
	})
	t.Run("a 1 MiB name reused by 20,000 defs ends with ErrWorkLimit, quickly", func(t *testing.T) {
		var c strings.Builder
		c.WriteString("1 dict begin /K /")
		c.WriteString(strings.Repeat("N", 1<<20))
		c.WriteString(" def\n")
		c.WriteString(strings.Repeat("K 0 def\n", 20_000))
		c.WriteString("end BT /F1 12 Tf (OK) Tj ET\n")
		res := fix10Read(t, fix10Doc(fix10PlainFont, c.String()), lim, 8*time.Second)
		if !errors.Is(res.err, ErrWorkLimit) {
			t.Fatalf("SEC5-2: 20 GiB of key hashing against a 16 Mi work budget must end with ErrWorkLimit, got err=%v text=%.40q", res.err, res.text)
		}
		if res.elapsed > 4*time.Second {
			t.Fatalf("SEC5-2: the stop took %v, want well under the clock ceiling", res.elapsed)
		}
	})
}

// G5-R1: a long /Differences glyph name used to be hashed for every shown
// byte. The table is now built once. 100,000 'A' with one 8 MiB name coded 65
// must finish quickly and decode to 100,000 'A' (an unknown glyph keeps the
// byte as its own character).
func TestFix10_LongDifferencesNameIsHashedOnce(t *testing.T) {
	lim := Limits{Work: 1 << 26, Text: 1 << 20, Time: time.Hour}
	differencesFont := func(diffs string) string {
		return "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding << /Differences [" + diffs + "] >> >>"
	}

	t.Run("control: a normal Differences font decodes by the first known glyph name", func(t *testing.T) {
		// Code 65 is unknownglyph (not a glyph name, keeps 'A'); then /B at 66
		// is B; the number 66 restarts at 66 with /C then /D (67): the first
		// known name for 66 is /B, for 67 it is /D. So (AB) -120 (CD) reads
		// A B D D, and ( A) keeps the space and A.
		font := differencesFont("65 /unknownglyph /B 66 /C /D")
		res := fix10Read(t, fix10Doc(font, "BT /F1 12 Tf [(AB) -120 (CD)] TJ ( A) Tj ET\n"), lim, 20*time.Second)
		if res.err != nil || strings.TrimSpace(res.text) != "ABDD A" {
			t.Fatalf("control text=%q err=%v, want %q", res.text, res.err, "ABDD A")
		}
	})
	t.Run("control: an unknown name before a known one at the same code does not hide it", func(t *testing.T) {
		font := differencesFont("65 /nosuchglyph 65 /C")
		res := fix10Read(t, fix10Doc(font, "BT /F1 12 Tf (AAB) Tj ET\n"), lim, 20*time.Second)
		if res.err != nil || strings.TrimSpace(res.text) != "CCB" {
			t.Fatalf("control text=%q err=%v, want %q", res.text, res.err, "CCB")
		}
	})
	t.Run("100,000 shown bytes against an 8 MiB glyph name are fast and correct", func(t *testing.T) {
		font := differencesFont("65 /" + strings.Repeat("g", 8<<20))
		res := fix10Read(t, fix10Doc(font, "BT /F1 12 Tf ("+strings.Repeat("A", 100_000)+") Tj ET\n"), lim, 8*time.Second)
		if res.err != nil {
			t.Fatalf("G5-R1: must finish within the budget, got %v", res.err)
		}
		if got, want := strings.TrimSpace(res.text), strings.Repeat("A", 100_000); got != want {
			t.Fatalf("G5-R1: text has %d characters, want 100,000 'A'", len(got))
		}
		if res.elapsed > 3*time.Second {
			t.Fatalf("G5-R1: took %v; the glyph name must not be hashed for every byte", res.elapsed)
		}
	})
}

// The wall-clock ceiling is a replacement for the removed counter checks on
// slow input: a TJ element charges, and a charge samples the clock, so a
// reused array shown without end is stopped by Limits.Time even when the work
// budget would allow it.
func TestFix10_TimeCeilingStopsAReusedTJArray(t *testing.T) {
	lim := Limits{Work: 1 << 50, Text: 1 << 20, Time: 300 * time.Millisecond}
	var c strings.Builder
	c.WriteString("1 dict begin /a [")
	c.WriteString(strings.Repeat("0 ", 50_000))
	c.WriteString("] def\n")
	c.WriteString(strings.Repeat("a TJ\n", 100_000))
	c.WriteString("end BT /F1 12 Tf (OK) Tj ET\n")
	res := fix10Read(t, fix10Doc(fix10PlainFont, c.String()), lim, 8*time.Second)
	if !errors.Is(res.err, ErrWorkLimit) || !strings.Contains(res.err.Error(), "time ceiling") {
		t.Fatalf("a 300 ms ceiling must stop 5e9 element visits with the time-ceiling error, got err=%v", res.err)
	}
	if res.elapsed > 3*time.Second {
		t.Fatalf("the ceiling is 300 ms but the stop came after %v", res.elapsed)
	}
}

// Replacement for the removed counter checks (FIX6 SEC2-2 and FIX7 SEC3-3
// single-miss base cases): one codespace miss must not stop the parse and
// decodes to one U+FFFD per byte consumed. Neither table holds a range with
// the code: fix6CodespaceTU(30000, false) declares only <01> singletons, so
// 'A' (0x41) is in none; fix7CodespaceTU(30000) declares 0x0063 and
// 0x1000 upward, so 0xFFFF is in none.
func TestFix10_SingleCodespaceMissSucceeds(t *testing.T) {
	lim := Limits{Work: 8 << 20, Text: 1 << 20}

	t.Run("FIX6 table: one byte outside 30,000 ranges", func(t *testing.T) {
		data := fix6OnePage(fix6CodespaceTU(30000, false), []byte("BT /F1 12 Tf (A) Tj ET"), "")
		text, err := fix6Read(t, data, lim)
		if err != nil {
			t.Fatalf("one miss must not stop the parse: %v", err)
		}
		if got := strings.TrimSpace(text); got != "�" {
			t.Fatalf("one miss decodes to one U+FFFD, got %q", got)
		}
	})
	t.Run("FIX7 table: one two-byte code outside 30,000 ranges", func(t *testing.T) {
		const top = 0x1000 + 30000 - 2
		if 0xFFFF <= top {
			t.Fatal("fixture precondition: 0xFFFF must lie outside the declared codespace")
		}
		data := fix6OnePage(fix7CodespaceTU(30000), []byte("BT /F1 12 Tf <FFFF> Tj ET"), "")
		text, err := fix6Read(t, data, lim)
		if err != nil {
			t.Fatalf("one miss must not stop the parse: %v", err)
		}
		if got := strings.TrimSpace(text); got != "��" {
			t.Fatalf("a missed two-byte code consumes one byte at a time, two U+FFFD expected, got %q", got)
		}
	})
}

// A text-budget stop on shown strings read through def aliases still reports
// ErrTextLimit and keeps the characters decoded before it (the replacement for
// the removed prefix and budget checks): 5 reuses of a 1,000-byte string
// cannot fit a 1,500-byte text budget; the first reuse always fits.
func TestFix10_TextBudgetStopOnReusedStringIsMarked(t *testing.T) {
	str := strings.Repeat("A", 1000)
	content := "1 dict begin /s (" + str + ") def BT /F1 12 Tf s Tj s Tj s Tj s Tj s Tj ET end\n"
	res := fix10Read(t, fix10Doc(fix10PlainFont, content), Limits{Work: 1 << 26, Text: 1500, Time: time.Hour}, 20*time.Second)
	if !errors.Is(res.err, ErrTextLimit) {
		t.Fatalf("a 1,500-byte text budget must stop 5,000 reused characters with ErrTextLimit, got %v", res.err)
	}
	// Limits.Text bounds the bytes of extracted text, line breaks included,
	// and each 'A' is one byte. The decoder keeps the prefix that fits, so the
	// whole budget is used: the text is exactly 1,500 bytes (a range would
	// accept a decoder that dropped the whole second reuse) and holds only 'A'
	// and line breaks.
	if len(res.text) != 1500 || strings.Trim(res.text, "A\n") != "" {
		t.Fatalf("kept text must be exactly 1,500 bytes of 'A' and line breaks, got %d bytes %.40q", len(res.text), res.text)
	}
	// The first reuse (1,000 'A') always fits, so at least that many 'A' must
	// survive: a result made only of line breaks would satisfy the length
	// check above while losing the document text.
	if got := strings.Count(res.text, "A"); got < 1000 {
		t.Fatalf("the first reused string (1,000 'A') must be kept whole, got %d 'A' in %.40q", got, res.text)
	}
	// Exact prefix: BT writes one charged line break, the five Tj calls add no
	// further breaks, so the 1,500-byte text budget leaves room for exactly
	// 1,499 'A' after it. Any other split loses text or invents breaks.
	if want := "\n" + strings.Repeat("A", 1499); res.text != want {
		t.Fatalf("kept text must be one line break then 1,499 'A', got %d bytes, %d 'A', %d line breaks, head %.20q",
			len(res.text), strings.Count(res.text, "A"), strings.Count(res.text, "\n"), res.text)
	}
}

// R3-SEC-1 (#1968): each structural parser bound, proved with a clock that
// cannot compete (Time: one hour, a work budget far above anything these
// files need), so only the bound itself can end the parse. The public
// InspectPDF test in the parent package asserts only the documented safe
// outcome, because there the ten-second sampled clock may legitimately win.
// Fixture arithmetic, not parser output:
//   - parent chain: a page whose /Parent is itself loops past the 256-step
//     inheritance bound;
//   - xref size: an xref stream announcing 2^30 entries exceeds the 2^20 bound;
//   - token: a 32 MiB string token exceeds the 16 MiB token bound
//     (the file stays small because the stream is deflated).
func TestFix10_StructuralBoundsStopWithTheirStructureLimit(t *testing.T) {
	flateToken := func(t *testing.T) []byte {
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		fmt.Fprintf(w, "BT /F1 12 Tf 72 720 Td (%s) Tj ET", strings.Repeat("X", 32<<20))
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return fix6Assemble([]string{
			"<< /Type /Catalog /Pages 2 0 R >>",
			"<< /Type /Pages /Count 1 /Kids [4 0 R] >>",
			fix10PlainFont,
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R >>",
			fix6Stream("/Filter /FlateDecode", z.Bytes()),
		})
	}
	cases := []struct {
		name     string
		build    func(t *testing.T) []byte
		fragment string // the exact structural cause, from the bound it breaks
	}{
		{"parent chain over 256", func(t *testing.T) []byte {
			return fix6Assemble([]string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
				"<< /Type /Page /Parent 3 0 R /Contents 4 0 R >>",
				"<< /Length 0 >>\nstream\n\nendstream",
			})
		}, "page /Parent chain longer than 256"},
		{"xref stream Size 2^30", func(t *testing.T) []byte {
			header := "%PDF-1.5\n"
			return []byte(fmt.Sprintf("%s1 0 obj\n<< /Type /XRef /Size 1073741824 /Index [0 1] /W [1 1 1] /Length 3 /Root 2 0 R >>\nstream\n\x00\x00\x00\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", header, len(header)))
		}, "xref stream Size 1073741824 outside 1..1048576"},
		{"32 MiB token", flateToken, "token longer than 16777216 bytes"},
	}
	lim := Limits{Work: 1 << 36, Text: 1 << 30, Time: time.Hour}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := fix10Read(t, tc.build(t), lim, 60*time.Second)
			if !errors.Is(res.err, ErrStructureLimit) {
				t.Fatalf("must end with ErrStructureLimit, got err=%v text=%d bytes", res.err, len(res.text))
			}
			if !strings.Contains(res.err.Error(), tc.fragment) {
				t.Fatalf("must name its own bound %q, got %v", tc.fragment, res.err)
			}
			if res.text != "" {
				t.Fatalf("a rejected file yields no text, got %d bytes", len(res.text))
			}
		})
	}
}
