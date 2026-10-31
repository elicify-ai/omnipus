// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
)

// Program budgets (AD-6).
const (
	programMemory     = 512 << 20
	officeMemory      = 1 << 30
	programCPUSeconds = 60
	programWall       = 60 * time.Second
	officeOutputLimit = protocol.MaxInputConverted
	// programFileSize is the renderer/extract file-size limit: they write no
	// file at all (MV-6); 1 MiB is a guard, not an allowance (the sandbox
	// denies the writes).
	programFileSize = 1 << 20
	programOpenFile = 64
	officeOpenFile  = 4096
	officeProcesses = 16
)

// failure is an MV-1 outcome: Message is the text after "preview_document: ".
type failure struct {
	Message string
	// Kind classifies failures that change routing (renderer failed vs. the
	// document itself being unusable).
	Kind failureKind
}

type failureKind int

const (
	failDocument failureKind = iota // the document is the problem
	failProgram                     // the program failed (protocol, crash, limit)
	failStopped                     // Stop or deadline
)

func (f *failure) Error() string { return f.Message }

func docFailure(msg string) *failure     { return &failure{Message: msg, Kind: failDocument} }
func programFailure(msg string) *failure { return &failure{Message: msg, Kind: failProgram} }

// stopFailure maps a finished context to "stopped" or the 120 s timeout.
func stopFailure(ctx context.Context) *failure {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &failure{Message: "timed out after 120 s", Kind: failStopped}
	}
	return &failure{Message: "stopped", Kind: failStopped}
}

// protocolProgram is one start of omnipus-pdfium or extract-text.
type protocolProgram struct {
	self      string
	target    string
	args      []string
	confine   confinement
	call      *call
	maxInput  int64
	extraExes []string
}

// run sends req with input and returns the validated response. Pages go to
// sink as they arrive (at most one frame held per program, AD-6).
func (p protocolProgram) run(ctx context.Context, req protocol.Request, input []byte, sink protocol.PageSink) (*protocol.Response, string, error) {
	req.MaxInput = p.maxInput
	hooks := hooksFor(p.confine)
	p.call.noteUnconfined(hooks.unconfined)
	proc, err := launcher.Start(launcher.Spec{
		Self:    p.self,
		Target:  p.target,
		Args:    p.args,
		Env:     []string{},
		Dir:     p.call.dir,
		Limits:  launcher.Limits{MemoryBytes: programMemory, CPUSeconds: programCPUSeconds, FileSizeBytes: programFileSize, OpenFiles: programOpenFile, Processes: 2},
		Prepare: hooks.prepare,
		Start:   hooks.start,
	})
	if err != nil {
		return nil, hooks.unconfined, programFailure("PDF renderer failed (start: " + err.Error() + ")")
	}
	defer func() { _ = p.call.idle() }()
	if err := p.publishAndGo(proc); err != nil {
		proc.Kill("record")
		_ = proc.Wait()
		return nil, hooks.unconfined, programFailure("PDF renderer failed (" + err.Error() + ")")
	}
	wall := time.NewTimer(programWall)
	defer wall.Stop()
	go func() {
		select {
		case <-ctx.Done():
			proc.Kill("stop")
		case <-wall.C:
			proc.Kill("wall")
		case <-proc.Done():
		}
	}()
	writeErr := make(chan error, 1)
	go func() {
		writeErr <- protocol.WriteRequest(proc.Stdin, req, bytes.NewReader(input), int64(len(input)))
		// stdin stays open: closing it tells the program its parent is gone.
	}()
	resp, readErr := protocol.ReadResponse(proc.Stdout, req, sink)
	if readErr != nil {
		proc.Kill("protocol")
	}
	_ = proc.Stdin.Close()
	_, _ = io.Copy(io.Discard, proc.Stdout)
	_ = proc.Wait()
	<-writeErr
	if ctx.Err() != nil {
		return nil, hooks.unconfined, stopFailure(ctx)
	}
	if readErr != nil {
		return nil, hooks.unconfined, classifyProgramError(readErr, proc)
	}
	return resp, hooks.unconfined, nil
}

func (p protocolProgram) publishAndGo(proc *launcher.Process) error {
	id, err := identify(proc.PID, p.self)
	if err != nil {
		return fmt.Errorf("identify: %w", err)
	}
	if err := p.call.running(id, proc.PID, append([]string{p.self, p.target}, p.extraExes...)); err != nil {
		return fmt.Errorf("owner record: %w", err)
	}
	return proc.Go()
}

// classifyProgramError maps an exchange failure to an MV-1 outcome.
func classifyProgramError(err error, proc *launcher.Process) *failure {
	var ef *protocol.PeerError
	if errors.As(err, &ef) {
		switch ef.Code {
		case "encrypted":
			return docFailure("password-protected documents cannot be previewed")
		case "open_failed", "page_failed":
			return docFailure("the PDF could not be opened (damaged or unsupported)")
		case "pixel_cap":
			return programFailure("rendering stopped: pixel limit exceeded")
		case "frame_too_large":
			return programFailure("rendering stopped: page image size exceeded")
		case "oom":
			return programFailure("rendering stopped: memory exceeded")
		default:
			return programFailure("PDF renderer failed (" + ef.Code + ")")
		}
	}
	switch proc.KillCause() {
	case launcher.MemoryKillCause:
		return programFailure("rendering stopped: memory exceeded")
	case "wall":
		return programFailure("rendering stopped: time limit exceeded")
	}
	if waitErr := proc.Wait(); waitErr != nil && strings.Contains(waitErr.Error(), "cputime limit exceeded") {
		return programFailure("rendering stopped: CPU time exceeded")
	}
	if protocol.IsProtocolError(err) {
		return programFailure("PDF renderer failed (protocol)")
	}
	if tail := filteredTail(proc.Stderr()); tail != "" {
		return programFailure("PDF renderer failed (" + tail + ")")
	}
	return programFailure("PDF renderer failed (" + err.Error() + ")")
}

// officeConversion converts one Office document to PDF with LibreOffice.
type officeConversion struct {
	self       string
	executable string
	root       string
	registry   string // macOS filtered registry folder
	extraExes  []string
	readModel  confinement
	call       *call
}

// profileSettings pins the LibreOffice profile: macros never run, links are
// never updated, external referer links are blocked, formulas recalculate on
// load (AD-7). Values per the officecfg schema of the pinned release:
// Writer Content/Update/Link 2 = Never; Calc Content/Update/Link 1 = LM_NEVER;
// Calc Formula/Load/*RecalcMode 0 = recalc always.
const profileSettings = `<?xml version="1.0" encoding="UTF-8"?>
<oor:items xmlns:oor="http://openoffice.org/2001/registry" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="DisableMacrosExecution" oor:op="fuse"><value>true</value></prop></item>
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="MacroSecurityLevel" oor:op="fuse"><value>3</value></prop></item>
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="BlockUntrustedRefererLinks" oor:op="fuse"><value>true</value></prop></item>
<item oor:path="/org.openoffice.Office.Writer/Content/Update"><prop oor:name="Link" oor:op="fuse"><value>2</value></prop></item>
<item oor:path="/org.openoffice.Office.Calc/Content/Update"><prop oor:name="Link" oor:op="fuse"><value>1</value></prop></item>
<item oor:path="/org.openoffice.Office.Calc/Formula/Load"><prop oor:name="OOXMLRecalcMode" oor:op="fuse"><value>0</value></prop></item>
<item oor:path="/org.openoffice.Office.Calc/Formula/Load"><prop oor:name="ODFRecalcMode" oor:op="fuse"><value>0</value></prop></item>
<item oor:path="/org.openoffice.Office.Common/Misc"><prop oor:name="FirstRun" oor:op="fuse"><value>false</value></prop></item>
</oor:items>
`

// run converts input (named name with extension ext) and returns the PDF.
func (o officeConversion) run(ctx context.Context, input []byte, ext string) ([]byte, string, error) {
	dirs := map[string]string{}
	for _, d := range []string{"in", "out", "home", "tmp", "profile/user"} {
		p := filepath.Join(o.call.dir, filepath.FromSlash(d))
		if err := os.MkdirAll(p, 0o700); err != nil {
			return nil, "", programFailure("conversion failed: scratch unavailable")
		}
		dirs[d] = p
	}
	if err := os.WriteFile(filepath.Join(dirs["profile/user"], "registrymodifications.xcu"), []byte(profileSettings), 0o600); err != nil {
		return nil, "", programFailure("conversion failed: scratch unavailable")
	}
	inPath := filepath.Join(dirs["in"], "document"+ext)
	if err := os.WriteFile(inPath, input, 0o600); err != nil {
		return nil, "", programFailure("conversion failed: scratch unavailable")
	}
	conf := o.readModel
	conf.readExec = append(append([]string(nil), conf.readExec...), o.root, filepath.Dir(o.self))
	conf.readWrite = append(append([]string(nil), conf.readWrite...), o.call.dir)
	env := []string{
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"HOME=" + dirs["home"],
		"TMPDIR=" + dirs["tmp"],
	}
	if runtime.GOOS == "darwin" {
		sock, err := o.call.makeSocketDir()
		if err != nil {
			return nil, "", programFailure("conversion failed: socket folder unavailable")
		}
		conf.socketDir = sock
		env = append(env, "OSL_SOCKET_PATH="+sock)
		if o.registry != "" {
			env = append(env, "URE_MORE_SERVICES=<"+fileURL(o.registry)+">*")
		}
	}
	if runtime.GOOS == "windows" {
		env = append(env, "TEMP="+dirs["tmp"], "TMP="+dirs["tmp"], "USERPROFILE="+dirs["home"], "SystemRoot="+os.Getenv("SystemRoot"))
	}
	args := []string{
		"--headless", "--invisible", "--nologo", "--nodefault", "--norestore", "--nolockcheck",
		"-env:UserInstallation=" + fileURL(filepath.Join(o.call.dir, "profile")),
		"--convert-to", "pdf", "--outdir", dirs["out"], inPath,
	}
	hooks := hooksFor(conf)
	proc, err := launcher.Start(launcher.Spec{
		Self:    o.self,
		Target:  o.executable,
		Args:    args,
		Env:     env,
		Dir:     o.call.dir,
		Limits:  launcher.Limits{MemoryBytes: officeMemory, CPUSeconds: programCPUSeconds, FileSizeBytes: officeOutputLimit, OpenFiles: officeOpenFile, Processes: officeProcesses},
		Prepare: hooks.prepare,
		Start:   hooks.start,
		Stdout:  io.Discard,
	})
	if err != nil {
		return nil, hooks.unconfined, programFailure("conversion failed: LibreOffice could not start (" + err.Error() + ")")
	}
	defer func() { _ = o.call.idle() }()
	id, err := identify(proc.PID, o.self)
	if err == nil {
		err = o.call.running(id, proc.PID, append([]string{o.self, o.executable}, o.extraExes...))
	}
	if err == nil {
		err = proc.Go()
	}
	if err != nil {
		proc.Kill("record")
		_ = proc.Wait()
		return nil, hooks.unconfined, programFailure("conversion failed: " + err.Error())
	}
	go func() {
		select {
		case <-ctx.Done():
			proc.Kill("stop")
		case <-proc.Done():
		}
	}()
	// LibreOffice reads nothing from stdin; close it after the go-byte so a
	// stray read sees EOF instead of blocking.
	_ = proc.Stdin.Close()
	waitErr := proc.Wait()
	if ctx.Err() != nil {
		return nil, hooks.unconfined, stopFailure(ctx)
	}
	if proc.KillCause() == launcher.MemoryKillCause {
		return nil, hooks.unconfined, programFailure("conversion failed: memory limit exceeded")
	}
	out := filepath.Join(dirs["out"], "document.pdf")
	info, statErr := os.Lstat(out)
	if statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > officeOutputLimit {
		tail := filteredTail(proc.Stderr())
		msg := "conversion failed: LibreOffice produced no readable PDF"
		if waitErr != nil && tail == "" {
			tail = waitErr.Error()
		}
		if tail != "" {
			msg += " (" + tail + ")"
		}
		return nil, hooks.unconfined, programFailure(msg)
	}
	pdf, err := os.ReadFile(out)
	if err != nil {
		return nil, hooks.unconfined, programFailure("conversion failed: LibreOffice produced no readable PDF")
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, hooks.unconfined, programFailure("conversion failed: LibreOffice produced no readable PDF")
	}
	return pdf, hooks.unconfined, nil
}

// filteredTail keeps the last lines of a program's stderr, passed through
// the sensitive-data filters (AD-2), for the person-facing failure text.
func filteredTail(stderr string) string {
	s := strings.TrimSpace(audit.RedactCredentials(logger.ScrubSensitiveValues(stderr)))
	if len(s) > 300 {
		s = s[len(s)-300:]
	}
	return strings.Join(strings.Fields(s), " ")
}

func fileURL(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + strings.ReplaceAll(p, " ", "%20")
}
