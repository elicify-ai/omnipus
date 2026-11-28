// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package programs

// gitExecutableName is the file name of git on this platform.
func gitExecutableName() string { return "git" }
