//go:build linux

package hoststat

func (s *nativeNodeSampler) platformSample() NodeReading {
	at := s.now()
	var out NodeReading
	total, idle, hostOK := readHostCPU("/proc/stat")
	proc, procOK := readProcCPU("/proc/self/stat")
	prev, ready := s.cpu.last, s.cpu.have
	s.cpu.last = cpuSnap{total: total, idle: idle, proc: proc}
	s.cpu.have = hostOK && procOK
	if ready && hostOK && total > prev.total && idle >= prev.idle && idle-prev.idle <= total-prev.total {
		dt := float64(total - prev.total)
		out.HostCPUPercent = observed(clampPct((1-float64(idle-prev.idle)/dt)*100), at)
		if procOK && proc >= prev.proc {
			out.ProcessCPUPercent = observed(clampPct(float64(proc-prev.proc)/dt*100), at)
		}
	}
	if total, available, ok := meminfoBytes("/proc/meminfo"); ok && total > 0 && available <= total {
		out.HostMemoryTotalBytes = observed(total, at)
		out.HostMemoryAvailableBytes = observed(available, at)
	}
	if rss, ok := statusRSS("/proc/self/status"); ok {
		out.ProcessRSSBytes = observed(rss, at)
	}
	readCgroupV2(s.cgroup, at, &out)
	return out
}
