package programs

// Runtime clauses formerly declared here are relocated, not dropped:
// S05 -> pkg/agent::TestProgramStoreFailure_ActualChildren (real in-lock failed
// publication, unchanged connection and actual next-child count16).
// S17/S36/S51/S53 -> pkg/agent::TestProgramRuntime_MissingS1Clauses (real lifecycle
// writes and actual loop-wired child observations).
// S44 -> pkg/agent::TestProgramRuntime_OngoingOriginSetting (actual running
// Telegram-carrier child across setting change, plus new child count0).
// There is intentionally no empty Test function that could emit a vacuous PASS.
