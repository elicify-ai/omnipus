package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// slideOldest advances only over an entire complete prefix. Newest/incomplete
// assistant structure, unarchived messages and unconsumed controls block a cut.
func (p *windowCheckpoint) slideOldest() bool {
	steps := windowSteps(p.messages)
	newest := -1
	for i, m := range p.messages {
		if m.Role == "assistant" {
			newest = i
		}
	}
	for _, step := range steps {
		if !step.complete || step.start == newest {
			return false
		}
		end := step.end
		for end < len(p.messages) && p.messages[end].Role == "assistant" && len(p.messages[end].ToolCalls) == 0 && end != newest {
			end++
		}
		cut := p.state.Skip
		for i := step.start; i < end; i++ {
			if p.lines[i] < 0 {
				return false
			}
			cut = max(cut, p.lines[i]+1)
		}
		if cut <= p.state.Skip {
			continue
		}
		for i := 0; i < end; i++ {
			m, line := p.messages[i], p.lines[i]
			if m.Role == "system" || line == p.anchor {
				continue
			}
			if line < 0 || p.ts.protectedWindowMessage(m) {
				return false
			}
		}
		for _, s := range steps {
			if s.start < end && (!s.complete || s.start == newest) {
				return false
			}
		}
		out := make([]providers.Message, 0, len(p.messages))
		lines := make([]int, 0, len(p.lines))
		for i, m := range p.messages {
			line := p.lines[i]
			if line >= 0 && line < cut && line != p.anchor {
				continue
			}
			out = append(out, m)
			lines = append(lines, line)
		}
		p.messages, p.lines = out, lines
		from := turnNumberForArchiveLine(p.snapshot.Archive, p.state.Skip)
		to := turnNumberForArchiveLine(p.snapshot.Archive, cut-1)
		p.notice.addEvicted(from, to)
		p.state.Skip = cut
		if p.anchor >= 0 && p.anchor < cut {
			a := p.anchor
			p.state.AnchorLine = &a
		} else {
			p.state.AnchorLine = nil
		}
		for key := range p.state.Projection.Entries {
			if key.ArchiveLine < cut {
				delete(p.state.Projection.Entries, key)
				delete(p.state.Projection.SourceRunes, key)
				delete(p.notice.shortened, key)
			}
		}
		for key := range p.state.Projection.TranscriptLine {
			if key.ArchiveLine < cut {
				delete(p.state.Projection.TranscriptLine, key)
			}
		}
		return true
	}
	return false
}

// Older retained results empty oldest-first. Once only newest text is eligible,
// traverse its declared call order; each operation removes original source runes.
func (p *windowCheckpoint) shortenNext() (bool, error) {
	steps := windowSteps(p.messages)
	if len(steps) == 0 {
		return false, nil
	}
	for _, step := range steps[:len(steps)-1] {
		for _, i := range step.results {
			changed, err := p.shortenResult(i, false)
			if changed || err != nil {
				return changed, err
			}
		}
	}
	newest := steps[len(steps)-1]
	for _, call := range p.messages[newest.start].ToolCalls {
		for _, i := range newest.results {
			if p.messages[i].ToolCallID != call.ID {
				continue
			}
			changed, err := p.shortenResult(i, true)
			if changed || err != nil {
				return changed, err
			}
		}
	}
	return false, nil
}

func (p *windowCheckpoint) shortenResult(i int, halve bool) (bool, error) {
	m, line := p.messages[i], p.lines[i]
	if line < 0 {
		return false, nil
	}
	key := memory.ProjectionKey{ToolCallID: m.ToolCallID, ArchiveLine: line}
	state := p.state.Projection.Entries[key]
	k, err := retainedSourceRunes(p.messages, i, line, state, p.projectionContext())
	if err != nil {
		return false, err
	}
	if k == 0 {
		return false, nil
	}
	kept := 0
	if halve {
		kept = k / 2
	}
	tool, _ := owningToolCall(p.messages, i, m.ToolCallID)
	content, err := projectSource(p.snapshot.Archive, line, kept, tool, m.ToolCallID)
	if err != nil {
		return false, err
	}
	p.messages[i].Content = content
	if kept == 0 {
		state = memory.ProjectionEmptied
	} else if state != memory.ProjectionCappedFailure {
		state = memory.ProjectionCapped
	}
	p.state.Projection.Entries[key] = state
	p.state.Projection.SourceRunes[key] = kept
	p.notice.shortened[key] = state
	return true, nil
}

func breadcrumbForWindow(snap memory.WindowSnapshot, skip int) string {
	// buildBreadcrumb consumes only the split length. Use actual Skip, not
	// anchored-view length, to name the archived prefix even within a user turn.
	return buildBreadcrumb(snap.Archive, make([]providers.Message, max(0, len(snap.Archive)-skip)), breadcrumbTokenCap)
}

func (p *windowCheckpoint) rebuildBreadcrumb() {
	if p.state.Skip == p.snapshot.State.Skip || len(p.messages) == 0 || p.messages[0].Role != "system" {
		return
	}
	old := p.breadcrumb
	next := breadcrumbForWindow(p.snapshot, p.state.Skip)
	m := p.messages[0]
	m.SystemParts = append([]providers.ContentBlock(nil), m.SystemParts...)
	found := false
	for i, part := range m.SystemParts {
		if strings.HasPrefix(part.Text, "## Earlier in this conversation") {
			m.SystemParts[i].Text = strings.Replace(part.Text, old, next, 1)
			found = true
		}
	}
	if old != "" && strings.Contains(m.Content, old) {
		m.Content = strings.Replace(m.Content, old, next, 1)
	} else {
		// Subsequent staged cuts replace the last staged breadcrumb rather
		// than generating another copy of archive framing.
		at := strings.Index(m.Content, "\n\n---\n\n## Earlier in this conversation")
		if at >= 0 {
			m.Content = m.Content[:at]
		}
		m.Content += "\n\n---\n\n" + next
	}
	if !found {
		m.SystemParts = append(m.SystemParts, providers.ContentBlock{Type: "text", Text: next})
	}
	p.messages[0] = m
	p.breadcrumb = next
}
