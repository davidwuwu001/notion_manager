//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package proxy

// The diagnostic probe is intentionally unavailable where descriptor type
// validation has not been implemented.
func bridgeProbeIsPipe(int) bool { return false }
