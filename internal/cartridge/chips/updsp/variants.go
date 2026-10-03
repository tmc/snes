package updsp

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Variant identifies one of the six DSP coprocessor program ROMs used by
// SNES cartridges. The variants differ in program ROM content only; the
// uPD77C25 execution core is identical.
type Variant uint8

const (
	VariantUnknown Variant = iota
	VariantDSP1
	VariantDSP1A
	VariantDSP1B
	VariantDSP2
	VariantDSP3
	VariantDSP4
)

// String returns the short name of the variant.
func (v Variant) String() string {
	switch v {
	case VariantDSP1:
		return "DSP-1"
	case VariantDSP1A:
		return "DSP-1A"
	case VariantDSP1B:
		return "DSP-1B"
	case VariantDSP2:
		return "DSP-2"
	case VariantDSP3:
		return "DSP-3"
	case VariantDSP4:
		return "DSP-4"
	default:
		return "unknown"
	}
}

// ROMSize returns the expected size in bytes of a variant's program ROM
// image (24-bit words packed big-endian, 2048 words) plus data ROM (16-bit
// words packed big-endian, 1024 words).
func (v Variant) ROMSize() (prg, drom int) {
	// uPD77C25 common layout: 2048×24-bit program, 1024×16-bit data.
	return 2048 * 3, 1024 * 2
}

// Loader constructs a Core and IO with program + data ROM loaded. It returns
// an error for invalid variants or sizes. Size validation does not identify
// firmware contents: callers must select the appropriate variant.
type Loader struct {
	Variant  Variant
	Core     *Core
	IO       *IO
	LoadedOK bool
}

// Load constructs a [Loader] from separate program and data ROM byte buffers.
// Both buffers are required for execution. Passing both as nil explicitly
// constructs a diagnostic loader with LoadedOK=false. A single missing buffer
// returns ErrROMMissing.
func Load(variant Variant, progROM, dataROM []byte) (*Loader, error) {
	if variant < VariantDSP1 || variant > VariantDSP4 {
		return nil, fmt.Errorf("updsp: unknown variant")
	}
	core := NewCore()
	io := NewIO(core)
	l := &Loader{Variant: variant, Core: core, IO: io}
	if progROM == nil && dataROM == nil {
		return l, nil
	}
	if progROM == nil || dataROM == nil {
		return nil, ErrROMMissing
	}
	wantPrg, wantData := variant.ROMSize()
	if progROM != nil {
		if len(progROM) != wantPrg {
			return l, fmt.Errorf("updsp: %s program rom is %d bytes, want %d", variant, len(progROM), wantPrg)
		}
		if err := core.LoadProgramROM(progROM); err != nil {
			return l, fmt.Errorf("updsp: load %s program: %w", variant, err)
		}
	}
	if dataROM != nil {
		if len(dataROM) != wantData {
			return l, fmt.Errorf("updsp: %s data rom is %d bytes, want %d", variant, len(dataROM), wantData)
		}
		if err := core.LoadDataROM(dataROM); err != nil {
			return l, fmt.Errorf("updsp: load %s data: %w", variant, err)
		}
	}
	l.LoadedOK = progROM != nil
	return l, nil
}

// ErrROMMissing is returned by [LoadWithEnv] when the caller explicitly asks
// for a variant but no ROM image is available. The caller's [Load] path
// should format a game-facing message such as
// "DSP-1 ROM not provided, this cartridge will not play correctly".
var ErrROMMissing = fmt.Errorf("updsp: program rom not provided")

// LoadWithEnv loads a variant from environment-provided firmware. A combined
// ROM named SNES_DSP1_ROM may contain the 0x1800-byte program image followed by
// the 0x0800-byte data image, matching bsnes' uPD7725 firmware layout. Separate
// SNES_DSP1_PROGRAM_ROM and SNES_DSP1_DATA_ROM paths are also accepted.
func LoadWithEnv(variant Variant) (*Loader, error) {
	if variant < VariantDSP1 || variant > VariantDSP4 {
		return nil, fmt.Errorf("updsp: unknown variant")
	}
	base := envVariantName(variant)
	combinedPath := os.Getenv("SNES_" + base + "_ROM")
	progPath := os.Getenv("SNES_" + base + "_PROGRAM_ROM")
	dataPath := os.Getenv("SNES_" + base + "_DATA_ROM")

	wantPrg, wantData := variant.ROMSize()
	if combinedPath != "" {
		rom, err := readFirmware(combinedPath, wantPrg+wantData)
		if err != nil {
			return nil, fmt.Errorf("updsp: read %s rom: %w", variant, err)
		}
		if len(rom) != wantPrg+wantData {
			return nil, fmt.Errorf("updsp: %s combined rom is %d bytes, want %d", variant, len(rom), wantPrg+wantData)
		}
		return Load(variant, rom[:wantPrg], rom[wantPrg:])
	}
	if progPath == "" && dataPath == "" {
		return nil, ErrROMMissing
	}
	if progPath == "" {
		return nil, ErrROMMissing
	}
	progROM, err := readFirmware(progPath, wantPrg)
	if err != nil {
		return nil, fmt.Errorf("updsp: read %s program rom: %w", variant, err)
	}
	var dataROM []byte
	if dataPath != "" {
		dataROM, err = readFirmware(dataPath, wantData)
		if err != nil {
			return nil, fmt.Errorf("updsp: read %s data rom: %w", variant, err)
		}
	}
	return Load(variant, progROM, dataROM)
}

func readFirmware(path string, size int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, int64(size)+1))
}

func envVariantName(variant Variant) string {
	name := strings.ToUpper(variant.String())
	name = strings.ReplaceAll(name, "-", "")
	return name
}
