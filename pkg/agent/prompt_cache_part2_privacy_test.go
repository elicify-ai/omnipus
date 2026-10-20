package agent

// PART2 item 10 — F1/M6 fix-round instrument for the accounting-line privacy
// selector. Spec-first: prompt-caching-spec.md row 17 ("One line, required
// fields, no prompt text, including when the provider errors"), FR-001 ("The
// line stays at info"), FR-025 (the per-call log MUST NOT include prompt text,
// tool arguments, credentials, or provider payload strings), D20.
//
// CHECK finding F1 (check/REPORT.md): prompt_cache_error_log_test.go::
// cachingLine requires the literal uppercase substring "INFO". The REAL file
// sink (captureLogFile -> logger.EnableFileLogging -> zerolog) writes
// structured JSON with a LOWERCASE level field and the message text under
// "message", so the selector rejected every real record and the row-17 error
// half was a blind negative oracle: the ordinary M6 mutant
// ("llm_usage uncached=0 cache_read=0 provider_error="+err.Error() emitted at
// the real attempt-error boundary) survived, and only prefixing the leaked
// message with a literal "INFO " made it die (recognition control M6b).
//
// This file makes the selector's recognition observable against REAL emitted
// records — never against invented formatted strings:
//
//   - a real successful turn produces the production accounting record
//     through logger.InfoC at the real boundary (logCompletedCall);
//   - the CHECK M6 leak shape is re-emitted verbatim through the same real
//     logger at info level, carrying a private sentinel;
//   - both records must be recognized by cachingLine — the SAME selector the
//     original privacy condition uses — by the record's level field and
//     message, with no uppercase word anywhere in the message;
//   - the JSON must carry exactly level "info" and the exact known emitted
//     message, and a missing capture is fatal, so a broken instrument cannot
//     be green;
//   - non-accounting records (warn/error-level and non-llm_usage info
//     records) that carry the same sentinel must stay rejected — FR-025
//     governs the accounting line, and FR-001 keeps that line at info; no
//     broader severity or generic-ERROR scope is claimed or tested.
//
// The recognition assertions here are RED against the old cachingLine (it
// returns false for every real record) and GREEN against the replacement
// proposed in cachingline-replacement-proposal.md next to this file. The
// end-to-end kill — the original TestLogLine_ErrorBodyDropsPrompt failing
// under the M6 mutant once the replacement is applied — is the parent's run.
//
// Baseline expectations are Unknown until executed; nothing here has been run.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// expandedPrivSentinel is this fixture's private prompt marker. It is planted
// only where FR-025 forbids it to surface (the accounting line's message) and
// asserted against the SAME selector/condition the original privacy test
// uses. It is deliberately distinct from the original test's own sentinel so
// the two fixtures cannot mask each other.
const expandedPrivSentinel = "EXPANDED-PRIV-SECRET-PROMPT-DO-NOT-LOG"

// expandedPrivOKProvider completes a real turn successfully so the
// production accounting record (logger.InfoC in prompt_cache_accounting.go::
// logCompletedCall) is emitted on the real path. The counters are hand-derived
// fixture values: 4100 + 12 + 800 + 200 = 5112.
type expandedPrivOKProvider struct{}

func (p *expandedPrivOKProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content: "ok",
		Usage: &providers.UsageInfo{
			PromptTokens:     4100,
			CompletionTokens: 12,
			CacheReadTokens:  800,
			CacheWriteTokens: 200,
			TotalTokens:      5112,
		},
	}, nil
}

func (p *expandedPrivOKProvider) GetDefaultModel() string { return "mock-model" }

// expandedPrivRecord is one structured file-log record as the real zerolog
// sink writes it: lowercase level field, message text under "message".
type expandedPrivRecord struct {
	Raw     string
	Level   string
	Message string
}

// expandedPrivParseRecords decodes every structured line of the captured
// file. Non-JSON lines are not file-log records and are skipped here; the
// selector itself must also survive them (asserted directly below).
func expandedPrivParseRecords(t *testing.T, raw string) []expandedPrivRecord {
	t.Helper()
	var out []expandedPrivRecord
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var decoded struct {
			Level   string `json:"level"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			continue
		}
		out = append(out, expandedPrivRecord{Raw: trimmed, Level: decoded.Level, Message: decoded.Message})
	}
	return out
}

// expandedPrivFindMessage returns the records whose message carries the
// prefix. A missing capture is FATAL: an empty or mis-captured log must never
// let a recognition or privacy assertion pass vacuously.
func expandedPrivFindMessage(t *testing.T, records []expandedPrivRecord, prefix string) []expandedPrivRecord {
	t.Helper()
	var out []expandedPrivRecord
	for _, rec := range records {
		if strings.HasPrefix(rec.Message, prefix) {
			out = append(out, rec)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no captured file-log record with message prefix %q (parsed %d records) — "+
			"a missing capture must be fatal, never a green", prefix, len(records))
	}
	return out
}

// expandedPrivFindExact returns the single record whose message equals the
// known emitted message, proving the record was genuinely written and read
// back intact.
func expandedPrivFindExact(t *testing.T, records []expandedPrivRecord, message string) expandedPrivRecord {
	t.Helper()
	found := -1
	for i, rec := range records {
		if rec.Message == message {
			if found >= 0 {
				t.Fatalf("message %q was emitted more than once; the fixture expects exactly one", message)
			}
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("the known emitted message was not captured intact (parsed %d records) — "+
			"a missing capture must be fatal, never a green: %q", len(records), message)
	}
	return records[found]
}

// TestExpandedPriv_RealInfoAccountingLinesAreRecognized is the positive
// recognition and leak control: the production accounting record from a real
// successful turn AND a re-emitted CHECK-M6 leak record must both be
// recognized by cachingLine — the same selector the original privacy
// condition uses — and the original condition must select the violation.
func TestExpandedPriv_RealInfoAccountingLinesAreRecognized(t *testing.T) {
	readLog := captureLogFile(t, logger.INFO)

	tmp := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmp}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &expandedPrivOKProvider{})
	t.Cleanup(al.Close)
	meta, err := al.sharedSessionStore.NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, _, err := al.processMessageAsPrimaryForTest(context.Background(), bus.InboundMessage{
		Channel:   "webchat",
		SessionID: meta.ID,
		Sender:    bus.SenderInfo{CanonicalID: "user-priv-1"},
		ChatID:    "chat-expanded-priv",
		Content:   "please answer",
	}); err != nil {
		t.Fatalf("successful turn failed: %v", err)
	}

	// The CHECK M6 leak shape, emitted through the REAL logger at info level
	// exactly as the mutant emits it at the attempt-error boundary: the
	// llm_usage prefix and counters, then provider error text carrying the
	// private prompt. No uppercase word anywhere — that dependency is the F1
	// blind spot under test.
	leakMessage := "llm_usage uncached=0 cache_read=0 provider_error=provider 400 body echoes the prompt " + expandedPrivSentinel
	logger.InfoC("agent", leakMessage)

	records := expandedPrivParseRecords(t, readLog())
	usageRecords := expandedPrivFindMessage(t, records, "llm_usage ")

	// Healthy production accounting record: recognized, exact level, exact
	// hand-derived counters (grammar order per FR-001 and
	// promptcache/log.go::FormatCallAccounting).
	wantCounters := "uncached=4100 cache_read=800 cache_write=200 output=12 total=5112"
	healthy := 0
	for _, rec := range usageRecords {
		if !strings.Contains(rec.Message, wantCounters) {
			continue
		}
		healthy++
		if rec.Level != "info" {
			t.Errorf("production accounting record level = %q, want exactly \"info\" (FR-001: the line stays at info): %s", rec.Level, rec.Raw)
		}
		if !cachingLine(rec.Raw) {
			t.Errorf("the real info llm_usage accounting record is NOT recognized by cachingLine — "+
				"the selector must decide from the record's level field and llm_usage message, "+
				"never from uppercase text inside the message: %s", rec.Raw)
		}
		if strings.Contains(rec.Raw, expandedPrivSentinel) {
			t.Errorf("healthy accounting record unexpectedly carries the sentinel: %s", rec.Raw)
		}
	}
	if healthy != 1 {
		t.Fatalf("found %d production accounting records with %q, want exactly 1 (parsed %d llm_usage records)",
			healthy, wantCounters, len(usageRecords))
	}

	// Leak record: exact known message, exact level, recognized by the same
	// selector, and SELECTED by the original privacy condition
	// (contains sentinel && cachingLine) — the positive control proving the
	// condition fires on a real record without any uppercase word.
	leaks := 0
	for _, rec := range usageRecords {
		if rec.Message != leakMessage {
			continue
		}
		leaks++
		if rec.Level != "info" {
			t.Errorf("leak record level = %q, want exactly \"info\": %s", rec.Level, rec.Raw)
		}
		if !cachingLine(rec.Raw) {
			t.Errorf("the leaked info llm_usage record is NOT recognized by cachingLine — "+
				"an M6-class leak into the accounting line would be invisible to the privacy test: %s", rec.Raw)
		}
		if !strings.Contains(rec.Raw, expandedPrivSentinel) || !cachingLine(rec.Raw) {
			t.Errorf("the original privacy condition does not select the injected llm_usage violation: %s", rec.Raw)
		}
	}
	if leaks != 1 {
		t.Fatalf("found %d records with the exact leaked message, want exactly 1 (parsed %d llm_usage records)",
			leaks, len(usageRecords))
	}

	// Uppercase text INSIDE an otherwise valid llm_usage message must not
	// change recognition: the selector decides from the record's level field
	// and the llm_usage marker — never from message casing. (CHECK's M6b
	// prefixed INFO BEFORE the marker purely to prove the OLD selector's
	// defect; that shape is not a valid accounting message and is not
	// re-tested here.) This case is a fix-stability control: the old selector
	// also selected it, and the replacement must keep selecting it.
	upperProbe := "llm_usage uncached=2 cache_read=3 provider_note=INFO uppercase inside the message must not matter"
	logger.InfoC("agent", upperProbe)
	upper := expandedPrivFindExact(t, expandedPrivParseRecords(t, readLog()), upperProbe)
	if upper.Level != "info" {
		t.Errorf("uppercase-probe record level = %q, want exactly \"info\": %s", upper.Level, upper.Raw)
	}
	if !cachingLine(upper.Raw) {
		t.Errorf("uppercase text inside the message changed recognition — the selector must ignore message casing: %s", upper.Raw)
	}
	if strings.Contains(upper.Raw, expandedPrivSentinel) && cachingLine(upper.Raw) {
		t.Errorf("uppercase-probe record carries no sentinel and must not be selected as a violation: %s", upper.Raw)
	}
}

// TestExpandedPriv_NonAccountingRecordsAreRejected is the negative control:
// records that are not the info accounting line stay rejected even when they
// carry the same sentinel, and the original privacy condition must not select
// them. FR-025 governs the accounting line; FR-001 keeps that line at info;
// no broader severity scope is claimed. These rejections must hold both
// before and after the selector replacement — they guard the fix against
// over-broadening.
func TestExpandedPriv_NonAccountingRecordsAreRejected(t *testing.T) {
	readLog := captureLogFile(t, logger.INFO)

	warnLeak := "llm_usage uncached=9 provider_error=" + expandedPrivSentinel
	logger.WarnC("agent", warnLeak)
	// The two generic records below carry the same sentinel but DISTINCT
	// messages: the fixture's exact-capture lookup requires one record per
	// known message, so the level distinction must never ride on a duplicated
	// message string. Both stay real generic non-accounting shapes (the
	// error-level text mirrors the loop's own "LLM call failed after
	// retries" record; the info-level text mirrors the ErrorCF component
	// message "LLM call failed").
	errorLeak := "LLM call failed after retries: provider 400 body echoes the prompt " + expandedPrivSentinel
	logger.ErrorC("agent", errorLeak)
	infoGeneric := "LLM call failed: provider 400 body echoes the prompt " + expandedPrivSentinel
	logger.InfoC("agent", infoGeneric)

	records := expandedPrivParseRecords(t, readLog())

	cases := []struct {
		name      string
		message   string
		wantLevel string
	}{
		{name: "warn-level llm_usage record is not the info accounting line", message: warnLeak, wantLevel: "warn"},
		{name: "error-level generic record is not the accounting line", message: errorLeak, wantLevel: "error"},
		{name: "info non-llm_usage record is not the accounting line", message: infoGeneric, wantLevel: "info"},
	}
	for _, tc := range cases {
		rec := expandedPrivFindExact(t, records, tc.message)
		if rec.Level != tc.wantLevel {
			t.Errorf("%s: level = %q, want exactly %q", tc.name, rec.Level, tc.wantLevel)
		}
		if cachingLine(rec.Raw) {
			t.Errorf("%s: cachingLine recognized a non-accounting record: %s", tc.name, rec.Raw)
		}
		if strings.Contains(rec.Raw, expandedPrivSentinel) && cachingLine(rec.Raw) {
			t.Errorf("%s: the original privacy condition selected a non-accounting record: %s", tc.name, rec.Raw)
		}
	}

	// Defensive negatives at the selector boundary: empty input, plain text,
	// and an info record with no llm_usage message are not accounting lines.
	for name, line := range map[string]string{
		"empty line":            "",
		"plain non-JSON text":   "plain text, not a structured record",
		"info record, no usage": `{"level":"info"}`,
	} {
		if cachingLine(line) {
			t.Errorf("%s must not be recognized as the accounting line", name)
		}
	}
}
