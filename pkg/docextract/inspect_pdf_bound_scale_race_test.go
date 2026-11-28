//go:build linux && race

package docextract

// pdfBoundScale multiplies the wall-clock ceilings of
// TestInspectPDF_PathologicalInputsBounded, RAISED under the race detector.
//
// The detector instruments every memory access, so building the 32 MiB
// flate-expansion fixture and inflating it costs several times its normal
// wall clock: under -race the flate-expansion child needed more than the
// 5s child limit and died with "panic: test timed out after 5s" before it
// could report (the same at 98d67fae31, before the shared admission fix).
// Scaling every ceiling by the same factor keeps what the test proves (the
// hostile input ends as a typed error or a bounded result inside a finite
// limit, a hang still fails) without asserting a number the detector makes
// meaningless. The ordinary, parent-cycle and xref-size children take about
// 1s under -race, so the factor leaves them unchanged in meaning.
const pdfBoundScale = 6
