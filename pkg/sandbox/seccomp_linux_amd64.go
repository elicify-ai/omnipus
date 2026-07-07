//go:build linux && amd64

package sandbox

import "golang.org/x/sys/unix"

// nativeAuditArch is the AUDIT_ARCH_* value the kernel reports in
// seccomp_data.arch for a native amd64 (x86_64) syscall entry. Used by
// assembleBPFMode in seccomp_linux.go to reject syscalls arriving through a
// different ABI entry point (e.g. the 32-bit compat/int-$0x80 path, which
// reports AUDIT_ARCH_I386 and uses a completely different syscall-number
// table than the one blockedNrs was built from).
const nativeAuditArch = unix.AUDIT_ARCH_X86_64

func init() {
	// Syscall constants available on amd64 but not on all architectures.
	syscallNrByName["create_module"] = unix.SYS_CREATE_MODULE
	syscallNrByName["kexec_file_load"] = unix.SYS_KEXEC_FILE_LOAD
}
