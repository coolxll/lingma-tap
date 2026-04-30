package encoding

import (
	"encoding/base64"
	"fmt"
)

const (
	CustomAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
	StdAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	CustomPad      = '$'
	StdPad         = '='
)

var (
	s2c = make(map[byte]byte)
	c2s = make(map[byte]byte)
)

func init() {
	for i := 0; i < 64; i++ {
		s2c[StdAlphabet[i]] = CustomAlphabet[i]
		c2s[CustomAlphabet[i]] = StdAlphabet[i]
	}
	s2c[StdPad] = CustomPad
	c2s[CustomPad] = StdPad
}

func Encode(plaintext []byte) string {
	std := base64.StdEncoding.EncodeToString(plaintext)
	n := len(std)
	if n == 0 {
		return ""
	}
	a := n / 3

	// Rearrangement: std.substring(n - a) + std.substring(a, n - a) + std.substring(0, a)
	rearranged := std[n-a:] + std[a:n-a] + std[:a]

	encoded := make([]byte, n)
	for i := 0; i < n; i++ {
		encoded[i] = s2c[rearranged[i]]
	}
	return string(encoded)
}

func Decode(encoded string) ([]byte, error) {
	n := len(encoded)
	if n == 0 {
		return []byte{}, nil
	}

	mapped := make([]byte, n)
	for i := 0; i < n; i++ {
		val, ok := c2s[encoded[i]]
		if !ok {
			return nil, fmt.Errorf("char out of custom alphabet: %c", encoded[i])
		}
		mapped[i] = val
	}

	a := n / 3
	std := string(mapped[n-a:]) + string(mapped[a:n-a]) + string(mapped[:a])

	return base64.StdEncoding.DecodeString(std)
}
