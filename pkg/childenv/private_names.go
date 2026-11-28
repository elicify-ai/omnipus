// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package childenv

import (
	"regexp"
	"runtime"
	"strings"
)

// The protected set for agent-private variable names (FR-076, R2-MAJ-007,
// dataset D6). A private variable is appended AFTER every managed layer of a
// child environment, so it must never be able to replace a name one of those
// layers sets. The set is the child allowlist above (keys and prefixes), the
// document-runtime layer, the environment_setup layer, and the OMNIPUS_,
// LD_ and DYLD_ families. Test 91 (the drift test) runs the real composers
// and asserts every key they emit is protected here.

// documentRuntimeNames are the names the document-runtime layer sets or
// strips (pkg/documentruntime/runtime.go::ChildEnvironment).
var documentRuntimeNames = map[string]struct{}{
	"PATH": {}, "PYTHONPATH": {}, "NODE_PATH": {}, "XDG_CACHE_HOME": {}, "HOME": {},
	"TMPDIR": {}, "OSL_SOCKET_PATH": {}, "LANG": {}, "LC_ALL": {},
}

// setupLayerNames are the names the environment_setup layer sets
// (pkg/tools/environment_setup_runner.go::setupEnv), uppercased.
var setupLayerNames = map[string]struct{}{
	"OMNIPUS_ENV_PREFIX": {}, "OMNIPUS_ENV_CACHE": {}, "OMNIPUS_ENV_TMP": {},
	"TMPDIR": {}, "NPM_CONFIG_CACHE": {}, "PIP_CACHE_DIR": {},
}

// gitLoginNames are the git environment-configuration names Omnipus sets for
// connected git logins (connected-programs spec FR-013): a private variable or
// a program token variable must never set them.
var gitLoginNames = map[string]struct{}{"GIT_CONFIG_COUNT": {}, "GIT_CONFIG_PARAMETERS": {}}

// gitLoginPrefixes are the indexed git environment-configuration families.
var gitLoginPrefixes = []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"}

// protectedPrivatePrefixes are whole families no private variable may use.
var protectedPrivatePrefixes = []string{"OMNIPUS_", "LD_", "DYLD_"}

var privateNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// CheckPrivateVariableName validates an agent-private variable name against
// the grammar ^[A-Z][A-Z0-9_]{0,63}$ and the protected set. The returned
// error message names the rule that refused the name (US-7 AS-5).
func CheckPrivateVariableName(name string) error {
	return checkPrivateVariableNameFor(name, runtime.GOOS == "windows")
}

// windowsSystemNames are the Windows system variables every Windows child's
// base environment carries (ruling WU19-win 2). None carries a secret; they
// play the role HOME and TMPDIR play on Unix. Windows matches them
// case-insensitively.
var windowsSystemNames = []string{
	"SystemRoot", "SystemDrive", "windir", "ComSpec", "PATHEXT", "TEMP", "TMP", "USERPROFILE",
}

// isWindowsSystemName reports whether name is one of windowsSystemNames,
// ignoring case.
func isWindowsSystemName(name string) bool {
	for _, key := range windowsSystemNames {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// checkPrivateVariableNameFor is CheckPrivateVariableName with the platform
// made explicit: on Windows the Windows system variables are protected too
// (#1960 S1 security round 2, F-win).
func checkPrivateVariableNameFor(name string, windows bool) error {
	if !privateNamePattern.MatchString(name) {
		if upper := strings.ToUpper(name); upper != name && privateNamePattern.MatchString(upper) {
			return privateNameError(name + " is refused: private variable names are uppercase only (A-Z, 0-9 and _)")
		}
		return privateNameError(name + " is refused by the name grammar: 1 to 64 characters, starting with A-Z, then A-Z, 0-9 or _")
	}
	if _, ok := documentRuntimeNames[name]; ok {
		return privateNameError(name + " is a protected name: the document runtime environment sets it")
	}
	if _, ok := allowedEnvKeys[name]; ok {
		return privateNameError(name + " is a protected name: it is part of every command's base environment")
	}
	if windows && isWindowsSystemName(name) {
		return privateNameError(name + " is a protected name: it is part of every command's base environment on Windows")
	}
	if _, ok := setupLayerNames[name]; ok {
		return privateNameError(name + " is a protected name: environment_setup sets it")
	}
	if _, ok := gitLoginNames[name]; ok {
		return privateNameError(name + " is a protected name: Omnipus sets it for connected git logins")
	}
	for _, prefix := range gitLoginPrefixes {
		if strings.HasPrefix(name, prefix) {
			return privateNameError(name + " is a protected name: Omnipus sets it for connected git logins")
		}
	}
	for _, prefix := range append(append([]string(nil), allowedEnvPrefixes...), protectedPrivatePrefixes...) {
		if strings.HasPrefix(name, prefix) {
			return privateNameError(name + " is a protected name: the " + prefix + " prefix is reserved")
		}
	}
	return nil
}

type privateNameError string

func (e privateNameError) Error() string { return string(e) }
