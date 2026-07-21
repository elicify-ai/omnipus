// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// taskStatusLabel / taskSummaryLabel are the single source of truth for the
// ADR-043 marker vocabulary. Both the parser regexes below AND
// TaskExecutor.buildPrompt's instruction strings (task_executor.go) are built
// from these two constants so the label text can never drift out of sync
// between "what we ask the model to emit" and "what we actually parse".
const (
	taskStatusLabel  = "TASK_STATUS"
	taskSummaryLabel = "TASK_SUMMARY"
)

// taskStatusLineRe matches a TASK_STATUS marker anywhere a line contains one
// (the `$` end-of-line anchor was deliberately dropped — see the review note
// below). Case-insensitive; tolerant of:
//   - markdown emphasis wrapping the whole line, just the label, just the
//     colon, or just the value (`**TASK_STATUS: success**`, “ `TASK_STATUS:
//     failure` “, `**TASK_STATUS**: success`, `TASK_STATUS: **success**`);
//   - trailing punctuation or short trailing prose after the value
//     (`TASK_STATUS: success.`, `TASK_STATUS: success — all tests pass`).
//
// The captured group is anchored immediately after the label+colon+wrapper —
// it is always the FIRST success|failure token on the line, so trailing
// prose that happens to mention the other status word never changes the
// captured value (`TASK_STATUS: success but almost failure occurred` still
// captures "success"). The trailing `\b` word boundary prevents a value like
// "successful" from being misread as "success" with garbage appended.
//
// A value other than success/failure (a hedge like "TASK_STATUS: maybe") does
// not match at all — it is simply not a recognized signal, exactly as before.
//
// This regex alone is deliberately NOT sufficient to decide "is this line a
// genuine marker line" — its `[*`_\s]*` wrapper class happily swallows a
// bullet's leading whitespace and a 4-space code-block indent too, since both
// are just more whitespace to the regex engine. isExcludedMarkerLine (below)
// is a separate, pre-regex gate every candidate line is run through first;
// see its doc comment for why that split (rather than growing this regex) was
// the chosen fix (hardening review, 2026-07-13).
var taskStatusLineRe = regexp.MustCompile(
	"(?i)^[*`_\\s]*" + taskStatusLabel + "[*`_\\s]*:[*`_\\s]*(success|failure)\\b",
)

// taskSummaryLineRe matches a TASK_SUMMARY marker line. Only the leading
// wrapper before the label and any wrapper between the label and the colon
// are stripped — the summary text itself is captured verbatim (trailing
// content is free-form and must not be mangled). Like taskStatusLineRe, a
// candidate line is expected to have already passed isExcludedMarkerLine
// before being tested against this regex.
var taskSummaryLineRe = regexp.MustCompile(
	"(?i)^[*`_\\s]*" + taskSummaryLabel + "[*`_\\s]*:\\s*(.*)$",
)

// bulletOrNumberedListPrefixRe matches a markdown bullet (`* `, `- `, `+ `) or
// numbered-list (`1. `, `12. `, ...) prefix at the start of a string — always
// applied to a line with any leading spaces already stripped (see
// isExcludedMarkerLine). Deliberately narrow: a bullet requires the marker
// character to be followed by WHITESPACE. That is what distinguishes a bullet
// from markdown emphasis:
//   - `* TASK_STATUS: success` — `*` then a space: a bullet. Excluded.
//   - `*TASK_STATUS: success*` — `*` then a letter: italic emphasis, no
//     bullet. Still matches taskStatusLineRe normally.
//   - `**TASK_STATUS**: success` — `*` then another `*` (bold), not
//     whitespace: not a bullet either. Still matches normally.
//
// This is implemented as a pre-check run before taskStatusLineRe/
// taskSummaryLineRe (isExcludedMarkerLine) rather than folded into those
// regexes — trying to teach the marker regex itself "don't match a bullet
// but do match bold" makes an already-dense character-class regex far harder
// to read and verify; a small, separately-named, separately-tested predicate
// is clearer (hardening review finding H2, 2026-07-13).
var bulletOrNumberedListPrefixRe = regexp.MustCompile(`^(?:[*\-+]\s+|\d+\.\s+)`)

// isExcludedMarkerLine reports whether line (already stripped of a trailing
// \r, but otherwise raw — leading whitespace intact) must never be treated as
// a genuine TASK_STATUS/TASK_SUMMARY marker line, independent of
// fence-awareness (computeFencedLines). Two cases, both hardening-review
// findings (2026-07-13):
//
//   - Indented code block (finding H3): markdown treats a line with a
//     leading tab, or 4-or-more leading spaces, as an indented code block —
//     a marker a model shows purely as a formatting example this way has no
//     more claim to being a real signal than one inside a fenced block.
//     Genuine markers have no reason to be indented that deeply; a 2-space
//     (or less) indent is common enough (buildPrompt's own instruction text
//     uses exactly 2 spaces, task_executor.go) that it must still match, so
//     the threshold is deliberately 4, not 1.
//   - Bullet/numbered-list prefix (finding H2): a model paraphrasing the
//     TASK_STATUS/TASK_STATUS instruction pair as a bulleted list (e.g.
//     summarizing "here's what I'll report" as `* TASK_STATUS: success` /
//     `* TASK_STATUS: failure`) is quoting the vocabulary, not emitting it —
//     the leading list-item marker is checked via
//     bulletOrNumberedListPrefixRe against the line with its leading spaces
//     removed (a nested/indented bullet like "  * TASK_STATUS: success" is
//     still a bullet).
func isExcludedMarkerLine(line string) bool {
	if strings.HasPrefix(line, "\t") {
		return true // any leading tab reads as an indented code block
	}
	leadingSpaces := 0
	for _, r := range line {
		if r != ' ' {
			break
		}
		leadingSpaces++
	}
	if leadingSpaces >= 4 {
		return true // markdown indented-code-block threshold
	}
	return bulletOrNumberedListPrefixRe.MatchString(line[leadingSpaces:])
}

// completionVerdict is the tri-state outcome of scanning agent output for the
// ADR-043 TASK_STATUS marker. Replaces a previous Found-bool/Success-bool
// field pair that allowed a meaningless Found=false,Success=true combination
// to be constructed — the tri-state makes that invalid state unrepresentable.
type completionVerdict int

const (
	// verdictNotFound means no parseable TASK_STATUS line was located
	// anywhere in the output. Callers MUST fail the task closed.
	verdictNotFound completionVerdict = iota
	// verdictSuccess means the last (non-fenced) TASK_STATUS line found was
	// "success".
	verdictSuccess
	// verdictFailure means the last (non-fenced) TASK_STATUS line found was
	// "failure".
	verdictFailure
)

// taskCompletionSignal is the parsed result of scanning an agent's raw task
// output for the standardized completion marker (ADR-043 — see
// docs/internal/architecture/ADR-043-task-completion-contract.md).
type taskCompletionSignal struct {
	// Verdict is the tri-state outcome. Only Result below is meaningful when
	// Verdict != verdictNotFound.
	Verdict completionVerdict
	// Result is the text to persist as the task's Result: the TASK_SUMMARY
	// text (plus any following lines) when a summary follows the marker, or
	// the full raw output when no summary is present — bounded to
	// maxFoundSignalResultChars either way (see truncateFoundSignalOutput).
	// Only meaningful when Verdict != verdictNotFound.
	Result string
}

// Found reports whether a TASK_STATUS marker was located at all
// (Verdict != verdictNotFound).
func (s taskCompletionSignal) Found() bool {
	return s.Verdict != verdictNotFound
}

// Status maps a found verdict to the task package's terminal status:
// verdictSuccess -> task.StatusDone, verdictFailure -> task.StatusFailed.
// Only meaningful when Found() is true — callers must check Found() first;
// this method does not attempt to synthesize a status for verdictNotFound
// (finishTaskRun's own fail-closed branch decides that case explicitly,
// without going through this method).
func (s taskCompletionSignal) Status() task.Status {
	if s.Verdict == verdictSuccess {
		return task.StatusDone
	}
	return task.StatusFailed
}

// computeFencedLines returns a per-line boolean marking which lines fall
// inside (or are themselves) a ``` / ~~~ fenced code block, so
// parseTaskCompletionSignal can ignore a marker a model quotes purely as a
// formatting example — e.g. "I'll report like this: ```TASK_STATUS:
// success```" while the surrounding prose says it is still only 60% done —
// rather than treating the quoted example as the real signal (ADR-043 §2.2,
// review finding A1). The fence delimiter line itself is marked fenced (true)
// so it can never match either marker regex; everything strictly between an
// opening and closing delimiter is fenced too. An unterminated trailing fence
// (no closing delimiter) fences everything through the end of the output —
// the conservative, fail-closed choice.
//
// Fence-type matching (hardening review finding H1, 2026-07-13): CommonMark
// requires a fence's closing delimiter to be the SAME character (``` or ~~~)
// as its opener — a ``` block is not closed by a ~~~ line, and vice versa.
// The original implementation toggled a single inFence bool on EITHER
// delimiter type, so "```\nexample\n~~~\nTASK_STATUS: success\n```" wrongly
// let the ~~~ line close the ``` block, un-fencing the marker line early. The
// fix tracks WHICH delimiter opened the current fence (fenceDelim, "" when
// not fenced): only a line of that same type closes it; a line of the OTHER
// delimiter type while already fenced is just fenced content (it neither
// opens a nested fence nor closes the current one).
func computeFencedLines(lines []string) []bool {
	fenced := make([]bool, len(lines))
	fenceDelim := "" // "" = not in a fence; otherwise "```" or "~~~" — the type that opened the current fence
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		var delim string
		switch {
		case strings.HasPrefix(trimmed, "```"):
			delim = "```"
		case strings.HasPrefix(trimmed, "~~~"):
			delim = "~~~"
		}
		if delim != "" {
			fenced[i] = true // the delimiter line itself is never a marker line
			switch fenceDelim {
			case "":
				fenceDelim = delim // opening a new fence of this type
			case delim:
				fenceDelim = "" // matching type closes the fence
			default:
				// A different delimiter type while already fenced: fenced
				// content, not a fence boundary — leave fenceDelim as-is.
			}
			continue
		}
		fenced[i] = fenceDelim != ""
	}
	return fenced
}

// parseTaskCompletionSignal scans output for the LAST non-fenced TASK_STATUS
// marker line (models sometimes echo the instruction earlier in their own
// reasoning; the real signal is the one that comes last) and an optional
// TASK_SUMMARY line immediately following it.
//
// Contract (ADR-043, as revised by the review that added fence-awareness,
// trailing-content tolerance, and the result-size caps — see the ADR's
// changelog):
//   - A line containing `TASK_STATUS: success` or `TASK_STATUS: failure` —
//     case-insensitive, tolerant of surrounding markdown emphasis and
//     trailing punctuation/prose (see taskStatusLineRe's doc comment). A line
//     whose value is neither success nor failure (e.g. a hedge like
//     "TASK_STATUS: maybe") simply does not match — it is not a recognized
//     signal.
//   - Lines inside (or forming) a ``` / ~~~ fenced code block are ignored for
//     BOTH the status and summary matches — a model quoting the marker as a
//     formatting example does not count as a real signal. A fence's closing
//     delimiter must match its opener's type (``` closes only ```, ~~~ closes
//     only ~~~ — computeFencedLines' doc comment, hardening review H1); a
//     line of the other delimiter type while already fenced is just fenced
//     content, not a boundary.
//   - A line is also never treated as a marker line — independent of
//     fencing — when it is markdown-indented as a code block (a leading tab,
//     or 4-or-more leading spaces) or when it is bullet/numbered-list-prefixed
//     (`* `, `- `, `+ `, `1. `, ...) — see isExcludedMarkerLine's doc comment
//     (hardening review H2/H3). Bold emphasis (`**TASK_STATUS**: success`,
//     `*` immediately followed by another `*`, never by whitespace) is NOT a
//     bullet and still matches; a 1-3 space indent (buildPrompt's own
//     instruction lines use 2) still matches.
//   - Last non-fenced, non-excluded occurrence wins. If a model echoes the instruction
//     early in its own reasoning (a real, observed LLM behavior) and then
//     emits the real signal at the end, the trailing one governs. This also
//     means a genuine marker whose SUMMARY body later quotes an unfenced
//     standalone TASK_STATUS line will have its verdict flipped by that later
//     line — a known, accepted limitation (ADR-043 §3), not a bug: fence the
//     quote, or avoid embedding bare marker lines in prose, to avoid it.
//   - An optional following `TASK_SUMMARY: <text>` line. When present, the
//     summary text plus every subsequent line to the end of the output
//     becomes the task's Result (so scratch reasoning ahead of the marker
//     never leaks into the persisted Result). When absent, the full raw
//     output remains Result, marker line included.
//   - Empty (or whitespace-only) output, or no parseable TASK_STATUS line
//     anywhere in the output, yields Verdict == verdictNotFound — the caller
//     must treat this as fail-closed, never as an implicit success.
//   - The returned Result is bounded to maxFoundSignalResultChars runes
//     (truncateFoundSignalOutput) so a runaway response can't bloat the task
//     store even on a genuine completion.
func parseTaskCompletionSignal(output string) taskCompletionSignal {
	if strings.TrimSpace(output) == "" {
		return taskCompletionSignal{Verdict: verdictNotFound}
	}

	lines := strings.Split(output, "\n")
	fenced := computeFencedLines(lines)

	statusIdx := -1
	success := false
	for i, line := range lines {
		if fenced[i] {
			continue
		}
		trimmedR := strings.TrimRight(line, "\r")
		if isExcludedMarkerLine(trimmedR) {
			continue
		}
		m := taskStatusLineRe.FindStringSubmatch(trimmedR)
		if m == nil {
			continue
		}
		statusIdx = i
		success = strings.EqualFold(m[1], "success")
	}
	if statusIdx == -1 {
		return taskCompletionSignal{Verdict: verdictNotFound}
	}

	verdict := verdictFailure
	if success {
		verdict = verdictSuccess
	}

	result := output
	for j := statusIdx + 1; j < len(lines); j++ {
		if fenced[j] {
			continue
		}
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		trimmedR := strings.TrimRight(lines[j], "\r")
		if !isExcludedMarkerLine(trimmedR) {
			if m := taskSummaryLineRe.FindStringSubmatch(trimmedR); m != nil {
				trailing := make([]string, 0, len(lines)-(j+1))
				for _, l := range lines[j+1:] {
					// CRLF fix (review A4): strings.Split(output, "\n") on a
					// CRLF-terminated input leaves a literal trailing "\r" on
					// every folded line except possibly the very last — trim it
					// so Result never carries an embedded \r.
					trailing = append(trailing, strings.TrimRight(l, "\r"))
				}
				summaryLines := append([]string{m[1]}, trailing...)
				result = strings.TrimSpace(strings.Join(summaryLines, "\n"))
			}
		}
		break
	}

	return taskCompletionSignal{Verdict: verdict, Result: truncateFoundSignalOutput(result)}
}

// maxFailClosedOutputChars bounds the raw agent output embedded in a
// fail-closed task Result so a runaway response can't bloat the task store.
const maxFailClosedOutputChars = 2000

// maxFoundSignalResultChars bounds Result when a TASK_STATUS marker WAS found
// (success or failure) — generous relative to maxFailClosedOutputChars since
// a genuine completion's own reported words are expected, wanted content, but
// still bounded so a runaway response (e.g. a summary with unbounded folded
// trailing lines) can't bloat the task store indefinitely (ADR-043 §2.2).
const maxFoundSignalResultChars = 8000

// truncateTaskOutput bounds output to maxFailClosedOutputChars (rune-safe —
// see the doc note below), noting the truncation so an operator isn't misled
// into thinking the output just ended there naturally.
func truncateTaskOutput(output string) string {
	return truncateRunes(output, maxFailClosedOutputChars)
}

// truncateFoundSignalOutput bounds output to maxFoundSignalResultChars using
// the same rune-safe, truncation-noted shape as truncateTaskOutput.
func truncateFoundSignalOutput(output string) string {
	return truncateRunes(output, maxFoundSignalResultChars)
}

// truncateRunes bounds s to at most maxRunes runes, appending a truncation
// note when it actually cuts anything. Rune-safe (review A6): the previous
// implementation sliced by byte index (output[:n]), which can split a
// multi-byte UTF-8 rune mid-sequence and corrupt the tail of the truncated
// string. Slicing a []rune conversion instead always cuts on a rune boundary.
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "\n... (truncated, output continues)"
}

// --- ADR-052 evidence-marker gate (FR-035 / R3-13) ---
//
// This EXTENDS the ADR-043 marker family defined above (TASK_STATUS /
// TASK_SUMMARY) with a second marker in the SAME family — `[goal:evidence]`
// — rather than inventing a second, parallel completion-signal protocol
// (grill finding R3-13, explicitly called out in ADR-052's "Evidence-marker
// gate" paragraph, adapted from the opencode `willytop8/OpenCode-goal-plugin`
// prior art cited there). A completion claim (a found TASK_STATUS marker
// line, success OR failure — the gate does not care which) is only HONORED
// when a non-empty `[goal:evidence] <what was verified>` line immediately
// precedes it. A bare claim — no evidence line, or an evidence line whose
// text is empty/whitespace-only — is REJECTED: callers must re-prompt the
// worker with SteeringText BEFORE any verifier dispatch (spec US-13
// Acceptance 5 / DS-8 "evidence-free completion claim").
//
// checkEvidenceMarkerGate deliberately does NOT reuse or mutate
// parseTaskCompletionSignal's return value or internals — it re-locates the
// completion marker line itself (via the same shared, already-hardened
// primitives: computeFencedLines, isExcludedMarkerLine, taskStatusLineRe) so
// the two functions can never disagree about fencing/exclusion semantics,
// while keeping parseTaskCompletionSignal's existing behavior and the 27
// pre-existing tests around it completely unchanged. This gate IS wired into
// TaskExecutor's dispatch path: TaskExecutor.finishTaskRun (task_executor.go)
// calls it before parseTaskCompletionSignal and, on Applicable && !Honored,
// re-prompts via rejectBareEvidenceClaim (which also owns the
// evidenceGateMaxConsecutiveRejections bound and its Warn logging) — this
// file only supplies the primitive.

// goalEvidenceLabel is the ADR-052 evidence-marker token. Case-insensitive at
// match time (goalEvidenceLineRe), like the rest of the ADR-043 vocabulary.
const goalEvidenceLabel = "[goal:evidence]"

// goalEvidenceLineRe matches a `[goal:evidence] <text>` line, tolerant of the
// same markdown-emphasis wrapping taskStatusLineRe/taskSummaryLineRe
// tolerate (bold, italic, or code-span around the label; an optional colon
// after the bracket). The captured group is the trailing text, which the
// caller must still trim and check for emptiness — a `[goal:evidence]` line
// with no (or whitespace-only) trailing text is a bare label, not evidence.
var goalEvidenceLineRe = regexp.MustCompile(
	"(?i)^[*`_\\s]*\\[goal:evidence\\][*`_\\s]*:?[*`_\\s]*(.*)$",
)

// evidenceGateVerdict is the result of checkEvidenceMarkerGate.
type evidenceGateVerdict struct {
	// Applicable is false when the output contains no completion marker at
	// all (parseTaskCompletionSignal's own verdictNotFound fail-closed path
	// already handles that case on its own terms — the evidence gate has
	// nothing additional to say when there is no claim to gate).
	Applicable bool
	// Honored is only meaningful when Applicable is true: true when a
	// genuine (non-fenced, non-excluded), non-empty [goal:evidence] line
	// immediately precedes the completion marker line.
	Honored bool
	// SteeringText is the re-prompt text to feed back to the worker when
	// Applicable && !Honored. Empty otherwise.
	SteeringText string
}

// evidenceGateSteeringText is the FR-035 re-prompt: verify first, then place
// the evidence marker immediately before the completion marker, before
// restating it.
const evidenceGateSteeringText = "completion claim rejected: no \"" + goalEvidenceLabel +
	" <what you verified>\" line was found immediately before the completion marker (" +
	taskStatusLabel + "). Verify your work first, then put \"" + goalEvidenceLabel +
	" <what you verified>\" immediately before the completion marker, then restate " +
	taskStatusLabel + "."

// checkEvidenceMarkerGate scans output for the LAST valid (non-fenced,
// non-excluded) TASK_STATUS completion marker line — the same "last
// occurrence wins" rule parseTaskCompletionSignal applies — and reports
// whether a genuine [goal:evidence] line immediately precedes it (ADR-052
// FR-035).
//
// "Immediately precedes" tolerates intervening BLANK lines (mirroring
// parseTaskCompletionSignal's own tolerance of blank lines between the
// TASK_STATUS marker and a following TASK_SUMMARY line) but nothing else:
// the nearest non-blank line above the marker must itself be a non-fenced,
// non-excluded [goal:evidence] line with non-empty trailing text. A fenced
// (quoted-as-example) or excluded (bulleted/code-indented — see
// isExcludedMarkerLine) candidate line does not count as evidence, exactly
// as fenced/excluded lines never count as a genuine TASK_STATUS/TASK_SUMMARY
// line either — quoting the vocabulary is not emitting it.
func checkEvidenceMarkerGate(output string) evidenceGateVerdict {
	if strings.TrimSpace(output) == "" {
		return evidenceGateVerdict{Applicable: false}
	}

	lines := strings.Split(output, "\n")
	fenced := computeFencedLines(lines)

	statusIdx := -1
	for i, line := range lines {
		if fenced[i] {
			continue
		}
		trimmedR := strings.TrimRight(line, "\r")
		if isExcludedMarkerLine(trimmedR) {
			continue
		}
		if taskStatusLineRe.MatchString(trimmedR) {
			statusIdx = i // last non-fenced, non-excluded match wins
		}
	}
	if statusIdx == -1 {
		return evidenceGateVerdict{Applicable: false}
	}

	for j := statusIdx - 1; j >= 0; j-- {
		trimmedR := strings.TrimRight(lines[j], "\r")
		if strings.TrimSpace(trimmedR) == "" {
			continue // blank lines are tolerated between evidence and marker
		}
		if fenced[j] || isExcludedMarkerLine(trimmedR) {
			return evidenceGateVerdict{
				Applicable: true, Honored: false, SteeringText: evidenceGateSteeringText,
			}
		}
		m := goalEvidenceLineRe.FindStringSubmatch(trimmedR)
		if m == nil || strings.TrimSpace(m[1]) == "" {
			return evidenceGateVerdict{
				Applicable: true, Honored: false, SteeringText: evidenceGateSteeringText,
			}
		}
		return evidenceGateVerdict{Applicable: true, Honored: true}
	}

	// No non-blank line at all before the marker (it's the first line of
	// output, or everything above it was blank) — nothing to have been
	// evidence, so the claim is bare.
	return evidenceGateVerdict{Applicable: true, Honored: false, SteeringText: evidenceGateSteeringText}
}
