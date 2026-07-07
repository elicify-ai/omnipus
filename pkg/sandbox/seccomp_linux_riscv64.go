//go:build linux && riscv64

package sandbox

import "golang.org/x/sys/unix"

// nativeAuditArch is the AUDIT_ARCH_* value the kernel reports in
// seccomp_data.arch for a native riscv64 syscall entry. Used by
// assembleBPFMode in seccomp_linux.go to reject syscalls arriving through a
// different ABI entry point than the one blockedNrs was built from.
//
// This build (per the Makefile's build-all target) has no additional
// architecture-only syscalls to register beyond the base syscallNrByName
// map, so there is no init() here, unlike seccomp_linux_amd64.go /
// seccomp_linux_arm64.go.
const nativeAuditArch = unix.AUDIT_ARCH_RISCV64
