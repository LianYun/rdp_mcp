// Package netcheck diagnoses connection failures that have a known local cause.
//
// macOS 15+ gates connections to the local network (directly attached subnets,
// link-local addresses) behind the "Local Network" privacy permission of the
// responsible app. When remoter-mcp is started by Remoter.app the permission
// belongs to Remoter; without it every TCP connect to a same-subnet host fails
// immediately, which FreeRDP reports as a generic ERRCONNECT_CONNECT_FAILED.
package netcheck

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// HintLocalNetwork: the failure is probably caused by the macOS Local Network permission.
const HintLocalNetwork = "local_network"

// Hooks, replaced in tests.
var (
	applies        = localNetworkPrivacy // OS has a Local Network permission
	interfaceAddrs = localSubnets
	lookupIP       = func(ctx context.Context, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, "ip", host)
	}
	dial = func(ctx context.Context, addr string) error {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			c.Close()
		}
		return err
	}
)

// reachErrors are substrings of errors meaning "the TCP connection could not be
// established" (as opposed to refused, TLS or authentication failures). Timeouts
// are left out: a blocked connection fails immediately.
var reachErrors = []string{
	"ERRCONNECT_CONNECT_FAILED",
	"no route to host",
	"host is down",
	"network is unreachable",
	"operation not permitted",
}

// Diagnose returns a hint code for a failed connection to host:port, or "".
// It only does work when errMsg looks like a TCP-level failure, the host is on a
// directly attached subnet and the OS gates local network access; it then probes
// the port from this process (which shares the worker's permission) to rule out
// a server that is simply refusing or reachable.
func Diagnose(host string, port int, errMsg string) string {
	if !applies() || !isReachError(errMsg) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	ips, err := lookupIP(ctx, host)
	cancel()
	if err != nil || !anyLocal(ips, interfaceAddrs()) {
		return ""
	}
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = dial(ctx, net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil || errors.Is(err, syscall.ECONNREFUSED) {
		return "" // the network path works; the failure is something else
	}
	// A blocked connection fails at once; a timeout means the host is offline (no ARP reply).
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ""
	}
	return HintLocalNetwork
}

// Explain is the English sentence appended to the error message (seen by MCP clients and logs).
func Explain(hint, host string) string {
	if hint != HintLocalNetwork {
		return ""
	}
	return host + " is on this Mac's local network and macOS may be blocking Local Network access: " +
		"allow the app that started remoter-mcp (Remoter, or your terminal) in " +
		"System Settings → Privacy & Security → Local Network, then try again"
}

func isReachError(msg string) bool {
	for _, s := range reachErrors {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func anyLocal(ips []net.IP, subnets []*net.IPNet) bool {
	for _, ip := range ips {
		if ip.IsLoopback() {
			continue
		}
		if ip.IsLinkLocalUnicast() {
			return true
		}
		for _, n := range subnets {
			if n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// localSubnets lists the subnets of up, broadcast-capable interfaces (Wi-Fi,
// Ethernet); loopback and point-to-point tunnels (VPN utun) are not "local network".
func localSubnets() []*net.IPNet {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				out = append(out, n)
			}
		}
	}
	return out
}
