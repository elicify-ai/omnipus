package catalog

import (
	"os"
	"testing"
)

// BenchmarkResolve — SC-011: mean < 1,000 ns/op with 0 allocs (-benchmem).
// Wall-clock figures are recorded in perf-nightly only; the alloc count is
// also asserted by TestResolve_ZeroAllocs.
func BenchmarkResolve(b *testing.B) {
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		b.Fatal(err)
	}
	c, err := NewCatalog(data)
	if err != nil {
		b.Fatal(err)
	}
	keys := [][2]string{
		{"openrouter", "z-ai/glm-5.2"}, // hit
		{"zai", "glm-5.2"},             // hit
		{"openrouter", "glm-5.2"},      // miss (DS-2.3)
		{"nope", "glm-5.2"},            // miss, unknown provider
	}
	b.ReportAllocs()
	b.ResetTimer()
	var sink int
	for i := 0; i < b.N; i++ {
		k := keys[i&3]
		h := c.Resolve(k[0], k[1])
		sink += h.Window() + h.Budget().LongEdgePx
		if h.Supports(ModalityImage) {
			sink++
		}
	}
	if sink == 0 {
		b.Fatal("sink unused")
	}
}
