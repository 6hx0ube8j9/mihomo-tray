package random

import (
	"crypto/rand"
	"encoding/hex"
)

const alphaNumLowerCharset = "abcdefghijklmnopqrstuvwxyz0123456789"

func String(length int) string {
	byteLen := (length / 2) + 1
	b := make([]byte, byteLen)

	if _, err := rand.Read(b); err != nil {
		return "FallbackSecureSecret"
	}

	encoded := hex.EncodeToString(b)
	if len(encoded) > length {
		encoded = encoded[:length]
	}

	return encoded
}

func AlphaNumLower(length int) string {
	if length <= 0 {
		return ""
	}

	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		const fallback = "ud65a9x2"
		if length <= len(fallback) {
			return fallback[:length]
		}
		return fallback
	}

	charsetLen := byte(len(alphaNumLowerCharset))
	for i := range b {
		b[i] = alphaNumLowerCharset[b[i]%charsetLen]
	}
	return string(b)
}