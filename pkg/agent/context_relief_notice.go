package agent

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Text authored by prometheus-prompt-engineer for MAJ-CW-008.
func contextReliefNoticeText(evictedFrom, evictedTo int, evictedOK bool, shortenedCount int) string {
	var clauses []string
	if evictedOK {
		rng := fmt.Sprintf("%d-%d", evictedFrom, evictedTo)
		clauses = append(clauses, fmt.Sprintf(
			"turns %d–%d left your view this turn — recall_conversation(turn_range=%q) returns them",
			evictedFrom, evictedTo, rng))
	}
	if shortenedCount > 0 {
		clauses = append(clauses, fmt.Sprintf(
			"%d tool result(s) in this request were shortened this turn — each carries a recall mark "+
				"with the tool_call_id/archive_line recall_conversation needs", shortenedCount))
	}
	if len(clauses) == 0 {
		return ""
	}
	return "[context notice: " + strings.Join(clauses, "; ") + "]"
}

type contextReliefNotice struct {
	from, to  int
	evicted   bool
	shortened memory.ProjectionSet
}

func (n contextReliefNotice) clone() contextReliefNotice {
	n.shortened = n.shortened.Clone()
	return n
}

func stripContextReliefNotice(msgs []providers.Message) []providers.Message {
	out := make([]providers.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" && strings.HasPrefix(m.Content, "[context notice: ") && strings.HasSuffix(m.Content, "]") {
			continue
		}
		out = append(out, m)
	}
	return out
}

func injectContextReliefNotice(msgs []providers.Message, n contextReliefNotice) []providers.Message {
	out := stripContextReliefNotice(msgs)
	text := contextReliefNoticeText(n.from, n.to, n.evicted, len(n.shortened))
	if text == "" {
		return out
	}
	// A raw live window has no pinned system slot. Keep its first user at
	// index zero and count the notice normally, rather than treating it as core.
	at := len(out)
	if len(out) > 0 && out[0].Role == "system" {
		at = 1
	}
	out = append(out, providers.Message{})
	copy(out[at+1:], out[at:len(out)-1])
	out[at] = providers.Message{Role: "system", Content: text}
	return out
}

func (n *contextReliefNotice) addEvicted(from, to int) {
	if !n.evicted {
		n.from, n.to = from, to
	} else {
		n.from = min(n.from, from)
		n.to = max(n.to, to)
	}
	n.evicted = true
}

func (p *windowCheckpoint) refreshNotice() {
	msgs := make([]providers.Message, 0, len(p.messages))
	lines := make([]int, 0, len(p.lines))
	for i, m := range p.messages {
		if m.Role == "system" && strings.HasPrefix(m.Content, "[context notice: ") && strings.HasSuffix(m.Content, "]") {
			continue
		}
		msgs = append(msgs, m)
		lines = append(lines, p.lines[i])
	}
	p.messages = injectContextReliefNotice(msgs, p.notice)
	if len(p.messages) > len(msgs) {
		at := len(msgs)
		if len(msgs) > 0 && msgs[0].Role == "system" {
			at = 1
		}
		lines = append(lines, -1)
		copy(lines[at+1:], lines[at:len(lines)-1])
		lines[at] = -1
	}
	p.lines = lines
}
