// Package t11combined contains test-only oracles from founder Q37 A
// (2026-11-08 approximately 05:20Z), not from implementation output.
package t11combined

const (
	// LocationUnverifiableCause is the exact approved clause for run refusals.
	LocationUnverifiableCause = "the list of folders agents can write to could not be read in full"
	// LocationUnverifiableReason is shared by every Q37-dependent test.
	LocationUnverifiableReason = LocationUnverifiableCause + ", so this location cannot be checked. Details are in the server log."
	// CodexSaveRefusal is the founder's exact Settings Save sentence.
	CodexSaveRefusal = "The program path for Codex was not saved: " + LocationUnverifiableReason
)
