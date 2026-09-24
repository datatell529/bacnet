package server

import (
	"errors"
	"testing"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/btypes/bacerr"
	"github.com/anviod/bacnet/encoding"
)

// recordingWriteHandler 记录回调调用并可选返回错误。
type recordingWriteHandler struct {
	calls []writeCall
	err   error
}

type writeCall struct {
	objType  btypes.ObjectType
	instance btypes.ObjectInstance
	prop     btypes.PropertyType
	value    interface{}
}

func (h *recordingWriteHandler) WriteProperty(objType btypes.ObjectType, instance btypes.ObjectInstance, prop btypes.PropertyType, value interface{}) error {
	h.calls = append(h.calls, writeCall{objType, instance, prop, value})
	return h.err
}

// newTestServerWithHandler 构造带写回调的测试服务端。
func newTestServerWithHandler(t *testing.T, h WritePropertyHandler) (*server, *mockDataLink) {
	t.Helper()
	cfg := DefaultDeviceConfig()
	cfg.DeviceID = 1000
	cfg.DeviceName = "TestServer"
	cfg.VendorID = 999
	cfg.WriteHandler = h

	mockDL := newMockDataLink()
	srv, err := NewServerWithDataLink(cfg, mockDL)
	if err != nil {
		t.Fatalf("NewServerWithDataLink: %v", err)
	}
	srv.AddObject(btypes.Object{
		ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
		Properties: []btypes.Property{
			{Type: btypes.PROP_PRESENT_VALUE, ArrayIndex: btypes.ArrayAll, Data: float64(23.5)},
			{Type: btypes.PROP_OBJECT_NAME, ArrayIndex: btypes.ArrayAll, Data: "TempSensor"},
		},
	})
	return srv.(*server), mockDL
}

// buildWritePropertyMsg 构造并处理一条 WriteProperty 请求，返回解析后的应答 APDU。
func sendWriteProperty(t *testing.T, srv *server, mockDL *mockDataLink, objType btypes.ObjectType, instance btypes.ObjectInstance, prop btypes.PropertyType, data interface{}) *btypes.APDU {
	t.Helper()
	enc := encoding.NewEncoder()
	wp := btypes.PropertyData{
		Object: btypes.Object{
			ID:         btypes.ObjectID{Type: objType, Instance: instance},
			Properties: []btypes.Property{{Type: prop, ArrayIndex: btypes.ArrayAll, Data: data}},
		},
	}
	if err := enc.WriteProperty(1, wp); err != nil {
		t.Fatalf("encode WriteProperty: %v", err)
	}
	full := enc.Bytes()
	// 拆出「APDU 头」与「服务负载」：handleWriteProperty 解码的是负载部分。
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

// 可写点：presentValue 写入必须经回调下发，成功 ⇒ SimpleAck + 本地值同步更新。
func TestWriteProperty_PresentValueRoutedThroughHandler(t *testing.T) {
	h := &recordingWriteHandler{}
	srv, mockDL := newTestServerWithHandler(t, h)

	resp := sendWriteProperty(t, srv, mockDL, btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE, float64(42))

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
	// 本地对象存储同步更新（写完立刻可读）
	val, found := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE)
	if !found {
		t.Fatal("presentValue 丢失")
	}
	if v, ok := val.(float64); !ok || v != 42 {
		t.Fatalf("本地值未更新: %#v", val)
	}
}

// 回调拒绝（例如只读点）⇒ 必须回 BACnet Error（propertyError/writeAccessDenied），
// 不再假 SimpleAck，且本地值不得被改动。
func TestWriteProperty_HandlerRejectsWithError(t *testing.T) {
	h := &recordingWriteHandler{err: errors.New("point is read-only")}
	srv, mockDL := newTestServerWithHandler(t, h)

	resp := sendWriteProperty(t, srv, mockDL, btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE, float64(99))

	if resp.DataType != btypes.Error {
		t.Fatalf("期望 Error PDU，实际 DataType=%v", resp.DataType)
	}
	if resp.Error.Class != bacerr.PropertyError || resp.Error.Code != bacerr.WriteAccessDenied {
		t.Fatalf("期望 propertyError/writeAccessDenied，实际 %v/%v", resp.Error.Class, resp.Error.Code)
	}
	val, _ := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE)
	if v, ok := val.(float64); !ok || v != 23.5 {
		t.Fatalf("被拒写入不得改动本地值: %#v", val)
	}
}

// 默认关闭：未注册回调时保持既有行为（只写本地 + SimpleAck）。
func TestWriteProperty_NoHandlerKeepsLegacyBehaviour(t *testing.T) {
	srv, mockDL := newTestServerWithHandler(t, nil)

	resp := sendWriteProperty(t, srv, mockDL, btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE, float64(7))

	if resp.DataType != btypes.SimpleAck {
		t.Fatalf("期望 SimpleAck，实际 %v", resp.DataType)
	}
	val, _ := srv.GetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE)
	if v, ok := val.(float64); !ok || v != 7 {
		t.Fatalf("本地值应为 7，实际 %#v", val)
	}
}

// 非 presentValue 属性写入不经过回调（回调只负责 presentValue）。
func TestWriteProperty_NonPresentValueNotRoutedToHandler(t *testing.T) {
	h := &recordingWriteHandler{}
	srv, mockDL := newTestServerWithHandler(t, h)

	resp := sendWriteProperty(t, srv, mockDL, btypes.AnalogInput, 1, btypes.PROP_OBJECT_NAME, "Renamed")

	if resp.DataType != btypes.SimpleAck {
		t.Fatalf("期望 SimpleAck，实际 %v", resp.DataType)
	}
	if len(h.calls) != 0 {
		t.Fatalf("非 presentValue 写入不应触发回调，实际 %d 次", len(h.calls))
	}
}
