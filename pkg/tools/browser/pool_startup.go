package browser

import "context"

func (p *BrowserPool) runStartup(key BrowsingKey, cfg BrowserConfig, flight *startupCohort, configErr error) {
	var inst *chromeInstance
	err := invokeStartup(func() error {
		if configErr != nil {
			return configErr
		}
		if !flight.live() {
			return context.Canceled
		}
		var err error
		inst, err = p.launch(flight.ctx, key, cfg)
		return err
	})
	id := key.String()
	p.mu.Lock()
	if err == nil {
		switch {
		case p.closed || p.launching[id] != flight || p.retiring[id] != nil:
			err = errPoolClosed
		case !flight.live():
			err = context.Canceled
		default:
			inst.lastUsed = p.clock()
			p.instances[id] = inst
		}
	}
	p.mu.Unlock()
	if err != nil && inst != nil {
		inst.coord.Shutdown()
		inst.coord.waitStartupDrain()
	}
	p.finishLaunch(id, flight, err)
}
