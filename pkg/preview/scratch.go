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
	"strings"
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

const ownerFile = "owner.json"

// procIdentity is what recovery must match before it kills anything.
type procIdentity struct {
	PID        int    `json:"pid"`
	Start      string `json:"start"`
	Executable string `json:"executable"`
}

// ownerRecord is $OMNIPUS_HOME/tmp/preview/<id>/owner.json.
type ownerRecord struct {
	State        string `json:"state"`
	GatewayPID   int    `json:"gateway_pid"`
	GatewayStart string `json:"gateway_start"`
	// Launcher is set once run-limited is started, before its go-byte.
	Launcher *procIdentity `json:"launcher,omitempty"`
	PGID     int           `json:"pgid,omitempty"`
	// Executables the launcher PID may legitimately show: the launcher
	// itself before execve, the target after it, and a LibreOffice helper.
	Executables []string `json:"executables,omitempty"`
	SocketDir   string   `json:"socket_dir,omitempty"`
}

// scratchRoot is $OMNIPUS_HOME/tmp/preview.
func scratchRoot(home string) string { return filepath.Join(home, "tmp", "preview") }

// call is one preview call's scratch folder and owner record.
type call struct {
	dir        string
	socketDir  string
	mu         sync.Mutex
	rec        ownerRecord
	unconfined string
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

// newCall creates the scratch folder and publishes the preparing record
// (AD-8 step 1).
func newCall(home string, gatewayPID int, gatewayStart string) (*call, error) {
	id, err := randomID(8)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(scratchRoot(home), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create preview scratch: %w", err)
	}
	c := &call{dir: dir, rec: ownerRecord{State: statePreparing, GatewayPID: gatewayPID, GatewayStart: gatewayStart}}
	if err := c.publish(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return c, nil
}

func (c *call) publish() error {
	data, err := json.Marshal(c.rec)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(c.dir, ownerFile), data, 0o600)
}

// running records the launcher identity before the go-byte (AD-8 step 3).
func (c *call) running(id procIdentity, pgid int, executables []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rec.State = stateRunning
	c.rec.Launcher = &id
	c.rec.PGID = pgid
	c.rec.Executables = executables
	c.rec.SocketDir = c.socketDir
	return c.publish()
}

// idle returns the record to preparing between program runs of one call
// (nothing is running then).
func (c *call) idle() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rec.State = statePreparing
	c.rec.Launcher = nil
	c.rec.PGID = 0
	c.rec.Executables = nil
	return c.publish()
}

// finish marks the call done and removes its folders (AD-8 step 5). A
// removal failure is logged; startup recovery retries it.
func (c *call) finish() {
	c.mu.Lock()
	c.rec.State = stateDone
	c.rec.Launcher = nil
	_ = c.publish()
	c.mu.Unlock()
	if c.socketDir != "" {
		if err := os.RemoveAll(c.socketDir); err != nil {
			logger.WarnCF("preview", "removing preview socket folder failed", map[string]any{"error": err.Error()})
		}
	}
	if err := os.RemoveAll(c.dir); err != nil {
		logger.WarnCF("preview", "removing preview scratch failed", map[string]any{"error": err.Error()})
	}
}

// makeSocketDir creates the macOS LibreOffice socket folder: a per-call 0700
// /private/tmp/o-<8 random> (shorter than the 104-byte socket path limit).
func (c *call) makeSocketDir() (string, error) {
	id, err := randomID(4)
	if err != nil {
		return "", err
	}
	dir := filepath.Join("/private/tmp", "o-"+id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
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

// Recover runs AD-8 startup recovery over $OMNIPUS_HOME/tmp/preview. self
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
		if rec.Launcher != nil && processAlive(rec.Launcher.PID) {
			if !identityMatches(*rec.Launcher, rec.Executables) {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s kept: process %d does not match its record (PID reused?)", name, rec.Launcher.PID))
				return
			}
			killGroup(rec.PGID)
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
	if rec.SocketDir != "" && strings.HasPrefix(rec.SocketDir, "/private/tmp/o-") {
		_ = os.RemoveAll(rec.SocketDir)
	}
	if err := os.RemoveAll(dir); err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("preview folder %s could not be removed: %v", name, err))
		return
	}
	rep.Removed++
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
