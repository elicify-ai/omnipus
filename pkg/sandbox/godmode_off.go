//go:build nogodmode

package sandbox

// GodModeAvailable is false in builds compiled with the `nogodmode` tag.
// SaaS variants use this tag to ensure the global god-mode runtime toggle
// (sandbox.god_mode) can never take effect, regardless of runtime flags.
const GodModeAvailable = false
