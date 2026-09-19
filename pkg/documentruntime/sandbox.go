package documentruntime

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// ApplySandboxAccess adds the document runtime to an already-derived turn
// policy. Every agent gets read+execute on the managed prefix and write on
// the per-worker cache only — write access that any base rule grants over the
// prefix is stripped, so installation happens exclusively through the
// environment_setup tool, never through ordinary turns.
func ApplySandboxAccess(policy sandbox.SandboxPolicy, layout Layout, socketDir string) (sandbox.SandboxPolicy, error) {
	out := policy
	out.FilesystemRules = append([]sandbox.PathRule(nil), policy.FilesystemRules...)
	access := sandbox.AccessRead | sandbox.AccessExecute
	for i := range out.FilesystemRules {
		if pathsOverlap(out.FilesystemRules[i].Path, layout.Prefix) {
			out.FilesystemRules[i].Access &^= sandbox.AccessWrite
		}
	}
	out.FilesystemRules = append(out.FilesystemRules, sandbox.PathRule{Path: filepath.Clean(layout.Prefix), Access: access})
	out.FilesystemRules = append(out.FilesystemRules, sandbox.PathRule{Path: filepath.Clean(layout.Cache), Access: sandbox.AccessRead | sandbox.AccessWrite})
	socketDir = filepath.Clean(socketDir)
	if !pathHasWriteAccess(out.FilesystemRules, socketDir) {
		return sandbox.SandboxPolicy{}, fmt.Errorf("document IPC directory is not covered by a writable sandbox path")
	}
	out.UnixSocketRules = append(append([]sandbox.UnixSocketRule(nil), policy.UnixSocketRules...), sandbox.UnixSocketRule{Path: socketDir, Bind: true, Connect: true})
	return out, nil
}

func pathHasWriteAccess(rules []sandbox.PathRule, path string) bool {
	for _, rule := range rules {
		if !filepath.IsAbs(path) || !filepath.IsAbs(rule.Path) || rule.Access&sandbox.AccessWrite == 0 {
			continue
		}
		rel, err := filepath.Rel(rule.Path, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func pathsOverlap(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if strings.EqualFold(a, b) {
		return true
	}
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
