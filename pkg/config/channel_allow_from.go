// channel_allow_from.go: allowed-sender list decoding for channel instances (U7).

package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// SplitAllowFromText splits a typed or legacy-saved Allow From text into its
// ids: commas (",", full-width "，") and any whitespace separate ids, and
// empty pieces are dropped. An empty or separator-only text yields an empty
// list, which means "allow every sender".
//
// Use it only for allowed-sender fields (allow_from, group_allow_from).
// Other list fields — outbound targets, IRC channels — keep
// FlexibleStringSlice's unsplit single-string behaviour.
func SplitAllowFromText(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '，' || unicode.IsSpace(r)
	})
}

// allowFromList is the dedicated JSON decoding type for a saved allowed-sender
// field. A single JSON string is split with SplitAllowFromText, so a value an
// older Configure saved as one string ("a, b c") self-heals on load into
// separate ids and "" becomes the empty (allow-all) list. Every other shape
// (a list, a number, null) decodes exactly as FlexibleStringSlice does, so a
// saved list loads unchanged.
type allowFromList []string

func (l *allowFromList) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*l = SplitAllowFromText(text)
		return nil
	}
	var flexible FlexibleStringSlice
	if err := json.Unmarshal(data, &flexible); err != nil {
		return err
	}
	*l = allowFromList(flexible)
	return nil
}

// UnmarshalJSON decodes a channel instance with the allowed-sender fields
// read through allowFromList; every other field decodes exactly as the plain
// struct would. A field absent from the JSON keeps its current value.
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
	if aux.AllowFrom != nil {
		var ids allowFromList
		if err := json.Unmarshal(aux.AllowFrom, &ids); err != nil {
			return fmt.Errorf("allow_from: %w", err)
		}
		c.AllowFrom = FlexibleStringSlice(ids)
	}
	if aux.GroupAllowFrom != nil {
		var ids allowFromList
		if err := json.Unmarshal(aux.GroupAllowFrom, &ids); err != nil {
			return fmt.Errorf("group_allow_from: %w", err)
		}
		c.GroupAllowFrom = FlexibleStringSlice(ids)
	}
	return nil
}
