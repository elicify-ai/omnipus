package gateway

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

// steeredLeakCheckEnv switches the leak detector on. It is OFF by default so CI
// pays nothing for it (one all-goroutine stack scan per test, about 1.3 to 2 ms).
// Turn it on to name the test that leaves a steered turn running:
//
//	OMNIPUS_STEERED_LEAK_CHECK=1 go test -tags goolm,stdjson -p 1 -run <names> ./pkg/gateway
const steeredLeakCheckEnv = "OMNIPUS_STEERED_LEAK_CHECK"

// closeLoopAtTestEnd registers the test's agent-loop shutdown: a plain Close,
// or Close plus the leak check when steeredLeakCheckEnv is "1".
func closeLoopAtTestEnd(t *testing.T, al *agent.AgentLoop) {
	t.Helper()
	t.Cleanup(func() {
		if os.Getenv(steeredLeakCheckEnv) == "1" {
			closeAndAssertNoSteeredTurns(t, al)
			return
		}
		al.Close()
	})
}

// closeAndAssertNoSteeredTurns closes the loop, then fails the test that owns it
// if one of ITS steered turns (pkg/agent/admission.go goSteeredTurn) is still
// running. Close drains steered turns within a budget and then proceeds, so a
// turn that outlives it keeps reading shared state while the next test runs
// (#1821, the cause behind the #1819 race). The failure names the test and the
// leaked turn's stack instead of surfacing later in an unrelated test.
//
// Ownership: goSteeredTurn.func1 itself carries no arguments, but the frames of
// a real turn below it (runDispatchedSteeredTurn, runTurn, ...) are methods of
// the owning *AgentLoop, whose receiver the runtime prints as the same pointer
// text fmt prints for %p. A goroutine counts only when a (*AgentLoop) method
// frame shows this loop's pointer as its receiver; the pointer as a plain
// argument of some other function (a parent's helper holding a child loop) does
// not count. The check can miss a turn whose frames print no receiver; a turn
// of loop A that calls a method on loop B would be blamed on B, which no
// gateway test does today.
func closeAndAssertNoSteeredTurns(t *testing.T, al *agent.AgentLoop) {
	t.Helper()
	al.Close()
	owner := fmt.Sprintf("%p", al)
	deadline := time.Now().Add(2 * time.Second)
	for {
		leaked := liveSteeredTurnStacks(owner)
		if len(leaked) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("#1821: %d steered turn(s) of this test's agent loop (%s) still running after Close "+
				"(the test must cancel or finish them before cleanup):\n%s",
				len(leaked), owner, strings.Join(leaked, "\n---\n"))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// liveSteeredTurnStacks returns the stacks of live goroutines started by
// AgentLoop.goSteeredTurn whose AgentLoop method frames have receiver owner.
func liveSteeredTurnStacks(owner string) []string {
	buf := make([]byte, 1<<22)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if steeredTurnOwnedBy(g, owner) {
			out = append(out, g)
		}
	}
	return out
}

// agentLoopMethodFrame matches the first argument of a (*AgentLoop) method
// frame: "pkg/agent.(*AgentLoop).name(0xRECEIVER," or a closure of it
// ("...name.func1(0xRECEIVER"). Group 1 is the receiver text, which the
// runtime may mark with a trailing "?".
var agentLoopMethodFrame = regexp.MustCompile(`\(\*AgentLoop\)\.[A-Za-z0-9_.]+\((0x[0-9a-f]+)`)

// steeredTurnOwnedBy reports whether one goroutine's stack text is a steered
// turn (created through goSteeredTurn) whose (*AgentLoop) receiver is owner.
func steeredTurnOwnedBy(goroutineStack, owner string) bool {
	if !strings.Contains(goroutineStack, "goSteeredTurn.func1") {
		return false
	}
	for _, m := range agentLoopMethodFrame.FindAllStringSubmatch(goroutineStack, -1) {
		if m[1] == owner {
			return true
		}
	}
	return false
}

// TestSteeredTurnOwnedBy_ReceiverNotBorrowedArgument pins the ownership rule:
// a parent's helper that merely holds a child loop as an argument is not the
// child's turn (CHECK #1821 round 2), while a real receiver frame is.
func TestSteeredTurnOwnedBy_ReceiverNotBorrowedArgument(t *testing.T) {
	const parent, child = "0xc000a1b000", "0xc000c2d000"
	stack := "goroutine 7 [chan receive]:\n" +
		"gw.parkWithBorrowedLoop(" + child + ", 0x1)\n" +
		"\t/x/helper_test.go:10 +0x1\n" +
		"github.com/elicify-ai/omnipus/pkg/agent.(*AgentLoop).runTurn(" + parent + "?, {0x1, 0x2})\n" +
		"\t/x/loop.go:2090 +0x62c\n" +
		"github.com/elicify-ai/omnipus/pkg/agent.(*AgentLoop).goSteeredTurn.func1()\n" +
		"\t/x/admission.go:613 +0x50\n" +
		"created by github.com/elicify-ai/omnipus/pkg/agent.(*AgentLoop).goSteeredTurn in goroutine 3\n"
	if !steeredTurnOwnedBy(stack, parent) {
		t.Errorf("the turn's own loop %s must own it", parent)
	}
	if steeredTurnOwnedBy(stack, child) {
		t.Errorf("a loop that is only an argument of another function (%s) must not own the turn", child)
	}
	if steeredTurnOwnedBy(strings.ReplaceAll(stack, "goSteeredTurn.func1", "other"), parent) {
		t.Error("a goroutine not started by goSteeredTurn is never a steered turn")
	}
}
