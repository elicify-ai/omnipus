package config

// Channels outbound setting (channels-spec-r8, "Persisted configuration",
// T-8, FR-014, FR-015, FR-018): the persisted type, the adapter view every
// connection-lifecycle comparison uses, and the outbound guard every channel
// save step runs.
//
// ChannelInstanceConfig.Outbound carries ChannelOutboundConfig. It landed
// together with the set_config deny entry channels.*.outbound (FR-024):
// set_config resolves struct fields by reflection, so without that entry the
// field alone would have made the setting agent-writable.

import (
	"errors"
	"slices"
	"sort"
	"strings"
)

// ChannelOutboundConfig is the persisted outbound setting of one channel
// instance, config.json key channels.<id>.outbound (FR-010). Absent means
// reply_only. Policy is "reply_only" or "agent_may_start"; Targets are
// canonical "platform:id" values.
type ChannelOutboundConfig struct {
	Policy  string              `json:"policy"`
	Targets FlexibleStringSlice `json:"targets"`
}

// Clone returns a deep copy (nil for nil).
func (o *ChannelOutboundConfig) Clone() *ChannelOutboundConfig {
	if o == nil {
		return nil
	}
	return &ChannelOutboundConfig{Policy: o.Policy, Targets: slices.Clone(o.Targets)}
}

// Equal reports whether two outbound settings carry the same policy and the
// same targets in the same order (nil equals only nil).
func (o *ChannelOutboundConfig) Equal(other *ChannelOutboundConfig) bool {
	if o == nil || other == nil {
		return o == other
	}
	return o.Policy == other.Policy && slices.Equal(o.Targets, other.Targets)
}

// ErrOutboundProtected is GuardOutbound's refusal (T-8 row 5): a save that is
// not the dedicated outbound writer tried to change a stored outbound
// setting. REST maps it to 403 "channels.<id>.outbound can only be changed
// with PUT /api/v1/channels/{id}/outbound".
var ErrOutboundProtected = errors.New("outbound_protected")

// OutboundProtectedError is the concrete ErrOutboundProtected refusal: it
// names the instance whose outbound setting the save tried to change.
type OutboundProtectedError struct{ InstanceID string }

func (e *OutboundProtectedError) Error() string {
	return "outbound_protected: channels." + e.InstanceID + ".outbound can only be changed with PUT /api/v1/channels/{id}/outbound"
}

// Is makes errors.Is(err, ErrOutboundProtected) true.
func (e *OutboundProtectedError) Is(target error) bool { return target == ErrOutboundProtected }

// ChannelAdapterView returns inst with its outbound setting cleared (FR-015).
// Every connection-lifecycle comparison uses it (toChannelHashes,
// channelConfigFingerprint, ChannelConfigChanges, MergeUnchangedChannelOrders)
// so a list-only change restarts nothing. The FR-088 revision does not use it.
func ChannelAdapterView(inst ChannelInstanceConfig) ChannelInstanceConfig {
	inst.Outbound = nil
	return inst
}

// boundPair is the (workspace, agent) pair an instance is bound to; the zero
// pair means unbound (T-8 row 1 compares it).
func boundPair(inst ChannelInstanceConfig) [2]string {
	if !inst.IsWorkspaceBound() {
		return [2]string{}
	}
	return [2]string{strings.TrimSpace(inst.WorkspaceID), strings.TrimSpace(inst.Identity.ID)}
}

// GuardOutbound is the outbound guard of T-8 (FR-014, FR-018). It runs on the
// decoded candidate before the file is written, at every channel save step,
// and adjusts candidate in place per instance id of before ∪ candidate:
// removed or rebound → outbound cleared; dedicated → kept; not dedicated and
// absent → restored from before; equal → kept; different → refused with
// ErrOutboundProtected (nothing written). changed lists the instance ids
// whose candidate outbound the guard cleared or restored, sorted. before is
// the saved file's configuration (may be nil for a first write).
//
// An instance that is new in candidate (absent from before) has no stored
// pair, so it is treated like a changed pair (row 1): a carried outbound
// setting is cleared — only the dedicated writer, which works on an existing
// instance, can introduce one.
func GuardOutbound(before, candidate *Config, dedicated bool) (changed []string, err error) {
	if candidate == nil {
		return nil, nil
	}
	var stored map[string]ChannelInstanceConfig
	if before != nil {
		stored = before.Channels
	}
	ids := make([]string, 0, len(candidate.Channels))
	for id := range candidate.Channels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		next := candidate.Channels[id]
		prev, existed := stored[id]
		switch {
		case !existed || boundPair(prev) != boundPair(next):
			// Row 1 (a removed instance has no candidate entry to clear).
			if next.Outbound == nil {
				continue
			}
			next.Outbound = nil
		case dedicated:
			continue // row 2
		case next.Outbound == nil:
			if prev.Outbound == nil {
				continue
			}
			next.Outbound = prev.Outbound.Clone() // row 3
		case next.Outbound.Equal(prev.Outbound):
			continue // row 4
		default:
			return nil, &OutboundProtectedError{InstanceID: id} // row 5
		}
		candidate.Channels[id] = next
		changed = append(changed, id)
	}
	return changed, nil
}
