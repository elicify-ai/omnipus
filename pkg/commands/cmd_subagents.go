package commands

import (
	"context"
	"fmt"
)

// TurnInfo is a mirrored struct from agent.TurnInfo to avoid circular dependencies.
type TurnInfo struct {
	TurnID       string
	ParentTurnID string
	Depth        int
	ChildTurnIDs []string
	IsFinished   bool
}

// tasksCommand is the canonical /tasks command (renamed from /subagents).
// Surfaces: CLI, Channel (not web — the Tasks screen covers this in the SPA).
func tasksCommand() Definition {
	return Definition{
		Name:        "tasks",
		Description: "Show running subagents and task tree",
		Usage:       "/tasks",
		Aliases:     []string{"subagents"},
		Surfaces:    []Surface{SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler: func(ctx context.Context, req Request, rt *Runtime) error {
			getTurnFn := rt.GetActiveTurn
			if getTurnFn == nil {
				return req.Reply("Runtime does not support querying active turns.")
			}

			turnRaw := getTurnFn()
			if turnRaw == nil {
				return req.Reply("No active tasks running in this session.")
			}

			if treeStr, ok := turnRaw.(string); ok {
				if treeStr == "" {
					return req.Reply("No active tasks running in this session.")
				}
				return req.Reply(fmt.Sprintf("🤖 **Active Subagents Tree**\n```text\n%s\n```", treeStr))
			}

			return req.Reply(fmt.Sprintf("🤖 **Active Subagents List**\n```text\n%+v\n```", turnRaw))
		},
	}
}
