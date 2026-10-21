package session

// SetRetentionNoticeDispatcher binds the running gateway's model/UI dispatcher.
// Manual and scheduled sweeps invoke it after all shard/backend/archive locks
// are released, including partial-failure outcomes. Success does not acknowledge
// an outbox item: only durable model admission does that.
func (us *UnifiedStore) SetRetentionNoticeDispatcher(dispatch func() error) {
	if dispatch == nil {
		us.retentionNoticeDispatch.Store(nil)
		return
	}
	us.retentionNoticeDispatch.Store(&dispatch)
}
