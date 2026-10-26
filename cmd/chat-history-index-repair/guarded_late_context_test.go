package main

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Approved Q2=A observation at the external cancellation boundary. This does
// not supply a repair result, native inspection or backup verdict. Every Err
// call still returns the real parent context's cancellation state. It pauses
// only AFTER the actual shared verifier establishes the first committed root.
type repairLateContext struct {
	context.Context
	firstDir string
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
	free     sync.Once
	reached  atomic.Bool
}

func (c *repairLateContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	if !c.reached.Load() && session.VerifyChatHistoryIndex(context.Background(), c.firstDir) == nil {
		c.once.Do(func() {
			c.reached.Store(true)
			close(c.entered)
		})
	}
	if c.reached.Load() {
		select {
		case <-c.release:
		case <-c.Done():
		}
	}
	return c.Context.Err()
}

func (c *repairLateContext) unblock() { c.free.Do(func() { close(c.release) }) }

type repairLateResult struct {
	exit   int
	output string
}

type repairLateApply struct {
	ctx    *repairLateContext
	cancel context.CancelFunc
	done   chan repairLateResult
	joined bool
}

func repairStartLateApply(t *testing.T, batch repairLateBatch) *repairLateApply {
	t.Helper()
	base, cancel := context.WithCancel(context.Background())
	observed := &repairLateContext{Context: base, firstDir: repairLateSessionDir(batch, batch.ids[0]),
		entered: make(chan struct{}), release: make(chan struct{})}
	work := &repairLateApply{ctx: observed, cancel: cancel, done: make(chan repairLateResult, 1)}
	go func() {
		var output bytes.Buffer
		code := runRepair(observed, batch.applyArgs(), &output) // actual CLI parser/guards/whole-batch entry
		work.done <- repairLateResult{exit: code, output: output.String()}
	}()
	t.Cleanup(func() {
		cancel()
		observed.unblock()
		if !work.joined {
			select {
			case <-work.done:
				work.joined = true
			case <-time.After(30 * time.Second):
				t.Fatal("C1 owned real CLI work did not join after cleanup cancellation/release")
			}
		}
	})
	select {
	case <-observed.entered:
		// The barrier is a positive observation, not a stopwatch or eventual
		// filesystem race. The command cannot pass another Err checkpoint.
		repairLatePublicationState(t, batch, true)
	case result := <-work.done:
		work.joined = true
		t.Fatalf("C1 prerequisite: real guarded CLI ended before first publication barrier; exit=%d output=%s", result.exit, result.output)
	case <-time.After(30 * time.Second):
		t.Fatal("C1 publication observation did not rendezvous; Q2=A not proved deterministic, report before requesting a production seam")
	}
	return work
}

func repairJoinLateApply(t *testing.T, work *repairLateApply) repairLateResult {
	t.Helper()
	work.ctx.unblock()
	select {
	case result := <-work.done:
		work.joined = true
		t.Logf("actual guarded CLI entry completed exit=%d output=%s", result.exit, result.output)
		return result
	case <-time.After(30 * time.Second):
		t.Fatal("C1 actual late-failure CLI did not join after barrier release")
		return repairLateResult{exit: -1, output: "unjoined"}
	}
}
