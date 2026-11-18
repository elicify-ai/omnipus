package openai_compat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// reasoningDetailsAccum joins OpenRouter's streamed reasoning_details
// fragments into the items a non-streaming response would carry
// (effort-levels spec FR-013, S-30): fragments with the same index are one
// item; their text and summary pieces are concatenated in arrival order and
// every other field (data, signature, id, format, unknown vendor fields) is
// kept as sent. Items without an index each stay separate.
type reasoningDetailsAccum struct {
	byIndex map[int]map[string]any
	order   []int
	loose   []map[string]any
}

// joinedDetailFields are the fragment fields that are concatenated.
var joinedDetailFields = map[string]bool{"text": true, "summary": true}

// add folds one streamed reasoning_details item into the accumulator.
func (a *reasoningDetailsAccum) add(raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var item map[string]any
	if err := dec.Decode(&item); err != nil {
		return fmt.Errorf("reasoning_details item: %w", err)
	}
	idxNum, ok := item["index"].(json.Number)
	if !ok {
		a.loose = append(a.loose, item)
		return nil
	}
	idx64, err := idxNum.Int64()
	if err != nil {
		return fmt.Errorf("reasoning_details index %q: %w", idxNum, err)
	}
	idx := int(idx64)
	if a.byIndex == nil {
		a.byIndex = make(map[int]map[string]any)
	}
	cur, seen := a.byIndex[idx]
	if !seen {
		a.byIndex[idx] = item
		a.order = append(a.order, idx)
		return nil
	}
	for k, v := range item {
		if prev, ok := cur[k].(string); ok && joinedDetailFields[k] {
			if s, ok := v.(string); ok {
				cur[k] = prev + s
				continue
			}
		}
		cur[k] = v
	}
	return nil
}

// items returns the joined items in index order (unindexed items last), or
// nil when the stream carried none.
func (a *reasoningDetailsAccum) items() ([]ReasoningDetail, error) {
	if len(a.order) == 0 && len(a.loose) == 0 {
		return nil, nil
	}
	idx := append([]int(nil), a.order...)
	sort.Ints(idx)
	out := make([]ReasoningDetail, 0, len(idx)+len(a.loose))
	for _, i := range idx {
		raw, err := json.Marshal(a.byIndex[i])
		if err != nil {
			return nil, fmt.Errorf("reasoning_details item %d: %w", i, err)
		}
		out = append(out, raw)
	}
	for _, it := range a.loose {
		raw, err := json.Marshal(it)
		if err != nil {
			return nil, fmt.Errorf("reasoning_details item: %w", err)
		}
		out = append(out, raw)
	}
	return out, nil
}
