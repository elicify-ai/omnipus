// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package childenv

import (
	"regexp"
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
	"TMPDIR": {}, "OSL_SOCKET_PATH": {}, "LANG": {}, "LC_ALL": {}, "OMNIPUS_DOCUMENT_SKILLS": {},
}

// setupLayerNames are the names the environment_setup layer sets
// (pkg/tools/environment_setup_runner.go::setupEnv), uppercased.
var setupLayerNames = map[string]struct{}{
	"OMNIPUS_ENV_PREFIX": {}, "OMNIPUS_ENV_CACHE": {}, "OMNIPUS_ENV_TMP": {},
	"TMPDIR": {}, "NPM_CONFIG_CACHE": {}, "PIP_CACHE_DIR": {},
}

// protectedPrivatePrefixes are whole families no private variable may use.
var protectedPrivatePrefixes = []string{"OMNIPUS_", "LD_", "DYLD_"}

var privateNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// CheckPrivateVariableName validates an agent-private variable name against
// the grammar ^[A-Z][A-Z0-9_]{0,63}$ and the protected set. The returned
// error message names the rule that refused the name (US-7 AS-5).
func CheckPrivateVariableName(name string) error {
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
	if _, ok := setupLayerNames[name]; ok {
		return privateNameError(name + " is a protected name: environment_setup sets it")
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
