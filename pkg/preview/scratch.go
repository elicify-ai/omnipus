// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Owner-record states (AD-8).
const (
	statePreparing = "preparing"
	stateRunning   = "running"
	stateDone      = "done"
)

// A call folder holds the owner record and the scratch folder (spec §3.4 D1).
const (
	ownerFile = "owner.json"
	workName  = "work"
)

// socketDirPattern only guards against a damaged record: the record is out of
// the converter's reach (spec §3.4 D1), and its socket_dir is published
// straight after call.makeSocketDir created that folder. A path of any other
// shape — one that climbs out through "..", for one — is never removed. A link
// at a matching name is safe too: RemoveAll removes the link, never its target.
var socketDirPattern = regexp.MustCompile(`^/private/tmp/o-[0-9a-f]{8}$`)

// procIdentity is what recovery must match before it kills anything.
type procIdentity struct {
	PID        int    `json:"pid"`
	Start      string `json:"start"`
	Executable string `json:"executable"`
}

// ownerRecord is $OMNIPUS_HOME/tmp/preview-calls/<id>/owner.json, beside the
// scratch folder and out of the converter's reach (spec §3.4 D1). It names no
// process group: recovery stops the launcher's own group, whose number is the
// launcher's PID (D4).
type ownerRecord struct {
	State        string `json:"state"`
	GatewayPID   int    `json:"gateway_pid"`
	GatewayStart string `json:"gateway_start"`
	// Launcher is set once run-limited is started, before its go-byte.
	Launcher *procIdentity `json:"launcher,omitempty"`
	// Executables the launcher PID may legitimately show: the launcher
	// itself before execve, the target after it, and a LibreOffice helper.
	Executables []string `json:"executables,omitempty"`
	SocketDir   string   `json:"socket_dir,omitempty"`
}

// scratchRoot is $OMNIPUS_HOME/tmp/preview-calls, the folder holding one call
// folder per call (spec §3.4 D1). The name is new on purpose: a converter
// could write the old tmp/preview/<id>/owner.json, so that root is never read.
func scratchRoot(home string) string { return filepath.Join(home, "tmp", "preview-calls") }

// call is one preview call: its call folder <id> (gateway only) holding the
// owner record and its scratch folder <id>/work (spec §3.4 D1).
type call struct {
	// dir is the scratch folder <id>/work, all the converter can write.
	dir string
	// realDir is dir with its links resolved, recorded before the first start.
	realDir string
	// root is the handle on dir, opened before the first start; it stays open
	// until finish. After any LibreOffice start the output read, the retry
	// cleanup, the profile reseed and the emptying of scratch go through it
	// (spec §3.1 D3.7).
	root *os.Root
	// callDir is the call folder <id>; callRoot is its handle, opened before
	// the first start. The final removal of work goes through callRoot (§3.4
	// D5); publish writes the record by path, outside the converter's reach.
	callDir    string
	callRoot   *os.Root
	socketDir  string
	mu         sync.Mutex
	rec        ownerRecord
	unconfined string
	finished   bool
}

// noteUnconfined keeps the first "without sandbox" reason of any program run
// in this call (AD-7 disclosure).
func (c *call) noteUnconfined(reason string) {
	if reason == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unconfined == "" {
		c.unconfined = reason
	}
}

func (c *call) unconfinedReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unconfined
}

func randomID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newCall creates the call folder, publishes the preparing record, creates
// the scratch folder and opens both handles (AD-8 step 1, spec §3.4 D2). No
// program has started yet, so nothing here can be raced by the converter.
func newCall(home string, gatewayPID int, gatewayStart string) (*call, error) {
	id, err := randomID(8)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(scratchRoot(home), 0o700); err != nil {
		return nil, fmt.Errorf("create preview scratch: %w", err)
	}
	callDir := filepath.Join(scratchRoot(home), id)
	if err = os.Mkdir(callDir, 0o700); err != nil {
		return nil, fmt.Errorf("create preview scratch: %w", err)
	}
	c := &call{dir: filepath.Join(callDir, workName), callDir: callDir, rec: ownerRecord{State: statePreparing, GatewayPID: gatewayPID, GatewayStart: gatewayStart}}
	if err = c.open(); err != nil {
		_ = os.RemoveAll(callDir)
		return nil, err
	}
	return c, nil
}

// open publishes the preparing record, creates the scratch folder and opens
// both handles, in the order of spec §3.4 D2. On failure every handle it
// opened is closed again; the caller removes the call folder.
func (c *call) open() error {
	if err := c.publish(); err != nil {
		return err
	}
	if err := os.Mkdir(c.dir, 0o700); err != nil {
		return fmt.Errorf("create preview scratch: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(c.dir)
	if err != nil {
		return fmt.Errorf("resolve preview scratch: %w", err)
	}
	callRoot, err := os.OpenRoot(c.callDir)
	if err != nil {
		return fmt.Errorf("open preview scratch: %w", err)
	}
	root, err := callRoot.OpenRoot(workName)
	if err != nil {
		_ = callRoot.Close()
		return fmt.Errorf("open preview scratch: %w", err)
	}
	c.realDir, c.callRoot, c.root = realDir, callRoot, root
	return nil
}

// publish writes the owner record to <id>/owner.json, a folder the converter
// cannot write (spec §3.4 D5).
func (c *call) publish() error {
	data, err := json.Marshal(c.rec)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(c.callDir, ownerFile), data, 0o600)
}

// running records the launcher identity before the go-byte (AD-8 step 3).
func (c *call) running(id procIdentity, executables []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rec.State = stateRunning
	c.rec.Launcher = &id
	c.rec.Executables = executables
	return c.publish()
}

// idle returns the record to preparing between program runs of one call
// (nothing is running then).
func (c *call) idle() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rec.State = statePreparing
	c.rec.Launcher = nil
	c.rec.Executables = nil
	return c.publish()
}

// finish marks the call done and removes its folders (AD-8 step 5, spec §3.4
// D2): the socket folder, scratch, the record and the call folder. A removal
// failure is logged and keeps the record, so startup recovery retries it. Only
// the first call acts: both handles are closed after it.
func (c *call) finish() {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	c.rec.State = stateDone
	c.rec.Launcher = nil
	_ = c.publish()
	c.mu.Unlock()
	if c.socketDir != "" {
		if err := os.RemoveAll(c.socketDir); err != nil {
			logger.WarnCF("preview", "removing preview socket folder failed", map[string]any{"error": err.Error()})
		}
	}
	if err := c.removeScratch(); err != nil {
		logger.WarnCF("preview", "removing preview scratch failed", map[string]any{"error": err.Error()})
	}
}

// makeSocketDir creates the macOS LibreOffice socket folder: a per-call 0700
// /private/tmp/o-<8 random> (shorter than the 104-byte socket path limit).
// The record names it at once (spec §3.4 D2), so a crash from here on still
// lets recovery remove it; if that publish fails the folder is removed again.
func (c *call) makeSocketDir() (string, error) {
	id, err := randomID(4)
	if err != nil {
		return "", err
	}
	dir := filepath.Join("/private/tmp", "o-"+id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rec.SocketDir = dir
	if err := c.publish(); err != nil {
		c.rec.SocketDir = ""
		return "", errors.Join(err, os.Remove(dir))
	}
	c.socketDir = dir
	return dir, nil
}

// identify captures a started process's identity.
func identify(pid int, executable string) (procIdentity, error) {
	start, err := processStart(pid)
	if err != nil {
		return procIdentity{}, err
	}
	return procIdentity{PID: pid, Start: start, Executable: executable}, nil
}

// RecoveryReport lists what startup recovery could not resolve; each entry
// becomes a log warning and a doctor line.
type RecoveryReport struct {
	Removed  int
	Killed   int
	Warnings []string
}

// Recover runs AD-8 startup recovery over $OMNIPUS_HOME/tmp/preview-calls. self
// identifies this gateway so its own folders are never touched.
func Recover(home string, selfPID int, selfStart string) RecoveryReport {
	var rep RecoveryReport
	root := scratchRoot(home)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return rep
	}
	if err != nil {
		rep.Warnings = append(rep.Warnings, "preview scratch unreadable: "+err.Error())
		return rep
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		recoverOne(dir, selfPID, selfStart, &rep)
	}
	return rep
}

// recoverOne handles one call folder <id>. It reads only <id>/owner.json and
// nothing inside work as data (spec §3.4 D3).
func recoverOne(dir string, selfPID int, selfStart string, rep *RecoveryReport) {
	name := filepath.Base(dir)
	data, err := os.ReadFile(filepath.Join(dir, ownerFile))
	if err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s kept: owner record unreadable", name))
		return
	}
	var rec ownerRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s kept: owner record unreadable", name))
		return
	}
	if rec.GatewayPID == selfPID && rec.GatewayStart == selfStart {
		return // ours
	}
	if gatewayAlive(rec.GatewayPID, rec.GatewayStart) {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s kept: its gateway (pid %d) is still running — two gateways share this home", name, rec.GatewayPID))
		return
	}
	switch rec.State {
	case statePreparing, stateDone:
		// Nothing can have started (the go-byte was never sent) or it ended.
	case stateRunning:
		if rec.Launcher == nil {
			// AD-8: a running record names the launcher before its go-byte;
			// without that identity nothing proves the program ended.
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s kept: running record has no launcher identity", name))
			return
		}
		if processAlive(rec.Launcher.PID) {
			if !identityMatches(*rec.Launcher, rec.Executables) {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s kept: process %d does not match its record (PID reused?)", name, rec.Launcher.PID))
				return
			}
			// The launcher leads its own group, so the group's number is its
			// PID (spec §3.4 D4). killGroup is a no-op on Windows, where
			// killPID ends the launcher and its Job Object (D7).
			killGroup(rec.Launcher.PID)
			killPID(rec.Launcher.PID)
			if !waitGone(rec.Launcher.PID, 5*time.Second) {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s kept: process %d did not end", name, rec.Launcher.PID))
				return
			}
			rep.Killed++
		}
	default:
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s kept: unknown state %q", name, rec.State))
		return
	}
	removeRecordedSocketDir(rec.SocketDir, name, rep)
	if err := removeCallFolder(dir); err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s could not be removed: %v", name, err))
		return
	}
	rep.Removed++
}

// removeCallFolder removes a dead call's folder in the order of spec §3.4 D3:
// work and anything else first, the record last, then the folder. A failure
// stops before the record goes, so the next start retries.
func removeCallFolder(dir string) error {
	if err := os.RemoveAll(filepath.Join(dir, workName)); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ownerFile {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(dir, ownerFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Remove(dir)
}

// removeRecordedSocketDir removes a dead call's socket folder. A recorded path
// of any other shape is left alone and reported, without repeating it: the
// converter may have written it.
func removeRecordedSocketDir(socketDir, name string, rep *RecoveryReport) {
	if socketDir == "" {
		return
	}
	if !socketDirPattern.MatchString(socketDir) {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s: its recorded socket folder was not removed: that is not a path Omnipus creates", name))
		return
	}
	if err := os.RemoveAll(socketDir); err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s: socket folder could not be removed: %v", name, err))
	}
}

func gatewayAlive(pid int, start string) bool {
	if !processAlive(pid) {
		return false
	}
	got, err := processStart(pid)
	if err != nil {
		// Alive but unidentifiable: treat as alive (keep, warn).
		return true
	}
	return got == start
}

// identityMatches requires PID, start time and executable to match.
func identityMatches(id procIdentity, executables []string) bool {
	start, err := processStart(id.PID)
	if err != nil || start != id.Start {
		return false
	}
	exe, err := processExecutable(id.PID)
	if err != nil {
		return false
	}
	for _, want := range append([]string{id.Executable}, executables...) {
		if want != "" && executableMatches(want, exe) {
			return true
		}
	}
	return false
}

func waitGone(pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !processAlive(pid)
}
