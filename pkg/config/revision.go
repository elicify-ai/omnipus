package config

// FR-088 revision tokens of install-wide shared resources (omnipus-system-agent
// §6.6), shared by the agent tools (pkg/sysagent/tools) and REST (pkg/gateway)
// so both entry points compute exactly the same token (channels-spec-r8
// FR-020: moved here from pkg/sysagent/tools/shared_revision.go).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// RevisionAbsent is the token of an entry that does not exist (FR-088).
const RevisionAbsent = "absent"

// RevisionHash returns a short, stable token for v: the first 16 hex digits
// of the SHA-256 of its JSON encoding. v must hold only non-secret data.
func RevisionHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Only plain strings, bools and slices of them reach here; a marshal
		// failure is a programming error. Fold it into the token so the write
		// is refused (no expected token can match) instead of guarded by "".
		b = []byte("unencodable:" + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// ChannelConfigRevision is the FR-088 token of channel instance id: a hash
// over the instance id and every non-secret field of its configuration —
// platform type, enabled state, binding (workspace and main agent), allow
// list, group and mention settings, the outbound setting and every
// credential reference name. The two fields that can hold a secret value
// inline (webhook_url, service_account_json) are excluded; their *_ref names
// are included. RevisionAbsent when not configured. It is deliberately NOT
// the adapter view: an outbound-only change moves it (channels-spec-r8,
// "Persisted configuration").
func ChannelConfigRevision(cfg *Config, id string) string {
	if cfg == nil {
		return RevisionAbsent
	}
	ch, ok := cfg.Channels[id]
	if !ok {
		return RevisionAbsent
	}
	ch.WebhookURL = SecureString{}
	ch.ServiceAccountJSON = SecureString{}
	return RevisionHash(struct {
		ID     string                `json:"id"`
		Config ChannelInstanceConfig `json:"config"`
	}{ID: id, Config: ch})
}
