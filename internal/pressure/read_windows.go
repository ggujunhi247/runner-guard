package pressure

import (
	"fmt"
	"syscall"
	"unsafe"
)

var procGlobalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

type memoryStatusEx struct {
	Length, MemoryLoad                        uint32
	TotalPhys, AvailPhys, TotalPageFile       uint64
	AvailPageFile, TotalVirtual, AvailVirtual uint64
	AvailExtendedVirtual                      uint64
}

// Windows has no load average; LoadPerCPU is -1.
func Read() (Sample, error) {
	m := memoryStatusEx{}
	m.Length = uint32(unsafe.Sizeof(m))
	if ok, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); ok == 0 {
		return Sample{}, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	return Sample{MemPct: float64(m.MemoryLoad), LoadPerCPU: -1, TotalMB: float64(m.TotalPhys) / (1 << 20)}, nil
}
