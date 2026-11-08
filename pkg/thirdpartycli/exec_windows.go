//go:build windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package thirdpartycli

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// admittedExtensions is the T11 spec §3.2 set (decision C1: .exe, .cmd and
// .bat only), compared case-insensitively.
var admittedExtensions = []string{".exe", ".cmd", ".bat"}

// shortNameComponent matches an 8.3 short-name component: "~" followed by digits.
var shortNameComponent = regexp.MustCompile(`~[0-9]+`)

// canExecute is the Windows rule: Windows keeps no execute bit, so a file is
// executable when its extension is admitted.
func canExecute(path string) error {
	if extensionAdmitted(path) {
		return nil
	}
	return errors.New("extension not admitted")
}

func extensionAdmitted(path string) bool {
	ext := filepath.Ext(path)
	for _, a := range admittedExtensions {
		if strings.EqualFold(ext, a) {
			return true
		}
	}
	return false
}

// isAbsolutePath is the Windows rule: a drive path or a UNC path.
func isAbsolutePath(p string) bool { return filepath.IsAbs(p) }

// windowsNameCheck applies the Windows-only rows of r17 §3.5 to one path
// element: no alternate data stream (":" after the drive), no 8.3 short
// component, an admitted extension.
func windowsNameCheck(p string) string {
	rest := strings.TrimPrefix(p, filepath.VolumeName(p))
	if strings.Contains(rest, ":") {
		return CheckWindowsStream
	}
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		if shortNameComponent.MatchString(part) {
			return CheckWindowsShortName
		}
	}
	if !extensionAdmitted(p) {
		return CheckWindowsExtension
	}
	return ""
}
