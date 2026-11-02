package gateway

import "github.com/elicify-ai/omnipus/pkg/config"

// configureAllowListFields are the allowed-sender fields a Configure request
// may carry. Their value is saved as a list of ids (U7, channels spec FR-054).
var configureAllowListFields = []string{"allow_from", "group_allow_from"}

// normalizeConfigureAllowLists rewrites a string value of an allowed-sender
// field (any key spelling encoding/json binds to it) into the list of ids it
// names, split by config.SplitAllowFromText with the separators of the
// instance's channel type: commas and full-width commas always, whitespace
// too except on WhatsApp (phone numbers may contain spaces). Each id is
// trimmed and empty pieces are dropped. An older client that still sends the
// typed text therefore saves the same list the current panel sends, and ""
// saves the empty (allow-all) list. A list or any other value is left as it
// is: config loading reads it exactly as before.
func normalizeConfigureAllowLists(updates map[string]any, channelID string) {
	for _, name := range configureAllowListFields {
		for _, key := range foldedKeys(updates, name) {
			text, isText := updates[key].(string)
			if !isText {
				continue
			}
			ids := config.SplitAllowFromText(channelID, text)
			updates[key] = ids
		}
	}
}

// configureNullAllowListRefusal returns the 400 text when an allowed-sender
// field (any key spelling encoding/json binds to it) is null, or "". A null
// is refused rather than saved: on load it would block every sender, and
// clearing the list to answer everyone is an empty list or empty text.
func configureNullAllowListRefusal(updates map[string]any) string {
	for _, name := range configureAllowListFields {
		for _, key := range foldedKeys(updates, name) {
			if updates[key] == nil {
				return name + " cannot be null; send a list of ids, or an empty list to allow every sender"
			}
		}
	}
	return ""
}

// dropConfigureOutcomeKeys removes the Configure response's own outcome keys
// (any key spelling encoding/json binds to them) from a request: they are
// never config (FR-023).
func dropConfigureOutcomeKeys(updates map[string]any) {
	for _, name := range []string{"apply_outcome", "apply_reason", "apply_note"} {
		for _, key := range foldedKeys(updates, name) {
			delete(updates, key)
		}
	}
}
