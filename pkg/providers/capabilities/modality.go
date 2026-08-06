package capabilities

// modality type — typed Modality string with known-constants set.
//
// The catalog records each model's input modality set. The wire shape
// is a JSON string array; the validated domain uses a typed Modality
// string with named constants. Operators may legitimately add new
// modalities (e.g. "3d", "hologram") ahead of runtime support — the
// validator accepts any non-empty unknown modality and the catalog
// records it as-is. The runtime never invents a modality name from
// thin air; the typed constants are the public API, the known-modality
// set is the runtime's recognition boundary.

// Modality is one input modality a model accepts. The seed MAY carry
// "unknown" modality values (forward-compat for new formats); the
// typed constants below are the runtime's public API.
type Modality string

// Known modality constants. A model advertised as accepting one of
// these passes capability-gate checks for that modality. The set is
// the runtime's recognition boundary; not a closed enum — the seed
// can carry additional values without breaking validation.
const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityPDF   Modality = "pdf"
	ModalityAudio Modality = "audio"
	ModalityVideo Modality = "video"
)

// KnownModality is the runtime's recognition boundary. Compare a
// Modality against this set to decide whether the runtime can route
// it (true) or whether it is a forward-compat unknown (false — still
// recorded in the catalog, still observable via Supports).
//
// Intentionally a `map[Modality]bool` rather than a slice so the
// constant-time lookup is the public read pattern; callers should NOT
// enumerate it for ordering decisions.
var KnownModality = map[Modality]bool{
	ModalityText:  true,
	ModalityImage: true,
	ModalityPDF:   true,
	ModalityAudio: true,
	ModalityVideo: true,
}
