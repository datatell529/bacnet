package server

import (
	"testing"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/encoding"
)

// This file covers the network write paths (WriteProperty / WritePropertyMultiple):
// the COV notification they emit must be gated on an actual Present_Value change,
// exactly like server.SetProperty, and a single write must never produce two
// notifications.
//
// 中文说明：网络写路径（WriteProperty / WritePropertyMultiple）的 COV 通知同样
// 按值门控；同一写操作最多产生一条通知。通知次数由 mock datalink 解帧观测，不用
// time.Sleep。

// decodeSentAPDUs decodes every frame captured by the mock data link from the
// BVLC header down to the APDU, exactly the way a client would.
//
// 中文说明：把 mock datalink 捕获的每帧按 BVLC→NPDU→APDU 回解。
func decodeSentAPDUs(t *testing.T, dl *mockDataLink) []btypes.APDU {
	t.Helper()
	out := make([]btypes.APDU, 0, len(dl.sentData))
	for i, frame := range dl.sentData {
		var bvlc btypes.BVLC
		if err := encoding.NewDecoder(frame).BVLC(&bvlc); err != nil {
			t.Fatalf("frame %d: BVLC decode: %v", i, err)
		}
		if len(frame) <= mtuHeaderLength {
			t.Fatalf("frame %d: shorter than BVLC header: %d bytes", i, len(frame))
		}
		dec := encoding.NewDecoder(frame[mtuHeaderLength:])
		var npdu btypes.NPDU
		if _, err := dec.NPDU(&npdu); err != nil {
			t.Fatalf("frame %d: NPDU decode: %v", i, err)
		}
		var apdu btypes.APDU
		if err := dec.APDU(&apdu); err != nil {
			t.Fatalf("frame %d: APDU decode: %v", i, err)
		}
		out = append(out, apdu)
	}
	return out
}

// isCOVNotification reports whether apdu carries a COV notification, confirmed
// or unconfirmed.
//
// 中文说明：判断 APDU 是否为 COV 通知（确认或非确认）。
func isCOVNotification(apdu btypes.APDU) bool {
	switch apdu.DataType {
	case btypes.UnconfirmedServiceRequest:
		return apdu.UnconfirmedService == btypes.ServiceUnconfirmedCOVNotification
	case btypes.ConfirmedServiceRequest:
		return apdu.Service == btypes.ServiceConfirmedCOVNotification
	default:
		return false
	}
}

// covNotifications decodes the monitored object ID carried by every COV
// notification in the captured frames. Its length is the notification count.
//
// 中文说明：解出每条 COV 通知的受监控对象 ID；切片长度即通知条数。
func covNotifications(t *testing.T, dl *mockDataLink) []btypes.ObjectID {
	t.Helper()
	var ids []btypes.ObjectID
	for i, apdu := range decodeSentAPDUs(t, dl) {
		if !isCOVNotification(apdu) {
			continue
		}
		var notif btypes.COVNotification
		if err := encoding.NewDecoder(apdu.RawData).COVNotification(&notif); err != nil {
			t.Fatalf("frame %d: COVNotification decode: %v", i, err)
		}
		ids = append(ids, notif.MonitoredObjectID)
	}
	return ids
}

// countSimpleAcks counts SimpleAck PDUs for the given confirmed service.
//
// 中文说明：统计指定确认服务的 SimpleAck 数量。
func countSimpleAcks(t *testing.T, dl *mockDataLink, service btypes.ServiceConfirmed) int {
	t.Helper()
	n := 0
	for _, apdu := range decodeSentAPDUs(t, dl) {
		if apdu.DataType == btypes.SimpleAck && apdu.Service == service {
			n++
		}
	}
	return n
}

// newWritePathCOVTestServer builds a server with AnalogInput 1 whose stored PV
// is 23.5 and a permanent COV subscription on it. WriteHandler is nil so the
// legacy local-write path is exercised.
//
// 中文说明：构造带 AI:1（PV=23.5）与永久 COV 订阅的测试服务端，无写回调。
func newWritePathCOVTestServer(t *testing.T) (*server, *mockDataLink) {
	t.Helper()
	srv, dl := newTestServerWithHandler(t, nil)
	subscribeCOVForTest(srv, btypes.AnalogInput, 1)
	return srv, dl
}

// TestWriteProperty_COVNotificationValueGated covers the single-property write
// path:
//
//   - writing the value already stored => SimpleAck, 0 COV notifications;
//   - writing a different value        => SimpleAck, exactly 1 notification;
//   - repeating that same value        => SimpleAck, no extra notification.
//
// The write itself must still land in the store every time.
//
// 中文说明：WriteProperty 写同值 0 条通知、写变值恰好 1 条、再写同值不再增加；
// 每次写都照常落地并回 SimpleAck。
func TestWriteProperty_COVNotificationValueGated(t *testing.T) {
	srv, dl := newWritePathCOVTestServer(t)

	// 1) Same value as stored: ack but no notification.
	//
	// NOTE: a COV notification is sent AFTER the SimpleAck, so "last sent" is
	// not necessarily the ack. ACKs are therefore counted by decoding every
	// captured frame below instead of inspecting the last one.
	//
	// 中文说明：通知在 SimpleAck 之后发送，故用解帧计数确认 Ack，而非取最后一帧。
	sendWriteProperty(t, srv, dl, btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE, float64(23.5))
	if got := countSimpleAcks(t, dl, btypes.ServiceConfirmedWriteProperty); got != 1 {
		t.Fatalf("same-value write: SimpleAck count = %d, want 1", got)
	}
	if got := len(covNotifications(t, dl)); got != 0 {
		t.Fatalf("same-value write sent %d COV notifications, want 0", got)
	}
	if v, found := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE); !found {
		t.Fatal("same-value write: Present_Value missing from store")
	} else if f, ok := v.(float64); !ok || f != 23.5 {
		t.Fatalf("same-value write: store value = %#v, want 23.5", v)
	}

	// 2) Changed value: exactly one notification (never two).
	sendWriteProperty(t, srv, dl, btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE, float64(24.5))
	if got := countSimpleAcks(t, dl, btypes.ServiceConfirmedWriteProperty); got != 2 {
		t.Fatalf("changed-value write: SimpleAck count = %d, want 2", got)
	}
	notifs := covNotifications(t, dl)
	if len(notifs) != 1 {
		t.Fatalf("changed-value write sent %d COV notifications, want exactly 1", len(notifs))
	}
	wantID := btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1}
	if notifs[0] != wantID {
		t.Fatalf("notification monitored object = %+v, want %+v", notifs[0], wantID)
	}
	if v, _ := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE); v != float64(24.5) {
		t.Fatalf("changed-value write: store value = %#v, want 24.5", v)
	}

	// 3) Repeat the now-stored value: still no new notification.
	sendWriteProperty(t, srv, dl, btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE, float64(24.5))
	if got := countSimpleAcks(t, dl, btypes.ServiceConfirmedWriteProperty); got != 3 {
		t.Fatalf("repeat write: SimpleAck count = %d, want 3", got)
	}
	if got := len(covNotifications(t, dl)); got != 1 {
		t.Fatalf("repeat equal write: %d COV notifications total, want 1", got)
	}
	if got := countSimpleAcks(t, dl, btypes.ServiceConfirmedWriteProperty); got != 3 {
		t.Fatalf("SimpleAck count = %d, want 3 (one per write)", got)
	}
}

// TestWriteMultiProperty_COVNotificationValueGated covers the multi-property
// path. With a batch where one object keeps its value and another changes, only
// the changed object may notify, and each changed object notifies exactly once.
//
// 中文说明：批量写部分同值、部分变值 ⇒ 只有变值对象各 1 条；全同值 ⇒ 0 条。
func TestWriteMultiProperty_COVNotificationValueGated(t *testing.T) {
	srv, dl := newWritePathCOVTestServer(t)
	// Second point: AI:2 with stored PV 10.
	if err := srv.AddObject(btypes.Object{
		ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 2},
		Properties: []btypes.Property{
			{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(10)},
		},
	}); err != nil {
		t.Fatalf("AddObject(AI,2): %v", err)
	}
	subscribeCOVForTest(srv, btypes.AnalogInput, 2)

	ai1 := btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1}
	ai2 := btypes.ObjectID{Type: btypes.AnalogInput, Instance: 2}

	// 1) Batch: AI:1 unchanged (23.5), AI:2 changed (10 -> 12).
	resp := sendWriteMultipleProperty(t, srv, dl, []btypes.Object{
		{ID: ai1, Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(23.5)}}},
		{ID: ai2, Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(12)}}},
	})
	if resp.DataType != btypes.SimpleAck {
		t.Fatalf("partial batch: want SimpleAck, got %v (%+v)", resp.DataType, resp.Error)
	}
	notifs := covNotifications(t, dl)
	if len(notifs) != 1 {
		t.Fatalf("partial batch sent %d COV notifications, want exactly 1 (AI:2 only)", len(notifs))
	}
	if notifs[0] != ai2 {
		t.Fatalf("partial batch notified %+v, want %+v", notifs[0], ai2)
	}
	if v, _ := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE); v != float64(23.5) {
		t.Fatalf("AI:1 store value = %#v, want 23.5", v)
	}
	if v, _ := srv.GetProperty(btypes.AnalogInput, 2, btypes.PROP_PRESENT_VALUE); v != float64(12) {
		t.Fatalf("AI:2 store value = %#v, want 12", v)
	}

	// 2) Batch where both values are already stored: no notifications.
	resp = sendWriteMultipleProperty(t, srv, dl, []btypes.Object{
		{ID: ai1, Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(23.5)}}},
		{ID: ai2, Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(12)}}},
	})
	if resp.DataType != btypes.SimpleAck {
		t.Fatalf("all-equal batch: want SimpleAck, got %v (%+v)", resp.DataType, resp.Error)
	}
	if got := len(covNotifications(t, dl)); got != 1 {
		t.Fatalf("all-equal batch: %d COV notifications total, want still 1", got)
	}

	// 3) Batch where both change: exactly one notification per object.
	resp = sendWriteMultipleProperty(t, srv, dl, []btypes.Object{
		{ID: ai1, Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(24)}}},
		{ID: ai2, Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(13)}}},
	})
	if resp.DataType != btypes.SimpleAck {
		t.Fatalf("both-changed batch: want SimpleAck, got %v (%+v)", resp.DataType, resp.Error)
	}
	notifs = covNotifications(t, dl)
	if len(notifs) != 3 {
		t.Fatalf("both-changed batch: %d COV notifications total, want 3 (1 + 2)", len(notifs))
	}
	if notifs[1] != ai1 || notifs[2] != ai2 {
		t.Fatalf("both-changed batch notified %+v, want [%+v %+v]", notifs[1:], ai1, ai2)
	}
	if got := countSimpleAcks(t, dl, btypes.ServiceConfirmedWritePropMultiple); got != 3 {
		t.Fatalf("SimpleAck count = %d, want 3 (one per WPM request)", got)
	}
}
