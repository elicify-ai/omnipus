package session

// scanSavedInputIntegrity must see every identity/consumption record before it
// can certify that a report is absent or unconsumed. Recall's tolerant scanners
// are deliberately unchanged: a skipped corrupt row is useful for recall but
// cannot establish the negative fact that makes a saved input pending again.
func (s *ArchiveDayStore) scanSavedInputIntegrity(fn func(ArchiveAddress, ArchiveRecord) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanAllLocked(fn, true)
}
