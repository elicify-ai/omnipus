package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// normalizeWindowMessages keeps the original initiating user and unconsumed
// controls as separate slots. Other runs retain the shared normalization rules.
// All instruction messages compose in order into one system stream so adapters
// with a single instructions field cannot overwrite the pinned prompt.
func normalizeWindowMessages(ts *turnState, messages []providers.Message) []providers.Message {
	var system providers.Message
	var text []string
	var body []providers.Message
	for _, m := range messages {
		if m.Role != "system" {
			body = append(body, m)
			continue
		}
		if system.Role == "" {
			system = m
			system.SystemParts = nil
		}
		text = append(text, m.Content)
		if len(m.SystemParts) > 0 {
			system.SystemParts = append(system.SystemParts, m.SystemParts...)
		} else if m.Content != "" {
			system.SystemParts = append(system.SystemParts, providers.ContentBlock{Type: "text", Text: m.Content})
		}
	}
	out := make([]providers.Message, 0, len(messages))
	if system.Role != "" {
		system.Content = strings.Join(text, "\n\n")
		out = append(out, system)
	}
	start := 0
	for i, m := range body {
		protected := ts != nil && ((m.Role == "user" && m.Content == ts.userMessage) || ts.protectedWindowMessage(m))
		if !protected {
			continue
		}
		out = append(out, normalizeMessagesForProvider(body[start:i])...)
		out = append(out, m)
		start = i + 1
	}
	return append(out, normalizeMessagesForProvider(body[start:])...)
}
