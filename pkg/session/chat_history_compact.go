package session

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Retention copies only retained reference metadata into a new journal. Old
// rich nodes are retired after the last old-cut reader closes; expired token
// lookups survive solely as position -> rank entries, on disk, not a hot map.
func (us *UnifiedStore) historyCompact(ctx context.Context, dir string, old *chatHistoryIndexTree, cp historyCheckpoint, expired []string) (*chatHistoryIndexTree, historyCheckpoint, error) {
	gone := map[string]bool{}
	for _, key := range expired {
		gone[key] = true
		delete(cp.ArchiveEnds, key)
	}
	fresh, next, err := historyCreateTree(ctx, dir, cp.SessionID)
	if err != nil {
		return nil, cp, err
	}
	next.Rank = cp.Rank
	next.ArchiveEnds = cp.ArchiveEnds
	next.CurrentKey = cp.CurrentKey
	next.DispositionEnd = cp.DispositionEnd
	next.Anchor = cp.Anchor
	if next.Anchor != nil && next.Anchor.Stream == "archive" && gone[next.Anchor.Record.Address.PartitionKey] {
		next.Anchor = nil
	}
	retained := func(rank uint64) (bool, error) {
		if rank == 0 {
			return false, nil
		}
		r, ok, err := historyReadRecordMeta(old, rank)
		return ok && !gone[r.Address.PartitionKey], err
	}
	err = old.walk(func(n historyNode) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := n.Key
		switch {
		case strings.HasPrefix(key, "p/"):
			// Value is only rank. Position is the dictionary key.
		case strings.HasPrefix(key, "dup/"), strings.HasPrefix(key, "last/"), strings.HasPrefix(key, "i/"):
			return nil
		case strings.HasPrefix(key, "r/"):
			var r historyRecord
			if err := json.Unmarshal(n.Value, &r); err != nil {
				return err
			}
			if gone[r.Address.PartitionKey] {
				return nil
			}
		case strings.HasPrefix(key, "a/"), strings.HasPrefix(key, "e/"), strings.HasPrefix(key, "b/"):
			var rank uint64
			if err := json.Unmarshal(n.Value, &rank); err != nil {
				return err
			}
			ok, err := retained(rank)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		case strings.HasPrefix(key, "d/"), strings.HasPrefix(key, "c/"):
			rank, err := strconv.ParseUint(key[2:], 10, 64)
			if err != nil {
				return err
			}
			ok, err := retained(rank)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if key[:2] == "c/" {
				var cancel uint64
				if err = json.Unmarshal(n.Value, &cancel); err != nil {
					return err
				}
				ok, err = retained(cancel)
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
			} else {
				var refs []uint64
				if err = json.Unmarshal(n.Value, &refs); err != nil {
					return err
				}
				keep := make([]uint64, 0, len(refs))
				for _, r := range refs {
					ok, err := retained(r)
					if err != nil {
						return err
					}
					if ok {
						keep = append(keep, r)
					}
				}
				return fresh.set(key, keep)
			}
		case strings.HasPrefix(key, "s/"):
			var refs historySpanRef
			if err := json.Unmarshal(n.Value, &refs); err != nil {
				return err
			}
			for _, p := range []*uint64{&refs.Start, &refs.State, &refs.Message, &refs.End} {
				ok, err := retained(*p)
				if err != nil {
					return err
				}
				if !ok {
					*p = 0
				}
			}
			if refs.Start == 0 && refs.State == 0 && refs.Message == 0 && refs.End == 0 {
				return nil
			}
			if refs.State == 0 {
				refs.HasRun = nil
			}
			return fresh.set(key, refs)
		case strings.HasPrefix(key, "q/"):
			var spans []string
			if err := json.Unmarshal(n.Value, &spans); err != nil {
				return err
			}
			keep := []string{}
			for _, span := range spans {
				var refs historySpanRef
				ok, err := old.get(historyIDKey("s/", span), &refs)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("missing span metadata")
				}
				present := false
				for _, rank := range []uint64{refs.Start, refs.State, refs.Message, refs.End} {
					ok, err := retained(rank)
					if err != nil {
						return err
					}
					present = present || ok
				}
				if present {
					keep = append(keep, span)
				}
			}
			if len(keep) == 0 {
				return nil
			}
			return fresh.set(key, keep)
		default:
			return errors.New("unknown history metadata key")
		}
		var value any
		if err := json.Unmarshal(n.Value, &value); err != nil {
			return err
		}
		// RawMessage avoids rounding lossless rank/offset integers through float64.
		return fresh.set(key, n.Value)
	})
	if err == nil {
		err = old.walk(func(n historyNode) error {
			if !strings.HasPrefix(n.Key, "r/") {
				return nil
			}
			var r historyRecord
			if err := json.Unmarshal(n.Value, &r); err != nil {
				return err
			}
			if gone[r.Address.PartitionKey] || !r.Eligible {
				return nil
			}
			if err := historyReduceDuplicate(fresh, "message", r.Address.EntryID, r.Rank); err != nil {
				return err
			}
			for _, id := range r.Calls {
				if err := historyReduceDuplicate(fresh, "tool", id, r.Rank); err != nil {
					return err
				}
			}
			if r.Clear {
				if err := fresh.set("last/clear", r.Rank); err != nil {
					return err
				}
			}
			if r.Role == "assistant" {
				if err := fresh.set("last/assistant", r.Rank); err != nil {
					return err
				}
				if err := fresh.set(historyIDKey("last/turn/", r.Turn), r.Rank); err != nil {
					return err
				}
			}
			var d historyDispositionRef
			if ok, err := old.get(historyIDKey("i/", r.Address.EntryID), &d); err != nil {
				return err
			} else if ok {
				return fresh.set(historyIDKey("i/", r.Address.EntryID), d)
			}
			return nil
		})
	}
	if err != nil {
		fresh.file.Close()
		return nil, cp, err
	}
	return fresh, next, nil
}
