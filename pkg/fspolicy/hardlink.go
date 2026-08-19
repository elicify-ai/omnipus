// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// hardlinkScanBudget caps how many directory entries one IsCarveOut call may
// examine while looking for the other end of a hard link.
//
// It exists because the scan is unbounded in principle — $OMNIPUS_HOME/agents
// contains every agent's whole working tree — and an unbounded scan on a hot
// path is its own kind of outage. Exhausting it is treated as "cannot decide",
// which DENIES (see aliasesSecretDirectory): a scan that ran out of budget has
// not ruled out a link into a secret, and the deny side is where this decision
// fails safe.
//
// Sized from measurement, and the measurement is not flattering.
// TestHardlink_ScanCostIsBounded walks real entries on this machine's APFS
// volume at 8µs each on an idle machine and 17µs each with the rest of the test
// suite running alongside — essentially one lstat per entry either way. So the
// full budget caps a single scan at roughly 0.8–1.7 SECONDS. (The constant was
// 200_000 until the loaded figure pushed the extrapolation past the test's own
// 3s ceiling; the test says to lower the constant rather than relax the
// assertion, and that is what happened.) That is the deliberate trade, stated
// rather than buried:
//
//	budget too low   a large install (agents/<id>/sessions/ runs to tens of
//	                 thousands of JSONL files) exhausts it, and exhaustion
//	                 DENIES. A pnpm project in the work dir would then have
//	                 ordinary reads refused. A broken product.
//	budget too high  a genuinely hard-linked file can stall a turn for over a
//	                 second. Slow, visible, and recoverable.
//
// The second is the better failure, so the budget is generous. It is only ever
// paid by a file that is ACTUALLY multiply linked — the Nlink gate means an
// ordinary file costs one stat and nothing else (measured at 172µs per
// IsCarveOut against the same 20,000-file tree, i.e. the scan never ran).
const hardlinkScanBudget = 100_000

// aliasesSecretDirectory reports whether candidate is a HARD LINK to a file
// that lives inside one of the DIRECTORY-shaped carve-out roots.
//
// # The hole this closes
//
// pathidentity.go's identity primitive asks "is any ancestor of the candidate
// the same FILE as the container". For a FILE-shaped secret that is exact: a
// hard link to credentials.json has credentials.json's inode, the chain's first
// entry matches, and it is denied. For a DIRECTORY-shaped secret it can never
// match — the alias's inode is a file inode, the container's is a directory's,
// and every one of the alias's ancestors is inside the work dir. So the check
// returned relOutside, confidently, and the file was served.
//
// Measured through the real tool chain (ResolvePath -> PathHandle.ReadFile and
// the send path), with the alias planted in the agent's own working directory:
//
//	credentials.json     (file secret, control)  -> DENIED
//	master.key           (file secret, control)  -> DENIED
//	backups/full.tar.gz  (the WHOLE VAULT)       -> LEAKED, read and send
//	system/audit.jsonl                           -> LEAKED
//	entities/agents/mia.json                     -> LEAKED
//
// backups/*.tar.gz is the worst of those: the gateway's own archive of all of
// $OMNIPUS_HOME, so one alias yields master.key, credentials.json, config.json,
// cli.token, auth.json and every entity record at once.
//
// # Why it is checked before the own-tree exception, not inside the loop
//
// The alias is a real file at a real path inside the work dir. Reached through
// IsCarveOut's per-root loop, the deny leg matches (the alias is under
// $OMNIPUS_HOME/agents) and then the own-tree exception un-matches it, because
// by path it genuinely IS the agent's own file. Only its CONTENT belongs to
// someone else. So the question has to be settled before the path rules run.
//
// # Cost
//
// Three gates, cheapest first, so the ordinary case pays one stat:
//
//  1. candidate must be an existing REGULAR file — directories cannot be hard
//     linked on any platform Omnipus targets.
//  2. its link count must exceed 1 — an ordinary file has exactly one link, so
//     this alone eliminates essentially every real call.
//  3. the container must be a DIRECTORY — file-shaped secrets are already
//     handled exactly by identity, and re-checking them here would be wasted
//     work with no new answer.
//
// The caller's OWN tree is excluded from the scan. That is required for
// correctness, not just for speed: two files inside an agent's own work dir
// hard linked to each other (pnpm does this constantly) are the agent's own
// files, and denying them would break ordinary work.
//
// # Failure, and the residual — both stated because the last residual claim in
// this package was wrong
//
// A scan that cannot complete — a listing error, or the budget above — returns
// DENY. There is no "assume it is fine" branch; not being able to see inside a
// secret directory is not evidence that the link does not point there.
//
// The residual that remains, precisely:
//
//	non-unix platforms  linkCount cannot be evaluated (os.FileInfo.Sys()
//	                    carries no link count on Windows), so gate 2 cannot be
//	                    reached and the scan does not run. The alternative —
//	                    scanning unconditionally on every path check, because
//	                    the gate is unavailable — would walk every agent's tree
//	                    on every file operation. That is not a trade worth
//	                    making for a platform with no kernel sandbox at all;
//	                    it is recorded here as a gap, not resolved.
//	created mid-scan    a link created while the scan is in flight may be
//	                    missed by that one call. The next call sees it; this is
//	                    the same freshness bound every filesystem check has.
func aliasesSecretDirectory(candidate string, policy FSPolicy) bool {
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	links, ok := linkCount(info)
	if !ok || links < 2 {
		return false
	}

	cleanWorkDir := filepath.Clean(policy.WorkDir)
	budget := hardlinkScanBudget
	// A hard link cannot cross filesystems, so a root on a different device can
	// never alias this file. Knowing the candidate's device lets whole roots be
	// skipped before a single directory entry is read — which is the difference
	// between walking an external volume's carve-out and not touching it.
	candidateDev, haveDev := deviceID(info)

	for _, root := range policy.CarveOuts {
		cleanRoot := filepath.Clean(root)
		rootInfo, statErr := os.Stat(cleanRoot)
		if statErr != nil || !rootInfo.IsDir() {
			// Absent, or file-shaped and therefore already exact by identity.
			continue
		}

		// Cross-device roots cannot hold a hard link to the candidate. Skipping
		// them is exact, not an optimisation that trades away detection: link(2)
		// fails with EXDEV across filesystems, so no alias can exist there.
		// Falls through to scanning whenever either device is unknown.
		if rootDev, haveRootDev := deviceID(rootInfo); haveDev && haveRootDev && rootDev != candidateDev {
			continue
		}

		// The own-tree exception, applied to the scan under the same per-root
		// rule IsCarveOut applies to paths: the work dir is skipped only when
		// it is a proper descendant of THIS root.
		var skip string
		if policy.WorkDir != "" && isProperDescendant(cleanWorkDir, cleanRoot) {
			skip = cleanWorkDir
		}

		found, decided := scanForSameFile(cleanRoot, skip, info, &budget)
		if !decided || found {
			return true
		}
	}
	return false
}

// scanForSameFile walks root looking for a regular file that is the same file
// as target, skipping the subtree at skip (empty means skip nothing).
//
// Returns (found, decided). decided=false means the walk could not be
// completed — a listing error or an exhausted budget — and the caller must
// treat that as a deny.
//
// Lstat semantics (WalkDir's default) rather than Stat: a hard link shares an
// inode, a symlink does not, so following symlinks would add cost and cycle
// risk while finding nothing this is looking for.
func scanForSameFile(root, skip string, target os.FileInfo, budget *int) (found, decided bool) {
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skip != "" && p == skip {
			return fs.SkipDir
		}
		if *budget <= 0 {
			return errScanBudgetExhausted
		}
		*budget--
		if !d.Type().IsRegular() {
			return nil
		}
		entryInfo, infoErr := d.Info()
		if infoErr != nil {
			if os.IsNotExist(infoErr) {
				// Removed between the readdir and the stat. It cannot be the
				// link we are looking for: the candidate still exists.
				return nil
			}
			return infoErr
		}
		if os.SameFile(entryInfo, target) {
			found = true
			return errScanFoundLink
		}
		return nil
	})

	switch {
	case err == nil:
		return false, true
	case errors.Is(err, errScanFoundLink):
		return true, true
	default:
		// errScanBudgetExhausted, a listing failure, a permission error: the
		// scan did not rule anything out.
		return false, false
	}
}

// errScanFoundLink and errScanBudgetExhausted are sentinel walk terminators.
// They never escape scanForSameFile.
//
// Deliberately NOT fs.SkipAll: WalkDir swallows fs.SkipAll and returns nil, so
// a "found it" signalled that way would be indistinguishable from "walked the
// whole tree and found nothing" — a silent false NEGATIVE on the deny side,
// which is the one direction this must never fail in.
var (
	errScanFoundLink       = errors.New("fspolicy: hard-link scan found the other end of the link")
	errScanBudgetExhausted = errors.New("fspolicy: hard-link scan budget exhausted; the link could not be ruled out")
)
