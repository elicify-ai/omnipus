package docextract

// resetPDFStuckForTest clears the count of timed-out PDF parses and frees all
// parse slots so a test that deliberately leaves stuck parsers behind does not
// affect later tests.
func resetPDFStuckForTest() {
	pdfStuck.Store(0)
	for {
		select {
		case <-pdfSlots:
		default:
			return
		}
	}
}
