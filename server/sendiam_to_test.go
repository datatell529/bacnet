package server

import (
	"bytes"
	"testing"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/datalink"
	"github.com/anviod/bacnet/encoding"
)

// iamCaptureDataLink is a minimal fake datalink that records every Send()
// call (payload and destination). It never touches a socket, so the tests
// below neither bind a fixed port nor depend on timing.
//
// 中文说明：iamCaptureDataLink 是最小假 datalink，记录每次 Send 的报文与目的
// 地址；不碰 socket，因此用例不占端口、不依赖时序。
type iamCaptureDataLink struct {
	myAddr        *btypes.Address
	broadcastAddr *btypes.Address
	sentData      [][]byte
	sentDests     []*btypes.Address
}

func newIAMCaptureDataLink() *iamCaptureDataLink {
	return &iamCaptureDataLink{
		myAddr: &btypes.Address{
			Net:    0,
			MacLen: 6,
			Mac:    []byte{127, 0, 0, 1, 0xBA, 0xC0},
			Adr:    []byte{127, 0, 0, 1},
			Len:    4,
		},
		broadcastAddr: &btypes.Address{
			Net:    0,
			MacLen: 6,
			Mac:    []byte{255, 255, 255, 255, 0xBA, 0xC0},
			Adr:    []byte{255, 255, 255, 255},
			Len:    4,
		},
	}
}

func (d *iamCaptureDataLink) GetMyAddress() *btypes.Address {
	return d.myAddr
}

func (d *iamCaptureDataLink) GetBroadcastAddress() *btypes.Address {
	return d.broadcastAddr
}

func (d *iamCaptureDataLink) Send(data []byte, npdu *btypes.NPDU, dest *btypes.Address) (int, error) {
	d.sentData = append(d.sentData, append([]byte(nil), data...))
	if dest == nil {
		d.sentDests = append(d.sentDests, nil)
	} else {
		destCopy := *dest
		d.sentDests = append(d.sentDests, &destCopy)
	}
	return len(data), nil
}

func (d *iamCaptureDataLink) Receive(data []byte) (*btypes.Address, int, error) {
	return nil, 0, nil
}

func (d *iamCaptureDataLink) Close() error {
	return nil
}

func newIAmTestServer(t *testing.T, dl datalink.DataLink) *server {
	t.Helper()
	cfg := DefaultDeviceConfig()
	cfg.DeviceID = 1234
	cfg.MaxPDU = 480
	cfg.VendorID = 321
	srv, err := NewServerWithDataLink(cfg, dl)
	if err != nil {
		t.Fatalf("NewServerWithDataLink: %v", err)
	}
	return srv.(*server)
}

// TestSendIAmToMatchesSendIAm verifies that SendIAm is an exact byte-for-byte
// alias of SendIAmTo(GetBroadcastAddress()).
//
// 中文说明：验证 SendIAm 与 SendIAmTo(GetBroadcastAddress()) 发出的字节逐字相同。
func TestSendIAmToMatchesSendIAm(t *testing.T) {
	dl := newIAMCaptureDataLink()
	s := newIAmTestServer(t, dl)

	if err := s.SendIAm(); err != nil {
		t.Fatalf("SendIAm: %v", err)
	}
	if len(dl.sentData) != 1 {
		t.Fatalf("SendIAm sent %d packets, want 1", len(dl.sentData))
	}
	want := append([]byte(nil), dl.sentData[0]...)
	wantDest := *dl.sentDests[0]

	if err := s.SendIAmTo(dl.GetBroadcastAddress()); err != nil {
		t.Fatalf("SendIAmTo: %v", err)
	}
	if len(dl.sentData) != 2 {
		t.Fatalf("SendIAmTo sent %d total packets, want 2", len(dl.sentData))
	}
	if !bytes.Equal(want, dl.sentData[1]) {
		t.Fatalf("SendIAmTo(broadcast) bytes differ from SendIAm\n got: %x\nwant: %x",
			dl.sentData[1], want)
	}
	assertSameAddress(t, dl.sentDests[1], &wantDest)
}

// TestSendIAmToTargetsDestination verifies that SendIAmTo actually passes the
// caller-supplied destination to the datalink and still emits a well-formed
// I-Am built from the server configuration.
//
// 中文说明：验证 SendIAmTo(addrX) 确实把报文发往 addrX，且报文仍是按服务端配置
// 构造的合法 I-Am。
func TestSendIAmToTargetsDestination(t *testing.T) {
	dl := newIAMCaptureDataLink()
	s := newIAmTestServer(t, dl)

	dest := &btypes.Address{
		Net:    0,
		Len:    4,
		MacLen: 6,
		Mac:    []byte{192, 0, 2, 10, 0xBA, 0xC0},
		Adr:    []byte{192, 0, 2, 10},
	}
	if err := s.SendIAmTo(dest); err != nil {
		t.Fatalf("SendIAmTo: %v", err)
	}
	if len(dl.sentData) != 1 {
		t.Fatalf("SendIAmTo sent %d packets, want 1", len(dl.sentData))
	}
	assertSameAddress(t, dl.sentDests[0], dest)

	iam := decodeIAmPacket(t, dl.sentData[0])
	if iam.ID.Type != btypes.DeviceType || iam.ID.Instance != s.config.DeviceID {
		t.Fatalf("I-Am object ID = %+v, want device %d", iam.ID, s.config.DeviceID)
	}
	if iam.MaxApdu != uint32(s.config.MaxPDU) {
		t.Fatalf("I-Am MaxApdu = %d, want %d", iam.MaxApdu, s.config.MaxPDU)
	}
	if iam.Segmentation != btypes.Enumerated(s.config.Segmentation) {
		t.Fatalf("I-Am Segmentation = %d, want %d", iam.Segmentation, s.config.Segmentation)
	}
	if iam.Vendor != s.store.GetVendorID() {
		t.Fatalf("I-Am Vendor = %d, want %d", iam.Vendor, s.store.GetVendorID())
	}
}

func assertSameAddress(t *testing.T, got, want *btypes.Address) {
	t.Helper()
	if got == nil {
		t.Fatal("no destination captured")
	}
	if got.Net != want.Net || got.Len != want.Len || got.MacLen != want.MacLen ||
		!bytes.Equal(got.Mac, want.Mac) || !bytes.Equal(got.Adr, want.Adr) {
		t.Fatalf("destination = %+v, want %+v", got, want)
	}
}

// decodeIAmPacket strips BVLC+NPDU+APDU framing from a captured packet and
// decodes the I-Am body.
func decodeIAmPacket(t *testing.T, pkt []byte) btypes.IAm {
	t.Helper()
	if len(pkt) < 4 {
		t.Fatalf("packet too short: %x", pkt)
	}
	dec := encoding.NewDecoder(pkt)
	var bvlc btypes.BVLC
	if err := dec.BVLC(&bvlc); err != nil {
		t.Fatalf("BVLC decode: %v (pkt=%x)", err, pkt)
	}
	ndec := encoding.NewDecoder(pkt[4:])
	var npdu btypes.NPDU
	if _, err := ndec.NPDU(&npdu); err != nil {
		t.Fatalf("NPDU decode: %v", err)
	}
	var apdu btypes.APDU
	if err := ndec.APDU(&apdu); err != nil {
		t.Fatalf("APDU decode: %v", err)
	}
	if apdu.DataType != btypes.UnconfirmedServiceRequest ||
		apdu.UnconfirmedService != btypes.ServiceUnconfirmedIAm {
		t.Fatalf("unexpected APDU type=%d service=%d, want I-Am", apdu.DataType, apdu.UnconfirmedService)
	}
	idec := encoding.NewDecoder(apdu.RawData)
	var iam btypes.IAm
	if err := idec.IAm(&iam); err != nil {
		t.Fatalf("I-Am decode: %v", err)
	}
	return iam
}
