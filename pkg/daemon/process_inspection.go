package daemon

// ProcessInspection is a read-only native observation, not a process lease.
// An inspection error or unknown state must never be interpreted as stopped.
type ProcessInspection struct {
	PID        int
	State      string
	StartID    string
	Executable string
}

// InspectProcess observes liveness, birth and executable without modifying the
// PID convention or signalling a process. Callers must recheck at publication.
func InspectProcess(pid int) (ProcessInspection, error) {
	return inspectNativeProcess(pid)
}
