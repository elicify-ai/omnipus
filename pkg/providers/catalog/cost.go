package catalog

import "encoding/json"

// Tier boundary words. They live on the catalog tier object as "boundary".
// Absent means above: the source says "over N", so exactly N stays on the
// lower band. That is the models.dev convention, including the
// context_over_200k alias. at_least means N belongs to the higher band.
const (
	CostBoundaryAbove   = "above"
	CostBoundaryAtLeast = "at_least"
)

// ModelCost is one models.dev cost object, in US dollars per 1 million tokens.
// A nil pointer is an absent price. A pointer to 0 is a published zero.
// CacheWrite1h is the catalog extension for a 1-hour write. models.dev does
// not publish that field; when the catalog does, this is where it lands.
type ModelCost struct {
	Input           *float64        `json:"input,omitempty"`
	Output          *float64        `json:"output,omitempty"`
	Reasoning       *float64        `json:"reasoning,omitempty"`
	CacheRead       *float64        `json:"cache_read,omitempty"`
	CacheWrite      *float64        `json:"cache_write,omitempty"`
	CacheWrite1h    *float64        `json:"cache_write_1h,omitempty"`
	InputAudio      *float64        `json:"input_audio,omitempty"`
	OutputAudio     *float64        `json:"output_audio,omitempty"`
	Tiers           []ModelCostTier `json:"tiers,omitempty"`
	ContextOver200k *ModelCost      `json:"context_over_200k,omitempty"`
}

// ModelCostTier is one context-length band. Prices on the tier are that
// band. Prices on the parent ModelCost are the base band. Tier.Size is the
// boundary token count. Tier.Boundary says which band that count belongs to.
type ModelCostTier struct {
	Input        *float64    `json:"input,omitempty"`
	Output       *float64    `json:"output,omitempty"`
	Reasoning    *float64    `json:"reasoning,omitempty"`
	CacheRead    *float64    `json:"cache_read,omitempty"`
	CacheWrite   *float64    `json:"cache_write,omitempty"`
	CacheWrite1h *float64    `json:"cache_write_1h,omitempty"`
	Tier         ContextTier `json:"tier"`
}

// ContextTier is models.dev's tier object. Type is "context". Boundary is
// the catalog key "boundary": "above" or "at_least". JSON with no boundary
// unmarshals as above.
type ContextTier struct {
	Type     string `json:"type"`
	Size     int    `json:"size"`
	Boundary string `json:"boundary,omitempty"`
}

// UnmarshalJSON reads a tier object and treats a missing boundary as above.
func (t *ContextTier) UnmarshalJSON(data []byte) error {
	type plain ContextTier
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if p.Boundary == "" {
		p.Boundary = CostBoundaryAbove
	}
	*t = ContextTier(p)
	return nil
}
