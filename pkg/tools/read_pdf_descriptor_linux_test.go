//go:build linux

package tools

// #1968 I4, FR-5/8 and OpenNonBlockingRead's existing descriptor invariant.
// Replace only the filesystem entry, after the checked descriptor's sniff.
// The parser, readOpenFile and PathHandle implementations stay real.

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/elicify-ai/omnipus/pkg/config"
)

type pdfFix5SwapAfterRead struct {
	fs.File
	swap    func() error
	didSwap bool
}

func (f *pdfFix5SwapAfterRead) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	if !f.didSwap && n > 0 {
		f.didSwap = true
		if swapErr := f.swap(); swapErr != nil {
			return n, swapErr
		}
	}
	return n, err
}

// Preserve the original descriptor's seek ability. A repair that consumes the
// checked file (instead of reopening a path) must be able to rewind the sniff.
func (f *pdfFix5SwapAfterRead) Seek(offset int64, whence int) (int64, error) {
	s, ok := f.File.(interface {
		Seek(offset int64, whence int) (int64, error)
	})
	if !ok {
		return 0, fs.ErrInvalid
	}
	return s.Seek(offset, whence)
}

func TestReadPDF_CheckedDescriptorSurvivesFIFOSwap(t *testing.T) {
	if mode := os.Getenv("PDF1968_FIFO_CHILD"); mode != "" {
		pdfFix5DescriptorChild(t, mode)
		return
	}
	for _, mode := range []string{"rooted", "unrestricted"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReadPDF_CheckedDescriptorSurvivesFIFOSwap$", "-test.v", "-test.timeout=5s")
			cmd.Env = append(os.Environ(), "PDF1968_FIFO_CHILD="+mode)
			out, err := cmd.CombinedOutput()
			if !bytes.Contains(out, []byte("PDF_FIFO_SNIFF_SWAPPED")) {
				t.Fatalf("swap instrument never reached the checked sniff: %v\n%s", err, out)
			}
			if err != nil {
				t.Fatalf("I4: checked PDF descriptor must not block on replacement FIFO (%s): %v\n%s", mode, err, out)
			}
		})
	}
}

func pdfFix5DescriptorChild(t *testing.T, mode string) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "report.pdf")
	data := buildTextPDF(t, "1.4", 1, true, nil)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	policy, err := ResolveTurnFSPolicy(ctx, root, false)
	if err != nil {
		t.Fatal(err)
	}
	handle := &PathHandle{abs: path, policy: policy}
	if mode == "rooted" {
		handle.root, err = os.OpenRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		handle.rel = "report.pdf"
	}
	defer handle.Close()
	file, err := handle.OpenNonBlockingRead()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	checked := &pdfFix5SwapAfterRead{File: file, swap: func() error {
		if renameErr := os.Rename(path, path+".original"); renameErr != nil {
			return renameErr
		}
		if fifoErr := unix.Mkfifo(path, 0o600); fifoErr != nil {
			return fifoErr
		}
		t.Log("PDF_FIFO_SNIFF_SWAPPED", mode)
		return nil
	}}
	reader := NewReadFileTool(root, false, MaxReadFileSize)
	done := make(chan *ToolResult, 1)
	go func() { done <- reader.readOpenFile(ctx, policy, handle, checked, path, 0, MaxReadFileSize, false) }()
	select {
	case got := <-done:
		if got == nil {
			t.Fatal("I4: read produced no explicit result")
		}
		if got.IsError {
			if !strings.Contains(strings.ToLower(got.ForLLM), "regular") && !strings.Contains(strings.ToLower(got.ForLLM), "changed") {
				t.Fatalf("I4: replacement refusal must identify the source change/nonregular file: %s", got.ForLLM)
			}
			return
		}
		if len(got.InspectionPDFs) != 1 || !bytes.Equal(got.InspectionPDFs[0].Bytes, data) {
			t.Fatalf("I4: success must consume exact original checked PDF, not replacement: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("I4: PDF read blocked reopening replacement FIFO after checked regular descriptor was sniffed")
	}
}
