package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// ComputeProjectRevision computes a deterministic SHA-256 fingerprint over all artifacts in the project.
func ComputeProjectRevision(projectDir string, doc *Document) string {
	h := sha256.New()
	if doc != nil {
		h.Write([]byte("rom:" + doc.ROM.NormalizedSHA256 + "\n"))
	}

	hashFileIfExists := func(tag, path string) {
		if data, err := os.ReadFile(path); err == nil {
			sum := sha256.Sum256(data)
			fmt.Fprintf(h, "%s:%x\n", tag, sum)
		}
	}

	hashFileIfExists("recovery", filepath.Join(projectDir, "recovery.json"))
	hashFileIfExists("coverage", filepath.Join(projectDir, "coverage.json"))
	hashFileIfExists("watches", filepath.Join(projectDir, "watches.json"))
	hashFileIfExists("snapshots_json", filepath.Join(projectDir, "snapshots.json"))
	hashFileIfExists("snapshots_jsonl", filepath.Join(projectDir, "snapshots.jsonl"))
	hashFileIfExists("frames_jsonl", filepath.Join(projectDir, "frames", "frames.jsonl"))
	hashFileIfExists("frames_manifest", filepath.Join(projectDir, "frames", "manifest.jsonl"))
	hashFileIfExists("frames_receipt", filepath.Join(projectDir, "frames", "frames.receipt.json"))

	snapDir := filepath.Join(projectDir, "snapshots")
	if entries, err := os.ReadDir(snapDir); err == nil {
		var names []string
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			hashFileIfExists("snap_"+name, filepath.Join(snapDir, name))
		}
	}

	recoveryPath := filepath.Join(projectDir, "recovery.json")
	if _, err := os.Stat(recoveryPath); os.IsNotExist(err) && doc != nil {
		for _, inst := range doc.Instructions {
			fmt.Fprintf(h, "inst:%s:%06x:%s\n", inst.ID, inst.Address, inst.Bytes)
		}
		for _, edge := range doc.Edges {
			fmt.Fprintf(h, "edge:%s:%s:%06x\n", edge.ID, edge.Kind, edge.Destination)
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}
