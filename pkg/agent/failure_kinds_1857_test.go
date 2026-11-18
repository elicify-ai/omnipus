// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// failure_kinds_1857_test.go — #1857 RED tests, wording-independent half.
//
// Spec: docs/internal/specs/notifications-spec.md @ 3748fd637a, §13.8
// (§13.8.2 failure-kind list, §13.8.4 surfaces, §13.8.5 disposition matrix),
// FR-092, FR-093, test rows T-52…T-56. Founder answers 2026-11-18: E1 B,
// E2 A, E3 A, sec2 A applied; E4 A keeps every exact-sentence copy OUT of
// scope (TODO(#1857 E4) markers below).
//
// The implementation (WP-0: the CLI-only classifier, the new codes as
// contract data, the sec2 re-keys) does not exist yet. New codes are
// referenced as string literals so this package compiles today; every test
// below must FAIL on the current tree for the RIGHT reason (wrong code /
// raw text present / generic result where a typed one is promised), never
// on a compile error. Oracles come from the spec rows, never from running
// the code.
//
// RED discipline (elicify-test-writing): this file is written before the
// implementation; the mutation checks and green runs are CHECK's job (a
// different instance), not RED's.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fixture vocabulary — the C-row shapes are the drivers' verbatim emission
// forms (read in this sweep: driver_claude.go's post-cmd.Wait exit path,
// driver_claude.go's result handling, driver_stream.go's streamParser,
// driver_claude/codex/opencode.go's turn-cap path, and the OpenCode nested
// error envelope named in §13.8.2 C2). Codes are the §13.8.2 "Proposed code"
// column — contract data WP-0 adds, referenced here as string literals only.
// ─────────────────────────────────────────────────────────────────────────────

// 1857C1Usage is C1's stderr-tail shape: a rejected command line (unknown
// option), delivered as "<cli> exited: <err>\nstderr:\n<tail>".
const test1857C1Usage = "claude exited: exit status 1\nstderr:\nerror: unknown option `--model'"

// test1857C1DuplicateFlag is the Mac team's case (§13.8.2 C1 / wording row W1):
// a duplicated --model flag passed via the worker profile's Additional CLI
// arguments — a usage error before any work ran.
const test1857C1DuplicateFlag = "codex exited: exit status 2\nstderr:\nerror: the argument '--model <model>' cannot be used multiple times"

// test1857C2Envelope is C2's OpenCode nested error envelope: the structured
// error.type (provider.no-route) that today's parseLine loses.
const test1857C2Envelope = `{"type":"error","error":{"type":"provider.no-route","message":"Model unavailable: no route found for the requested model"}}`

// test1857C3Signin is C3's Claude result shape: sign-in failure on the stream
// result, not stderr.
const test1857C3Signin = "Not logged in · Please run /login"

// test1857C4Bare is C4: non-zero exit with no recognised cause.
const test1857C4Bare = "claude exited: exit status 1\nstderr:\nworker process aborted without a diagnostic"

// test1857C5TurnCap is C5: the CLI worker's own iteration ceiling.
const test1857C5TurnCap = "turn cap exceeded: 3 turns (max 2)"

// test1857C6Transport is C6's transport-wording stream-read failure (today it
// misclassifies as the provider code `network` — F-7 forbids that on the CLI
// path).
const test1857C6Transport = "stream read error: unexpected EOF"

// test1857C6Other is C6's unrecognised scanner failure (generic line today).
const test1857C6Other = "stream read error: scanner: invalid input while reading the worker's stdout"

// test1857C7Start is C7's exec-race start failure, wrapped by the dispatch
// path ("external-cli dispatch: driver start (%s): %w").
const test1857C7Start = "external-cli dispatch: driver start (opencode): fork/exec /opt/tools/bin/opencode: resource temporarily unavailable"

// test1857C9Limit is C9's CLI-relayed rate-limit refusal (today: rate_limited
// — the wrong remedy, it points at Omnipus's provider settings).
const test1857C9Limit = "API Error: 429 rate limit exceeded, please retry later"

// test1857C9Credit is C9's CLI-relayed out-of-credit refusal (generic today).
const test1857C9Credit = "Your credit balance is too low to access the API"

// rawSegmentsNeverOnUserSurfaces are the never-leak tokens shared by T-53 and
// T-56: a filesystem path, a flag-with-value whose value is NOT a model string
// (C2's safe facts may legitimately carry the user-chosen model string, so a
// model id must never be a never-leak token), and a key-shaped string.
var test1857RawSegments = []string{
	"/Users/dani/omnipus-debug/secret-config.json",
	"--timeout 47",
	"sk-proj-Ab3dEf7Gh9Jk2Lm4",
}

// test1857StderrTail is T-53's CLI stderr tail: an unambiguous C1 usage
// error whose tail carries the path + flag-with-value + key-shaped segments.
const test1857StderrTail = "codex exited: exit status 2\nstderr:\nerror: the argument '--model <model>' cannot be used multiple times\n(config read from /Users/dani/omnipus-debug/secret-config.json, invoked with --timeout 47, fallback key sk-proj-Ab3dEf7Gh9Jk2Lm4)"

// test1857EnvelopeRaw is T-53's stdout error envelope carrying the same three
// never-leak segments.
const test1857EnvelopeRaw = `{"type":"error","error":{"type":"provider.no-route","message":"Model unavailable: no route found for the requested model","detail":"config read from /Users/dani/omnipus-debug/secret-config.json (invoked with --timeout 47) failed: sk-proj-Ab3dEf7Gh9Jk2Lm4"}}`

// test1857UniqueRawError is B25/B26's distinctive raw cause: its tokens appear
// in no fixed sentence, so a substring hit proves raw text reached a surface.
var test1857UniqueRawError = errors.New("dial tcp 10.1.2.3:5432: connect: permission denied on /var/lib/postgresql/1857")

// test1857ErrorEvents collects every emitted event synchronously (the bus tap
// runs on the emitting goroutine).
type test1857ErrorEvents struct {
	mu     sync.Mutex
	events []Event
}

func (c *test1857ErrorEvents) tap(evt Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evt)
}

func (c *test1857ErrorEvents) errorPayloads() []ErrorPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []ErrorPayload
	for _, evt := range c.events {
		if evt.Kind != EventKindError {
			continue
		}
		if p, ok := evt.Payload.(ErrorPayload); ok {
			out = append(out, p)
		}
	}
	return out
}

// requireNoRawSegment fails with the enforcing spec row named when any raw
// segment reaches a user-facing surface.
func requireNoRawSegment(t *testing.T, row, surface string, text string) {
	t.Helper()
	for _, seg := range test1857RawSegments {
		if strings.Contains(text, seg) {
			t.Errorf("%s: raw error text reached %s — %q contains never-leak segment %q (no-raw-text rule, spec %s; FR-093)",
				row, surface, text, seg, row)
		}
	}
}

// The dedicated server-log lines the B25/B26 reporters write — raw error in
// their "error" field (sec2 A: raw text goes to the server log only). The
// markers ARE the production log messages, characterized here so the log
// assertion identifies THAT line; the event logger's copy of an emitted frame
// never carries them:
//   - pkg/agent/execution_disposition.go::reportOrdinarySettlementFailure —
//     logger.ErrorCF("agent.worker", "the turn finished, but saving its final
//     state failed", …{"error": err.Error()})
//   - pkg/agent/goal_record_error.go::reportGoalReadError —
//     logger.ErrorCF("agent", "goal_read_error: "+message, …{"error": err.Error()})
const (
	test1857B25SettlementLogMarker = "the turn finished, but saving its final state failed"
	test1857B26GoalReadLogMarker   = "goal_read_error: "
)

// test1857RequireRawOnDedicatedLogLine requires the captured server log to
// contain the reporter's dedicated ErrorCF line (identified by its log-message
// marker) AND the raw error on that same line. Removing only the dedicated
// line fails here even while the frame still carries the raw text — the event
// logger's frame copy lacks the marker. Cleaning the user text while keeping
// the ErrorCF line does not fail it: the line's error field still carries the
// raw text, so only the code/raw-text checks fail in that mutant.
func test1857RequireRawOnDedicatedLogLine(t *testing.T, logFile, lineMarker, raw, row string) {
	t.Helper()
	logBytes, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr, "SETUP: reading the captured server log")
	dedicated := 0
	for _, line := range strings.Split(string(logBytes), "\n") {
		if !strings.Contains(line, lineMarker) {
			continue
		}
		dedicated++
		assert.Contains(t, line, raw,
			"%s: the dedicated server log line (%q) must keep the raw error — sec2 A: raw text goes to the server log only", row, lineMarker)
	}
	require.NotZero(t, dedicated,
		"%s: the dedicated server log line (%q) must exist — removing only that ErrorCF line fails here even while the frame's raw text still reaches the log through the event logger's frame copy", row, lineMarker)
}

// ─────────────────────────────────────────────────────────────────────────────
// T-52 — pkg/agent::TestTurnFailureKindClassification
// ─────────────────────────────────────────────────────────────────────────────

// Test1857CLIShapeCodes is the C-row fixture table: every CLI shape and the
// §13.8.2 proposed code it must return from the CLI-only classifier (R-2).
// The CLI-only classifier does not exist yet; its named entry point today is
// external_dispatch.go::SanitizeRunnerError ("All C rows reach the classifier
// through SanitizeRunnerError" — §13.8.2 preamble), which currently routes to
// classifyByMessage via a synthetic status-0 ProviderError. The oracle is the
// spec's Proposed-code column, never the current mapping.
//
// TODO(#1857 E4): exact copy after founder wording review — the chat forms
// W1–W7 and their neutral twins for these rows are asserted in CHECK once the
// founder's E4 review lands; only the code half is in scope now.
func TestTurnFailureKindClassification(t *testing.T) {
	t.Run("cli_shapes_classify_to_proposed_codes", func(t *testing.T) {
		cases := []struct {
			name string
			row  string
			raw  string
			code string // §13.8.2 proposed code (string literal — WP-0 contract data)
		}{
			{"C1_unknown_option_usage_error", "T-52/C1 (§13.8.2 C1)", test1857C1Usage, "external_cli_invalid_args"},
			{"C1_duplicated_flag_Mac_team_case", "T-52/C1 (§13.8.2 C1)", test1857C1DuplicateFlag, "external_cli_invalid_args"},
			{"C2_nested_provider_no_route_envelope", "T-52/C2 (§13.8.2 C2)", test1857C2Envelope, "external_cli_model_unavailable"},
			{"C3_not_signed_in", "T-52/C3 (§13.8.2 C3)", test1857C3Signin, "external_cli_needs_signin"},
			{"C4_nonzero_exit_no_recognised_cause", "T-52/C4 (§13.8.2 C4)", test1857C4Bare, "external_cli_failed"},
			{"C5_turn_cap_exceeded", "T-52/C5 (§13.8.2 C5)", test1857C5TurnCap, "external_cli_turn_limit"},
			{"C6_stream_read_error_transport_wording", "T-52/C6 (§13.8.2 C6, F-7)", test1857C6Transport, "external_cli_failed"},
			{"C6_stream_read_error_unrecognised", "T-52/C6 (§13.8.2 C6, F-7)", test1857C6Other, "external_cli_failed"},
			{"C7_start_failure_exec_race", "T-52/C7 (§13.8.2 C7)", test1857C7Start, "external_cli_failed"},
			{"C9_relayed_rate_limit_refusal", "T-52/C9 (§13.8.2 C9)", test1857C9Limit, "external_cli_upstream_refused"},
			{"C9_relayed_credit_refusal", "T-52/C9 (§13.8.2 C9)", test1857C9Credit, "external_cli_upstream_refused"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				sanitized := SanitizeRunnerError(tc.raw)
				assert.Equal(t, tc.code, string(sanitized.Code),
					"%s: the CLI path must classify this shape to its proposed code %q from the CLI-only classifier, got %q (today: the shared status-0 classifyByMessage verdict — the missing WP-0 classification, not a fixture problem)",
					tc.row, tc.code, string(sanitized.Code))
			})
		}
	})

	// Test1857CLIPathNeverYieldsProviderOrNetworkCodes pins R-2's rule: the CLI
	// path never yields a provider or network code (its only pass-through typed
	// exits are turn_canceled/turn_timed_out — row C10, covered separately).
	t.Run("cli_path_never_yields_provider_or_network_codes", func(t *testing.T) {
		forbidden := map[string]bool{
			string(CodeNetwork):            true,
			string(CodeRateLimited):        true,
			string(CodeQuotaBilling):       true,
			string(CodeProviderAuthFailed): true,
			string(CodeProviderRejected):   true,
			string(CodeProviderStalled):    true,
			string(CodeProviderEmptyReply): true,
		}
		for _, tc := range []struct {
			name, row, raw string
		}{
			{"C6_transport_wording", "T-52/C6 (§13.8.2 C6, R-2/F-7)", test1857C6Transport},
			{"C9_limit_wording", "T-52/C9 (§13.8.2 C9, R-2)", test1857C9Limit},
			{"C2_envelope", "T-52/C2 (§13.8.2 C2, R-2)", test1857C2Envelope},
		} {
			t.Run(tc.name, func(t *testing.T) {
				code := string(SanitizeRunnerError(tc.raw).Code)
				assert.False(t, forbidden[code],
					"%s: the CLI path yielded provider/network code %q — R-2 forbids provider or network codes on the CLI path (row %s)", tc.row, code, tc.row)
			})
		}
	})

	// Test1857CLITypedExitControlRows pins row C10: the CLI path's only
	// pass-through typed exits stay turn_timed_out / turn_canceled (control —
	// must keep passing; listed in §13.8.2 C10 so the six-code rule's exceptions
	// stay visible).
	t.Run("typed_exit_control_rows_pass_through", func(t *testing.T) {
		code, ok := typedExitCode(ErrTurnTimedOut)
		require.True(t, ok, "T-52/C10 (§13.8.2 C10): the run-timeout typed exit must still be recognised")
		assert.Equal(t, string(CodeTurnTimedOut), string(code),
			"T-52/C10 (§13.8.2 C10): the CLI path's timeout pass-through must stay turn_timed_out")
		code, ok = typedExitCode(ErrTurnCanceled)
		require.True(t, ok, "T-52/C10 (§13.8.2 C10): the cancel typed exit must still be recognised")
		assert.Equal(t, string(CodeTurnCanceled), string(code),
			"T-52/C10 (§13.8.2 C10): the CLI path's cancel pass-through must stay turn_canceled")
	})

	// Test1857ClassifyByMessageByteIdentical pins R-2's byte-identity rule on
	// classifyByMessage's existing provider fixtures: the CLI amendment must not
	// grow provider-side matchers. These are the spec-mandated characterization
	// controls (the oracle is "unchanged classification", per §13.8.1/T-52).
	t.Run("classify_by_message_byte_identical_on_existing_fixtures", func(t *testing.T) {
		cases := []struct {
			name string
			body string
			want LLMErrorCode
		}{
			{"capability absence", "model does not support image input", CodeMediaUnsupported},
			{"image format rejection (incident string)", "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image.", CodeMediaUnsupported},
			{"pdf rejection", "pdf input not supported", CodeMediaUnsupported},
			{"image too large", "requires 38560 patches after processing, exceeding the limit of 30000", CodeMediaTooLarge},
			{"content policy", "content policy violation", CodeContentPolicy},
			{"context overflow", "context length exceeded", CodeContextTooLong},
			{"tool args", "invalid tool arguments", CodeToolArgs},
			{"rate limit", "rate limit exceeded, try again later", CodeRateLimited},
			{"transport wording", "connection reset by peer", CodeNetwork},
			{"unrelated", "something completely unrelated", CodeUnknown},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assert.Equal(t, string(tc.want), string(classifyByMessage(tc.body)),
					"T-52 (§13.8.1 R-2): classifyByMessage must stay byte-identical on its provider fixtures — this body must keep classifying %q", string(tc.want))
			})
		}
	})

	// Test1857ResidualPromotionNarrowed pins R-4 (A4/A5): a residual dispatched
	// attempt with an HTTP status ≥ 400 that matched nothing — and a 200 stream
	// that ended unparseable — returns provider_error at the terminal presentation
	// (TranslateTurnError); a residual 400 keeps provider_request_rejected; a
	// pre-send / no-status attempt is NOT promoted and stays unknown.
	t.Run("residual_promotion_narrowed_R4", func(t *testing.T) {
		t.Run("A4_residual_4xx_promotes_to_provider_error", func(t *testing.T) {
			// Real dispatched-attempt chain shape: a providers-side
			// common.ProviderError (what HandleErrorResponse emits) reachable
			// from the error — errorToProviderError walks for that type.
			pe := &common.ProviderError{Status: 418, Body: "endpoint retired without a pinned phrase"}
			got := TranslateTurnError(pe)
			assert.Equal(t, "provider_error", string(got.Code),
				"T-52/A4 (§13.8.2 A4, R-4): a residual ≥400 dispatched attempt must return provider_error at the terminal presentation, got %q", string(got.Code))
		})
		t.Run("A5_failover_exhausted_last_attempt_unclassifiable_promotes", func(t *testing.T) {
			inner := &common.ProviderError{Status: 418, Body: "endpoint retired without a pinned phrase"}
			fe := &providers.FailoverError{Reason: providers.FailoverUnknown, Provider: "p", Model: "m", Status: 418, Wrapped: inner}
			got := TranslateTurnError(fe)
			assert.Equal(t, "provider_error", string(got.Code),
				"T-52/A5 (§13.8.2 A5, R-4): a fallback chain exhausted on an unclassifiable last attempt must return provider_error at the terminal presentation, got %q", string(got.Code))
		})
		t.Run("A4_200_stream_unparseable_body_promotes", func(t *testing.T) {
			streamErr := &common.StreamProviderError{Code: "weird_unknown_1857", Message: "stream ended with an unparseable body"}
			got := TranslateTurnError(streamErr)
			assert.Equal(t, "provider_error", string(got.Code),
				"T-52/A4 (§13.8.2 A4, R-4): a 200 stream that ended unparseable must return provider_error at the terminal presentation, got %q", string(got.Code))
		})
		t.Run("residual_400_keeps_provider_request_rejected", func(t *testing.T) {
			pe := &common.ProviderError{Status: 400, Body: "nothing pinned in this body"}
			got := TranslateTurnError(pe)
			assert.Equal(t, string(CodeProviderRequestRejected), string(got.Code),
				"T-52/A4 control (§13.8.2 A4): the residual-400 promotion must stay provider_request_rejected")
		})
		t.Run("pre_send_or_no_status_attempt_not_promoted", func(t *testing.T) {
			got := TranslateTurnError(errors.New("a pre-send failure with no status and no provider shape"))
			assert.Equal(t, string(CodeUnknown), string(got.Code),
				"T-52/A4 (R-4): a pre-send / no-status attempt must NOT be promoted — it stays unknown (the reserved generic case, S-135)")
		})
		t.Run("reserved_genuinely_unknown_stays_unknown", func(t *testing.T) {
			// S-135's reserved case: nothing known at all.
			got := TranslateLLMError(nil, "something completely unrelated")
			assert.Equal(t, string(CodeUnknown), string(got.Code),
				"T-52 (S-135): a genuinely nothing-known failure must stay in the reserved generic case")
		})
	})

	// Test1857MediaGatesKeepTheirInputs pins FR-092's media-strip-retry
	// preservation at the two gates' actual inputs: outcomeFallbackEligible
	// consumes the RECOVERY classification (which must stay residual so the gate
	// stays reachable), outcomeRelabelApplies consumes the presentation code after
	// TranslateTurnError and keeps accepting the residual class exactly as today.
	// The promotion ordering (FR-092): a promoted provider_error must never reach
	// the relabel predicate early — pinned here by requiring the recovery
	// classifier to still return the residual on the very input that promotes at
	// terminal presentation.
	t.Run("media_gates_keep_their_inputs", func(t *testing.T) {
		residual := &ProviderError{Status: 418, Body: "endpoint retired without a pinned phrase"}

		t.Run("recovery_classification_stays_residual", func(t *testing.T) {
			got := TranslateLLMError(residual, "")
			assert.Equal(t, string(CodeUnknown), string(got.Code),
				"T-52 (FR-092 promotion ordering): the recovery classification must stay residual (unknown) so outcomeFallbackEligible keeps firing — promotion happens only at the terminal presentation")
		})
		t.Run("fallback_gate_accepts_residual_recovery_classification", func(t *testing.T) {
			assert.True(t, outcomeFallbackEligible(residual, CodeUnknown),
				"T-52 (FR-092): outcomeFallbackEligible must keep accepting the residual class (ProviderError + recovery code) exactly as today, or the FR-017 media strip-retry relabel breaks (F-4)")
		})
		t.Run("fallback_gate_still_excludes_pinned_and_typed", func(t *testing.T) {
			assert.False(t, outcomeFallbackEligible(&ProviderError{Status: 401, Body: "unauthorized"}, CodeUnknown),
				"T-52 (FR-092): 401 must keep excluding the media fallback")
			assert.False(t, outcomeFallbackEligible(&ProviderError{Status: 400, Body: "context length exceeded"}, CodeUnknown),
				"T-52 (FR-092): an exclusion body must keep excluding the media fallback")
			assert.False(t, outcomeFallbackEligible(residual, CodeToolArgs),
				"T-52 (FR-092): a non-residual code must keep excluding the media fallback")
		})
		t.Run("relabel_gate_accepts_residual_class_exactly_as_today", func(t *testing.T) {
			assert.True(t, outcomeRelabelApplies("", CodeMediaUnsupported),
				"T-52 (FR-092): outcomeRelabelApplies must keep accepting an empty presentation code")
			assert.True(t, outcomeRelabelApplies(CodeUnknown, CodeMediaUnsupported),
				"T-52 (FR-092): outcomeRelabelApplies must keep accepting unknown")
			assert.True(t, outcomeRelabelApplies(CodeProviderRequestRejected, CodeMediaUnsupported),
				"T-52 (FR-092): outcomeRelabelApplies must keep accepting provider_request_rejected")
			assert.False(t, outcomeRelabelApplies(CodeToolArgs, CodeMediaUnsupported),
				"T-52 (FR-092): a distinct typed code must keep its own label — the relabel must not override it")
		})
	})

	// FR-092/T-52: the promotion ordering is asserted on the strip-retry
	// sequence's live and persisted outcomes, not on unchanged function
	// bodies — the function-body input pins above stay as the inputs.
	t.Run("strip_retry_then_residual_live_and_persisted_keep_the_media_label", func(t *testing.T) {
		live, persisted := test1857DriveStripRetryThenLaterFailure(t,
			&common.ProviderError{Status: 418, Body: "endpoint retired without a pinned phrase"})
		assert.Equal(t, string(CodeMediaUnsupported), live.Code,
			"T-52 (FR-092, live): a residual dispatch-provider failure (A4) arriving after a successful strip-retry stays relabeled media on the emitted frame — a provider_error promoted before the relabel predicate would suppress the media label here")
		assert.Equal(t, string(CodeMediaUnsupported), persisted.ErrorCode,
			"T-52 (FR-092, reloaded transcript): the media label must survive reload, not just the live frame")
	})
	t.Run("distinct_later_typed_failure_keeps_its_code_live_and_persisted", func(t *testing.T) {
		live, persisted := test1857DriveStripRetryThenLaterFailure(t,
			&common.ProviderError{Status: 400, Body: "content policy violation"})
		assert.Equal(t, string(CodeContentPolicy), live.Code,
			"T-52 (FR-092, live): a later distinct classified failure keeps its own code on the emitted frame — the relabel must never overwrite it")
		assert.Equal(t, string(CodeContentPolicy), persisted.ErrorCode,
			"T-52 (FR-092, reloaded transcript): the distinct code survives reload")
	})
}

// test1857StripSeqProvider scripts the strip-retry-then-later-failure
// sequence for the T-52/FR-092 live drive: attempt 1 fails with a residual
// 400 whose body matches nothing (the FR-017 outcome fallback strips the
// image and retries), attempt 2 succeeds (the FR-017a relabel stamp is
// written by the real loop path), attempt 3 fails with the caller's later
// error.
type test1857StripSeqProvider struct {
	calls     int
	third     error
	mediaSeen [][]int
}

func (*test1857StripSeqProvider) GetDefaultModel() string { return "m" }

func (p *test1857StripSeqProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	counts := make([]int, len(messages))
	for i := range messages {
		counts[i] = len(messages[i].Media)
	}
	p.mediaSeen = append(p.mediaSeen, counts)
	p.calls++
	switch p.calls {
	case 1:
		return nil, &common.ProviderError{Status: 400, Body: `{"error":{"message":"upstream capacity pool exhausted for this request shape 1857"}}`}
	case 2:
		return &providers.LLMResponse{Content: "recovered answer 1857"}, nil
	default:
		return nil, p.third
	}
}

// test1857DriveStripRetryThenLaterFailure drives the real loop path with the
// routing-1340 fixture (only the external provider is substituted; selection,
// recovery, error emission and durable transcript handling remain real): the
// outcome-based strip-retry recovers (attempt 1 → strip → attempt 2 succeeds,
// relabel stamped), then a later provider failure is classified and consumed
// at the real persist/emit point (handleProviderResponse). Returns the live
// error frame and the persisted classified entry — the sequence's live and
// reloaded-transcript outcomes.
func test1857DriveStripRetryThenLaterFailure(t *testing.T, later error) (ErrorPayload, *session.TranscriptEntry) {
	t.Helper()
	client := &test1857StripSeqProvider{third: later}
	a := &AgentInstance{ID: "1857-media-seq", Model: "m", Candidates: []providers.FallbackCandidate{{Provider: "openrouter", Model: "m"}}}
	a.StoreProviderPool(map[string]providers.LLMProvider{"openrouter": client})
	f := newRouting1340ReviewFixture(t, nil, a)
	f.selectProvider(t)
	f.rf.callMessages = []providers.Message{{Role: "user", Content: "inspect this 1857", Media: []string{"data:image/png;base64,iVBORw0KGgo="}}}
	f.ri.messages = f.rf.callMessages

	// Pass 1: residual 400 with media → strip → pass 2 succeeds → stamp.
	if flow := f.rr.callLLMWithRetries(); flow != agentLoopRunTurnResponseNext {
		t.Fatalf("SETUP: the strip-retry pass must recover (flow=%v err=%v)", flow, f.rf.err)
	}
	require.Len(t, client.mediaSeen, 2, "SETUP: the recovery pass consumes exactly two provider calls")
	require.Equal(t, []int{1}, client.mediaSeen[0], "SETUP: the refused attempt carries the image block")
	require.Equal(t, []int{0}, client.mediaSeen[1], "SETUP: the recovered attempt carries no image (FR-017 outcome fallback stripped it)")
	require.Equal(t, string(CodeMediaUnsupported), string(f.rt.ts.outcomeRelabel),
		"SETUP: the successful strip-retry must stamp the FR-017a relabel")

	// The later failure: the next provider call fails; the real response
	// stage classifies it, consults the relabel gate at the persist/emit
	// point, emits the live frame and persists the classified entry.
	f.rr.callLLMWithRetries()
	require.Error(t, f.rf.err, "SETUP: the scripted later failure must reach the response stage")
	if flow := f.rr.handleProviderResponse(); flow != agentLoopRunTurnResponseReturn {
		t.Fatalf("SETUP: the failed response stage must end the response (flow=%v)", flow)
	}

	var live ErrorPayload
	liveFound := false
	f.mu.Lock()
	for _, evt := range f.events {
		if evt.Kind != EventKindError {
			continue
		}
		payload, ok := evt.Payload.(ErrorPayload)
		if !ok || payload.Stage != "llm" {
			continue
		}
		live, liveFound = payload, true
	}
	f.mu.Unlock()
	require.True(t, liveFound, "SETUP: the later failure must emit the llm error frame")

	entries, readErr := f.store.ReadTranscript(f.sessionID)
	require.NoError(t, readErr, "SETUP: reading back the persisted transcript")
	var persisted *session.TranscriptEntry
	for i := range entries {
		if entries[i].Status == "error" && entries[i].ErrorCode != "" {
			persisted = &entries[i]
		}
	}
	require.NotNil(t, persisted, "SETUP: the classified error entry must persist for reload")
	return live, persisted
}

// ─────────────────────────────────────────────────────────────────────────────
// T-53 — pkg/agent::TestSanitizeRunnerErrorSafeFacts
// ─────────────────────────────────────────────────────────────────────────────

// TestSanitizeRunnerErrorSafeFacts drives a CLI stderr tail and a stdout
// error envelope, each carrying a path, a flag-with-value and a key-shaped
// string, through the CLI-path sanitizer and the hub's renderer, and asserts
// (E1 B + E3 A): only the program display name, the exit status and the
// failure kind are user-facing facts — not in chat (AssistantText / frame
// Message), not in technical details (the wire Detail) — while gateway.log
// keeps the raw text (LogText). The matched-kind assertions are the RED half;
// the no-raw-text assertions pin FR-093's status quo so the fix cannot regress
// it.
//
// TODO(#1857 E4): exact copy after founder wording review — the §13.8.3 voice
// guard and copy-rule constraint halves of this row (both voices, resolved
// variants, no-fact forms, closed CLI display names) are written after the E4
// review; only the safe-facts/no-raw-text half is in scope now.
func TestSanitizeRunnerErrorSafeFacts(t *testing.T) {
	cases := []struct {
		name     string
		row      string
		raw      string
		wantCode string // §13.8.2 proposed code for this shape
	}{
		{"stderr_tail", "T-53 (S-134/S-136; §13.8.2 C1)", test1857StderrTail, "external_cli_invalid_args"},
		{"stdout_error_envelope", "T-53 (S-134/S-136; §13.8.2 C2)", test1857EnvelopeRaw, "external_cli_model_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sanitized := SanitizeRunnerError(tc.raw)

			// Chat surface: the sanitized assistant copy carries no raw text.
			requireNoRawSegment(t, tc.row, "the assistant-facing copy", sanitized.AssistantText)
			assert.NotContains(t, sanitized.AssistantText, "exit status 2",
				"%s: the exit status is a technical-details fact (VS-2), not chat copy (E1 B/E3 A)", tc.row)

			// gateway.log keeps the raw text — the one surface for it.
			assert.Contains(t, sanitized.LogText, tc.raw[:20],
				"%s: gateway.log's carrier (LogText) must keep the raw text (§13.8.4 gateway.log row; E1 B/E3 A)", tc.row)

			// The matched kind — the RED half: today the shared status-0
			// classifier returns unknown.
			assert.Equal(t, tc.wantCode, string(sanitized.Code),
				"%s: the sanitizer must carry the matched failure kind %q, got %q", tc.row, tc.wantCode, string(sanitized.Code))

			// Technical details (Verbose/Debug): render the error event the
			// dispatcher emits (emitExternalCLIErrorEvent's payload) the way
			// the gateway hub renders it (hubError → WireLLMError) — the
			// frame Message and Detail carry no raw text.
			payload := ErrorPayload{
				Stage:   "external_cli",
				Code:    string(sanitized.Code),
				Message: sanitized.AssistantText,
			}
			wire := WireLLMError(payload)
			requireNoRawSegment(t, tc.row, "the wire frame Message", wire.Message)
			if wire.Detail != nil {
				requireNoRawSegment(t, tc.row, "the wire frame Detail (technical details)", *wire.Detail)
			}
			// F-1 trust rule: a frame whose message is not a provider-message
			// template must never flag itself provider_message.
			assert.NotEqual(t, true, wire.ProviderMessage != nil && *wire.ProviderMessage,
				"%s: the CLI error frame must not set provider_message (the SPA would then show the frame message raw; FR-092/F-1)", tc.row)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// T-54 — pkg/agent::TestCodeUnknownPayloadGuard
// ─────────────────────────────────────────────────────────────────────────────

// TestCodeUnknownPayloadGuard is FR-092's guard (T-54 row, MAJ-004).
// Assertion (1): untrusted text attached to a CodeUnknown frame never renders
// (the F-1 trust rule at the Go-side seam). Assertion (2): the T-54
// allow-list's payloads stay catalogue-clean — exactly two named groups
// (A4/A5's non-promoted dispatched-provider residuals; the six B24
// record-save refusals) — and the CONVERTED sites (the B10 duplicate) are no
// longer in the generic result. Row 3's mutation clause is CHECK's work.
func TestCodeUnknownPayloadGuard(t *testing.T) {
	t.Run("untrusted_attached_text_never_renders", func(t *testing.T) {
		untrusted := "untrusted attached text 1857 — never display this"
		wire := WireLLMError(ErrorPayload{Code: string(CodeUnknown), Message: untrusted})
		flagSet := wire.ProviderMessage != nil && *wire.ProviderMessage
		assert.False(t, flagSet,
			"T-54 (1) (FR-092/MAJ-004): an attached message on a CodeUnknown frame must never render — provider_message must stay unset so the SPA trust rule (F-1) shows the catalogue line, never the attached text")
	})

	t.Run("b24_record_save_refusals_stay_catalogue_clean", func(t *testing.T) {
		// The six B24 producers share the messageNotSavedError branch
		// (§13.8.2 B24). Its payload stays on the allow-list: the fixed
		// reply, never the raw store error.
		rawStoreErr := errors.New("write /var/omnipus/sessions/1857.json: no space left on device")
		notSaved := &messageNotSavedError{role: "user", cause: rawStoreErr}
		llm := TranslateTurnError(notSaved)
		assert.Equal(t, string(CodeUnknown), string(llm.Code),
			"T-54 (2) (FR-092 allow-list, B24): the record-save refusals deliberately stay generic — the branch keeps CodeUnknown")
		assert.Equal(t, messageNotSavedReply, llm.Message,
			"T-54 (2) (FR-092 allow-list, B24): the branch's payload carries its permitted fixed reply, never authored text")
		assert.NotContains(t, llm.Message, "no space left on device",
			"T-54 (2) (FR-092 allow-list, B24): the raw store error must not ride the message")
	})

	t.Run("non_promoted_dispatched_residual_stays_catalogue_clean", func(t *testing.T) {
		// A4/A5's non-promoted class (pre-send / no-status): genuinely
		// unclassified, stays on the catalogue fallback.
		llm := TranslateLLMError(nil, "a pre-send failure with no status and no provider shape")
		assert.Equal(t, string(CodeUnknown), string(llm.Code), "T-54 (2) (FR-092 allow-list, A4/A5 non-promoted): the residual stays unknown")
		assert.Equal(t, generated.LLMErrorUserMessages["unknown"], llm.Message,
			"T-54 (2) (FR-092 allow-list, A4/A5 non-promoted): the residual payload carries the catalogue fallback, never authored text")
	})

	// B10 (§13.8.2 B10, §13.8.5 B10, FR-092/T-54): the tool-assembly
	// duplicate emit is CONVERTED off the generic result — it carries
	// tool_assembly_duplicate for both source classes, keyed on the only
	// fact the detector records (CategoryMCP → mcp:unknown, everything
	// else → builtin). Driven deterministically the way wu22DuplicateSurface
	// drives it (pkg/agent/wu22_turn_archive_failure_test.go): two distinct
	// names registered — the registry's collision guard only sees
	// Register-time names — then the second renamed onto the first, so the
	// real prepareToolSurface assembles a same-name pair. Neither the
	// registry nor prepareToolSurface is mocked.
	drive1857DuplicateSurface := func(t *testing.T, mcpInvolved bool) (ErrorPayload, *session.TranscriptEntry, error) {
		t.Helper()
		h := wu22NewHarness(t, wu22HealthyProvider())
		a := &test1857DupTool{name: "1857_dup_base", scopeVal: tools.ScopeCore}
		b := &test1857DupTool{name: "1857_dup_other", scopeVal: tools.ScopeCore}
		var registered tools.Tool = b
		if mcpInvolved {
			// The MCP-involved side is a real connector tool (MCPSource +
			// server binding): the assignment gate would otherwise filter it
			// out before the dedup check ever sees the pair.
			wrapper := &test1857MCPDupTool{test1857DupTool{name: "1857_dup_other", scopeVal: tools.ScopeCore, mcp: true}}
			b = &wrapper.test1857DupTool
			registered = wrapper
		}
		h.agent.Tools = tools.NewToolRegistry()
		h.agent.Tools.Register(a)
		h.agent.Tools.Register(registered)
		b.name = a.name
		h.agent.StoreToolPolicy(&tools.ToolPolicyCfg{
			GlobalPolicies: map[string]config.ToolPolicy{"1857_dup_base": config.ToolPolicyAllow},
			MCPServers:     []config.AgentMCPServerBinding{{ID: "1857-srv"}},
		})
		rq := h.responseStage().rq
		if flow := rq.prepareToolSurface(); flow != agentLoopRunTurnRequestReturn {
			t.Fatalf("SETUP: the duplicate tool surface must end the request at the dedup branch (flow=%v)", flow)
		}
		require.Error(t, rq.ret1, "SETUP: the FR-066 detector must fire on the assembled duplicate")
		h.mu.Lock()
		var payload *ErrorPayload
		for _, evt := range h.events {
			if evt.Kind != EventKindError {
				continue
			}
			p, ok := evt.Payload.(ErrorPayload)
			if !ok || p.Stage != "tool_assembly" {
				continue
			}
			payload = &p
		}
		h.mu.Unlock()
		require.NotNil(t, payload,
			"T-54/B10 (§13.8.5 B10): the tool_assembly error event must be emitted — a missing event fails here, the code assertion is never vacuous")
		entries, readErr := h.store.ReadTranscript(h.sid)
		require.NoError(t, readErr, "SETUP: reading back the persisted transcript")
		var persisted *session.TranscriptEntry
		for i := range entries {
			if entries[i].Status == "error" && entries[i].ErrorCode != "" {
				persisted = &entries[i]
			}
		}
		require.NotNil(t, persisted, "SETUP: the classified error entry must persist for reload")
		return *payload, persisted, rq.ret1
	}

	t.Run("converted_b10_mcp_involved_duplicate_carries_tool_assembly_duplicate", func(t *testing.T) {
		// The MCP-involved class is expressed through Category() — the only
		// fact the detector reads (dupTool's category is hard-wired
		// CategoryCore and cannot express this class; hence the local double).
		mcp := &test1857MCPDupTool{test1857DupTool{name: "1857_dup_probe", scopeVal: tools.ScopeCore, mcp: true}}
		require.Equal(t, tools.CategoryMCP, mcp.Category(),
			"SETUP: the fixture must express the MCP class through Category()")
		live, persisted, detectorErr := drive1857DuplicateSurface(t, true)
		assert.Contains(t, detectorErr.Error(), "mcp",
			"T-54/B10 (§13.8.2 B10): the MCP-involved class must carry the mcp tag — the only source fact the converted emit keys its catalogue variant on")
		assert.Equal(t, "tool_assembly_duplicate", live.Code,
			"T-54/B10 (§13.8.2/§13.8.5 B10): the MCP-involved duplicate must carry tool_assembly_duplicate on the emitted frame, got %q (today the emit block is hard-coded CodeUnknown — the generic result this conversion leaves)", live.Code)
		assert.Equal(t, "tool_assembly_duplicate", persisted.ErrorCode,
			"T-54/B10 (§13.8.5 B10, reloaded transcript): the same code persists for reload")
	})
	t.Run("converted_b10_no_mcp_duplicate_carries_tool_assembly_duplicate", func(t *testing.T) {
		live, persisted, detectorErr := drive1857DuplicateSurface(t, false)
		assert.NotContains(t, detectorErr.Error(), "mcp",
			"T-54/B10 (§13.8.2 B10): the no-MCP class must carry the builtin tag — the fail-closed fallback variant's key (the round-1 builtin-only exemption stays removed)")
		assert.Equal(t, "tool_assembly_duplicate", live.Code,
			"T-54/B10 (§13.8.5 B10): the no-MCP duplicate must carry the same tool_assembly_duplicate code (fail-closed fallback variant), got %q", live.Code)
		assert.Equal(t, "tool_assembly_duplicate", persisted.ErrorCode,
			"T-54/B10 (§13.8.5 B10, reloaded transcript): the same code persists for reload")
	})
}

// test1857DupTool is the B10 fixture double with a real category: the dedup
// detector reads only Category() (CategoryMCP → mcp:unknown, anything else →
// builtin), so the MCP-involved class is expressed through the category, not
// through a sourceTag string.
type test1857DupTool struct {
	name     string
	scopeVal tools.ToolScope
	mcp      bool
}

func (d *test1857DupTool) Name() string               { return d.name }
func (d *test1857DupTool) Description() string        { return "1857 dedup fixture" }
func (d *test1857DupTool) Parameters() map[string]any { return map[string]any{} }
func (d *test1857DupTool) Scope() tools.ToolScope     { return d.scopeVal }

func (d *test1857DupTool) Category() tools.ToolCategory {
	if d.mcp {
		return tools.CategoryMCP
	}
	return tools.CategoryCore
}

func (d *test1857DupTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "1857 dedup fixture result"}
}

// test1857MCPDupTool is the MCP-involved side of the colliding pair: a real
// connector tool, so it survives the assignment gate
// (pkg/tools::mcpAssignmentAllows — a CategoryMCP tool without an MCPSource
// binding is filtered out before the dedup check ever sees it) while still
// reporting CategoryMCP to the detector.
type test1857MCPDupTool struct {
	test1857DupTool
}

func (*test1857MCPDupTool) MCPSource() (string, string) { return "1857-srv", "1857_remote_dup" }

// ─────────────────────────────────────────────────────────────────────────────
// T-55 — pkg/agent::TestKnownCauseKindsProduceTypedOutcomes (code and
// attribution assertions only — the sentences are WP-6 manifest copy, E4)
// ─────────────────────────────────────────────────────────────────────────────

// TestKnownCauseKindsProduceTypedOutcomes drives the emit blocks named in the
// brief: B3 (own rate limiter → agent_request_limit / attribution config,
// founder E2), B25 (settlement storage failure → transcript_save_failed, raw
// text nowhere user-facing), B26 (goal-read failure → goal_state_unavailable,
// raw %v out of the frame and the persisted transcript).
//
// TODO(#1857 E4): exact copy after founder wording review — B3's W11 + neutral
// twin, B25's manifest M-1857-B25 sentence and B26's M-1857-B26 sentence are
// asserted after the E4 review; only the code/attribution/no-raw-text halves
// are in scope now.
func TestKnownCauseKindsProduceTypedOutcomes(t *testing.T) {
	t.Run("B3_own_rate_limiter_codes_agent_request_limit", func(t *testing.T) {
		// Fixture drives the real emit block (loop_run_turn.go::beginIteration,
		// the SEC-26 deny branch): budget 1, first call consumes it, second is
		// refused before any provider call (same drive as
		// runturn_rate_limit_test.go).
		tmpHome := t.TempDir()
		workspaceDir := filepath.Join(tmpHome, "workspace")
		require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
		provider := testutil.NewScenario().WithText("first response — call 1 succeeded")
		const customAgentID = "1857-rate-test-agent"
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:              workspaceDir,
					DefaultModel:      config.DefaultModel{Model: "scripted-model"},
					MaxTokens:         4096,
					MaxToolIterations: 10,
				},
				List: []config.AgentConfig{
					{ID: customAgentID, Name: "1857 Rate Test Agent", Type: config.AgentTypeCustom},
				},
			},
			Sandbox: config.OmnipusSandboxConfig{
				AuditLog: true,
				RateLimits: config.OmnipusRateLimitsConfig{
					MaxAgentLLMCallsPerHour: 1,
				},
			},
		}
		al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
		defer al.Close()
		customAgent, ok := al.GetRegistry().GetAgent(customAgentID)
		require.True(t, ok, "SETUP: custom agent must be registered")

		ctx := context.Background()
		_, err1 := al.runAgentLoop(ctx, customAgent, processOptions{
			SessionKey:      "1857-b3-session-1",
			Channel:         "cli",
			ChatID:          "direct",
			UserMessage:     "first message",
			DefaultResponse: defaultResponse,
			SendResponse:    false,
		})
		require.NoError(t, err1, "SETUP: call 1 must consume the only allowed slot")
		_, err2 := al.runAgentLoop(ctx, customAgent, processOptions{
			SessionKey:      "1857-b3-session-2",
			Channel:         "cli",
			ChatID:          "direct",
			UserMessage:     "second message",
			DefaultResponse: defaultResponse,
			SendResponse:    false,
		})
		require.Error(t, err2, "SETUP: the denied iteration must end the turn with an error (the emit block must have run)")
		require.Contains(t, err2.Error(), "rate limit",
			"SETUP: the denial must be the SEC-26 loop-level refusal, not some other error")

		// The classification the turn error must produce (B3, §13.8.5 matrix
		// row; oracle S-139/T-55): own limiter → agent_request_limit, never
		// the provider-overloaded rate_limited copy.
		got := TranslateTurnError(err2)
		assert.Equal(t, "agent_request_limit", string(got.Code),
			"T-55/B3 (§13.8.5 B3, founder E2): Omnipus's own rate limiter must classify agent_request_limit, got %q — today the emit block's wording substring-matches the provider-overloaded rate_limited copy (the misattribution this row fixes)", string(got.Code))
		assert.NotEqual(t, string(CodeRateLimited), string(got.Code),
			"T-55/B3 (§13.8.5 B3): the provider-blame misattribution must be gone — a mutation re-matching the provider-overloaded copy fails here")

		// The attribution is contract data (per code, never per instance):
		// agent_request_limit must be attributed config.
		assert.Equal(t, generated.LLMErrorAttributionConfig, generated.LLMErrorUserAttributions["agent_request_limit"],
			"T-55/B3 (§13.8.5 B3): agent_request_limit must be attributed config in the contract catalogue (absent today — WP-0 adds it)")
	})

	t.Run("B25_settlement_storage_failure_transcript_save_failed_no_raw_text", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &noCallProvider{t: t}, nil)
		collector := &test1857ErrorEvents{}
		al.SetEventSyncTap(collector.tap)
		t.Cleanup(func() { al.SetEventSyncTap(nil) })

		// The raw text's one permitted surface is the server log (§13.8.5 B25
		// oracle: "the log call keeps it") — captured through the real
		// logger.EnableFileLogging sink, the gateway.log writer.
		logFile := filepath.Join(t.TempDir(), "1857-b25-serverlog.log")
		require.NoError(t, logger.EnableFileLogging(logFile))
		t.Cleanup(func() { logger.DisableFileLogging() })

		msg := bus.InboundMessage{
			SessionID:  "1857-b25-session",
			SessionKey: "1857-b25-key",
			ChatID:     "1857-b25-chat",
		}
		al.reportOrdinarySettlementFailure(msg, test1857UniqueRawError)

		payloads := collector.errorPayloads()
		require.NotEmpty(t, payloads,
			"T-55/B25 (§13.8.5 B25): the settlement reporter must emit its error event")
		var settlementPayload *ErrorPayload
		for i := range payloads {
			if payloads[i].Stage == "settlement" {
				settlementPayload = &payloads[i]
				break
			}
		}
		require.NotNil(t, settlementPayload,
			"T-55/B25 (§13.8.5 B25): the settlement-stage error payload must be emitted")

		// Code: re-keyed to transcript_save_failed (settlement catalogue
		// variant — no new code; the internal settlement_failed retires).
		assert.Equal(t, "transcript_save_failed", settlementPayload.Code,
			"T-55/B25 (§13.8.5 B25, sec2 A): the settlement storage failure must carry transcript_save_failed, got %q (today the internal settlement_failed)", settlementPayload.Code)

		// The frame as the hub renders it: the fixed safe sentence — the raw
		// err.Error() reaches no user-facing field.
		assert.NotContains(t, settlementPayload.Message, test1857UniqueRawError.Error(),
			"T-55/B25 (§13.8.2 B25, sec2 A): raw error text reached the frame message — the fail-closed copy rule forbids it (today the reporter appends err.Error())")
		requireNoRawSegment(t, "T-55/B25 (§13.8.2 B25)", "the settlement frame message", settlementPayload.Message)
		wire := WireLLMError(*settlementPayload)
		assert.NotContains(t, wire.Message, test1857UniqueRawError.Error(),
			"T-55/B25 (§13.8.2 B25): raw error text reached the rendered wire frame")
		if wire.Detail != nil {
			assert.NotContains(t, *wire.Detail, test1857UniqueRawError.Error(),
				"T-55/B25 (§13.8.2 B25): raw error text reached the rendered frame detail (technical details)")
		}

		// And the one permitted surface still has it: the DEDICATED ErrorCF
		// line keeps the raw error — identified by its marker so the event
		// logger's copy of a still-raw frame cannot satisfy this in its place.
		test1857RequireRawOnDedicatedLogLine(t, logFile, test1857B25SettlementLogMarker, test1857UniqueRawError.Error(),
			"T-55/B25 (§13.8.5 B25 oracle)")
	})

	t.Run("B26_goal_read_failure_goal_state_unavailable_no_raw_text", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &noCallProvider{t: t}, nil)
		collector := &test1857ErrorEvents{}
		al.SetEventSyncTap(collector.tap)
		t.Cleanup(func() { al.SetEventSyncTap(nil) })

		// The server log is the raw text's one permitted surface here too
		// (§13.8.5 B26 oracle: "the log keeps it") — captured through the
		// real logger.EnableFileLogging sink, the gateway.log writer.
		logFile := filepath.Join(t.TempDir(), "1857-b26-serverlog.log")
		require.NoError(t, logger.EnableFileLogging(logFile))
		t.Cleanup(func() { logger.DisableFileLogging() })

		// The transcript write is AppendTranscriptStrict — the session must
		// exist, or the write is silently dropped and the guard would pass
		// for the wrong reason (instrument check).
		store := al.GetSessionStore()
		require.NotNil(t, store, "SETUP: the loop's shared session store must exist")
		meta, err := store.NewSession(session.SessionTypeChat, "", "native-agent")
		require.NoError(t, err, "SETUP: creating the transcript session")

		message := al.reportGoalReadError(meta.ID, "background reminder sweep", test1857UniqueRawError)

		// The returned message (the fixed safe sentence post-fix) carries no
		// raw text — today it is fmt.Sprintf("Goal state could not be read;
		// %s deferred: %v", operation, err).
		assert.NotContains(t, message, test1857UniqueRawError.Error(),
			"T-55/B26 (§13.8.2 B26, sec2 A): raw error text reached the goal-read message — the fail-closed copy rule forbids the raw %v (today the reporter embeds it)")

		// The persisted chat transcript line is a rendered surface: no raw
		// substring may persist.
		entries, readErr := store.ReadTranscript(meta.ID)
		require.NoError(t, readErr, "SETUP: reading back the persisted transcript")
		require.NotEmpty(t, entries, "SETUP: the transcript write must have persisted at least one entry (AppendTranscriptStrict is strict — an empty read proves the instrument, not the fix)")
		for _, entry := range entries {
			assert.NotContains(t, entry.Content, test1857UniqueRawError.Error(),
				"T-55/B26 (§13.8.2 B26, sec2 A): raw error text persisted in the chat transcript (entry %s) — today writeGoalSystemTranscript stores the raw %%v message", entry.ID)
		}

		// The live error event: no raw text, and the goal_state_unavailable
		// code. A missing event FAILS here — the code assertion is never
		// vacuously skipped by a later change that stops emitting.
		payloads := collector.errorPayloads()
		var goalPayload *ErrorPayload
		for i := range payloads {
			if payloads[i].Stage == "goal_record_read" {
				goalPayload = &payloads[i]
				break
			}
		}
		require.NotNil(t, goalPayload,
			"T-55/B26 (§13.8.5 B26): the goal_record_read error event must be emitted — a missing event fails, the code assertion is never skipped")
		assert.NotContains(t, goalPayload.Message, test1857UniqueRawError.Error(),
			"T-55/B26 (§13.8.2 B26): raw error text reached the emitted frame message")
		assert.Equal(t, "goal_state_unavailable", goalPayload.Code,
			"T-55/B26 (§13.8.5 B26): the goal-read failure must carry goal_state_unavailable, got %q (today CodeUnknown)", goalPayload.Code)

		// And the one permitted surface still has it: the DEDICATED ErrorCF
		// line keeps the raw error — identified by its marker so the event
		// logger's copy of a still-raw frame cannot satisfy this in its place.
		test1857RequireRawOnDedicatedLogLine(t, logFile, test1857B26GoalReadLogMarker, test1857UniqueRawError.Error(),
			"T-55/B26 (§13.8.5 B26 oracle)")
	})

	t.Run("B26_refusal_carrier_fixed", func(t *testing.T) {
		// refuseUnreadGoalState sets the failed turn's final content to the
		// raw-text message today — the second carrier this row fixes.
		al, _ := newGoalLoopTestLoop(t, &noCallProvider{t: t}, nil)
		ts := &turnState{
			agent:               nil, // the refusal path touches only opts + the reporter
			agentID:             "native-agent",
			sessionKey:          "1857-b26-refusal",
			turnID:              "1857-b26-refusal-turn",
			transcriptSessionID: "1857-b26-refusal-session",
		}
		rt := &agentLoopRunTurn{al: al, ts: ts}
		rf := &agentLoopRunTurnFallbacks{rt: rt}
		ri := &agentLoopRunTurnIteration{rf: rf}
		rq := &agentLoopRunTurnRequest{ri: ri}

		rq.refuseUnreadGoalState(test1857UniqueRawError)

		require.Equal(t, TurnEndStatusError, ri.turnStatus,
			"T-55/B26 (§13.8.5 B22 binding): the refusal stays terminal for the active request")
		assert.NotContains(t, rq.ret0.finalContent, test1857UniqueRawError.Error(),
			"T-55/B26 (§13.8.2 B26, sec2 A): raw error text reached the failed turn's final content — both carriers of the raw %v are fixed by this row (today refuseUnreadGoalState sets the raw-text message as finalContent)")
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// T-56 — pkg/agent::TestCLIFailureAdaptersEndToEnd (agent leg: fake CLI
// process/output boundary → dispatcher → error assembly → persistence →
// renderer; no-raw-text and code assertions only)
// ─────────────────────────────────────────────────────────────────────────────

// TestCLIFailureAdaptersEndToEnd drives the real pipeline from the fake CLI
// output boundary (runnertest.FakeRunner) through runExternalCLISubTurn →
// drainExternalRun → emitExternalCLIErrorEvent (the wire event) and
// appendClassifiedError (the persisted record) and the ToolResult (the parent
// tool result). No raw stderr/stdout text may reach the wire, the history or
// the parent tool result; the codes must be the proposed §13.8.2 codes.
//
// TODO(#1857 E4): exact copy after founder wording review — (a)'s helper-chat
// W1 and Settings W1-neutral forms, (b)'s W2 forms, (e)'s task/centre neutral
// copy and (g)'s pinned Settings copy are asserted after the E4 review; only
// the no-raw-text and code halves are in scope now.
// test1857TranscriptBody concatenates every persisted entry's content for a
// whole-transcript never-leak scan.
func test1857TranscriptBody(entries []session.TranscriptEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Content)
	}
	return sb.String()
}

// new1857ExternalStore builds the real UnifiedStore the T-56 drives attach to
// the sub-turn's turnState, so the persisted half reads a disk-backed
// transcript.
func new1857ExternalStore(t *testing.T, home string) (*session.UnifiedStore, string) {
	t.Helper()
	dir := filepath.Join(home, "sessions")
	st, err := session.NewUnifiedStore(dir)
	require.NoError(t, err)
	meta, err := st.NewSession(session.SessionTypeChat, "", "ext-agent")
	require.NoError(t, err)
	return st, meta.ID
}

func TestCLIFailureAdaptersEndToEnd(t *testing.T) {
	driveFatal := func(t *testing.T, rawMessage string) (res *tools.ToolResult, payloads []ErrorPayload, entries []session.TranscriptEntry, store *session.UnifiedStore, sessionID string, fatalErr error) {
		t.Helper()
		home := t.TempDir()
		t.Setenv(config.EnvHome, home)
		al, ts := newExternalTestLoop(t, "codex", "")

		st, sessionID := new1857ExternalStore(t, home)
		ts.transcriptStore = st
		ts.transcriptSessionID = sessionID

		collector := &test1857ErrorEvents{}
		al.SetEventSyncTap(collector.tap)
		t.Cleanup(func() { al.SetEventSyncTap(nil) })

		fr, restore := withFakeDriver(t)
		defer restore()
		go func() {
			require.NoError(t, fr.InjectEvent(runner.RunEvent{
				Kind: runner.EventKindError,
				Err:  &runner.ErrorEvent{Message: rawMessage, Fatal: true},
			}))
			fr.Cancel()
		}()

		res, err := runExternalCLISubTurn(context.Background(), al, ts, "task")
		require.Error(t, err, "SETUP: a fatal CLI error must fail the run")
		fatalErr = err
		require.NoError(t, st.Close())
		entries, err = st.ReadTranscript(sessionID)
		require.NoError(t, err, "SETUP: reading back the persisted transcript")
		return res, collector.errorPayloads(), entries, st, sessionID, fatalErr
	}

	t.Run("a_duplicated_flag_rejection_wire_history_parent_no_raw_correct_code", func(t *testing.T) {
		row := "T-56(a) (§13.8.2 C1 / §13.8.5 B16cfg surfaces; FR-092, FR-093)"
		res, payloads, entries, _, _, _ := driveFatal(t, test1857C1DuplicateFlag)

		// Parent tool result: the failure is reported, never the raw text.
		require.NotNil(t, res, "SETUP: the dispatcher must return a tool result")
		require.NotNil(t, res.Err, "T-56(a): the fatal CLI error must fail the parent tool result")
		assert.NotContains(t, res.Err.Error(), "cannot be used multiple times",
			"T-56(a): raw stderr text reached the parent tool result error")
		assert.NotContains(t, res.ForLLM, "external-cli run failed",
			"T-56(a) (§13.8.4 rule 3): the \"external-cli run failed: \" prefix never reaches a user surface — the parent tool result included; post-fix it survives only in gateway.log (today the tool result carries it, doubled with the curated turn error)")
		assert.Contains(t, res.ForLLM, "external_cli_invalid_args",
			"T-56(a) (§13.8.4 tool-result row): the typed code and its sentence reach the parent tool result (today ForLLM carries neither — the sanitized copy only)")
		requireNoRawSegment(t, row, "the parent tool result (ForLLM)", res.ForLLM)

		// Wire event: correct code, no raw text.
		var cliPayload *ErrorPayload
		for i := range payloads {
			if payloads[i].Stage == "external_cli" {
				cliPayload = &payloads[i]
				break
			}
		}
		require.NotNil(t, cliPayload, "T-56(a): the external_cli error event must be emitted")
		assert.Equal(t, "external_cli_invalid_args", cliPayload.Code,
			"T-56(a): the duplicated-flag rejection must carry external_cli_invalid_args on the wire event, got %q", cliPayload.Code)
		assert.NotContains(t, cliPayload.Message, "cannot be used multiple times",
			"T-56(a): raw stderr text reached the wire event message")
		wire := WireLLMError(*cliPayload)
		assert.NotContains(t, wire.Message, "cannot be used multiple times",
			"T-56(a): raw stderr text reached the rendered wire frame")
		if wire.Detail != nil {
			requireNoRawSegment(t, row, "the rendered wire frame detail", *wire.Detail)
		}

		// History: the persisted record is clean and carries the same code.
		var errEntry *session.TranscriptEntry
		for i := range entries {
			if entries[i].ErrorCode != "" {
				errEntry = &entries[i]
				break
			}
		}
		require.NotNil(t, errEntry, "T-56(a): the classified error entry must be persisted")
		assert.Equal(t, "external_cli_invalid_args", errEntry.ErrorCode,
			"T-56(a): the persisted error record must carry external_cli_invalid_args, got %q", errEntry.ErrorCode)
		assert.NotContains(t, errEntry.Content, "cannot be used multiple times",
			"T-56(a): raw stderr text persisted in the chat history")
		requireNoRawSegment(t, row, "the persisted transcript", test1857TranscriptBody(entries))
	})

	t.Run("b_nested_envelope_no_raw_correct_code", func(t *testing.T) {
		res, payloads, entries, _, _, _ := driveFatal(t, test1857C2Envelope)

		require.NotNil(t, res)
		requireNoRawSegment(t, "T-56(b) (§13.8.2 C2; FR-092/FR-093)", "the parent tool result", res.ForLLM)

		var cliPayload *ErrorPayload
		for i := range payloads {
			if payloads[i].Stage == "external_cli" {
				cliPayload = &payloads[i]
				break
			}
		}
		require.NotNil(t, cliPayload, "T-56(b): the external_cli error event must be emitted")
		assert.Equal(t, "external_cli_model_unavailable", cliPayload.Code,
			"T-56(b): the nested provider.no-route envelope must carry external_cli_model_unavailable, got %q", cliPayload.Code)
		assert.NotContains(t, cliPayload.Message, "provider.no-route",
			"T-56(b): raw stdout envelope text reached the wire event message")

		for i := range entries {
			assert.NotContains(t, entries[i].Content, "provider.no-route",
				"T-56(b): raw stdout envelope text persisted in the chat history (entry %s)", entries[i].ID)
		}
	})

	t.Run("c_pre_run_start_refusal_correct_code", func(t *testing.T) {
		// A start refusal before the run: the driver factory fails (not the
		// resume sentinel) — the B16-config-gap class (§13.8.5 B16-config,
		// oracle T-56). Driven at the dispatcher boundary: withFakeDriver's
		// seam cannot fail mid-Run, so the drivable pre-run refusal is the
		// driver-construction failure.
		home := t.TempDir()
		t.Setenv(config.EnvHome, home)
		al, ts := newExternalTestLoop(t, "codex", "")
		// Force the driver construction failure (not the resume sentinel —
		// the B16-config class per §13.8.5).
		prev := newExternalDriver
		newExternalDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
			return nil, errors.New("driver construction failed: executor binary missing")
		}
		defer func() { newExternalDriver = prev }()

		collector := &test1857ErrorEvents{}
		al.SetEventSyncTap(collector.tap)
		t.Cleanup(func() { al.SetEventSyncTap(nil) })

		res, err := runExternalCLISubTurn(context.Background(), al, ts, "task")
		require.Error(t, err, "T-56(c): the pre-run refusal must fail the dispatch")
		assert.Nil(t, res, "T-56(c): a pre-run refusal returns no tool result")
		assert.NotContains(t, err.Error(), "sk-proj-Ab3dEf7Gh9Jk2Lm4",
			"T-56(c): no raw segment may ride the refusal")
		// §13.8.5 B16-config: a driver-construction failure (not the resume
		// sentinel) is the config-gap class — external_cli_invalid_config.
		got := TranslateTurnError(err)
		assert.Equal(t, "external_cli_invalid_config", string(got.Code),
			"T-56(c) (§13.8.5 B16-config): the pre-run driver-construction refusal must classify external_cli_invalid_config, got %q (today a plain fmt.Errorf → unknown)", string(got.Code))
	})

	t.Run("d_parent_completion_route_real_failure_path_no_prefix_typed_cause", func(t *testing.T) {
		// MAJ-003: the parent completion message is its own surface, driven
		// here on the REAL failure route — completionDisposition consumes the
		// sub-turn's actual fatal error and completionMessage builds the
		// SessionMessageError — never a prefix inserted by the test.
		// TODO(#1857 E4): exact copy after founder wording review (row d /
		// W-forms for the completion route).
		_, _, _, _, _, runErr := driveFatal(t, test1857C1DuplicateFlag)
		require.Error(t, runErr, "SETUP: the fatal CLI error must fail the run")
		outcome, _, failureReason := completionDisposition(turnResult{status: TurnEndStatusError}, runErr, "partial answer 1857")
		require.Equal(t, steer.OutcomeFailed, outcome, "SETUP: the failed sub-turn lands on the failed outcome")
		al, _ := newGoalLoopTestLoop(t, &noCallProvider{t: t}, nil)
		rec := &session.LifecycleRecord{SessionID: "1857-d-session", AgentID: "ext-agent", Generation: 1}
		msg, msgErr := al.completionMessage(rec, outcome, "partial answer 1857", failureReason)
		require.NoError(t, msgErr, "SETUP: the completion message must build")
		errVariant, asErr := msg.AsSessionMessageError()
		require.NoError(t, asErr, "SETUP: the failed completion must be a SessionMessageError variant")
		text := errVariant.Text
		assert.NotContains(t, text, "external-cli run failed",
			"T-56(d) (§13.8.4 rule 3): the \"external-cli run failed: \" prefix never reaches the parent completion message — the typed cause replaces it (today the real route carries the prefix via the curated turn error)")
		assert.NotContains(t, text, "cannot be used multiple times",
			"T-56(d): raw stderr text reached the parent completion message")
		assert.Contains(t, text, "external_cli_invalid_args",
			"T-56(d) (§13.8.4 parent completion row, MAJ-003): the failure text must carry the typed cause to the parent agent on this route — today it carries the untyped prefix form instead")
		requireNoRawSegment(t, "T-56(d) (§13.8.4 parent completion row)", "the parent completion message", text)
	})

	t.Run("g_nonfatal_diagnostic_no_terminal_line_no_persisted_line", func(t *testing.T) {
		// §13.8.5 C11 / T-56(g): a Fatal:false diagnostic on an otherwise
		// successful run shows no terminal sentence and NO emitted/persisted
		// diagnostic line (the removal asserted). Today the diagnostic is
		// emitted and persisted before the Fatal test (R2-MAJ-005 baseline).
		home := t.TempDir()
		t.Setenv(config.EnvHome, home)
		al, ts := newExternalTestLoop(t, "codex", "")

		st, sessionID := new1857ExternalStore(t, home)
		ts.transcriptStore = st
		ts.transcriptSessionID = sessionID

		collector := &test1857ErrorEvents{}
		al.SetEventSyncTap(collector.tap)
		t.Cleanup(func() { al.SetEventSyncTap(nil) })

		fr, restore := withFakeDriver(t)
		defer restore()
		go func() {
			require.NoError(t, fr.InjectEvent(runner.RunEvent{
				Kind: runner.EventKindError,
				Err:  &runner.ErrorEvent{Message: "opencode: malformed JSON line on stdout", Fatal: false},
			}))
			require.NoError(t, fr.InjectEvent(runner.RunEvent{
				Kind:   runner.EventKindOutput,
				Output: &runner.OutputEvent{Text: "the actual answer"},
			}))
			require.NoError(t, fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd}))
			fr.Cancel()
		}()

		res, runErr := runExternalCLISubTurn(context.Background(), al, ts, "task")
		require.NoError(t, runErr, "T-56(g): the run must succeed — the diagnostic is nonfatal")
		require.NoError(t, st.Close())
		entries, err := st.ReadTranscript(sessionID)
		require.NoError(t, err)

		assert.NotContains(t, res.ForLLM, "malformed JSON",
			"T-56(g): the diagnostic text must not ride the parent tool result")
		assert.NotContains(t, res.ForLLM, "external-cli run failed",
			"T-56(g): a successful run must show no terminal failure sentence")

		for _, p := range collector.errorPayloads() {
			if p.Stage == "external_cli" {
				assert.Fail(t, "T-56(g) (§13.8.5 C11, R2-MAJ-005): the nonfatal diagnostic was still emitted as a user-facing error line on a successful run — the amendment removes this emission (the classified kind stays in gateway.log only)")
			}
		}
		for _, e := range entries {
			assert.NotContains(t, e.Content, "malformed JSON",
				"T-56(g) (§13.8.5 C11): the nonfatal diagnostic persisted into the chat history on a successful run — the persisted diagnostic line is removed by this amendment")
		}
	})

	t.Run("h_logtext_keeps_raw_wire_and_history_do_not", func(t *testing.T) {
		// The one surface for raw text is gateway.log (SanitizeRunnerError's
		// LogText / the slog.Warn's raw_log_text field).
		sanitized := SanitizeRunnerError(test1857C1DuplicateFlag)
		assert.Contains(t, sanitized.LogText, "cannot be used multiple times",
			"T-56(h) (§13.8.4 gateway.log row): gateway.log keeps the raw text")
	})
}
