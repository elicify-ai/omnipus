package gateway

// SetInterruptionReason carries an engine-owned ended outcome to finalization.
func (s *wsStreamer) SetInterruptionReason(reason string) {
	s.statsMu.Lock()
	s.statsInterruptionReason = reason
	s.statsMu.Unlock()
}
