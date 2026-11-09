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
	"io/fs"
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
	if err := p.call.running(id, append([]string{p.self, p.target}, p.extraExes...)); err != nil {
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
	call       *call
}

// existingFolders returns list without the folders that do not exist: a
// missing system folder is left out (Landlock refuses a rule for a missing
// path; spec §3.4 D8c). Any other error keeps the folder, so the start fails
// visibly instead of reading less silently.
func existingFolders(list []string) []string {
	out := make([]string, 0, len(list))
	for _, d := range list {
		if _, err := os.Stat(d); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// officeStartHooks returns the confinement hooks for one LibreOffice start:
// conf plus, outside Windows, look-up-only access to every folder above its
// listed folders, computed from their real paths now (spec §3.4 D8d; macOS
// LibreOffice cannot start without it, Linux ignores the field). A folder
// whose real path cannot be resolved ends the call before the start.
func officeStartHooks(conf confinement) (spawnHooks, *failure) {
	if runtime.GOOS != "windows" {
		listed := append(append(append([]string(nil), conf.readExec...), conf.readOnly...), conf.readWrite...)
		if conf.socketDir != "" {
			listed = append(listed, conf.socketDir)
		}
		above, err := foldersAbove(listed...)
		if err != nil {
			return spawnHooks{}, programFailure("conversion failed: sandbox folders unavailable (" + err.Error() + ")")
		}
		conf.lookup = above
	}
	return hooksFor(conf).observedBy(testHookConverterStart), nil
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
	// LibreOffice is handed the real path of its call folder: its sandbox
	// rules and look-up folders are computed from real paths (spec §3.4
	// D8d), so a path through a symlink would need look-ups that list
	// never names.
	callDir, err := filepath.EvalSymlinks(o.call.dir)
	if err != nil {
		return nil, "", programFailure("conversion failed: scratch unavailable")
	}
	dirs := map[string]string{}
	for _, d := range []string{"in", "out", "home", "tmp", "profile/user"} {
		p := filepath.Join(callDir, filepath.FromSlash(d))
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
	// The converter's own read list (spec §3.4 D8c), the same in both file
	// models: never the calling turn's read rights.
	conf := confinement{
		readExec:  []string{o.root, filepath.Dir(o.self)},
		readOnly:  existingFolders(officeFontFolders),
		readWrite: []string{o.call.dir},
	}
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
			// Read in place, read only (spec §3.4 D8c R1); its integrity is
			// that of the program files (Component.Verify / VerifyIfChanged).
			conf.readOnly = append(conf.readOnly, o.registry)
			env = append(env, "URE_MORE_SERVICES=<"+fileURL(o.registry)+">*")
		}
	}
	if runtime.GOOS == "windows" {
		env = append(env, "TEMP="+dirs["tmp"], "TMP="+dirs["tmp"], "USERPROFILE="+dirs["home"], "SystemRoot="+os.Getenv("SystemRoot"))
	}
	args := []string{
		"--headless", "--invisible", "--nologo", "--nodefault", "--norestore", "--nolockcheck",
		"-env:UserInstallation=" + fileURL(filepath.Join(callDir, "profile")),
		"--convert-to", "pdf", "--outdir", dirs["out"], inPath,
	}
	hooks, hookFail := officeStartHooks(conf)
	if hookFail != nil {
		return nil, "", hookFail
	}
	spec := launcher.Spec{
		Self:    o.self,
		Target:  o.executable,
		Args:    args,
		Env:     env,
		Dir:     callDir,
		Limits:  launcher.Limits{MemoryBytes: officeMemory, CPUSeconds: programCPUSeconds, FileSizeBytes: officeOutputLimit, OpenFiles: officeOpenFile, Processes: officeProcesses},
		Prepare: hooks.prepare,
		Start:   hooks.start,
		Stdout:  io.Discard,
	}
	defer func() { _ = o.call.idle() }()
	// At most two starts (spec §3.1 D3): soffice.bin exits 81 ("restart
	// requested") on its first start with a fresh profile, and LibreOffice's
	// own start programs then start it again. The second start reuses this
	// call's admission slot, deadline (ctx) and profile, and is a full AD-8
	// cycle: the first launcher has exited and its group was killed (Wait),
	// and the new launcher's identity is recorded before its go-byte.
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			// Every start gets its look-up list from the real paths at
			// that start (spec §3.4 D8d).
			if hooks, hookFail = officeStartHooks(conf); hookFail != nil {
				return nil, hooks.unconfined, hookFail
			}
			spec.Prepare, spec.Start = hooks.prepare, hooks.start
		}
		proc, fail := o.startOnce(ctx, spec)
		if fail != nil {
			return nil, hooks.unconfined, fail
		}
		waitErr := proc.Wait() // already waited: returns the recorded result
		pdf, readable := readConvertedPDF(o.call)
		if readable {
			return pdf, hooks.unconfined, nil
		}
		if attempt == 1 && proc.ExitCode() == officeRestartExit {
			// Spec §3.4 D6: the first start may have replaced <id>/work; a
			// second start on a folder it chose would get sandbox rules built
			// from that folder, so the call ends instead. Limit: a process that
			// escaped the group could still swap it after this check.
			if !o.call.workUnreplaced() {
				return nil, hooks.unconfined, programFailure("conversion failed: scratch unavailable")
			}
			// "out/ holds no document.pdf before the retry" (D3.3): an
			// unreadable leftover is removed; a removal failure ends here, and
			// so does a link leading outside scratch (D3.7).
			if err := o.call.removeInside(officeOutput); err != nil {
				return nil, hooks.unconfined, programFailure("conversion failed: scratch unavailable")
			}
			// "The profile's macro, link and recalculation settings (AD-7)
			// must still be in force on the second start" (D3.4): the first
			// start may rewrite registrymodifications.xcu, so the seeded
			// settings are written again before the retry; a failure ends
			// here rather than starting with an unknown profile.
			if err := reseedOfficeProfile(o.call); err != nil {
				return nil, hooks.unconfined, programFailure("conversion failed: " + err.Error())
			}
			if err := o.call.idle(); err != nil {
				return nil, hooks.unconfined, programFailure("conversion failed: " + err.Error())
			}
			continue
		}
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
}

// reseedOfficeProfile writes the AD-7 settings again before the exit-81
// second start, through the call's scratch handle (spec §3.1 D3.7). It runs
// after the first start's process group was killed (launcher.Process.Wait).
// The first start could still have replaced any part of the path with a link,
// so a link leading outside fails here, and the old file (or a link staying
// inside) is removed and the settings are created fresh, never written
// through whatever the first start left there.
func reseedOfficeProfile(c *call) error {
	if c.removeInside(officeSettings) != nil || c.createInside(officeSettings, []byte(profileSettings)) != nil {
		return errors.New("the LibreOffice profile settings could not be restored")
	}
	return nil
}

// The two files the gateway touches after a LibreOffice start, relative to the
// call's scratch folder.
var (
	officeOutput   = filepath.Join("out", "document.pdf")
	officeSettings = filepath.Join("profile", "user", "registrymodifications.xcu")
)

// officeRestartExit is LibreOffice's EXITHELPER_NORMAL_RESTART
// (include/desktop/exithelper.h).
const officeRestartExit = 81

// startOnce runs one LibreOffice start under the launcher: identity record
// (state running) before the go-byte, Stop ends it, Wait kills the group.
// A non-nil failure ends the conversion (start failure, Stop, memory).
func (o officeConversion) startOnce(ctx context.Context, spec launcher.Spec) (*launcher.Process, *failure) {
	proc, err := launcher.Start(spec)
	if err != nil {
		return nil, programFailure("conversion failed: LibreOffice could not start (" + err.Error() + ")")
	}
	id, err := identify(proc.PID, o.self)
	if err == nil {
		err = o.call.running(id, append([]string{o.self, o.executable}, o.extraExes...))
	}
	if err == nil {
		err = proc.Go()
	}
	if err != nil {
		proc.Kill("record")
		_ = proc.Wait()
		return nil, programFailure("conversion failed: " + err.Error())
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
	_ = proc.Wait()
	if ctx.Err() != nil {
		return nil, stopFailure(ctx)
	}
	if proc.KillCause() == launcher.MemoryKillCause {
		return nil, programFailure("conversion failed: memory limit exceeded")
	}
	return proc, nil
}

// readConvertedPDF returns the converted document when the call's output is a
// regular, non-empty, size-capped file that starts like a PDF (FR-011:
// validity is judged by the output, never by exit codes). An output reached
// through a link leading outside scratch is not readable (spec §3.1 D3.7).
func readConvertedPDF(c *call) ([]byte, bool) {
	out, err := c.inside(officeOutput)
	if err != nil {
		return nil, false
	}
	info, err := c.root.Lstat(out)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > officeOutputLimit {
		return nil, false
	}
	pdf, err := c.root.ReadFile(out)
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, false
	}
	return pdf, true
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
