package main

import (
	"bufio"
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// The Go runtime ignores container memory limits, so by default the heap may
// grow to twice its live size before a collection: on a 512 MiB host a single
// large restore or burst of long prompts then ends in an OOM kill. Unless
// GOMEMLIMIT is set explicitly, use a soft limit below the cgroup or physical
// memory so the collector works harder before the kernel intervenes.
func applyMemoryLimit() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	limit := availableMemory()
	if limit <= 0 {
		return
	}
	// Leave headroom for SQLite's C-heap page cache, goroutine stacks and the
	// binary itself, none of which count toward the Go heap limit.
	soft := limit / 10 * 7
	debug.SetMemoryLimit(soft)
	slog.Info("memory limit applied", "available_mib", limit>>20, "soft_limit_mib", soft>>20)
}

// availableMemory returns the smallest of the cgroup v2, cgroup v1 and
// physical memory limits, or 0 when none can be determined (e.g. non-Linux).
func availableMemory() int64 {
	var limit int64
	consider := func(n int64) {
		// cgroup v1 reports "unlimited" as a value near MaxInt64.
		if n > 0 && n < 1<<50 && (limit == 0 || n < limit) {
			limit = n
		}
	}
	for _, path := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		if data, err := os.ReadFile(path); err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
				consider(n)
			}
		}
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 && fields[0] == "MemTotal:" {
				if kib, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					consider(kib << 10)
				}
				break
			}
		}
	}
	return limit
}
