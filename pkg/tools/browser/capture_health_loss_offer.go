package browser

type captureOfferLossNotifier interface {
	SetOnIngestLostForOffer(func(uint64, uint64, uint64, string))
	InstalledIngestOffer() (uint64, uint64, uint64, string, uint64)
}

type captureLostOffer struct {
	binding, offer, generation uint64
	target                     string
}

// onIngestLostForOffer retains the cleared peer's immutable identity.
func (cs *CaptureSession) onIngestLostForOffer(bindingToken, offerID, generation uint64, targetID string) {
	cs.reportIngestLossClaim(nil, &captureLostOffer{bindingToken, offerID, generation, targetID})
}

// Caller holds cs.mu. The relay snapshot briefly takes its own state lock,
// includes receipt serial at that point, and invokes no capture callback.
func (cs *CaptureSession) captureLostOfferCurrentLocked(original captureLostOffer) (uint64, bool) {
	relay, ok := cs.relay.(captureOfferLossNotifier)
	frame := cs.frameStateLocked()
	if !ok || !cs.ingestContextBound || cs.ingestBindingCtx == nil || cs.ingestBindingCtx.Err() != nil || cs.ingestSend == nil ||
		original.binding == 0 || original.binding != cs.ingestBindingToken || original.offer == 0 || original.offer > 9007199254740991 ||
		original.generation == 0 || original.generation != frame.Generation || original.target == "" || original.target != frame.TargetID || frame.Width <= 0 || frame.Height <= 0 {
		return 0, false
	}
	binding, offer, generation, target, serial := relay.InstalledIngestOffer()
	return serial, original == (captureLostOffer{binding, offer, generation, target})
}
