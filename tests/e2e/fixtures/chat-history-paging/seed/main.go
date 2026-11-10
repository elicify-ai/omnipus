// A test-only executable: fixture writes go through the real canonical appender
// so the product's chat-index publication is exercised, never reimplemented.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func main() {
	root := flag.String("sessions-root", "", "isolated sessions directory")
	id := flag.String("session", "", "REST-created session")
	label := flag.String("label", "history", "fixture identity prefix")
	count := flag.Int("count", 240, "source group count")
	start := flag.Int("start", 0, "first fixture ordinal")
	bodyBytes := flag.Int("body-bytes", 0, "ASCII tool-result or content bytes")
	shape := flag.String("shape", "text", "text, varied, tools, or normal")
	clearAt := flag.String("clear-at", "", "comma-separated ordinals replaced with exact clear markers")
	imageURL := flag.String("image-url", "", "external image in first entry")
	flag.Parse()
	if !filepath.IsAbs(*root) || *id == "" || *count < 0 || *bodyBytes < 0 {
		fail(fmt.Errorf("absolute --sessions-root, --session and nonnegative counts are required"))
	}
	store, err := session.NewUnifiedStore(*root)
	if err != nil {
		fail(err)
	}
	meta, err := store.GetMeta(*id)
	if err != nil {
		fail(err)
	}
	clears := map[int]bool{}
	if *clearAt != "" {
		for _, raw := range strings.Split(*clearAt, ",") {
			n, parseErr := strconv.Atoi(raw)
			if parseErr != nil {
				fail(parseErr)
			}
			clears[n] = true
		}
	}
	// Fixed acceptance identities and times, not expected output copied from a run.
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	for i := *start; i < *start+*count; i++ {
		key := fmt.Sprintf("%s-%06d", *label, i)
		entry := session.TranscriptEntry{
			ID: key, Type: session.EntryTypeMessage, Role: "assistant",
			ViewMembership: session.ViewMembershipChat, AgentID: meta.AgentID,
			Timestamp: at.Add(time.Duration(i) * time.Second), Content: key,
		}
		switch *shape {
		case "varied":
			entry.Content += strings.Repeat("\n\nA paragraph with enough words to wrap naturally in the real chat column.", []int{0, 1, 4, 24}[i%4])
		case "tools":
			entry.Content = "" // One finished logical tool row, not narration + tool.
			entry.ToolCalls = []session.ToolCall{{
				ID: session.ToolCallID("call-" + key), Tool: "read_file", Status: "success",
				Parameters: map[string]any{"path": key + ".txt"},
				Result:     map[string]any{"content": key + strings.Repeat("x", *bodyBytes)},
			}}
		case "text", "normal":
			if *bodyBytes > len(key) {
				entry.Content += strings.Repeat("x", *bodyBytes-len(key))
			}
		default:
			fail(fmt.Errorf("unknown fixture shape %q", *shape))
		}
		if clears[i] {
			entry.Type, entry.Role, entry.Content = session.EntryTypeSystem, "system", "Conversation context cleared"
			entry.ToolCalls = nil
		}
		if *imageURL != "" && i == *start {
			entry.Content += "\n\n![Deferred history image](" + *imageURL + ")"
		}
		if _, err := store.AppendTranscriptAddressed(*id, entry); err != nil {
			fail(fmt.Errorf("append fixture %s: %w", key, err))
		}
	}
	if err := store.Close(); err != nil {
		fail(fmt.Errorf("close seeded store: %w", err))
	}
	// Private fixture receipt, not a gateway/SPA wire-format type.
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"session_id": *id, "label": *label, "count": *count, "first": *start,
		"last": *start + *count - 1, "canonical_writer": "UnifiedStore.AppendTranscriptAddressed",
	}); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
