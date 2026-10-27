package tools

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SFH-RR1 HIGH: writes must retain their callback/destination and return the
// actual committed outcome. Channel barriers plus synctest.Wait, not sleeps,
// make the caller-cancel window deterministic.
func TestAttachmentSaveCancellationRetainsDestinationAndCommittedOutcome(t *testing.T) {
	for _, stage := range []string{"mailbox_read", "audit_append"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				fixture := newAttachmentLifetimeFixture(t, stage)
				defer fixture.barrier.unblock()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx = WithWorkspaceID(ctx, "lifetime-ws")
				ctx = WithAgentID(ctx, "lifetime-agent")
				permission := &ToolPolicyCfg{Policies: map[string]config.ToolPolicy{fixture.tool.Name(): config.ToolPolicyAllow}, GlobalPolicies: map[string]config.ToolPolicy{fixture.tool.Name(): config.ToolPolicyAllow}}
				require.Equal(t, "allow", EffectiveToolPolicy(permission, fixture.tool.Scope(), "custom", fixture.tool.Name()), "the cancellation probe is a permitted save")
				args := map[string]any{"folder": "inbox", "message_ref": "fixture-issued-ref", "part_index": float64(1)}
				var result *ToolResult
				done := make(chan struct{})
				go func() {
					result = fixture.tool.Execute(ctx, args)
					fixture.toolReturned.Store(true)
					close(done)
				}()
				<-fixture.barrier.entered
				require.True(t, fixture.barrier.paused.Load(), "positive control: the actual Save callback is active at the selected edge")
				relative := path.Join("mail", "lifetime@example.test", time.Now().UTC().Format("2006-01"), "lifetime.txt")
				absolute := fixture.root.HostPath(relative)
				if stage == "audit_append" {
					// SFH's concrete committed-output scenario: publication precedes the waiting audit append.
					data, err := os.ReadFile(absolute)
					require.NoError(t, err)
					require.Equal(t, lifetimePayload, string(data))
				}
				cancel()
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				synctest.Wait() // settle the canceled foreground waiter while Save remains durably blocked
				returnedEarly := false
				select {
				case <-done:
					returnedEarly = true
				default:
				}
				// SFH-RR1: an active callback must not be abandoned or lose its destination.
				assert.False(t, returnedEarly, "Execute must await the actual Save outcome, not return an independent canceled wait")
				assert.Zero(t, fixture.closeCalls.Load(), "destination must not close while Save is active")
				_, openErr := fixture.root.StatDir(".")
				assert.NoError(t, openErr, "the actual Library root must remain usable while the callback waits")
				if returnedEarly && stage == "audit_append" {
					assert.NotContains(t, result.ForLLM, "could not save", "a physically published save must not be reported as no save")
				}
				fixture.barrier.unblock()
				<-done
				synctest.Wait() // drain any abandoned old read-flight so its real late outcome is inspected
				assertAttachmentLifetimeOutcome(t, fixture, result, relative, absolute)
			})
		})
	}
}

func assertAttachmentLifetimeOutcome(t *testing.T, fixture *attachmentLifetimeFixture, result *ToolResult, relative, absolute string) {
	t.Helper()
	require.NotNil(t, result)
	// The controlled read/audit complete successfully; returning cancellation
	// after their commit must not replace the receipt with a false failure.
	assert.False(t, result.IsError, "caller-visible result must preserve the controlled committed save: %s", result.ForLLM)
	assert.NotContains(t, result.ForLLM, "could not save", "never tell the agent to retry a save that committed")
	assert.False(t, fixture.closeWhileBusy.Load(), "actual root.Close must follow, never precede, the active callback")
	assert.Equal(t, int32(1), fixture.closeCalls.Load(), "destination ownership ends exactly once")
	assert.False(t, fixture.audit.afterToolReturn.Load(), "no unobserved audit commit may continue after Execute returns")
	assert.Equal(t, int32(1), fixture.audit.calls.Load(), "the successful save has one actual audit append")
	assert.Nil(t, fixture.audit.logError.Load())
	data, err := os.ReadFile(absolute)
	assert.NoError(t, err, "the save's actual disk outcome must agree with the receipt")
	assert.Equal(t, lifetimePayload, string(data))
	events := lifetimeAttachmentEvents(t, fixture.auditPath)
	assert.Len(t, events, 1, "inspect the real JSONL audit outcome, not just a reported status")
	if len(events) == 1 {
		assert.Equal(t, relative, events[0].Details["workspace_relative_path"])
		assert.Equal(t, float64(len(lifetimePayload)), events[0].Details["size_bytes"])
	}
	if !result.IsError {
		var receipt struct {
			Saved        bool   `json:"saved"`
			Path         string `json:"path"`
			AbsolutePath string `json:"absolute_path"`
			SizeBytes    int64  `json:"size_bytes"`
			AuditStatus  string `json:"audit_status"`
		}
		require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &receipt))
		assert.True(t, receipt.Saved)
		assert.Equal(t, relative, receipt.Path)
		assert.Equal(t, absolute, receipt.AbsolutePath)
		assert.Equal(t, int64(len(lifetimePayload)), receipt.SizeBytes)
		assert.Equal(t, "recorded", receipt.AuditStatus)
	}
}
