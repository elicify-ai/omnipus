package tools

// GetSharedSessionManager returns the single process-wide SessionManager
// instance used to track exec/bash background sessions (the same instance
// getSessionManager returns internally within pkg/tools, e.g. for ExecTool's
// own construction in shell.go).
//
// getSessionManager itself is unexported and package-private to pkg/tools, so
// callers outside this package (pkg/agent's RequestCancel cascade,
// pkg/gateway's cancel-surface wiring) cannot reach it directly. This wrapper
// exists solely to bridge that gap for ADR-036's session-cancel-cascade
// feature (FR-B10/FR-B11): CancelHooks.KillBackgroundSessions needs a way to
// reach the shared SessionManager and call KillAllForSession on it.
func GetSharedSessionManager() *SessionManager {
	return getSessionManager()
}
