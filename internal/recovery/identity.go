package recovery

import (
	"crypto/sha256"
	"encoding/hex"
)

// ROMIdentity captures provenance and cryptographic identities of a ROM.
type ROMIdentity struct {
	OriginalSHA256   string `json:"original_sha256"`
	OriginalSize     int64  `json:"original_size"`
	NormalizedSHA256 string `json:"normalized_sha256"`
	NormalizedSize   int64  `json:"normalized_size"`
	Normalization    string `json:"normalization"`
	Mapper           string `json:"mapper"`
}

// ComputeROMIdentity computes the identity of original and normalized ROM bytes.
func ComputeROMIdentity(original, normalized []byte, normOp string, mapper string) ROMIdentity {
	origSum := sha256.Sum256(original)
	normSum := sha256.Sum256(normalized)
	return ROMIdentity{
		OriginalSHA256:   hex.EncodeToString(origSum[:]),
		OriginalSize:     int64(len(original)),
		NormalizedSHA256: hex.EncodeToString(normSum[:]),
		NormalizedSize:   int64(len(normalized)),
		Normalization:    normOp,
		Mapper:           mapper,
	}
}
