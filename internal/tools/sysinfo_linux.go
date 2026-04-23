//go:build linux

package tools

import "syscall"

func uptimeSec() int64 {
	var si syscall.Sysinfo_t
	if err := syscall.Sysinfo(&si); err != nil {
		return 0
	}
	return si.Uptime
}

func totalMemMB() uint64 {
	var si syscall.Sysinfo_t
	if err := syscall.Sysinfo(&si); err != nil {
		return 0
	}
	return (si.Totalram * uint64(si.Unit)) / (1024 * 1024)
}
