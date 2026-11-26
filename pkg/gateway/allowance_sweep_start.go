// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// startAllowanceExpirySweep starts the C-26 allowance expiry sweep
// (AgentLoop.RunAllowanceExpirySweep, Dataset D17 row 9: a timed address
// allowance that ends during a running command revokes the command's egress
// token) on the agent loop's context. The returned channel closes when the
// sweep has stopped, which it does once that context ends.
// startBackgroundServices launches the sweep only through this method.
func (rc *runContextWithOptions) startAllowanceExpirySweep() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		rc.agentLoop.RunAllowanceExpirySweep(rc.agentLoopCtx)
	}()
	return done
}
