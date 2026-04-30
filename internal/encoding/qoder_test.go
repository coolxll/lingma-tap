package encoding

import (
	"bytes"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"hello":"world"}`),
		[]byte(`{"choices":[{"delta":{"content":"Hello","role":"assistant"}}]}`),
		[]byte(``),
		[]byte(`a`),
		[]byte(`ab`),
		[]byte(`abc`),
		[]byte(`Hello, 世界!`),
	}
	for _, plain := range cases {
		enc := Encode(plain)
		dec, err := Decode(enc)
		if err != nil {
			t.Errorf("Decode(%q) error: %v", enc, err)
			continue
		}
		if !bytes.Equal(dec, plain) {
			t.Errorf("roundtrip failed: %q -> %q -> %q", plain, enc, dec)
		}
	}
}

func TestDecodeInvalidChar(t *testing.T) {
	_, err := Decode("hello\x00world")
	if err == nil {
		t.Error("expected error for invalid character")
	}
}

func TestDecodeEmpty(t *testing.T) {
	dec, err := Decode("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(dec) != 0 {
		t.Errorf("expected empty, got %q", dec)
	}
}
