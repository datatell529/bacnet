package encoding

import (
	"bytes"
	"strings"
	"testing"
)

// TestAppDataIntegerWidths verifies the integer widths added in V1.30-F:
// uint8/uint16 encode as BACnet UNSIGNED, int8/int16/int as BACnet SIGNED,
// using the shortest value length and the correct application tag.
func TestAppDataIntegerWidths(t *testing.T) {
	tests := []struct {
		name    string
		value   interface{}
		wantTag uint8
		wantLen int
		want    []byte
	}{
		{name: "uint8-0", value: uint8(0), wantTag: tagUint, wantLen: size8, want: []byte{0x21, 0x00}},
		{name: "uint8-200", value: uint8(200), wantTag: tagUint, wantLen: size8, want: []byte{0x21, 0xC8}},
		{name: "uint16-0", value: uint16(0), wantTag: tagUint, wantLen: size8, want: []byte{0x21, 0x00}},
		{name: "uint16-255", value: uint16(255), wantTag: tagUint, wantLen: size8, want: []byte{0x21, 0xFF}},
		{name: "uint16-256", value: uint16(256), wantTag: tagUint, wantLen: size16, want: []byte{0x22, 0x01, 0x00}},
		{name: "uint16-65535", value: uint16(65535), wantTag: tagUint, wantLen: size16, want: []byte{0x22, 0xFF, 0xFF}},

		{name: "int8--100", value: int8(-100), wantTag: tagInt, wantLen: size8, want: []byte{0x31, 0x9C}},
		{name: "int8-127", value: int8(127), wantTag: tagInt, wantLen: size8, want: []byte{0x31, 0x7F}},
		{name: "int16--1", value: int16(-1), wantTag: tagInt, wantLen: size8, want: []byte{0x31, 0xFF}},
		{name: "int16-0", value: int16(0), wantTag: tagInt, wantLen: size8, want: []byte{0x31, 0x00}},
		{name: "int16--129", value: int16(-129), wantTag: tagInt, wantLen: size16, want: []byte{0x32, 0xFF, 0x7F}},
		{name: "int16-32767", value: int16(32767), wantTag: tagInt, wantLen: size16, want: []byte{0x32, 0x7F, 0xFF}},

		{name: "int-1e6", value: int(1e6), wantTag: tagInt, wantLen: size24, want: []byte{0x33, 0x0F, 0x42, 0x40}},
		{name: "int--1", value: int(-1), wantTag: tagInt, wantLen: size8, want: []byte{0x31, 0xFF}},
		{name: "int--32769", value: int(-32769), wantTag: tagInt, wantLen: size24, want: []byte{0x33, 0xFF, 0x7F, 0xFF}},
		{name: "int-maxint32", value: int(1<<31 - 1), wantTag: tagInt, wantLen: size32, want: []byte{0x34, 0x7F, 0xFF, 0xFF, 0xFF}},
		{name: "int-minint32", value: int(-1 << 31), wantTag: tagInt, wantLen: size32, want: []byte{0x34, 0x80, 0x00, 0x00, 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := NewEncoder()
			if err := enc.AppData(tt.value, false); err != nil {
				t.Fatalf("AppData(%T) error: %v", tt.value, err)
			}
			if err := enc.Error(); err != nil {
				t.Fatalf("encoder error: %v", err)
			}

			got := enc.Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("encoded bytes = % X, want % X", got, tt.want)
			}
			if tag := got[0] >> 4; tag != tt.wantTag {
				t.Errorf("tag = %d, want %d", tag, tt.wantTag)
			}
			if length := int(got[0] & 0x07); length != tt.wantLen {
				t.Errorf("length = %d, want %d", length, tt.wantLen)
			}
		})
	}
}

// TestAppDataUnsupportedTypesStillError ensures unknown types keep reporting
// "Unknown type" instead of being silently encoded as an integer.
func TestAppDataUnsupportedTypesStillError(t *testing.T) {
	for _, value := range []interface{}{uint64(1), []byte{0x01, 0x02}} {
		enc := NewEncoder()
		err := enc.AppData(value, false)
		if err == nil {
			t.Fatalf("AppData(%T) = nil error, want Unknown type", value)
		}
		if !strings.Contains(err.Error(), "Unknown type") {
			t.Errorf("AppData(%T) error = %q, want it to contain %q", value, err.Error(), "Unknown type")
		}
	}
}

// TestAppDataIntOutOfRange verifies int values outside BACnet SIGNED 32-bit
// range are rejected instead of silently truncated.
func TestAppDataIntOutOfRange(t *testing.T) {
	if strconvIntSize() < 64 {
		t.Skip("int is 32-bit on this platform; no out-of-range value to test")
	}
	for _, v64 := range []int64{int64(1) << 31, -(int64(1) << 31) - 1} {
		enc := NewEncoder()
		err := enc.AppData(int(v64), false)
		if err == nil {
			t.Fatalf("AppData(int(%d)) = nil error, want out-of-range error", v64)
		}
		if !strings.Contains(err.Error(), "out of BACnet SIGNED 32-bit range") {
			t.Errorf("AppData(int(%d)) error = %q", v64, err.Error())
		}
	}
}

func strconvIntSize() int {
	return 32 << (^uint(0) >> 63)
}
