package commands

// loopCommand is the `/loop` slash command family (ADR-049 D6, spec Part B
// US-9): `/loop every <interval> <prompt>` (interval mode), `/loop <prompt>`
// (self-paced mode), bare `/loop` (status), `/loop stop` (cancel). Like
// goalCommand, this is agent-delivery with Handler: nil —
// pkg/agent/loop_command.go's applyLoopCommandPrompt hook does all the real
// work (cron job creation,
// status formatting, stop) BEFORE the registered-command dispatch path is
// ever reached, mirroring applyGoalCommandPrompt's shape exactly. Starting or
// stopping a loop needs no LLM call (it is pure scheduling bookkeeping), so
// every verb answers synchronously (matched=true, handled=true) — `/loop`
// never itself runs a worker turn; the CRON-FIRED run later does, via the
// dedicated LoopScheduler (pkg/agent/loop_scheduler.go) calling back into
// AgentLoop.ProcessScheduled, exactly like a heartbeat/schedule fire.
func loopCommand() Definition {
	return Definition{
		Name:        "loop",
		Description: "Run this session on a recurring or self-paced cadence",
		Usage:       "/loop every <interval> <prompt> | /loop <prompt> | /loop | /loop stop",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler:     nil,
	}
}
