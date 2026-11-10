package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
)

// historyVerifyEmptyUnpublished permits only resuming the new-identity path.
// It never repairs, truncates, replaces or adopts an existing content baseline.
// The caller holds the session shard, as for initial identity publication.
func historyVerifyEmptyUnpublished(dir, sid string) (cp historyCheckpoint, err error) {
	for _, path := range []string{dir, historyIndexPath(dir), filepath.Join(historyIndexPath(dir), "generations")} {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return cp, statErr
		}
		if !info.IsDir() {
			return cp, errors.New("unpublished history baseline has an invalid directory")
		}
	}
	if _, statErr := os.Lstat(filepath.Join(dir, "meta.json")); statErr == nil {
		return cp, errors.New("history identity is already published")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return cp, statErr
	}
	cp, err = historyLoadCheckpoint(context.Background(), dir, sid)
	if err != nil {
		return cp, err
	}
	if cp.Root != 0 || cp.Rank != 0 || len(cp.ArchiveEnds) != 0 || cp.DispositionEnd != 0 || cp.CurrentKey != "" || cp.Anchor != nil || len(cp.Facts) != 0 {
		return cp, errors.New("history baseline is not a pristine empty checkpoint")
	}
	current, err := os.Lstat(filepath.Join(dir, archiveCurrentFile))
	if err != nil {
		return cp, err
	}
	if !current.Mode().IsRegular() || current.Size() != 0 {
		return cp, errors.New("unpublished history archive is not empty")
	}
	streams, dispositionEnd, expired, currentKey, err := historyCaptureStreams(dir, cp)
	if err != nil {
		return cp, err
	}
	if len(streams) != 0 || dispositionEnd != 0 || len(expired) != 0 || currentKey != "" {
		return cp, errors.New("unpublished history contains prior source state")
	}
	// A new empty generation consists of its one complete checksummed commit
	// footer. Check actual bytes, not merely a checkpoint claiming root/rank0.
	want, err := historyEncodeFrame(historyFrame{Commit: &cp})
	if err != nil {
		return cp, err
	}
	info, err := os.Lstat(filepath.Join(historyIndexPath(dir), "generations", cp.File))
	if err != nil {
		return cp, err
	}
	if !info.Mode().IsRegular() || info.Size() != cp.MutationEnd || cp.MutationEnd != int64(len(want)) {
		return cp, errors.New("unpublished history has incomplete or nonempty generation state")
	}
	tree, err := historyOpenTree(context.Background(), dir, cp, false)
	if err != nil {
		return cp, err
	}
	defer func() {
		if closeErr := tree.file.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	got := make([]byte, len(want))
	if _, err = tree.file.ReadAt(got, 0); err != nil {
		return cp, err
	}
	if !bytes.Equal(got, want) {
		return cp, errors.New("unpublished history checkpoint disagrees with its empty footer")
	}
	return cp, nil
}
