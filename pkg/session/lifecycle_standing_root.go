package session

// LifecycleRecordIsStandingRoot identifies an ordinary reusable conversation,
// not a steered session or a task/plan owner. Boot recovery and read-only lost-
// prompt projection share this predicate; plan recovery keeps its own authority.
func LifecycleRecordIsStandingRoot(rec *LifecycleRecord) bool {
	if rec == nil || rec.SteeredBy != nil || rec.OwnsPlanID != "" || rec.OwnerScopeKind == OwnerScopePlan {
		return false
	}
	if rec.Origin == nil {
		return true
	}
	switch rec.Origin.Kind {
	case OriginKindChat, OriginKindChannel, OriginKindMain, OriginKindHeartbeat, OriginKindScheduled:
		return true
	default:
		return false
	}
}
