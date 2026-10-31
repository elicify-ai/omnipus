package config

// Channels package WP-0 shells (channels-spec-r8, "Persisted configuration",
// T-8, FR-014, FR-015, FR-018). Signatures only; nothing in production calls
// them yet.
//
// ChannelOutboundConfig is declared but NOT yet a field of
// ChannelInstanceConfig: adding `Outbound *ChannelOutboundConfig
// json:"outbound,omitempty"` makes set_config accept
// channels.<id>.outbound[.policy] today (its schema walk resolves struct
// fields by reflection), a behaviour change WP-0 must not make. The field
// lands together with the set_config deny entry (FR-024) and the guard.

import "errors"

// ChannelOutboundConfig is the persisted outbound setting of one channel
// instance, config.json key channels.<id>.outbound (FR-010). Absent means
// reply_only. Policy is "reply_only" or "agent_may_start"; Targets are
// canonical "platform:id" values.
type ChannelOutboundConfig struct {
	Policy  string              `json:"policy"`
	Targets FlexibleStringSlice `json:"targets"`
}

// ErrOutboundProtected is GuardOutbound's refusal (T-8 row 5): a save that is
// not the dedicated outbound writer tried to change a stored outbound
// setting. REST maps it to 403 "channels.<id>.outbound can only be changed
// with PUT /api/v1/channels/{id}/outbound".
var ErrOutboundProtected = errors.New("outbound_protected")

// ChannelAdapterView returns inst with its outbound setting cleared (FR-015).
// Every connection-lifecycle comparison uses it (toChannelHashes,
// channelConfigFingerprint, ChannelConfigChanges, MergeUnchangedChannelOrders)
// so a list-only change restarts nothing. The FR-088 revision does not use it.
//
// WP-0 shell: returns the zero ChannelInstanceConfig.
func ChannelAdapterView(inst ChannelInstanceConfig) ChannelInstanceConfig {
	return ChannelInstanceConfig{}
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
// WP-0 shell: returns (nil, nil) and changes nothing.
func GuardOutbound(before, candidate *Config, dedicated bool) (changed []string, err error) {
	return nil, nil
}
