package browser

import "errors"

var errCaptureManagerTeardown = errors.New("browser capture: manager connection is closing")

// CaptureSessionForPanel returns only the resolved panel's capture. An empty
// panel ID explicitly addresses the workspace operator tab set.
func (m *BrowserManager) CaptureSessionForPanel(panelSessionID string) *CaptureSession {
	if panelSessionID == "" {
		panelSessionID = m.OperatorSessionID()
	}
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	return m.captures[panelSessionID]
}

// captureSessions snapshots lifecycle ownership without holding captureMu across
// Stop, whose callback can remove the stopped session from this manager.
func (m *BrowserManager) captureSessions() []*CaptureSession {
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	sessions := make([]*CaptureSession, 0, len(m.captures))
	for _, cs := range m.captures {
		if cs != nil {
			sessions = append(sessions, cs)
		}
	}
	return sessions
}

func (m *BrowserManager) beginCaptureTeardown() []*CaptureSession {
	m.captureMu.Lock()
	m.captureTeardowns++
	m.captureMu.Unlock()
	return m.captureSessions()
}

func (m *BrowserManager) endCaptureTeardown() {
	m.captureMu.Lock()
	m.captureTeardowns--
	m.captureMu.Unlock()
}

func (m *BrowserManager) EnsureCaptureSessionForPanel(panelSessionID string, newFn func() (*CaptureSession, error)) (*CaptureSession, error) {
	if panelSessionID == "" {
		panelSessionID = m.OperatorSessionID()
	}
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	if m.captureTeardowns > 0 {
		return nil, errCaptureManagerTeardown
	}
	if cs := m.captures[panelSessionID]; cs != nil {
		return cs, nil
	}
	cs, err := newFn()
	if err != nil {
		return nil, err
	}
	if m.videoHealthObs != nil {
		cs.SetOnVideoHealth(m.videoHealthObs)
	}
	if m.captures == nil {
		m.captures = make(map[string]*CaptureSession)
	}
	m.captures[panelSessionID] = cs
	return cs, nil
}
