package catalog

// Source-boundary words for a context tier. models.dev publishes tier.size
// and does not say whether that size is still on the base rate. These values
// are set in Go. They are not read from catalog JSON: the catalog's JSON key
// for this meaning is not published yet.
const (
	// CostBoundaryUpTo means the source says "up to N" at the base rate and
	// "over N" at the higher band, so exactly N stays on the base rate.
	CostBoundaryUpTo = "up_to"
	// CostBoundaryIncluding means the source includes N in the higher band.
	CostBoundaryIncluding = "including"
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
// boundary token count. SourceBoundary says whether that count is still base.
type ModelCostTier struct {
	Input          *float64    `json:"input,omitempty"`
	Output         *float64    `json:"output,omitempty"`
	Reasoning      *float64    `json:"reasoning,omitempty"`
	CacheRead      *float64    `json:"cache_read,omitempty"`
	CacheWrite     *float64    `json:"cache_write,omitempty"`
	CacheWrite1h   *float64    `json:"cache_write_1h,omitempty"`
	Tier           ContextTier `json:"tier"`
	SourceBoundary string      `json:"-"`
}

// ContextTier is models.dev's tier object. Type is "context".
type ContextTier struct {
	Type string `json:"type"`
	Size int    `json:"size"`
}
