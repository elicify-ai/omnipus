// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// tool_result_admit.go — the ADR-066 D4 choke point: the ONE function through
// which every tool result becomes a context message (spec FR-009–FR-013,
// FR-019 capped half, FR-046; US-3; B-11 … B-16b).
//
// Order, fixed (FR-032 / behavioural contract):
//
//	filter → per-surface cap → clamp to half the budget (and /N for parallel
//	calls that would not fit) → encoded-line bound → archive the full
//	filtered content → record (tool_call_id, archive_line) → capped → hand
//	the window form to the caller.
//
// The twelve producers (nine in loop.go — the success-path result, the seven
// denied sites and the skipped site — plus attachment hydration and the
// recall page) call here; the orphan-repair placeholder in repair.go is the
// single exemption (bounded by construction). TestChokePoint_ProducerListByGrep
// fails the build-time invariant if a new `Role: "tool"` literal appears
// anywhere else in the package.
//
// Nothing here summarises, rewrites or deletes: the archive line always holds
// the full filtered content (bounded only by the encoded-line limit that keeps
// the session readable), the window carries a head-and-tail cut around the
// typed cap mark, and the projection function (projection.go) re-derives the
// same bytes on reload from the persisted state.
package agent

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// toolResultSurface names the cap a result falls under (ADR-066 §5 surface
// table). Success/failure is the producer's IsError; MCP-ness is the tool's
// registry name (`mcp_<server>_<tool>`, pkg/tools/mcp_tool.go) — a name-based
// predicate so the live path and the reload projection, which only sees the
// name on the owning assistant call, classify identically.
type toolResultSurface string

const (
	surfaceMCP            toolResultSurface = "mcp"
	surfaceBuiltinSuccess toolResultSurface = "builtin_success"
	surfaceBuiltinFailure toolResultSurface = "builtin_failure"

	// mcpToolNamePrefix is the registry-name prefix every MCP tool carries
	// (pkg/tools/mcp_tool.go builds "mcp_<server>_<tool>").
	mcpToolNamePrefix = "mcp_"

	// estimatorCharsPerToken is the 2.5 chars/token ratio the budget clamp
	// converts B (tokens) into characters with — the inverse of
	// estimateMessageTokens' chars × 2/5.
	estimatorCharsPerToken = 2.5

	// archivedLineSafetyBytes is the margin the encoded-line bound keeps
	// below memory.EncodedLineBound for the per-line timestamp the store
	// stamps after the choke point has measured the line.
	archivedLineSafetyBytes = 64
)

// toolResultLargeTotal backs omnipus_tool_result_large_total (B-13,
// US-3.AC7): results over the warn threshold but under their cap.
var toolResultLargeTotal atomic.Int64

// ToolResultLargeTotal returns the tool_result_large_total counter — results
// admitted unmodified while over the warn threshold. Rendered by the gateway's
// /metrics handler.
func ToolResultLargeTotal() int64 { return toolResultLargeTotal.Load() }

// toolResultSurfaceFor classifies a result. Every failure (builtin or MCP,
// denied, skipped, refused arguments) is the builtin-failure surface; an MCP
// success is the mcp surface; everything else — builtin success, a delegate
// report, a hydrated attachment, a recall page — is builtin-success. No
// per-tool or per-server opt-out exists (FR-010).
func toolResultSurfaceFor(tool string, isError bool) toolResultSurface {
	if isError {
		return surfaceBuiltinFailure
	}
	if strings.HasPrefix(tool, mcpToolNamePrefix) {
		return surfaceMCP
	}
	return surfaceBuiltinSuccess
}

// resultCapPolicy is the per-call snapshot of everything the cap depends on:
// the three configured caps, the warn threshold and the one budget B
// (tokens). It is built fresh for every admission (US-3.AC11 — settings are
// read per call) and for every reload projection.
type resultCapPolicy struct {
	McpCap            int
	BuiltinSuccessCap int
	BuiltinFailureCap int
	WarnThreshold     int
	// Budget is B in tokens. ≤ 0 means "no budget" (an exempt provider, or
	// a caller without an agent): the configured cap applies unclamped.
	Budget int
}

// capPolicyFor snapshots the settings. A cap that is unset (≤ 0 — a config
// literal in a test, a config.json written before the field existed) falls
// back to the shipped default: 0 is never a valid cap (the settings API
// rejects it with 400), and treating it as "cap everything to nothing" would
// silently blank every result.
func capPolicyFor(cs config.ContextSettings, budget int) resultCapPolicy {
	orDefault := func(v, def int) int {
		if v <= 0 {
			return def
		}
		return v
	}
	return resultCapPolicy{
		McpCap:            orDefault(cs.McpResultCap, config.DefaultMcpResultCap),
		BuiltinSuccessCap: orDefault(cs.BuiltinSuccessCap, config.DefaultBuiltinSuccessCap),
		BuiltinFailureCap: orDefault(cs.BuiltinFailureCap, config.DefaultBuiltinFailureCap),
		WarnThreshold:     orDefault(cs.WarnThreshold, config.DefaultContextWarnThreshold),
		Budget:            budget,
	}
}

// configuredCap is the surface's operator-set cap before any clamp.
func (p resultCapPolicy) configuredCap(surface toolResultSurface) int {
	switch surface {
	case surfaceMCP:
		return p.McpCap
	case surfaceBuiltinFailure:
		return p.BuiltinFailureCap
	default:
		return p.BuiltinSuccessCap
	}
}

// effectiveCap applies FR-011:
//
//	effective_cap = min(configured_cap, floor(0.5 × B × 2.5))
//	per-result cap = effective_cap / N   when N × effective_cap × 0.4 > B
//
// so one result can never exceed half the budget and N parallel results of
// one assistant message always fit in B together — the invariant that keeps
// the D6 floor satisfiable (CRIT-002, E3). parallelN < 1 is treated as 1.
func (p resultCapPolicy) effectiveCap(surface toolResultSurface, parallelN int) int {
	eff := p.configuredCap(surface)
	if p.Budget > 0 {
		half := int(math.Floor(0.5 * float64(p.Budget) * estimatorCharsPerToken))
		if half < eff {
			eff = half
		}
		if parallelN > 1 && parallelN*eff*2/5 > p.Budget {
			eff /= parallelN
		}
	}
	if eff < 1 {
		eff = 1
	}
	return eff
}

// projectToolResult is the pure cap: content at or under capChars (runes) is
// returned unmodified; over it, the result is cut head-and-tail around the
// mark markFor produces for it, the mark's own length counted toward the cap.
// It is the single function both the live admission and the reload
// projection use, so the two agree byte for byte for the same inputs.
func projectToolResult(content string, capChars int, markFor func(full string) string) (string, bool) {
	if utf8.RuneCountInString(content) <= capChars {
		return content, false
	}
	return cutHeadAndTail(content, capChars, markFor(content)), true
}

// cutHeadAndTail returns head + "\n" + mark + "\n" + tail with exactly
// capChars runes in total, head and tail split 50/50 (the tail takes the odd
// rune) and never splitting a rune (E5). When the mark alone does not fit,
// the mark is truncated to the cap — the only information that survives is
// the pointer back to the archive, which is the right thing to keep.
func cutHeadAndTail(content string, capChars int, mark string) string {
	markRunes := utf8.RuneCountInString(mark)
	avail := capChars - markRunes - 2
	if avail < 0 {
		mr := []rune(mark)
		if capChars < len(mr) {
			mr = mr[:max(0, capChars)]
		}
		return string(mr)
	}
	r := []rune(content)
	head := avail / 2
	tail := avail - head
	var sb strings.Builder
	sb.Grow(len(content))
	sb.WriteString(string(r[:head]))
	sb.WriteString("\n")
	sb.WriteString(mark)
	sb.WriteString("\n")
	sb.WriteString(string(r[len(r)-tail:]))
	return sb.String()
}

// boundArchivedMessage enforces FR-012: the JSON-encoded archive line of msg
// must fit in memory.EncodedLineBound (0.8 × the store's scanner limit). A
// line over the scanner limit would break every later line of the session
// read, so this is checked post-filter, pre-append, on the exact bytes the
// store writes. Content that does not fit is cut head-and-tail — a plain
// join, no mark: the mark the window carries states the archived size and
// the WARN the caller logs states the original.
//
// Media counts too. attachToolResultMedia inlines base64 data URLs into
// msg.Media (bounded only by max_media_size, 20 MB by default) and
// encodedArchiveLineLen measures them — but the shrink loop only ever
// touched Content, so a single large screenshot made the loop unable to make
// progress: once `keep` hit 0 every further iteration was a no-op and the
// function returned "cut: true" for a line that was still megabytes over the
// STORE's own maxLineSize. Appending it broke every later read of that
// session (bufio.Scanner ErrTooLong), and GetHistory then returned an empty
// slice — the session's whole history silently gone, permanently. So once
// Content is exhausted the media entries are dropped, largest first.
//
// Returns the (possibly cut) message, the original rune count, whether a cut
// happened, and whether the line STILL does not fit — which the caller must
// treat as "do not archive this".
func boundArchivedMessage(msg providers.Message) (out providers.Message, origRunes int, cut, overflow bool) {
	origRunes = utf8.RuneCountInString(msg.Content)
	target := memory.EncodedLineBound - archivedLineSafetyBytes
	encodedLen := encodedArchiveLineLen(msg)
	if encodedLen <= target {
		return msg, origRunes, false, false
	}
	// Media first, and ONLY when it is the term that does not fit: measure the
	// line with the content removed. If even that is over the bound, no amount
	// of content shrinking can help, so entries go largest-first. If it fits,
	// the media stays and the content shrink below does the work.
	probe := msg
	probe.Content = ""
	for encodedArchiveLineLen(probe) > target && len(probe.Media) > 0 {
		largest := 0
		for i := range probe.Media {
			if len(probe.Media[i]) > len(probe.Media[largest]) {
				largest = i
			}
		}
		trimmed := make([]string, 0, len(probe.Media)-1)
		trimmed = append(trimmed, probe.Media[:largest]...)
		trimmed = append(trimmed, probe.Media[largest+1:]...)
		probe.Media = trimmed
	}
	if len(probe.Media) != len(msg.Media) {
		msg.Media = probe.Media
		encodedLen = encodedArchiveLineLen(msg)
		if encodedLen <= target {
			return msg, origRunes, true, false
		}
	}

	r := []rune(msg.Content)
	keep := len(r)
	for i := 0; i < 24 && encodedLen > target; i++ {
		// Shrink proportionally with a 5 % margin, then re-measure on the
		// real encoding: escapes make bytes-per-rune content-dependent.
		ratio := float64(target) / float64(encodedLen) * 0.95
		next := int(float64(keep) * ratio)
		if next >= keep {
			next = keep - 1
		}
		if next < 0 {
			next = 0
		}
		keep = next
		head := keep / 2
		tail := keep - head
		msg.Content = string(r[:head]) + "\n" + string(r[len(r)-tail:])
		encodedLen = encodedArchiveLineLen(msg)
	}
	return msg, origRunes, true, encodedLen > target
}

// encodedArchiveLineLen measures the line exactly as memory.JSONLStore writes
// it (ArchivedMessage = Message + ts), with a worst-case timestamp width.
func encodedArchiveLineLen(msg providers.Message) int {
	b, err := json.Marshal(memory.ArchivedMessage{Message: msg, TS: math.MaxInt64})
	if err != nil {
		// A message that cannot be encoded cannot be archived at all; report
		// "too large" so the caller shrinks it toward something encodable.
		return math.MaxInt32
	}
	return len(b)
}

// toolResultAdmission is what a producer hands the choke point.
type toolResultAdmission struct {
	// Tool is the registry name of the tool whose call this result answers.
	// For the mark and the surface it must be the name on the owning
	// assistant message's tool call (tc.Name), so reload classifies the
	// same way.
	Tool string
	// ToolCallID is the call this result answers.
	ToolCallID string
	// Content is the result text after the prompt guard (SEC-25) and before
	// the sensitive-data filter — the choke point applies the filter itself
	// (FR-013), on the full content, before anything is cut.
	Content string
	// Media are inline media refs attached to the result (the success path's
	// attachToolResultMedia output); carried unchanged on both forms.
	Media []string
	// IsError selects the failure surface. Denied, skipped and refused
	// results set it.
	IsError bool
	// ParallelN is the number of tool calls on the owning assistant message
	// (FR-011's N). < 1 is treated as 1.
	ParallelN int
}

// admittedToolResult is what the producer gets back.
type admittedToolResult struct {
	// Message is the window form: what goes into the in-memory slice and
	// therefore into the next request. Role "tool", ToolCallID set.
	Message providers.Message
	// Archived is the line appended to the archive: the full filtered
	// content (cut only by the encoded-line bound).
	Archived providers.Message
	// Capped reports that Message carries the head-and-tail form and the
	// projection state was recorded.
	Capped bool
	// ArchiveLine is the zero-based archive line Archived landed on, or -1
	// when nothing was persisted (NoHistory, no store).
	ArchiveLine int
	// OriginalRunes is the rune count of the filtered content before the
	// encoded-line bound; equals len([]rune(Archived.Content)) unless the
	// bound cut it.
	OriginalRunes int
	// EffectiveCap is the cap (runes) the result was measured against.
	EffectiveCap int
}

// admitToolResult is the choke point (FR-009). It filters, caps, clamps,
// bounds, archives and records state for one tool result, and returns the
// window form the producer appends to the in-memory slice. Persistence is
// done here (the producers no longer call AddFullMessage for tool messages)
// so the archive line the mark cites is the line the content actually took.
func (al *AgentLoop) admitToolResult(ts *turnState, adm toolResultAdmission) admittedToolResult {
	cfg := al.GetConfig()
	content := adm.Content
	var cs config.ContextSettings
	if cfg != nil {
		// FR-013 / B-16: filter the FULL content first; the archive holds
		// the filtered copy, the cut never straddles a secret.
		content = cfg.FilterSensitiveData(content)
		cs = cfg.Context
	}
	budget := 0
	if ts != nil && ts.agent != nil {
		budget = agentContextBudget(ts.agent)
	}
	policy := capPolicyFor(cs, budget)
	surface := toolResultSurfaceFor(adm.Tool, adm.IsError)
	parallelN := max(1, adm.ParallelN)
	capChars := policy.effectiveCap(surface, parallelN)

	archived := toolResultMessage(adm.ToolCallID, content, adm.Media)
	archived, origRunes, lineCut, lineOverflow := boundArchivedMessage(archived)
	sessionKey := ""
	if ts != nil {
		sessionKey = ts.sessionKey
	}
	if lineOverflow {
		// Unreachable once Content and Media have both been exhausted, but
		// a line that STILL does not fit must never be appended: a line over
		// the store's scanner limit makes every later read of the session
		// fail and GetHistory answer with an empty slice — the whole history
		// gone from the agent's point of view, permanently. Refuse the
		// archive and tell the model what happened instead.
		logger.ErrorCF("agent", "tool result cannot be archived within the encoded line bound; not archived",
			map[string]any{
				"tool":           adm.Tool,
				"tool_call_id":   adm.ToolCallID,
				"session_key":    sessionKey,
				"original_chars": origRunes,
				"encoded_len":    encodedArchiveLineLen(archived),
				"line_bound":     memory.EncodedLineBound,
			})
		notice := toolResultMessage(adm.ToolCallID,
			"[tool result discarded: it could not be stored within the archive line bound "+
				"and was dropped to keep this session readable]", nil)
		return admittedToolResult{
			Message:       notice,
			Archived:      notice,
			ArchiveLine:   -1,
			OriginalRunes: origRunes,
			EffectiveCap:  capChars,
		}
	}
	if lineCut {
		logger.WarnCF("agent", "tool result cut to the encoded archive-line bound (ADR-066 FR-012)",
			map[string]any{
				"tool":           adm.Tool,
				"tool_call_id":   adm.ToolCallID,
				"session_key":    sessionKey,
				"original_chars": origRunes,
				"archived_chars": utf8.RuneCountInString(archived.Content),
				"line_bound":     memory.EncodedLineBound,
			})
	}

	size := utf8.RuneCountInString(archived.Content)
	overCap := size > capChars
	if !overCap && size > policy.WarnThreshold {
		toolResultLargeTotal.Add(1)
		logger.WarnCF("agent", "tool result over the warn threshold (admitted unmodified)",
			map[string]any{
				"tool":           adm.Tool,
				"tool_call_id":   adm.ToolCallID,
				"session_key":    sessionKey,
				"chars":          size,
				"warn_threshold": policy.WarnThreshold,
				"cap":            capChars,
				"surface":        string(surface),
			})
	}

	// Archive the full filtered content (US-3.AC5). The archive read that
	// follows is needed only on the capped path — the mark cites the line
	// and the turn number, both of which only the archive knows.
	line := -1
	var archive []memory.ArchivedMessage
	persist := ts != nil && ts.agent != nil && ts.agent.Sessions != nil && !ts.opts.NoHistory
	if persist {
		ts.agent.Sessions.AddFullMessage(ts.sessionKey, archived)
		if overCap {
			if read, err := ts.agent.Sessions.ReadArchive(context.Background(), ts.sessionKey); err == nil && len(read) > 0 {
				archive = read
				line = len(read) - 1
			} else if err != nil {
				logger.WarnCF("agent", "choke point: archive read after append failed; cap mark cites no line",
					map[string]any{"session_key": ts.sessionKey, "tool_call_id": adm.ToolCallID, "error": err.Error()})
			}
		}
	}

	window := archived
	if overCap {
		window.Content = cutHeadAndTail(archived.Content, capChars, capMarkOrEmpty(adm.Tool, adm.ToolCallID, line, archived.Content, archive))
		if persist && line >= 0 {
			// Record WHICH surface produced the live cut: the window carries
			// no IsError, so without this the reload re-cuts a failure at
			// the success cap (FR-019 / B-22 byte identity).
			cappedState := memory.ProjectionCapped
			if adm.IsError {
				cappedState = memory.ProjectionCappedFailure
			}
			ts.agent.Sessions.SetProjectionState(ts.sessionKey,
				memory.ProjectionKey{ToolCallID: adm.ToolCallID, ArchiveLine: line}, cappedState)
		}
		logger.InfoCF("agent", "tool result capped at the door (ADR-066 D4)",
			map[string]any{
				"tool":          adm.Tool,
				"tool_call_id":  adm.ToolCallID,
				"session_key":   sessionKey,
				"surface":       string(surface),
				"chars":         size,
				"cap":           capChars,
				"parallel_n":    parallelN,
				"budget_tokens": budget,
				"archive_line":  line,
			})
	}

	return admittedToolResult{
		Message:       window,
		Archived:      archived,
		Capped:        overCap,
		ArchiveLine:   line,
		OriginalRunes: origRunes,
		EffectiveCap:  capChars,
	}
}

// capMarkOrEmpty builds the capped mark; a marshal failure (already reported
// by buildRecallMark) degrades to an empty mark so the result is still cut
// to the cap — bounded first, annotated second.
func capMarkOrEmpty(tool, toolCallID string, line int, content string, archive []memory.ArchivedMessage) string {
	mark, err := buildRecallMark("capped", tool, toolCallID, line, content, archive)
	if err != nil {
		return ""
	}
	return mark
}

// toolResultMessage is the ONE constructor of a role:"tool" provider message
// in this package (repair.go's exempt placeholder aside). Producers never
// build the literal themselves — that is what TestChokePoint_ProducerListByGrep
// pins.
func toolResultMessage(toolCallID, content string, media []string) providers.Message {
	return providers.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
		Media:      media,
	}
}
