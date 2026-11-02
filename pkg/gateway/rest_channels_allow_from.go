package gateway

import "github.com/elicify-ai/omnipus/pkg/config"

// configureAllowListFields are the allowed-sender fields a Configure request
// may carry. Their value is saved as a list of ids (U7, channels spec FR-054).
var configureAllowListFields = []string{"allow_from", "group_allow_from"}

// normalizeConfigureAllowLists rewrites a string value of an allowed-sender
// field (any key spelling encoding/json binds to it) into the list of ids it
// names, split by config.SplitAllowFromText on commas, full-width commas and
// whitespace with empty pieces dropped. An older client that still sends the
// typed text therefore saves the same list the current panel sends, and ""
// saves the empty (allow-all) list. A list or any other value is left as it
// is: config loading reads it exactly as before.
func normalizeConfigureAllowLists(updates map[string]any) {
	for _, name := range configureAllowListFields {
		for _, key := range foldedKeys(updates, name) {
			text, isText := updates[key].(string)
			if !isText {
				continue
			}
			ids := config.SplitAllowFromText(text)
			if ids == nil {
				ids = []string{}
			}
			updates[key] = ids
		}
	}
}
