package encoding

import (
	"bytes"
	"testing"
)

// TestSigned24_Table covers the 24-bit SIGNED decoding cases required by
// V1.31-F2. Before the fix the sign bit was masked away, so every negative
// value decoded as a large positive number.
func TestSigned24_Table(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want int32
	}{
		{name: "zero", raw: []byte{0x00, 0x00, 0x00}, want: 0},
		{name: "one", raw: []byte{0x00, 0x00, 0x01}, want: 1},
		{name: "minus-one", raw: []byte{0xFF, 0xFF, 0xFF}, want: -1},
		{name: "127", raw: []byte{0x00, 0x00, 0x7F}, want: 127},
		{name: "minus-128", raw: []byte{0xFF, 0xFF, 0x80}, want: -128},
		{name: "32767", raw: []byte{0x00, 0x7F, 0xFF}, want: 32767},
		{name: "minus-32768", raw: []byte{0xFF, 0x80, 0x00}, want: -32768},
		{name: "8388607", raw: []byte{0x7F, 0xFF, 0xFF}, want: 8388607},
		{name: "minus-8388608", raw: []byte{0x80, 0x00, 0x00}, want: -8388608},
		{name: "123456", raw: []byte{0x01, 0xE2, 0x40}, want: 123456},
		{name: "minus-123456", raw: []byte{0xFE, 0x1D, 0xC0}, want: -123456},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDecoder(tt.raw)
			got := d.signed24()
			if err := d.Error(); err != nil {
				t.Fatalf("signed24() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("signed24(% X) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

// TestSigned24_AppDataRoundTrip encodes int values that take the 3-byte SIGNED
// branch and decodes them back through AppData. It also asserts the wire form
// really is application tag 3 with length 3 (so the round trip exercises
// signed24, not the 1/2/4-byte paths).
func TestSigned24_AppDataRoundTrip(t *testing.T) {
	for _, value := range []int{-123456, 8388607, -8388608} {
		t.Run(testValueName(value), func(t *testing.T) {
			enc := NewEncoder()
			if err := enc.AppData(value, false); err != nil {
				t.Fatalf("AppData(%d) error: %v", value, err)
			}
			if err := enc.Error(); err != nil {
				t.Fatalf("encoder error: %v", err)
			}
			b := enc.Bytes()
			if len(b) != 4 {
				t.Fatalf("encoded length = %d, want 4 (tag+3 bytes)", len(b))
			}
			if tag := b[0] >> 4; tag != tagInt {
				t.Errorf("tag = %d, want tagInt(%d)", tag, tagInt)
			}
			if length := int(b[0] & 0x07); length != size24 {
				t.Errorf("length = %d, want size24(%d)", length, size24)
			}
			// -123456 has a known wire representation; verify the bytes so the
			// round trip cannot mask an encoder/decoder mismatch.
			if value == -123456 {
				if want := []byte{0x33, 0xFE, 0x1D, 0xC0}; !bytes.Equal(b, want) {
					t.Errorf("encoded bytes = % X, want % X", b, want)
				}
			}

			dec := NewDecoder(b)
			got, err := dec.AppData()
			if err != nil {
				t.Fatalf("AppData() decode error: %v", err)
			}
			gotInt, ok := got.(int32)
			if !ok {
				t.Fatalf("decoded type = %T, want int32", got)
			}
			if gotInt != int32(value) {
				t.Errorf("round trip value = %d, want %d", gotInt, value)
			}
		})
	}
}

func testValueName(v int) string {
	switch v {
	case -123456:
		return "minus-123456"
	case 8388607:
		return "8388607"
	case -8388608:
		return "minus-8388608"
	default:
		return "value"
	}
}
