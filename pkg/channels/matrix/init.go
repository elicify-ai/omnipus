// goolm is the pure-Go OLM implementation (replaces libolm which requires CGo).
// Build with: -tags goolm
//go:build goolm

package matrix

import (
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

func init() {
	channels.RegisterFactory(
		"matrix",
		func(cfg *config.Config, instanceID string, secrets credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
			inst := cfg.Channels[instanceID]
			matrixCfg := config.InstanceToMatrix(inst)
			cryptoDatabasePath := matrixCfg.CryptoDatabasePath
			if cryptoDatabasePath == "" {
				// Namespace by instanceID (mirrors whatsapp_native/init.go): two
				// Matrix instances (e.g. "matrix.eu" and "matrix.us") must never
				// share one Olm/Megolm store, or their E2E crypto state collides —
				// each instance would overwrite the other's device/session keys.
				channelDir := filepath.Join(cfg.WorkspacePath(), "matrix")
				cryptoDatabasePath = filepath.Join(channelDir, instanceID)
				migrateLegacyMatrixCryptoStore(cfg, channelDir, instanceID, cryptoDatabasePath)
			}
			return NewMatrixChannel(matrixCfg, secrets, b, cryptoDatabasePath)
		},
	)
}

// migrateLegacyMatrixCryptoStore performs a one-time, best-effort migration of
// a pre-namespacing Olm/Megolm crypto store (previously written directly at
// channelDir, e.g. "<workspace>/matrix/store.db") into the new
// instance-namespaced directory ("<workspace>/matrix/<instanceID>/store.db").
//
// This exists because, unlike the identical namespacing gap already fixed for
// whatsapp_native (see whatsapp_native/init.go) — where losing the old
// session store just costs a cheap QR re-pair — losing the Matrix Olm/Megolm
// store makes every E2E-encrypted room's history PERMANENTLY undecryptable.
// That is a materially worse outcome, so this bugfix does not lean on the
// project's usual "fresh build, no back-compat" default for a namespacing
// change; it migrates the unambiguous common case and loudly warns on every
// other case rather than silently orphaning real crypto state.
//
// Deliberately conservative:
//   - Only migrates when a legacy flat store file exists directly under
//     channelDir (i.e. this deployment predates namespacing) AND nothing yet
//     exists at the destination (never overwrites, never re-triggers once
//     migrated).
//   - Only migrates when exactly one Matrix instance is enabled in config. A
//     pre-namespacing deployment with two-plus Matrix instances already had
//     those instances silently colliding on the same shared file (that
//     collision IS this bug) — there is no way to know which instance's keys
//     are in the shared file, so migrating it to one arbitrarily chosen
//     instance would be a guess dressed up as a fix. That case is left in
//     place with a loud, repeating warning so an operator can resolve it
//     manually instead.
//   - Never blocks channel construction: a failed or skipped migration falls
//     back to the new namespaced path (an empty, freshly-initialized crypto
//     store) — matching this codebase's "fresh build" default for every other
//     namespacing gap — but only after a WARN log makes the behavior change
//     visible, so it is never silent.
func migrateLegacyMatrixCryptoStore(cfg *config.Config, channelDir, instanceID, namespacedPath string) {
	legacyDBFile := filepath.Join(channelDir, dbName)
	if _, err := os.Stat(legacyDBFile); err != nil {
		return // no legacy flat store present — fresh install, or already namespaced
	}
	if _, err := os.Stat(namespacedPath); err == nil {
		return // destination already exists — never overwrite, and already migrated
	}

	enabledMatrixInstances := 0
	for _, other := range cfg.Channels {
		if other.Enabled && other.Type == "matrix" {
			enabledMatrixInstances++
		}
	}
	if enabledMatrixInstances != 1 {
		logger.WarnCF(
			"matrix",
			"Legacy un-namespaced Olm/Megolm crypto store found, but multiple Matrix instances are configured — cannot safely determine which instance owns it, so automatic migration was skipped. The instance(s) that do NOT end up using this shared store will start a fresh crypto identity and lose access to previously encrypted room history. Manually move or copy the store into the correct instance directory to preserve it.",
			map[string]any{
				"legacy_path":              legacyDBFile,
				"instance_id":              instanceID,
				"enabled_matrix_instances": enabledMatrixInstances,
			},
		)
		return
	}

	// namespacedPath is nested inside channelDir (channelDir/instanceID), so
	// renaming channelDir directly onto namespacedPath would be renaming a
	// directory into its own subtree, which the OS rejects (EINVAL). Stage the
	// move through a sibling temp path instead: move channelDir out of the way,
	// recreate channelDir, then move the temp into its final namespaced slot.
	tmpPath := channelDir + ".pre-namespacing-migration-tmp"
	if err := os.RemoveAll(tmpPath); err != nil {
		logger.WarnCF(
			"matrix",
			"Failed to clear a stale migration temp directory — skipping automatic crypto store migration; the legacy store was NOT touched",
			map[string]any{
				"tmp_path": tmpPath, "error": err.Error(),
			},
		)
		return
	}
	if err := os.Rename(channelDir, tmpPath); err != nil {
		logger.WarnCF(
			"matrix",
			"Failed to stage the legacy crypto store for namespacing migration — continuing with a fresh namespaced store; the OLD store is still on disk at its original location and was NOT deleted",
			map[string]any{
				"legacy_path": channelDir, "error": err.Error(),
			},
		)
		return
	}
	if err := os.MkdirAll(channelDir, 0o700); err != nil {
		if restoreErr := os.Rename(tmpPath, channelDir); restoreErr != nil {
			logger.WarnCF(
				"matrix",
				"Failed to restore the legacy crypto store after a failed migration step — store may be stranded at the temp path; check this path manually",
				map[string]any{
					"tmp_path": tmpPath, "intended_path": channelDir, "restore_error": restoreErr.Error(),
				},
			)
			return
		}
		logger.WarnCF(
			"matrix",
			"Failed to recreate the channel directory during crypto store migration — continuing with a fresh namespaced store; the OLD store was restored to its original location",
			map[string]any{
				"path": channelDir, "error": err.Error(),
			},
		)
		return
	}
	if err := os.Rename(tmpPath, namespacedPath); err != nil {
		logger.WarnCF(
			"matrix",
			"Failed to move the legacy crypto store into its namespaced location — continuing with a fresh namespaced store; the OLD store is preserved (not deleted) at the temp path",
			map[string]any{
				"tmp_path": tmpPath, "namespaced_path": namespacedPath, "error": err.Error(),
			},
		)
		return
	}
	logger.WarnCF(
		"matrix",
		"Migrated a pre-namespacing Olm/Megolm crypto store to its instance-namespaced path (one-time automatic migration triggered by the multi-instance namespacing fix)",
		map[string]any{
			"from": legacyDBFile,
			"to":   filepath.Join(namespacedPath, dbName),
		},
	)
}
