package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// buildVolatileContext moves only the clock behind
// history. Sender/session metadata and live permission context stay in system.
func buildVolatileContext() string {
	localNow := time.Now()
	// Keep the local zone/offset and explicit UTC timestamp together, as before.
	now := localNow.Format("2006-01-02 15:04 MST (-07:00) Monday")
	utcNow := localNow.UTC().Format("2006-01-02 15:04:05Z")
	return fmt.Sprintf("## Current Time\n%s\nUTC: %s", now, utcNow)
}

// appendVolatileNote stages request-only text. consolidateVolatileNotes collects
// all such notes into one user-role message after the complete history. It never
// mutates the live model window or inserts text inside a tool/result group.
func appendVolatileNote(messages []providers.Message, note string) []providers.Message {
	if note == "" {
		return messages
	}
	out := append([]providers.Message(nil), messages...)
	return append(out, providers.Message{Role: "user", Content: note, Volatile: true})
}

func withoutVolatileNotes(messages []providers.Message) []providers.Message {
	out := make([]providers.Message, 0, len(messages))
	for _, message := range messages {
		if !message.Volatile {
			out = append(out, message)
		}
	}
	return out
}

func consolidateVolatileNotes(messages []providers.Message) []providers.Message {
	out := make([]providers.Message, 0, len(messages))
	var notes []string
	for _, message := range messages {
		if message.Volatile {
			if message.Content != "" {
				notes = append(notes, message.Content)
			}
			continue
		}
		out = append(out, message)
	}
	if len(notes) > 0 {
		out = append(out, providers.Message{Role: "user", Content: strings.Join(notes, "\n\n"), Volatile: true})
	}
	return out
}
