package watches

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Format constants.
const (
	FileFormatWatches  = "snes-game-state-watches"
	FileFormatSnapshot = "snes-wram-snapshot"
	CurrentSchema      = 1
)

// ValidityState describes the validity of an evaluated watch observation.
type ValidityState string

const (
	ValidityValid            ValidityState = "valid"
	ValidityInvalidInContext ValidityState = "invalid-in-context"
	ValidityMissing          ValidityState = "missing"
	ValidityUnknown          ValidityState = "unknown-validity"
)

// File holds the persisted watch definitions for a ROM.
type File struct {
	Format        string            `json:"format"`
	SchemaVersion int               `json:"schema_version"`
	ROMSHA256     string            `json:"rom_sha256"`
	Watches       []WatchDefinition `json:"watches"`
}

// WatchDefinition specifies how to extract and interpret a game state value
// from physical memory.
type WatchDefinition struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	MemorySpace      string            `json:"memory_space"` // e.g. "wram"
	Offset           uint32            `json:"offset"`       // physical offset ($00000..$1FFFF for WRAM)
	Width            int               `json:"width"`        // 1, 2, or 3 bytes
	ByteOrder        string            `json:"byte_order,omitempty"` // "little" or "big" (default "little")
	Signed           bool              `json:"signed,omitempty"`
	Mask             *uint32           `json:"mask,omitempty"`
	Shift            int               `json:"shift,omitempty"`
	ScaleNumerator   *int64            `json:"scale_numerator,omitempty"`
	ScaleDenominator *int64            `json:"scale_denominator,omitempty"`
	Unit             string            `json:"unit,omitempty"`
	EnumLabels       map[string]string `json:"enum_labels,omitempty"`
	MeaningSource    string            `json:"meaning_source,omitempty"` // "hypothesis", "annotation", "external"
	Evidence         []string          `json:"evidence,omitempty"`
	CPUAddress       *uint32           `json:"cpu_address,omitempty"` // display metadata only
	Conditions       []Condition       `json:"conditions,omitempty"`
}

// Condition expresses a typed comparison on raw/masked values.
type Condition struct {
	WatchID      string  `json:"watch_id"`
	Op           string  `json:"op"` // "==", "!=", "<", "<=", ">", ">="
	Value        *int64  `json:"value,omitempty"`
	OtherWatchID *string `json:"other_watch_id,omitempty"`
}

// Snapshot represents an immutable memory capture at a specific frame boundary.
type Snapshot struct {
	Format         string `json:"format"`
	SchemaVersion  int    `json:"schema_version"`
	RunID          string `json:"run_id"`
	ROMSHA256      string `json:"rom_sha256"`
	MemorySpace    string `json:"memory_space"`
	BaseOffset     uint32 `json:"base_offset"`
	Length         uint32 `json:"length"`
	Sequence       uint64 `json:"sequence"`
	Frame          uint64 `json:"frame"`
	EmulatedTimeNs uint64 `json:"emulated_time_ns,omitempty"`
	Boundary       string `json:"boundary,omitempty"` // e.g. "vblank", "safe_frame"
	ContentSHA256  string `json:"content_sha256"`
	Data           []byte `json:"data"` // raw bytes of length Length
}

// UnmarshalJSON unmarshals a Snapshot, supporting Data encoded as base64, hex, or byte array.
func (s *Snapshot) UnmarshalJSON(b []byte) error {
	type Alias Snapshot
	aux := struct {
		*Alias
		RawData json.RawMessage `json:"data"`
	}{
		Alias: (*Alias)(s),
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if len(aux.RawData) > 0 {
		var str string
		if err := json.Unmarshal(aux.RawData, &str); err == nil {
			// Try hex first if even length and hex characters
			if h, err := hex.DecodeString(str); err == nil && len(h) > 0 {
				s.Data = h
			} else if decoded, err := base64.StdEncoding.DecodeString(str); err == nil {
				s.Data = decoded
			} else {
				s.Data = []byte(str)
			}
		} else {
			var byteArr []byte
			if err := json.Unmarshal(aux.RawData, &byteArr); err == nil {
				s.Data = byteArr
			}
		}
	}
	return nil
}

// Evaluation records the evaluated state of a watch against a snapshot.
type Evaluation struct {
	WatchID        string        `json:"watch_id"`
	Name           string        `json:"name"`
	MemorySpace    string        `json:"memory_space"`
	Offset         uint32        `json:"offset"`
	CPUAddress     *uint32       `json:"cpu_address,omitempty"`
	Validity       ValidityState `json:"validity"`
	ValidityReason string        `json:"validity_reason,omitempty"`
	RawBytes       []byte        `json:"raw_bytes,omitempty"`
	RawValue       uint32        `json:"raw_value,omitempty"`
	MaskedValue    uint32        `json:"masked_value,omitempty"`
	EffectiveBits  int           `json:"effective_bits,omitempty"`
	DecodedNumber  float64       `json:"decoded_number,omitempty"`
	DecodedString  string        `json:"decoded_string,omitempty"`
	EnumLabel      string        `json:"enum_label,omitempty"`
	Unit           string        `json:"unit,omitempty"`
	MeaningSource  string        `json:"meaning_source,omitempty"`
	SnapshotFrame  uint64        `json:"snapshot_frame"`
	SnapshotRunID  string        `json:"snapshot_run_id"`
	DefinitionHash string        `json:"definition_hash"`
}

// HistoryEntry records a sampled history point or interval for a watch.
type HistoryEntry struct {
	RunID          string     `json:"run_id"`
	FrameStart     uint64     `json:"frame_start"`
	FrameEnd       uint64     `json:"frame_end"` // half-open interval [FrameStart, FrameEnd)
	SequenceStart  uint64     `json:"sequence_start"`
	SequenceEnd    uint64     `json:"sequence_end"`
	Evaluation     Evaluation `json:"evaluation"`
	Changed        bool       `json:"changed"` // true if differs from previous sample
	Gap            bool       `json:"gap"`     // true if an unobserved gap preceded this
}

// Hash returns a deterministic SHA-256 hash of a watch definition.
func (w *WatchDefinition) Hash() string {
	b, _ := json.Marshal(w)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Validate checks the watch definition for consistency.
func (w *WatchDefinition) Validate() error {
	if w.ID == "" {
		return fmt.Errorf("watches: empty watch ID")
	}
	if w.MemorySpace != "wram" {
		return fmt.Errorf("watches: unsupported memory space %q (only %q supported)", w.MemorySpace, "wram")
	}
	if w.Width < 1 || w.Width > 3 {
		return fmt.Errorf("watches: invalid width %d (must be 1, 2, or 3)", w.Width)
	}
	// WRAM physical offset bounds: 128KB ($00000..$1FFFF)
	const maxWRAM = 128 * 1024
	if uint64(w.Offset)+uint64(w.Width) > maxWRAM {
		return fmt.Errorf("watches: offset 0x%05x + width %d exceeds WRAM bounds 0x%05x", w.Offset, w.Width, maxWRAM)
	}
	if w.ByteOrder != "" && w.ByteOrder != "little" && w.ByteOrder != "big" {
		return fmt.Errorf("watches: invalid byte order %q", w.ByteOrder)
	}
	if w.ScaleDenominator != nil && *w.ScaleDenominator == 0 {
		return fmt.Errorf("watches: scale denominator cannot be 0")
	}
	maxVal := (uint64(1) << (w.Width * 8)) - 1
	if w.Mask != nil && uint64(*w.Mask) > maxVal {
		return fmt.Errorf("watches: mask 0x%x exceeds width %d bytes", *w.Mask, w.Width)
	}
	if w.Shift < 0 || w.Shift >= w.Width*8 {
		return fmt.Errorf("watches: invalid shift %d for width %d", w.Shift, w.Width)
	}
	for _, c := range w.Conditions {
		if c.WatchID == "" {
			return fmt.Errorf("watches: condition missing watch_id")
		}
		switch c.Op {
		case "==", "!=", "<", "<=", ">", ">=":
		default:
			return fmt.Errorf("watches: invalid condition op %q", c.Op)
		}
		if c.Value == nil && c.OtherWatchID == nil {
			return fmt.Errorf("watches: condition must specify either value or other_watch_id")
		}
	}
	return nil
}
