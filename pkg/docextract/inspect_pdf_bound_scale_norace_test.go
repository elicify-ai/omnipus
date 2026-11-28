//go:build linux && !race

package docextract

// pdfBoundScale multiplies the wall-clock ceilings of
// TestInspectPDF_PathologicalInputsBounded (the child process time limit and
// the 2s inspection deadline). See the race variant for why -race needs more.
const pdfBoundScale = 1
