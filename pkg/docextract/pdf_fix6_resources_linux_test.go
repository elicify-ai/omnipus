//go:build linux

package docextract

// #1968 FIX6 (SEC2-1, SEC2-2; code review G2-1). FR-6 and this module's
// bounded-extraction contract: a hostile PDF ends as a typed error or a
// bounded result, promptly and without unbounded allocation. The earlier
// FIX5 fixtures used a large /Size with a small normal /Index; these use the
// overflowing integers themselves. Every probe runs in a disposable child
// process (address-space ceiling, exact start marker, hard deadline,
// TotalAlloc ceiling): a goroutine timeout alone proves nothing about whether
// the work stopped, so the child must also exit cleanly and stay under the
// allocation envelope. Harness limits, not provider quotas.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type pdfFix6Kind struct {
	name string
	// mustStop: a successful complete result is a failure (the document needs
	// more work than the parser budget allows). Otherwise any typed error or
	// bounded result passes.
	mustStop bool
	// control: a valid document that must extract; wantText is checked in order.
	control  bool
	wantText []string
	deadline time.Duration
}

var pdfFix6Kinds = []pdfFix6Kind{
	{name: "control-three-pages", control: true, wantText: []string{"PAGE 1 text", "PAGE 2 text", "PAGE 3 text"}, deadline: 4 * time.Second},
	{name: "xref-table-overflow", deadline: 4 * time.Second},
	{name: "xref-stream-overflow", deadline: 4 * time.Second},
	{name: "cmap-bfchar-1<<62", deadline: 4 * time.Second},
	{name: "cmap-bfrange-1<<62", deadline: 4 * time.Second},
	{name: "cmap-bfrange-3n-wraps-to-2", deadline: 4 * time.Second},
	{name: "control-codespace", control: true, wantText: []string{strings.Repeat("A", 1024)}, deadline: 20 * time.Second},
	{name: "codespace-miss-1MiB", deadline: 8 * time.Second},
	{name: "control-filter-chain", control: true, wantText: []string{"hello"}, deadline: 20 * time.Second},
	{name: "filter-chain-whitespace", mustStop: true, deadline: 30 * time.Second},
}

var pdfFix6Entries = []string{"inspect", "extract-bytes", "extract-path", "extract-pdf"}

func TestPDFFix6_HostileInputsBounded(t *testing.T) {
	if spec := os.Getenv("PDF1968_FIX6_CHILD"); spec != "" {
		pdfFix6Child(t, spec)
		return
	}
	dir := t.TempDir()
	for _, k := range pdfFix6Kinds {
		path := filepath.Join(dir, strings.NewReplacer("<", "", "-", "_").Replace(k.name)+".pdf")
		if err := os.WriteFile(path, pdfFix6Fixture(t, k.name), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, entry := range pdfFix6Entries {
			t.Run(k.name+"/"+entry, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), k.deadline+10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPDFFix6_HostileInputsBounded$", "-test.v", "-test.timeout="+(k.deadline+8*time.Second).String())
				cmd.Env = append(os.Environ(), "PDF1968_FIX6_CHILD="+k.name+"|"+entry+"|"+path, "GOTRACEBACK=none")
				out, err := cmd.CombinedOutput()
				if !bytes.Contains(out, []byte("PDF_FIX6_STARTED")) {
					t.Fatalf("containment instrument never reached the parser: %v\n%s", err, tail(out))
				}
				if err != nil {
					t.Fatalf("%s via %s must end promptly with a typed error or bounded result (child failed: %v)\n%s", k.name, entry, err, tail(out))
				}
			})
		}
	}
}

func pdfFix6Child(t *testing.T, spec string) {
	t.Helper()
	parts := strings.SplitN(spec, "|", 3)
	if len(parts) != 3 {
		t.Fatalf("bad child spec %q", spec)
	}
	var kind pdfFix6Kind
	for _, k := range pdfFix6Kinds {
		if k.name == parts[0] {
			kind = k
		}
	}
	if kind.name == "" {
		t.Fatalf("unknown kind %q", parts[0])
	}
	entry, path := parts[1], parts[2]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Baseline-relative address-space ceiling: 2 GiB above what the runtime
	// already reserved. A tiny file cannot legitimately need more.
	var limit unix.Rlimit
	if err = unix.Getrlimit(unix.RLIMIT_AS, &limit); err != nil {
		t.Fatal(err)
	}
	stat, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		t.Fatal(err)
	}
	pages, err := strconv.ParseUint(strings.Fields(string(stat))[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	limit.Cur = min(limit.Cur, pages*uint64(os.Getpagesize())+uint64(2<<30))
	if err = unix.Setrlimit(unix.RLIMIT_AS, &limit); err != nil {
		t.Fatal(err)
	}
	t.Log("PDF_FIX6_STARTED", kind.name, entry, "raw_bytes", len(data))

	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	type outcome struct {
		text string // extracted text
		err  error  // typed error (inspect) or nil
		ok   bool
		why  string
	}
	done := make(chan outcome, 1)
	go func() {
		switch entry {
		case "inspect":
			info, err := InspectPDF(data)
			done <- outcome{text: info.Text, err: err, ok: err == nil}
		case "extract-bytes":
			text, ok, why := ExtractBytes(data, "application/pdf", "fixture.pdf")
			done <- outcome{text: text, ok: ok, why: why}
		case "extract-path":
			text, ok, why := Extract(path, "application/pdf", "fixture.pdf")
			done <- outcome{text: text, ok: ok, why: why}
		case "extract-pdf":
			text, ok, why := extractPDF(bytes.NewReader(data), int64(len(data)))
			done <- outcome{text: text, ok: ok, why: why}
		default:
			panic("unknown entry " + entry)
		}
	}()
	select {
	case got := <-done:
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		// TotalAlloc counts churn, not retention: decoding up to the work
		// budget legitimately cycles about that many bytes through the
		// filter readers, so a budget-bound kind gets the budget plus slack;
		// every other kind stays under 256 MiB.
		ceiling := uint64(256 << 20)
		if kind.mustStop {
			ceiling = pdfWorkBudget + 32<<20
		}
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > ceiling {
			t.Fatalf("%s via %s allocated %d bytes, above the %d-byte envelope", kind.name, entry, alloc, ceiling)
		}
		if entry == "inspect" && got.err != nil &&
			!errors.Is(got.err, ErrPDFMalformed) && !errors.Is(got.err, ErrPDFEncrypted) && !errors.Is(got.err, ErrPDFUnsupportedVersion) {
			t.Fatalf("FR-6: untyped inspection error %T %v", got.err, got.err)
		}
		if !got.ok && entry != "inspect" && got.why == "" {
			t.Fatalf("a failed extraction must say why (reason empty)")
		}
		if len(got.text) > 25<<20 {
			t.Fatalf("unbounded text: %d bytes", len(got.text))
		}
		switch {
		case kind.control:
			if !got.ok {
				t.Fatalf("control %s via %s must extract: err=%v reason=%q", kind.name, entry, got.err, got.why)
			}
			if strings.Contains(got.text, "[truncated]") {
				t.Fatalf("control %s must not be marked truncated", kind.name)
			}
			at := 0
			for _, want := range kind.wantText {
				i := strings.Index(got.text[at:], want)
				if i < 0 {
					t.Fatalf("control %s via %s lost %.30q (in order); text %.80q", kind.name, entry, want, got.text)
				}
				at += i + len(want)
			}
		case kind.mustStop:
			if got.ok {
				t.Fatalf("%s via %s: the work needed exceeds the parser's work budget, so a complete result is a missing meter; got ok with %d bytes %.40q", kind.name, entry, len(got.text), got.text)
			}
		}
	case <-time.After(kind.deadline):
		t.Fatalf("%s via %s did not stop within the %s deadline", kind.name, entry, kind.deadline)
	}
}
