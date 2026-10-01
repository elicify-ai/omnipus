package agent

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/memory"
)

// buildArchiveBreadcrumb renders the actual evicted prefix of a validated window
// snapshot. Skip is explicit because staged relief may advance it before commit.
// Only capped literal snippets/pointers are retained; no recap is manufactured.
func buildArchiveBreadcrumb(archive []memory.ArchivedMessage, skip int) string {
	if skip == 0 {
		return ""
	}
	capRunes := breadcrumbTokenCap * breadcrumbCharsPerToken
	reserve := utf8.RuneCountInString(archiveBreadcrumbHeader(maxIntValue()))
	reserve += utf8.RuneCountInString(fmt.Sprintf("\n+%d earlier ranges", maxIntValue()))
	budget := capRunes - reserve
	type entry struct {
		text  string
		runes int
	}
	var entries []entry
	used, omitted, turn := 0, 0, 0
	for idx, msg := range archive[:skip] {
		if msg.Role == "user" {
			turn++
		}
		text := archiveBreadcrumbEntry(idx, turn, msg)
		if text == "" {
			continue
		}
		n := utf8.RuneCountInString(text) + 1
		if n > budget {
			omitted++
			continue
		}
		for used+n > budget && len(entries) > 0 {
			used -= entries[0].runes
			entries[0] = entry{} // release the dropped text, not just its slice index
			entries = entries[1:]
			omitted++
		}
		entries = append(entries, entry{text: text, runes: n})
		used += n
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
	return out.String()
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
