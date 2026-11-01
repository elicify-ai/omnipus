package agent

import "testing"

// T-34/T-5/FR-006. The real renderer/context injection is absent, as recorded
// by the positive-controlled production-source sweep in the T-34 report.
// These are loud missing-seam rows, not a fake renderer or a claim that labels
// were exercised. The real REST-success/cache-miss premise is independently
// exercised by the Discord package's TestT34_ExternalHTTPControl root.
func TestT34_ChannelsKindLabelsUseOnlyTheRealLocalCache(t *testing.T) {
	t.Run("rest_success_cache_miss_is_person_or_group", func(t *testing.T) {
		t.Fatal("BLOCKED: real Channels renderer/local-cache injection not implemented — required by T-34/T-5/FR-006; after REST resolves public-thread604 without populating State, require exact target line '  - 604 (person or group)', never person, with zero HTTP calls during rendering")
	})
	t.Run("supported_local_cache_hit_is_group", func(t *testing.T) {
		t.Fatal("BLOCKED: real Channels renderer/local-cache injection not implemented — required by T-34/T-5/FR-006; seed actual State with supported announcement605, require exact target line '  - 605 (group)' and zero HTTP calls during rendering")
	})
	t.Run("unsupported_local_cache_hit_is_unknown_label", func(t *testing.T) {
		t.Fatal("BLOCKED: real Channels renderer/local-cache injection not implemented — required by T-34/T-5/FR-006; an unsupported forum601 in the local cache is not a supported group, so require exact target line '  - 601 (person or group)' and zero HTTP calls during rendering")
	})
}
