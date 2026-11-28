// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/childenv"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// maxCheckErrorRunes bounds the vendor error quoted in a failed check.
const maxCheckErrorRunes = 300

type checkResult struct {
	state   string
	message string
	label   string
	// reason is a value-free class of the outcome for the audit row
	// (FR-021, ruling H50); never the message, which may quote the program.
	reason string
}

// boundedBuffer keeps at most limit bytes and drops the rest.
type boundedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runCheck runs the entry's account check for the CURRENT stored token
// (FR-005): the program word is looked up on the gateway's PATH, the child
// gets a clean environment (PATH, a temporary HOME and the token variables
// only), its output is masked at the source and capped, and the whole process
// group is stopped at the timeout.
//
// FR-047: the process starts only while r is still the entry's current
// reservation and has not been cancelled. That is re-checked under s.mu, and
// s.mu is held through the start and the process-group setup, so a launch
// that was replaced while it was on its way starts no process at all.
func (s *Service) runCheck(ctx context.Context, entry ProgramEntry, r *reservation, timeout time.Duration) checkResult {
	check := entry.IdentityCheck
	store, err := s.store()
	if err != nil {
		return checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the credential store could not be read.", reason: CheckReasonStoreUnreadable}
	}
	token, _, err := credentials.ReadProgramCredential(store, TokenReference(entry.ID))
	if err != nil {
		return checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the credential store could not be read.", reason: CheckReasonStoreUnreadable}
	}
	found, err := exec.LookPath(check.ProgramWord)
	if err != nil {
		return checkResult{state: StateSavedNotChecked,
			message: "Saved, not checked: " + check.ProgramWord + " was not found on this computer.", reason: CheckReasonProgramNotFound}
	}
	// FR-005/S-08: a program agents could have planted never runs with
	// gateway authority; the checked, resolved file is the one executed.
	path, err := s.programPathCheck()(check.ProgramWord, found, s.cfg())
	if err != nil {
		slog.Warn("programs: account check program refused by its location check", "program", check.ProgramWord, "error", err)
		return checkResult{state: StateSavedNotChecked,
			message: "Saved, not checked: " + check.ProgramWord + " is installed in a folder agents can write to, so it was not run. Install it in a system folder.", reason: CheckReasonProgramRefused}
	}
	home, err := os.MkdirTemp("", "omnipus-program-check-")
	if err != nil {
		return checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the account check could not start.", reason: CheckReasonNotStarted}
	}
	defer os.RemoveAll(home)

	limit := check.MaxOutputBytes
	if limit <= 0 {
		limit = CheckMaxOutputBytes
	}
	stdout := &boundedBuffer{limit: limit}
	stderr := &boundedBuffer{limit: limit}
	cmd := exec.Command(path, check.Args...)
	cmd.Dir = home
	cmd.Env = checkEnv(entry, token, home)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if attachErr := Attach(cmd, ResolvedPrograms{Tokens: []string{token}}); attachErr != nil {
		return checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the account check could not start.", reason: CheckReasonNotStarted}
	}
	setProcessGroup(cmd)
	started, startRes := s.startCurrentCheck(ctx, entry, r, cmd)
	if !started {
		return startRes
	}
	defer releaseProcessGroup(cmd.Process)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		stopProcessGroup(cmd.Process)
		<-done
		awaitProcessGroupGone(cmd.Process)
		return checkResult{state: StateCheckFailed,
			message: fmt.Sprintf("The account check did not finish in %d s.", int(timeout/time.Second)), reason: CheckReasonTimeout}
	case <-ctx.Done():
		stopProcessGroup(cmd.Process)
		<-done
		awaitProcessGroupGone(cmd.Process)
		return replacedCheckResult
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return checkResult{state: StateCheckFailed, message: "The account check could not run.", reason: CheckReasonRunError}
		}
		said := firstLine(stderr.String())
		if said == "" {
			said = firstLine(stdout.String())
		}
		return checkResult{state: StateCheckFailed,
			message: "The account check failed. " + entry.DisplayName + " said: \"" + truncateRunes(said, maxCheckErrorRunes) + "\".", reason: CheckReasonExitStatus}
	}
	label, err := AccountLabel(entry.DisplayName, stdout.String(), []string{token})
	if err != nil {
		var carries *LabelCarriesTokenError
		if errors.As(err, &carries) {
			return checkResult{state: StateCheckFailed, message: carries.Error(), reason: CheckReasonLabelRefused}
		}
		return checkResult{state: StateCheckFailed, message: "The account check printed no account name.", reason: CheckReasonNoAccountName}
	}
	return checkResult{state: StateConnected, label: label, reason: CheckReasonAccountNamed}
}

// checkEnv is the check child's clean environment: the gateway PATH, a
// temporary HOME (and its Windows equivalents) and the entry's token
// variables. Nothing else from the gateway's environment is passed.
// checkEnvKeys are the only parent-environment keys the account check keeps
// (matched with childenv.SameKey: case-insensitively on Windows).
var checkEnvKeys = []string{"PATH", "SystemRoot", "ComSpec", "PATHEXT", "TEMP", "TMP"}

func isCheckEnvKey(key string) bool {
	for _, k := range checkEnvKeys {
		if childenv.SameKey(k, key) {
			return true
		}
	}
	return false
}

// checkEnv builds the account check environment (FR-239, ruling D10 A2). It
// starts from the scrubbed child environment (childenv.Filter), never from
// the raw gateway environment, keeps only checkEnvKeys, then adds the
// temporary home (HOME, and USERPROFILE when the scrubbed environment carries
// that key) and the entry's token variables.
func checkEnv(entry ProgramEntry, token, home string) []string {
	scrubbed := childenv.Filter()
	env := make([]string, 0, len(checkEnvKeys)+2+len(entry.TokenVariables))
	hasUserProfile := false
	for _, kv := range scrubbed {
		key, _, _ := strings.Cut(kv, "=")
		if childenv.SameKey(key, "USERPROFILE") {
			hasUserProfile = true
		}
		if isCheckEnvKey(key) {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+home)
	if hasUserProfile {
		env = append(env, "USERPROFILE="+home)
	}
	for _, v := range entry.TokenVariables {
		env = append(env, v+"="+token)
	}
	return env
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

var _ io.Writer = (*boundedBuffer)(nil)

// replacedCheckResult is the result of a check whose reservation was
// replaced; finish discards it, because the reservation is no longer current.
var replacedCheckResult = checkResult{state: StateCheckFailed, message: "The account check was replaced by a newer one.", reason: CheckReasonReplaced}

// startCurrentCheck starts cmd only if r is still the entry's current,
// uncancelled reservation (FR-047, S-139), holding s.mu across the check,
// cmd.Start and the process-group setup so a newer reservation cannot slip
// in between. It reports whether the process is running; when it is not, the
// result to return. FR-005: when the process tree cannot be owned (Windows
// Job Object), the started process is stopped and the check is not trusted.
func (s *Service) startCurrentCheck(ctx context.Context, entry ProgramEntry, r *reservation, cmd *exec.Cmd) (bool, checkResult) {
	s.mu.Lock()
	if s.checks[entry.ID] != r || ctx.Err() != nil {
		s.mu.Unlock()
		return false, replacedCheckResult
	}
	if startErr := cmd.Start(); startErr != nil {
		s.mu.Unlock()
		return false, checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the account check could not start.", reason: CheckReasonNotStarted}
	}
	groupErr := startProcessGroup(cmd)
	s.mu.Unlock()
	if groupErr != nil {
		slog.Warn("programs: account check stopped: its processes could not be owned", "program", entry.ID, "error", groupErr)
		_ = cmd.Wait()
		return false, checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the account check could not start.", reason: CheckReasonNotStarted}
	}
	return true, checkResult{}
}
