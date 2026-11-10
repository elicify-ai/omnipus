package gateway

import (
	"fmt"
	"testing"
)

// D11/B19/T11 amended by spec34612f172: malformed unrecognized raw key
// or value has a fixed sanitized error; access precedes raw decoding, which
// precedes recognized-field validation. Expectations are from that amendment.
func TestGetSessionHistory_MalformedKeyAndResolvedRangePrecedence(t *testing.T) {
	for _, validate := range []bool{false, true} {
		t.Run(fmt.Sprintf("validate_%t", validate), func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			api.agentLoop.GetConfig().Gateway.ValidateInbound = validate
			sid := createTestSession(t, api)
			for _, query := range []string{"bad%ZZkey=x", "unused=%ZZ", "limit=19&unused=%ZZ", "bad%ZZkey=x&min_rows=0"} {
				t.Run(query, func(t *testing.T) {
					pagingRefusal(t, pagingRequest(api, sid, query), 400, "invalid history query")
				})
			}
			for _, missing := range []string{"missing-child", "main-session-missing-workspace+mia"} {
				t.Run("access_"+missing, func(t *testing.T) {
					pagingRefusal(t, pagingRequest(api, missing, "bad%ZZkey=x&limit=19"), 404, "session not found")
				})
			}
			t.Run("well_formed_unknown", func(t *testing.T) {
				p := pagingRead(t, api, sid, "unused=well_formed")
				if p.PageInfo.Mode != "latest" {
					t.Fatalf("well-formed unknown key enabled an invented history mode: %q", p.PageInfo.Mode)
				}
			})
		})
	}
}

func TestSessionHistory_AllPersistedFrameClassesHaveLocalOrigin(t *testing.T) {
	pagingPersistedOriginMatrix(t)
}

func TestSessionHistory_SnapshotBoundaryToolResultCoverageAndCurrentCards(t *testing.T) {
	pagingSeparateTerminalCoverage(t)
}
