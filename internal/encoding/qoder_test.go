package encoding

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"empty", []byte{}},
		{"single byte", []byte{0x00}},
		{"two bytes", []byte{0x01, 0x02}},
		{"three bytes (no base64 padding)", []byte("abc")},
		{"four bytes (one pad)", []byte("abcd")},
		{"five bytes (two pads)", []byte("abcde")},
		{"text", []byte("Hello, Lingma!")},
		{"all zeros", bytes.Repeat([]byte{0x00}, 16)},
		{"all 0xff", bytes.Repeat([]byte{0xff}, 16)},
		{"binary mix", []byte{0x00, 0x7f, 0x80, 0xff, 0x10, 0x20}},
		{"long", bytes.Repeat([]byte("Lingma"), 64)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := Encode(tc.in)
			got, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode(Encode(%q)) error: %v", tc.in, err)
			}
			if !bytes.Equal(got, tc.in) {
				t.Fatalf("round-trip mismatch: got %q, want %q (encoded=%q)", got, tc.in, encoded)
			}
		})
	}
}

func TestEncodeOutputAlphabet(t *testing.T) {
	allowed := CustomAlphabet + string(CustomPad)
	allowedSet := make(map[byte]bool, len(allowed))
	for i := 0; i < len(allowed); i++ {
		allowedSet[allowed[i]] = true
	}

	// Use input that exercises a wide range of base64 output bytes.
	input := make([]byte, 256)
	for i := range input {
		input[i] = byte(i)
	}
	out := Encode(input)
	if out == "" {
		t.Fatal("expected non-empty encoded output")
	}
	for i := 0; i < len(out); i++ {
		if !allowedSet[out[i]] {
			t.Fatalf("Encode produced char %q at index %d outside CustomAlphabet/CustomPad", out[i], i)
		}
	}
}

func TestDecodeEmpty(t *testing.T) {
	got, err := Decode("")
	if err != nil {
		t.Fatalf("Decode(\"\") error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Decode(\"\") = %v, want empty slice", got)
	}
}

func TestEncodeEmpty(t *testing.T) {
	if got := Encode(nil); got != "" {
		t.Fatalf("Encode(nil) = %q, want empty string", got)
	}
	if got := Encode([]byte{}); got != "" {
		t.Fatalf("Encode([]) = %q, want empty string", got)
	}
}

func TestDecodeRejectsForeignChar(t *testing.T) {
	// '?' is not in CustomAlphabet and not the CustomPad.
	if strings.ContainsRune(CustomAlphabet+string(CustomPad), '?') {
		t.Fatal("test precondition broken: '?' unexpectedly in custom alphabet")
	}
	_, err := Decode("???")
	if err == nil {
		t.Fatal("Decode of foreign-char string should fail, got nil")
	}
	if !strings.Contains(err.Error(), "char out of custom alphabet") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestAlphabetIsBijection(t *testing.T) {
	// Sanity check on the package init — every std base64 char must map to a
	// unique custom char and back. This guards against typos in CustomAlphabet.
	if len(CustomAlphabet) != len(StdAlphabet) {
		t.Fatalf("alphabet length mismatch: custom=%d std=%d", len(CustomAlphabet), len(StdAlphabet))
	}
	seen := make(map[byte]bool, len(CustomAlphabet))
	for i := 0; i < len(CustomAlphabet); i++ {
		c := CustomAlphabet[i]
		if seen[c] {
			t.Fatalf("CustomAlphabet has duplicate char %q at index %d", c, i)
		}
		seen[c] = true
		if c2s[c] != StdAlphabet[i] {
			t.Fatalf("c2s[%q] = %q, want %q", c, c2s[c], StdAlphabet[i])
		}
		if s2c[StdAlphabet[i]] != c {
			t.Fatalf("s2c[%q] = %q, want %q", StdAlphabet[i], s2c[StdAlphabet[i]], c)
		}
	}
}
