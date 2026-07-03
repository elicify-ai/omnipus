//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// ErrLastAdmin is the sentinel returned from inside a safeUpdateConfigJSON
// callback when a delete-user or role-demote mutation would leave the
// deployment with zero administrators. The last-admin guard MUST run
// against the JSON map that was just read under configMu — not against a
// stale pre-lock snapshot — so two admins concurrently demoting each other
// cannot both pass the check and leave the system admin-less.
var ErrLastAdmin = errors.New("cannot leave the deployment with zero administrators")

// ErrUserNotFound is the gateway-local sentinel. This endpoint deliberately
// does not reuse rest_auth.go's ErrUserNotFound because the HTTP mapping is
// different: auth flows respond 401 (ambiguity-on-purpose), whereas
// user-management responds 404. Kept local and unexported.
var errUserAbsent = errors.New("user not found")

// errUserExists signals duplicate-username at CREATE time; mapped to 409.
var errUserExists = errors.New("user already exists")

// usernameRE restricts usernames to an ASCII subset safe for filesystem
// paths, audit log keys, and URL path segments. The first character must be
// alphanumeric (no leading dash/dot/underscore) to reduce path-traversal
// style surface; total length is 2-63 characters. The regex matches
// exactly — no trimming, no normalization.
var usernameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$`)

// usernameInvalidMsg is returned verbatim in 400 responses. The message
// names the offending character class rather than echoing the raw input
// (which could contain control characters).
const usernameInvalidMsg = `username must start with an alphanumeric and contain only letters, digits, dots, dashes, and underscores (length 2-63)`

// roleInvalidMsg is returned for role-field validation failures. Case-
// sensitive per spec; lowercase "admin" and "user" are the only values.
const roleInvalidMsg = `role must be exactly "admin" or "user"`

// HandleUsersList handles GET /api/v1/users.
// Admin-only; dev-mode-bypass disables the endpoint (503).
//
// Returns a JSON array of {username, role, has_password, has_active_token}
// entries. Hashes (password_hash, token_hash) are NEVER included in the
// response — the flags carry the boolean presence signal the UI needs.
func (a *restAPI) HandleUsersList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := a.agentLoop.GetConfig()
	out := make([]gen.User, 0, len(cfg.Gateway.Users))
	for _, u := range cfg.Gateway.Users {
		out = append(out, gen.User{
			Username:       u.Username,
			Role:           gen.UserRole(u.Role),
			HasPassword:    u.PasswordHash != "",
			HasActiveToken: u.HasActiveToken(),
		})
	}
	jsonOK(w, out)
}

// HandleUserCreate handles POST /api/v1/users.
// Admin-only; dev-mode-bypass disables the endpoint (503).
//
// Body: {"username": string, "role": "admin"|"user", "password": string}.
// Validation is performed before any bcrypt work:
//   - Username must match usernameRE (400).
//   - Role must be exactly "admin" or "user" (400, case-sensitive).
//   - Password length ≥ 8 (400).
//
// Then the entry is persisted via safeUpdateConfigJSON. TokenHash is
// deliberately left empty at creation — the created user obtains a bearer
// token only by logging in via POST /api/v1/auth/login. No token at
// creation time, so no admin can silently issue tokens without proof of password.
//
// Single-user model: Omnipus is a single-user, self-hosted instance with no
// practical admin-vs-normal-user distinction. The wire contract still
// accepts and validates "admin"/"user" as the two legal input shapes (so
// existing/future clients aren't broken), but the role actually PERSISTED to
// config.json is always "admin" regardless of what was requested — this
// closes the create path so a new divergence between config.json and
// runtime behavior (which already treats every user as admin) can never be
// introduced. See also config.normalizeAdminOnlyRoles, which self-heals any
// pre-existing "role":"user" entries at load time.
func (a *restAPI) HandleUserCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body gen.UserCreateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "UserCreateRequest", &body, validateEnabled) {
		return
	}
	if !usernameRE.MatchString(body.Username) {
		jsonErr(w, http.StatusBadRequest, usernameInvalidMsg)
		return
	}
	if string(body.Role) != string(config.UserRoleAdmin) && string(body.Role) != string(config.UserRoleUser) {
		jsonErr(w, http.StatusBadRequest, roleInvalidMsg)
		return
	}
	if len(body.Password) < 8 {
		jsonErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	// Pre-compute the bcrypt hash outside the config lock — bcrypt is
	// intentionally slow (~100ms at DefaultCost) and holding configMu for
	// its duration would serialize every other config write behind a
	// single user-create call.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("rest: bcrypt hash password failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not create user")
		return
	}

	// Single-user model: the value actually persisted (and reported back) is
	// always admin, regardless of what body.Role requested. body.Role was
	// already validated above so the wire contract keeps accepting both
	// "admin" and "user" as legal shapes — only the persisted/observed value
	// is pinned.
	persistedRole := config.UserRoleAdmin

	if updErr := a.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, _ := m["gateway"].(map[string]any)
		if gw == nil {
			gw = map[string]any{}
			m["gateway"] = gw
		}
		var users []any
		if raw, ok := gw["users"]; ok && raw != nil {
			arr, isArr := raw.([]any)
			if !isArr {
				return fmt.Errorf("gateway.users is not an array")
			}
			users = arr
		}
		for _, u := range users {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if name, _ := um["username"].(string); name == body.Username {
				return errUserExists
			}
		}
		users = append(users, map[string]any{
			"username":      body.Username,
			"password_hash": string(passwordHash),
			"token_hash":    "", // no token at creation time — user must log in explicitly.
			"role":          string(persistedRole),
		})
		gw["users"] = users
		return nil
	}); updErr != nil {
		if errors.Is(updErr, errUserExists) {
			jsonErr(w, http.StatusConflict, "user already exists")
			return
		}
		slog.Error("rest: create user failed", "error", updErr, "username", body.Username)
		jsonErr(w, http.StatusInternalServerError, "could not create user")
		return
	}

	if err := a.triggerReloadAndWait(); err != nil {
		emitUserAudit(r, a, "gateway.users."+body.Username, nil, map[string]any{
			"username": body.Username,
			"role":     string(persistedRole),
			// requested_role: the role actually named in the request body,
			// distinct from "role" (what was persisted, always admin). Without
			// this the audit trail cannot distinguish "requested admin" from
			// "requested user, silently pinned to admin" — forensically
			// indistinguishable no-ops from real intent otherwise.
			"requested_role": string(body.Role),
			"password":       body.Password,
		})
		slog.Info("rest: user created (restart required)", "username", body.Username, "role", string(persistedRole))
		reqRestart := true
		warningMsg := "config saved to disk but hot-reload failed; restart the gateway to apply"
		w.WriteHeader(http.StatusCreated)
		jsonBodyOnlyCreated(w, gen.UserCreateResponse{
			Username:        body.Username,
			Role:            gen.UserCreateResponseRole(persistedRole),
			RequiresRestart: &reqRestart,
			Warning:         &warningMsg,
		})
		return
	}
	emitUserAudit(r, a, "gateway.users."+body.Username, nil, map[string]any{
		"username":       body.Username,
		"role":           string(persistedRole),
		"requested_role": string(body.Role), // see comment in the reload-failure branch above.
		"password":       body.Password,     // redactSensitive masks this as "***redacted***".
	})

	slog.Info("rest: user created", "username", body.Username, "role", string(persistedRole))
	w.WriteHeader(http.StatusCreated)
	jsonBodyOnlyCreated(w, gen.UserCreateResponse{
		Username: body.Username,
		Role:     gen.UserCreateResponseRole(persistedRole),
	})
}

// HandleUserDelete handles DELETE /api/v1/users/{username}.
// Admin-only; dev-mode-bypass disables the endpoint (503).
//
// The last-admin guard runs INSIDE the safeUpdateConfigJSON callback
// against the just-read JSON map. Two admins concurrently deleting each
// other race on configMu; the second caller sees the first caller's write
// on disk and blocks. A check outside the lock would admit both and strand
// the deployment.
func (a *restAPI) HandleUserDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	username, err := extractUsernameFromPath(r, "/api/v1/users/")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !usernameRE.MatchString(username) {
		jsonErr(w, http.StatusBadRequest, usernameInvalidMsg)
		return
	}

	var removedRole string
	if updErr := a.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, _ := m["gateway"].(map[string]any)
		if gw == nil {
			return errUserAbsent
		}
		raw, ok := gw["users"]
		if !ok || raw == nil {
			return errUserAbsent
		}
		users, isArr := raw.([]any)
		if !isArr {
			return fmt.Errorf("gateway.users is not an array")
		}
		idx := -1
		for i, u := range users {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if name, _ := um["username"].(string); name == username {
				idx = i
				if role, _ := um["role"].(string); role != "" {
					removedRole = role
				}
				break
			}
		}
		if idx < 0 {
			return errUserAbsent
		}
		// Splice out the target entry.
		newUsers := make([]any, 0, len(users)-1)
		newUsers = append(newUsers, users[:idx]...)
		newUsers = append(newUsers, users[idx+1:]...)
		// Last-admin guard evaluated against the POST-delete slice we are
		// about to persist. Returning ErrLastAdmin here aborts the write
		// entirely — the on-disk file is untouched.
		if countAdmins(newUsers) == 0 {
			return ErrLastAdmin
		}
		gw["users"] = newUsers
		return nil
	}); updErr != nil {
		if errors.Is(updErr, ErrLastAdmin) {
			jsonErr(w, http.StatusConflict, ErrLastAdmin.Error())
			return
		}
		if errors.Is(updErr, errUserAbsent) {
			jsonErr(w, http.StatusNotFound, "user not found")
			return
		}
		slog.Error("rest: delete user failed", "error", updErr, "username", username)
		jsonErr(w, http.StatusInternalServerError, "could not delete user")
		return
	}

	if reloadErr := a.triggerReloadAndWait(); reloadErr != nil {
		// Old value contains {username, role} only — no hash fields.
		emitUserAudit(r, a, "gateway.users."+username, map[string]any{
			"username": username,
			"role":     removedRole,
		}, nil)
		slog.Info("rest: user deleted (restart required)", "username", username)
		reqRestart := true
		warningMsg := "config saved to disk but hot-reload failed; restart the gateway to apply"
		jsonOK(w, gen.UserDeleteResponse{
			Username:        username,
			Deleted:         true,
			RequiresRestart: &reqRestart,
			Warning:         &warningMsg,
		})
		return
	}
	// Old value contains {username, role} only — no hash fields, even though
	// EmitSecuritySettingChange's recursive redactor would mask them.
	// Belt-and-suspenders: don't hand the audit pipeline data it shouldn't see.
	emitUserAudit(r, a, "gateway.users."+username, map[string]any{
		"username": username,
		"role":     removedRole,
	}, nil)

	slog.Info("rest: user deleted", "username", username)
	jsonOK(w, gen.UserDeleteResponse{
		Username: username,
		Deleted:  true,
	})
}

// HandleUserChangeRole handles PATCH /api/v1/users/{username}/role.
// Admin-only; dev-mode-bypass disables the endpoint (503).
//
// The last-admin guard is evaluated inside the callback against the
// post-mutation slice — same rationale as HandleUserDelete. NOTE: with the
// single-user model below, this guard is now unreachable dead code (the
// mutation always sets the target to admin, so countAdmins can never drop to
// zero as a result of this handler) — it is retained deliberately per the
// "keep RBAC scaffolding" decision rather than removed.
//
// Single-user model: Omnipus is a single-user, self-hosted instance with no
// practical admin-vs-normal-user distinction. The wire contract still
// accepts and validates "admin"/"user" as the two legal input shapes, but a
// role-change request — including an explicit request to demote to "user" —
// is a no-op with respect to actual authority: the value persisted to
// config.json (and reported back) is always "admin". See also
// config.normalizeAdminOnlyRoles for the load-time self-heal of any
// pre-existing "role":"user" entries.
func (a *restAPI) HandleUserChangeRole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	username, err := extractUsernameFromPath(r, "/api/v1/users/")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Strip trailing "/role" path segment.
	username = strings.TrimSuffix(username, "/role")
	if !usernameRE.MatchString(username) {
		jsonErr(w, http.StatusBadRequest, usernameInvalidMsg)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body gen.UserRoleChangeRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "UserRoleChangeRequest", &body, validateEnabled) {
		return
	}
	if string(body.Role) != string(config.UserRoleAdmin) && string(body.Role) != string(config.UserRoleUser) {
		jsonErr(w, http.StatusBadRequest, roleInvalidMsg)
		return
	}

	// Single-user model: the value actually persisted (and reported back) is
	// always admin, regardless of what body.Role requested — including an
	// explicit "user" demotion request, which becomes a no-op with respect
	// to actual authority.
	persistedRole := config.UserRoleAdmin

	var oldRole string
	if updErr := a.safeUpdateConfigJSON(func(m map[string]any) error {
		if err := a.findAndMutateUser(m, username, func(u map[string]any) error {
			oldRole, _ = u["role"].(string)
			u["role"] = string(persistedRole)
			return nil
		}); err != nil {
			return err
		}
		// Last-admin guard evaluated against the POST-mutation slice. Now
		// unreachable in practice (the mutation above always sets the target
		// to admin, so countAdmins can never drop to zero as a result of this
		// handler) — retained per the "keep RBAC scaffolding" decision. The
		// guard MUST still run here, inside the callback, after the role
		// flip — two admins concurrently demoting each other race on
		// configMu; the second writer sees the first's committed state.
		gw := m["gateway"].(map[string]any)
		users := gw["users"].([]any)
		if countAdmins(users) == 0 {
			return ErrLastAdmin
		}
		return nil
	}); updErr != nil {
		if errors.Is(updErr, ErrLastAdmin) {
			jsonErr(w, http.StatusConflict, ErrLastAdmin.Error())
			return
		}
		if errors.Is(updErr, errUserAbsent) {
			jsonErr(w, http.StatusNotFound, "user not found")
			return
		}
		slog.Error("rest: change role failed", "error", updErr, "username", username)
		jsonErr(w, http.StatusInternalServerError, "could not change role")
		return
	}

	// newValue carries both the persisted role (always admin) and the
	// originally-REQUESTED role (body.Role) so an attempted demotion is
	// audit-distinguishable from a no-op request that already asked for
	// admin — see the parallel comment in HandleUserCreate.
	auditNewValue := map[string]any{
		"role":           string(persistedRole),
		"requested_role": string(body.Role),
	}
	if reloadErr := a.triggerReloadAndWait(); reloadErr != nil {
		emitUserAudit(r, a, "gateway.users."+username+".role", oldRole, auditNewValue)
		slog.Info(
			"rest: user role changed (restart required)",
			"username",
			username,
			"old",
			oldRole,
			"new",
			string(persistedRole),
		)
		reqRestart := true
		warningMsg := "config saved to disk but hot-reload failed; restart the gateway to apply"
		jsonOK(w, gen.UserRoleChangeResponse{
			Username:        username,
			Role:            gen.UserRoleChangeResponseRole(persistedRole),
			RequiresRestart: &reqRestart,
			Warning:         &warningMsg,
		})
		return
	}
	emitUserAudit(r, a, "gateway.users."+username+".role", oldRole, auditNewValue)

	slog.Info("rest: user role changed", "username", username, "old", oldRole, "new", string(persistedRole))
	jsonOK(w, gen.UserRoleChangeResponse{
		Username: username,
		Role:     gen.UserRoleChangeResponseRole(persistedRole),
	})
}

// HandleUserResetPassword handles PUT /api/v1/users/{username}/password.
// Admin-only; dev-mode-bypass disables the endpoint (503).
//
// This is the ADMIN-resets-another-user's-password endpoint — NOT
// self-change-password. The self-change flow is POST /api/v1/auth/change-password.
//
// The callback performs two mutations atomically:
//  1. Sets the target user's password_hash to bcrypt(newPassword).
//  2. Sets the target user's token_hash to "" — this invalidates any
//     currently-issued bearer token for that user. After the config
//     refresh inside safeUpdateConfigJSON, the withAuth middleware
//     compares presented tokens against the now-empty stored hash, which
//     bcrypt.CompareHashAndPassword rejects, and unauthenticated 401
//     results. The user must log in again with the new password.
func (a *restAPI) HandleUserResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	username, err := extractUsernameFromPath(r, "/api/v1/users/")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	username = strings.TrimSuffix(username, "/password")
	if !usernameRE.MatchString(username) {
		jsonErr(w, http.StatusBadRequest, usernameInvalidMsg)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body gen.UserResetPasswordRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "UserResetPasswordRequest", &body, validateEnabled) {
		return
	}
	if len(body.Password) < 8 {
		jsonErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	// Pre-compute the new hash outside the lock — same rationale as in
	// HandleUserCreate / HandleChangePassword.
	newHash, hashErr := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if hashErr != nil {
		slog.Error("rest: bcrypt hash password failed", "error", hashErr)
		jsonErr(w, http.StatusInternalServerError, "could not reset password")
		return
	}

	if updErr := a.safeUpdateConfigJSON(func(m map[string]any) error {
		return a.findAndMutateUser(m, username, func(u map[string]any) error {
			u["password_hash"] = string(newHash)
			// Zero token_hash in the SAME transaction so the target
			// user's currently-issued bearer 401s after the refresh.
			// SEC-1 / UAT #399: also clear the whole bearer-token SET so ALL of
			// this user's sessions are revoked, not just the legacy token_hash.
			// Scoped to this one user (reset invalidates every session).
			u["token_hash"] = ""
			u["tokens"] = []any{}
			return nil
		})
	}); updErr != nil {
		if errors.Is(updErr, errUserAbsent) {
			jsonErr(w, http.StatusNotFound, "user not found")
			return
		}
		slog.Error("rest: reset password failed", "error", updErr, "username", username)
		jsonErr(w, http.StatusInternalServerError, "could not reset password")
		return
	}

	if reloadErr := a.triggerReloadAndWait(); reloadErr != nil {
		emitUserAudit(r, a, "gateway.users."+username+".password",
			map[string]any{"password": ""},
			map[string]any{"password": body.Password},
		)
		slog.Info("rest: user password reset by admin (restart required)", "username", username)
		reqRestart := true
		warningMsg := "config saved to disk but hot-reload failed; restart the gateway to apply"
		jsonOK(w, gen.UserResetPasswordResponse{
			Username:        username,
			PasswordReset:   true,
			RequiresRestart: &reqRestart,
			Warning:         &warningMsg,
		})
		return
	}
	emitUserAudit(r, a, "gateway.users."+username+".password",
		map[string]any{"password": ""},            // redacted to "***redacted***".
		map[string]any{"password": body.Password}, // redacted to "***redacted***".
	)

	slog.Info("rest: user password reset by admin", "username", username)
	jsonOK(w, gen.UserResetPasswordResponse{
		Username:      username,
		PasswordReset: true,
	})
}

// --- helpers ---

// findAndMutateUser walks m["gateway"]["users"] looking for an entry whose
// "username" field equals the given name, passes it to mutate, and returns.
// Returns errUserAbsent when no match is found. Any error from mutate
// propagates up — callers like HandleUserChangeRole use this to abort
// safeUpdateConfigJSON with ErrLastAdmin when the mutation leaves zero admins.
//
// The function modifies the user map in place. Callers that REPLACE the
// entire user entry (e.g. HandleUserDelete splicing out an entry) or APPEND
// a new entry (e.g. HandleUserCreate) should not use this helper — they manage
// the slice directly inside their own safeUpdateConfigJSON callback.
func (a *restAPI) findAndMutateUser(m map[string]any, username string, mutate func(u map[string]any) error) error {
	gw, ok := m["gateway"].(map[string]any)
	if !ok {
		return fmt.Errorf("findAndMutateUser: gateway is not a map: %T", m["gateway"])
	}
	raw, ok := gw["users"].([]any)
	if !ok {
		return fmt.Errorf("findAndMutateUser: gateway.users is not an array: %T", gw["users"])
	}
	for _, entry := range raw {
		u, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if u["username"] == username {
			return mutate(u)
		}
	}
	return errUserAbsent
}

// extractUsernameFromPath returns the path segment after prefix, or an error
// if the segment is empty. The caller is expected to further trim subpath
// suffixes (e.g. "/role", "/password") and validate the remaining username
// against usernameRE. Does NOT canonicalize the path; the caller must pass
// the exact prefix it expects ("/api/v1/users/").
func extractUsernameFromPath(r *http.Request, prefix string) (string, error) {
	path := r.URL.Path
	if !strings.HasPrefix(path, prefix) {
		return "", fmt.Errorf("path does not start with %q", prefix)
	}
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		return "", fmt.Errorf("username missing from path")
	}
	return rest, nil
}

// countAdmins counts entries in a []any of user maps whose "role" key is
// exactly "admin". Non-map entries and entries missing the role key are
// skipped. Used by the last-admin guard inside safeUpdateConfigJSON
// callbacks — MUST be called against the in-progress map, never against a
// pre-lock snapshot.
func countAdmins(users []any) int {
	n := 0
	for _, u := range users {
		um, ok := u.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := um["role"].(string); role == string(config.UserRoleAdmin) {
			n++
		}
	}
	return n
}

// emitUserAudit emits a security_setting_change audit record if the audit
// logger is enabled. Errors are logged at Warn and swallowed — user-
// management mutations are already persisted on disk and we must not 500
// due to a downstream audit failure.
func emitUserAudit(r *http.Request, a *restAPI, resource string, oldValue, newValue any) {
	if a == nil || a.agentLoop == nil {
		return
	}
	logger := a.agentLoop.AuditLogger()
	if logger == nil {
		return
	}
	if err := audit.EmitSecuritySettingChange(r.Context(), logger, resource, oldValue, newValue); err != nil {
		slog.Error("rest: audit emit user change", "resource", resource, "error", err)
	}
}

// jsonBodyOnlyCreated writes the response body after w.WriteHeader has
// already been called with 201. The stock jsonOK helper would set status
// 200 via its implicit WriteHeader call, which is wrong for POST success.
func jsonBodyOnlyCreated(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("rest: write created response body failed", "error", err)
	}
}
