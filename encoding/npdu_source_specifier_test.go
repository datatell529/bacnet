package encoding

import (
	"encoding/hex"
	"testing"

	"github.com/anviod/bacnet/btypes"
)

// localIPv4Address builds the address shape the data link reports for this
// station on a BACnet/IP socket: Net == 0 (local network) with a 6 byte
// MAC/IP+port (see datalink.UDPToAddress).
func localIPv4Address() *btypes.Address {
	return &btypes.Address{
		Net:    0,
		MacLen: 6,
		Mac:    []byte{172, 17, 222, 204, 0xBB, 0x84}, // 172.17.222.204:47812
		Adr:    []byte{172, 17, 222, 204, 0xBB, 0x84},
		Len:    6,
	}
}

// TestNPDU_DirectIPv4UnicastOmitsSourceSpecifier pins the *unicast reply*
// network layer shape required by ASHRAE 135 Annex J (BACnet/IP):
//
//	A directly addressed IPv4 unicast must NOT carry an NPDU source specifier.
//	The source of such a message is the BVLC source address. Encoding
//	control=0x08 (source specifier) with SNET=0x0000 + SADR=<local station>
//	is a self-contradiction ("I am a remote station on network 0") which makes
//	bacpypes3 key the reply to a network-0 station (addrType=4) instead of the
//	IPv4 peer the request went to (addrType=2); the transaction never matches,
//	so every confirmed ACK is silently dropped and reads/writes time out.
//
// Before the fix this test fails with:
//
//	control = 0x08 (maskSource set), SNET = 0x0000, SADR = ac11deccbb84
//	hex = 0108000006ac11deccbb84
func TestNPDU_DirectIPv4UnicastOmitsSourceSpecifier(t *testing.T) {
	n := btypes.NPDU{
		Version: btypes.ProtocolVersion,
		Source:  localIPv4Address(),
	}

	e := NewEncoder()
	e.NPDU(&n)
	if err := e.Error(); err != nil {
		t.Fatal(err)
	}
	raw := e.Bytes()
	t.Logf("unicast reply NPDU hex = %s", hex.EncodeToString(raw))

	// 1) control byte must not advertise a source specifier.
	if len(raw) < 2 {
		t.Fatalf("encoded NPDU too short: %d bytes", len(raw))
	}
	control := NPDUMetadata(raw[1])
	if control.HasSource() {
		t.Errorf("control byte = 0x%02X: source specifier (maskSource 0x%02X) must be clear for a direct IPv4 unicast",
			byte(control), maskSource)
	}

	// 2) no SNET/SADR may follow the (absent) destination specifier.
	if len(raw) != 2 {
		t.Errorf("expected bare NPDU header (2 bytes), got %d bytes: %s — SNET=0x0000/SADR self-exposed",
			len(raw), hex.EncodeToString(raw))
	}

	// 3) a standard decoder must not invent a source address either.
	var out btypes.NPDU
	if _, err := NewDecoder(raw).NPDU(&out); err != nil {
		t.Fatal(err)
	}
	if out.Source != nil {
		t.Errorf("decoded Source = %+v, expected nil for a direct IPv4 unicast", out.Source)
	}
}

// TestNPDU_RoutedSourceSpecifierIsPreserved is the guard rail for the fix: a
// genuinely routed message (source on a non-zero BACnet network) MUST still
// carry its source specifier, otherwise routers could not deliver the reply.
func TestNPDU_RoutedSourceSpecifierIsPreserved(t *testing.T) {
	n := btypes.NPDU{
		Version: btypes.ProtocolVersion,
		Source:  &btypes.Address{Net: 200, Adr: []byte{0x03, 0x04}},
	}

	e := NewEncoder()
	e.NPDU(&n)
	if err := e.Error(); err != nil {
		t.Fatal(err)
	}
	raw := e.Bytes()
	t.Logf("routed NPDU hex = %s", hex.EncodeToString(raw))

	control := NPDUMetadata(raw[1])
	if !control.HasSource() {
		t.Fatalf("control byte = 0x%02X: source specifier must stay set for a routed (Net != 0) source", byte(control))
	}

	var out btypes.NPDU
	if _, err := NewDecoder(raw).NPDU(&out); err != nil {
		t.Fatal(err)
	}
	if out.Source == nil || out.Source.Net != 200 {
		t.Fatalf("decoded Source = %+v, expected Net 200", out.Source)
	}
}
