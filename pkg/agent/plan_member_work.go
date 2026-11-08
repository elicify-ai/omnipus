package agent

import "github.com/elicify-ai/omnipus/pkg/task"

// planMemberWorkInFlight distinguishes an executing member from a pending row
// that can still be dispatched. Stop leaves historical next members on a failed
// plan; those rows cannot keep its initiating goal busy after real work settles.
func (pe *PlanEngine) planMemberWorkInFlight(planID string, members []task.Task) (bool, string) {
	var pending *task.Task
	for i := range members {
		member := &members[i]
		if member.Status == task.StatusInProgress {
			return true, "member task " + member.ID + " is " + string(member.Status)
		}
		if member.Status == task.StatusNext && pending == nil {
			pending = member
		}
	}
	if pending == nil {
		return false, ""
	}
	if pe.planStore == nil {
		return true, "pending member dispatch state could not be read: plan store is unavailable"
	}
	current, err := pe.planStore.Get(planID)
	if err != nil {
		return true, "pending member dispatch state could not be read: " + err.Error()
	}
	if current == nil {
		return true, "pending member dispatch state could not be read: plan is unavailable"
	}
	if !current.PermitsMemberDispatch() {
		return false, ""
	}
	return true, "member task " + pending.ID + " is " + string(pending.Status)
}
