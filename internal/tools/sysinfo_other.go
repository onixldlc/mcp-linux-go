//go:build !linux

package tools

func uptimeSec() int64 { return 0 }
func totalMemMB() uint64 { return 0 }
