package server

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/encoding"
)

// bacnetIPv4Address mirrors datalink.UDPToAddress: the shape of an address on a
// BACnet/IP socket — Net == 0 (local network), 6 byte Mac = IPv4 (4) + port (2).
func bacnetIPv4Address(ip [4]byte, port uint16) *btypes.Address {
	mac := []byte{ip[0], ip[1], ip[2], ip[3], byte(port >> 8), byte(port & 0xFF)}
	return &btypes.Address{
		Net:    0,
		MacLen: 6,
		Mac:    mac,
		Adr:    mac,
		Len:    6,
	}
}

// TestServer_UnicastReplyNPDU_NoSourceSpecifier captures the *wire* bytes of a
// confirmed ReadProperty reply and pins the NPDU shape seen by an external
// client. Evidence anchor: /tmp/bacnet-exp/cap8.txt showed the pre-fix reply as
//
//	... 01 08 0000 06ac11deccb2 30 00 ...   (control=0x08, SNET=0x0000, SADR=local)
//
// which made bacpypes3 resolve the responder as device addrType=4 (remote
// station on network 0) instead of addrType=2 (IPv4 peer) ⇒ confirmed ACKs
// silently dropped. After the fix the reply NPDU must be a bare 01 00 header.
func TestServer_UnicastReplyNPDU_NoSourceSpecifier(t *testing.T) {
	srv, mockDL := newTestServer(t)
	// This station: 172.17.222.204:47812 (the isolated POC instance port).
	mockDL.myAddr = bacnetIPv4Address([4]byte{172, 17, 222, 204}, 47812)

	// Confirmed ReadProperty analog-input:1 present-value, from a real IPv4 peer.
	// The service body must be framed *after* a single APDU header: enc.ReadProperty
	// already emits header+body, so the header is stripped before handing the
	// remaining service payload to buildBACnetMessage (which prepends its own).
	enc := encoding.NewEncoder()
	err := enc.ReadProperty(1, btypes.PropertyData{
		Object: btypes.Object{
			ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			Properties: []btypes.Property{
				{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll},
			},
		},
	})
	if err != nil {
		t.Fatalf("ReadProperty encoding failed: %v", err)
	}
	apdu := btypes.APDU{
		DataType: btypes.ConfirmedServiceRequest,
		Service:  btypes.ServiceConfirmedReadProperty,
		InvokeId: 1,
		MaxSegs:  0,
		MaxApdu:  btypes.MaxAPDU,
	}
	hdr := buildAPDUHeader(t, apdu)
	body := enc.Bytes()
	if len(body) <= len(hdr) {
		t.Fatalf("ReadProperty APDU too short: %d bytes", len(body))
	}

	client := bacnetIPv4Address([4]byte{192, 168, 1, 100}, 47808)
	mockDL.injectReceive(client, buildBACnetMessage(t, hdr, body[len(hdr):], false))

	go func() { _ = srv.Serve() }()
	time.Sleep(100 * time.Millisecond)
	srv.Close()

	if mockDL.sentCount() == 0 {
		t.Fatal("no reply was sent for the confirmed ReadProperty request")
	}
	frame := mockDL.getLastSent()
	t.Logf("reply frame hex       = %s", hex.EncodeToString(frame))

	if len(frame) < 6 {
		t.Fatalf("reply frame too short: %d bytes", len(frame))
	}
	// frame = BVLC (4 bytes) + NPDU
	if frame[4] != byte(btypes.ProtocolVersion) {
		t.Errorf("NPDU version = 0x%02X, expected 0x%02X", frame[4], byte(btypes.ProtocolVersion))
	}
	control := frame[5]
	if control&0x08 != 0 {
		t.Errorf("reply NPDU control = 0x%02X: source specifier must not be set on a direct IPv4 unicast reply", control)
	}
	if len(frame) >= 8 && frame[5]&0x08 == 0 && control == 0x00 {
		// A source specifier would put SNET right after the control byte;
		// SNET=0x0000 is precisely the self-exposed shape from cap8.txt.
		if frame[6] == 0x00 && frame[7] == 0x00 {
			t.Errorf("reply NPDU still carries SNET=0x0000 at offset 6..7: %s", hex.EncodeToString(frame[:10]))
		}
	}

	// Decode the NPDU part exactly like a client would.
	var out btypes.NPDU
	if _, err := encoding.NewDecoder(frame[4:]).NPDU(&out); err != nil {
		t.Fatalf("reply NPDU decode failed: %v", err)
	}
	if out.Source != nil {
		t.Errorf("decoded reply Source = %+v, expected nil", out.Source)
	}
	// The payload after the NPDU must be a ComplexAck (0x30), i.e. a real
	// ReadPropertyAck — not an Error PDU (0x50) that would mask a bad frame.
	if frame[6]&0xF0 != byte(btypes.ComplexAck) {
		t.Errorf("reply APDU type = 0x%02X, expected ComplexAck (0x%02X)", frame[6]&0xF0, byte(btypes.ComplexAck))
	}
	t.Logf("reply NPDU hex        = %s", hex.EncodeToString(frame[4:10]))
}
