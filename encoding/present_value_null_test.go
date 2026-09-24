package encoding

import (
	"bytes"
	"testing"

	"github.com/anviod/bacnet/btypes"
	"github.com/anviod/bacnet/btypes/null"
)

// N-98：空/未初始化的 presentValue 必须编码成**合法**应用数据。
// 旧行为：AppData(nil) 不写任何字节 ⇒ ReadPropertyAck 里属性值只剩
// 空的 opening/closing 标记对（`3E 3F`）⇒ bacpypes3 报
// InvalidTag('unexpected opening/closing tag')。
// 新行为：编码成 BACnet NULL（应用标签 0，单字节 `00`）。
func TestPresentValue_NilEncodesNullTag(t *testing.T) {
	p := btypes.PropertyData{
		Object: btypes.Object{
			ID: btypes.ObjectID{Type: btypes.AnalogInput, Instance: 7},
			Properties: []btypes.Property{{
				Type:       btypes.PROP_PRESENT_VALUE,
				ArrayIndex: btypes.ArrayAll,
				Data:       nil,
			}},
		},
	}

	enc := NewEncoder()
	if err := enc.ReadPropertyAck(1, p); err != nil {
		t.Fatalf("ReadPropertyAck: %v", err)
	}
	raw := enc.Bytes()

	// 属性值 = opening tag 3 (0x3E) + NULL (0x00) + closing tag 3 (0x3F)
	if !bytes.HasSuffix(raw, []byte{0x3E, 0x00, 0x3F}) {
		t.Fatalf("nil presentValue 应以 3E 00 3F 结尾（NULL），实际 % X", raw)
	}
	// 旧的非法形态（空 opening/closing 对）必须不复存在
	if bytes.Contains(raw, []byte{0x3E, 0x3F}) {
		t.Fatalf("仍出现非法空标记对 3E 3F: % X", raw)
	}

	// 回环：解码后应得到 null.Null（而不是报错/空）
	dec := NewDecoder(raw)
	var apdu btypes.APDU
	if err := dec.APDU(&apdu); err != nil {
		t.Fatalf("APDU decode: %v", err)
	}
	var out btypes.PropertyData
	if err := NewDecoder(apdu.RawData).ReadProperty(&out); err != nil {
		t.Fatalf("ReadProperty decode: %v", err)
	}
	if _, ok := out.Object.Properties[0].Data.(null.Null); !ok {
		t.Fatalf("解码后应为 null.Null，实际 %T (%#v)",
			out.Object.Properties[0].Data, out.Object.Properties[0].Data)
	}
}

// 覆盖多个对象类型/数据类型下「值为空」的编码，全部必须合法（可解码）。
func TestPresentValue_NilEncodesNullTag_AllObjectTypes(t *testing.T) {
	cases := []struct {
		name    string
		objType btypes.ObjectType
	}{
		{"AnalogInput", btypes.AnalogInput},
		{"AnalogValue", btypes.AnalogValue},
		{"BinaryInput", btypes.BinaryInput},
		{"BinaryValue", btypes.BinaryValue},
		{"MultiStateValue", btypes.MultiStateValue},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := btypes.PropertyData{
				Object: btypes.Object{
					ID: btypes.ObjectID{Type: c.objType, Instance: 1},
					Properties: []btypes.Property{{
						Type:       btypes.PROP_PRESENT_VALUE,
						ArrayIndex: btypes.ArrayAll,
						Data:       nil,
					}},
				},
			}
			enc := NewEncoder()
			if err := enc.ReadPropertyAck(2, p); err != nil {
				t.Fatalf("ReadPropertyAck: %v", err)
			}
			raw := enc.Bytes()
			if !bytes.HasSuffix(raw, []byte{0x3E, 0x00, 0x3F}) {
				t.Fatalf("期望以 3E 00 3F 结尾，实际 % X", raw)
			}

			dec := NewDecoder(raw)
			var apdu btypes.APDU
			if err := dec.APDU(&apdu); err != nil {
				t.Fatalf("APDU decode: %v", err)
			}
			var out btypes.PropertyData
			if err := NewDecoder(apdu.RawData).ReadProperty(&out); err != nil {
				t.Fatalf("ReadProperty decode: %v", err)
			}
		})
	}
}
