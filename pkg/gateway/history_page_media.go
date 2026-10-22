package gateway

import (
	"encoding/json"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// The original generated frame and resolved physical paths are prepared once.
// Each ascending candidate filters only these cached facts; selection itself
// proceeds newest-first, so sharing a replay state's seenPaths would give the
// newest alias ownership instead of the earliest selected source occurrence.
// Encoded subsets are memoized too, so final output never resolves or replays.
type historyPreparedMedia struct {
	frame    generated.MediaFrame
	paths    []string
	original []byte
	variants map[string][]byte
}

func newHistoryPreparedMedia(frame generated.MediaFrame, raw []byte, store *historyReadOnlyMedia) *historyPreparedMedia {
	prepared := &historyPreparedMedia{frame: frame, original: raw, paths: make([]string, len(frame.Parts))}
	if store != nil {
		for i, part := range frame.Parts {
			prepared.paths[i] = store.urls[part.Url]
		}
	}
	return prepared
}

func (m *historyPreparedMedia) filtered(seen map[string]struct{}) ([]byte, error) {
	mask := make([]byte, len(m.paths))
	kept := 0
	for i, path := range m.paths {
		if path != "" {
			if _, duplicate := seen[path]; duplicate {
				continue
			}
			seen[path] = struct{}{}
		}
		mask[i] = 1
		kept++
	}
	if kept == len(m.paths) {
		return m.original, nil
	}
	if kept == 0 {
		return nil, nil
	}
	key := string(mask)
	if raw, ok := m.variants[key]; ok {
		return raw, nil
	}
	frame := m.frame
	frame.Parts = make([]generated.MediaPart, 0, kept)
	for i, part := range m.frame.Parts {
		if mask[i] != 0 {
			frame.Parts = append(frame.Parts, part)
		}
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	if m.variants == nil {
		m.variants = make(map[string][]byte)
	}
	m.variants[key] = raw
	return raw, nil
}

func (g historyPreparedGroup) appendFrames(out [][]byte, seen map[string]struct{}) ([][]byte, error) {
	for i, raw := range g.frames {
		if media := g.media[i]; media != nil {
			var err error
			raw, err = media.filtered(seen)
			if err != nil {
				return nil, err
			}
		}
		if raw != nil {
			out = append(out, raw)
		}
	}
	return out, nil
}
