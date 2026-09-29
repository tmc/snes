package recovery

import (
	"errors"
	"fmt"
	"io"
)

// AdmissionOptions configures ROM admission requirements.
type AdmissionOptions struct {
	// AllowCopierHeader permits stripping a leading 512-byte copier header.
	AllowCopierHeader bool
}

// AdmittedROM contains the validated original and normalized ROM data.
type AdmittedROM struct {
	Identity      ROMIdentity
	NormalizedROM []byte
	OriginalROM   []byte
}

var (
	// ErrROMTooSmall indicates the ROM is smaller than the minimum 32 KiB size.
	ErrROMTooSmall = errors.New("rom too small: minimum size is 32 KiB")
	// ErrCopierHeaderRejected indicates a 512-byte header was found but not allowed.
	ErrCopierHeaderRejected = errors.New("rom has 512-byte copier header: stripping not permitted")
	// ErrInvalidSize indicates the ROM size is not a valid multiple.
	ErrInvalidSize = errors.New("rom size is invalid: must be a multiple of 1024 bytes")
	// ErrUnsupportedMapper indicates the ROM header indicates an unsupported mapper.
	ErrUnsupportedMapper = errors.New("unsupported mapper: only LoROM is currently admitted")
)

// AdmitROM admits and validates a ROM image from r.
func AdmitROM(r io.Reader, opts AdmissionOptions) (*AdmittedROM, error) {
	if r == nil {
		return nil, errors.New("recovery: reader is nil")
	}
	return admitROM(r, opts)
}

func admitROM(r io.Reader, opts AdmissionOptions) (*AdmittedROM, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("recovery: read rom: %w", err)
	}

	rawLen := len(raw)
	if rawLen < 32*1024 {
		return nil, fmt.Errorf("recovery: %w", ErrROMTooSmall)
	}

	normOp := "none"
	normalized := raw

	if rawLen%1024 == 512 {
		if !opts.AllowCopierHeader {
			return nil, fmt.Errorf("recovery: %w", ErrCopierHeaderRejected)
		}
		normOp = "copier_header_removed"
		normalized = raw[512:]
	} else if rawLen%1024 != 0 {
		return nil, fmt.Errorf("recovery: %w", ErrInvalidSize)
	}

	normLen := len(normalized)
	if normLen < 32*1024 || normLen%(32*1024) != 0 {
		return nil, fmt.Errorf("recovery: %w", ErrInvalidSize)
	}

	// Validate LoROM header mode ($7FC0 + $15).
	headerOffset := 0x7FC0
	if normLen < headerOffset+0x20 {
		return nil, fmt.Errorf("recovery: %w", ErrROMTooSmall)
	}
	mapMode := normalized[headerOffset+0x15] & 0x0F
	// 0: LoROM, 2: LoROM + S-DD1 (or fastrom 0x20/0x30). Mode nibble 0 is standard LoROM.
	if mapMode != 0x00 {
		return nil, fmt.Errorf("recovery: map mode 0x%02x: %w", normalized[headerOffset+0x15], ErrUnsupportedMapper)
	}

	ident := ComputeROMIdentity(raw, normalized, normOp, "lorom")

	return &AdmittedROM{
		Identity:      ident,
		NormalizedROM: normalized,
		OriginalROM:   raw,
	}, nil
}
