//go:build !linux

package browser

// audiosink_other.go is the non-Linux stand-in for the PulseAudio sidecar
// (Platform Matrix M-3 / FR-022): PulseAudio (and Xvfb, display_other.go) are
// Linux-only, so on every other platform the install classifies
// not-video-capable and audio is simply absent — agent browsing (headless
// Chrome/chrome-headless-shell) continues unaffected.

import "context"

// noopAudioSidecar never spawns anything; Start is a no-op that always
// succeeds (there is nothing to fail), Healthy is always false, and
// PulseServer returns "" so nothing wires a PULSE_SERVER env var into a
// non-existent audio setup.
type noopAudioSidecar struct{}

// NewAudioSidecar returns the non-Linux no-op AudioSidecar.
func NewAudioSidecar(_ AudioConfig) AudioSidecar { return &noopAudioSidecar{} }

func (n *noopAudioSidecar) Start(_ context.Context) error { return nil }
func (n *noopAudioSidecar) PulseServer() string           { return "" }
func (n *noopAudioSidecar) Healthy() bool                 { return false }
func (n *noopAudioSidecar) Stop()                         {}
