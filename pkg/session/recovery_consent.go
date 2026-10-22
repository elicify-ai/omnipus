package session

// RecoveryConsent binds a later explicit continuation to the exact unresolved
// occurrence set and the execution it admits. It grants no filesystem rights.
// not-wire-format: private lifecycle persistence only.
type RecoveryConsent struct {
	Generation     int                `json:"generation"`
	OccurrenceKeys []string           `json:"occurrence_keys"`
	ExecutionID    *ExecutionIdentity `json:"execution_id,omitempty"`
}
