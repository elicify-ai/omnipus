//go:build linux

package agent

// #1968 FIX6 (SEC2-1, SEC2-2, G2-1) at chat inspection: the same overflowing
// integers and unmetered-work shapes as the docextract probes, reaching the
// parser through chatPDF.inspect. Each probe is a disposable child process
// with an address-space ceiling, a start marker, a hard deadline and a
// TotalAlloc envelope; the child must also exit cleanly.

import (
	"bytes"
	"context"
	"encoding/base64"
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

type pdfFix6ChatKind struct {
	name     string
	mustStop bool // a complete text result is a missing meter
	control  bool
	want     []string
	deadline time.Duration
}

var pdfFix6ChatKinds = []pdfFix6ChatKind{
	{name: "control-three-pages", control: true, want: []string{"PAGE 1 text", "PAGE 2 text", "PAGE 3 text"}, deadline: 4 * time.Second},
	{name: "xref-table-overflow", deadline: 4 * time.Second},
	{name: "xref-stream-overflow", deadline: 4 * time.Second},
	{name: "cmap-bfchar-1<<62", deadline: 4 * time.Second},
	{name: "cmap-bfrange-1<<62", deadline: 4 * time.Second},
	{name: "cmap-bfrange-3n-wraps-to-2", deadline: 4 * time.Second},
	{name: "control-codespace", control: true, want: []string{strings.Repeat("A", 1024)}, deadline: 20 * time.Second},
	{name: "codespace-miss-1MiB", deadline: 8 * time.Second},
	{name: "control-filter-chain", control: true, want: []string{"hello"}, deadline: 20 * time.Second},
	{name: "filter-chain-whitespace", mustStop: true, deadline: 30 * time.Second},
}

func TestChatPDFFix6_HostileInspectionBounded(t *testing.T) {
	if spec := os.Getenv("PDF1968_FIX6_CHAT_CHILD"); spec != "" {
		pdfFix6ChatChild(t, spec)
		return
	}
	for _, k := range pdfFix6ChatKinds {
		t.Run(k.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.pdf")
			if err := os.WriteFile(path, pdfFix6Fixture(t, k.name), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), k.deadline+10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestChatPDFFix6_HostileInspectionBounded$", "-test.v", "-test.timeout="+(k.deadline+8*time.Second).String())
			cmd.Env = append(os.Environ(), "PDF1968_FIX6_CHAT_CHILD="+k.name+"|"+path, "GOTRACEBACK=none")
			out, err := cmd.CombinedOutput()
			if !bytes.Contains(out, []byte("PDF_FIX6_CHAT_STARTED")) {
				t.Fatalf("containment instrument never reached chat inspection: %v\n%s", err, pdfFix6Tail(out))
			}
			if err != nil {
				t.Fatalf("%s: chat inspection must end promptly with a notice or bounded text (child failed: %v)\n%s", k.name, err, pdfFix6Tail(out))
			}
		})
	}
}

// pdfFix6Tail keeps the head (the failure's own message) and the tail of child output.
func pdfFix6Tail(b []byte) string {
	if len(b) > 3000 {
		return string(b[:1500]) + "\n...\n" + string(b[len(b)-1500:])
	}
	return string(b)
}

func pdfFix6ChatChild(t *testing.T, spec string) {
	t.Helper()
	name, path, _ := strings.Cut(spec, "|")
	var kind pdfFix6ChatKind
	for _, k := range pdfFix6ChatKinds {
		if k.name == name {
			kind = k
		}
	}
	if kind.name == "" {
		t.Fatalf("unknown kind %q", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
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
	c := &chatPDF{name: "fixture.pdf"}
	dataURL := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)
	t.Log("PDF_FIX6_CHAT_STARTED", kind.name)
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	done := make(chan struct{})
	go func() { c.inspect(dataURL); close(done) }()
	select {
	case <-done:
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		// TotalAlloc counts churn, not retention: decoding up to the 1<<29
		// work budget legitimately cycles about that many bytes through the
		// filter readers, so a budget-bound kind gets the budget plus slack.
		ceiling := uint64(256 << 20)
		if kind.mustStop {
			ceiling = 1<<29 + 32<<20
		}
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > ceiling {
			t.Fatalf("%s: chat inspection allocated %d bytes, above the %d-byte envelope", kind.name, alloc, ceiling)
		}
		if strings.TrimSpace(c.injection) == "" {
			t.Fatal("FR-13: chat inspection silently omitted the PDF")
		}
		if len(c.injection) > 25<<20 {
			t.Fatalf("unbounded injection: %d", len(c.injection))
		}
		switch {
		case kind.control:
			if !c.hasText || strings.Contains(c.injection, "[truncated]") {
				t.Fatalf("control %s must show complete text: hasText=%t injection=%.120q", kind.name, c.hasText, c.injection)
			}
			at := 0
			for _, want := range kind.want {
				i := strings.Index(c.injection[at:], want)
				if i < 0 {
					t.Fatalf("control %s lost %.30q (in order): %.120q", kind.name, want, c.injection)
				}
				at += i + len(want)
			}
		case kind.mustStop:
			if c.hasText {
				t.Fatalf("%s: work beyond the parser budget must not be presented as extracted text: %.120q", kind.name, c.injection)
			}
		}
	case <-time.After(kind.deadline):
		t.Fatalf("%s: chat inspection did not stop within %s", kind.name, kind.deadline)
	}
}
