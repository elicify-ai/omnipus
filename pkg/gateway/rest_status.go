// rest_status.go: Doctor, app state, version, devices, activity, storage stats

package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- Doctor / Diagnostics ---

// HandleDoctor handles GET/POST /api/v1/doctor.
func (a *restAPI) HandleDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.agentLoop.GetConfig()

	// Run real diagnostic checks and compute a score.
	issues := a.runDiagnosticChecks(cfg)
	score := 100
	for _, iss := range issues {
		sev, _ := iss["severity"].(string)
		switch sev {
		case "high":
			score -= 20
		case "medium":
			score -= 10
		case "low":
			score -= 5
		}
	}
	if score < 0 {
		score = 0
	}

	// Persist the doctor run result.
	if a.onboardingMgr != nil {
		if err := a.onboardingMgr.RecordDoctorRun(score); err != nil {
			slog.Warn("rest: could not persist doctor run", "error", err)
		}
	}

	result := map[string]any{
		"score":      score,
		"issues":     issues,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	}

	if r.Method == http.MethodGet {
		info := a.agentLoop.GetStartupInfo()
		checks := map[string]any{
			"gateway": map[string]any{
				"status":  "ok",
				"address": fmt.Sprintf("%s:%d", cfg.Gateway.Host, cfg.Gateway.Port),
			},
			"agent_loop": map[string]any{
				"status": "ok",
				"info":   info,
			},
			"session_store": func() map[string]any {
				for _, id := range a.agentLoop.GetRegistry().ListAgentIDs() {
					if store := a.agentLoop.GetAgentStore(id); store != nil {
						return map[string]any{"status": "ok", "available": true}
					}
				}
				return map[string]any{"status": "degraded", "available": false}
			}(),
			"go_runtime": map[string]any{
				"version":    runtime.Version(),
				"goroutines": runtime.NumGoroutine(),
				"os":         runtime.GOOS,
				"arch":       runtime.GOARCH,
			},
		}
		result["status"] = "ok"
		result["checks"] = checks
	}

	jsonOK(w, result)
}

// runDiagnosticChecks performs real diagnostic checks and returns issues found.
// Returns a non-nil empty slice when there are no issues so the JSON shape is
// always `"issues": []` rather than `"issues": null` — frontend consumers
// (DiagnosticsSection.tsx) call .filter() directly on the field.
func (a *restAPI) runDiagnosticChecks(cfg *config.Config) []map[string]any {
	issues := []map[string]any{}

	// Check if a default model is configured.
	if len(cfg.Providers) == 0 {
		issues = append(issues, map[string]any{
			"id":             "no-models",
			"severity":       "high",
			"title":          "No LLM models configured",
			"description":    "No models are configured in model_list. The agent cannot generate responses without at least one model.",
			"recommendation": "Go to Settings → Providers and add an API key.",
			"action_link":    "/settings?tab=providers",
			"action_label":   "Configure providers",
		})
	}

	// Session store is always available via the unified store on each agent.

	// Check if any agents are configured.
	if len(cfg.Agents.List) == 0 {
		issues = append(issues, map[string]any{
			"id":             "no-custom-agents",
			"severity":       "low",
			"title":          "No custom agents configured",
			"description":    "Only the built-in agents are available. Custom agents can be defined to personalise your assistant.",
			"recommendation": "Go to Settings → Agents and create a custom agent.",
			"action_link":    "/settings?tab=agents",
			"action_label":   "Manage agents",
		})
	}

	// Check sandbox configuration. ResolvedMode is the source of truth.
	// Both "enforce" and "permissive" represent an active sandbox; only
	// "off" (or an empty config that resolves to off) should warn.
	if cfg.Sandbox.ResolvedMode() == string(config.SandboxModeOff) {
		issues = append(issues, map[string]any{
			"id":             "sandbox-disabled",
			"severity":       "medium",
			"title":          "Sandbox is disabled",
			"description":    "Filesystem and process sandboxing is not enabled. Agent tool executions run without confinement.",
			"recommendation": "Go to Settings → Security → Advanced to enable sandbox mode.",
			"action_link":    "/settings?tab=security",
			"action_label":   "Open security settings",
		})
	}

	// D6: God mode armed. Triggered on the raw persisted intent
	// (cfg.Sandbox.GodMode), NOT on cfg.Sandbox.GodModeAllowed alone —
	// GodModeAllowed being true (S3: authorized in the past, currently
	// disabled) is a genuinely inert state and must not false-positive here.
	// cfg.Sandbox.GodMode true covers both S1 (armed via the UI, pending
	// restart — the config write already happened even though this boot
	// hasn't activated it) and S2 (live-active): either way an operator has
	// committed to disabling the kernel sandbox's filesystem confinement and
	// network port controls and opening egress, which is strictly worse than
	// the sandbox-disabled check above (that one only concerns the sandbox;
	// god mode disables multiple controls simultaneously and floors every
	// tool's global policy at allow), hence "high" not "medium". It does NOT
	// disable the shell's outside-workspace write refusal — that refusal
	// still fires under god mode, it just never prompts first.
	if cfg.Sandbox.GodMode {
		issues = append(issues, map[string]any{
			"id":             "god-mode-armed",
			"severity":       "high",
			"title":          "God-mode is armed",
			"description":    "God-mode is enabled or pending activation. It bypasses every permission prompt and disables the kernel sandbox's filesystem confinement and network port controls, and opens outbound network access, for every agent's shell tool.",
			"recommendation": "Go to Settings → Security → Danger zone and turn god-mode off, unless this is intentional.",
			"action_link":    "/settings?tab=security",
			"action_label":   "Open security settings",
		})
	}

	return issues
}

// HandleUserContext handles GET and PUT /api/v1/user-context.
// It reads and writes USER.md in the default workspace directory, which holds
// workspace-level context about the user (their background, preferences, etc.).
func (a *restAPI) HandleUserContext(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getUserContext(w)
	case http.MethodPut:
		a.putUserContext(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) getUserContext(w http.ResponseWriter) {
	// config.ReadUserProfile is the single resolver, shared with the agent
	// context builder. Reading USER.md independently here is what let the two
	// halves drift apart in the first place.
	_, content, err := config.ReadUserProfile()
	if err != nil {
		slog.Error("rest: read USER.md", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not read USER.md: %v", err))
		return
	}
	jsonOK(w, gen.UserContextResponse{Content: content})
}

func (a *restAPI) putUserContext(w http.ResponseWriter, r *http.Request) {
	var req gen.UserContextRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "UserContextRequest", &req, validateEnabled) {
		return
	}
	// Always writes the global path, never the legacy one — so the first save
	// after upgrading moves the profile to its proper home.
	userMDPath := config.UserProfilePath()
	if err := os.MkdirAll(filepath.Dir(userMDPath), 0o700); err != nil {
		slog.Error("rest: create USER.md parent", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not write USER.md: %v", err))
		return
	}
	if err := fileutil.WriteFileAtomic(userMDPath, []byte(req.Content), 0o600); err != nil {
		slog.Error("rest: write USER.md", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not write USER.md: %v", err))
		return
	}
	jsonOK(w, gen.UserContextResponse(req))
}

// --- App State ---

// videoEmbedHosts returns the allow-list this install advertises to the reader.
//
// The nil-agent-loop branch resolves to the SHIPPED DEFAULT rather than to an
// empty list, and that choice is deliberate: it is exactly what
// newSPAHandler(nil) puts in the served Content-Security-Policy when it has no
// config either. Answering "none" here while the policy says "one host" would
// manufacture the drift EMB-080 forbids — out of a defensive nil check, in the
// one code path where nothing else would notice.
func (a *restAPI) videoEmbedHosts() []string {
	if a.agentLoop == nil {
		return ResolveVideoEmbedHosts(nil)
	}
	return ResolveVideoEmbedHosts(a.agentLoop.GetConfig())
}

// identityFromRequest resolves the ADR-0010 `identity` block
// (docs/specs/login-and-onboarding-spec.md §2.2): which edition this binary
// was built as, and whether the caller of THIS request is signed in.
//
// `mode` and `edition` are read from the stamped config.Edition / the
// AuthMode it derives — never from anything a request can influence. The
// boot-time EditionMisbuild tripwire (gateway_boot_credentials.go) already
// refuses to serve an unknown edition, so by the time this runs Edition is
// always one of core/desktop/hosted and EditionAuthMode() is never "".
//
// `signedIn` is resolved from the request's own context, not a global: GET
// /api/v1/state is registered withOptionalAuth, so a non-nil
// *config.UserConfig under UserContextKey is exactly what "signed in" means
// for this request — there is no separate session lookup to run.
func identityFromRequest(r *http.Request) (mode, edition string, signedIn bool, label, emailMasked string) {
	mode = string(config.EditionAuthMode())
	edition = config.Edition
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if ok && user != nil {
		signedIn = true
		label = user.Username
		emailMasked = maskEmailForIdentity(label)
	}
	return mode, edition, signedIn, label, emailMasked
}

// maskEmailForIdentity masks the local part of an email-shaped label for
// display in `identity.account.email_masked`: "daniel@elicify.ai" becomes
// "d•••@elicify.ai". A label that is not email-shaped (no "@", or "@" as the
// first character) is returned unchanged — Username is not always an email
// address (a locally chosen username, or the CLI-token synthetic identity).
func maskEmailForIdentity(label string) string {
	at := strings.IndexByte(label, '@')
	if at <= 0 {
		return label
	}
	// The first RUNE, not the first byte: a multi-byte first letter sliced at
	// [:1] is invalid UTF-8 that the JSON encoder rewrites to U+FFFD.
	_, size := utf8.DecodeRuneInString(label)
	if size > at {
		size = at
	}
	return label[:size] + "•••" + label[at:]
}

// HandleState handles GET/PATCH /api/v1/state (onboarding state).
func (a *restAPI) HandleState(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		complete := true
		var lastRun *time.Time
		var lastScore *int
		if a.onboardingMgr != nil {
			complete = a.onboardingMgr.IsComplete()
			lastRun = a.onboardingMgr.LastDoctorRun()
			lastScore = a.onboardingMgr.LastDoctorScore()
		}
		mode, edition, signedIn, label, emailMasked := identityFromRequest(r)
		identity := map[string]any{
			"mode":      mode,
			"edition":   edition,
			"signed_in": signedIn,
		}
		if signedIn {
			identity["account"] = map[string]any{
				"label":        label,
				"email_masked": emailMasked,
				"org":          nil,
			}
		} else {
			identity["blocked_reason"] = string(gen.AppStateIdentityBlockedReasonSignedOut)
		}
		resp := map[string]any{
			"onboarding_complete": complete,
			// ADR-0010, login-and-onboarding-spec.md §2.2 — which edition this
			// binary was built as, and whether THIS request is signed in. The
			// only field the UI reads to branch on edition or sign-in state.
			"identity": identity,
			// contracts/components/schemas/AppState.yaml `dev_mode_bypass` —
			// "True when gateway.dev_mode_bypass is enabled... The SPA uses
			// this to hide controls that are inoperative when bypass is
			// active." This field was documented and generated
			// (gen.AppState.DevModeBypass) but never populated here, so every
			// SPA gate keyed on it (GodModeControl / GodModeActiveBanner,
			// commit 671a68ad6) silently never engaged: `appState?.
			// dev_mode_bypass === true` read false even when bypass was on,
			// because the field was always absent, not false. Confirmed via
			// CI run 35990283526's gateway.log: 79
			// gateway.admin_route_blocked_by_bypass_gate 503s on
			// /api/v1/gateway/god-mode across the E2E job despite the SPA
			// gate's own logic being correct. Read directly from config
			// (mirrors HandleDoctor's `a.agentLoop.GetConfig()` above) —
			// read-only, no auth-decision here, matches how every other
			// dev_mode_bypass check in this package reads the same field.
			"dev_mode_bypass": a.agentLoop.GetConfig().Gateway.DevModeBypass,
			// ADR-083 CW-3 / EMB-080 — the video-embed allow-list the READER
			// uses to decide whether to draw a play control at all.
			//
			// It is resolved by the SAME function that renders the served
			// Content-Security-Policy's frame-src sources
			// (ResolveVideoEmbedHosts), because the two must be equal: a
			// reader offering a play control for a host the policy refuses
			// gives a control that does nothing, and a policy permitting a
			// host the reader will not draw gives a feature nobody can reach.
			//
			// ALWAYS PRESENT, and `[]` when the operator has declined the
			// external host — never absent. The reader has to tell "this
			// installation says no" from "the server did not answer", and an
			// omitted field collapses the two into the same undefined.
			"video_embed_hosts": a.videoEmbedHosts(),
		}
		if lastRun != nil {
			resp["last_doctor_run"] = lastRun.Format(time.RFC3339)
		}
		if lastScore != nil {
			resp["last_doctor_score"] = *lastScore
		}
		jsonOK(w, resp)
	case http.MethodPatch:
		var body gen.AppStatePatchRequest
		validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
		if !decodeAndValidate(w, r, "AppStatePatchRequest", &body, validateEnabled) {
			return
		}
		if body.OnboardingComplete == nil || !*body.OnboardingComplete {
			jsonErr(w, http.StatusBadRequest, "onboarding_complete must be true")
			return
		}
		if a.onboardingMgr != nil {
			if err := a.onboardingMgr.CompleteOnboarding(); err != nil {
				slog.Error("rest: could not persist onboarding completion", "error", err)
				jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save onboarding state: %v", err))
				return
			}
		}
		resp := gen.AppState{
			OnboardingComplete: true,
		}
		mode, edition, signedIn, label, emailMasked := identityFromRequest(r)
		resp.Identity.Mode = gen.AppStateIdentityMode(mode)
		resp.Identity.Edition = gen.AppStateIdentityEdition(edition)
		resp.Identity.SignedIn = signedIn
		if signedIn {
			resp.Identity.Account = &struct {
				EmailMasked string  `json:"email_masked"`
				Label       string  `json:"label"`
				Org         *string `json:"org"`
			}{
				EmailMasked: emailMasked,
				Label:       label,
				Org:         nil,
			}
		} else {
			reason := gen.AppStateIdentityBlockedReasonSignedOut
			resp.Identity.BlockedReason = &reason
		}
		jsonOK(w, resp)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// --- Gateway Status ---

// HandleStatus handles GET /api/v1/status (polled by StatusBar every 15s).
func (a *restAPI) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.agentLoop.GetConfig()
	v := Version
	jsonOK(w, gen.GatewayStatus{
		Online:       true,
		AgentCount:   len(cfg.Agents.List) + 1,      // +1 for system agent
		ChannelCount: countEnabledChannels(cfg) + 1, // +1 for webchat (always available)
		DailyCost:    0,
		Version:      &v,
	})
}

// HandleVersion handles GET /api/v1/version — unauthenticated build-info endpoint
// used by the frontend to detect version drift and prompt "New version available" (#110).
// Both fields are contract-constrained: `version` matches ^\d+\.\d+\.\d+(?:[-+].*)?$
// (semver) and `build_sha` matches ^([0-9a-f]{7,40}|dev)$ (see VersionResponse.yaml).
// build_sha is "dev" when built outside a version-controlled tree, else a 7-40 char
// lowercase hex SHA pulled from debug.ReadBuildInfo() vcs.revision.
func (a *restAPI) HandleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	buildSha := "dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				buildSha = s.Value
				break
			}
		}
	}
	jsonOK(w, gen.VersionResponse{
		Version:  Version,
		BuildSha: buildSha,
	})
}

// --- Devices ---

// HandleDevices handles GET /api/v1/devices. Returns pending pairing requests
// and already-paired devices. Device pairing infrastructure is not yet fully
// implemented (the device-side request entry point and persistence are
// missing); this handler returns valid empty arrays so the SPA renders its
// empty state. Dark-launched behind Sandbox.Experimental.DevicePairingEnabled
// — returns 404 when disabled (default).
// Traces to: contracts/openapi.yaml#/paths/~1devices/get (operationId: listDevices).
// Traces to: contracts/components/schemas/DevicesResponse.yaml.
func (a *restAPI) HandleDevices(w http.ResponseWriter, r *http.Request) {
	if !a.agentLoop.GetConfig().Sandbox.Experimental.DevicePairingEnabled {
		jsonErr(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	resp := gen.DevicesResponse{
		Pending: []struct {
			CreatedAt   time.Time "json:\"created_at\""
			DeviceId    string    "json:\"device_id\""
			DeviceName  string    "json:\"device_name\""
			ExpiresAt   time.Time "json:\"expires_at\""
			Fingerprint string    "json:\"fingerprint\""
			PairingCode string    "json:\"pairing_code\""
		}{},
		Paired: []struct {
			DeviceId    string                          "json:\"device_id\""
			DeviceName  string                          "json:\"device_name\""
			Fingerprint string                          "json:\"fingerprint\""
			LastSeenAt  time.Time                       "json:\"last_seen_at\""
			PairedAt    time.Time                       "json:\"paired_at\""
			Status      gen.DevicesResponsePairedStatus "json:\"status\""
		}{},
	}
	jsonOK(w, resp)
}

// The unified /api/v1/tasks handlers (HandleTasks and friends) live in
// rest_tasks.go.

// --- Activity ---

// ActivityEvent type is defined in contracts/components/schemas/ActivityEvent.yaml
// and generated into pkg/api/generated/. Use gen.ActivityEvent directly.

// HandleActivity handles GET /api/v1/activity.
// Returns up to 50 activity events from the last 24 hours, sorted reverse-chronological.
func (a *restAPI) HandleActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	var events []gen.ActivityEvent
	var sessionWarning string

	// Build agent name lookup
	cfg := a.agentLoop.GetConfig()
	agentNames := map[string]string{}
	for _, ac := range cfg.Agents.List {
		agentNames[ac.ID] = ac.Name
	}

	// Collect session_start events from all agent stores (last 24h).
	{
		// ADR-057 FR-092/FR-098 (U9's new paginated signature): this feed
		// scans every session across every store, hierarchy notwithstanding
		// (it existed before FR-091's roots/children split), so flat=true
		// preserves the pre-pagination "all agent stores" semantics exactly.
		// limit=0 means "no limit" (FR-098(b)) — the 24h cutoff below is
		// itself the real bound on how much of the result actually surfaces.
		page, partialErrs := a.agentLoop.ListAllSessions(0, 0, "", true)
		metas := page.Sessions
		if len(partialErrs) > 0 {
			agentIDs := make([]string, 0, len(partialErrs))
			for _, pe := range partialErrs {
				sanitized := sanitizePartialError(pe)
				// Extract the "agent=<id>" prefix for the summary message.
				agentLabel := sanitized
				if idx := strings.Index(sanitized, ":"); idx > 0 {
					agentLabel = sanitized[:idx]
				}
				agentIDs = append(agentIDs, agentLabel)
				slog.Warn("rest: activity: session listing failed", "error", pe)
			}
			sessionWarning = fmt.Sprintf("could not load session history for %d agents: %s (see gateway logs)",
				len(agentIDs), strings.Join(agentIDs, ", "))
		}
		{
			for _, m := range metas {
				if m.CreatedAt.After(cutoff) {
					summary := m.Title
					if summary == "" {
						summary = "New session"
					}
					agentID := m.AgentID
					agentName := agentNames[m.AgentID]
					ev := gen.ActivityEvent{
						Id:        "session-" + m.ID,
						Type:      "session_start",
						Timestamp: m.CreatedAt,
						Summary:   &summary,
					}
					if agentID != "" {
						ev.AgentId = &agentID
					}
					if agentName != "" {
						ev.AgentName = &agentName
					}
					events = append(events, ev)
				}
			}
		}
	}

	// Collect task_created / task_updated events from the unified task store.
	recentTasks, taskErr := a.taskStore.List(task.Filter{})
	if taskErr != nil {
		slog.Warn("rest: activity: list tasks", "error", taskErr)
	}
	for _, t := range recentTasks {
		if createdAt, perr := time.Parse(time.RFC3339, t.CreatedAt); perr == nil && createdAt.After(cutoff) {
			taskAgentID := t.AgentID
			title := t.Title
			ev := gen.ActivityEvent{
				Id:        "task-c-" + t.ID,
				Type:      "task_created",
				Timestamp: createdAt,
				Summary:   &title,
			}
			if taskAgentID != "" {
				ev.AgentId = &taskAgentID
			}
			events = append(events, ev)
		}
		if t.CompletedAt != "" {
			if completedAt, perr := time.Parse(time.RFC3339, t.CompletedAt); perr == nil && completedAt.After(cutoff) {
				taskAgentID := t.AgentID
				title := t.Title
				ev := gen.ActivityEvent{
					Id:        "task-u-" + t.ID,
					Type:      "task_updated",
					Timestamp: completedAt,
					Summary:   &title,
				}
				if taskAgentID != "" {
					ev.AgentId = &taskAgentID
				}
				events = append(events, ev)
			}
		}
	}

	// Sort reverse-chronological.
	slices.SortFunc(events, func(a, b gen.ActivityEvent) int {
		return b.Timestamp.Compare(a.Timestamp)
	})

	// Limit to 50 entries.
	if len(events) > 50 {
		events = events[:50]
	}

	// FINAL-REVIEW HIGH — a partial-failure session-listing warning was computed
	// above (sessionWarning) but previously only slog'd and then discarded: the
	// handler always returned the bare events array, so a caller had no way to
	// know the results were incomplete. gen.ActivityEventsResponse carries an
	// explicit Warning field for exactly this case (contracts/components/schemas/
	// ActivityEventsResponse.yaml) — return it instead of the bare array so the
	// caller can surface the partial-failure state.
	resp := gen.ActivityEventsResponse{
		Events: make([]struct {
			AgentId   *string                              `json:"agent_id,omitempty"`
			AgentName *string                              `json:"agent_name,omitempty"`
			Id        string                               `json:"id"`
			Summary   *string                              `json:"summary,omitempty"`
			Timestamp time.Time                            `json:"timestamp"`
			Type      gen.ActivityEventsResponseEventsType `json:"type"`
		}, len(events)),
	}
	for i, ev := range events {
		resp.Events[i].AgentId = ev.AgentId
		resp.Events[i].AgentName = ev.AgentName
		resp.Events[i].Id = ev.Id
		resp.Events[i].Summary = ev.Summary
		resp.Events[i].Timestamp = ev.Timestamp
		resp.Events[i].Type = gen.ActivityEventsResponseEventsType(ev.Type)
	}
	if sessionWarning != "" {
		slog.Warn("rest: activity: partial results due to session listing errors", "warning", sessionWarning)
		resp.Warning = &sessionWarning
	}
	jsonOK(w, resp)
}

// --- Storage Stats ---

// HandleStorageStats handles GET /api/v1/storage/stats.
func (a *restAPI) HandleStorageStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var sessionCount int
	var workspaceSize int64
	var warnings []string
	// ADR-057 FR-092/FR-098: this only ever needed a COUNT, never the rows —
	// page.Total is the full merged sequence length computed before slicing
	// (session.SessionListPage's doc comment), so limit=1 avoids copying the
	// whole session set into this handler just to discard it. flat=true
	// preserves the pre-pagination "every session, every store" semantics.
	if page, partialErrs := a.agentLoop.ListAllSessions(1, 0, "", true); len(partialErrs) > 0 {
		for _, pe := range partialErrs {
			slog.Warn("rest: storage stats: list sessions partial error", "error", pe)
			warnings = append(warnings, sanitizePartialError(pe))
		}
		sessionCount = page.Total
	} else {
		sessionCount = page.Total
	}
	// Walk the home directory for workspace size.
	homeDir := a.homePath
	if err := filepath.Walk(homeDir, func(_ string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			if !errors.Is(walkErr, os.ErrNotExist) {
				slog.Warn("rest: storage stats: walk error", "error", walkErr)
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		workspaceSize += info.Size()
		return nil
	}); err != nil {
		slog.Warn("rest: storage stats: walk failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("workspace size unavailable: %v", err))
	}

	resp := map[string]any{
		"workspace_size_bytes": workspaceSize,
		"session_count":        sessionCount,
		"memory_entry_count":   0,
	}
	if len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	jsonOK(w, resp)
}
