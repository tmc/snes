package cartridge

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge/chips/cx4"
	"github.com/tmc/snes/internal/cartridge/chips/dsp1"
	"github.com/tmc/snes/internal/cartridge/chips/gsu"
	"github.com/tmc/snes/internal/cartridge/chips/obc1"
	"github.com/tmc/snes/internal/cartridge/chips/sa1"
	"github.com/tmc/snes/internal/cartridge/chips/srtc"
	"github.com/tmc/snes/internal/cartridge/chips/updsp"
)

// ErrUnsupportedCoprocessor signals that a cartridge declares a
// coprocessor whose execution is not implemented. The current
// emulator supports loading the ROM but cannot run it correctly,
// so System-level loaders should fail closed rather than silently
// produce wrong output. ST-018 (SETA ARM6) cartridges trigger this
// per implementation_plan.md:495 + 507; bsnes heuristic at
// heuristics/super-famicom.cpp:101-104, 303 (cartridge type-hi 0xF
// + subtype 0x02 in the SFC header at offset 0x0F).
var ErrUnsupportedCoprocessor = errors.New("unsupported coprocessor")

// Cartridge represents a SNES game cartridge.
type Cartridge struct {
	ROM []byte
	RAM []byte

	// Size info
	ROMSize int
	RAMSize int

	// Mapping
	Mode MappingMode
	PAL  bool

	CoprocessorID string
	coprocessor   Coprocessor
	obc1          *obc1.Device // SRAM-window indirection for OBC-1 carts; nil otherwise.
	irqTarget     interface{ TriggerIRQ() }
	gsuIRQLine    bool
	sa1IRQLine    bool
}

type MappingMode int

const (
	LoROM MappingMode = iota
	HiROM
	ExLoROM
	ExHiROM
)

const (
	loROMHeader   = 0x7FC0
	hiROMHeader   = 0xFFC0
	exLoROMHeader = 0x407FC0
	exHiROMHeader = 0x40FFC0
)

type header struct {
	mapMode uint8
	chipset uint8
	subtype uint8 // SFC ARM/SETA chip subtype at header offset 0x0F
	ramSize uint8
	region  uint8
}

func headerAt(rom []byte, base int) (header, bool) {
	if base < 0 || base+0x1F >= len(rom) {
		return header{}, false
	}
	return header{
		mapMode: rom[base+0x15],
		chipset: rom[base+0x16],
		subtype: rom[base+0x0F],
		ramSize: rom[base+0x18],
		region:  rom[base+0x19],
	}, true
}

func scoreHeader(rom []byte, base int, want MappingMode) int {
	h, ok := headerAt(rom, base)
	if !ok {
		return -100
	}
	score := 0
	mode := h.mapMode & 0x0F
	switch want {
	case LoROM:
		if mode == 0x0 || mode == 0x2 {
			score += 6
		}
	case HiROM:
		if mode == 0x1 || mode == 0x3 {
			score += 6
		}
	case ExHiROM:
		if mode == 0x5 {
			score += 8
		}
	}

	// Valid complement checksum pair is a good hint.
	if base+0x1F < len(rom) {
		sum := uint16(rom[base+0x1E]) | (uint16(rom[base+0x1F]) << 8)
		csum := uint16(rom[base+0x1C]) | (uint16(rom[base+0x1D]) << 8)
		if sum^csum == 0xFFFF {
			score += 4
		}
	}

	// Reasonable RAM size nibble.
	if h.ramSize <= 8 {
		score++
	}
	return score
}

func detectMappingMode(rom []byte) MappingMode {
	lo := scoreHeader(rom, loROMHeader, LoROM)
	hi := scoreHeader(rom, hiROMHeader, HiROM)
	exlo := scoreHeader(rom, exLoROMHeader, LoROM)
	exhi := scoreHeader(rom, exHiROMHeader, ExHiROM)
	if exlo > -100 {
		exlo += 4
	}
	if exhi > hi && exhi > lo {
		if exhi >= exlo {
			return ExHiROM
		}
	}
	if exlo > hi && exlo > lo {
		return ExLoROM
	}
	if exhi > hi && exhi > lo {
		return ExHiROM
	}
	if hi > lo {
		return HiROM
	}
	return LoROM
}

func detectRAMSize(rom []byte, mode MappingMode) int {
	base := loROMHeader
	if mode == HiROM {
		base = hiROMHeader
	}
	if mode == ExLoROM {
		base = exLoROMHeader
	}
	if mode == ExHiROM {
		base = exHiROMHeader
	}
	h, ok := headerAt(rom, base)
	if !ok {
		return 32 * 1024
	}
	if h.ramSize == 0 || h.ramSize > 8 {
		return 0
	}
	// Header RAM size encoding is power-of-two bytes, where 5 = 32KiB.
	size := 1024 << h.ramSize
	if size < 0 {
		return 0
	}
	return size
}

func detectCoprocessor(rom []byte, mode MappingMode) string {
	base := loROMHeader
	if mode == HiROM {
		base = hiROMHeader
	}
	if mode == ExLoROM {
		base = exLoROMHeader
	}
	if mode == ExHiROM {
		base = exHiROMHeader
	}
	h, ok := headerAt(rom, base)
	if !ok {
		return ""
	}
	if h.chipset>>4 == 0x03 && h.chipset&0x0f >= 3 {
		return "sa1"
	}
	// Cx4 / Hitachi HG51BS169. Custom-chip headers use type-hi 0xF
	// with subtype 0x10 in bsnes and subtype 0x03 in the SNESdev ROM
	// header table. Explicit subtype detection precedes the DSP-family map-mode predicate.
	if h.chipset>>4 == 0x0F && (h.subtype == 0x10 || h.subtype == 0x03) {
		return "cx4"
	}
	if h.chipset&0x0f >= 3 {
		switch h.chipset >> 4 {
		case 4:
			return "sdd1"
		case 0xe:
			return "supergameboy"
		case 0xf:
			if h.subtype == 1 {
				return "st01x"
			}
		}
	}
	// Common encoding for DSP-1 carts uses map mode nibble 0x3.
	if (h.mapMode & 0x0F) == 0x03 {
		return "dsp1"
	}
	if h.chipset>>4 == 0x01 {
		return "gsu"
	}
	// OBC-1 (SETA OAM-indirection) per bsnes heuristic
	// (heuristics/super-famicom.cpp:292+295): cartridge type-hi
	// nibble 0x2 with lo-nibble >= 0x3. The chip's body is in
	// internal/cartridge/chips/obc1 and the cartridge SRAM read/
	// write path routes through it when CoprocessorID == "obc1".
	// The lo-nibble >= 3 gate matches bsnes's outer guard at
	// super-famicom.cpp:292; it is applied here only for OBC-1
	// because GSU's existing detection (above) intentionally keeps
	// the simpler high-nibble-only check to avoid churning ROMs
	// that have already been validated against the looser predicate.
	if h.chipset>>4 == 0x02 && h.chipset&0x0F >= 0x03 {
		return "obc1"
	}
	// S-RTC / Sharp RTC per bsnes heuristic
	// (heuristics/super-famicom.cpp:298+310): cartridge type-hi
	// nibble 0x5 with lo-nibble >= 0x3 outer gate.
	// Body in internal/cartridge/chips/srtc.
	// The chip implements the cartridge Coprocessor interface and
	// is dispatched via cart.coprocessor.Read/Write at $2800/$2801.
	if h.chipset>>4 == 0x05 && h.chipset&0x0F >= 0x03 {
		return "srtc"
	}
	// SPC7110 per bsnes heuristic (heuristics/super-famicom.cpp:
	// 300-301): chipset 0xF5 (no RTC) or 0xF9 (with Epson RTC) with
	// subtype 0x00. Detection only — execution (DCU
	// decompressor + dataport + MCU + EpsonRTC) is not implemented;
	// System.LoadROM converts this ID into ErrUnsupportedCoprocessor.
	// The ALU sub-unit at $4820-$482F is bounded but not game-
	// observable on its own (every known SPC7110 game invokes the
	// DCU first for graphics decompression), so the stub keeps the
	// fail-closed contract until the full chip lands.
	if h.chipset>>4 == 0x0F && h.chipset&0x0F >= 0x03 && (h.chipset == 0xF5 || h.chipset == 0xF9) && h.subtype == 0x00 {
		return "spc7110"
	}
	// ST-018 (SETA ARM6) per bsnes heuristic
	// (super-famicom.cpp:101-104, 303): cartridge type-hi 0xF +
	// subtype 0x02. Detected
	// only — execution is not implemented; System.LoadROM converts
	// this ID into ErrUnsupportedCoprocessor.
	if h.chipset>>4 == 0x0F && h.subtype == 0x02 {
		return "st018"
	}
	return ""
}

func detectPAL(rom []byte, mode MappingMode) bool {
	base := loROMHeader
	switch mode {
	case HiROM:
		base = hiROMHeader
	case ExLoROM:
		base = exLoROMHeader
	case ExHiROM:
		base = exHiROMHeader
	}
	h, ok := headerAt(rom, base)
	if !ok {
		return false
	}
	switch h.region {
	case 0x00, 0x01, 0x0D, 0x0F, 0x10:
		return false
	default:
		return true
	}
}

func detectDSP1MapType(mode MappingMode, romSize int) dsp1.MapType {
	if mode == HiROM {
		return dsp1.MapHiROM
	}
	if romSize > 0x100000 {
		return dsp1.MapLoROMLarge
	}
	return dsp1.MapLoROMSmall
}

// ValidateHeader checks the header before cartridge allocation. RAM encodings
// 1 through 8 describe 2 KiB through 256 KiB; zero means no RAM. The maximum
// matches bsnes heuristics/super-famicom.cpp ramSize, but invalid encodings
// are rejected rather than clamped.
func ValidateHeader(data []byte) error {
	if len(data) > 512 && len(data)&0x7fff == 512 {
		data = data[512:]
	}
	mode := detectMappingMode(data)
	base := loROMHeader
	switch mode {
	case HiROM:
		base = hiROMHeader
	case ExLoROM:
		base = exLoROMHeader
	case ExHiROM:
		base = exHiROMHeader
	}
	h, ok := headerAt(data, base)
	if !ok {
		return fmt.Errorf("cartridge header is truncated")
	}
	if h.ramSize > 8 {
		return fmt.Errorf("cartridge ram encoding %d exceeds supported maximum 8", h.ramSize)
	}
	return nil
}

// New constructs a cartridge using its standard hardware header.
func New(data []byte) *Cartridge {
	return newCartridge(data, "")
}

// NewWithCoprocessor constructs a cartridge with an explicit hardware profile.
// An empty id uses header detection. The caller supplies board information that
// an ambiguous header cannot establish; no game title or digest is consulted.
func NewWithCoprocessor(data []byte, id string) (*Cartridge, error) {
	switch id {
	case "", "dsp1", "gsu", "sa1", "obc1", "srtc", "cx4", "st018", "spc7110", "sdd1", "st01x", "supergameboy":
	default:
		return nil, fmt.Errorf("unknown coprocessor %q", id)
	}
	return newCartridge(data, id), nil
}

func newCartridge(data []byte, id string) *Cartridge {
	rom := append([]byte(nil), data...)
	if len(rom) > 512 && (len(rom)&0x7fff) == 512 {
		rom = append([]byte(nil), rom[512:]...)
	}
	mode := detectMappingMode(rom)
	ramSize := detectRAMSize(rom, mode)
	cart := &Cartridge{
		ROM:     rom,
		ROMSize: len(rom),
		RAMSize: ramSize,
		Mode:    mode,
		PAL:     detectPAL(rom, mode),
	}
	if cart.RAMSize > 0 {
		cart.RAM = make([]byte, cart.RAMSize)
	}
	cart.CoprocessorID = id
	if id == "" {
		cart.CoprocessorID = detectCoprocessor(rom, mode)
	}
	switch cart.CoprocessorID {
	case "dsp1":
		dev := dsp1.New()
		dev.SetMapType(detectDSP1MapType(mode, len(rom)))
		cart.coprocessor = dev
	case "gsu":
		dev := gsu.New(cart.ROM, cart.RAM)
		cart.attachGSURAM(dev)
		cart.coprocessor = dev
	case "sa1":
		dev := sa1.New()
		dev.SetROMReader(cart.sa1VBRReader)
		dev.SetBWRAMSlice(cart.RAM)
		cart.coprocessor = dev
	case "obc1":
		// OBC-1 needs at least 8 KiB SRAM as its indirection buffer.
		// Allocate if the header declares less; cartridge.RAMSize
		// is left as the larger value so the SRAM window covers
		// the full chip working area.
		if cart.RAMSize < 0x2000 {
			cart.RAMSize = 0x2000
			cart.RAM = make([]byte, 0x2000)
		}
		dev := obc1.New()
		dev.SetRAM(cart.RAM)
		cart.obc1 = dev
	case "srtc":
		// Sharp RTC: pure MMIO chip, no SRAM-window dependence.
		// Production wires time.Now via the nil default; tests
		// can override via the device's SetClock after attach.
		dev := srtc.New(nil)
		dev.SyncTime()
		cart.coprocessor = dev
	case "cx4":
		cart.AttachCx4()
	}
	return cart
}

// sa1VBRReader implements the SA-1 variable-bit-read bus mux. bsnes
// memory.cpp:113-130 routes $230C/$230D reads through three regions:
//
//   - ROM at $00-3F:8000-FFFF + $80-BF:8000-FFFF + $C0-FF:0000-FFFF
//     (rom.readSA1 → readCPU; Device.CPUROMAddress produces the same
//     linear offset).
//   - I-RAM at $00-3F:0000-07FF + $00-3F:3000-37FF (with $80-BF
//     mirrors via mask 0x40f800), routed to iram.read raw (no
//     SIWP/CIWP gate, 2 KiB bus.mirror) — Device.ReadIRAMSA1 has
//     matching semantics.
//   - BW-RAM at $00-3F:6000-7FFF + $80-BF:6000-7FFF + $40-4F:0000-FFFF,
//     routed to bwram.read raw (full 24-bit bank+offset reduced via
//     bus.mirror to BW-RAM size; bsnes/sfc/coprocessor/sa1/bwram.cpp:9-13).
//     This is distinct from both Device.SA1BWRAMAddress (cbm/sw46
//     SA-1-CPU projection) and Cartridge.sa1BWRAMAddress (BMAPS
//     S-CPU page-mapped projection); $00:6000 and $40:0000 are NOT
//     aliases under the raw projection.
func (c *Cartridge) sa1VBRReader(addr uint32) uint8 {
	d, ok := c.coprocessor.(*sa1.Device)
	if !ok {
		return 0xFF
	}
	bank := (addr >> 16) & 0xff
	off := addr & 0xffff
	if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) &&
		(off < 0x0800 || (off >= 0x3000 && off <= 0x37FF)) {
		return d.ReadIRAMSA1(off)
	}
	if linOff, ok := c.sa1VBRBWRAMAddress(addr); ok {
		return c.RAM[linOff]
	}
	if len(c.ROM) == 0 {
		return 0xFF
	}
	pc, ok := d.CPUROMAddress(addr)
	if !ok {
		return 0xFF
	}
	return c.ROM[int(pc)%len(c.ROM)]
}

// sa1VectorOverride checks whether a CPU read at addr should be
// intercepted by the SA-1's S-CPU NMI/IRQ vector override per
// bsnes/sfc/coprocessor/sa1/rom.cpp:22-26. When the corresponding
// SCNT switch is set, $00:FFEA/EB return SNV-low/high and
// $00:FFEE/EF return SIV-low/high (with $80-BF mirrors).
//
// Out of scope (must NOT be intercepted): RESET ($00:FFFC),
// emulation-mode vectors ($00:FFFA-FFFF), and $C0-FF banks.
func (c *Cartridge) sa1VectorOverride(addr uint32) (uint8, bool) {
	if c.CoprocessorID != "sa1" {
		return 0, false
	}
	d, ok := c.coprocessor.(*sa1.Device)
	if !ok {
		return 0, false
	}
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if !(bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) {
		return 0, false
	}
	switch offset {
	case 0xFFEA:
		if d.SCPUNMIOverrideEnabled() {
			return uint8(d.SCPUNMIVector()), true
		}
	case 0xFFEB:
		if d.SCPUNMIOverrideEnabled() {
			return uint8(d.SCPUNMIVector() >> 8), true
		}
	case 0xFFEE:
		if d.SCPUIRQOverrideEnabled() {
			return uint8(d.SCPUIRQVector()), true
		}
	case 0xFFEF:
		if d.SCPUIRQOverrideEnabled() {
			return uint8(d.SCPUIRQVector() >> 8), true
		}
	}
	return 0, false
}

// sa1VBRBWRAMAddress maps a raw VBR-side BW-RAM access to a linear
// BW-RAM offset per bsnes/sfc/coprocessor/sa1/bwram.cpp:11
// (bus.mirror(address, size())). VBR ignores both the SA-1-side
// cbm/sw46 projection and the S-CPU BMAPS page-mapped projection —
// it reads the raw 24-bit bank+offset modulo BW-RAM size. The $80
// bank-mirror bit is stripped explicitly so $80-BF aliases $00-3F
// regardless of size; for power-of-2 sizes ≤ $80000 (the realistic
// SA-1 case) the bit is also cleared by the modulo naturally.
//
// Returns the linear BW-RAM offset and ok=true if addr falls in
// $00-3F:6000-7FFF, $80-BF:6000-7FFF, or $40-4F:0000-FFFF.
func (c *Cartridge) sa1VBRBWRAMAddress(addr uint32) (int, bool) {
	if c.RAMSize == 0 {
		return 0, false
	}
	bank := (addr >> 16) & 0xff
	off := addr & 0xffff
	inLowMirror := (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) &&
		off >= 0x6000 && off <= 0x7FFF
	inLinearWindow := bank >= 0x40 && bank <= 0x4F
	if !inLowMirror && !inLinearWindow {
		return 0, false
	}
	raw := uint32(bank&0x7F)<<16 | off
	return int(raw % uint32(c.RAMSize)), true
}

func (c *Cartridge) isSRAMAddress(bank, offset uint32) bool {
	if c.RAMSize == 0 {
		return false
	}
	if c.CoprocessorID == "sa1" {
		if ((bank <= 0x3F) || (bank >= 0x80 && bank <= 0xBF)) && offset >= 0x6000 && offset <= 0x7FFF {
			return true
		}
		return bank >= 0x40 && bank <= 0x4F
	}
	switch c.Mode {
	case LoROM:
		// GSU exposes the full $70-$71:0000-FFFF flat shared RAM bus
		// per ares/bsnes superfx/bus.cpp; non-GSU LoROM only uses the
		// lower half of $70-$7D as SRAM.
		if c.CoprocessorID == "gsu" && bank >= 0x70 && bank <= 0x71 {
			return true
		}
		if bank >= 0x70 && bank <= 0x7D && offset < 0x8000 {
			return true
		}
		if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset >= 0x6000 && offset <= 0x7FFF {
			return true
		}
		return false
	case ExLoROM:
		if bank >= 0x70 && bank <= 0x7D && offset < 0x8000 {
			return true
		}
		if (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) && offset >= 0x6000 && offset <= 0x7FFF {
			return true
		}
		return false
	case HiROM:
		return ((bank >= 0x20 && bank <= 0x3F) || (bank >= 0xA0 && bank <= 0xBF)) && offset >= 0x6000 && offset <= 0x7FFF
	case ExHiROM:
		return ((bank >= 0x20 && bank <= 0x3F) || (bank >= 0xA0 && bank <= 0xBF)) && offset >= 0x6000 && offset <= 0x7FFF
	default:
		return false
	}
}

func (c *Cartridge) sramAddress(bank, offset uint32) int {
	if c.RAMSize == 0 {
		return 0
	}
	if c.CoprocessorID == "sa1" {
		return int(c.sa1BWRAMAddress(bank, offset) % uint32(c.RAMSize))
	}
	var addr uint32
	switch c.Mode {
	case LoROM, ExLoROM:
		if c.CoprocessorID == "gsu" && bank >= 0x70 && bank <= 0x71 {
			addr = ((bank - 0x70) << 15) | offset
		} else if bank >= 0x70 && bank <= 0x7D && offset < 0x8000 {
			addr = ((bank - 0x70) << 15) | offset
		} else {
			addr = ((bank & 0x3F) << 13) | (offset & 0x1FFF)
		}
	case HiROM, ExHiROM:
		addr = ((bank & 0x1F) << 13) | (offset & 0x1FFF)
	default:
		addr = offset
	}
	return int(addr % uint32(c.RAMSize))
}

func (c *Cartridge) sa1BWRAMAddress(bank, offset uint32) uint32 {
	if bank >= 0x40 && bank <= 0x4F {
		return ((bank - 0x40) << 16) | offset
	}
	page := uint32(0)
	if d, ok := c.coprocessor.(*sa1.Device); ok {
		page = uint32(d.CPUBWRAMPage())
	}
	return page<<13 | (offset & 0x1FFF)
}

func (c *Cartridge) romAddress(addr uint32) (int, bool) {
	if len(c.ROM) == 0 {
		return 0, false
	}
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF

	var pcAddr int
	if c.CoprocessorID == "sa1" {
		if d, ok := c.coprocessor.(*sa1.Device); ok {
			if sa1Addr, ok := d.CPUROMAddress(addr); ok {
				pcAddr = int(sa1Addr)
				pcAddr %= len(c.ROM)
				if pcAddr < 0 {
					pcAddr += len(c.ROM)
				}
				return pcAddr, true
			}
		}
	}
	switch c.Mode {
	case LoROM:
		if offset < 0x8000 {
			if c.CoprocessorID == "gsu" && offset < 0x6000 && (bank <= 0x3F || (bank >= 0x80 && bank <= 0xBF)) {
				pcAddr = ((int(bank) & 0x3F) << 15) | (int(offset) & 0x7FFF)
				break
			}
			if !((bank >= 0x40 && bank <= 0x7D) || (bank >= 0xC0 && bank <= 0xFF)) {
				return 0, false
			}
			pcAddr = ((int(bank) & 0x3F) << 16) | int(offset)
		} else {
			pcAddr = ((int(bank) & 0x7F) << 15) | (int(offset) & 0x7FFF)
		}
	case ExLoROM:
		if offset < 0x8000 {
			if !((bank >= 0x40 && bank <= 0x7D) || (bank >= 0xC0 && bank <= 0xFF)) {
				return 0, false
			}
			pcAddr = ((int(bank) & 0x3F) << 16) | int(offset)
		} else {
			pcAddr = ((int(bank) & 0x7F) << 15) | (int(offset) & 0x7FFF)
		}
	case HiROM:
		if offset < 0x8000 {
			if !((bank >= 0x40 && bank <= 0x7D) || (bank >= 0xC0 && bank <= 0xFF)) {
				return 0, false
			}
		}
		pcAddr = ((int(bank) & 0x3F) << 16) | int(offset)
	case ExHiROM:
		if offset < 0x8000 {
			if !((bank >= 0x40 && bank <= 0x7D) || (bank >= 0xC0 && bank <= 0xFF)) {
				return 0, false
			}
		}
		secondHalf := bank < 0x80
		pcAddr = ((int(bank) & 0x3F) << 16) | int(offset)
		if secondHalf {
			pcAddr += 0x400000
		}
	default:
		return 0, false
	}

	return mirrorAddress(pcAddr, len(c.ROM)), true
}

// ROMAddress returns the physical ROM offset addressed by a CPU bus read.
// It reports false for cartridge RAM, SA-1 vector overrides, and unmapped
// cartridge addresses.
func (c *Cartridge) ROMAddress(addr uint32) (uint32, bool) {
	if _, ok := c.sa1VectorOverride(addr); ok {
		return 0, false
	}
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if c.isSRAMAddress(bank, offset) {
		return 0, false
	}
	pc, ok := c.romAddress(addr)
	if !ok {
		return 0, false
	}
	return uint32(pc), true
}

func mirrorAddress(addr, size int) int {
	if size <= 0 {
		return 0
	}
	base := 0
	mask := 1 << 23
	for addr >= size {
		for addr&mask == 0 {
			mask >>= 1
		}
		addr -= mask
		if size > mask {
			size -= mask
			base += mask
		}
		mask >>= 1
	}
	return base + addr
}

func (c *Cartridge) Read(addr uint32) uint8 {
	// Fast path: plain LoROM and HiROM carts map $8000-$FFFF of every bank
	// to ROM with no SRAM, vector override or coprocessor in the way.
	if c.coprocessor == nil && c.CoprocessorID == "" && addr&0x8000 != 0 {
		pc := -1
		switch c.Mode {
		case LoROM:
			pc = int(addr>>16&0x7F)<<15 | int(addr&0x7FFF)
		case HiROM:
			pc = int(addr>>16&0x3F)<<16 | int(addr&0xFFFF)
		}
		if pc >= 0 && pc < len(c.ROM) {
			return c.ROM[pc]
		}
	}
	if c.coprocessor != nil {
		if v, ok := c.coprocessor.Read(addr); ok {
			c.stepGSURegisterAccess()
			c.pollGSUIRQ()
			c.pollSA1IRQ()
			return v
		}
	}
	if v, ok := c.sa1VectorOverride(addr); ok {
		return v
	}
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if c.isSRAMAddress(bank, offset) {
		// Match ares/bsnes superfx/bus.cpp CPURAM::read: while the GSU is
		// running and owns the RAM bus, the CPU sees open-bus, but the
		// access does not stall.
		if d, ok := c.coprocessor.(*gsu.Device); ok && d.OwnsRAM() {
			return 0
		}
		// SA-1 type-1 character-conversion DMA lazily synthesizes
		// tile data into I-RAM on the S-CPU's first BW-RAM read of
		// each character window. Per bsnes/sfc/coprocessor/sa1/
		// bwram.cpp:24-31, the address is translated through the
		// SA-1 BMAPS register (already done by sa1BWRAMAddress) and
		// then dispatched to dmaCC1Read while bwram.dma is set.
		if d, ok := c.coprocessor.(*sa1.Device); ok && d.BWRAMDMAActive() {
			return d.DMACC1Read(c.sa1BWRAMAddress(bank, offset), c.RAM)
		}
		// OBC-1 carts route SRAM reads through the indirection
		// device so the $7FF0-$7FF6 register window unpacks the
		// stored OAM-format data per bsnes/sfc/coprocessor/obc1/
		// obc1.cpp:18-30. Outside the register window the device
		// returns the literal SRAM byte.
		if c.obc1 != nil {
			return c.obc1.Read(offset)
		}
		return c.RAM[c.sramAddress(bank, offset)]
	}
	if c.arbitrateGSUROM(addr) {
		return gsuCPUROMVector(addr)
	}
	pcAddr, ok := c.romAddress(addr)
	if !ok {
		return 0
	}
	return c.ROM[pcAddr]
}

func (c *Cartridge) Write(addr uint32, val uint8) {
	if c.coprocessor != nil {
		// Per bsnes/sfc/cpu/timing.cpp:81-84 + superfx/io.cpp:75-82,
		// the CPU synchronizes the coprocessor before the IO write
		// applies. A $3030 write that clears SFR.G must therefore
		// retire any GSU work already in flight at the moment of
		// the CPU access; stepping after the mutation would short-
		// circuit on !Running() and silently drop the in-flight
		// instruction. Probe the coprocessor first so the pre-step
		// only fires for register-window hits.
		if c.gsuRegisterAccess(addr) {
			c.stepGSURegisterAccess()
		}
		if c.coprocessor.Write(addr, val) {
			c.pollGSUIRQ()
			c.pollSA1IRQ()
			return
		}
	}
	bank := (addr >> 16) & 0xFF
	offset := addr & 0xFFFF
	if !c.isSRAMAddress(bank, offset) {
		c.arbitrateGSUROM(addr)
		return
	}
	// Match ares/bsnes superfx/bus.cpp CPURAM::write: CPU writes to the
	// shared RAM window are unconditional, even while the GSU is running
	// and owns the RAM bus. This is the producer/consumer path that lets
	// the CPU wake the GSU mid-execution.
	if c.CoprocessorID == "sa1" {
		if d, ok := c.coprocessor.(*sa1.Device); ok && !d.AllowCPUBWRAMWrite(c.sa1BWRAMAddress(bank, offset)) {
			return
		}
	}
	// OBC-1 carts route SRAM writes through the indirection device
	// so the $7FF0-$7FF6 register window packs OAM-format data per
	// bsnes/sfc/coprocessor/obc1/obc1.cpp:32-60. The device writes
	// into the shared SRAM slice; outside the register window it
	// stores the literal byte.
	if c.obc1 != nil {
		c.obc1.Write(offset, val)
		return
	}
	c.RAM[c.sramAddress(bank, offset)] = val
}

func (c *Cartridge) BlockRead(addr uint32, length int) []byte {
	if length <= 0 {
		return nil
	}
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = c.Read(addr + uint32(i))
	}
	return buf
}

func (c *Cartridge) mapLoROM(b *bus.Bus) {
	if c.CoprocessorID == "gsu" {
		for bank := uint32(0); bank <= 0x3F; bank++ {
			b.Map(bank<<16, (bank<<16)|0x5FFF, c)
			b.Map(((bank | 0x80) << 16), ((bank|0x80)<<16)|0x5FFF, c)
		}
		for bank := uint32(0); bank <= 0x3F; bank++ {
			b.Map((bank<<16)|0x3000, (bank<<16)|0x32FF, c)
			b.Map(((bank|0x80)<<16)|0x3000, ((bank|0x80)<<16)|0x32FF, c)
		}
	}
	if c.CoprocessorID == "srtc" {
		// Sharp RTC: $2800 read / $2801 write in banks $00-$3F and
		// $80-$BF mirrors per snes9x ppu.cpp:1218 + bsnes XML manifest.
		for bank := uint32(0); bank <= 0x3F; bank++ {
			b.Map((bank<<16)|0x2800, (bank<<16)|0x2801, c)
			b.Map(((bank|0x80)<<16)|0x2800, ((bank|0x80)<<16)|0x2801, c)
		}
	}
	if c.CoprocessorID == "sa1" {
		for bank := uint32(0); bank <= 0x3F; bank++ {
			b.Map((bank<<16)|0x2200, (bank<<16)|0x23FF, c)
			b.Map(((bank|0x80)<<16)|0x2200, ((bank|0x80)<<16)|0x23FF, c)
			// I-RAM S-CPU window per
			// bsnes/sfc/coprocessor/sa1/iram.cpp:1-6 +
			// cartridge/load.cpp:327.
			b.Map((bank<<16)|0x3000, (bank<<16)|0x37FF, c)
			b.Map(((bank|0x80)<<16)|0x3000, ((bank|0x80)<<16)|0x37FF, c)
			b.Map((bank<<16)|0x6000, (bank<<16)|0x7FFF, c)
			b.Map(((bank|0x80)<<16)|0x6000, ((bank|0x80)<<16)|0x7FFF, c)
		}
		for bank := uint32(0x40); bank <= 0x4F; bank++ {
			b.Map(bank<<16, (bank<<16)|0xFFFF, c)
		}
	}
	// Banks 00-7D and 80-FF upper half.
	for bank := uint32(0); bank <= 0x7D; bank++ {
		b.Map((bank<<16)|0x8000, (bank<<16)|0xFFFF, c)
	}
	for bank := uint32(0x80); bank <= 0xFF; bank++ {
		b.Map((bank<<16)|0x8000, (bank<<16)|0xFFFF, c)
	}
	// Banks 40-7D and C0-FF lower half linear mirror.
	for bank := uint32(0x40); bank <= 0x7D; bank++ {
		b.Map(bank<<16, (bank<<16)|0x7FFF, c)
	}
	for bank := uint32(0xC0); bank <= 0xFF; bank++ {
		b.Map(bank<<16, (bank<<16)|0x7FFF, c)
	}
	// SRAM windows.
	for bank := uint32(0x70); bank <= 0x7D; bank++ {
		b.Map(bank<<16, (bank<<16)|0x7FFF, c)
	}
	// GSU shared RAM upper halves of $70-$71 (ares/bsnes superfx/bus.cpp).
	if c.CoprocessorID == "gsu" {
		for bank := uint32(0x70); bank <= 0x71; bank++ {
			b.Map((bank<<16)|0x8000, (bank<<16)|0xFFFF, c)
		}
	}
	for bank := uint32(0); bank <= 0x3F; bank++ {
		b.Map((bank<<16)|0x6000, (bank<<16)|0x7FFF, c)
		b.Map(((bank|0x80)<<16)|0x6000, ((bank|0x80)<<16)|0x7FFF, c)
	}
}

func (c *Cartridge) mapHiROM(b *bus.Bus) {
	// Banks 00-3F and 80-BF upper half.
	for bank := uint32(0); bank <= 0x3F; bank++ {
		b.Map((bank<<16)|0x8000, (bank<<16)|0xFFFF, c)
		b.Map(((bank|0x80)<<16)|0x8000, ((bank|0x80)<<16)|0xFFFF, c)
	}
	// Banks 40-7D and C0-FF full range.
	for bank := uint32(0x40); bank <= 0x7D; bank++ {
		b.Map(bank<<16, (bank<<16)|0xFFFF, c)
	}
	for bank := uint32(0xC0); bank <= 0xFF; bank++ {
		b.Map(bank<<16, (bank<<16)|0xFFFF, c)
	}
	// SRAM windows.
	for bank := uint32(0x20); bank <= 0x3F; bank++ {
		b.Map((bank<<16)|0x6000, (bank<<16)|0x7FFF, c)
		b.Map(((bank|0x80)<<16)|0x6000, ((bank|0x80)<<16)|0x7FFF, c)
	}
	// DSP-1 HiROM window at banks 00-1F / 80-9F, offsets 6000-6BFF.
	// Overlaps what would otherwise be open bus; the updsp coprocessor
	// claims those addresses internally via its Mapper.mapped() check.
	if c.CoprocessorID == "updsp" {
		for bank := uint32(0); bank <= 0x1F; bank++ {
			b.Map((bank<<16)|0x6000, (bank<<16)|0x6BFF, c)
			b.Map(((bank|0x80)<<16)|0x6000, ((bank|0x80)<<16)|0x6BFF, c)
		}
	}
}

// MapToBus maps the cartridge to the provided bus.
func (c *Cartridge) MapToBus(b *bus.Bus) {
	switch c.Mode {
	case HiROM:
		c.mapHiROM(b)
	case ExHiROM:
		c.mapHiROM(b)
	case ExLoROM:
		c.mapLoROM(b)
	default:
		c.mapLoROM(b)
	}
}

// AttachUPDSP installs a uPD77C25 (DSP-1/2/3/4) coprocessor at the
// address window implied by the cartridge mapping mode. The caller is
// responsible for loading the DSP program and data ROMs into the
// Loader's Core before calling; passing a nil loader yields a
// passthrough mapper that claims the window but returns idle values,
// matching the behaviour of a cartridge whose DSP ROM image is missing.
//
// Returns the installed Mapper so test harnesses can drive SetDSPResult
// and inspect SR transitions.
func (c *Cartridge) AttachUPDSP(loader *updsp.Loader) *updsp.Mapper {
	var mt updsp.MapType
	switch c.Mode {
	case HiROM, ExHiROM:
		mt = updsp.MapHiROM
	default:
		if c.ROMSize > 0x100000 {
			mt = updsp.MapLoROMSmall
		} else {
			mt = updsp.MapLoROM
		}
	}
	var io *updsp.IO
	if loader != nil {
		io = loader.IO
	}
	m := updsp.NewMapper(io, mt)
	m.SetPAL(c.PAL)
	c.coprocessor = m
	c.CoprocessorID = "updsp"
	return m
}

// LoadUPDSP loads caller-supplied DSP program and data ROMs into the
// cartridge's uPD77C25 mapper. If both ROM buffers are nil, LoadUPDSP still
// installs the mapper in passthrough mode so the cartridge claims the DSP
// window with deterministic idle values.
func (c *Cartridge) LoadUPDSP(variant updsp.Variant, progROM, dataROM []byte) (*updsp.Loader, error) {
	loader, err := updsp.Load(variant, progROM, dataROM)
	if err != nil {
		return loader, err
	}
	c.AttachUPDSP(loader)
	return loader, nil
}

// AttachGSU installs a Super FX coprocessor and returns it so the system
// layer can bind board-local hooks such as VRAM commits.
func (c *Cartridge) AttachGSU(w gsu.VRAMWriter) *gsu.Device {
	d, ok := c.coprocessor.(*gsu.Device)
	if !ok {
		d = gsu.New(c.ROM, c.RAM)
	}
	c.attachGSURAM(d)
	d.SetVRAMWriter(w)
	c.coprocessor = d
	c.CoprocessorID = "gsu"
	return d
}

// SetIRQTarget installs the CPU-side IRQ sink used by cartridge hardware.
func (c *Cartridge) SetIRQTarget(t interface{ TriggerIRQ() }) { c.irqTarget = t }

func (c *Cartridge) attachGSURAM(d *gsu.Device) {
	if c.RAM == nil {
		c.RAM = d.RAM
		c.RAMSize = len(d.RAM)
		return
	}
	d.RAM = c.RAM
}

func (c *Cartridge) arbitrateGSUROM(addr uint32) bool {
	d, ok := c.coprocessor.(*gsu.Device)
	if !ok || !d.OwnsROM() {
		return false
	}
	if _, ok := c.romAddress(addr); !ok {
		return false
	}
	d.Step(6)
	c.pollGSUIRQ()
	return d.OwnsROM()
}

func gsuCPUROMVector(addr uint32) uint8 {
	vector := [...]uint8{
		0x00, 0x01, 0x00, 0x01, 0x04, 0x01, 0x00, 0x01,
		0x00, 0x01, 0x08, 0x01, 0x00, 0x01, 0x0c, 0x01,
	}
	return vector[addr&15]
}

func (c *Cartridge) stepGSURegisterAccess() {
	d, ok := c.coprocessor.(*gsu.Device)
	if !ok {
		return
	}
	d.Step(6)
}

// gsuRegisterAccess reports whether addr falls inside the SuperFX
// register/cache window ($3000-$32FF in banks $00-$3F or $80-$BF).
// It mirrors the bank/offset gate at gsu/board.go:24-46 and is used
// by Write to decide whether to synchronize the GSU before the IO
// mutation, matching bsnes/sfc/cpu/timing.cpp:81-84.
func (c *Cartridge) gsuRegisterAccess(addr uint32) bool {
	if _, ok := c.coprocessor.(*gsu.Device); !ok {
		return false
	}
	bank := (addr >> 16) & 0xff
	if !(bank <= 0x3f || (bank >= 0x80 && bank <= 0xbf)) {
		return false
	}
	off := addr & 0xffff
	return off >= 0x3000 && off <= 0x32ff
}

// Step advances cartridge hardware. Plain ROM boards have no timed logic.
func (c *Cartridge) Step(masterCycles uint64) {
	if c.coprocessor != nil {
		c.coprocessor.Step(masterCycles)
		c.pollGSUIRQ()
		c.pollSA1IRQ()
	}
}

func (c *Cartridge) pollGSUIRQ() {
	d, ok := c.coprocessor.(*gsu.Device)
	if !ok {
		c.gsuIRQLine = false
		return
	}
	irq := d.SFR&gsu.SFRIRQ != 0 && d.CFGR&0x80 == 0
	if !irq {
		c.gsuIRQLine = false
		return
	}
	if c.gsuIRQLine {
		return
	}
	c.gsuIRQLine = true
	if c.irqTarget != nil {
		c.irqTarget.TriggerIRQ()
	}
}

func (c *Cartridge) pollSA1IRQ() {
	d, ok := c.coprocessor.(*sa1.Device)
	if !ok {
		c.sa1IRQLine = false
		return
	}
	if !d.CPUIRQPending() {
		c.sa1IRQLine = false
		return
	}
	if c.sa1IRQLine {
		return
	}
	c.sa1IRQLine = true
	if c.irqTarget != nil {
		c.irqTarget.TriggerIRQ()
	}
}

// SaveRAM returns a copy of the persistent RAM.
func (c *Cartridge) SaveRAM() []byte {
	return append([]byte(nil), c.RAM...)
}

// LoadSaveRAM restores the persistent RAM contents.
func (c *Cartridge) LoadSaveRAM(data []byte) error {
	if len(c.RAM) == 0 {
		if len(data) == 0 {
			return nil
		}
		// Keep a best-effort SRAM buffer for ROMs with missing/incorrect header size.
		c.RAM = make([]byte, len(data))
		c.RAMSize = len(data)
		copy(c.RAM, data)
		return nil
	}
	if len(data) > len(c.RAM) {
		return fmt.Errorf("save ram too large: got %d bytes, want at most %d", len(data), len(c.RAM))
	}
	for i := range c.RAM {
		c.RAM[i] = 0
	}
	copy(c.RAM, data)
	return nil
}

type state struct {
	Mode          MappingMode
	RAM           []byte
	CoprocessorID string
	Coprocessor   []byte
	GSUIRQLine    bool
	SA1IRQLine    bool
}

// Serialize returns the cartridge board state.
func (c *Cartridge) Serialize() ([]byte, error) {
	var coprocessorData []byte
	if c.coprocessor != nil {
		data, err := c.coprocessor.Serialize()
		if err != nil {
			return nil, fmt.Errorf("serialize cartridge coprocessor: %w", err)
		}
		coprocessorData = data
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{
		Mode:          c.Mode,
		RAM:           c.SaveRAM(),
		CoprocessorID: c.CoprocessorID,
		Coprocessor:   coprocessorData,
		GSUIRQLine:    c.gsuIRQLine,
		SA1IRQLine:    c.sa1IRQLine,
	}); err != nil {
		return nil, fmt.Errorf("serialize cartridge: %w", err)
	}
	return buf.Bytes(), nil
}

// ValidateState checks that data can restore this cartridge without changing it.
func (c *Cartridge) ValidateState(data []byte) error {
	_, err := c.decodeState(data)
	return err
}

func (c *Cartridge) decodeState(data []byte) (state, error) {
	var s state
	if len(data) > 32<<20 {
		return s, fmt.Errorf("unserialize cartridge: state too large")
	}
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return s, fmt.Errorf("unserialize cartridge: %w", err)
	}
	if s.Mode != c.Mode || s.CoprocessorID != c.CoprocessorID {
		return s, fmt.Errorf("unserialize cartridge: board does not match loaded cartridge")
	}
	if len(s.RAM) != len(c.RAM) {
		return s, fmt.Errorf("unserialize cartridge: ram size %d, want %d", len(s.RAM), len(c.RAM))
	}
	var chip Coprocessor
	switch s.CoprocessorID {
	case "dsp1":
		chip = dsp1.New()
	case "updsp":
		m := updsp.NewMapper(nil, updsp.MapLoROM)
		if current, ok := c.coprocessor.(*updsp.Mapper); ok && current.IO != nil {
			io := *current.IO
			if current.IO.Core != nil {
				core := *current.IO.Core
				io.Core = &core
			}
			m.IO = &io
		}
		chip = m
	case "gsu":
		chip = gsu.New(c.ROM, make([]byte, len(c.RAM)))
	case "sa1":
		chip = sa1.New()
	case "cx4":
		chip = cx4.New(c.ROM)
	case "srtc":
		chip = srtc.New(nil)
	default:
		if len(s.Coprocessor) != 0 {
			return s, fmt.Errorf("unserialize cartridge: unexpected coprocessor state")
		}
	}
	if chip != nil {
		if err := chip.Unserialize(s.Coprocessor); err != nil {
			return s, fmt.Errorf("unserialize cartridge coprocessor: %w", err)
		}
	}
	if g, ok := chip.(*gsu.Device); ok && !bytes.Equal(g.RAM, s.RAM) {
		return s, fmt.Errorf("unserialize cartridge: inconsistent gsu ram")
	}
	return s, nil
}

// Unserialize restores the cartridge board state.
func (c *Cartridge) Unserialize(data []byte) error {
	state, err := c.decodeState(data)
	if err != nil {
		return err
	}
	c.Mode = state.Mode
	c.CoprocessorID = state.CoprocessorID
	c.gsuIRQLine = state.GSUIRQLine
	c.sa1IRQLine = state.SA1IRQLine
	switch c.CoprocessorID {
	case "dsp1":
		if c.coprocessor == nil {
			c.coprocessor = dsp1.New()
		}
		if err := c.coprocessor.Unserialize(state.Coprocessor); err != nil {
			return fmt.Errorf("unserialize cartridge coprocessor: %w", err)
		}
	case "updsp":
		// Restore relies on the caller having already AttachUPDSP'd a
		// loader with a Core so Unserialize has a target to copy into.
		if c.coprocessor == nil {
			c.coprocessor = updsp.NewMapper(nil, updsp.MapLoROM)
		}
		if err := c.coprocessor.Unserialize(state.Coprocessor); err != nil {
			return fmt.Errorf("unserialize cartridge coprocessor: %w", err)
		}
	case "gsu":
		if c.coprocessor == nil {
			c.coprocessor = gsu.New(c.ROM, c.RAM)
		}
		if err := c.coprocessor.Unserialize(state.Coprocessor); err != nil {
			return fmt.Errorf("unserialize cartridge coprocessor: %w", err)
		}
	case "sa1":
		if c.coprocessor == nil {
			c.coprocessor = sa1.New()
		}
		// Reinstall the VBR reader so $230C/$230D continue to source
		// ROM bytes through this cartridge after a state restore.
		if d, ok := c.coprocessor.(*sa1.Device); ok {
			d.SetROMReader(c.sa1VBRReader)
			d.SetBWRAMSlice(c.RAM)
		}
		if err := c.coprocessor.Unserialize(state.Coprocessor); err != nil {
			return fmt.Errorf("unserialize cartridge coprocessor: %w", err)
		}
	case "srtc":
		if c.coprocessor == nil {
			c.coprocessor = srtc.New(nil)
		}
		if err := c.coprocessor.Unserialize(state.Coprocessor); err != nil {
			return fmt.Errorf("unserialize cartridge coprocessor: %w", err)
		}
	case "cx4":
		if c.coprocessor == nil {
			c.AttachCx4()
		}
		if err := c.coprocessor.Unserialize(state.Coprocessor); err != nil {
			return fmt.Errorf("unserialize cartridge coprocessor: %w", err)
		}
	default:
		c.coprocessor = nil
	}
	if err := c.LoadSaveRAM(state.RAM); err != nil {
		return err
	}
	if c.obc1 != nil {
		c.obc1.SetRAM(c.RAM)
	}
	return nil
}

// AttachCx4 installs the Cx4 HLE coprocessor and returns it for tests.
func (c *Cartridge) AttachCx4() *cx4.Device {
	d, ok := c.coprocessor.(*cx4.Device)
	if !ok {
		d = cx4.New(c.ROM)
	}
	c.coprocessor = d
	c.CoprocessorID = "cx4"
	return d
}

// Power resets attached chip execution without replacing cartridge memory or hooks.
// bsnes System::power invokes chip power for both console power and reset.
func (c *Cartridge) Power() {
	c.gsuIRQLine = false
	if c.obc1 != nil {
		c.obc1.SetRAM(c.RAM)
	}
	switch d := c.coprocessor.(type) {
	case *gsu.Device:
		d.Reset()
	case *cx4.Device:
		d.Power()
	case *srtc.Device:
		d.Power()
	case *updsp.Mapper:
		d.Power()
	}
}
