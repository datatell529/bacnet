package encoding

import (
	"reflect"
	"testing"

	"github.com/anviod/bacnet/btypes"
)

// decodeWritePropertyRoundTrip encodes a WriteProperty request and decodes it
// back through Decoder.ReadProperty (the exact path the server uses for an
// inbound WriteProperty), returning the decoded PropertyData.
// 中文说明：真实编解码往返 —— Encoder.WriteProperty 编码，Decoder.ReadProperty 解码。
func decodeWritePropertyRoundTrip(t *testing.T, pd btypes.PropertyData) btypes.PropertyData {
	t.Helper()

	enc := NewEncoder()
	if err := enc.WriteProperty(1, pd); err != nil {
		t.Fatalf("WriteProperty encode: %v", err)
	}

	var apdu btypes.APDU
	if err := NewDecoder(enc.Bytes()).APDU(&apdu); err != nil {
		t.Fatalf("APDU decode: %v", err)
	}

	var out btypes.PropertyData
	if err := NewDecoder(apdu.RawData).ReadProperty(&out); err != nil {
		t.Fatalf("ReadProperty decode: %v", err)
	}

	if len(out.Object.Properties) != 1 {
		t.Fatalf("expected exactly 1 property, got %d", len(out.Object.Properties))
	}
	return out
}

// TestWriteProperty_PriorityTag covers the regression where an optional
// context tag 4 (priority) present after the value list was mis-decoded as an
// application value, turning a scalar PresentValue into a polluted array.
// 中文说明：覆盖「值列表循环把闭合标签/可选 priority 标签当应用数据」的缺陷。
func TestWriteProperty_PriorityTag(t *testing.T) {
	tests := []struct {
		name         string
		value        interface{}
		priority     btypes.NPDUPriority
		wantData     interface{}
		wantScalar   bool
		wantPriority btypes.NPDUPriority
	}{
		{
			name:         "normal priority scalar",
			value:        float32(3.5),
			priority:     btypes.Normal,
			wantData:     float32(3.5),
			wantScalar:   true,
			wantPriority: btypes.Normal,
		},
		{
			name:         "priority 16 scalar",
			value:        float32(9.5),
			priority:     btypes.NPDUPriority(16),
			wantData:     float32(9.5),
			wantScalar:   true,
			wantPriority: btypes.NPDUPriority(16),
		},
		{
			name:         "priority 1 scalar (small tag value)",
			value:        float32(7.25),
			priority:     btypes.Urgent,
			wantData:     float32(7.25),
			wantScalar:   true,
			wantPriority: btypes.Urgent,
		},
		{
			name:         "priority 8 multi element list",
			value:        []interface{}{float32(1.5), float32(2.5)},
			priority:     btypes.NPDUPriority(8),
			wantData:     []interface{}{float32(1.5), float32(2.5)},
			wantScalar:   false,
			wantPriority: btypes.NPDUPriority(8),
		},
		{
			name:         "normal priority multi element list",
			value:        []interface{}{float32(1.5), float32(2.5)},
			priority:     btypes.Normal,
			wantData:     []interface{}{float32(1.5), float32(2.5)},
			wantScalar:   false,
			wantPriority: btypes.Normal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pd := btypes.PropertyData{
				Object: btypes.Object{
					ID: btypes.ObjectID{Type: btypes.AnalogOutput, Instance: 42},
					Properties: []btypes.Property{{
						Type:       btypes.PROP_PRESENT_VALUE,
						ArrayIndex: ArrayAll,
						Data:       tt.value,
						Priority:   tt.priority,
					}},
				},
			}

			out := decodeWritePropertyRoundTrip(t, pd)
			got := out.Object.Properties[0]

			// The priority tag must land in Priority, never be folded into Data.
			if got.Priority != tt.wantPriority {
				t.Errorf("Priority = %d, want %d", got.Priority, tt.wantPriority)
			}

			if !reflect.DeepEqual(got.Data, tt.wantData) {
				t.Errorf("Data = %#v, want %#v", got.Data, tt.wantData)
			}

			// A single element must collapse to the scalar (existing behavior);
			// it must never surface as an array.
			if tt.wantScalar {
				if _, isList := got.Data.([]interface{}); isList {
					t.Errorf("Data decoded as array %#v, want scalar %#v", got.Data, tt.wantData)
				}
			}
		})
	}
}

// TestWriteProperty_PriorityTag_GoldenBytes pins the wire form so a future
// change cannot silently drop or re-order the optional priority tag.
// 中文说明：固定编码字节，防止可选 priority 标签被静默丢弃。
func TestWriteProperty_PriorityTag_GoldenBytes(t *testing.T) {
	pd := btypes.PropertyData{
		Object: btypes.Object{
			ID: btypes.ObjectID{Type: btypes.AnalogOutput, Instance: 1},
			Properties: []btypes.Property{{
				Type:       btypes.PROP_PRESENT_VALUE,
				ArrayIndex: ArrayAll,
				Data:       float32(9.5),
				Priority:   btypes.NPDUPriority(16),
			}},
		},
	}

	enc := NewEncoder()
	if err := enc.WriteProperty(1, pd); err != nil {
		t.Fatalf("WriteProperty encode: %v", err)
	}

	var apdu btypes.APDU
	if err := NewDecoder(enc.Bytes()).APDU(&apdu); err != nil {
		t.Fatalf("APDU decode: %v", err)
	}

	// [value real 9.5] 3E 44 41 18 00 00 | closing tag 3 (3F) | priority tag 4 = 16 (49 10)
	want := []byte{
		0x0C, 0x00, 0x40, 0x00, 0x01, // object id tag 0 + AnalogOutput(1):1
		0x19, 0x55, // property id tag 1 = 85
		0x3E, 0x44, 0x41, 0x18, 0x00, 0x00, // opening tag 3 + real 9.5
		0x3F,       // closing tag 3
		0x49, 0x10, // optional priority tag 4 = 16
	}
	if !reflect.DeepEqual(apdu.RawData, want) {
		t.Fatalf("WriteProperty raw data mismatch\n got: % X\nwant: % X", apdu.RawData, want)
	}
}
