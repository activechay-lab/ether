package perfharness

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Usage is a server's cumulative resource counters at one instant.
type Usage struct {
	CPU        time.Duration // user + system CPU time
	ReadBytes  int64         // bytes read from block devices
	WriteBytes int64         // bytes written to block devices
}

// Sub is u minus an earlier sample.
func (u Usage) Sub(earlier Usage) Usage {
	return Usage{CPU: u.CPU - earlier.CPU, ReadBytes: u.ReadBytes - earlier.ReadBytes, WriteBytes: u.WriteBytes - earlier.WriteBytes}
}

// Probe samples a measured server's resource counters, so a sequential
// replay can charge each request with the CPU and disk work the server did
// while answering it.
type Probe interface {
	Sample() (Usage, error)
}

// CgroupProbe reads a cgroup v2 directory: cpu.stat's usage_usec (every
// process in the group, microsecond resolution) and io.stat's rbytes and
// wbytes summed over devices. Block IO counts only what missed the page
// cache; writes are charged when the kernel writes them back, which may be
// after the request that dirtied them.
type CgroupProbe struct{ Dir string }

func (p CgroupProbe) Sample() (Usage, error) {
	var u Usage
	cpu, err := os.ReadFile(filepath.Join(p.Dir, "cpu.stat"))
	if err != nil {
		return u, err
	}
	for line := range strings.SplitSeq(string(cpu), "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return u, fmt.Errorf("cpu.stat: %w", err)
			}
			u.CPU = time.Duration(n) * time.Microsecond
		}
	}
	io, err := os.ReadFile(filepath.Join(p.Dir, "io.stat"))
	if err != nil {
		// The io controller is not always enabled; CPU alone still counts.
		return u, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(io))
	for sc.Scan() {
		for _, f := range strings.Fields(sc.Text())[1:] {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			n, _ := strconv.ParseInt(v, 10, 64)
			switch k {
			case "rbytes":
				u.ReadBytes += n
			case "wbytes":
				u.WriteBytes += n
			}
		}
	}
	return u, nil
}

// CgroupMemory reads a cgroup's memory.current and memory.peak (0 when
// unavailable).
func CgroupMemory(dir string) (current, peak int64) {
	read := func(name string) int64 {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return 0
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		return n
	}
	return read("memory.current"), read("memory.peak")
}

// DockerCgroup is the cgroup v2 directory of a running container under the
// systemd cgroup driver, or under the cgroupfs driver as a fallback.
func DockerCgroup(containerID string) (string, error) {
	for _, dir := range []string{
		"/sys/fs/cgroup/system.slice/docker-" + containerID + ".scope",
		"/sys/fs/cgroup/docker/" + containerID,
	} {
		if _, err := os.Stat(filepath.Join(dir, "cpu.stat")); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no cgroup for container %.12s", containerID)
}
