//go:build !linux

package sandbox

// HardenGatewaySelf is a no-op on non-Linux platforms. The PR_SET_DUMPABLE
// path is Linux-specific. macOS and Windows have separate process-isolation
// mechanisms (codesigning, AppContainer) which Omnipus does not currently
// orchestrate; these platforms fall back to the same-user trust boundary.
func HardenGatewaySelf() error {
	return nil
}
