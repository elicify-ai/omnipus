// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"github.com/elicify-ai/omnipus/pkg/config"
)

// SecretRef is the name of a credential stored in credentials.json.
// It is safe to log and to compare.
type SecretRef string

// SecretBundle holds resolved plaintext secrets keyed by ref name.
// Values are plaintext; the map should be scoped to the gateway process
// lifetime and never written to disk, logs, or env.
type SecretBundle map[SecretRef]string

// Get returns the plaintext for ref, or ("", false) if the ref is not in
// the bundle. Missing refs are not an error — caller decides whether
// empty is fatal (enabled channel) or acceptable (disabled channel).
func (b SecretBundle) Get(ref SecretRef) (string, bool) {
	v, ok := b[ref]
	return v, ok
}

// GetString is a convenience that returns the plaintext or "" if missing.
// Use Get() if you need to distinguish "ref set to empty" from "ref not in bundle".
func (b SecretBundle) GetString(ref string) string {
	v := b[SecretRef(ref)]
	return v
}

// ResolveBundle walks every *Ref field on cfg (providers + channels + tools)
// and returns a SecretBundle with all resolved plaintexts. Does NOT call os.Setenv.
// ErrNotFound for a configured ref is collected as an error but does not stop
// resolution of other refs.
func ResolveBundle(cfg *config.Config, store *Store) (SecretBundle, []error) {
	plaintexts, errs := ResolveAll(cfg, store)
	bundle := make(SecretBundle, len(plaintexts))
	for k, v := range plaintexts {
		bundle[SecretRef(k)] = v
	}
	return bundle, errs
}
