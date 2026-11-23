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
)

// maxCheckErrorRunes bounds the vendor error quoted in a failed check.
const maxCheckErrorRunes = 300

type checkResult struct {
	state   string
	message string
	label   string
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
func (s *Service) runCheck(ctx context.Context, entry ProgramEntry, timeout time.Duration) checkResult {
	check := entry.IdentityCheck
	store, err := s.store()
	if err != nil {
		return checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the credential store could not be read."}
	}
	token, err := store.Get(TokenReference(entry.ID))
	if err != nil {
		return checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the credential store could not be read."}
	}
	found, err := exec.LookPath(check.ProgramWord)
	if err != nil {
		return checkResult{state: StateSavedNotChecked,
			message: "Saved, not checked: " + check.ProgramWord + " was not found on this computer."}
	}
	// FR-005/S-08: a program agents could have planted never runs with
	// gateway authority; the checked, resolved file is the one executed.
	path, err := s.programPathCheck()(check.ProgramWord, found, s.cfg())
	if err != nil {
		slog.Warn("programs: account check program refused by its location check", "program", check.ProgramWord, "error", err)
		return checkResult{state: StateSavedNotChecked,
			message: "Saved, not checked: " + check.ProgramWord + " is installed in a folder agents can write to, so it was not run. Install it in a system folder."}
	}
	home, err := os.MkdirTemp("", "omnipus-program-check-")
	if err != nil {
		return checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the account check could not start."}
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
	Attach(cmd, ResolvedPrograms{Tokens: []string{token}})
	setProcessGroup(cmd)
	if startErr := cmd.Start(); startErr != nil {
		return checkResult{state: StateSavedNotChecked, message: "Saved, not checked: the account check could not start."}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		stopProcessGroup(cmd)
		<-done
		awaitProcessGroupGone(cmd)
		return checkResult{state: StateCheckFailed,
			message: fmt.Sprintf("The account check did not finish in %d s.", int(timeout/time.Second))}
	case <-ctx.Done():
		stopProcessGroup(cmd)
		<-done
		awaitProcessGroupGone(cmd)
		return checkResult{state: StateCheckFailed, message: "The account check was replaced by a newer one."}
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return checkResult{state: StateCheckFailed, message: "The account check could not run."}
		}
		said := firstLine(stderr.String())
		if said == "" {
			said = firstLine(stdout.String())
		}
		return checkResult{state: StateCheckFailed,
			message: "The account check failed. " + entry.DisplayName + " said: \"" + truncateRunes(said, maxCheckErrorRunes) + "\"."}
	}
	label, err := AccountLabel(entry.DisplayName, stdout.String(), []string{token})
	if err != nil {
		var carries *LabelCarriesTokenError
		if errors.As(err, &carries) {
			return checkResult{state: StateCheckFailed, message: carries.Error()}
		}
		return checkResult{state: StateCheckFailed, message: "The account check printed no account name."}
	}
	return checkResult{state: StateConnected, label: label}
}

// checkEnv is the check child's clean environment: the gateway PATH, a
// temporary HOME (and its Windows equivalents) and the entry's token
// variables. Nothing else from the gateway's environment is passed.
func checkEnv(entry ProgramEntry, token, home string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	for _, name := range []string{"SystemRoot", "ComSpec", "PATHEXT", "TEMP", "TMP"} {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	if v, ok := os.LookupEnv("USERPROFILE"); ok && v != "" {
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
