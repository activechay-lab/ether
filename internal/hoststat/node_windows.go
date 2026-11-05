//go:build windows

package hoststat

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

func (s *nativeNodeSampler) platformSample() NodeReading {
	at := s.now()
	var out NodeReading
	var idle, kernel, user windows.Filetime
	var create, exit, kproc, uproc windows.Filetime
	if getSystemTimes(&idle, &kernel, &user) == nil && windows.GetProcessTimes(windows.CurrentProcess(), &create, &exit, &kproc, &uproc) == nil {
		total, idleN, proc := filetimeUint(kernel)+filetimeUint(user), filetimeUint(idle), filetimeUint(kproc)+filetimeUint(uproc)
		prev, ready := s.cpu.last, s.cpu.have
		s.cpu.last = cpuSnap{total: total, idle: idleN, proc: proc}
		s.cpu.have = true
		if ready && total > prev.total && idleN >= prev.idle && idleN-prev.idle <= total-prev.total && proc >= prev.proc {
			dt := float64(total - prev.total)
			out.HostCPUPercent = observed(clampPct((1-float64(idleN-prev.idle)/dt)*100), at)
			out.ProcessCPUPercent = observed(clampPct(float64(proc-prev.proc)/dt*100), at)
		}
	}
	var mem memoryStatusEx
	mem.Length = uint32(unsafe.Sizeof(mem))
	if ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&mem))); ok != 0 && mem.TotalPhys > 0 && mem.AvailPhys <= mem.TotalPhys {
		out.HostMemoryTotalBytes = observed(mem.TotalPhys, at)
		out.HostMemoryAvailableBytes = observed(mem.AvailPhys, at)
	}
	var pmc processMemoryCounters
	pmc.CB = uint32(unsafe.Sizeof(pmc))
	if ok, _, _ := procGetProcessMemoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.CB)); ok != 0 {
		out.ProcessRSSBytes = observed(uint64(pmc.WorkingSetSize), at)
	}
	return out
}
