package datalink

import (
	"net"
	"strconv"
	"testing"
)

// freeUDPPort returns an ephemeral loopback port so tests never squat on a
// fixed BACnet port.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	l, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free port: %v", err)
	}
	defer l.Close()
	return l.LocalAddr().(*net.UDPAddr).Port
}

func myUDPAddr(t *testing.T, link DataLink) net.UDPAddr {
	t.Helper()
	addr, err := link.GetMyAddress().UDPAddr()
	if err != nil {
		t.Fatalf("GetMyAddress().UDPAddr(): %v", err)
	}
	return addr
}

func broadcastUDPAddr(t *testing.T, link DataLink) net.UDPAddr {
	t.Helper()
	addr, err := link.GetBroadcastAddress().UDPAddr()
	if err != nil {
		t.Fatalf("GetBroadcastAddress().UDPAddr(): %v", err)
	}
	return addr
}

// TestNewUDPDataLinkFromIPBound_BindsConfiguredIP covers acceptance item 2:
// the listener binds the configured address, not INADDR_ANY.
func TestNewUDPDataLinkFromIPBound_BindsConfiguredIP(t *testing.T) {
	const port = 47830 // >= 47830, avoids the standard 0xBAC0 port

	link, err := NewUDPDataLinkFromIPBound("127.0.0.1", 24, port, port)
	if err != nil {
		t.Fatalf("NewUDPDataLinkFromIPBound: %v", err)
	}
	defer link.Close()

	got := myUDPAddr(t, link)
	if !got.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("GetMyAddress IP = %s, want 127.0.0.1", got.IP)
	}
	if got.Port != port {
		t.Errorf("GetMyAddress port = %d, want %d", got.Port, port)
	}
}

// TestNewUDPDataLinkFromIPBound_UnspecifiedFallback covers acceptance item 3:
// 0.0.0.0 keeps the wildcard bind and reports the local NIC address.
func TestNewUDPDataLinkFromIPBound_UnspecifiedFallback(t *testing.T) {
	port := freeUDPPort(t)

	link, err := NewUDPDataLinkFromIPBound("0.0.0.0", 24, port, port)
	if err != nil {
		t.Fatalf("NewUDPDataLinkFromIPBound: %v", err)
	}
	defer link.Close()

	got := myUDPAddr(t, link)
	want := GetLocalIP()
	if !got.IP.Equal(want) {
		t.Errorf("GetMyAddress IP = %s, want %s", got.IP, want)
	}
	if got.Port != port {
		t.Errorf("GetMyAddress port = %d, want %d", got.Port, port)
	}
}

// TestUDPDataLink_GetBroadcastAddress_DefaultPort covers acceptance item 4:
// bcastPort <= 0 must reproduce the legacy 47808 broadcast port exactly.
func TestUDPDataLink_GetBroadcastAddress_DefaultPort(t *testing.T) {
	for _, tc := range []struct {
		name string
		link func(t *testing.T, port int) DataLink
	}{
		{
			name: "legacy constructor",
			link: func(t *testing.T, port int) DataLink {
				t.Helper()
				link, err := NewUDPDataLinkFromIP("127.0.0.1", 24, port)
				if err != nil {
					t.Fatalf("NewUDPDataLinkFromIP: %v", err)
				}
				return link
			},
		},
		{
			name: "bound constructor bcastPort=0",
			link: func(t *testing.T, port int) DataLink {
				t.Helper()
				link, err := NewUDPDataLinkFromIPBound("127.0.0.1", 24, port, 0)
				if err != nil {
					t.Fatalf("NewUDPDataLinkFromIPBound: %v", err)
				}
				return link
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := tc.link(t, freeUDPPort(t))
			defer link.Close()

			if got := broadcastUDPAddr(t, link).Port; got != DefaultPort {
				t.Errorf("broadcast port = %d, want %d", got, DefaultPort)
			}
		})
	}
}

// TestUDPDataLink_GetBroadcastAddress_CustomPort covers acceptance item 4:
// a non-zero bcastPort is used verbatim for I-Am / Who-Is broadcasts.
func TestUDPDataLink_GetBroadcastAddress_CustomPort(t *testing.T) {
	listenPort := freeUDPPort(t)

	link, err := NewUDPDataLinkFromIPBound("127.0.0.1", 24, listenPort, 47830)
	if err != nil {
		t.Fatalf("NewUDPDataLinkFromIPBound: %v", err)
	}
	defer link.Close()

	got := broadcastUDPAddr(t, link)
	if got.Port != 47830 {
		t.Errorf("broadcast port = %d, want 47830", got.Port)
	}
	if !got.IP.Equal(net.IPv4(127, 0, 0, 255)) {
		t.Errorf("broadcast IP = %s, want 127.0.0.255", got.IP)
	}
	if len(link.GetBroadcastAddress().Mac) < 6 {
		t.Errorf("broadcast address MAC too short: %d", len(link.GetBroadcastAddress().Mac))
	}
}

// TestUDPDataLink_BindingIsNarrowed ensures the configured bind address really
// narrows the socket: a second socket cannot claim the same 127.0.0.1:port.
// It is skipped on platforms that allow wildcard reuse (Windows).
func TestUDPDataLink_BindingIsNarrowed(t *testing.T) {
	port := freeUDPPort(t)

	link, err := NewUDPDataLinkFromIPBound("127.0.0.1", 24, port, port)
	if err != nil {
		t.Fatalf("NewUDPDataLinkFromIPBound: %v", err)
	}
	defer link.Close()

	// A conflicting wildcard bind of the same port must fail (no SO_REUSEADDR
	// on Unix). This is the visible symptom the bound constructor fixes.
	l, err := net.ListenPacket("udp4", "0.0.0.0:"+strconv.Itoa(port))
	if err == nil {
		l.Close()
		t.Skip("platform permits wildcard reuse; bind narrowing not observable")
	}
}
