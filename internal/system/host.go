package system

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// The machine, as Linux tells it in /proc. Inside a container /proc/stat,
// /proc/meminfo, /proc/loadavg and /proc/uptime describe the whole server and
// not the container, which is what is wanted here: the question is how loaded
// the server is, and TeamUp's other containers (the database, the call server)
// share it. Without /proc (macOS while developing) these report errors and the
// page says the numbers are not available.

// cpuTimes are the cumulative jiffies of all CPUs together.
type cpuTimes struct {
	total, idle, steal uint64
}

// readCPU reads the first line of /proc/stat: user nice system idle iowait irq
// softirq steal guest guest_nice. guest and guest_nice are already counted in
// user and nice, so the total is the first eight. Waiting on the disk counts as
// idle: the CPU could have done something else.
func readCPU(proc string) (cpuTimes, int, error) {
	f, err := os.Open(filepath.Join(proc, "stat"))
	if err != nil {
		return cpuTimes{}, 0, err
	}
	defer func() { _ = f.Close() }()

	var t cpuTimes
	cores, found := 0, false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		if fields[0] != "cpu" {
			cores++
			continue
		}
		if len(fields) < 9 {
			return cpuTimes{}, 0, fmt.Errorf("short cpu line in /proc/stat")
		}
		var v [8]uint64
		for i := range v {
			n, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return cpuTimes{}, 0, fmt.Errorf("cpu field %d: %w", i, err)
			}
			v[i] = n
			t.total += n
		}
		t.idle = v[3] + v[4]
		t.steal = v[7]
		found = true
	}
	if err := sc.Err(); err != nil {
		return cpuTimes{}, 0, err
	}
	if !found {
		return cpuTimes{}, 0, fmt.Errorf("no cpu line in /proc/stat")
	}
	return t, cores, nil
}

// cpuBetween is how busy the CPUs were between two readings, and how much of
// the time the hypervisor kept for others ("steal": on a burstable cloud server
// out of credit, this is the server being slowed down), both in per cent.
func cpuBetween(a, b cpuTimes) (busy, steal float64, ok bool) {
	if b.total <= a.total {
		return 0, 0, false
	}
	total := float64(b.total - a.total)
	idle := float64(b.idle - a.idle)
	busy = 100 * (total - idle) / total
	steal = 100 * float64(b.steal-a.steal) / total
	return clampPct(busy), clampPct(steal), true
}

func clampPct(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

// readMemory is the total and the memory available to start new work without
// swapping, in bytes. MemAvailable counts the page cache that can be given back,
// which "free" does not, so a server that has read a lot is not called full.
func readMemory(proc string) (total, available uint64, err error) {
	f, err := os.Open(filepath.Join(proc, "meminfo"))
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		n, perr := strconv.ParseUint(fields[1], 10, 64)
		if perr != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = n * 1024
		case "MemAvailable:":
			available = n * 1024
		}
	}
	if err := sc.Err(); err != nil {
		return 0, 0, err
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("no MemTotal in /proc/meminfo")
	}
	return total, available, nil
}

// readLoad is the one-minute load average: how many things wanted a CPU, on
// average. Above the number of cores, work is queueing.
func readLoad(proc string) (float64, error) {
	b, err := os.ReadFile(filepath.Join(proc, "loadavg"))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty /proc/loadavg")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// readUptime is how long the server has been running, in seconds.
func readUptime(proc string) (float64, error) {
	b, err := os.ReadFile(filepath.Join(proc, "uptime"))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty /proc/uptime")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// readDisk is the size of the file system holding path and the space an
// ordinary program may still use. In a container "/" is on the server's disk,
// so this is the server's disk.
func readDisk(path string) (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize) //nolint:gosec // block size is never negative
	return st.Blocks * bsize, st.Bavail * bsize, nil
}
