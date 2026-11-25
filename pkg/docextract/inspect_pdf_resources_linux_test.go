//go:build linux

package docextract

// #1968 FIX5 F1. FR-6 and this module's bounded-extraction contract require
// hostile inputs to end as typed errors or bounded results. These are harness
// safety limits, NOT catalogue/provider quotas. Run every hazardous call in a
// disposable process: a goroutine timeout alone would leak the parser's work.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestInspectPDF_PathologicalInputsBounded(t *testing.T) {
	if kind := os.Getenv("PDF1968_INSPECT_CHILD"); kind != "" {
		pdfFix5InspectChild(t, kind)
		return
	}
	for _, kind := range []string{"ordinary", "parent-cycle", "xref-size", "flate-expansion"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 6*pdfBoundScale*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInspectPDF_PathologicalInputsBounded$", "-test.v", "-test.timeout="+strconv.Itoa(5*pdfBoundScale)+"s")
			cmd.Env = append(os.Environ(), "PDF1968_INSPECT_CHILD="+kind, "GOTRACEBACK=none")
			out, err := cmd.CombinedOutput()
			if !bytes.Contains(out, []byte("PDF_INSPECT_STARTED")) {
				t.Fatalf("containment instrument never reached inspection: %v\n%s", err, out)
			}
			if err != nil {
				t.Fatalf("F1: %s inspection must terminate with a typed error or bounded result, not a hang/process failure: %v\n%s", kind, err, out)
			}
		})
	}
}

func pdfFix5InspectChild(t *testing.T, kind string) {
	t.Helper()
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_AS, &limit); err != nil {
		t.Fatal(err)
	}
	// Bound additional address space, not mappings the Go runtime (or the
	// race instrument) already reserved before this test starts. Otherwise
	// the containment itself can reject an ordinary PDF under go test -race.
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
	data := pdfFix5ResourcePDF(t, kind)
	t.Log("PDF_INSPECT_STARTED", kind, "raw_bytes", len(data))
	type outcome struct {
		info PDFInfo
		err  error
	}
	done := make(chan outcome, 1)
	go func() { info, err := InspectPDF(data); done <- outcome{info, err} }()
	select {
	case got := <-done:
		if kind == "ordinary" && got.err != nil {
			t.Fatalf("ordinary positive control must parse successfully: %v", got.err)
		}
		if kind != "ordinary" {
			// R3-SEC-1: the public InspectPDF competes with the default ten
			// second sampled parser clock, and which safety stop (a structural
			// bound, the work meter, or the clock) ends a hostile file first
			// depends on the machine, most of all under -race. The docs
			// (docs/operations/security-considerations.md) allow work or time
			// to stop it, with the typed no-text outcome. So this public check
			// asks only for the documented safe outcome: ErrPDFMalformed, no
			// text, inside the watchdog below. That each structural bound
			// itself fires is proved at the parser level with a clock that
			// cannot compete: TestFix10_StructuralBoundsStopWithTheirStructureLimit
			// in internal/pdf.
			if got.err == nil || !errors.Is(got.err, ErrPDFMalformed) || got.info.Text != "" {
				t.Fatalf("F1: %s must end as ErrPDFMalformed with no text, got err=%v text=%d bytes", kind, got.err, len(got.info.Text))
			}
		}
		if got.err != nil {
			if !errors.Is(got.err, ErrPDFMalformed) && !errors.Is(got.err, ErrPDFEncrypted) && !errors.Is(got.err, ErrPDFUnsupportedVersion) {
				t.Fatalf("FR-6: untyped inspection error: %T %v", got.err, got.err)
			}
		} else if kind == "ordinary" {
			if got.info.Pages != 1 || got.info.Text != "resource control" {
				t.Fatalf("ordinary positive control: %+v", got.info)
			}
		} else if len(got.info.Text) > 25<<20 {
			t.Fatalf("F1: inspection returned %d bytes of expanded text, above the bounded document safety envelope", len(got.info.Text))
		}
	case <-time.After(2 * pdfBoundScale * time.Second):
		t.Fatalf("F1: %s inspection did not terminate within the %ds safety deadline", kind, 2*pdfBoundScale)
	}
}

func pdfFix5ResourcePDF(t *testing.T, kind string) []byte {
	t.Helper()
	switch kind {
	case "ordinary":
		return buildCompressedPDF(t, "resource control")
	case "parent-cycle":
		return assemblePDF("%PDF-1.4", []pdfObj{
			{num: fixtureCatalogNum, body: fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", fixturePagesNum)},
			{num: fixturePagesNum, body: "<< /Type /Pages /Count 1 /Kids [3 0 R] >>"},
			{num: 3, body: "<< /Type /Page /Parent 3 0 R /Contents 4 0 R >>"},
			{num: 4, body: "<< /Length 0 >>\nstream\n\nendstream"},
		}, "")
	case "xref-size":
		// A tiny xref stream advertising 2^30 entries must be rejected before
		// input-controlled allocation. Its Index contains only object zero.
		header := "%PDF-1.5\n"
		return []byte(fmt.Sprintf("%s1 0 obj\n<< /Type /XRef /Size 1073741824 /Index [0 1] /W [1 1 1] /Length 3 /Root 2 0 R >>\nstream\n\x00\x00\x00\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", header, len(header)))
	case "flate-expansion":
		// 32 MiB of repeated printable text compresses to a small input. A
		// correct parser may reject it OR cap work/output, never inflate forever.
		return buildCompressedPDF(t, strings.Repeat("X", 32<<20))
	default:
		t.Fatalf("unknown resource fixture %q", kind)
		return nil
	}
}
