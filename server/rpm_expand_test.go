package server

import (
	"testing"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/encoding"
)

// sendReadMultipleProperty 构造并处理一条 ReadPropertyMultiple 请求，
// 返回 ComplexAck 中解码后的对象属性列表。
func sendReadMultipleProperty(t *testing.T, srv *server, mockDL *mockDataLink, objects []btypes.Object) btypes.MultiplePropertyData {
	t.Helper()

	enc := encoding.NewEncoder()
	req := btypes.MultiplePropertyData{Objects: objects}
	if err := enc.ReadMultipleProperty(1, req); err != nil {
		t.Fatalf("encode ReadPropertyMultiple: %v", err)
	}
	full := enc.Bytes()

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
	if out.DataType != btypes.ComplexAck {
		t.Fatalf("期望 ComplexAck，实际 DataType=%v err=%+v", out.DataType, out.Error)
	}

	var ack btypes.MultiplePropertyData
	if err := encoding.NewDecoder(out.RawData).ReadMultiplePropertyAck(&ack); err != nil {
		t.Fatalf("ReadMultiplePropertyAck decode: %v", err)
	}
	return ack
}

// newExpandTestServer 构造一个带默认属性的非 Device 对象（AI-1）的服务端。
func newExpandTestServer(t *testing.T) (*server, *mockDataLink) {
	t.Helper()
	cfg := DefaultDeviceConfig()
	cfg.DeviceID = 1000
	cfg.DeviceName = "ExpandDevice"
	cfg.VendorID = 999

	dl := newMockDataLink()
	srvIface, err := NewServerWithDataLink(cfg, dl)
	if err != nil {
		t.Fatalf("NewServerWithDataLink: %v", err)
	}
	srv := srvIface.(*server)

	// 只用 ID：AddObject 会填充默认属性集（10 项，含 Object_Name）。
	if err := srv.AddObject(btypes.Object{ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1}}); err != nil {
		t.Fatalf("AddObject: %v", err)
	}
	if err := srv.SetProperty(btypes.AnalogInput, 1, btypes.PROP_PRESENT_VALUE, float64(23.5)); err != nil {
		t.Fatalf("SetProperty: %v", err)
	}
	return srv, dl
}

func propsByType(props []btypes.Property) map[btypes.PropertyType]btypes.Property {
	m := make(map[btypes.PropertyType]btypes.Property, len(props))
	for _, p := range props {
		m[p.Type] = p
	}
	return m
}

// 非 Device 对象的 RPM(PROP_ALL) 必须展开出完整属性集（>=8 且含 Object_Name），
// PresentValue 按既有 normalize 规则编码（Analog → float32）。
func TestReadMultiProperty_ExpandAllOnNonDevice(t *testing.T) {
	srv, dl := newExpandTestServer(t)

	ack := sendReadMultipleProperty(t, srv, dl, []btypes.Object{
		{
			ID:         btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
			Properties: []btypes.Property{{Type: btypes.PROP_ALL, ArrayIndex: btypes.ArrayAll}},
		},
	})

	if len(ack.Objects) != 1 {
		t.Fatalf("期望 1 个对象，实际 %d", len(ack.Objects))
	}
	props := ack.Objects[0].Properties
	if len(props) < 8 {
		t.Fatalf("AnalogInput PROP_ALL 期望 >=8 项属性，实际 %d", len(props))
	}
	byType := propsByType(props)
	name, ok := byType[btypes.PROP_OBJECT_NAME]
	if !ok {
		t.Fatal("展开结果缺少 Object_Name")
	}
	if got, ok := name.Data.(string); !ok || got != "AnalogInput-1" {
		t.Fatalf("Object_Name = %#v，期望 \"AnalogInput-1\"", name.Data)
	}
	pv, ok := byType[btypes.PROP_PRESENT_VALUE]
	if !ok {
		t.Fatal("展开结果缺少 Present_Value")
	}
	if got, ok := pv.Data.(float32); !ok || got != 23.5 {
		t.Fatalf("Present_Value = %T(%v)，期望 float32(23.5)", pv.Data, pv.Data)
	}
}

// PROP_ALL / PROP_REQUIRED / PROP_OPTIONAL 对非 Device 对象都应展开。
func TestReadMultiProperty_AllRequiredOptionalExpandOnNonDevice(t *testing.T) {
	srv, dl := newExpandTestServer(t)

	for _, tc := range []struct {
		name string
		prop btypes.PropertyType
	}{
		{"ALL", btypes.PROP_ALL},
		{"REQUIRED", btypes.PROP_REQUIRED},
		{"OPTIONAL", btypes.PROP_OPTIONAL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ack := sendReadMultipleProperty(t, srv, dl, []btypes.Object{
				{
					ID:         btypes.ObjectID{Type: btypes.AnalogInput, Instance: 1},
					Properties: []btypes.Property{{Type: tc.prop, ArrayIndex: btypes.ArrayAll}},
				},
			})
			if len(ack.Objects) != 1 {
				t.Fatalf("期望 1 个对象，实际 %d", len(ack.Objects))
			}
			if len(ack.Objects[0].Properties) < 8 {
				t.Fatalf("%s 展开期望 >=8 项属性，实际 %d", tc.name, len(ack.Objects[0].Properties))
			}
			if _, ok := propsByType(ack.Objects[0].Properties)[btypes.PROP_OBJECT_NAME]; !ok {
				t.Fatalf("%s 展开结果缺少 Object_Name", tc.name)
			}
		})
	}
}

// Device 对象的 RPM(PROP_ALL) 仍走 ListDeviceProperties。
func TestReadMultiProperty_DeviceAllUsesListDeviceProperties(t *testing.T) {
	srv, dl := newExpandTestServer(t)

	ack := sendReadMultipleProperty(t, srv, dl, []btypes.Object{
		{
			ID:         btypes.ObjectID{Type: btypes.DeviceType, Instance: 1000},
			Properties: []btypes.Property{{Type: btypes.PROP_ALL, ArrayIndex: btypes.ArrayAll}},
		},
	})
	if len(ack.Objects) != 1 {
		t.Fatalf("期望 1 个对象，实际 %d", len(ack.Objects))
	}

	want := srv.store.ListDeviceProperties()
	if len(ack.Objects[0].Properties) != len(want) {
		t.Fatalf("Device PROP_ALL 属性数 = %d，期望 ListDeviceProperties 的 %d",
			len(ack.Objects[0].Properties), len(want))
	}
	byType := propsByType(ack.Objects[0].Properties)
	if name, ok := byType[btypes.PROP_OBJECT_NAME]; !ok {
		t.Fatal("Device 展开缺少 Object_Name")
	} else if got, ok := name.Data.(string); !ok || got != "ExpandDevice" {
		t.Fatalf("Device Object_Name = %#v，期望 \"ExpandDevice\"", name.Data)
	}
	if _, ok := byType[btypes.PROP_VENDOR_IDENTIFIER]; !ok {
		t.Fatal("Device 展开缺少 Vendor_Identifier")
	}
	if _, ok := byType[btypes.PROP_OBJECT_LIST]; !ok {
		t.Fatal("Device 展开缺少 Object_List")
	}
}

// expandObjectProps 单元级验证：非 Device 返回对象自身属性并 normalize PV；
// Device 返回 ListDeviceProperties。
func TestExpandObjectProps(t *testing.T) {
	srv, _ := newExpandTestServer(t)

	props := srv.expandObjectProps(btypes.AnalogInput, 1)
	if len(props) < 8 {
		t.Fatalf("AnalogInput 展开期望 >=8 项，实际 %d", len(props))
	}
	if _, ok := propsByType(props)[btypes.PROP_OBJECT_NAME]; !ok {
		t.Fatal("AnalogInput 展开缺少 Object_Name")
	}

	devProps := srv.expandObjectProps(btypes.DeviceType, 1000)
	if len(devProps) != len(srv.store.ListDeviceProperties()) {
		t.Fatalf("Device 展开应与 ListDeviceProperties 一致")
	}

	if got := srv.expandObjectProps(btypes.AnalogInput, 999); got != nil {
		t.Fatalf("未知对象应返回 nil，实际 %d 项", len(got))
	}
}

// RPM 伪属性常量必须使用标准线上值；PROP_OBJECT_LIST 必须是 76（object-list），
// 72 是 notify-type。
func TestRPMPseudoPropertyConstants(t *testing.T) {
	if btypes.PROP_ALL != btypes.PropertyType(0xff) {
		t.Fatalf("PROP_ALL = 0x%X，期望 0xff", uint32(btypes.PROP_ALL))
	}
	if btypes.PROP_OPTIONAL != btypes.PropertyType(0xfd) {
		t.Fatalf("PROP_OPTIONAL = 0x%X，期望 0xfd", uint32(btypes.PROP_OPTIONAL))
	}
	if btypes.PROP_REQUIRED != btypes.PropertyType(0xfe) {
		t.Fatalf("PROP_REQUIRED = 0x%X，期望 0xfe", uint32(btypes.PROP_REQUIRED))
	}
	if btypes.PROP_OBJECT_LIST != btypes.PropertyType(76) {
		t.Fatalf("PROP_OBJECT_LIST = %d，期望 76", uint32(btypes.PROP_OBJECT_LIST))
	}
	if btypes.PROP_NOTIFY_TYPE != btypes.PropertyType(72) {
		t.Fatalf("PROP_NOTIFY_TYPE = %d，期望 72", uint32(btypes.PROP_NOTIFY_TYPE))
	}
}
