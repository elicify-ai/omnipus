package browser

// IsCurrentIngestOffer checks the immutable identity retained by an offer
// handler before that handler publishes an answer or error response.
func (cs *CaptureSession) IsCurrentIngestOffer(epoch, offerID, generation uint64, targetID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return offerID != 0 && offerID == cs.ingestOfferID && cs.matchesIngestFrameLocked(epoch, generation, targetID)
}
