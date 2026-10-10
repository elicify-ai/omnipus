package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/memory"
)

// breadcrumbReadChunk is how many evicted slots one indexed read returns while
// the breadcrumb fills from the newest evicted slot backwards.
const breadcrumbReadChunk = 64

// buildArchiveBreadcrumb renders the actual evicted prefix of a validated window
// snapshot, newest first, through the ordinal index: it reads only as many
// evicted slots as the breadcrumb budget holds, never the whole prefix. Skip is
// explicit because staged relief may advance it before commit. Only capped
// literal snippets/pointers are retained; no recap is manufactured.
//
// "+N earlier ranges" counts by ordinal: every evicted slot older than the first
// one that no longer fits (and every oversize one skipped on the way) is
// omitted, whether or not it would have rendered text.
func buildArchiveBreadcrumb(ctx context.Context, src slotsBefore, skip int) (string, error) {
	if skip == 0 {
		return "", nil
	}
	capRunes := breadcrumbTokenCap * breadcrumbCharsPerToken
	reserve := utf8.RuneCountInString(archiveBreadcrumbHeader(maxIntValue()))
	reserve += utf8.RuneCountInString(fmt.Sprintf("\n+%d earlier ranges", maxIntValue()))
	budget := capRunes - reserve
	var entries []string // newest first
	used, omitted := 0, 0
fill:
	for hi := skip - 1; hi >= 0; {
		lo := max(0, hi-breadcrumbReadChunk+1)
		type slotEntry struct {
			idx, turn int
			msg       memory.ArchivedMessage
		}
		var chunk []slotEntry
		if err := src.readSlots(ctx, lo, hi, func(idx, turn int, msg memory.ArchivedMessage) error {
			chunk = append(chunk, slotEntry{idx, turn, msg})
			return nil
		}); err != nil {
			return "", fmt.Errorf("breadcrumb: read evicted slots [%d,%d]: %w", lo, hi, err)
		}
		for i := len(chunk) - 1; i >= 0; i-- {
			e := chunk[i]
			text := archiveBreadcrumbEntry(e.idx, e.turn, e.msg)
			if text == "" {
				continue
			}
			n := utf8.RuneCountInString(text) + 1
			if n > budget {
				omitted++
				continue
			}
			if used+n > budget {
				omitted += e.idx + 1 // this slot and everything older
				break fill
			}
			entries = append(entries, text)
			used += n
		}
		hi = lo - 1
	}
	var out strings.Builder
	out.WriteString(archiveBreadcrumbHeader(skip))
	for _, e := range entries {
		out.WriteByte('\n')
		out.WriteString(e)
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
