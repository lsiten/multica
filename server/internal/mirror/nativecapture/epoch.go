package nativecapture

import (
	"crypto/rand"
	"encoding/hex"
)

func newEpoch() string {
	var value [32]byte
	_, _ = rand.Read(value[:])
	return hex.EncodeToString(value[:])
}
