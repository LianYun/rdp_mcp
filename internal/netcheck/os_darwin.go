package netcheck

import (
	"strconv"
	"strings"
	"syscall"
)

// localNetworkPrivacy reports whether this macOS has the Local Network permission (15 Sequoia+).
func localNetworkPrivacy() bool {
	v, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return true // unknown: assume a current system
	}
	major, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return err != nil || major >= 15
}
