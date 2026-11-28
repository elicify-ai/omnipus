//go:build s1seams

package gateway

// H46/S113 v2. This is an explicit, frozen input table prepared from declared
// registrations before live-mux execution, NOT generated from the observed mux
// or from route-name heuristics. An unknown runtime pattern has no default row.
import (
	"fmt"
	"sort"
	"strings"
)

type pgLiveMuxRow struct {
	pattern, kind, reason string
}

func pgLiveMuxRows() []pgLiveMuxRow {
	// Generic S113 request/refusal probe is appropriate for these non-program
	// credential endpoints. I3/S127 and the typed writer allowlist independently
	// guard credential mutations; this row is not a declaration of reachability.
	const generic = `
/
/api/
/api/v1/about
/api/v1/activity
/api/v1/agents
/api/v1/agents/
/api/v1/audit-log
/api/v1/auth/logout
/api/v1/auth/validate
/api/v1/automations
/api/v1/browser/capture-ingest
/api/v1/browser/inspect
/api/v1/browser/ws
/api/v1/channels
/api/v1/channels/
/api/v1/chat/ws
/api/v1/commands
/api/v1/config
/api/v1/config/gateway/rotate-token
/api/v1/config/pending-restart
/api/v1/devices
/api/v1/doctor
/api/v1/gateway/god-mode
/api/v1/gateway/restart
/api/v1/integrations/providers
/api/v1/integrations/providers/
/api/v1/library
/api/v1/library/
/api/v1/mailboxes
/api/v1/mailboxes/cleanup
/api/v1/mcp-servers
/api/v1/mcp-servers/
/api/v1/media/
/api/v1/media/workspace/
/api/v1/notifications
/api/v1/notifications/
/api/v1/onboarding/complete
/api/v1/onboarding/probe-provider
/api/v1/packages/export
/api/v1/packages/export/plan
/api/v1/packages/export/plan/
/api/v1/packages/import
/api/v1/packages/import/
/api/v1/performance
/api/v1/performance/max-tool-iterations/preview
/api/v1/plans
/api/v1/plans/
/api/v1/providers
/api/v1/providers/
/api/v1/providers/catalog
/api/v1/providers/default-model
/api/v1/rooms/
/api/v1/schedules
/api/v1/schedules/
/api/v1/secrets
/api/v1/secrets/
/api/v1/security/audit-log
/api/v1/security/exec-proxy-status
/api/v1/security/prompt-guard
/api/v1/security/rate-limits
/api/v1/security/retention
/api/v1/security/retention/sweep
/api/v1/security/sandbox-config
/api/v1/security/sandbox-status
/api/v1/security/session-scope
/api/v1/security/skill-trust
/api/v1/security/tool-policies
/api/v1/sessions
/api/v1/sessions/
/api/v1/sessions/all
/api/v1/settings/context
/api/v1/settings/memory
/api/v1/settings/third-party-agents
/api/v1/skills
/api/v1/skills/
/api/v1/state
/api/v1/stats/tokens
/api/v1/status
/api/v1/storage/stats
/api/v1/system/cli-detect
/api/v1/system/cli-validate
/api/v1/system/folders
/api/v1/tasks
/api/v1/tasks/
/api/v1/tasks/occurrences
/api/v1/tool-approvals/
/api/v1/tools
/api/v1/tools/mcp
/api/v1/upload
/api/v1/uploads/
/api/v1/user-context
/api/v1/version
/api/v1/voice/provider
/api/v1/voice/transcribe
/api/v1/workspaces
/api/v1/workspaces/
/metrics
`
	rows := make([]pgLiveMuxRow, 0, len(strings.Fields(generic)))
	for _, pattern := range strings.Fields(generic) {
		rows = append(rows, pgLiveMuxRow{pattern, "generic", "Generic S113 refusal/state probe; program ownership protected separately by I3/S127 and the typed writer allowlist."})
	}
	for _, pattern := range []string{"/api/v1/programs", "/api/v1/programs/", "/api/v1/credentials", "/api/v1/credentials/", "/api/v1/secret-requests/"} {
		rows = append(rows, pgLiveMuxRow{pattern, "payload", "S113 route-specific valid method/payload rows in pgSweepTarget/pgConcreteProgramsPayloads and the prepared secret-request fixture; no generic-only writer exemption."})
	}
	for _, pattern := range []string{
		"/health", "/ready", "/reload", "/browser-start", "/preview/", "/serve/", "/dev/", "/library-preview/", "/mail-preview/", "/mail-preview/attachment/",
		"/api/v1/library/preview-token", "/api/v1/library/preview-token/", "/api/v1/mail/attachment-preview-token", "/api/v1/mail/attachment-preview-token/",
		"/api/v1/mail/html-preview-token", "/api/v1/mail/signature-preview-token",
	} {
		rows = append(rows, pgLiveMuxRow{pattern, "generic", "Health/static/preview route has no permitted program writer; generic S113 request/refusal probe, not content execution."})
	}
	for _, pattern := range []string{"/api/v1/auth/login", "/api/v1/auth/change-password", "/api/v1/auth/reauth", "/api/v1/auth/platform/start", "/api/v1/auth/session", "/api/v1/auth/platform/claim", "/auth/callback"} {
		rows = append(rows, pgLiveMuxRow{pattern, "generic", "Edition-specific sign-in route cannot write a program token; generic S113 auth/refusal probe; provider supplies platform routes."})
	}
	return rows
}

func pgLiveMuxAccounting(patterns []string, rows []pgLiveMuxRow) []string {
	counts := map[string]int{}
	var failures []string
	for _, row := range rows {
		counts[row.pattern]++
		if (row.kind != "payload" && row.kind != "generic") || strings.TrimSpace(row.reason) == "" {
			failures = append(failures, row.pattern+": row lacks payload/generic rationale")
		}
	}
	for pattern, count := range counts {
		if count != 1 {
			failures = append(failures, fmt.Sprintf("%s: rows=%d, want exactly1", pattern, count))
		}
	}
	for _, pattern := range patterns {
		if counts[pattern] == 0 {
			failures = append(failures, pattern+": rows=0, want exactly1")
		}
	}
	sort.Strings(failures)
	return failures
}
