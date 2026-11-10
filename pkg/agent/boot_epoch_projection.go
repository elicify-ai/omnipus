package agent

// CurrentBootEpoch exposes the already-minted process epoch to read-only
// lifecycle projections. It never mints or changes the counter; zero means
// boot has not been wired, so callers retain their non-epoch projection.
func (al *AgentLoop) CurrentBootEpoch() uint64 {
	return al.bootEpochFor()
}
