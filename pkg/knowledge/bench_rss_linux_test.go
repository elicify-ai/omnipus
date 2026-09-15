//go:build linux

package knowledge

// maxRSSUnitBytes is the unit of getrusage's ru_maxrss on Linux: kilobytes.
const maxRSSUnitBytes = 1024
