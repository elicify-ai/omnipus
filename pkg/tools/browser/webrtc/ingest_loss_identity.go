package webrtc

// SetOnIngestLostForOffer replaces legacy loss delivery with the immutable
// installed identity. Callbacks run outside relay locks, before writer drain.
func (s *Session) SetOnIngestLostForOffer(cb func(uint64, uint64, uint64, string)) {
	s.mu.Lock()
	s.onIngestLostForOffer = cb
	s.mu.Unlock()
}

// InstalledIngestOffer returns the last installed identity, retained after
// loss, and the accepted-packet serial sampled under the same session lock.
// This baseline is ingress evidence, never a successful-forward boundary.
func (s *Session) InstalledIngestOffer() (bindingToken, offerID, generation uint64, targetID string, receiptSerial uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt := s.videoForward.latestReceipt()
	return s.ingestInstalledBindingToken, s.ingestInstalledOfferID,
		s.ingestInstalledGeneration, s.ingestInstalledTargetID, receipt.Serial
}
