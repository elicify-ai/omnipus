package tools

import (
	"fmt"
	"strings"
)

const sandboxDenialSummary = "The command was blocked by the security sandbox (installing software / spawning processes is not permitted in this environment)."

const sandboxDenialGuidance = "This was blocked by the sandbox and cannot be worked around by installing software or changing commands. Use the built-in tools or report the limitation to the user."

// spawnContextTokens are the low-level tokens that, when co-occurring with a
// generic EPERM/EAGAIN phrase, confirm the error originated from a kernel-level
// process-spawn or package-manager denial rather than an ordinary permission
// check (e.g. chmod on a user file, a busy TCP socket, file-locking).
var spawnContextTokens = []string{
	"fork", "clone", "exec", "seccomp", "sigsys",
	"namespace", "unshare", "ptrace",
	"apt", "dpkg", "snap",
}

// hasSpawnContext reports whether low (already lowercased) contains at least
// one token that ties an EPERM/EAGAIN error to a spawn or package-manager
// context. Mirrors the hasForkOOM pattern: require co-occurrence.
func hasSpawnContext(low string) bool {
	for _, tok := range spawnContextTokens {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
}

// summarizeSandboxDenial inspects command stderr for signatures that indicate a
// kernel/sandbox-level denial — seccomp fork blocks, POSIX permission denials on
// apt/dpkg/snap lock paths, and Chromium snap-install prompts. When matched,
// blocked=true and summary is a concise, non-leaky message safe to show to users
// and the LLM. When not matched, blocked=false and summary is empty — the caller
// leaves the result unchanged.
//
// The summary never contains the raw substrings "Cannot fork", "Permission denied",
// or a lock-file path, so sandbox internals cannot leak through to the chat UI.
//
// FIX A1: "operation not permitted" (EPERM) and "resource temporarily unavailable"
// (EAGAIN) are now only treated as sandbox denials when they CO-OCCUR with a
// spawn/package-manager context token (fork, clone, exec, seccomp, sigsys,
// namespace, unshare, ptrace, apt, dpkg, snap). This prevents ordinary non-sandbox
// failures — e.g. `chmod: Operation not permitted` on a read-only file, or a busy
// file lock returning EAGAIN — from being misclassified as sandbox denials.
func summarizeSandboxDenial(stderr string) (summary string, blocked bool) {
	low := strings.ToLower(stderr)

	// Unambiguous seccomp / rlimit process-spawn blocks. These phrases are
	// specific enough that no co-occurrence requirement is needed:
	//   "cannot fork"       — shell reports fork(2) failure directly
	//   "fork: retry"       — shell retry loop on EAGAIN during fork(2)
	//   "fork: Cannot allocate memory" — RLIMIT_NPROC / seccomp SIGSYS OOM path
	hasForkKeyword := strings.Contains(low, "cannot fork") || strings.Contains(low, "fork: retry")
	hasForkOOM := strings.Contains(low, "fork") && strings.Contains(low, "cannot allocate memory")
	if hasForkKeyword || hasForkOOM {
		return sandboxDenialSummary, true
	}

	// "resource temporarily unavailable" (EAGAIN) — only a sandbox denial when
	// it co-occurs with a spawn/package-manager context token. A bare EAGAIN on
	// a socket or file lock is NOT a sandbox denial.
	if strings.Contains(low, "resource temporarily unavailable") && hasSpawnContext(low) {
		return sandboxDenialSummary, true
	}

	// "operation not permitted" (EPERM) — only a sandbox denial when it
	// co-occurs with a spawn/package-manager context token. Ordinary EPERM from
	// chmod, kill, or network ops is NOT a sandbox denial.
	if strings.Contains(low, "operation not permitted") && hasSpawnContext(low) {
		return sandboxDenialSummary, true
	}

	// apt/dpkg/snap package-manager lock denials. These may or may not include
	// "permission denied" in the message (dpkg "could not get lock" does not);
	// match on the lock-path presence alone.
	if strings.Contains(low, "/var/lib/apt") ||
		strings.Contains(low, "/var/lib/dpkg") {
		return sandboxDenialSummary, true
	}

	// Permission denied on other lock paths or snap.
	if strings.Contains(low, "permission denied") {
		if strings.Contains(low, "lock file") ||
			strings.Contains(low, "snap") {
			return sandboxDenialSummary, true
		}
	}

	// Chromium / snap install prompts (unambiguous).
	if strings.Contains(low, "requires the chromium snap") ||
		strings.Contains(low, "snap install") {
		return sandboxDenialSummary, true
	}

	return "", false
}

// sandboxDenialResult builds the standard sanitized ToolResult for a command the
// sandbox blocked: a clean summary (no raw kernel stderr) that still preserves the
// exit code and any stdout, with IsError + the do-not-retry Guidance. ForUser stays
// empty (the raw kernel text must not reach the user).
//
// Pass exitCode=-1 when the process was killed by a signal (displays "killed by signal"
// instead of a numeric code). Pass a non-empty stdout to append it after the summary so
// the model can reason about what ran before the denial.
func sandboxDenialResult(exitCode int, stdout string) *ToolResult {
	var msg string
	if exitCode == -1 {
		msg = sandboxDenialSummary + " [command killed by signal]"
	} else {
		msg = sandboxDenialSummary + fmt.Sprintf(" [command exited with code %d]", exitCode)
	}
	if strings.TrimSpace(stdout) != "" {
		msg += "\nstdout:\n" + stdout
	}
	return &ToolResult{
		ForLLM:   msg,
		ForUser:  "",
		IsError:  true,
		Guidance: sandboxDenialGuidance,
	}
}

// isSnapInstallPrompt reports whether stderr contains only a Chromium/snap
// install prompt — the one case where an exit-0 command's stderr warrants
// a note. All other signatures require a non-zero exit code to be meaningful.
func isSnapInstallPrompt(stderr string) bool {
	low := strings.ToLower(stderr)
	return strings.Contains(low, "requires the chromium snap") ||
		strings.Contains(low, "snap install")
}
