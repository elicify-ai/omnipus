//go:build goolm && stdjson

package gateway

// Order 38 — TestPermissionsPatch_PerKey (S-82, S-99; Dataset D6; the PATCH
// rules block is the oracle). Rides the order 5/8 harness in this package
// (newPermissionsAPI / patchPermissions / getPermissions / requireErrContains
// / attachTestAuditor / readAuditEntries): the real route table built by
// registerAdditionalEndpoints, so today's red is a 404 for every leg until
// PATCH /security/permissions exists (C-2).
//
// Legs the earlier orders do NOT already pin:
//   - S-82: two tabs changing different keys both keep their change (a key
//     the request does not name is never written); each request names only
//     its own key.
//   - S-99: the audit holds exactly one change entry per changed key with
//     the entry's key, old value, new value and actor asserted (CHECK round
//     1 finding 6: count alone passed a wrong-keyed entry); equal-value
//     writes add none.
//   - D6: empty request → 400 "Nothing to change."; null member or value →
//     400 naming the key; one bad key → 400 naming the first bad key with
//     nothing saved — the FULL read model is compared unchanged against a
//     pre-request snapshot (CHECK round 1 finding 6: demanding one
//     earlier-leg value absent punished correct atomic preservation);
//     the same skill twice in one map, differing only by letter case →
//     400 "`plan` is listed twice." (spec D6 row 9, C-23 case-insensitive
//     skill ids) — CHECK round 1 finding 6: permission and visibility for
//     one skill together are INDEPENDENT per-key maps and must be accepted,
//     so a third leg asserts exactly that.
//
// Each leg builds a fresh API so legs cannot read each other's stored state
// (CHECK round 1 finding 6: the bad-key leg read the shared API's earlier
// leg and mistook a preserved value for a leak).
import (
	"encoding/json"
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"

	"github.com/stretchr/testify/require"
)

func TestPermissionsPatch_PerKey(t *testing.T) {
	t.Run("S-82: two writes naming different keys both keep their change", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "first tab's write (body: %s)", w.Body.String())
		w = patchPermissions(t, api, routes, `{"policies":{"send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "second tab's write (body: %s)", w.Body.String())

		w = getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, w.Code, "read model (body: %s)", w.Body.String())
		require.Contains(t, w.Body.String(), `"plan":"ask"`, "tab A's change survives tab B's write")
		require.Contains(t, w.Body.String(), `"send_email":"deny"`, "tab B's change is stored")
	})

	t.Run("S-99: one audit entry per changed key with key, old, new and actor; none for equal-value writes", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		auditDir := attachTestAuditor(t, api)
		// Explicit old values, not assumptions about shipped defaults. A
		// two-key write also detects crossed or aggregated audit records.
		seed := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask"},"policies":{"send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, seed.Code, "setup: %s", seed.Body.String())
		before := len(readAuditEntries(t, auditDir, audit.EventSecuritySettingChange))

		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"},"policies":{"send_email":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		entries := readAuditEntries(t, auditDir, audit.EventSecuritySettingChange)
		require.Len(t, entries, before+2, "C-2/S-99: exactly one canonical audit record per changed key")
		observed := make([]map[string]any, 0, 2)
		for _, e := range entries[before:] {
			// These are EmitSecuritySettingChange's published field names.
			for _, field := range []string{"resource", "old_value", "new_value", "actor"} {
				require.Contains(t, e, field, "S-99: each individual record carries all four fields")
			}
			observed = append(observed, map[string]any{
				"resource": e["resource"], "old_value": e["old_value"], "new_value": e["new_value"], "actor": e["actor"],
			})
		}
		require.ElementsMatch(t, []map[string]any{
			{"resource": "sandbox.skill_policies.plan", "old_value": "ask", "new_value": "deny", "actor": envTokenName},
			{"resource": "sandbox.tool_policies.send_email", "old_value": "deny", "new_value": "ask", "actor": envTokenName},
		}, observed, "S-99 binds each key, old, new and authenticated actor together")

		w = patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"},"policies":{"send_email":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "equal-value write is a valid no-op (body: %s)", w.Body.String())
		require.Equal(t, entries, readAuditEntries(t, auditDir, audit.EventSecuritySettingChange),
			"equal-value writes add or alter no change entry")
	})

	t.Run("D6: an empty request is 400 Nothing to change.", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{}`)
		requireErrContains(t, w, http.StatusBadRequest, "Nothing to change.")
	})

	t.Run("D6: a null value is 400 naming the key", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":null}}`)
		requireErrContains(t, w, http.StatusBadRequest, "plan")
	})

	t.Run("D6: one bad key refuses the whole request and the full state is unchanged", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"policies":{"send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: the valid key stores (body: %s)", w.Body.String())

		snapshot := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, snapshot.Code, "setup: the read model answers")

		w = patchPermissions(t, api, routes, `{"policies":{"send_email":"allow"},"skill_policies":{"not-a-known-skill":"ask"}}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "validate before write (body: %s)", w.Body.String())

		after := getPermissions(t, api, routes)
		require.Equal(t, snapshot.Body.String(), after.Body.String(),
			"D6: nothing stored when one key is bad — the FULL read model is byte-identical, "+
				"including the value a correct atomic write preserves")
	})

	t.Run("D6 row 9: the same skill twice in one map, differing by letter case, is refused", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		auditDir := attachTestAuditor(t, api)
		seed := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"allow"},"policies":{"send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, seed.Code, "setup: preserve an EXISTING plan value: %s", seed.Body.String())
		before := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, before.Code, "setup read model: %s", before.Body.String())
		entries := readAuditEntries(t, auditDir, audit.EventSecuritySettingChange)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask","PLAN":"deny"}}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "C-23 duplicate request: %s", w.Body.String())
		var failure gen.ErrorResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &failure))
		require.Equal(t, "`plan` is listed twice.", failure.Error, "D6#9 exact refusal, not an unrelated error")

		after := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, after.Code, "after read model: %s", after.Body.String())
		require.JSONEq(t, before.Body.String(), after.Body.String(), "D6#9: complete stored state, including pre-existing plan, is unchanged")
		require.Equal(t, entries, readAuditEntries(t, auditDir, audit.EventSecuritySettingChange), "a refused request writes no change audit")
	})

	t.Run("D6: permission and visibility for one skill together are accepted (independent maps)", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask"},"skill_visibility":{"plan":"name_only"}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"C-2's per-key maps are independent: one request may set a skill's permission "+
				"AND its visibility (body: %s)", w.Body.String())

		w = getPermissions(t, api, routes)
		require.Contains(t, w.Body.String(), `"plan":"ask"`, "the permission half is stored")
		require.Contains(t, w.Body.String(), `"plan":"name_only"`, "the visibility half is stored")
	})
}
