package gateway

// T-07 — Configure saved-not-refreshed and credential-partial failure codes
// (channels-spec-r8, T-07; T-6b, S-58, S-47; FR-056, FR-057; US-6 AS5/AS6).
//
// Oracles (T-6b normative; the codes are part of the wire contract — FR-056):
//   S-58 file written, live refresh failed (no credential in body)
//     → 500, code `saved_not_refreshed`, exact text `settings saved, but the
//       running configuration could not be refreshed; the connection was not
//       restarted`; the file holds the new list; no restart.
//   S-58 failure before the write, no credential
//     → 500, code `could_not_save` (`could not save config`); file unchanged.
//   S-47 shape 1: single secret stored, then the configuration write fails
//     → 500, code `credential_saved_not_saved` with the T-6b truthful text —
//       NEVER `could_not_save`, never "nothing changed"; the credential store
//       holds exactly what the loop stored; the config file is unchanged; no
//       secret value in the response.
//   S-47 zero-write case: the first credential store fails (or no credential)
//       and the write path fails → 500 `could_not_save`, zero credentials stored.
//   S-47 shape 3: the saved config selects card-approved reference R; B is
//       stored, then the write fails → `credential_saved_not_saved`; the saved
//       reference still names R (a restart resolves R, not B — FR-057).
//
// Injection (the existing real-component patterns, no fakes of the code under
// test): a live-refresh failure = swapping in a real LOCKED credential store
// (the actual live loader refuses it — the channel_ordering_writers pattern);
// a configuration-write failure = chmod 0500 on the config directory; a
// store-succeeds-write-fails split = swapping api.credStore to a second REAL
// unlocked store outside the read-only directory.
//
// RED expectation: today configureChannel maps every save failure to a plain
// 500 "could not save config" with no `code` field on the wire, so every
// code/text assertion below fails.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// t07ErrorBody decodes a non-2xx Configure response into its wire map.
func t07ErrorBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out),
		"the Configure error body must be a JSON object (ChannelConfigureError); got: %s", rec.Body.String())
	return out
}

func requireWireCode(t *testing.T, body map[string]any, want string, where string) {
	t.Helper()
	require.Contains(t, body, "code",
		"%s: FR-056 makes the failure code part of the wire contract (ChannelConfigureError); the body has no code field: %v", where, body)
	require.Equal(t, want, body["code"], "%s: wire code must be %q", where, want)
}

func readConfigChannelsFile(t *testing.T, f *wu6Fixture) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(f.f1277.configPath)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	channels, _ := doc["channels"].(map[string]any)
	require.NotNil(t, channels, "instrument: config file must carry channels")
	return channels
}

func TestT07_SavedNotRefreshed_CodeAndTextAndFileHoldsNewList(t *testing.T) {
	// S-58: the live refresh fails after the file write (real locked vault:
	// the actual live loader refuses the locked prerequisite), no credential
	// in the body → 500 saved_not_refreshed; the file holds the new list.
	f, _, _ := orderingWriterFixture(t)
	previous := f.api.credStore
	f.api.credStore = credentials.NewStore(filepath.Join(f.f1277.home, "locked-credentials.json"))
	t.Cleanup(func() { f.api.credStore = previous })

	rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/configure", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	body := t07ErrorBody(t, rec)
	requireWireCode(t, body, "saved_not_refreshed", "S-58")
	require.Equal(t,
		"settings saved, but the running configuration could not be refreshed; the connection was not restarted",
		body["error"], "S-58: the normative saved_not_refreshed text")

	// The file holds the new list (durable), whatever the live state is.
	channels := readConfigChannelsFile(t, f)
	inst, _ := channels["line.healthy"].(map[string]any)
	require.NotNil(t, inst)
	require.Contains(t, inst["allow_from"], "qa1277-user",
		"S-58: the file must hold the new allow_from list")
}

func TestT07_CouldNotSave_BeforeWriteWithNoCredential_FileUnchanged(t *testing.T) {
	// S-58 second half: a failure before the write with no credential in the
	// body → 500 could_not_save; the file is unchanged. The injection is the
	// existing read-only-directory pattern (the write cannot land).
	f, _, _ := orderingWriterFixture(t)
	before := readConfigChannelsFile(t, f)

	dir := filepath.Dir(f.f1277.configPath)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(dir, info.Mode().Perm())) })

	rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/configure", `{"allow_from":["qa1277-user"]}`)
	require.NoError(t, os.Chmod(dir, info.Mode().Perm()))
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	body := t07ErrorBody(t, rec)
	requireWireCode(t, body, "could_not_save", "S-58 before-write")
	after := readConfigChannelsFile(t, f)
	require.Equal(t, before["line.healthy"], after["line.healthy"],
		"S-58: nothing changed on disk for a before-write failure with no credential")
}

func TestT07_CredentialSavedNotSaved_SingleSecretStoreSucceedsWriteFails(t *testing.T) {
	// S-47 shape 1: the credential store accepts B (second real store outside
	// the read-only config dir), then the configuration write fails →
	// credential_saved_not_saved with the T-6b text; never could_not_save.
	f, _, _ := orderingWriterFixture(t)
	before := readConfigChannelsFile(t, f)

	alt := credentials.NewStore(filepath.Join(f.f1277.home, "alt-store"))
	require.NoError(t, credentials.Unlock(alt))
	previous := f.api.credStore
	f.api.credStore = alt
	t.Cleanup(func() { f.api.credStore = previous })

	dir := filepath.Dir(f.f1277.configPath)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))

	rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/configure",
		`{"channel_access_token":"qa1277-token-B-secret"}`)
	require.NoError(t, os.Chmod(dir, info.Mode().Perm()))
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	body := t07ErrorBody(t, rec)
	requireWireCode(t, body, "credential_saved_not_saved", "S-47 shape 1")
	require.Equal(t,
		"The credential was saved, but the configuration change was not: nothing else was applied. "+
			"The connection keeps its previous token until it next restarts; whether that restart picks the new value up "+
			"depends on the credential reference the saved configuration holds (see below). Save again to retry — "+
			"a successful re-save is the general recovery.",
		body["error"], "S-47: the normative credential_saved_not_saved text")
	require.NotContains(t, rec.Body.String(), "nothing changed",
		"S-47/FR-057: the response must never claim nothing changed")
	require.NotContains(t, rec.Body.String(), "could_not_save",
		"S-47: credential_saved_not_saved must not degrade to could_not_save")

	// The store holds exactly what the loop stored.
	stored, err := alt.Get("channel_line.healthy_channel_access_token")
	require.NoError(t, err, "S-47: B must be retrievable under the loop's key")
	require.Equal(t, "qa1277-token-B-secret", stored)

	// The config file is unchanged.
	after := readConfigChannelsFile(t, f)
	require.Equal(t, before["line.healthy"], after["line.healthy"],
		"S-47: the configuration write must not have landed")

	// FR-053/NB-8: the secret value appears in neither the response nor the body.
	require.NotContains(t, rec.Body.String(), "qa1277-token-B-secret")
}

func TestT07_ZeroCredentialsStored_CouldNotSave(t *testing.T) {
	// S-47 zero-write case: the first credential store already fails (locked
	// real store) and the write path fails → could_not_save with ZERO
	// credentials stored (the pre-credential failure path, precisely named).
	f, _, _ := orderingWriterFixture(t)
	locked := credentials.NewStore(filepath.Join(f.f1277.home, "locked-store"))
	previous := f.api.credStore
	f.api.credStore = locked
	t.Cleanup(func() { f.api.credStore = previous })

	dir := filepath.Dir(f.f1277.configPath)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(dir, info.Mode().Perm())) })

	rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/configure",
		`{"channel_access_token":"qa1277-token-B-secret"}`)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	body := t07ErrorBody(t, rec)
	requireWireCode(t, body, "could_not_save", "S-47 zero-write")

	// Zero credentials stored: the main store must not hold the value under
	// the loop's key (the store attempt failed).
	_, getErr := f.store.Get("channel_line.healthy_channel_access_token")
	require.Error(t, getErr, "S-47 zero-write: no credential of this request may be stored")
}

func TestT07_CredentialSavedNotSaved_SavedReferenceStillNamesR(t *testing.T) {
	// S-47 shape 3: the saved config selects card-approved reference R;
	// B is stored, then the write fails → credential_saved_not_saved; the
	// saved reference still names R (a restart resolves R, not B — FR-057's
	// reference-conditional recovery).
	f, _, _ := orderingWriterFixture(t)

	// Point the saved instance at card-approved R and store R (post-boot
	// fixture surgery through the real config write path).
	require.NoError(t, f.store.Set("QA1277_CARD_REF", "qa1277-card-R-value"))
	require.NoError(t, f.api.safeUpdateConfigJSON(func(doc map[string]any) error {
		channels, ok := doc["channels"].(map[string]any)
		require.True(t, ok, "instrument: config must carry channels")
		inst, ok := channels["line.healthy"].(map[string]any)
		require.True(t, ok, "instrument: line.healthy instance must exist")
		inst["channel_access_token_ref"] = "QA1277_CARD_REF"
		return nil
	}))

	alt := credentials.NewStore(filepath.Join(f.f1277.home, "alt-store-r"))
	require.NoError(t, credentials.Unlock(alt))
	previous := f.api.credStore
	f.api.credStore = alt
	t.Cleanup(func() { f.api.credStore = previous })

	dir := filepath.Dir(f.f1277.configPath)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0o500))

	rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/configure",
		`{"channel_access_token":"qa1277-token-B-secret"}`)
	require.NoError(t, os.Chmod(dir, info.Mode().Perm()))
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	body := t07ErrorBody(t, rec)
	requireWireCode(t, body, "credential_saved_not_saved", "S-47 shape 3")

	// R is still the saved reference (the write did not land), and R's value
	// is intact in the main store; B sits only in the loop's store.
	after := readConfigChannelsFile(t, f)
	inst, ok := after["line.healthy"].(map[string]any)
	require.True(t, ok, "instrument: line.healthy instance must survive the failed save")
	require.Equal(t, "QA1277_CARD_REF", inst["channel_access_token_ref"],
		"FR-057: the saved reference must still name R after the failed save")
	rValue, err := f.store.Get("QA1277_CARD_REF")
	require.NoError(t, err)
	require.Equal(t, "qa1277-card-R-value", rValue, "FR-057: R stays intact")
	bValue, err := alt.Get("channel_line.healthy_channel_access_token")
	require.NoError(t, err)
	require.Equal(t, "qa1277-token-B-secret", bValue,
		"S-47 shape 3: B stays stored but unreferenced until a configuration save selects it")
}

func TestT07_MultiSecretSecondStoreFailureNeedsSeam(t *testing.T) {
	// S-47 shape 2 (r4): Slack bot_token stored, the app_token store then
	// fails → credential_saved_not_saved (bot_token is in the store), never
	// could_not_save. There is no way to fail exactly the second store of a
	// multi-secret request with real components: the credential loop has no
	// injectable per-store failure seam. WP-1 must expose the seam named in
	// the RED report (an injectable credential-loop store step).
	t.Fatalf("BLOCKED: no seam to fail exactly the second credential store of a multi-secret request — required by S-47 shape 2 / R3-MAJ-004 A / FR-056; WP-1 must provide an injectable credential-loop store step named in the RED report")
}
