package channels

// LocalChannelCache is implemented by an adapter that can tell, from its
// local session cache only (no network call), whether an id is a channel a
// started message can be posted to (channels-spec-r8 T-5 / FR-006; Discord).
type LocalChannelCache interface {
	CachedPostableChannel(id string) bool
}

// CachedPostableChannel reports whether the running adapter of instanceID
// holds bareID in its local cache as a postable channel. false for an
// adapter without a local cache, an unknown instance or a cache miss. Never
// makes a network call.
func (m *Manager) CachedPostableChannel(instanceID, bareID string) bool {
	ch, ok := m.GetChannel(instanceID)
	if !ok {
		return false
	}
	cache, ok := ch.(LocalChannelCache)
	return ok && cache.CachedPostableChannel(bareID)
}
