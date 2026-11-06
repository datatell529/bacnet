package server

import (
	"net"
	"testing"
)

func v131FreeUDPPort(t *testing.T) int {
	t.Helper()
	l, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free port: %v", err)
	}
	defer l.Close()
	return l.LocalAddr().(*net.UDPAddr).Port
}

func v131LoIPv4Net(t *testing.T) *net.IPNet {
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

func v131SubnetBroadcast(ipNet *net.IPNet) net.IP {
	ip := ipNet.IP.To4()
	b := make(net.IP, net.IPv4len)
	for i := 0; i < net.IPv4len; i++ {
		b[i] = ip[i] | ^ipNet.Mask[i]
	}
	return b
}

// TestNewServer_InterfaceUsesBoundDataLink covers acceptance item 6: when only
// cfg.Interface is set, NewServer must use the interface-bound constructor
// (narrow IPv4 bind and broadcast port = listening port), not the legacy
// wildcard/47808 client constructor.
func TestNewServer_InterfaceUsesBoundDataLink(t *testing.T) {
	ipNet := v131LoIPv4Net(t)
	port := v131FreeUDPPort(t)

	cfg := DefaultDeviceConfig()
	cfg.DeviceID = 1234
	cfg.Interface = "lo"
	cfg.Port = port

	srvIface, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer(Interface=lo): %v", err)
	}
	defer srvIface.Close()

	srv, ok := srvIface.(*server)
	if !ok {
		t.Fatalf("NewServer returned %T, want *server", srvIface)
	}
	if srv.dataLink == nil {
		t.Fatal("server data link is nil")
	}

	my, err := srv.dataLink.GetMyAddress().UDPAddr()
	if err != nil {
		t.Fatalf("GetMyAddress().UDPAddr(): %v", err)
	}
	if !my.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("server bind IP = %s, want 127.0.0.1", my.IP)
	}
	if my.Port != port {
		t.Errorf("server bind port = %d, want %d", my.Port, port)
	}

	bcast, err := srv.dataLink.GetBroadcastAddress().UDPAddr()
	if err != nil {
		t.Fatalf("GetBroadcastAddress().UDPAddr(): %v", err)
	}
	if bcast.Port != port {
		t.Errorf("server broadcast port = %d, want listening port %d", bcast.Port, port)
	}
	if want := v131SubnetBroadcast(ipNet); !bcast.IP.Equal(want) {
		t.Errorf("server broadcast IP = %s, want %s", bcast.IP, want)
	}
	if bcast.IP.Equal(net.IPv4bcast) {
		t.Errorf("server broadcast IP = %s, must not be the limited global broadcast", bcast.IP)
	}
}

// TestNewServer_InterfaceErrorIsFatal verifies the Interface branch propagates
// the new constructor's fail-fast error instead of silently binding 0.0.0.0.
func TestNewServer_InterfaceErrorIsFatal(t *testing.T) {
	cfg := DefaultDeviceConfig()
	cfg.DeviceID = 1235
	cfg.Interface = "no-such-if0"
	cfg.Port = v131FreeUDPPort(t)

	srv, err := NewServer(cfg)
	if err == nil {
		if srv != nil {
			srv.Close()
		}
		t.Fatal("NewServer with unknown interface = nil error, want error")
	}
	if srv != nil {
		t.Errorf("NewServer returned server %v alongside error", srv)
	}
}
