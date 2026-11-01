package credentials

// RED scaffolding — landing/skills (train T9, #1458 first slice / §7.4).
// SensitiveNamedValues is the read-only names-and-values enumeration the save
// check consumes (#1455 first slice). Declared here only so the RED tests
// compile; the implementing worker replaces the body with the real decrypt
// loop (same as SensitiveValues, returning (name, value) pairs, OAuth
// sub-values named <NAME>.access_token / <NAME>.refresh_token), then removes
// this file's RED-SCAFFOLD markers.

// NamedValue is one decrypted credential name and its plaintext value.
type NamedValue struct {
	Name  string
	Value string
}

// SensitiveNamedValues returns every stored secret as a (name, value) pair.
// Same contract as SensitiveValues (card.go): locked store → ErrStoreLocked;
// a store file that does not exist → empty (allowed); damaged entries surface
// via DamagedEntriesError.
func (s *Store) SensitiveNamedValues() ([]NamedValue, error) {
	return nil, errRedSensitiveNamedValuesNotImplemented // RED scaffold
}

// errRedSensitiveNamedValuesNotImplemented is the RED-scaffold sentinel.
var errRedSensitiveNamedValuesNotImplemented = errRedNotImplemented{}

// errRedNotImplemented marks the T9 RED scaffold failure.
type errRedNotImplemented struct{}

func (errRedNotImplemented) Error() string {
	return "credentials: RED scaffold — SensitiveNamedValues not implemented (landing/skills T9)"
}
