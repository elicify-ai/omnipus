// channel_allow_from.go: allowed-sender list decoding for channel instances (U7).

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// AllowFromSplitsOnWhitespace reports whether whitespace separates ids in an
// allowed-sender text of the given channel type (an instance key such as
// "whatsapp.eu" is accepted too). Commas ("," and full-width "，") always
// separate ids. WhatsApp is the one exception to whitespace (chief ruling
// 2026-11-02, option B): a phone number may contain spaces, so a WhatsApp
// text splits at commas only and each id keeps its inner spaces.
func AllowFromSplitsOnWhitespace(channelType string) bool {
	base, _ := ParseInstanceKey(strings.ToLower(strings.TrimSpace(channelType)))
	return base != "whatsapp" && base != "whatsapp_native"
}

// SplitAllowFromText splits a typed or legacy-saved Allow From text of the
// given channel type into its ids with that type's separators (see
// AllowFromSplitsOnWhitespace). Each id is trimmed and empty pieces are
// dropped, so an empty or separator-only text yields an empty list, which
// means "allow every sender".
//
// Use it only for allowed-sender fields (allow_from, group_allow_from).
// Other list fields — outbound targets, IRC channels — keep
// FlexibleStringSlice's unsplit single-string behaviour.
func SplitAllowFromText(channelType, text string) []string {
	onWhitespace := AllowFromSplitsOnWhitespace(channelType)
	pieces := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '，' || (onWhitespace && unicode.IsSpace(r))
	})
	ids := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		if id := strings.TrimSpace(piece); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// pendingAllowFromText holds allowed-sender fields that were saved as one
// string on an instance without a "type" key. The effective type then comes
// from the instance's map key, which only the loader knows, so
// CanonicalChannelInstance re-splits these texts with it.
type pendingAllowFromText struct {
	allowFrom      *string
	groupAllowFrom *string
}

// decodeAllowFromField decodes one saved allowed-sender field. A single JSON
// string is split with SplitAllowFromText for channelType, so a value an
// older Configure saved as one string ("a, b c") self-heals on load into
// separate ids and "" becomes the empty (allow-all) list; the text is also
// returned so it can be re-split once the effective type is known. An
// explicit null loads as one empty id, which matches no sender, so it keeps
// the release's deny-all meaning (chief ruling C, CHECK F4); a missing key
// never reaches this function and keeps allowing everyone. Every other shape
// (a list, a number) decodes exactly as FlexibleStringSlice does, so a saved
// list loads unchanged.
func decodeAllowFromField(raw json.RawMessage, channelType string) (FlexibleStringSlice, *string, error) {
	if string(bytes.TrimSpace(raw)) == "null" {
		return FlexibleStringSlice{""}, nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return FlexibleStringSlice(SplitAllowFromText(channelType, text)), &text, nil
	}
	var flexible FlexibleStringSlice
	if err := json.Unmarshal(raw, &flexible); err != nil {
		return nil, nil, err
	}
	return flexible, nil, nil
}

// UnmarshalJSON decodes a channel instance with the allowed-sender fields
// read through decodeAllowFromField, using the instance's own "type" for the
// separators; every other field decodes exactly as the plain struct would. A
// field absent from the JSON keeps its current value.
func (c *ChannelInstanceConfig) UnmarshalJSON(data []byte) error {
	type plainInstance ChannelInstanceConfig
	aux := struct {
		*plainInstance
		AllowFrom      json.RawMessage `json:"allow_from,omitempty"`
		GroupAllowFrom json.RawMessage `json:"group_allow_from,omitempty"`
	}{plainInstance: (*plainInstance)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	pending := &pendingAllowFromText{}
	if aux.AllowFrom != nil {
		ids, text, err := decodeAllowFromField(aux.AllowFrom, c.Type)
		if err != nil {
			return fmt.Errorf("allow_from: %w", err)
		}
		c.AllowFrom, pending.allowFrom = ids, text
	}
	if aux.GroupAllowFrom != nil {
		ids, text, err := decodeAllowFromField(aux.GroupAllowFrom, c.Type)
		if err != nil {
			return fmt.Errorf("group_allow_from: %w", err)
		}
		c.GroupAllowFrom, pending.groupAllowFrom = ids, text
	}
	c.allowFromText = nil
	if strings.TrimSpace(c.Type) == "" && (pending.allowFrom != nil || pending.groupAllowFrom != nil) {
		c.allowFromText = pending
	}
	return nil
}

// resolvePendingAllowFrom re-splits allowed-sender texts kept for an instance
// that had no "type" key, now that its effective type is known, and clears
// them. It works on the caller's copy.
func resolvePendingAllowFrom(inst *ChannelInstanceConfig, effectiveType string) {
	pending := inst.allowFromText
	if pending == nil {
		return
	}
	if pending.allowFrom != nil {
		inst.AllowFrom = FlexibleStringSlice(SplitAllowFromText(effectiveType, *pending.allowFrom))
	}
	if pending.groupAllowFrom != nil {
		inst.GroupAllowFrom = FlexibleStringSlice(SplitAllowFromText(effectiveType, *pending.groupAllowFrom))
	}
	inst.allowFromText = nil
}
