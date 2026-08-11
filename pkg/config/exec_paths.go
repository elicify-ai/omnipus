// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "runtime"

// DefaultAllowedExecPaths returns the install-time seed for
// OmnipusSandboxConfig.AllowedExecPaths: directories an agent may READ and
// EXECUTE from, never write to.
//
// # Why this list exists
//
// The kernel policy grants execute only on the system binary directories
// (/bin, /usr/bin, …). On a real developer machine almost nothing lives there:
// Homebrew installs to /opt/homebrew (Apple Silicon) or /usr/local (Intel), and
// language toolchains are installed per-user by version managers. Without this
// seed an agent on macOS cannot run node, npm, python, cargo or go at all —
// which is not a security posture, it is a broken product, and the predictable
// operator response is to switch the sandbox off entirely.
//
// # Why the entries are narrow
//
// Each root is named at the subdirectory level rather than granting a whole
// prefix. Granting /usr/local wholesale would also expose /usr/local/etc —
// which on a real machine contains WireGuard private keys, rustup config and
// service state — and /usr/local/var. Naming bin/sbin/lib/opt/Cellar/share/
// Frameworks/include covers every Homebrew binary (their bin/x -> ../Cellar/…
// symlink chains resolve inside this set) while leaving etc/ and var/ closed.
//
// For the same reason no $HOME-wide entry appears here: each version manager
// gets its own root, so granting fnm does not expose ~/.ssh or ~/Documents.
//
// Entries that do not exist on a given machine are skipped harmlessly by the
// policy layer, so this list is a superset covering common setups.
func DefaultAllowedExecPaths() []string {
	common := []string{
		// Per-user language toolchains and version managers.
		"~/.local/share/fnm",
		"~/.local/state/fnm_multishells",
		"~/.nvm/versions",
		"~/.volta",
		"~/.asdf",
		"~/.pyenv",
		"~/.rbenv",
		"~/.cargo/bin",
		"~/.rustup/toolchains",
		"~/.bun/bin",
		"~/.deno/bin",
		"~/go/bin",
		"~/.local/bin",
	}

	switch runtime.GOOS {
	case "darwin":
		return append(common,
			// Homebrew, Apple Silicon prefix.
			"/opt/homebrew/bin",
			"/opt/homebrew/sbin",
			"/opt/homebrew/lib",
			"/opt/homebrew/opt",
			"/opt/homebrew/Cellar",
			"/opt/homebrew/share",
			"/opt/homebrew/Frameworks",
			"/opt/homebrew/include",
			// Homebrew, Intel prefix.
			"/usr/local/bin",
			"/usr/local/sbin",
			"/usr/local/lib",
			"/usr/local/opt",
			"/usr/local/Cellar",
			"/usr/local/share",
			"/usr/local/Frameworks",
			"/usr/local/include",
		)
	case "linux":
		// The same gap exists on Linux and is in fact wider there: Landlock
		// handles all filesystem rights, so a path absent from the policy is
		// not merely non-executable but unreadable. Distro-packaged tools live
		// in /usr/bin (already granted), which is why this stayed invisible —
		// it bites the moment anyone uses a version manager or the standard
		// /usr/local/go install.
		return append(common,
			"/usr/local/bin",
			"/usr/local/sbin",
			"/usr/local/lib",
			"/usr/local/go",
			"/usr/local/share",
			"/home/linuxbrew/.linuxbrew",
			"/snap/bin",
		)
	default:
		return common
	}
}
