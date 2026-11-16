package main

import (
	"net/http"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var started = time.Now()

func (a agent) metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var disk syscall.Statfs_t
	if e := syscall.Statfs(a.root, &disk); e != nil {
		http.Error(w, "filesystem statistics unavailable", 503)
		return
	}
	load, _ := os.ReadFile("/proc/loadavg")
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	jsonReply(w, 200, map[string]any{"observed_at": time.Now().UTC(), "uptime_seconds": time.Since(started).Seconds(), "logical_cpus": runtime.NumCPU(), "loadavg": strings.TrimSpace(string(load)), "disk_available_bytes": disk.Bavail * uint64(disk.Bsize), "disk_total_bytes": disk.Blocks * uint64(disk.Bsize), "agent_heap_bytes": m.Alloc})
}
