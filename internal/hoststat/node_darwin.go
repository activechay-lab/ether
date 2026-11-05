//go:build darwin

package hoststat

import (
	"golang.org/x/sys/unix"
	"syscall"
)

func (s *nativeNodeSampler) platformSample() NodeReading {
	at := s.now()
	var out NodeReading
	// Reuse the dashboard's process accounting, without presenting load average
	// as measured CPU utilization or peak RSS as current memory usage.
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		proc := uint64(ru.Utime.Nano()) + uint64(ru.Stime.Nano())
		prev, ready := s.cpu.last, s.cpu.have
		s.cpu.last = cpuSnap{proc: proc, wall: at}
		s.cpu.have = true
		if n, err := unix.SysctlUint32("hw.ncpu"); err == nil && n > 0 && ready && at.After(prev.wall) && proc >= prev.proc {
			out.ProcessCPUPercent = observed(clampPct(float64(proc-prev.proc)/1e9/at.Sub(prev.wall).Seconds()/float64(n)*100), at)
		}
	}
	if total, err := unix.SysctlUint64("hw.memsize"); err == nil && total > 0 {
		out.HostMemoryTotalBytes = observed(total, at)
	}
	// Current RSS/available RAM/CPU utilization require native APIs not used here.
	return out
}
