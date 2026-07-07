package tools

// ExecResponse is the wire shape returned by the bash tool (pkg/tools/shell.go)
// for run/poll/read/kill actions.
type ExecResponse struct {
	SessionID string        `json:"sessionId,omitempty"`
	Status    string        `json:"status,omitempty"`
	ExitCode  int           `json:"exitCode,omitempty"`
	Output    string        `json:"output,omitempty"`
	Error     string        `json:"error,omitempty"`
	Sessions  []SessionInfo `json:"sessions,omitempty"`
}
