package netcheck

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
)

func stub(t *testing.T, osApplies bool, dialErr error) {
	t.Helper()
	oa, oi, ol, od := applies, interfaceAddrs, lookupIP, dial
	t.Cleanup(func() { applies, interfaceAddrs, lookupIP, dial = oa, oi, ol, od })
	applies = func() bool { return osApplies }
	_, lan, _ := net.ParseCIDR("10.253.210.47/24")
	interfaceAddrs = func() []*net.IPNet { return []*net.IPNet{lan} }
	lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
		if ip := net.ParseIP(host); ip != nil {
			return []net.IP{ip}, nil
		}
		return nil, errors.New("no such host")
	}
	dial = func(context.Context, string) error { return dialErr }
}

// timeout is a dial error like the one for an offline LAN host (no ARP reply).
type timeout struct{}

func (timeout) Error() string   { return "i/o timeout" }
func (timeout) Timeout() bool   { return true }
func (timeout) Temporary() bool { return true }

func TestDiagnose(t *testing.T) {
	const failed = "connect failed: The connection failed. (ERRCONNECT_CONNECT_FAILED)"
	unreach := &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}
	refused := &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	cases := []struct {
		name    string
		applies bool
		host    string
		msg     string
		dialErr error
		want    string
	}{
		{"same subnet, blocked", true, "10.253.210.106", failed, unreach, HintLocalNetwork},
		{"vnc dial error", true, "10.253.210.106", "dial tcp 10.253.210.106:5900: connect: no route to host", unreach, HintLocalNetwork},
		{"link-local", true, "169.254.10.2", failed, unreach, HintLocalNetwork},
		{"routed host", true, "10.243.14.141", failed, unreach, ""},
		{"not macOS 15+", false, "10.253.210.106", failed, unreach, ""},
		{"auth failure", true, "10.253.210.106", "connect failed: Logon failed. (ERRCONNECT_LOGON_FAILURE)", unreach, ""},
		{"port refused", true, "10.253.210.106", failed, refused, ""},
		{"probe succeeds", true, "10.253.210.106", failed, nil, ""},
		{"host offline (probe times out)", true, "10.253.210.251", failed, timeout{}, ""},
		{"vnc dial timeout", true, "10.253.210.251", "dial tcp 10.253.210.251:5900: i/o timeout", timeout{}, ""},
		{"unresolvable", true, "nope.invalid", failed, unreach, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub(t, c.applies, c.dialErr)
			if got := Diagnose(c.host, 3389, c.msg); got != c.want {
				t.Fatalf("Diagnose = %q, want %q", got, c.want)
			}
		})
	}
}

func TestExplain(t *testing.T) {
	if Explain("", "h") != "" {
		t.Fatal("empty hint must explain nothing")
	}
	if s := Explain(HintLocalNetwork, "10.0.0.5"); s == "" {
		t.Fatal("missing explanation")
	}
}
