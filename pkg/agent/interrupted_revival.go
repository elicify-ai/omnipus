package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// authorizeInterruptedRevival is checked before taking the child's record lock.
// System outcomes and scheduled/goal wakes are not an explicit continuation.
func (c *SteerCanceller) authorizeInterruptedRevival(rec *session.LifecycleRecord, by steer.Principal) error {
	if by.Kind == steer.PrincipalKindHuman && by.ID != "" {
		return nil
	}
	if by.Kind != steer.PrincipalKindAgent || by.ID == "" {
		return fmt.Errorf("interrupted execution requires an explicit authorized continuation")
	}
	seen := map[string]bool{rec.SessionID: true}
	id := rec.SteeringSessionID()
	for id != "" && !seen[id] {
		seen[id] = true
		parent, err := c.Lifecycle.Load(id)
		if err != nil {
			return err
		}
		if by.ID == id || by.ID == parent.AgentID {
			return nil
		}
		id = parent.SteeringSessionID()
	}
	return fmt.Errorf("interrupted execution cannot be revived by this producer")
}
