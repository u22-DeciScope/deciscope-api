package domain

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func NewID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])

	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80

	encoded := hex.EncodeToString(bytes[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func IsUUID(value string) bool {
	if len(value) != 36 {
		return false
	}

	for _, position := range []int{8, 13, 18, 23} {
		if value[position] != '-' {
			return false
		}
	}

	trimmed := strings.ReplaceAll(value, "-", "")
	if len(trimmed) != 32 {
		return false
	}

	_, err := hex.DecodeString(trimmed)
	return err == nil
}
