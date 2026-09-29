package crypto

import (
	"crypto/rand"
	"encoding/hex"
)

func GenerateRandomSecret(length int) string {
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
