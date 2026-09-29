package recovery_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
)

// makeSyntheticLoROM creates a valid LoROM byte buffer of the given size in KiB.
func makeSyntheticLoROM(sizeKiB int) []byte {
	rom := make([]byte, sizeKiB*1024)
	for i := range rom {
		rom[i] = 0xEA // NOP
	}

	headerOffset := 0x7FC0
	copy(rom[headerOffset:headerOffset+21], []byte("SYNTHETIC LOROM TEST "))
	rom[headerOffset+0x15] = 0x20 // LoROM
	rom[headerOffset+0x19] = 0x08 // 512KB indicator

	var sum uint16
	for _, b := range rom {
		sum += uint16(b)
	}

	compOffset := headerOffset + 0x1C
	binary.LittleEndian.PutUint16(rom[compOffset:], ^sum)
	binary.LittleEndian.PutUint16(rom[compOffset+2:], sum)

	return rom
}

func TestAdmitROM(t *testing.T) {
	t.Run("PlainLoROM", func(t *testing.T) {
		rom := makeSyntheticLoROM(64)
		admitted, err := recovery.AdmitROM(bytes.NewReader(rom), recovery.AdmissionOptions{})
		if err != nil {
			t.Fatalf("AdmitROM failed: %v", err)
		}
		if admitted.Identity.Normalization != "none" {
			t.Errorf("got normalization %q, want 'none'", admitted.Identity.Normalization)
		}
		if admitted.Identity.NormalizedSize != int64(len(rom)) {
			t.Errorf("got normalized size %d, want %d", admitted.Identity.NormalizedSize, len(rom))
		}
		if admitted.Identity.Mapper != "lorom" {
			t.Errorf("got mapper %q, want 'lorom'", admitted.Identity.Mapper)
		}
	})

	t.Run("CopierHeaderStripped", func(t *testing.T) {
		rom := makeSyntheticLoROM(64)
		withHeader := append(make([]byte, 512), rom...)

		// Without option: must fail.
		_, err := recovery.AdmitROM(bytes.NewReader(withHeader), recovery.AdmissionOptions{AllowCopierHeader: false})
		if err == nil {
			t.Fatal("expected error for unpermitted copier header, got nil")
		}

		// With option: must succeed and strip.
		admitted, err := recovery.AdmitROM(bytes.NewReader(withHeader), recovery.AdmissionOptions{AllowCopierHeader: true})
		if err != nil {
			t.Fatalf("AdmitROM failed: %v", err)
		}
		if admitted.Identity.Normalization != "copier_header_removed" {
			t.Errorf("got normalization %q, want 'copier_header_removed'", admitted.Identity.Normalization)
		}
		if admitted.Identity.NormalizedSize != int64(len(rom)) {
			t.Errorf("got normalized size %d, want %d", admitted.Identity.NormalizedSize, len(rom))
		}
		if admitted.Identity.OriginalSize != int64(len(withHeader)) {
			t.Errorf("got original size %d, want %d", admitted.Identity.OriginalSize, len(withHeader))
		}
	})

	t.Run("InvalidSizeRejected", func(t *testing.T) {
		short := make([]byte, 1024)
		if _, err := recovery.AdmitROM(bytes.NewReader(short), recovery.AdmissionOptions{}); err == nil {
			t.Error("expected error for small ROM, got nil")
		}

		odd := make([]byte, 33*1024)
		if _, err := recovery.AdmitROM(bytes.NewReader(odd), recovery.AdmissionOptions{}); err == nil {
			t.Error("expected error for odd-sized ROM, got nil")
		}
	})

	t.Run("NilReader", func(t *testing.T) {
		if _, err := recovery.AdmitROM(nil, recovery.AdmissionOptions{}); err == nil {
			t.Error("expected error for nil reader, got nil")
		}
	})
}

func TestDocument(t *testing.T) {
	t.Run("RoundTripJSON", func(t *testing.T) {
		ident := recovery.ROMIdentity{
			OriginalSHA256:   "abcd",
			OriginalSize:     65536,
			NormalizedSHA256: "abcd",
			NormalizedSize:   65536,
			Normalization:    "none",
			Mapper:           "lorom",
		}
		doc := recovery.NewDocument(ident)
		doc.Issues = append(doc.Issues, recovery.Issue{
			ID:       "iss-1",
			Offset:   0x8000,
			Address:  0x808000,
			Reason:   "unresolved branch target",
			Blocking: false,
		})

		var buf bytes.Buffer
		if err := recovery.Encode(&buf, doc); err != nil {
			t.Fatalf("Encode failed: %v", err)
		}

		decoded, err := recovery.Decode(&buf)
		if err != nil {
			t.Fatalf("Decode failed: %v", err)
		}

		if decoded.Format != recovery.Format {
			t.Errorf("got format %q, want %q", decoded.Format, recovery.Format)
		}
		if decoded.Schema != recovery.SchemaVersion {
			t.Errorf("got schema %d, want %d", decoded.Schema, recovery.SchemaVersion)
		}
		if len(decoded.Issues) != 1 || decoded.Issues[0].ID != "iss-1" {
			t.Errorf("issues mismatch: %+v", decoded.Issues)
		}
	})

	t.Run("RejectUnknownFields", func(t *testing.T) {
		badJSON := `{
			"format": "snes-recovery",
			"schema": 1,
			"rom": {},
			"producer": {"tool": "test", "version": "1"},
			"evidence": [],
			"instructions": [],
			"edges": [],
			"objects": [],
			"issues": [],
			"unknown_field": 123
		}`
		if _, err := recovery.Decode(strings.NewReader(badJSON)); err == nil {
			t.Error("expected error on unknown field, got nil")
		}
	})

	t.Run("RejectInvalidSchema", func(t *testing.T) {
		badJSON := `{
			"format": "snes-recovery",
			"schema": 99,
			"rom": {},
			"producer": {"tool": "test", "version": "1"},
			"evidence": [],
			"instructions": [],
			"edges": [],
			"objects": [],
			"issues": []
		}`
		if _, err := recovery.Decode(strings.NewReader(badJSON)); err == nil {
			t.Error("expected error on unsupported schema, got nil")
		}
	})

	t.Run("RejectTrailingData", func(t *testing.T) {
		badJSON := `{
			"format": "snes-recovery",
			"schema": 1,
			"rom": {},
			"producer": {"tool": "test", "version": "1"},
			"evidence": [],
			"instructions": [],
			"edges": [],
			"objects": [],
			"issues": []
		} {"trailing": true}`
		if _, err := recovery.Decode(strings.NewReader(badJSON)); err == nil {
			t.Error("expected error on trailing JSON data, got nil")
		}
	})
}
