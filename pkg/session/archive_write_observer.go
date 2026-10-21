package session

// observeWriteStep is an instance-local test observation seam. Production stores
// leave onWriteStep nil. A test may panic here to model a process stopping after
// a completed session-owned operation; no writer ordering or I/O is replaced.
func (s *ArchiveDayStore) observeWriteStep(step string) {
	if s.onWriteStep != nil {
		s.onWriteStep(step)
	}
}
