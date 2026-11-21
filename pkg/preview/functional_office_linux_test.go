//go:build linux

package preview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/tests/officepreview/realprograms"
	pdfreader "github.com/ledongthuc/pdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-114/T-115/T-116: spec §3.6 at 4763de351 plus founder O2/O3 rulings.
// Real Office, real Service/launcher, enforcing Landlock, source-defined
// 3/6/4-page fixtures, actual exit-81 restart, inotify cleanup, immediate repeat.
// No deferred private-folder/filter/network hardening. Linux only (T-117).
func TestPreviewOffice_T114_T115_T116_LinuxFormatsRestartCleanupAndRepeat(t *testing.T) {
	officeRoot, _ := realprograms.Roots(t, true, false)
	functionalPinDataOutsideTmp(t)
	if !primeKernelSandbox(t) {
		return
	}
	functionalSocketInstrumentControl(t)
	root := functionalRecordedOffice(t, officeRoot)
	home := t.TempDir()
	manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Operator: "office-functional-test", Folder: root})
	require.NoError(t, err)
	t.Logf("real LibreOffice version=%s; parent Landlock enforcing; original fixture=%s", manifest.Version, officeRoot)
	self, err := os.Executable()
	require.NoError(t, err)
	svc, err := New(Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)
	for _, doc := range functionalDocuments(t) {
		t.Logf("source fixture %s sha256=%x expected pages=%d source strings=%v", doc.ID, sha256.Sum256(doc.Data), doc.Pages, doc.Text)
		t.Run(doc.ID, func(t *testing.T) {
			// Same fixture and real binary work outside the per-converter
			// restriction before a failure can be attributed to confinement.
			functionalOutsideControl(t, root, doc)
			var previousWork string
			for callNumber := 1; callNumber <= 2; callNumber++ {
				t.Run(fmt.Sprintf("call-%d", callNumber), func(t *testing.T) {
					work := functionalPreviewCall(t, svc, root, doc, callNumber)
					if previousWork != "" && work != "" {
						assert.NotEqual(t, previousWork, work, "T-116: an immediate repeat uses a new call/profile")
					}
					previousWork = work
				})
				// No fixture reset, socket removal, delay or service rebuild here.
				// Attempt call 2 even if call 1 failed: it is independent evidence.
			}
		})
	}
}

type functionalCapture struct {
	Work    string
	CallDir string
	Starts  int
	Receipt *os.File
}

func (c *functionalCapture) observe(t *testing.T, cs converterStart) {
	t.Helper()
	work := previewWorkDirOf(t, cs.args)
	if c.Receipt == nil {
		c.Work, c.CallDir = work, filepath.Dir(work)
		f, err := os.OpenFile(filepath.Join(work, functionalReceiptName), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err, "instrument: hold telemetry descriptor before the real start")
		c.Receipt = f
		t.Cleanup(func() { _ = f.Close() })
	}
	require.Equal(t, c.Work, work, "T-114: exit-81 restart stays in the same call/profile")
	c.Starts++
}

func functionalPreviewCall(t *testing.T, svc *Service, root string, doc functionalDocument, number int) string {
	t.Helper()
	capture := &functionalCapture{}
	testHookConverterStart = func(cs converterStart) { capture.observe(t, cs) }
	t.Cleanup(func() { testHookConverterStart = nil })
	finishObservation := functionalObserve(t, root)
	t.Cleanup(func() { _ = finishObservation() })
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	adm, err := svc.Admit(ctx, true)
	require.NoError(t, err)
	defer adm.Release()
	result, previewErr := svc.Preview(ctx, adm, Request{Name: "functional" + doc.Ext, Ext: doc.Ext, Data: doc.Data, Default: true})
	observation := finishObservation()

	assert.NoError(t, previewErr, "T-114/T-116: real confined conversion %d must succeed", number)
	if result == nil {
		assert.Fail(t, "T-114/T-116: no readable converted PDF", "call=%d error=%v", number, previewErr)
	} else {
		assert.Empty(t, result.Unconfined, "T-114: unconfined conversion is not acceptance")
		functionalAssertPDFText(t, result.PDF, doc.Text, doc.Pages)
		assert.Equal(t, doc.Pages, result.PageCount, "T-114: page count from source-defined fixture")
		var text strings.Builder
		for _, page := range result.Text {
			text.WriteString(page.Text)
			text.WriteByte(' ')
		}
		for _, expected := range doc.Text {
			assert.Contains(t, strings.Join(strings.Fields(text.String()), " "), expected, "T-114: real converted content, not page count alone")
		}
	}
	assert.Equal(t, 2, capture.Starts, "T-114: two real launcher starts with a fresh profile")
	if capture.Receipt == nil {
		assert.Fail(t, "instrument: no real converter start was observed")
	} else {
		_, err := capture.Receipt.Seek(0, io.SeekStart)
		require.NoError(t, err)
		receipts := functionalReadExits(t, capture.Receipt)
		exits := make([]int, 0, len(receipts))
		pids := make([]int, 0, len(receipts))
		for _, receipt := range receipts {
			exits = append(exits, receipt.Exit)
			pids = append(pids, receipt.PID)
			assert.Equal(t, filepath.Join(root, "recorded", "program", "soffice.bin"), receipt.Executable, "instrument: each result comes from unchanged real LibreOffice")
			assert.Positive(t, receipt.PID)
			functionalAssertProcessGone(t, receipt.PID, receipt.PIDStart, observation.Starts)
			functionalAssertProcessGone(t, receipt.Recorder, receipt.RecorderStart, observation.Starts)
		}
		t.Logf("call=%d actual real LibreOffice exits=%v pids=%v", number, exits, pids)
		assert.Equal(t, []int{81, 0}, exits, "T-114: actual exit-81 restart followed by successful second start")
		if len(pids) == 2 {
			assert.NotEqual(t, pids[0], pids[1], "two actual real program processes")
		}
	}
	functionalAssertClean(t, root, capture, observation)
	return capture.Work
}

func functionalReadExits(t *testing.T, r io.Reader) []functionalOfficeExit {
	t.Helper()
	decoder := json.NewDecoder(r)
	var receipts []functionalOfficeExit
	for {
		var receipt functionalOfficeExit
		err := decoder.Decode(&receipt)
		if err == io.EOF {
			return receipts
		}
		require.NoError(t, err, "instrument: real-program telemetry must be complete JSON")
		receipts = append(receipts, receipt)
	}
}

func functionalAssertPDFText(t *testing.T, data []byte, expected []string, pages int) {
	t.Helper()
	require.True(t, bytes.HasPrefix(data, []byte("%PDF-")), "T-114: real PDF bytes required")
	reader, err := pdfreader.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err, "T-114: independently readable PDF, not just an exit code")
	require.Equal(t, pages, reader.NumPage(), "T-114: exact count from FX-XLSX3/FX-DOCX6/FX-PPTX4 source")
	var text strings.Builder
	for page := 1; page <= reader.NumPage(); page++ {
		plain, readErr := reader.Page(page).GetPlainText(nil)
		require.NoError(t, readErr)
		text.WriteString(plain)
		text.WriteByte(' ')
	}
	for _, sourceText := range expected {
		assert.Contains(t, strings.Join(strings.Fields(text.String()), " "), sourceText, "T-114: literal text comes from the source fixture, never from observed output")
	}
}

func functionalAssertClean(t *testing.T, root string, capture *functionalCapture, observation functionalObservation) {
	t.Helper()
	assert.Empty(t, observation.Errors, "T-115 instrument: socket/process observation must not lose errors or overflow")
	assert.Positive(t, observation.Samples, "instrument: observer must actually scan")
	t.Logf("T-115 instrument: %d procfs samples; %d PIDs with retained exec/exit permission races (still-live failures remain fatal)", observation.Samples, len(observation.Pending))
	live, err := functionalUnixTable()
	require.NoError(t, err)
	pipes := 0
	for inode, socket := range observation.Sockets {
		assert.NotContains(t, live, inode, "T-115: a conversion-owned Unix socket is still live")
		if socket.Path == "" || strings.HasPrefix(socket.Path, "@") {
			continue
		}
		_, statErr := os.Lstat(socket.Path)
		assert.ErrorIs(t, statErr, os.ErrNotExist, "T-115: conversion-owned socket path left behind: %s", socket.Path)
		name := filepath.Base(socket.Path)
		if !strings.HasPrefix(name, "OSL_PIPE_") {
			continue
		}
		pipes++
		assert.Positive(t, observation.Created[socket.Path], "T-115: pre-call /tmp watch must see THIS process-owned pipe created, not an unrelated socket")
		assert.True(t, strings.HasPrefix(name, "OSL_PIPE_"+strconv.Itoa(os.Getuid())+"_"), "T-114/O3: socket name must carry the numeric account ID; got %s", name)
		assert.NotZero(t, socket.Mode&os.ModeSocket, "T-114/O3 instrument: actual socket mode must have been observed")
		assert.Zero(t, socket.Mode.Perm()&0002, "T-114/O3: socket must not be world-writable when account lookup works")
		t.Logf("T-114/T-115: observed socket=%s mode=%#o inotify creates=%d; absent after return", name, socket.Mode.Perm(), observation.Created[socket.Path])
	}
	assert.Positive(t, pipes, "T-115: no conversion-owned OSL_PIPE socket observed; its absence is NOT cleanup proof")
	assert.NotEmpty(t, capture.CallDir, "T-115 instrument: real call folder must be observed")
	if capture.CallDir != "" {
		_, statErr := os.Lstat(capture.CallDir)
		assert.ErrorIs(t, statErr, os.ErrNotExist, "T-115: call folder must already be gone before test cleanup")
	}
	pids, err := functionalOfficePIDs(root)
	require.NoError(t, err)
	assert.Empty(t, pids, "T-115: no process from this isolated LibreOffice install may remain")
}

func functionalOutsideControl(t *testing.T, root string, doc functionalDocument) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"profile", "home", "tmp", "out"} {
		require.NoError(t, os.Mkdir(filepath.Join(dir, name), 0700))
	}
	input := filepath.Join(dir, "fixture"+doc.Ext)
	require.NoError(t, os.WriteFile(input, doc.Data, 0600))
	f, err := os.OpenFile(filepath.Join(dir, functionalReceiptName), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	for start := 1; start <= 2; start++ {
		cmd := exec.CommandContext(ctx, filepath.Join(root, "program", "soffice.bin"),
			"--headless", "--invisible", "--nologo", "--nodefault", "--norestore", "--nolockcheck",
			"-env:UserInstallation="+fileURL(filepath.Join(dir, "profile")), "--convert-to", "pdf", "--outdir", filepath.Join(dir, "out"), input)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "HOME=" + filepath.Join(dir, "home"), "TMPDIR=" + filepath.Join(dir, "tmp")}
		output, runErr := cmd.CombinedOutput()
		if start == 1 {
			var exitErr *exec.ExitError
			require.ErrorAs(t, runErr, &exitErr, "outside first start must demonstrate actual fresh-profile restart: %s", output)
			require.Equal(t, 81, exitErr.ExitCode(), "outside fixture must really use LibreOffice's exit-81 startup: %s", output)
		} else {
			require.NoError(t, runErr, "outside real conversion control: %s", output)
		}
	}
	pdf, err := os.ReadFile(filepath.Join(dir, "out", "fixture.pdf"))
	require.NoError(t, err, "outside exact same format must produce a readable PDF")
	functionalAssertPDFText(t, pdf, doc.Text, doc.Pages)
	receipts := functionalReadExits(t, f)
	require.Len(t, receipts, 2)
	require.Equal(t, []int{81, 0}, []int{receipts[0].Exit, receipts[1].Exit})
	t.Logf("outside %s fixture control: real exits 81 -> 0, expected source text present", doc.Ext)
}
