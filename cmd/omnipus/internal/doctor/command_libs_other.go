//go:build !linux && !darwin

// Non-Linux, non-darwin stub for WARN-BROWSER-005 (ADR-052 SEC-ADR052-007).
// Phase 1 only ships Linux packages; the macOS .app bundle ships its
// dylibs (Phase 3, command_libs_darwin.go), and Windows Chrome ships
// its own runtime (Phase 4). On any non-Linux, non-darwin host the
// doctor check is a clean no-op - same posture as doctor's existing
// per-OS gating.
package doctor

// missingChromeLibsELF is a no-op on non-Linux, non-darwin hosts.
// Phase 1 intentionally surfaces only Linux-bundled-Chrome findings;
// the macOS implementation lives in command_libs_darwin.go and the
// Windows implementation is out of scope per ADR-052 section 5.
// The CLI-parse-time OS gate in checkBrowserPackageChrome
// (runtime.GOOS == "linux") already prevents this from being called
// on non-Linux, but the build-tagged stub keeps the linux-only ELF
// parser out of darwin/windows binaries.
func missingChromeLibsELF(binPath string) ([]string, error) {
	_ = binPath
	return nil, nil
}

// missingChromeLibsMachO is a no-op on non-Linux, non-darwin hosts.
// The real implementation lives in command_libs_darwin.go; this stub
// exists so that command.go (which has no build tag and references
// missingChromeLibsMachO in the "darwin" switch case) compiles on
// Windows and other non-darwin platforms. The runtime GOOS gate
// prevents this from being called outside darwin.
func missingChromeLibsMachO(binPath string) ([]string, error) {
	_ = binPath
	return nil, nil
}
