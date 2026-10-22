package channels

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Note: hiddenValues and updateKeys were removed. Previously they re-injected *Ref fields
// into hash/config maps because the old SecureString type returned "[NOT_HERE]" from
// MarshalJSON, silently erasing secret refs on JSON round-trips. Now that all secret
// fields are plain strings (*Ref), they survive JSON marshal/unmarshal without any
// special handling.

// toChannelHashes produces a map from instanceID → config-hash for all enabled
// channel instances. Both m.channels and m.channelHashes are keyed by instanceID
// (FR-015 / WS-B), so compareChannels and Reload operate on a consistent key
// space. Legacy single-instance names ("telegram", "whatsapp") behave
// identically to before because their instanceID IS their type name.
func toChannelHashes(cfg *config.Config) map[string]string {
	result := make(map[string]string)
	for instanceID, inst := range cfg.Channels {
		if !inst.Enabled {
			continue
		}
		// Canonicalize supported instance fields exactly like the loader. The
		// manager's typed/factory callers retain their existing enabled set;
		// filtering unsupported file sections remains the loader's job.
		canonical, _ := config.CanonicalChannelInstance(instanceID, inst)
		valueBytes, err := json.Marshal(canonical)
		if err != nil {
			logger.WarnCF("channels", "toChannelHashes: failed to marshal channel instance",
				map[string]any{"instance_id": instanceID, "error": err.Error()})
			valueBytes = []byte("{}")
		}
		hash := md5.Sum(valueBytes)
		// Key by instanceID so m.channels[instanceID] and m.channelHashes[instanceID]
		// refer to the same slot. Reload's "added/removed" diff is over instanceIDs.
		result[instanceID] = hex.EncodeToString(hash[:])
	}
	return result
}

func compareChannels(old, news map[string]string) (added, removed []string) {
	for key, newHash := range news {
		if oldHash, ok := old[key]; ok {
			if newHash != oldHash {
				removed = append(removed, key)
				added = append(added, key)
			}
		} else {
			added = append(added, key)
		}
	}
	for key := range old {
		if _, ok := news[key]; !ok {
			removed = append(removed, key)
		}
	}
	return added, removed
}

// credentialSignature summarizes which of one enabled instance's configured
// credential references were present (non-empty) in the supplied bundle —
// never their values (#1277). Sorted so the signature is deterministic; a
// signature change between boot/reloads means the instance's secret set
// changed (deleted, rotated away, unreadable, or restored) even though its
// config bytes did not, and Reload must re-initialize that one instance.
func credentialSignature(inst config.ChannelInstanceConfig, secrets credentials.SecretBundle) string {
	refs := inst.CredentialRefs()
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			continue
		}
		state := "absent"
		if secrets.GetString(ref) != "" {
			state = "present"
		}
		parts = append(parts, ref+"="+state)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// credentialSignatures maps every enabled instanceID to its
// credentialSignature. Mirrors toChannelHashes' key space (enabled instances
// only) so Reload can diff the two maps in lockstep.
func credentialSignatures(cfg *config.Config, secrets credentials.SecretBundle) map[string]string {
	result := make(map[string]string)
	for instanceID, inst := range cfg.Channels {
		if !inst.Enabled {
			continue
		}
		result[instanceID] = credentialSignature(inst, secrets)
	}
	return result
}

// toChannelConfig returns the subset of cfg.Channels whose instanceIDs appear
// in list. The returned map is passed to initChannels on hot-reload. Both
// m.channels and m.channelHashes are now keyed by instanceID (FR-015 / WS-B),
// so list is a slice of instanceIDs (from compareChannels's "added" output).
func toChannelConfig(cfg *config.Config, list []string) (map[string]config.ChannelInstanceConfig, error) {
	wanted := make(map[string]struct{}, len(list))
	for _, s := range list {
		wanted[s] = struct{}{}
	}
	result := make(map[string]config.ChannelInstanceConfig, len(list))
	for instanceID, inst := range cfg.Channels {
		if !inst.Enabled {
			continue
		}
		if _, ok := wanted[instanceID]; !ok {
			continue
		}
		result[instanceID] = inst
	}
	return result, nil
}
