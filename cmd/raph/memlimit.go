package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"

	"raph/internal/verbose"
)

// applyMemoryLimit installs a soft heap ceiling so a large index or graph read
// can't run RSS away. Precedence:
//  1. GOMEMLIMIT — honored natively by the Go runtime; we don't touch it.
//  2. RAPH_MEMORY_LIMIT — an explicit override ("2GiB", "1500MiB", or raw bytes).
//  3. Otherwise a default of 80% of detected host/cgroup memory.
//
// It is best-effort: any detection failure leaves the runtime default (no
// limit) in place. The limit is *soft* — Go spends more GC effort as the heap
// approaches it rather than crashing, so a generous default is safe.
func applyMemoryLimit() {
	if strings.TrimSpace(os.Getenv("GOMEMLIMIT")) != "" {
		return // runtime already applied the operator's explicit limit
	}
	if v := strings.TrimSpace(os.Getenv("RAPH_MEMORY_LIMIT")); v != "" {
		if bytes := parseByteSize(v); bytes > 0 {
			debug.SetMemoryLimit(bytes)
			verbose.Printf("memory limit set to %d bytes (RAPH_MEMORY_LIMIT)", bytes)
			return
		}
		// Unparseable value: warn and fall through to auto-detection rather than
		// silently leaving the process with no guard at all.
		fmt.Fprintf(os.Stderr, "raph: warning: ignoring unparseable RAPH_MEMORY_LIMIT=%q; using detected memory\n", v)
	}
	total := detectTotalMemory()
	if total <= 0 {
		return
	}
	limit := total / 100 * 80 // 80% guard against OOM, not a throughput throttle
	debug.SetMemoryLimit(limit)
	verbose.Printf("memory limit set to %d bytes (80%% of detected %d)", limit, total)
}

// parseByteSize accepts raw bytes or an IEC/SI suffix (KiB/MiB/GiB, KB/MB/GB,
// K/M/G), case-insensitively ("2gb", "1500MiB", "2GB" all work).
func parseByteSize(s string) int64 {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, suf := range []struct {
		tag string
		m   int64
	}{
		{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10},
		{"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1},
	} {
		if v, ok := strings.CutSuffix(s, suf.tag); ok {
			s = strings.TrimSpace(v)
			mult = suf.m
			break
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n * mult
}

// detectTotalMemory returns total memory in bytes for linux (cgroup-aware) and
// darwin, or 0 when it can't tell.
func detectTotalMemory() int64 {
	host := hostMemory()
	if cg := cgroupMemoryLimit(); cg > 0 && (host == 0 || cg < host) {
		return cg
	}
	return host
}

// hostMemory reads physical RAM: /proc/meminfo on linux, sysctl on darwin.
func hostMemory() int64 {
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "MemTotal:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					return kb * 1024 // MemTotal is in kB
				}
			}
		}
	}
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// cgroupMemoryLimit reads a container's memory ceiling (cgroup v2, then v1),
// returning 0 when unlimited or unset. It resolves the process's own cgroup
// path from /proc/self/cgroup first, because in nested slices (Kubernetes,
// systemd) the limit lives there, not at the controller root — reading only the
// root file would miss the pod limit and let the guard fall back to host RAM.
func cgroupMemoryLimit() int64 {
	candidates := []string{
		"/sys/fs/cgroup/memory.max",                   // v2 root
		"/sys/fs/cgroup/memory/memory.limit_in_bytes", // v1 root
	}
	if rel := cgroupRelPath(); rel != "" {
		candidates = append([]string{
			"/sys/fs/cgroup" + rel + "/memory.max",                   // v2 nested
			"/sys/fs/cgroup/memory" + rel + "/memory.limit_in_bytes", // v1 nested
		}, candidates...)
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		raw := strings.TrimSpace(string(data))
		if raw == "" || raw == "max" {
			continue
		}
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 && n < (1<<62) {
			return n
		}
	}
	return 0
}

// cgroupRelPath returns the process's cgroup path relative to the mount root
// (e.g. "/kubepods/pod123/abc"), or "" if it can't be determined. It prefers
// the cgroup v2 unified line ("0::<path>").
func cgroupRelPath() string {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	var fallback string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 {
			continue
		}
		path := strings.TrimSpace(fields[2])
		if path == "" || path == "/" {
			continue
		}
		if fields[0] == "0" { // cgroup v2 unified hierarchy
			return path
		}
		if strings.Contains(fields[1], "memory") && fallback == "" {
			fallback = path // cgroup v1 memory controller
		}
	}
	return fallback
}
