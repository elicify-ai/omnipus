package tools

// Test-only pending binding for D-3's live global plan ceilings. These are NOT
// production hooks. Production cannot call a variable declared in a _test.go
// file. A nil binding makes the affected rows explicitly BLOCKED; assigning
// this variable alone would not configure a real tool. The test author must
// replace this pending binding with a real injected configuration in test-side
// reconciliation. No scheduling or goal-sync hook is fabricated here.
type PlanBoundsCeilings struct {
	PlanJudgeMaxRounds            int
	IdleExpiryDays                int
	SupervisionMaxAttempts        int
	SupervisionTurnTimeoutSeconds int
}

var PlanBoundsCeilingsFn func() PlanBoundsCeilings
