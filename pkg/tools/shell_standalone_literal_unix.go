//go:build !windows

package tools

import (
	"errors"

	"golang.org/x/sys/unix"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// rootIsWritable is spec §3.1 condition 5 on Unix: the effective-access check
// (faccessat with AT_EACCESS, so the effective uid/gid decide, not the real
// ids) for write access to "/". EACCES (not allowed) and EROFS (a read-only
// root, such as the macOS sealed system volume) mean the root is not writable,
// so the stand-alone literal rule may apply. Any other error answers
// "writable", which turns the rule off and keeps today's refusal.
//
// The unexpected-error case is logged with its cause (T11 fix round 1, SF-5)
// so a disabled rule is diagnosable rather than looking like a broken fix.
func rootIsWritable() bool {
	err := unix.Faccessat(unix.AT_FDCWD, "/", unix.W_OK, unix.AT_EACCESS)
	switch {
	case err == nil:
		return true
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EROFS):
		return false
	default:
		logger.WarnCF("tools", "bash guard: could not check whether the filesystem root is writable; the quoted-literal rule is off for this command",
			map[string]any{"error": err.Error()})
		return true
	}
}
