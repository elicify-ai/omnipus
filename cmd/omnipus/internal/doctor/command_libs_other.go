//go:build !linux

// Non-Linux stub for WARN-BROWSER-005 (ADR-052 SEC-ADR052-007). Phase 1
// only ships Linux packages; the macOS .app bundle ships its dylibs
// (Phase 3), and Windows Chrome ships its own runtime (Phase 4). On any
// non-Linux host the doctor check is a clean no-op — same posture as
// doctor.ClassifyVideoCapabilityWithExec's existing per-OS gating.
package doctor

// missingChromeLibsELF is a no-op on non-Linux. Phase 1 intentionally
// surfaces only Linux-bundled-Chrome findings; the macOS / Windows
// implementations are out of scope per ADR §5. The CLI-parse-time OS
// gate in checkBrowserPackageChrome (runtime.GOOS == "linux") already
// prevents this from being called on non-Linux, but the build-tagged
// stub keeps the linux-only ELF parser out of darwin/windows binaries.
func missingChromeLibsELF(binPath string) ([]string, error) {
	_ = binPath
	return nil, nil
}
