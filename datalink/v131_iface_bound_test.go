package datalink

import (
	"net"
	"testing"
)

// loIPv4Net returns the IPv4 network of the loopback interface. The tests are
// skipped on hosts without a usable "lo" instead of silently testing something
// else.
func loIPv4Net(t *testing.T) *net.IPNet {
	t.Helper()
	iface, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skipf("loopback interface not available: %v", err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		t.Fatalf("lo.Addrs(): %v", err)
	}
	for _, addr := range addrs {
		ip, ipNet, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		if ip.To4() != nil {
			return ipNet
		}
	}
	t.Skip("loopback interface has no IPv4 address")
	return nil
}

// subnetBroadcast computes ip | ^mask, the subnet-directed broadcast address.
func subnetBroadcast(ipNet *net.IPNet) net.IP {
	ip := ipNet.IP.To4()
	b := make(net.IP, net.IPv4len)
	for i := 0; i < net.IPv4len; i++ {
		b[i] = ip[i] | ^ipNet.Mask[i]
	}
	return b
}

// TestNewUDPDataLinkFromInterfaceBound_Lo covers acceptance item 3: binding
// "lo" narrows the socket to 127.0.0.1, broadcasts on the listening port, and
// targets the loopback subnet-directed broadcast address.
func TestNewUDPDataLinkFromInterfaceBound_Lo(t *testing.T) {
	ipNet := loIPv4Net(t)
	const port = 47810

	link, err := NewUDPDataLinkFromInterfaceBound("lo", port)
	if err != nil {
		t.Fatalf("NewUDPDataLinkFromInterfaceBound: %v", err)
	}
	defer link.Close()

	local := link.(*udpDataLink).listener.LocalAddr().(*net.UDPAddr)
	if !local.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("bound socket IP = %s, want 127.0.0.1", local.IP)
	}
	if local.Port != port {
		t.Errorf("bound socket port = %d, want %d", local.Port, port)
	}

	my := myUDPAddr(t, link)
	if !my.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("GetMyAddress IP = %s, want 127.0.0.1", my.IP)
	}
	if my.Port != port {
		t.Errorf("GetMyAddress port = %d, want %d", my.Port, port)
	}

	bcast := broadcastUDPAddr(t, link)
	if bcast.Port != port {
		t.Errorf("broadcast port = %d, want listening port %d", bcast.Port, port)
	}
	want := subnetBroadcast(ipNet)
	if !bcast.IP.Equal(want) {
		t.Errorf("broadcast IP = %s, want subnet-directed %s (lo %s)", bcast.IP, want, ipNet)
	}
	if bcast.IP.Equal(net.IPv4bcast) {
		t.Errorf("broadcast IP = %s, must not be the limited global broadcast", bcast.IP)
	}
	if got := link.GetBroadcastAddress().MacLen; got != 0 {
		t.Errorf("broadcast MacLen = %d, want 0", got)
	}
}

// TestNewUDPDataLinkFromInterfaceBound_InputForms verifies that both an
// interface name and an ip/prefix string select the same bind and broadcast
// semantics.
func TestNewUDPDataLinkFromInterfaceBound_InputForms(t *testing.T) {
	ipNet := loIPv4Net(t)

	for _, tc := range []struct {
		name  string
		inter string
	}{
		{name: "interface-name", inter: "lo"},
		{name: "ip-prefix", inter: "127.0.0.1/8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := freeUDPPort(t)
			link, err := NewUDPDataLinkFromInterfaceBound(tc.inter, port)
			if err != nil {
				t.Fatalf("NewUDPDataLinkFromInterfaceBound(%q): %v", tc.inter, err)
			}
			defer link.Close()

			local := link.(*udpDataLink).listener.LocalAddr().(*net.UDPAddr)
			if !local.IP.Equal(net.IPv4(127, 0, 0, 1)) || local.Port != port {
				t.Errorf("bound socket = %s, want 127.0.0.1:%d", local, port)
			}
			bcast := broadcastUDPAddr(t, link)
			if bcast.Port != port {
				t.Errorf("broadcast port = %d, want %d", bcast.Port, port)
			}
			if want := subnetBroadcast(ipNet); !bcast.IP.Equal(want) {
				t.Errorf("broadcast IP = %s, want %s", bcast.IP, want)
			}
		})
	}
}

// TestNewUDPDataLinkFromInterfaceBound_Errors covers acceptance item 4:
// resolution failures are returned, never panic and never fall back to a
// wildcard bind.
func TestNewUDPDataLinkFromInterfaceBound_Errors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		inter string
	}{
		{name: "unknown-interface", inter: "no-such-if0"},
		{name: "bad-cidr", inter: "not-an-ip/24"},
		{name: "ipv6-only", inter: "::1/128"},
		{name: "unspecified", inter: "0.0.0.0/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link, err := NewUDPDataLinkFromInterfaceBound(tc.inter, 47811)
			if err == nil {
				if link != nil {
					link.Close()
				}
				t.Fatalf("NewUDPDataLinkFromInterfaceBound(%q) = nil error, want error", tc.inter)
			}
			if link != nil {
				t.Errorf("link = %v, want nil on error", link)
			}
		})
	}
}

// TestNewUDPDataLink_LegacyBehaviorUnchanged covers acceptance item 5: the
// client-facing constructor must keep the wildcard bind and the legacy 47808
// broadcast port.
func TestNewUDPDataLink_LegacyBehaviorUnchanged(t *testing.T) {
	ipNet := loIPv4Net(t)
	port := freeUDPPort(t)

	link, err := NewUDPDataLink("lo", port)
	if err != nil {
		t.Fatalf("NewUDPDataLink: %v", err)
	}
	defer link.Close()

	local := link.(*udpDataLink).listener.LocalAddr().(*net.UDPAddr)
	if !local.IP.IsUnspecified() {
		t.Errorf("legacy bind IP = %s, want wildcard 0.0.0.0", local.IP)
	}
	if local.Port != port {
		t.Errorf("legacy bind port = %d, want %d", local.Port, port)
	}

	bcast := broadcastUDPAddr(t, link)
	if bcast.Port != DefaultPort {
		t.Errorf("legacy broadcast port = %d, want %d", bcast.Port, DefaultPort)
	}
	if want := subnetBroadcast(ipNet); !bcast.IP.Equal(want) {
		t.Errorf("legacy broadcast IP = %s, want %s", bcast.IP, want)
	}
}
