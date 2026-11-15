package pressure

import (
	"os"
	"runtime"
)

func Read() (Sample, error) {
	mi, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Sample{}, err
	}
	mem, err := parseMeminfo(string(mi))
	if err != nil {
		return Sample{}, err
	}
	la, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return Sample{}, err
	}
	load, err := parseLoadavg(string(la), runtime.NumCPU())
	if err != nil {
		return Sample{}, err
	}
	return Sample{MemPct: mem, LoadPerCPU: load}, nil
}
