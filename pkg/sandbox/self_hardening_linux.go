//go:build linux

package sandbox

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// HardenGatewaySelf applies process-level self-hardening to the gateway
// itself. Today this means PR_SET_DUMPABLE=0, which closes C6 from the
// insider-pentest report (a same-uid child can read /proc/<gateway>/environ
// — and therefore OMNIPUS_MASTER_KEY/OMNIPUS_BEARER_TOKEN — by default on
// Linux because /proc/<pid>/{environ,mem,maps,...} are owned by the
// process's real UID).
//
// Setting PR_SET_DUMPABLE=0 makes those files root-owned and unreadable
// even by other processes running as the same UID. The Linux kernel rule
// is documented in proc(5):
//
//	"Permission to access this file is governed by a ptrace access mode
//	 PTRACE_MODE_READ_FSCREDS check; see ptrace(2). When the dumpable
//	 attribute is 0, the files are owned by root and not readable."
//
// Side effects:
//   - The gateway can no longer be ptrace(2)-attached except by root.
//     We accept this — production gateways should not be debugged in-place.
//   - core(5) dumps are suppressed when DUMPABLE=0. This is a security win
//     in production: a crashing gateway no longer leaves the master key in
//     a /var/lib/systemd/coredump/* file readable by anyone in the systemd-
//     coredump group.
//
// Idempotent: prctl(PR_SET_DUMPABLE, 0) is safe to call repeatedly.
//
// Closes: C6 (TestChildCannotReadGatewayProcEnviron) — v0.2 #155.
func HardenGatewaySelf() error {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl(PR_SET_DUMPABLE, 0): %w", err)
	}
	return nil
}
