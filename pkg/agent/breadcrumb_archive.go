package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/memory"
)

// buildPersistedBreadcrumb indexes the actual evicted prefix, even when it has
// no user boundary or assembled history includes extra/shortened live messages.
// Only capped literal snippets/pointers are retained; no recap is manufactured.
func buildPersistedBreadcrumb(ctx context.Context, archive ConversationArchiveReader, key string, tokenCap int) (string, error) {
	reader, ok := archive.(memory.EvictedArchiveScanner)
	if !ok {
		return "", fmt.Errorf("persisted evicted archive reader unavailable")
	}
	capRunes := tokenCap * breadcrumbCharsPerToken
	reserve := utf8.RuneCountInString(archiveBreadcrumbHeader(maxIntValue()))
	reserve += utf8.RuneCountInString(fmt.Sprintf("\n+%d earlier ranges", maxIntValue()))
	budget := capRunes - reserve
	if budget < 1 {
		return "", fmt.Errorf("breadcrumb cap cannot fit the archive address")
	}
	type entry struct {
		text  string
		runes int
	}
	var entries []entry
	used, omitted, turn := 0, 0, 0
	skip, err := reader.ScanEvictedArchive(ctx, key, func(idx int, _ []byte, msg memory.ArchivedMessage) error {
		if msg.Role == "user" {
			turn++
		}
		text := archiveBreadcrumbEntry(idx, turn, msg)
		if text == "" {
			return nil
		}
		n := utf8.RuneCountInString(text) + 1
		if n > budget {
			omitted++
			return nil
		}
		for used+n > budget && len(entries) > 0 {
			used -= entries[0].runes
			entries[0] = entry{} // release the dropped text, not just its slice index
			entries = entries[1:]
			omitted++
		}
		entries = append(entries, entry{text: text, runes: n})
		used += n
		return nil
	})
	if err != nil {
		if skip > 0 {
			return archiveBreadcrumbHeader(skip), err
		}
		return "", err
	}
	if skip == 0 {
		return "", nil
	}
	var out strings.Builder
	out.WriteString(archiveBreadcrumbHeader(skip))
	for i := len(entries) - 1; i >= 0; i-- {
		out.WriteByte('\n')
		out.WriteString(entries[i].text)
	}
	if omitted > 0 {
		fmt.Fprintf(&out, "\n+%d earlier ranges", omitted)
	}
	return out.String(), nil
}

func archiveBreadcrumbHeader(skip int) string {
	return fmt.Sprintf("## Earlier in this conversation (evicted from the live window)\n"+
		"Use the recall_conversation tool to read any of these verbatim.\narchive_range={from:0,to:%d}", skip-1)
}

func archiveBreadcrumbEntry(idx, turn int, msg memory.ArchivedMessage) string {
	snippet := truncateSnippet(msg.Content, 80)
	if msg.Role == "tool" && msg.ToolCallID != "" {
		return fmt.Sprintf("- tool_call_id=%q, archive_line=%d · %q", msg.ToolCallID, idx, snippet)
	}
	if snippet == "" {
		return ""
	}
	if msg.Role != "user" {
		return fmt.Sprintf("- archive_line=%d · %q", idx, snippet)
	}
	rel := "earlier"
	if msg.TS != 0 {
		rel = formatRelTime(time.Unix(msg.TS, 0), breadcrumbNowFn())
	}
	entities := extractEntities(msg.Content)
	if entities != "" {
		entities = " · " + entities
	}
	return fmt.Sprintf("- turn %d · archive_line=%d · %s · %q%s", turn, idx, rel, snippet, entities)
}

// Preserve a verified prefix address on storage faults, but do not publish the
// partially indexed snippets as if indexing succeeded. The cause stays visible.
func breadcrumbReadFailure(header string, err error, tokenCap int) string {
	if header == "" {
		header = "## Earlier in this conversation"
	}
	text := header + "\nArchive read failed: " + err.Error()
	limit := tokenCap * breadcrumbCharsPerToken
	runes := []rune(text)
	return string(runes[:min(len(runes), limit)])
}
