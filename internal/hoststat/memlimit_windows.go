//go:build windows

package hoststat

import "unsafe"

func physicalMemory() uint64 {
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))
	r1, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st)))
	if r1 == 0 {
		return 0
	}
	return st.TotalPhys
}
