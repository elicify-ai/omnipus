package tools

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Every test in this file asserts an OUTCOME a caller can observe — the value
// a row carries — rather than that some internal branch was taken. A test that
// asserted "the stalled branch ran" would pass just as happily if that branch
// emitted `running`.

// TestNormalizeStatus_StalledIsBlockedNotRunning is the core US-2 property: a
// plan that is not progressing must never be reported as merely `running`,
// because an agent reading `running` waits, and a stalled plan will wait
// forever.
func TestNormalizeStatus_StalledIsBlockedNotRunning(t *testing.T) {
	stalled := &plan.Plan{State: plan.StateRunning, PlanPhase: plan.PhaseStalled}
	got := normalizePlan(stalled)

	if got.status != jobStatusBlocked {
		t.Fatalf("a stalled plan must report blocked, got %q", got.status)
	}
	if got.status == jobStatusRunning {
		t.Fatalf("a stalled plan reported running — the caller would wait forever")
	}
	if got.attention != attentionCaller {
		t.Errorf("a stalled plan needs the caller's correction: want attention %q, got %q",
			attentionCaller, got.attention)
	}
}

// TestAttention_DerivedPerKind covers all six FR-036 rows.
//
// The final assertion is the one that matters most: if every blocked row
// carried the SAME attention value the field would carry no information at
// all, and a per-row equality check would still pass. Discrimination is the
// property, so it is asserted directly.
func TestAttention_DerivedPerKind(t *testing.T) {
	cases := []struct {
		name          string
		status        string
		attention     string
		wantAttention string
	}{
		{
			name:          "non-blocked row is never flagged",
			status:        normalizePlan(&plan.Plan{State: plan.StateRunning}).status,
			attention:     normalizePlan(&plan.Plan{State: plan.StateRunning}).attention,
			wantAttention: attentionNone,
		},
		{
			name: "dependency-blocked task is informational",
			status: normalizeTask(&task.Task{Status: task.StatusBlocked}).
				status,
			attention: normalizeTask(&task.Task{Status: task.StatusBlocked}).
				attention,
			wantAttention: attentionNone,
		},
		{
			name: "plan awaiting supervision belongs to another principal",
			status: normalizePlan(&plan.Plan{
				State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
			}).status,
			attention: normalizePlan(&plan.Plan{
				State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
			}).attention,
			wantAttention: attentionElsewhere,
		},
		{
			name: "stalled plan needs the caller",
			status: normalizePlan(&plan.Plan{
				State: plan.StateRunning, PlanPhase: plan.PhaseStalled,
			}).status,
			attention: normalizePlan(&plan.Plan{
				State: plan.StateRunning, PlanPhase: plan.PhaseStalled,
			}).attention,
			wantAttention: attentionCaller,
		},
		{
			name: "paused plan needs the caller",
			status: normalizePlan(&plan.Plan{
				State: plan.StateRunning, PausedReason: "waiting on a decision",
			}).status,
			attention: normalizePlan(&plan.Plan{
				State: plan.StateRunning, PausedReason: "waiting on a decision",
			}).attention,
			wantAttention: attentionCaller,
		},
		{
			name: "subagent in needs_input waits on the caller",
			status: normalizeSubagent(&session.LifecycleRecord{
				State: session.LifecycleNeedsInput,
			}).status,
			attention: normalizeSubagent(&session.LifecycleRecord{
				State: session.LifecycleNeedsInput,
			}).attention,
			wantAttention: attentionCaller,
		},
		{
			name: "unmapped state is never asserted to need action",
			status: normalizePlan(&plan.Plan{State: plan.State("wat")}).
				status,
			attention: normalizePlan(&plan.Plan{State: plan.State("wat")}).
				attention,
			wantAttention: attentionNone,
		},
	}

	blockedAttentions := map[string]bool{}
	for _, tc := range cases {
		if tc.attention != tc.wantAttention {
			t.Errorf("%s: want attention %q, got %q", tc.name, tc.wantAttention, tc.attention)
		}
		if tc.status == jobStatusBlocked {
			blockedAttentions[tc.attention] = true
		}
	}

	// A constant would satisfy every per-row check above while telling the
	// caller nothing. The whole point of `attention` is that a blocked row the
	// caller must act on looks different from one it must not touch and one it
	// can ignore.
	if len(blockedAttentions) < 3 {
		t.Fatalf("blocked rows must discriminate: got attention values %v, want all three of none/caller/elsewhere",
			keysOf(blockedAttentions))
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestNativeStatus_ComposedFromConstants asserts byte-equality against the
// exported constants rather than against literals.
//
// This is what makes a rename in pkg/plan or pkg/session land here safely: a
// hand-typed "running/awaiting_supervision" is invisible to the compiler and
// to a rename sweep, so the tool would ship a wrong native_status with every
// gate green.
func TestNativeStatus_ComposedFromConstants(t *testing.T) {
	parked := normalizePlan(&plan.Plan{
		State: plan.StateRunning, PlanPhase: plan.PhaseAwaitingSupervision,
	})
	want := string(plan.StateRunning) + "/" + string(plan.PhaseAwaitingSupervision)
	if parked.nativeStatus != want {
		t.Errorf("plan native_status: want %q, got %q", want, parked.nativeStatus)
	}

	stopped := normalizePlan(&plan.Plan{
		State: plan.StateFailed, FailedReason: plan.FailedReasonStoppedByUser,
	})
	wantStopped := string(plan.StateFailed) + "/" + string(plan.FailedReasonStoppedByUser)
	if stopped.nativeStatus != wantStopped {
		t.Errorf("failed plan native_status: want %q, got %q", wantStopped, stopped.nativeStatus)
	}

	blockedTask := normalizeTask(&task.Task{Status: task.StatusBlocked})
	if blockedTask.nativeStatus != string(task.StatusBlocked) {
		t.Errorf("task native_status: want %q, got %q",
			string(task.StatusBlocked), blockedTask.nativeStatus)
	}

	needsInput := normalizeSubagent(&session.LifecycleRecord{State: session.LifecycleNeedsInput})
	if needsInput.nativeStatus != string(session.LifecycleNeedsInput) {
		t.Errorf("subagent native_status: want %q, got %q",
			string(session.LifecycleNeedsInput), needsInput.nativeStatus)
	}
}

// TestNativeStatus_AwaitingSupervisionOutranksStalled pins the precedence.
// Getting it backwards would tell a plan Owner to intervene on an adjudication
// they are forbidden from touching — whose only available verb is stop_plan.
func TestNativeStatus_AwaitingSupervisionOutranksStalled(t *testing.T) {
	// A record carrying a paused reason AND the parked phase: the parked
	// reading must win on both fields.
	got := normalizePlan(&plan.Plan{
		State:        plan.StateRunning,
		PlanPhase:    plan.PhaseAwaitingSupervision,
		PausedReason: "someone set this too",
	})
	if got.attention != attentionElsewhere {
		t.Fatalf("awaiting_supervision must outrank a paused reason: got attention %q", got.attention)
	}
	if !strings.Contains(got.nativeStatus, string(plan.PhaseAwaitingSupervision)) {
		t.Fatalf("native_status must name the parked phase, got %q", got.nativeStatus)
	}
}

// TestNormalizeStatus_UnmappedNativeState covers a record written by a newer
// build or hand-edited on disk. It must not panic, must not vanish, and must
// not be silently coerced to `failed` — an invented terminal would tell the
// caller its work is dead.
func TestNormalizeStatus_UnmappedNativeState(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  struct {
			status, native string
			unmapped       bool
		}
	}{
		{"plan", asTriple(normalizePlan(&plan.Plan{State: plan.State("wat")}).status,
			normalizePlan(&plan.Plan{State: plan.State("wat")}).nativeStatus,
			normalizePlan(&plan.Plan{State: plan.State("wat")}).unmapped)},
		{"task", asTriple(normalizeTask(&task.Task{Status: task.Status("wat")}).status,
			normalizeTask(&task.Task{Status: task.Status("wat")}).nativeStatus,
			normalizeTask(&task.Task{Status: task.Status("wat")}).unmapped)},
		{"subagent", asTriple(normalizeSubagent(&session.LifecycleRecord{State: session.LifecycleState("")}).status,
			normalizeSubagent(&session.LifecycleRecord{State: session.LifecycleState("")}).nativeStatus,
			normalizeSubagent(&session.LifecycleRecord{State: session.LifecycleState("")}).unmapped)},
	} {
		if tc.got.status != jobStatusBlocked {
			t.Errorf("%s: unmapped state must report blocked, got %q", tc.name, tc.got.status)
		}
		if tc.got.status == jobStatusFailed {
			t.Errorf("%s: unmapped state was coerced to failed — the caller would abandon live work", tc.name)
		}
		if !strings.HasPrefix(tc.got.native, unknownNativeStatusPrefix) {
			t.Errorf("%s: native_status must be marked unknown, got %q", tc.name, tc.got.native)
		}
		if !tc.got.unmapped {
			t.Errorf("%s: unmapped must be flagged so the caller's counter increments", tc.name)
		}
	}
}

func asTriple(status, native string, unmapped bool) struct {
	status, native string
	unmapped       bool
} {
	return struct {
		status, native string
		unmapped       bool
	}{status, native, unmapped}
}

// TestIntentionallyStopped_DerivedFromClosedEnums pins the cancelled-vs-crashed
// distinction. An agent that stopped a delegation on purpose and then lost
// context must not re-dispatch work the user deliberately cancelled.
func TestIntentionallyStopped_DerivedFromClosedEnums(t *testing.T) {
	cancelled := normalizeSubagent(&session.LifecycleRecord{State: session.LifecycleStopped})
	if !cancelled.stopped {
		t.Error("a cancelled session must report intentionally_stopped=true")
	}
	// Oracle change (founder rule 2026-10-06, a session does not fail because
	// of a restart): failed_reason "interrupted" no longer normalises to
	// failed (see TestNormalizeSubagent_RestartInterruptedIsNotFailed). The
	// "crashed" example is a genuine failure instead; the cancelled-vs-crashed
	// distinction this test pins is unchanged.
	crashed := normalizeSubagent(&session.LifecycleRecord{
		State: session.LifecycleFailed, FailedReason: "judge_rounds_exhausted",
	})
	if crashed.stopped {
		t.Error("a crashed session must report intentionally_stopped=false")
	}
	if cancelled.status != crashed.status {
		t.Fatalf("both normalize to failed; the boolean is what distinguishes them")
	}

	stoppedPlan := normalizePlan(&plan.Plan{
		State: plan.StateFailed, FailedReason: plan.FailedReasonStoppedByUser,
	})
	if !stoppedPlan.stopped {
		t.Error("a user-stopped plan must report intentionally_stopped=true")
	}
	// dod_unreachable is written by BOTH a deliberate adjudicated abandon and
	// the involuntary cannot-progress path, and nothing on the record
	// separates them. false is the deliberate choice: a false `true` would
	// suppress a legitimate re-dispatch of work nobody stopped.
	ambiguous := normalizePlan(&plan.Plan{
		State: plan.StateFailed, FailedReason: plan.FailedReasonDoDUnreachable,
	})
	if ambiguous.stopped {
		t.Error("dod_unreachable must report false — it is not a reliable stop signal")
	}
}

// TestNormalizeSubagent_RedirectPauseIsDistinctFromFailed is a regression pin.
//
// Before the lifecycle-state collapse (commit 4c59cd17a, "replace paused and
// cancelled states with stopped"), `normalizeSubagent` had a DEDICATED
// `session.LifecyclePaused` branch reporting `jobStatusBlocked`/
// `attentionCaller` — see `git show 6fd215138^:pkg/tools/list_jobs_row.go`,
// which still carried that branch verbatim, and
// docs/internal/specs/list-jobs-spec.md's operator-ruling-3 table (line 161):
// "A subagent waiting on an answer | state ∈ {needs_input, paused} | caller |
// Yes. It is blocked on input only the caller can supply." That table's
// `needs_input` half still has its dedicated branch today
// (`session.LifecycleNeedsInput` -> jobStatusBlocked/attentionCaller,
// unchanged); only the `paused` half was lost.
//
// Commit 6fd215138 ("route stopped turns through lifecycle callers") folded
// `LifecyclePaused` into the unconditional `LifecycleStopped ->
// jobStatusFailed, stopped:true` branch with no replacement. But
// `LifecycleStopped`'s own doc comment (pkg/session/lifecycle.go) says it
// "covers cancellation, timeout, AND a plan-owner session idling while its
// plan is durably awaiting_supervision" — i.e. the old `paused` condition is
// still one of the things that can land a session at `LifecycleStopped`
// today, and `stop_note.cause` (pkg/session/lifecycle_edge.go) is the
// mechanism the sub-agent control-plane ADR added specifically so a caller
// can still tell them apart: "stop_note {..., cause: stop/redirect_pause/
// cascade/restart/timeout} ... merges the prior paused, cancelled, and
// restart-only interrupted outcomes" (ADR-20260928-sub-agent-control-plane,
// Vocabulary table, `stopped` row). Of the five causes, `redirect_pause` is
// documented as "a fresh instruction pausing the CURRENT generation ...
// rather than stopping the session outright"
// (session.StopCauseRedirectPause's own doc comment) — the one that means
// "parked, not stopped", matching the old `paused` semantics this test pins.
//
// Without this fix, a legitimately-idling plan-owner (the same case
// pkg/agent/boot_sweep.go's exemption (b) still explicitly spares from its
// own sweep) is reported to a list_jobs caller as "failed / intentionally
// stopped" — indistinguishable from a real cancel — and an agent that lost
// context could re-dispatch work nobody actually stopped.
func TestNormalizeSubagent_RedirectPauseIsDistinctFromFailed(t *testing.T) {
	paused := normalizeSubagent(&session.LifecycleRecord{
		State:    session.LifecycleStopped,
		StopNote: &session.StopNote{Cause: session.StopCauseRedirectPause},
	})

	if paused.status == jobStatusFailed {
		t.Fatalf("a redirect_pause stop must NOT report status=%q — that is "+
			"indistinguishable from a genuine stop and loses exactly the "+
			"distinction stop_note.cause exists to preserve; got status=%q attention=%q",
			jobStatusFailed, paused.status, paused.attention)
	}
	// jobStatusBlocked/attentionCaller are pre-existing FR-006 constants
	// (this file's own vocabulary comment: "Exactly five values — the
	// vocabulary is NOT extended"), referenced here, not invented — they are
	// the exact pair the pre-collapse LifecyclePaused branch reported, and
	// the exact pair session.LifecycleNeedsInput's still-intact sibling
	// branch reports today for the same list-jobs-spec.md table row.
	if paused.status != jobStatusBlocked {
		t.Errorf("want status=%q (matching the needs_input sibling case and the "+
			"pre-collapse LifecyclePaused mapping), got %q", jobStatusBlocked, paused.status)
	}
	if paused.attention != attentionCaller {
		t.Errorf("a redirect-paused session is actionable by the caller that issued "+
			"the redirect: want attention=%q, got %q", attentionCaller, paused.attention)
	}
	if paused.stopped {
		t.Error("a redirect_pause stop must not report intentionally_stopped=true — " +
			"nothing was stopped, the current generation was merely paused")
	}

	// Positive control: a genuine direct stop is UNCHANGED by this fix. This
	// repeats TestIntentionallyStopped_DerivedFromClosedEnums's cancel/crash
	// pin, but WITH an explicit stop_note, so a fix that special-cases "any
	// StopNote present" instead of "cause == redirect_pause" cannot flip this
	// case too and still pass.
	genuineStop := normalizeSubagent(&session.LifecycleRecord{
		State:    session.LifecycleStopped,
		StopNote: &session.StopNote{Cause: session.StopCauseStop},
	})
	if genuineStop.status != jobStatusFailed {
		t.Errorf("a direct stop must still report status=%q, got %q",
			jobStatusFailed, genuineStop.status)
	}
	if !genuineStop.stopped {
		t.Error("a direct stop must still report intentionally_stopped=true")
	}
	if genuineStop.attention != attentionNone {
		t.Errorf("a direct stop carries no caller action: want attention=%q, got %q",
			attentionNone, genuineStop.attention)
	}

	// The property that matters most: the two causes must never collapse
	// onto the same status, or the distinction stop_note.cause exists to
	// preserve is gone regardless of which literal values either side uses.
	if paused.status == genuineStop.status {
		t.Fatalf("redirect_pause and a genuine stop must report different statuses; "+
			"both report %q", paused.status)
	}
}

// TestNormalizeSubagent_TimeoutIsNotIntentionallyStopped is a second
// regression pin on the same collapsed LifecycleStopped branch this file's
// redirect_pause test covers: pre-collapse, `LifecycleTimedOut` (see `git show
// 6fd215138^:pkg/tools/list_jobs_row.go`) reported status=failed but left
// `stopped` at its zero value — only the dedicated `LifecycleCancelled`
// branch set stopped=true. A session's own execution-budget timeout
// (session.StopCauseTimeout) is not a deliberate cancel, so it must not be
// reported as intentionally_stopped=true: a caller that saw that flag could
// wrongly conclude a human or agent chose to cancel the work, when in fact
// nobody did.
func TestNormalizeSubagent_TimeoutIsNotIntentionallyStopped(t *testing.T) {
	timedOut := normalizeSubagent(&session.LifecycleRecord{
		State:    session.LifecycleStopped,
		StopNote: &session.StopNote{Cause: session.StopCauseTimeout},
	})
	if timedOut.status != jobStatusFailed {
		t.Errorf("a timed-out session must still report status=%q, got %q",
			jobStatusFailed, timedOut.status)
	}
	if timedOut.stopped {
		t.Error("a timeout must not report intentionally_stopped=true — nobody " +
			"deliberately cancelled this session, its own execution budget expired")
	}

	// Positive control, same shape as the redirect_pause test above: a
	// genuine direct stop is UNCHANGED by this fix.
	genuineStop := normalizeSubagent(&session.LifecycleRecord{
		State:    session.LifecycleStopped,
		StopNote: &session.StopNote{Cause: session.StopCauseStop},
	})
	if !genuineStop.stopped {
		t.Error("a direct stop must still report intentionally_stopped=true")
	}
}

// TestLabel_RedactBeforeTruncate proves the ORDER, not just that both steps
// happen. Truncating first can split a registered secret across the boundary
// so the replacer no longer matches it, which is exactly the leak the pipeline
// exists to prevent.
func TestLabel_RedactBeforeTruncate(t *testing.T) {
	const secret = "sk-live-0123456789abcdefghijklmnop"
	cfg := &config.Config{}
	cfg.Tools.FilterSensitiveData = true
	cfg.RegisterSensitiveValues([]string{secret})
	red := newRedactor(cfg)

	// Position the secret so a truncate-first implementation would cut it in
	// half and leave the tail visible.
	label := strings.Repeat("a", labelMaxRunes-10) + secret + " trailing"
	got := truncateField(red.redact(label), labelMaxRunes, labelMaxBytes)

	if strings.Contains(got, secret) {
		t.Fatalf("full secret leaked into the label: %q", got)
	}
	for i := 0; i+8 <= len(secret); i++ {
		if window := secret[i : i+8]; strings.Contains(got, window) {
			t.Fatalf("8-byte window %q of the secret leaked into the label: %q", window, got)
		}
	}
}

// TestLabel_ShortSecretBelowFilterMinLength is the bypass case.
//
// config.Config.FilterSensitiveData returns content UNCHANGED when it is
// shorter than FilterMinLength (default 8, a BYTE comparison), so a 7-byte
// ASCII secret is never filtered by that helper. The corpus is fixed and
// explicitly mixes byte lengths, because a test written over CJK characters
// would never exercise the bypass at all — 3 CJK runes are 9 bytes and ARE
// filtered.
func TestLabel_ShortSecretBelowFilterMinLength(t *testing.T) {
	const shortASCII = "hunter2" // 7 BYTES — below the gate
	// The Han corpus is the assertion, not user-facing copy: 3 CJK runes are
	// 9 BYTES and therefore are NOT bypassed, which is exactly the contrast
	// with the 7-byte ASCII case above. Replacing it with ASCII would delete
	// half of what this test proves.
	const shortCJK = "秘密鍵"

	cfg := &config.Config{}
	cfg.Tools.FilterSensitiveData = true
	cfg.RegisterSensitiveValues([]string{shortASCII, shortCJK})

	// Establish that the bypass this test exists for is real, so the test
	// cannot quietly stop testing anything if the helper changes.
	if cfg.FilterSensitiveData(shortASCII) != shortASCII {
		t.Fatalf("precondition changed: FilterSensitiveData no longer bypasses short content")
	}

	red := newRedactor(cfg)
	for _, secret := range []string{shortASCII, shortCJK} {
		if got := truncateField(red.redact(secret), labelMaxRunes, labelMaxBytes); strings.Contains(got, secret) {
			t.Errorf("short secret %q leaked through the label path: %q", secret, got)
		}
	}
}

// TestLabel_FilterDisabledStillBounded: an operator disabling redaction must
// not be able to disable the length bound with it.
func TestLabel_FilterDisabledStillBounded(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.FilterSensitiveData = false
	red := newRedactor(cfg)

	got := truncateField(red.redact(strings.Repeat("x", 10_000)), labelMaxRunes, labelMaxBytes)
	if n := len([]rune(got)); n > labelMaxRunes {
		t.Errorf("label exceeded the rune bound with filtering disabled: %d runes", n)
	}
	if n := jsonEncodedLen(got); n > labelMaxBytes {
		t.Errorf("label exceeded the byte bound with filtering disabled: %d bytes", n)
	}
}

// TestRedactor_NilConfigStillBounded: no config handle means no redaction, but
// a bound must never depend on whether redaction happened.
func TestRedactor_NilConfigStillBounded(t *testing.T) {
	red := newRedactor(nil)
	got := truncateField(red.redact(strings.Repeat("y", 10_000)), nativeStatusMaxRunes, nativeStatusMaxBytes)
	if n := len([]rune(got)); n > nativeStatusMaxRunes {
		t.Errorf("native_status exceeded the rune bound with a nil config: %d runes", n)
	}
	if n := jsonEncodedLen(got); n > nativeStatusMaxBytes {
		t.Errorf("native_status exceeded the byte bound with a nil config: %d bytes", n)
	}
}

// TestTruncateField_RuneBoundaryAndByteBound asserts both bounds hold
// simultaneously and that no rune is ever split — a split rune produces
// invalid UTF-8, which breaks the JSON encoding of the whole response.
func TestTruncateField_RuneBoundaryAndByteBound(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"ascii", strings.Repeat("a", 5_000)},
		// A 3-byte-rune corpus proves the rune bound and the byte bound are
		// enforced independently.
		{"cjk 3-byte runes", strings.Repeat("漢", 5_000)},
		{"emoji 4-byte runes", strings.Repeat("🐙", 5_000)},
		{"json-hostile", strings.Repeat(`"\`+"\n", 5_000)},
	} {
		got := truncateField(tc.input, labelMaxRunes, labelMaxBytes)
		if n := len([]rune(got)); n > labelMaxRunes {
			t.Errorf("%s: %d runes exceeds the rune bound %d", tc.name, n, labelMaxRunes)
		}
		if n := jsonEncodedLen(got); n > labelMaxBytes {
			t.Errorf("%s: %d encoded bytes exceeds the byte bound %d", tc.name, n, labelMaxBytes)
		}
		if !strings.HasPrefix(tc.input, got) {
			t.Errorf("%s: truncation must yield a prefix of the input", tc.name)
		}
		if strings.ContainsRune(got, '�') && !strings.ContainsRune(tc.input, '�') {
			t.Errorf("%s: truncation split a rune", tc.name)
		}
	}
}

// TestTruncateField_ShortInputUntouched guards against an over-eager bound.
func TestTruncateField_ShortInputUntouched(t *testing.T) {
	const label = "Migrate the audit chain to HMAC"
	if got := truncateField(label, labelMaxRunes, labelMaxBytes); got != label {
		t.Errorf("a short label must pass through unchanged: want %q, got %q", label, got)
	}
	if got := truncateField("", labelMaxRunes, labelMaxBytes); got != "" {
		t.Errorf("an empty label must stay empty, got %q", got)
	}
}
