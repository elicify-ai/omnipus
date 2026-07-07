//go:build linux && arm64

package sandbox

import "golang.org/x/sys/unix"

// nativeAuditArch is the AUDIT_ARCH_* value the kernel reports in
// seccomp_data.arch for a native arm64 (AArch64) syscall entry. Used by
// assembleBPFMode in seccomp_linux.go to reject syscalls arriving through a
// different ABI entry point (e.g. the 32-bit ARM compat path, which reports
// AUDIT_ARCH_ARM and uses a completely different syscall-number table than
// the one blockedNrs was built from).
const nativeAuditArch = unix.AUDIT_ARCH_AARCH64

func init() {
	// kexec_file_load is available on arm64; create_module is not.
	syscallNrByName["kexec_file_load"] = unix.SYS_KEXEC_FILE_LOAD
}
