package hoststat

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type nativeNodeSampler struct {
	now    func() time.Time
	cgroup string
	cpu    cpuState
}

func newNativeNodeSampler(now func() time.Time, cgroup string) NodeSampler {
	return &nativeNodeSampler{now: now, cgroup: cgroup}
}
func (s *nativeNodeSampler) Sample(ctx context.Context) (NodeReading, error) {
	if err := ctx.Err(); err != nil {
		return NodeReading{}, err
	}
	out := s.platformSample()
	return out, ctx.Err()
}

// readCgroupV2 reads only three bounded files in an explicitly selected directory.
// It deliberately does not discover mounts, walk ancestors, or claim effective
// capacity. "max" is distinct from failed reads; current is usage, not RSS.
func readCgroupV2(dir string, at time.Time, out *NodeReading) {
	if dir == "" {
		return
	}
	read := func(name string) string {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return ""
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil || len(b) > 4096 {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	if v := read("memory.max"); v == "max" {
		out.CgroupMemoryLimitBytes = Metric[uint64]{At: at, State: Unlimited}
	} else if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
		out.CgroupMemoryLimitBytes = observed(n, at)
	}
	if n, err := strconv.ParseUint(read("memory.current"), 10, 64); err == nil {
		out.CgroupMemoryCurrentBytes = observed(n, at)
	}
	fields := strings.Fields(read("cpu.max"))
	if len(fields) != 2 {
		return
	}
	period, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || period == 0 {
		return
	}
	if fields[0] == "max" {
		out.CgroupCPUQuotaCores = Metric[float64]{At: at, State: Unlimited}
		return
	}
	quota, err := strconv.ParseUint(fields[0], 10, 64)
	if err == nil && quota > 0 {
		out.CgroupCPUQuotaCores = observed(float64(quota)/float64(period), at)
	}
}
