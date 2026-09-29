package random

import (
	"crypto/rand"
	"encoding/hex"
)

// Secret 生成指定长度的十六进制安全随机字符串
func Secret(length int) string {
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
