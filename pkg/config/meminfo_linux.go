// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// readMemTotalBytes reads the total physical memory from /proc/meminfo.
// Returns a conservative 4 GB fallback when the file cannot be read or parsed.
func readMemTotalBytes() uint64 {
	const fallback = 4 * 1024 * 1024 * 1024 // 4 GB
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return fallback
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		// Format: "MemTotal:     16384000 kB"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return fallback
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return fallback
		}
		return kb * 1024
	}
	return fallback
}
