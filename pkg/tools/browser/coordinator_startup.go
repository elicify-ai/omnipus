package browser

import (
	"context"
	"fmt"
)

func (c *BrowserCoordinator) runStartup(flight *startupCohort) {
	err := invokeStartup(func() error {
		if !flight.live() {
			return context.Canceled
		}
		return c.launchChrome(flight.ctx)
	})
	c.mu.Lock()
	if err == nil {
		switch {
		case c.shutdown || c.startup != flight:
			err = fmt.Errorf("browser: shared Chrome launch aborted by concurrent shutdown")
		case c.rootCtx == nil || c.rootCtx.Err() != nil:
			err = fmt.Errorf("browser: shared Chrome exited before startup completed")
		case !flight.live():
			err = context.Canceled
		default:
			c.launched = true
		}
	}
	var cancel context.CancelFunc
	var lockFile = c.lockFile
	if err != nil && c.startup == flight {
		cancel = c.rootCancel
		c.rootCtx = nil
		c.rootCancel = nil
		c.lockFile = nil
		c.cmd = nil
		c.launched = false
	} else {
		lockFile = nil
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	releaseLaunchLock(lockFile)
	c.mu.Lock()
	if c.startup == flight {
		c.startup = nil
	}
	c.mu.Unlock()
	flight.finish(err)
}

// A pool worker keeps its old launch marker until the owned coordinator's
// canceled launch has released its profile/process resources. Callers wait on
// their own contexts, so this drain does not extend a canceled request.
func (c *BrowserCoordinator) waitStartupDrain() {
	c.mu.Lock()
	flight := c.startup
	c.mu.Unlock()
	if flight != nil {
		<-flight.done
	}
}
