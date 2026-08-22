//go:build !windows && !linux

package knowledge

// maxRSSUnitBytes is the unit of getrusage's ru_maxrss on Darwin and the BSDs:
// bytes.
const maxRSSUnitBytes = 1
