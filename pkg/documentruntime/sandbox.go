package documentruntime

import (
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// ApplySandboxAccess adds the document runtime to an already-derived turn
// policy. Worker access is read+execute; Admin additionally receives write.
func ApplySandboxAccess(policy sandbox.SandboxPolicy, layout Layout, admin bool) sandbox.SandboxPolicy {
	out := policy
	out.FilesystemRules = append([]sandbox.PathRule(nil), policy.FilesystemRules...)
	access := sandbox.AccessRead | sandbox.AccessExecute
	if admin {
		access |= sandbox.AccessWrite
	} else {
		for i := range out.FilesystemRules {
			if pathsOverlap(out.FilesystemRules[i].Path, layout.Prefix) {
				out.FilesystemRules[i].Access &^= sandbox.AccessWrite
			}
		}
	}
	out.FilesystemRules = append(out.FilesystemRules, sandbox.PathRule{Path: filepath.Clean(layout.Prefix), Access: access})
	out.FilesystemRules = append(out.FilesystemRules, sandbox.PathRule{Path: filepath.Clean(layout.Cache), Access: sandbox.AccessRead | sandbox.AccessWrite})
	return out
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
