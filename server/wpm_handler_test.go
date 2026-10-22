package server

import (
	"errors"
	"testing"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/btypes/bacerr"
	"github.com/anviod/bacnet/encoding"
)

// sendWriteMultipleProperty 构造并处理一条 WritePropertyMultiple 请求，
// 返回解析后的应答 APDU（SimpleAck / Error）。
func sendWriteMultipleProperty(t *testing.T, srv *server, mockDL *mockDataLink, objects []btypes.Object) *btypes.APDU {
	t.Helper()

	enc := encoding.NewEncoder()
	wmp := btypes.MultiplePropertyData{Objects: objects}
	if err := enc.WriteMultiProperty(1, wmp); err != nil {
		t.Fatalf("encode WritePropertyMultiple: %v", err)
	}
	full := enc.Bytes()

	// 拆出「APDU 头」与「服务负载」：handleWriteMultiProperty 解码的是负载部分。
	var probe btypes.APDU
	if err := encoding.NewDecoder(full).APDU(&probe); err != nil {
		t.Fatalf("APDU 拆分失败: %v", err)
	}
	hdr := full[:len(full)-len(probe.RawData)]
	msg := buildBACnetMessage(t, hdr, probe.RawData, false)
	src := &btypes.Address{Adr: []byte{192, 168, 1, 100}, Len: 4}

	srv.handleMsg(src, msg)

	sent := mockDL.getLastSent()
	if sent == nil {
		t.Fatal("no response sent")
	}
	dec := encoding.NewDecoder(sent)
	var bvlc btypes.BVLC
	if err := dec.BVLC(&bvlc); err != nil {
		t.Fatalf("BVLC decode: %v", err)
	}
	var npdu btypes.NPDU
	if _, err := dec.NPDU(&npdu); err != nil {
		t.Fatalf("NPDU decode: %v", err)
	}
	var out btypes.APDU
	if err := dec.APDU(&out); err != nil {
		t.Fatalf("APDU decode: %v", err)
	}
	return &out
}

// WPM 写 PresentValue：handler 必须先被调用 1 次，成功后才更新本地值并回 SimpleAck。
func TestWriteMultiProperty_PresentValueRoutedThroughHandler(t *testing.T) {
	h := &recordingWriteHandler{}
	srv, mockDL := newTestServerWithHandler(t, h)

	resp := sendWriteMultipleProperty(t, srv, mockDL, []btypes.Object{
		{
			ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			Properties: []btypes.Property{
				{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(42)},
			},
		},
	})

	if resp.DataType != btypes.SimpleAck {
		t.Fatalf("期望 SimpleAck，实际 DataType=%v err=%+v", resp.DataType, resp.Error)
	}
	if len(h.calls) != 1 {
		t.Fatalf("期望回调被调用 1 次，实际 %d", len(h.calls))
	}
	c := h.calls[0]
	if c.objType != btypes.AnalogInput || c.instance != 1 || c.prop != btypes.PROP_PRESENT_VALUE {
		t.Fatalf("回调参数异常: %+v", c)
	}
	if v, ok := c.value.(float64); !ok || v != 42 {
		t.Fatalf("回调值异常: %#v", c.value)
	}
	val, found := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE)
	if !found {
		t.Fatal("presentValue 丢失")
	}
	if v, ok := val.(float64); !ok || v != 42 {
		t.Fatalf("本地值未更新: %#v", val)
	}
}

// WPM handler 拒绝 ⇒ 必须回 BACnet Error（propertyError/writeAccessDenied），
// 不得假 SimpleAck，且本地值不得被改动。
func TestWriteMultiProperty_HandlerRejectsWithError(t *testing.T) {
	h := &recordingWriteHandler{err: errors.New("point is read-only")}
	srv, mockDL := newTestServerWithHandler(t, h)

	resp := sendWriteMultipleProperty(t, srv, mockDL, []btypes.Object{
		{
			ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			Properties: []btypes.Property{
				{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(99)},
			},
		},
	})

	if resp.DataType != btypes.Error {
		t.Fatalf("期望 Error PDU，实际 DataType=%v", resp.DataType)
	}
	if resp.Error.Class != bacerr.PropertyError || resp.Error.Code != bacerr.WriteAccessDenied {
		t.Fatalf("期望 propertyError/writeAccessDenied，实际 %v/%v", resp.Error.Class, resp.Error.Code)
	}
	if len(h.calls) != 1 {
		t.Fatalf("被拒时回调应被调用 1 次，实际 %d", len(h.calls))
	}
	val, _ := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE)
	if v, ok := val.(float64); !ok || v != 23.5 {
		t.Fatalf("被拒写入不得改动本地值: %#v", val)
	}
}

// 未注册回调时保持既有行为：WPM 只写本地并回 SimpleAck。
func TestWriteMultiProperty_NoHandlerKeepsLegacyBehaviour(t *testing.T) {
	srv, mockDL := newTestServerWithHandler(t, nil)

	resp := sendWriteMultipleProperty(t, srv, mockDL, []btypes.Object{
		{
			ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			Properties: []btypes.Property{
				{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(7)},
			},
		},
	})

	if resp.DataType != btypes.SimpleAck {
		t.Fatalf("期望 SimpleAck，实际 %v", resp.DataType)
	}
	val, _ := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE)
	if v, ok := val.(float64); !ok || v != 7 {
		t.Fatalf("本地值应为 7，实际 %#v", val)
	}
}

// 非 PresentValue 属性写入不经过回调（回调只负责 PresentValue）。
func TestWriteMultiProperty_NonPresentValueNotRoutedToHandler(t *testing.T) {
	h := &recordingWriteHandler{}
	srv, mockDL := newTestServerWithHandler(t, h)

	resp := sendWriteMultipleProperty(t, srv, mockDL, []btypes.Object{
		{
			ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			Properties: []btypes.Property{
				{Type: btypes.PROP_OBJECT_NAME, ArrayIndex: btypes.ArrayAll, Data: "Renamed"},
			},
		},
	})

	if resp.DataType != btypes.SimpleAck {
		t.Fatalf("期望 SimpleAck，实际 %v", resp.DataType)
	}
	if len(h.calls) != 0 {
		t.Fatalf("非 presentValue 写入不应触发回调，实际 %d 次", len(h.calls))
	}
	val, found := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_OBJECT_NAME)
	if !found || val != "Renamed" {
		t.Fatalf("Object_Name 未更新: %#v", val)
	}
}

// 多对象 WPM：每个 PresentValue 变更各调用一次回调。
func TestWriteMultiProperty_MultipleObjectsEachCallHandler(t *testing.T) {
	h := &recordingWriteHandler{}
	srv, mockDL := newTestServerWithHandler(t, h)

	if err := srv.AddObject(btypes.Object{
		ID:         btypes.ObjectID{Type: btypes.AnalogInput, Instance: 2},
		Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(10)}},
	}); err != nil {
		t.Fatalf("AddObject: %v", err)
	}

	resp := sendWriteMultipleProperty(t, srv, mockDL, []btypes.Object{
		{
			ID:         btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(11)}},
		},
		{
			ID:         btypes.ObjectID{Type: btypes.AnalogInput, Instance: 2},
			Properties: []btypes.Property{{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(12)}},
		},
	})

	if resp.DataType != btypes.SimpleAck {
		t.Fatalf("期望 SimpleAck，实际 %v", resp.DataType)
	}
	if len(h.calls) != 2 {
		t.Fatalf("期望回调被调用 2 次，实际 %d", len(h.calls))
	}
	if h.calls[0].instance != 1 || h.calls[1].instance != 2 {
		t.Fatalf("回调对象顺序异常: %+v", h.calls)
	}
}
