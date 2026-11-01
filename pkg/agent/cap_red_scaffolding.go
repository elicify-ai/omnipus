package agent

// RED scaffolding for #1547, cap-visibility-spec-r6 R-7/CAP-B9.
// Compile-only placeholders: these do NOT implement admission policy. The
// existing gates are deliberately unchanged. The implementing owner must delete
// this file and supply the real shared fold and exported cron seam. Returning
// zero evidence keeps the missing implementation visible in the RED tests.

func foldMemory(configured int) (limit int, cause string) {
	return 0, ""
}

func ScheduledLaneLimit() (limit int, cause string) {
	return foldMemory(0)
}
