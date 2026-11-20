//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package proxy

import "syscall"

func bridgeProbeIsPipe(fd int) bool {
	var stat syscall.Stat_t
	return syscall.Fstat(fd, &stat) == nil && stat.Mode&syscall.S_IFMT == syscall.S_IFIFO
}
