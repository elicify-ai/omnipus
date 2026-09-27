package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

type activeRequestCountingProvider struct {
	calls atomic.Int64
}

func (p *activeRequestCountingProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (*activeRequestCountingProvider) GetDefaultModel() string { return "test-model" }

// A turn can be between provider calls when shutdown starts. The next call
// must be refused before it reaches the provider; otherwise an ungated
// request registration can race the shutdown waiter or outlive store teardown.
func TestActiveRequests_StopRejectsProviderCallBetweenRounds(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)
	provider := &activeRequestCountingProvider{}
	agent := &AgentInstance{ID: "test-agent", Provider: provider}
	al := &AgentLoop{}
	rt := &agentLoopRunTurn{
		al:             al,
		ts:             newTurnState(agent, processOptions{}, turnEventScope{}),
		turnCtx:        context.Background(),
		activeProvider: provider,
		llmModel:       provider.GetDefaultModel(),
	}

	if _, err := rt.callProviderOnce(nil, nil); err != nil {
		t.Fatalf("first provider call: %v", err)
	}
	al.Stop()

	if _, err := rt.callProviderOnce(nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("provider call after Stop = %v, want context.Canceled", err)
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1; shutdown admitted a call between rounds", got)
	}
	logs := readLog()
	if got := strings.Count(logs, `"site":"callProviderOnce"`); got != 1 {
		t.Fatalf("refused provider admission WARNs naming callProviderOnce = %d, want 1; logs:\n%s", got, logs)
	}
}

func TestActiveRequests_DrainClosesIntakeAndSignalsLastCompletion(t *testing.T) {
	al := &AgentLoop{}
	if !al.beginActiveRequest() {
		t.Fatal("initial request was refused before shutdown")
	}

	drained, alreadyWaiting := al.activeRequests.startWait()
	if alreadyWaiting {
		t.Fatal("first drain was reported as an existing wait")
	}
	if al.beginActiveRequest() {
		t.Fatal("request intake remained open after drain started")
	}
	select {
	case <-drained:
		t.Fatal("drain completed while one request remained active")
	default:
	}

	al.endActiveRequest()
	select {
	case <-drained:
	default:
		t.Fatal("last completion did not signal the drain")
	}
}

func TestActiveRequests_CanceledGatewayWaitIsNotRepeatedByClose(t *testing.T) {
	al := &AgentLoop{}
	if !al.beginActiveRequest() {
		t.Fatal("initial request was refused before shutdown")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if al.WaitForActiveRequestsContext(ctx) {
		t.Fatal("canceled gateway wait reported a completed drain")
	}
	_, alreadyWaiting := al.activeRequests.startWait()
	if !alreadyWaiting {
		t.Fatal("Close would repeat the gateway's already-attempted drain")
	}

	al.endActiveRequest()
	if !al.WaitForActiveRequestsContext(context.Background()) {
		t.Fatal("completed request did not close the existing drain signal")
	}
}

// Site-level wiring of the four admission sites the tracker-level tests above
// cannot see (PTA review of #961, finding G2). Plan, with the spec each
// expectation derives from — never the implementation:
//
//	Control phase     active_requests.go's tracker header: every done must be
//	                  paired with a successful, begin-gated admission. A site
//	                  called while intake is open admits exactly one tracked
//	                  request, and the tracker itself joins the spawned work:
//	                  WaitForActiveRequestsContext returns true only once that
//	                  goroutine's done() landed. This phase is the instrument
//	                  check — without it a site that never dispatches at all
//	                  would pass the refusal half vacuously.
//	Refusal phase     The #961 follow-up (commit 8805c7797) mandates a WARN
//	                  naming the site at every refused admission, logged
//	                  synchronously on the caller's goroutine; the refusal
//	                  leaves nothing behind (PTA G2: no goroutine, no work, no
//	                  admission), and intake stays closed (Stop is the
//	                  production intake-close).
//	Work signatures   Each site's spawned work has one log signature that any
//	                  execution of that goroutine produces (its supervising
//	                  recover, or its first statement). Absence after the
//	                  refusal is the "no work ran" oracle.
//	Board task        task_executor.go::ExecuteBoardTask's documented shutdown
//	                  contract: a refused dispatch calls onComplete with the
//	                  cancellation error — exactly ("", context.Canceled),
//	                  synchronously, exactly once (finding G4).
//
// Zero-value AgentLoop on purpose: the tracker's zero value is contractually
// ready (active_requests.go header), the refusal path touches nothing else,
// and the control phase measures admission+drain pairing, not work success.
//
// Deliberate gap: a hypothetical mutant that moves begin() INSIDE the spawned
// goroutine (refused there) is behaviourally equivalent to the refusal — no
// work, count 0 — and no deterministic oracle distinguishes that no-op spawn.
// The harmful mutants the review names (gate dropped; begin after the go
// statement with the work already dispatched) both die on the synchronous
// oracles plus the tracker's underflow tripwire; each test below was
// mutation-probed against them.
func TestActiveRequests_LaunchSystemMessageRefusesAdmissionAfterIntakeCloses(t *testing.T) {
	al := &AgentLoop{}
	msg := bus.InboundMessage{
		Channel:   "system",
		ChatID:    "webchat:chat-ar",
		Content:   "Task 'ar' completed.\n\nResult:\ndone",
		SessionID: "sess-ar",
	}
	activeRequestsControlJoin(t, al, func() {
		al.launchSystemMessage(context.Background(), msg)
	}, "launchSystemMessage")

	al.Stop()
	readLog := captureLogFile(t, logger.INFO)
	al.launchSystemMessage(context.Background(), msg)
	logs := readLog()
	activeRequestsAssertRefused(t, al, logs, "launchSystemMessage",
		"Processing system message", "Panic in system-message goroutine")
}

func TestActiveRequests_LaunchUnroutableMessageRefusesAdmissionAfterIntakeCloses(t *testing.T) {
	al := &AgentLoop{}
	msg := bus.InboundMessage{
		Channel:    "webchat",
		ChatID:     "chat-ar",
		Content:    "hello",
		SessionID:  "sess-ar",
		SessionKey: "agent:test-agent:session:sess-ar",
	}
	activeRequestsControlJoin(t, al, func() {
		al.launchUnroutableMessage(context.Background(), msg)
	}, "launchUnroutableMessage")

	al.Stop()
	readLog := captureLogFile(t, logger.INFO)
	al.launchUnroutableMessage(context.Background(), msg)
	logs := readLog()
	activeRequestsAssertRefused(t, al, logs, "launchUnroutableMessage",
		"Panic in unroutable-message goroutine")
}

func TestActiveRequests_RunRevivedOrdinaryTurnRefusesAdmissionAfterIntakeCloses(t *testing.T) {
	al := &AgentLoop{}
	msg := bus.InboundMessage{
		Channel:   "webchat",
		ChatID:    "chat-ar",
		Content:   "carry on",
		SessionID: "sess-ar",
	}
	activeRequestsControlJoin(t, al, func() {
		al.runRevivedOrdinaryTurn(msg, "agent:test-agent:session:sess-ar")
	}, "runRevivedOrdinaryTurn")

	al.Stop()
	readLog := captureLogFile(t, logger.INFO)
	al.runRevivedOrdinaryTurn(msg, "agent:test-agent:session:sess-ar")
	logs := readLog()
	activeRequestsAssertRefused(t, al, logs, "runRevivedOrdinaryTurn",
		"Panic in revived ordinary-root turn goroutine")
}

// TestActiveRequests_ExecuteBoardTaskRefusalNotifiesOnCompleteWithCanceled is
// the board-task site test (G2) and the onComplete contract test (G4) in one:
// the refusal must notify the caller exactly once, with exactly the empty
// result and context.Canceled, synchronously before ExecuteBoardTask returns
// (task_executor.go::ExecuteBoardTask's documented shutdown behaviour — the
// task transitions to failed, never stuck "active" past shutdown).
func TestActiveRequests_ExecuteBoardTaskRefusalNotifiesOnCompleteWithCanceled(t *testing.T) {
	al := &AgentLoop{}
	activeRequestsControlJoin(t, al, func() {
		al.ExecuteBoardTask("agent-ar", "task-ar", "sess-ar", "do the thing", nil)
	}, "ExecuteBoardTask")

	al.Stop()
	readLog := captureLogFile(t, logger.INFO)
	completions := make(chan string, 1)
	errs := make(chan error, 1)
	al.ExecuteBoardTask("agent-ar", "task-ar", "sess-ar", "do the thing", func(result string, err error) {
		completions <- result
		errs <- err
	})
	logs := readLog()
	activeRequestsAssertRefused(t, al, logs, "ExecuteBoardTask",
		"ExecuteBoardTask: dispatching")
	select {
	case result := <-completions:
		if result != "" {
			t.Fatalf("refused board task onComplete result = %q, want exactly an empty result", result)
		}
	case <-time.After(time.Second):
		t.Fatal("onComplete was never called — a refused board task would stay status active with no terminal transition")
	}
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("refused board task onComplete err = %v, want context.Canceled", err)
		}
	default:
		t.Fatal("onComplete delivered a result without an error — refusal notification is not atomic")
	}
}

// activeRequestsControlJoin is the refusal tests' instrument check: it calls
// site once with intake still open and joins the site's spawned work through
// the tracker itself — WaitForActiveRequestsContext returns true only once the
// spawned goroutine's done() landed, which is a happens-before for everything
// that goroutine did. A site that stopped dispatching entirely fails here
// instead of letting the refusal half below pass vacuously. 5s bound: the
// control work runs against an unwired loop and fails fast; the bound only
// has to absorb -race scheduler jitter, never real work.
func activeRequestsControlJoin(t *testing.T, al *AgentLoop, site func(), label string) {
	t.Helper()
	site()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !al.WaitForActiveRequestsContext(ctx) {
		t.Fatalf("%s: the site admitted no tracked request while intake was open (5s join timeout) — the refusal half of this test would pass vacuously", label)
	}
	if n := al.activeRequests.active; n != 0 {
		t.Fatalf("%s: tracker count after the control drain = %d, want 0 — begin/done are not paired around the dispatch", label, n)
	}
}

// activeRequestsAssertRefused is the refusal phase's oracle: exactly one WARN
// naming the site (#961 follow-up commit 8805c7797: a WARN at every refused
// admission), nothing left behind (tracker count 0), intake still closed
// (Stop is the production close), and none of siteSignatures — the log lines
// any execution of the site's spawned work would produce — present. All of
// these are synchronous on the caller's goroutine except the signatures: the
// correct code spawns nothing, so reading them is race-free; a mutant's rogue
// goroutine additionally trips the tracker's underflow tripwire on its
// unpaired done().
func activeRequestsAssertRefused(t *testing.T, al *AgentLoop, logs, site string, siteSignatures ...string) {
	t.Helper()
	if got := strings.Count(logs, fmt.Sprintf(`"site":"%s"`, site)); got != 1 {
		t.Fatalf("refused-admission WARNs naming %s = %d, want 1; logs:\n%s", site, got, logs)
	}
	if n := al.activeRequests.active; n != 0 {
		t.Fatalf("%s: tracker count after refusal = %d, want 0 — an admission leaked past the closed intake", site, n)
	}
	if al.beginActiveRequest() {
		t.Fatalf("%s: request intake was open again after the refused admission — Stop's close did not hold", site)
	}
	for _, signature := range siteSignatures {
		if got := strings.Count(logs, signature); got != 0 {
			t.Fatalf("%s: refused admission ran work anyway — %d hit(s) of work signature %q; logs:\n%s", site, got, signature, logs)
		}
	}
}

// waitActiveRequestsDrain direct tests (PTA review of #961, finding G3) —
// loop.go::waitActiveRequestsDrain's documented contract, not its code:
//
//	Already-waiting skip   Close must not stack its own budget behind a
//	                       gateway wait that already ran (#952-SFH-3's 65s+30s
//	                       shutdown stacking). A prior WaitForActiveRequests
//	                       Context — the production caller, here driven to
//	                       its canceled-ctx outcome — must make the next
//	                       drain return without waiting out a budget and
//	                       without a budget-exceeded warning.
//	Budget expiry          With an admission held and no prior wait, the
//	                       drain returns after approximately the budget it
//	                       was given (not instantly, not after a full
//	                       shutdown budget), warns exactly once carrying
//	                       that budget, and — the property that keeps the
//	                       warning benign — the drain is still signalled as
//	                       soon as the held request ends. All waits are
//	                       channel-driven; the short budget is the one
//	                       permitted timing element, and the elapsed bounds
//	                       are wide: Go timers never fire early, and 5s
//	                       absorbs -race scheduler jitter.
func TestActiveRequests_DrainSkipsDuplicateWaitAfterGatewayWait(t *testing.T) {
	al := &AgentLoop{}
	if !al.beginActiveRequest() {
		t.Fatal("initial request was refused before shutdown")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if al.WaitForActiveRequestsContext(ctx) {
		t.Fatal("canceled gateway wait reported a completed drain")
	}

	readLog := captureLogFile(t, logger.WARN)
	al.waitActiveRequestsDrain(2 * time.Second)
	logs := readLog()
	if got := strings.Count(logs, "active-request drain budget exceeded"); got != 0 {
		t.Fatalf("already-waiting drain logged %d budget-exceeded warning(s), want 0 — Close stacked its own budget behind the gateway's wait (#952-SFH-3); logs:\n%s", got, logs)
	}

	al.endActiveRequest()
	if !al.WaitForActiveRequestsContext(context.Background()) {
		t.Fatal("completed request did not signal the drain the skipped wait left behind")
	}
}

func TestActiveRequests_DrainBudgetExpiryWarnsAndStillSignalsLater(t *testing.T) {
	al := &AgentLoop{}
	if !al.beginActiveRequest() {
		t.Fatal("initial request was refused before shutdown")
	}

	readLog := captureLogFile(t, logger.WARN)
	const budget = 50 * time.Millisecond
	start := time.Now()
	al.waitActiveRequestsDrain(budget)
	elapsed := time.Since(start)
	logs := readLog()

	if elapsed < budget {
		t.Fatalf("drain returned after %s, before its %s budget elapsed — the expiry branch did not wait", elapsed, budget)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("drain returned after %s for a %s budget — the budget was not honoured (a full shutdown budget stacked behind a still-active request)", elapsed, budget)
	}
	if got := strings.Count(logs, "active-request drain budget exceeded"); got != 1 {
		t.Fatalf("expired drain logged %d budget-exceeded warning(s), want exactly 1 — the timeout must be warned, not silent; logs:\n%s", got, logs)
	}
	if !strings.Contains(logs, `"budget":"50ms"`) {
		t.Fatalf("budget-exceeded warning does not echo the caller's 50ms budget — the helper may be ignoring its parameter; logs:\n%s", logs)
	}

	al.endActiveRequest()
	if !al.WaitForActiveRequestsContext(context.Background()) {
		t.Fatal("the expired drain was left unsignalled after the held request ended — later Close-time waiters would hang")
	}
}
