package gateway

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// The exact synthetic algorithm/namespace is pinned in the shared golden
// migration map. It preserves equality and valid wire shape, not raw source
// fingerprints or generation IDs. No arbitrary field/extension is dropped.
func (n *fixtureNormalizer) pagingString(key, raw string) (string, bool) {
	switch key {
	case "position", "first_position", "last_position", "history_position", "canonical_position":
		if value, ok := n.positions[raw]; ok {
			return value, true
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("catchup-fixture-position-%d", len(n.positions)+1)))
		value := "hp1_" + base64.RawURLEncoding.EncodeToString(digest[:])
		n.positions[raw] = value
		return value, true
	case "retention_generation":
		if value, ok := n.generations[raw]; ok {
			return value, true
		}
		value := fmt.Sprintf("fixture-generation-%d", len(n.generations)+1)
		n.generations[raw] = value
		return value, true
	default:
		return raw, false
	}
}
