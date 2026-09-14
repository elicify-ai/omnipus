// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_judge_timeout_fr049_test.go is the Test Matrix's named oracle for
// JUDGE-FR-049 (judge-active-reviewer-spec.md): "The judge turn timeout MUST
// be operator-configurable, MUST default to 420 s", with a 900 s hard ceiling
// ("Default judge turn timeout | raised from 120 s to 420 s; operator-
// configurable; hard ceiling 900 s"). ADR-084 D9 names it as prerequisite 1.
//
// UAT E-14 run 2 is why it matters: the Judge's turn was cut off at a fixed
// 120 s twice while its stream was still producing output ("Transport drop
// after partial stream", 2 162 and 3 242 chars), each cut followed by a D7
// backoff, so a verdict the third try produced in 70 s took 8 min 10 s.
//
// The expected deadlines are the spec's numbers, not values read back from
// the implementation. The deadline is observed where it matters — on the ctx
// the Judge's provider call actually receives.
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestJudgeTimeout_ConfigurableAndDefaults420s(t *testing.T) {
	cases := []struct {
		name          string
		configuredSec int
		want          time.Duration
	}{
		{name: "unset defaults to 420 s", configuredSec: 0, want: 420 * time.Second},
		{name: "operator value is honoured", configuredSec: 200, want: 200 * time.Second},
		{name: "above the hard ceiling is clamped to 900 s", configuredSec: 5000, want: 900 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
				cfg.Judge.TimeoutSec = tc.configuredSec
			})
			judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
				return &providers.LLMResponse{
					Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"the file was reviewed"}]}`,
				}, nil
			}}
			judgeInst.Provider = judge

			started := time.Now()
			res := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
				Scope:           task.VerdictScopeTask,
				TaskID:          "t-fr049-" + tc.name,
				AssigneeAgentID: "native-agent",
				Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the file was reviewed")},
				Attempt:         1,
				ClaimText:       "done",
			})
			if res.Unavailable || res.Verdict == nil {
				t.Fatalf("setup: expected a real verdict, got unavailable=%v reason=%q", res.Unavailable, res.Reason)
			}

			ctx := judge.capturedCtx()
			if ctx == nil {
				t.Fatal("the Judge's provider was never called")
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("the Judge's provider call carries no deadline at all")
			}
			got := deadline.Sub(started)
			// The deadline is set a few milliseconds after `started`; allow
			// generous scheduling slack below, none above.
			if got > tc.want+time.Second || got < tc.want-15*time.Second {
				t.Errorf("Judge turn deadline = %v after dispatch; want %v (JUDGE-FR-049)", got.Round(time.Second), tc.want)
			}
		})
	}
}
