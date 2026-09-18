// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
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

// legacyUserProfileDirName is the pre-fix location: the default agent's
// workspace directory, which is named "workspace". It is read ONLY as a
// fallback so that content written before the fix is not silently orphaned.
// Nothing writes here any more.
//
// This covers the shipped default. An installation that customised
// `agents.defaults.workspace` and has USER.md content there must move the file
// by hand; the fallback deliberately does not guess at a configured path,
// because guessing wrong means reading an unrelated file as the user's profile.
const legacyUserProfileDirName = "workspace"

// UserProfilePath returns the one global USER.md path. This is the path that is
// written, and the path that should be shown to a user who asks where their
// profile lives.
func UserProfilePath() string {
	return filepath.Join(OmnipusHomeDir(), userProfileFileName)
}

// legacyUserProfilePath returns the pre-fix location. Read-only.
func legacyUserProfilePath() string {
	return filepath.Join(OmnipusHomeDir(), legacyUserProfileDirName, userProfileFileName)
}

// ReadUserProfile returns the user's profile content and the path it came from.
//
// It prefers the global path and falls back to the legacy per-workspace
// location when the global file does not exist. A missing profile is not an
// error: it returns ("", "", nil), because an installation that has never
// opened Settings has no profile and that is normal.
//
// Every reader — the agent context builder and the REST layer alike — must go
// through this function. Two callers resolving USER.md independently is exactly
// how the original defect happened.
func ReadUserProfile() (path string, content string, err error) {
	global := UserProfilePath()
	switch data, readErr := os.ReadFile(global); {
	case readErr == nil:
		return global, string(data), nil
	case !os.IsNotExist(readErr):
		return "", "", readErr
	}

	legacy := legacyUserProfilePath()
	switch data, readErr := os.ReadFile(legacy); {
	case readErr == nil:
		return legacy, string(data), nil
	case !os.IsNotExist(readErr):
		return "", "", readErr
	}

	return "", "", nil
}
