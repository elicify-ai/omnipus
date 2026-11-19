package omnpkg

import (
	"archive/tar"
	"regexp"
	"strings"
)

// agentIDPattern is the agent-ID shape the agent store accepts
// (pkg/agentstore/state.go::ValidateAgentID). It is restated here because
// pkg/agentstore imports gateway sub-packages and this engine must stay a
// leaf; the two must change together.
var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9]+(?:[-_.][A-Za-z0-9]+)*$`)

// validAgentID reports whether id is a storable agent ID (1–128 characters of
// the agent-store shape).
func validAgentID(id string) bool {
	return len(id) > 0 && len(id) <= 128 && agentIDPattern.MatchString(id)
}

// UnsafeEntryPath reports whether a package entry path breaks the universal
// path checks (FR-007): empty, absolute, containing a ".." segment, a
// backslash, a NUL byte, or a drive letter. Package upload refuses such an
// entry; Backup & Restore keeps its own skip policy.
func UnsafeEntryPath(name string) bool {
	if name == "" || strings.ContainsRune(name, 0) {
		return true
	}
	if strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		return true
	}
	if hasDriveLetter(name) {
		return true
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// hasDriveLetter reports a leading Windows drive letter ("C:").
func hasDriveLetter(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	c := name[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// SafeEntryType reports whether a tar typeflag is a regular file or a folder,
// the only entry types a package may hold (FR-007). Links, devices and FIFOs
// are not safe.
func SafeEntryType(typeflag byte) bool {
	switch typeflag {
	case tar.TypeReg, tar.TypeRegA, tar.TypeDir: //nolint:staticcheck // TypeRegA is how old writers mark regular files
		return true
	default:
		return false
	}
}

// agentLayoutAllowed reports whether path is one of the three entries an
// agent element may own under omnipus/agents/<id>/ (ADR D2, FR-007).
func agentLayoutAllowed(id, path string) bool {
	switch path {
	case agentRecordPath(id), agentSoulPath(id), agentInstructionsPath(id):
		return true
	default:
		return false
	}
}
