//go:build !windows && !darwin

package clitrust

import "os"

// isTrustedAdminFolder: only macOS has a trusted group-writable folder
// (/Applications, see check_admin_darwin.go). Elsewhere none is trusted.
func isTrustedAdminFolder(string, os.FileInfo) bool { return false }
