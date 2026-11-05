package images

import (
	"crypto/sha256"
	"encoding/hex"
)

// Tag changes whenever the input artwork bytes change.
func Tag(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:16]) }
