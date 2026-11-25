//go:build linux

package agent

// F1: chat's page/text inspection must not re-enter an unbounded legacy
// extractor after a bounded InspectPDF rejection. Each probe has a disposable
// process, a goroutine deadline and an outer kill/reap watchdog.

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestChatPDF_PathologicalInspectionBounded(t *testing.T) {
	if kind := os.Getenv("PDF1968_CHAT_CHILD"); kind != "" {
		pdfFix5ChatResourceChild(t, kind)
		return
	}
	for _, kind := range []string{"ordinary", "parent-cycle", "xref-size", "flate-expansion"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestChatPDF_PathologicalInspectionBounded$", "-test.v", "-test.timeout=18s")
			cmd.Env = append(os.Environ(), "PDF1968_CHAT_CHILD="+kind, "GOTRACEBACK=none")
			out, err := cmd.CombinedOutput()
			if !bytes.Contains(out, []byte("PDF_CHAT_INSPECT_STARTED")) {
				t.Fatalf("inspection instrument did not start: %v\n%s", err, out)
			}
			if err != nil {
				t.Fatalf("F1 chat: %s must terminate without unsafe parser/fallback work: %v\n%s", kind, err, out)
			}
		})
	}
}

func pdfFix5ChatResourceChild(t *testing.T, kind string) {
	t.Helper()
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_AS, &limit); err != nil {
		t.Fatal(err)
	}
	// Preserve pre-existing Go/runtime instrumentation mappings; allow at
	// most 4 GiB of additional virtual space for the hostile probe. A tiny
	// input cannot legitimately demand the advertised 32 GiB xref table.
	stat, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(stat))
	if len(fields) == 0 {
		t.Fatal("containment instrument: missing virtual-page count")
	}
	pages, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	limit.Cur = min(limit.Cur, pages*uint64(os.Getpagesize())+uint64(4<<30))
	if err := unix.Setrlimit(unix.RLIMIT_AS, &limit); err != nil {
		t.Fatal(err)
	}
	data := pdfFix5ChatResourcePDF(t, kind)
	c := &chatPDF{name: "resource.pdf"}
	dataURL := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)
	t.Log("PDF_CHAT_INSPECT_STARTED", kind, "raw_bytes", len(data))
	// The fixture is built before this measurement. Checking just injection
	// length misses an extractor that allocates the full expansion then caps
	// its display. This isolated child allows 256 MiB of parser work for a
	// sub-100 KiB compressed fixture: a harness safety envelope, not a model
	// quota. TotalAlloc includes temporary allocations even after a GC.
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	done := make(chan struct{})
	go func() { c.inspect(dataURL); close(done) }()
	select {
	case <-done:
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Log("PDF_CHAT_INSPECT_ALLOCATED", allocated)
		if allocated > 256<<20 {
			t.Fatalf("F1 chat: inspection/fallback allocated %d bytes, above the 256 MiB work envelope", allocated)
		}
		if strings.TrimSpace(c.injection) == "" {
			t.Fatal("FR-13: chat inspection silently omitted the PDF")
		}
		if len(c.injection) > 25<<20 {
			t.Fatalf("F1 chat: unbounded expanded text: %d", len(c.injection))
		}
		if kind == "ordinary" && (!c.hasText || c.pages != 1 || !strings.Contains(c.injection, "PAGE 1 text")) {
			t.Fatalf("ordinary positive control: pages=%d hasText=%t injection=%q", c.pages, c.hasText, c.injection)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("F1 chat: %s inspection/fallback did not terminate within the 15s safety deadline", kind)
	}
}

func pdfFix5ChatResourcePDF(t *testing.T, kind string) []byte {
	t.Helper()
	switch kind {
	case "ordinary":
		return pdf1968TextPDF(t, 1, true)
	case "xref-size":
		return []byte("%PDF-1.5\n1 0 obj\n<< /Type /XRef /Size 1073741824 /W [1 1 1] /Index [0 1] /Length 3 >>\nstream\n\x00\x00\x00\nendstream\nendobj\nstartxref\n9\n%%EOF\n")
	case "parent-cycle":
		return pdf1968AssemblePDF("%PDF-1.4", []pdfObj{
			{num: 1, body: "<< /Type /Catalog /Pages 2 0 R >>"},
			{num: 2, body: "<< /Type /Pages /Count 1 /Kids [3 0 R] >>"},
			{num: 3, body: "<< /Type /Page /Parent 3 0 R /Contents 4 0 R >>"},
			{num: 4, body: "<< /Length 0 >>\nstream\n\nendstream"},
		}, 1, "")
	case "flate-expansion":
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		if _, err := fmt.Fprintf(w, "BT /F1 12 Tf 72 720 Td (%s) Tj ET", strings.Repeat("X", 32<<20)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return pdf1968AssemblePDF("%PDF-1.4", []pdfObj{
			{num: 1, body: "<< /Type /Catalog /Pages 2 0 R >>"},
			{num: 2, body: "<< /Type /Pages /Count 1 /Kids [3 0 R] >>"},
			{num: 3, body: "<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>"},
			{num: 4, body: fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream", z.Len(), z.Bytes())},
			{num: 5, body: "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"},
		}, 1, "")
	default:
		t.Fatalf("unknown resource fixture %q", kind)
		return nil
	}
}
