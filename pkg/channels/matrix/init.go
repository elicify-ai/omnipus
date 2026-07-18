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
				channelDir := filepath.Join(cfg.AgentHomeBasePath(), "matrix")
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
//   - Crash-safe: this function is checkpointed by staging through tmpPath
//     (see below) before ever touching the final namespaced location, and
//     EVERY call re-checks tmpPath for leftovers from an earlier, interrupted
//     attempt before deciding "there is nothing to migrate" — see the
//     tmpPath-first check up front. Without that check, a process kill
//     between the two renames below would leave channelDir looking exactly
//     like a fresh install (empty or missing) on the next boot, silently and
//     permanently stranding the real crypto store at tmpPath forever.
func migrateLegacyMatrixCryptoStore(cfg *config.Config, channelDir, instanceID, namespacedPath string) {
	// namespacedPath is nested inside channelDir (channelDir/instanceID), so
	// renaming channelDir directly onto namespacedPath would be renaming a
	// directory into its own subtree, which the OS rejects (EINVAL). Stage the
	// move through a sibling temp path instead: move channelDir out of the way,
	// recreate channelDir, then move the temp into its final namespaced slot.
	tmpPath := channelDir + ".pre-namespacing-migration-tmp"
	legacyDBFile := filepath.Join(channelDir, dbName)

	// Computed once, up front, from cfg only (no I/O) — used to gate BOTH the
	// crash-resume path below and the fresh-migration path further down. The
	// ambiguity guard must block every filesystem-mutating step this function
	// can take, not just the fresh-migration one: there is no way to know
	// which instance's keys are in a shared legacy/staged store, so mutating
	// the filesystem before this check is consulted reintroduces the exact
	// "guess dressed up as a fix" this function exists to prevent.
	enabledMatrixInstances := 0
	for _, other := range cfg.Channels {
		if other.Enabled && other.Type == "matrix" {
			enabledMatrixInstances++
		}
	}

	_, legacyStatErr := os.Stat(legacyDBFile)
	legacyPresent := legacyStatErr == nil
	if legacyStatErr != nil && !os.IsNotExist(legacyStatErr) {
		// Something other than "file not found" (e.g. permission denied) —
		// treating this the same as "nothing to migrate" could paper over a
		// real problem, so it gets its own loud, distinct log instead of
		// silently falling into the fresh-install fast path below.
		logger.WarnCF(
			"matrix",
			"Failed to check for a legacy un-namespaced Olm/Megolm crypto store — skipping automatic migration this run rather than guessing; the legacy store, if any, was NOT touched",
			map[string]any{
				"legacy_path": legacyDBFile,
				"instance_id": instanceID,
				"error":       legacyStatErr.Error(),
			},
		)
		return
	}

	// This check is deliberately independent of legacyPresent above. A
	// tmpPath left over here means an EARLIER migration attempt was
	// interrupted (process killed, OOM-kill, host eviction) after staging the
	// legacy store at tmpPath but before it reached namespacedPath. In that
	// state channelDir is empty or missing, so the legacyDBFile check above
	// found nothing and would otherwise be indistinguishable from a genuine
	// fresh install — the real store would be silently and permanently
	// orphaned at tmpPath with zero diagnostic trail.
	if tmpInfo, err := os.Stat(tmpPath); err == nil && tmpInfo.IsDir() {
		if enabledMatrixInstances != 1 {
			// An interrupted migration was detected, but with multiple Matrix
			// instances enabled there is no way to know which instance's keys
			// are staged at tmpPath — auto-resuming here would silently
			// complete exactly the guess this function's ambiguity guard
			// exists to prevent (see the doc comment above). Whichever
			// instance's factory call happens to run first (Go map iteration
			// order is randomized in manager.go's initChannels) would
			// otherwise win the staged store while every other instance
			// silently gets a fresh, empty identity. Leave BOTH tmpPath and
			// namespacedPath untouched — do not MkdirAll or Rename — and
			// loudly ask for manual resolution instead.
			logger.ErrorCF(
				"matrix",
				"An Olm/Megolm crypto store migration was interrupted mid-flight (likely a process crash) and could NOT be automatically resumed, because multiple Matrix instances are configured — there is no way to safely determine which instance's keys are staged at the temp path below. The ORIGINAL crypto store may still be recoverable there and was deliberately left untouched. Resolve this manually: once you know which instance the staged store belongs to, move it into that instance's namespaced directory yourself; until then, every enabled Matrix instance is running on a fresh, empty crypto identity and previously encrypted room history will remain permanently undecryptable",
				map[string]any{
					"tmp_path":                 tmpPath,
					"namespaced_path":          namespacedPath,
					"instance_id":              instanceID,
					"enabled_matrix_instances": enabledMatrixInstances,
				},
			)
			return
		}
		if _, destErr := os.Stat(namespacedPath); os.IsNotExist(destErr) {
			// Nothing has used the namespaced destination yet since the crash
			// (no fresh store was created there), so it is safe to finish the
			// interrupted move — this is exactly the final step a healthy run
			// would have taken. Confirm tmpPath actually looks like our
			// staged legacy store (not some unrelated directory that happens
			// to collide with this exact suffix) before touching it.
			if _, err := os.Stat(filepath.Join(tmpPath, dbName)); err == nil {
				// channelDir (namespacedPath's parent) may not exist yet — the
				// crash could have landed before the interrupted attempt's own
				// os.MkdirAll ran. os.Rename requires the destination's parent
				// to exist, and MkdirAll on an already-existing directory is a
				// harmless no-op, so this is safe for both possible crash
				// points (before or after that MkdirAll).
				if err := os.MkdirAll(channelDir, 0o700); err != nil {
					logger.ErrorCF(
						"matrix",
						"Detected an interrupted Olm/Megolm crypto store migration but failed to recreate the channel directory needed to resume it — the ORIGINAL crypto store remains safe and untouched at the temp path below; resolve this filesystem error and it will be retried on next start, or move it manually",
						map[string]any{
							"tmp_path":    tmpPath,
							"channel_dir": channelDir,
							"instance_id": instanceID,
							"error":       err.Error(),
						},
					)
					return
				}
				if err := os.Rename(tmpPath, namespacedPath); err == nil {
					logger.WarnCF(
						"matrix",
						"Detected an Olm/Megolm crypto store migration that was interrupted mid-flight (likely a process crash between the migration's two rename steps) and automatically resumed it — the legacy store staged at the temp path has now been moved into its namespaced location and no data was lost",
						map[string]any{
							"tmp_path":        tmpPath,
							"namespaced_path": namespacedPath,
							"instance_id":     instanceID,
						},
					)
					return
				}
				// Resume attempt itself failed — fall through to the loud,
				// unconditional warning below instead of swallowing this.
			}
		}
		// Either the namespaced destination already exists (something else
		// already started using a fresh store there since the crash, so
		// overwriting it would destroy that data too) or the resume attempt
		// above did not succeed. Do NOT delete or otherwise touch tmpPath —
		// it may be the only remaining copy of this instance's E2E keys.
		logger.ErrorCF(
			"matrix",
			"An Olm/Megolm crypto store migration was interrupted mid-flight (likely a process crash) and could NOT be automatically completed — the ORIGINAL crypto store may still be recoverable at the temp path below and was deliberately left untouched. Manually inspect it and, if it contains a genuine store, move it into this instance's namespaced directory yourself; until that is done, this instance is running on a fresh, empty crypto identity and previously encrypted room history will remain permanently undecryptable",
			map[string]any{
				"tmp_path":        tmpPath,
				"namespaced_path": namespacedPath,
				"instance_id":     instanceID,
			},
		)
		return
	}

	if !legacyPresent {
		return // no legacy flat store present — fresh install, or already namespaced
	}
	if _, err := os.Stat(namespacedPath); err == nil {
		return // destination already exists — never overwrite, and already migrated
	}

	if enabledMatrixInstances != 1 {
		logger.WarnCF(
			"matrix",
			"Legacy un-namespaced Olm/Megolm crypto store found, but multiple Matrix instances are configured — cannot safely determine which instance owns it, so automatic migration was skipped for ALL of them. None of these instances will automatically inherit this shared store: every one of them will start a fresh crypto identity and lose access to previously encrypted room history unless you manually move the legacy store into the correct instance's directory.",
			map[string]any{
				"legacy_path":              legacyDBFile,
				"instance_id":              instanceID,
				"enabled_matrix_instances": enabledMatrixInstances,
			},
		)
		return
	}

	// tmpPath is guaranteed not to exist as a directory at this point (the
	// check above returns early otherwise), so this only ever clears a stray
	// non-directory leftover before this invocation stages its own migration
	// there fresh — it can never destroy a stranded backup from an earlier,
	// unrelated crash.
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
