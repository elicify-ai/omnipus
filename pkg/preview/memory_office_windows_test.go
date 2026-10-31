//go:build windows

package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/elicify-ai/omnipus/pkg/preview/extracttext"
	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

type officeWindowsJobReceipt struct {
	PID        int    `json:"pid"`
	Child      int    `json:"child"`
	Limit      uint64 `json:"limit"`
	Flags      uint32 `json:"flags"`
	QueryError string `json:"query_error"`
}

func officeWindowsJob() officeWindowsJobReceipt {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	// Microsoft QueryInformationJobObject contract: a null handle queries
	// the calling process's immediate job, including a nested job.
	// https://learn.microsoft.com/en-us/windows/win32/api/jobapi2/nf-jobapi2-queryinformationjobobject
	err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
	r := officeWindowsJobReceipt{PID: os.Getpid(), Limit: uint64(info.JobMemoryLimit), Flags: info.BasicLimitInformation.LimitFlags}
	if err != nil {
		r.QueryError = err.Error()
	}
	return r
}

func officeWindowsPublishJob(r officeWindowsJobReceipt) bool {
	raw, err := json.Marshal(r)
	return err == nil && os.WriteFile("windows-job-receipt.json", raw, 0600) == nil
}

func init() {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case launcher.ModeArg:
		os.Exit(launcher.Main(os.Args[2:]))
	case "__office-windows-job-extract":
		if !officeWindowsPublishJob(officeWindowsJob()) {
			os.Exit(70)
		}
		os.Exit(extracttext.Main())
	case "__office-windows-job-grandchild":
		if os.WriteFile("windows-grandchild-ready", []byte("ready"), 0600) != nil {
			os.Exit(71)
		}
		time.Sleep(60 * time.Second)
		os.Exit(0)
	case "__office-windows-job-bomb":
		r := officeWindowsJob()
		self, err := os.Executable()
		if err != nil {
			os.Exit(72)
		}
		child := exec.Command(self, "__office-windows-job-grandchild")
		if child.Start() != nil {
			os.Exit(73)
		}
		r.Child = child.Process.Pid
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		poll := time.NewTicker(5 * time.Millisecond)
		defer poll.Stop()
	ready:
		for {
			if data, readErr := os.ReadFile("windows-grandchild-ready"); readErr == nil && string(data) == "ready" {
				break ready
			}
			select {
			case <-deadline.C:
				os.Exit(74)
			case <-poll.C:
			}
		}
		if !officeWindowsPublishJob(r) {
			os.Exit(75)
		}
		var held [][]byte
		for {
			block := make([]byte, 8<<20)
			for i := 0; i < len(block); i += 4096 {
				block[i] = 1
			}
			held = append(held, block)
			runtime.KeepAlive(held)
			if len(held) > 256 {
				os.Exit(76) // instrument guard: 2 GiB means the 512 MiB job failed
			}
		}
	}
}

// Generate the independently specified one-page fixture in the test, keeping
// PDF's required padded xref records out of the repository's text whitespace
// gate. Exact content is job text control, on a 200x100-point page.
func officeWindowsMemoryPDF() []byte {
	stream := "BT /F1 12 Tf 10 10 Td (job text control) Tj ET\n"
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 1 /Kids [4 0 R] >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R >>", fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream)}
	var out strings.Builder
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	out.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return []byte(out.String())
}

// T-79 drives the real protocolProgram, rather than equating a killed child
// with a correctly classified user result. A protocol/launcher error is RED.
func TestWindowsExtractTextAndAllocationBombInside512MiBJob(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	input := officeWindowsMemoryPDF()
	// Instrument: this same valid PDF has the independent exact text oracle.
	controlIn, controlWriter := io.Pipe()
	defer func() { _ = controlIn.Close(); _ = controlWriter.Close() }()
	req := protocol.Request{Op: protocol.OpText, FirstPage: 1, LastPage: 1, MaxInput: protocol.MaxInputPDF}
	controlWritten := make(chan error, 1)
	go func() {
		controlWritten <- protocol.WriteRequest(controlWriter, req, bytes.NewReader(input), int64(len(input)))
	}()
	// Keep the parent stream open: EOF means parent death, not successful
	// completion, and must not race this positive control's page boundary.
	var controlOut bytes.Buffer
	require.Zero(t, extracttext.Run(controlIn, &controlOut))
	require.NoError(t, <-controlWritten)
	require.NoError(t, controlWriter.Close())
	parsed, err := protocol.ReadResponse(&controlOut, req, nil)
	require.NoError(t, err)
	require.Len(t, parsed.Text, 1)
	require.Equal(t, "job text control", strings.TrimSpace(parsed.Text[0].Text))
	for _, mode := range []string{"extract", "bomb"} {
		t.Run(mode, func(t *testing.T) {
			start, startErr := processStart(os.Getpid())
			require.NoError(t, startErr)
			c, callErr := newCall(t.TempDir(), os.Getpid(), start)
			require.NoError(t, callErr)
			defer c.finish()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			arg := "__office-windows-job-extract"
			if mode == "bomb" {
				arg = "__office-windows-job-bomb"
			}
			program := protocolProgram{self: self, target: self, args: []string{arg}, call: c, maxInput: protocol.MaxInputPDF}
			result, _, runErr := program.run(ctx, req, input, nil)
			require.NoError(t, ctx.Err(), "T-79: memory budget, not the safety deadline, must end the bomb")
			raw, readErr := os.ReadFile(filepath.Join(c.dir, "windows-job-receipt.json"))
			require.NoError(t, readErr, "actual target must reach and query its job")
			var receipt officeWindowsJobReceipt
			require.NoError(t, json.Unmarshal(raw, &receipt))
			require.Empty(t, receipt.QueryError)
			require.Equal(t, uint64(512<<20), receipt.Limit)
			require.NotZero(t, receipt.Flags&windows.JOB_OBJECT_LIMIT_JOB_MEMORY)
			require.NotZero(t, receipt.Flags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
			if mode == "extract" {
				require.NoError(t, runErr)
				require.Len(t, result.Text, 1)
				require.Equal(t, "job text control", strings.TrimSpace(result.Text[0].Text))
			} else {
				require.Positive(t, receipt.Child, "grandchild readiness was established before the bomb")
				require.Error(t, runErr)
				assert.Contains(t, runErr.Error(), "memory limit exceeded", "T-79: never a launcher crash or a protocol failure")
				assert.NotContains(t, runErr.Error(), "protocol")
				require.Eventually(t, func() bool { return !processAlive(receipt.PID) && !processAlive(receipt.Child) }, 5*time.Second, 10*time.Millisecond, "whole target tree must be gone")
			}
		})
	}
}
