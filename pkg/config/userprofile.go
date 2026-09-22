// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"errors"
	"os"
	"path/filepath"
)

// USER.md — the user's own profile. ONE file, global to the installation.
//
// It describes the person, not an agent and not a workspace, so every agent
// reads the same copy no matter which agent it is or which workspace it is
// acting in. That is the whole point of the file, and it is the property this
// package exists to make unambiguous.
//
// The three bootstrap files are easy to confuse, so for the record:
//
//	agents/<id>/SOUL.md         an agent's identity        — per agent
//	agents/<id>/AGENT.md        an agent's own prompt      — per agent
//	workspaces/<id>/AGENT.md    Project Instructions       — per workspace
//	USER.md                     the user                   — GLOBAL, here
//
// Until this file existed, USER.md was resolved from the *agent's* directory,
// so every agent read its own private copy while the Settings UI wrote exactly
// one — and the control described in the UI as "shared context available to all
// agents" reached no agent at all except the legacy `main` singleton.
const userProfileFileName = "USER.md"

// UserProfilePath returns the one global USER.md path. This is the path that is
// written, and the path that should be shown to a user who asks where their
// profile lives.
func UserProfilePath() string {
	return filepath.Join(OmnipusHomeDir(), userProfileFileName)
}

// ReadUserProfile returns the user's profile content and the path it came from.
// A missing profile is not an error: it returns ("", "", nil), because an
// installation that has never opened Settings has no profile and that is normal.
//
// Every reader — the agent context builder and the REST layer alike — must go
// through this function. Two callers resolving USER.md independently is exactly
// how the original defect happened.
func ReadUserProfile() (path string, content string, err error) {
	global := UserProfilePath()
	data, readErr := os.ReadFile(global)
	switch {
	case readErr == nil:
		return global, string(data), nil
	case errors.Is(readErr, os.ErrNotExist):
		return "", "", nil
	default:
		return "", "", readErr
	}
}
