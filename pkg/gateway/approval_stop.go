package gateway

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// stopApprovalResult is an in-process outcome, not a wire format. Approval
// and cancellation compete under the registry mutex; neither can overwrite
// the other's terminal decision.
type stopApprovalResult struct {
	Canceled        int
	AlreadyApproved int
}

// stopApprovalCleanup owns one web Stop's immutable approval selection. The
// selection is accepted at gateway intake, before durable Stop callbacks can
// pause or interleave with a later Resume. Neither later callbacks nor the
// final fallback may re-walk the tree or select a newer approval.
type stopApprovalCleanup struct {
	reg      *approvalRegistryV2
	selected map[string]*approvalEntry
	walkErr  error
	once     sync.Once
	result   stopApprovalResult
}

func (h *WSHandler) prepareStopApprovals(root string) *stopApprovalCleanup {
	cleanup := &stopApprovalCleanup{reg: h.approvalRegV2, selected: make(map[string]*approvalEntry)}
	if cleanup.reg == nil || h.agentLoop == nil || root == "" {
		return cleanup
	}
	// Freeze the population BEFORE any storage I/O. A later execution's Ask
	// is never part of this Stop, even if discovery or a callback is delayed.
	population := cleanup.reg.approvalPopulationForStop()
	descendants, err := agent.CollectDescendantSessionIDs(h.agentLoop.GetSessionLifecycleStore(), root)
	cleanup.walkErr = err
	for _, id := range append([]string{root}, descendants...) {
		for _, entry := range population[id] {
			cleanup.selected[entry.ApprovalID] = entry
		}
	}
	return cleanup
}

func (r *approvalRegistryV2) approvalPopulationForStop() map[string][]*approvalEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	population := make(map[string][]*approvalEntry)
	for _, entry := range r.entries {
		if entry.state == ApprovalStatePending {
			population[entry.SessionID] = append(population[entry.SessionID], entry)
		}
	}
	return population
}

func (c *stopApprovalCleanup) cancel(reason string) stopApprovalResult {
	c.once.Do(func() {
		if c.reg != nil {
			c.result = c.reg.cancelMatchingApprovals(func(entry *approvalEntry) bool {
				_, selected := c.selected[entry.ApprovalID]
				return selected
			}, reason, true, c.selected)
			slog.Info("approval: auto-denied pending approvals on session cancel",
				"selected", len(c.selected), "count", c.result.Canceled,
				"already_approved", c.result.AlreadyApproved, "reason", reason)
		}
	})
	return c.result
}

func (h *WSHandler) publishStopApprovedOutcome(wc *wsConn, root string, result stopApprovalResult) {
	if result.AlreadyApproved == 0 {
		return
	}
	h.publishStopNotice(wc, root, "approved", "Some requests were already approved",
		fmt.Sprintf("Stop did not withdraw %d already-approved tool request(s). Those actions may already have run.", result.AlreadyApproved), notifications.SeverityWarning)
}

// retainStopApprovalFailure preserves the original error for the caller and
// adds only a safe description to the existing public partial-Stop report.
func (c *stopApprovalCleanup) retainFailure(root string, report *steer.CancelReport) {
	if c.walkErr != nil {
		report.Unreachable = append(report.Unreachable, steer.UnreachableSession{
			ID:                 root,
			Reason:             "Some helper approvals could not be cancelled because saved session data could not be read. Repair storage and retry Stop.",
			HelperTreeUnlisted: true,
		})
	}
}
